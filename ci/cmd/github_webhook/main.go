// Application that accepts github webhook events and then queues the
// appropriate work to Temporal for CI.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/ejholmes/hookshot"
	"github.com/ejholmes/hookshot/events"
	"github.com/go-chi/chi/v5"
	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/ingress"
	shared "go.goldmine.build/ci/go"
	"go.goldmine.build/ci/go/triggers/github"
	"go.goldmine.build/go/common"
	"go.goldmine.build/go/httputils"
	"go.goldmine.build/go/profsrv"
	"go.goldmine.build/go/sklog"
)

type ServerFlags struct {
	Port        string
	PromPort    string
	PprofPort   string
	HealthzPort string
	Secret      string
	Main        string

	// Storage info need by PRConvert.
	StorageCredentialsDir string
	StorageEndpoint       string
	StorageUseSSL         bool
	StorageBucketName     string
	StoragePRPath         string
}

// Flagset constructs a flag.FlagSet for the App.
func (s *ServerFlags) Flagset() *flag.FlagSet {
	fs := flag.NewFlagSet("gold-server", flag.ExitOnError)
	fs.StringVar(&s.Port, "port", ":8000", "Main UI address (e.g., ':8000').")
	fs.StringVar(&s.PromPort, "prom_port", ":20000", "Metrics service address (e.g., ':20000').")
	fs.StringVar(&s.PprofPort, "pprof_port", "", "PProf handler (e.g., ':9001'). PProf not enabled if the empty string (default).")
	fs.StringVar(&s.HealthzPort, "healthz_port", ":10000", "The port for health checks.")
	fs.StringVar(&s.Secret, "secret", "", "The file location of the github-webhook-secret.")
	fs.StringVar(&s.Main, "main", "refs/heads/main", "The name of the main branch to follow.")

	fs.StringVar(&s.StorageCredentialsDir, "store_cred_dir", "", "Directory that contains storage credentials in two files, 'key' and 'secret'.")
	fs.StringVar(&s.StorageEndpoint, "store_endpoint", "", "Storage endpoint, such as play.min.io")
	fs.BoolVar(&s.StorageUseSSL, "store_ssl", true, "Use SSL when connecting to --store_endpoint")
	fs.StringVar(&s.StorageBucketName, "store_bucket", "", "Storage bucket name.")
	fs.StringVar(&s.StoragePRPath, "store_pr_path", "", "Storage directory in the bucket that holds the Pull Request info.")

	return fs
}

var (
	flags         ServerFlags
	prConvert     *github.PRConvert
	restateClient *ingress.Client
)

func HandlePing(w http.ResponseWriter, r *http.Request) {
	sklog.Infof("Got ping")
	defer r.Body.Close()

	var ping events.Ping
	err := json.NewDecoder(r.Body).Decode(&ping)
	if err != nil {
		sklog.Errorf("decoding ping: %s", err)
	}
	w.WriteHeader(200)
	w.Write([]byte(`Pong`))
}

func HandlePush(w http.ResponseWriter, r *http.Request) {
	sklog.Infof("Got push")
	w.WriteHeader(200)
	defer r.Body.Close()

	var push events.Push
	err := json.NewDecoder(r.Body).Decode(&push)
	if err != nil {
		sklog.Errorf("decoding push: %s", err)
	}

	if push.Ref != flags.Main {
		sklog.Infof("Ignoring push to non-main branch %q", push.Ref)
		return
	}

	b, err := json.MarshalIndent(push, "", "  ")
	if err != nil {
		sklog.Error(err)
	}
	sklog.Infof("Push: \n%s", string(b))
}

func HandlePullRequest(w http.ResponseWriter, r *http.Request) {
	sklog.Infof("Got push")
	w.WriteHeader(200)
	var pull events.PullRequest
	err := json.NewDecoder(r.Body).Decode(&pull)
	if err != nil {
		sklog.Errorf("decoding pull request: %s", err)
	}

	// Now trigger the Temporal workflow by passing in the prNumber,
	// patchsetNumber, login, and sha. Well do the checking in the Workflow for
	// login being a valid CI user, so that way errors here will be visible, as
	// opposed to silently swallowing them.
	wf, err := prConvert.WorkflowArgsFromPullRequest(context.Background(), github.Patchset{
		PRNumber: pull.Number,
		Login:    pull.PullRequest.User.Login,
		SHA:      pull.PullRequest.Head.Sha,
	})
	if err != nil {
		sklog.Errorf("Failed to create/update pull request: %s", err)
	}

	// Log the struct we are going to send to restate.
	sklog.Infof("Workflow: %#v", wf)
	sklog.Infof("Client: %v", restateClient)

	invocation, err := ingress.ServiceSend[*shared.TrybotWorkflowArgs](
		restateClient, "CI", "RunAllBuildsAndTestsV1").
		Send(context.Background(), wf,
			restate.WithIdempotencyKey(fmt.Sprintf("PR-%d-%d-%s", wf.PRNumber, wf.PatchsetNumber, wf.SHA)))

	if err != nil {
		sklog.Errorf("Failed to send request to restate: %s", err)
	}

	fmt.Println("ServiceSend invocation ID:", invocation.Id())

	// Log the pull request.
	b, err := json.MarshalIndent(pull, "", "  ")
	if err != nil {
		sklog.Error(err)
	}
	sklog.Infof("Pull: \n%s", string(b))
}

func main() {
	// Command line flags.
	common.InitWithMust(
		"github-triggers",
		common.PrometheusOpt(&flags.PromPort),
		common.FlagSetOpt((&flags).Flagset()),
	)

	var err error
	prConvert, err = github.New(
		flags.StorageCredentialsDir,
		flags.StorageEndpoint,
		flags.StorageUseSSL,
		flags.StoragePRPath,
		flags.StorageBucketName,
	)
	if err != nil {
		sklog.Fatalf("Creating PRConvert: %s", err)
	}

	restateClient = ingress.NewClient("http://restate-requests:8080")

	// Start pprof services.
	profsrv.Start(flags.PprofPort)

	httputils.StartHealthzServer(flags.HealthzPort)

	// Load the GitHub webhook secret.
	b, err := os.ReadFile(flags.Secret)
	if err != nil {
		sklog.Fatalf("Failed to open secret file %q: %s", flags.Secret, err)
	}

	hookRouter := hookshot.NewRouter()
	hookRouter.Handle("ping", hookshot.Authorize(http.HandlerFunc(HandlePing), string(b)))
	hookRouter.Handle("push", hookshot.Authorize(http.HandlerFunc(HandlePush), string(b)))
	hookRouter.Handle("pull_request", hookshot.Authorize(http.HandlerFunc(HandlePullRequest), string(b)))

	chiRouter := chi.NewRouter()
	chiRouter.Handle("/webhook", hookRouter)

	sklog.Info("Ready to serve.")
	sklog.Fatal(http.ListenAndServe(flags.Port, httputils.LoggingGzipRequestResponse(chiRouter)))
}

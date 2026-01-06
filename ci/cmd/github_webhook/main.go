// Application that accepts github webhook events and then queues the
// appropriate work to Temporal for CI.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/ejholmes/hookshot"
	"github.com/ejholmes/hookshot/events"
	"github.com/go-chi/chi/v5"
	shared "go.goldmine.build/ci/go"
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

	return fs
}

var (
	flags ServerFlags
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

	wf := shared.TrybotWorkflowArgs{
		PRNumber: pull.Number,
		Login:    pull.PullRequest.User.Login,
		SHA:      pull.PullRequest.Head.Sha,
	}
	if err != nil {
		sklog.Errorf("Failed to create/update pull request: %s", err)
	}

	// Log the struct we are going to send to restate.
	sklog.Infof("Workflow: %#v", wf)

	idempotencyKey := fmt.Sprintf("PR-%d-%s", wf.PRNumber, wf.SHA)

	requestURL := "http://restate-requests:8080/CI/RunAllBuildsAndTestsV1/send"

	b, err := json.MarshalIndent(wf, "", "  ")
	if err != nil {
		sklog.Errorf("Failed to encode request body: %s", err)
		return
	}
	sklog.Infof("Body: \n%s", string(b))
	sklog.Infof("Idempotency: %s", idempotencyKey)
	body := bytes.NewBuffer(b)

	client := httputils.DefaultClientConfig().With2xxOnly().Client()
	req, err := http.NewRequest("POST", requestURL, body)
	if err != nil {
		sklog.Errorf("Failed to build request object: %s", err)
		return
	}
	req.Header.Add("idempotency-key", idempotencyKey)
	req.Header.Add("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		sklog.Errorf("Failed to make request: %s", err)
		return
	}
	sklog.Infof("Status: %q", resp.Status)

	/*
	   curl --include --request POST \
	     --url http://127.0.0.1:8080/CI/RunAllBuildsAndTestsV1/send \
	     --header 'Accept: application/json' \
	     --header 'Content-Type: application/json' \
	     --header 'idempotency-key: ' \
	     --data '{  "login": "jcgregorio",  "patchset": 13,  "pr": 7, "sha": "01482eb651c1881437dc8f9e928677222943e1dc" }'
	*/

	if err != nil {
		sklog.Errorf("Failed to send request to restate: %s", err)
	}
}

func main() {
	// Command line flags.
	common.InitWithMust(
		"github-triggers",
		common.PrometheusOpt(&flags.PromPort),
		common.FlagSetOpt((&flags).Flagset()),
	)

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

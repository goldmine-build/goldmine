// Application that accepts github webhook events and then queues the
// appropriate work to temporal for CI.
package main

import (
	"encoding/json"
	"flag"
	"net/http"
	"os"

	"github.com/ejholmes/hookshot"
	"github.com/ejholmes/hookshot/events"
	"github.com/go-chi/chi/v5"
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
	// TODO Need a list of approved accounts that can run CI workloads.
	// TODO Need the ref to track for main, e.g. "refs/heads/main".
}

// Flagset constructs a flag.FlagSet for the App.
func (s *ServerFlags) Flagset() *flag.FlagSet {
	fs := flag.NewFlagSet("gold-server", flag.ExitOnError)
	fs.StringVar(&s.Port, "port", ":8000", "Main UI address (e.g., ':8000')")
	fs.StringVar(&s.PromPort, "prom_port", ":20000", "Metrics service address (e.g., ':20000')")
	fs.StringVar(&s.PprofPort, "pprof_port", "", "PProf handler (e.g., ':9001'). PProf not enabled if the empty string (default).")
	fs.StringVar(&s.HealthzPort, "healthz_port", ":10000", "The port for health checks.")
	fs.StringVar(&s.Secret, "secret", "", "The file location of the github-webhook-secret")

	return fs
}

var flags ServerFlags

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
		sklog.Errorf("decoding ping: %s", err)
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
		sklog.Errorf("decoding ping: %s", err)
	}
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

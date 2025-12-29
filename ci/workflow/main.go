// This will be the main workflow for the goldmine CI.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"slices"
	"time"

	shared "go.goldmine.build/ci/go"
	"go.goldmine.build/go/common"
	"go.goldmine.build/go/git"
	"go.goldmine.build/go/git/provider/providers/gitapi"
	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/sklog"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

type ServerFlags struct {
	Port        string
	PromPort    string
	PprofPort   string
	HealthzPort string
	CheckoutDir string

	PatPath string
	Owner   string
	Repo    string
	Branch  string

	TemporalDomain string
}

// Flagset constructs a flag.FlagSet for the App.
func (s *ServerFlags) Flagset() *flag.FlagSet {
	fs := flag.NewFlagSet("gold-server", flag.ExitOnError)
	fs.StringVar(&s.Port, "port", ":8000", "Main UI address (e.g., ':8000').")
	fs.StringVar(&s.PromPort, "prom_port", ":20000", "Metrics service address (e.g., ':20000').")
	fs.StringVar(&s.PprofPort, "pprof_port", "", "PProf handler (e.g., ':9001'). PProf not enabled if the empty string (default).")
	fs.StringVar(&s.HealthzPort, "healthz_port", ":10000", "The port for health checks.")
	fs.StringVar(&s.CheckoutDir, "checkout_dir", "", "The file location of the git checkout.")

	fs.StringVar(&s.PatPath, "pat_path", "", "The file location of the git auth token in a file.")
	fs.StringVar(&s.Owner, "owner", "goldmine-build", "GitHub user or organization.")
	fs.StringVar(&s.Repo, "repo", "goldmine", "GitHub repo.")
	fs.StringVar(&s.Branch, "branch", "main", "GitHub repo branch.")
	fs.StringVar(&s.TemporalDomain, "domain", "https://temporal.tail433733.ts.net", "Temporal UI domain name.")

	return fs
}

var flags ServerFlags

var approvedCIUsers = []string{"jcgregorio"}
var gitApi *gitapi.GitApi = nil
var temporalDomain *url.URL

func main() {
	// Command line flags.
	common.InitWithMust(
		"github-ci-workflow",
		common.PrometheusOpt(&flags.PromPort),
		common.FlagSetOpt((&flags).Flagset()),
	)

	c, err := client.Dial(client.Options{})
	if err != nil {
		sklog.Fatalf("Unable to create Temporal client: %s", err)
	}
	defer c.Close()

	gitApi, err = gitapi.New(context.Background(), flags.PatPath, flags.Owner, flags.Repo, flags.Branch)
	if err != nil {
		sklog.Fatalf("Unable to create GitHub API: %s", err)
	}

	temporalDomain, err = url.Parse(flags.TemporalDomain)
	if err != nil {
		sklog.Fatalf("Failed to parse Temporal Domain %q: %s", flags.TemporalDomain, err)
	}

	w := worker.New(c, shared.GitHubGoldMineCIQueue, worker.Options{})

	// This worker hosts both Workflow and Activity functions.
	w.RegisterWorkflow(GoldmineCI)

	// And set the status, url, description, and context.
	w.RegisterActivity(CheckoutCode)
	w.RegisterActivity(RunTests)
	w.RegisterActivity(UploadGoldResults)

	// Start listening to the Task Queue.
	err = w.Run(worker.InterruptCh())
	if err != nil {
		sklog.Fatalf("Unable to start Worker: %s", err)
	}

}

func GoldmineCI(ctx workflow.Context, input shared.TrybotWorkflowArgs) (string, error) {
	if !slices.Contains(approvedCIUsers, input.Login) {
		return "", temporal.NewApplicationError(input.Login+" is not approved CI user.", "auth failure")
	}
	// RetryPolicy specifies how to automatically handle retries if an Activity fails.
	retrypolicy := &temporal.RetryPolicy{
		InitialInterval:        time.Second,
		BackoffCoefficient:     2.0,
		MaximumAttempts:        1,
		NonRetryableErrorTypes: []string{},
	}

	options := workflow.ActivityOptions{
		// Timeout options specify when to automatically timeout Activity functions.
		StartToCloseTimeout: 30 * time.Minute,
		// Optionally provide a customized RetryPolicy.
		// Temporal retries failed Activities by default.
		RetryPolicy: retrypolicy,
	}

	// Apply the options.
	ctx = workflow.WithActivityOptions(ctx, options)

	// TODO Is there a way to start bazel and keep it running?

	err := workflow.ExecuteActivity(ctx, CheckoutCode, input).Get(ctx, nil)
	if err != nil {
		return "", err
	}

	// TODO Spin up emulators.

	err = workflow.ExecuteActivity(ctx, RunTests, input).Get(ctx, nil)
	if err != nil {
		return "", err
	}

	// TODO Spin down emulators. Note we spin up and down because there might be
	// new emulators added or updated.

	err = workflow.ExecuteActivity(ctx, UploadGoldResults, input).Get(ctx, nil)
	if err != nil {
		return "", err
	}
	return "CI run complete", nil
}

type Context string

const (
	Infra Context = "Infra"
	CI    Context = "CI"
)

func infraError(ctx context.Context, input shared.TrybotWorkflowArgs, err error, format string, args ...interface{}) error {
	return _error(ctx, Infra, input, err, format, args...)
}

func buildError(ctx context.Context, input shared.TrybotWorkflowArgs, err error, format string, args ...interface{}) error {
	return _error(ctx, CI, input, err, format, args...)
}

func _error(ctx context.Context, context Context, input shared.TrybotWorkflowArgs, err error, format string, args ...interface{}) error {
	fullErrorMsg := fmt.Sprintf("%s:%s", fmt.Sprintf(format, args...), err)
	sklog.Error(fullErrorMsg)
	info := activity.GetInfo(ctx)
	workflowID := info.WorkflowExecution.ID
	runID := info.WorkflowExecution.RunID
	ns := info.WorkflowNamespace

	// Construct URL from workflow ID?
	// https://your-ui-host/namespaces/{namespace}/workflows/{workflowId}/{runId}/history

	u := url.URL{
		Scheme: temporalDomain.Scheme,
		Host:   temporalDomain.Host,
		Path:   path.Join("namespaces", ns, "workflows", workflowID, runID, "history"),
	}

	err = gitApi.SetStatus(ctx, input.SHA, gitapi.Error, u.String(), fullErrorMsg, string(context))
	if err != nil {
		sklog.Errorf("Failed to set GitHub status: %s", err)
	}
	return temporal.NewApplicationError(fullErrorMsg, "app error")
}

func CheckoutCode(ctx context.Context, input shared.TrybotWorkflowArgs) error {
	checkout, err := git.NewCheckout(ctx, "https://github.com/goldmine-build/goldmine.git", flags.CheckoutDir)
	if err != nil {
		return infraError(ctx, input, err, "Failed checkout")
	}

	refs := fmt.Sprintf("refs/pull/%d/head", input.PRNumber)
	_, err = checkout.Git(ctx, "fetch", "origin", refs)
	if err != nil {
		return infraError(ctx, input, err, "Failed to pull ref: %s", refs)
	}

	_, err = checkout.Git(ctx, "checkout", "FETCH_HEAD")
	if err != nil {
		return infraError(ctx, input, err, "Failed to checkout FETCH_HEAD")
	}

	return nil
}

func RunTests(ctx context.Context, input shared.TrybotWorkflowArgs) error {
	bazel, err := exec.LookPath("bazelisk")
	if err != nil {
		return skerr.Wrap(err)
	}
	cmd := exec.CommandContext(ctx, bazel, "test", "//golden/modules/...", "//perf/modules/...", "//go/...")
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "PWD="+flags.CheckoutDir)

	// TODO Use streaming output and pull the BuildBuddy URL from the output and write that back to the GitHub PR.
	// The line looks like:
	//
	//     INFO: Streaming build results to: https://app.buildbuddy.io/invocation/some-uuid-here
	_, err = cmd.CombinedOutput()
	if err != nil {
		// TODO, pass in the buildbuddy URL to buildError handler.
		return buildError(ctx, input, err, "Failed to build")
	}

	return nil
}

func UploadGoldResults(ctx context.Context, input shared.TrybotWorkflowArgs) error {
	// TBD
	return nil
}

// This will be the main workflow for the goldmine CI.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"time"

	shared "go.goldmine.build/ci/go"
	"go.goldmine.build/go/common"
	"go.goldmine.build/go/git"
	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/sklog"
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
}

// Flagset constructs a flag.FlagSet for the App.
func (s *ServerFlags) Flagset() *flag.FlagSet {
	fs := flag.NewFlagSet("gold-server", flag.ExitOnError)
	fs.StringVar(&s.Port, "port", ":8000", "Main UI address (e.g., ':8000').")
	fs.StringVar(&s.PromPort, "prom_port", ":20000", "Metrics service address (e.g., ':20000').")
	fs.StringVar(&s.PprofPort, "pprof_port", "", "PProf handler (e.g., ':9001'). PProf not enabled if the empty string (default).")
	fs.StringVar(&s.HealthzPort, "healthz_port", ":10000", "The port for health checks.")
	fs.StringVar(&s.CheckoutDir, "checkout_dir", "", "The file location of the git checkout.")

	return fs
}

var flags ServerFlags

var approvedCIUsers = []string{"jcgregorio"}

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

	w := worker.New(c, shared.GitHubGoldMineCIQueue, worker.Options{})

	// This worker hosts both Workflow and Activity functions.
	w.RegisterWorkflow(GoldmineCI)
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
		MaximumAttempts:        2,
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

	err := workflow.ExecuteActivity(ctx, CheckoutCode, input).Get(ctx, nil)
	if err != nil {
		return "", err
	}

	err = workflow.ExecuteActivity(ctx, RunTests, input).Get(ctx, nil)
	if err != nil {
		return "", err
	}

	err = workflow.ExecuteActivity(ctx, UploadGoldResults, input).Get(ctx, nil)
	if err != nil {
		return "", err
	}
	return "CI run complete", nil
}

func appError(err error, format string, args ...interface{}) error {
	fullErrorMsg := fmt.Sprintf("%s:%s", fmt.Sprintf(format, args...), err)
	sklog.Error(fullErrorMsg)
	return temporal.NewApplicationError(fullErrorMsg, "app error")
}

func CheckoutCode(ctx context.Context, input shared.TrybotWorkflowArgs) error {
	checkout, err := git.NewCheckout(ctx, "https://github.com/goldmine-build/goldmine.git", flags.CheckoutDir)
	if err != nil {
		return appError(err, "Failed checkout")
	}

	refs := fmt.Sprintf("refs/pull/%d/head", input.PRNumber)
	_, err = checkout.Git(ctx, "fetch", "origin", refs)
	if err != nil {
		return appError(err, "Failed to pull ref: %s", refs)
	}

	_, err = checkout.Git(ctx, "checkout", "FETCH_HEAD")
	if err != nil {
		return appError(err, "Failed to checkout FETCH_HEAD")
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
	b, err := cmd.CombinedOutput()
	if err != nil {
		return appError(err, string(b))
	}

	return nil
}

func UploadGoldResults(ctx context.Context, input shared.TrybotWorkflowArgs) error {
	// TBD
	return nil
}

// This will be the main workflow for the goldmine CI.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	shared "go.goldmine.build/ci/go"
	"go.goldmine.build/go/common"
	"go.goldmine.build/go/git"
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

func main() {
	// Command line flags.
	common.InitWithMust(
		"github-ci-workflow",
		common.PrometheusOpt(&flags.PromPort),
		common.FlagSetOpt((&flags).Flagset()),
	)

	c, err := client.Dial(client.Options{})
	if err != nil {
		log.Fatalln("Unable to create Temporal client.", err)
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
		log.Fatalln("unable to start Worker", err)
	}

}

func GoldmineCI(ctx workflow.Context, input shared.TrybotWorkflowArgs) (string, error) {
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

func CheckoutCode(ctx context.Context, input shared.TrybotWorkflowArgs) (string, error) {
	checkout, err := git.NewCheckout(ctx, "https://github.com/goldmine-build/goldmine.git", flags.CheckoutDir)
	if err != nil {
		return "Failed checkout", err
	}

	refs := fmt.Sprintf("refs/pull/%d/head", input.PRNumber)
	_, err = checkout.Git(ctx, "fetch", "origin", refs)
	if err != nil {
		return "Failed to pull the ref", err
	}

	_, err = checkout.Git(ctx, "checkout", "FETCH_HEAD")
	if err != nil {
		return "Failed to checkout the ref", err
	}

	return "Checkout Success", nil
}

func RunTests(ctx context.Context, input shared.TrybotWorkflowArgs) (string, error) {
	bazel, err := exec.LookPath("bazelisk")
	if err != nil {
		return "Could not find bazelisk", err
	}
	cmd := exec.CommandContext(ctx, bazel, "test", "//golden/modules/...", "//perf/modules/...", "//go/...")
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "PWD="+flags.CheckoutDir)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return string(b), err
	}

	return "RunTests Success", nil
}

func UploadGoldResults(ctx context.Context, input shared.TrybotWorkflowArgs) (string, error) {
	// TBD
	return "UploadGoldResults not implemented yet.", nil
}

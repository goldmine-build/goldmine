// This will be the main workflow for the goldmine CI.
//
// Can be triggered manually by sending a message to the restate server, for
// example:
//
//
//     kubectl port-forward svc/restate-requests 8080:8080
//
//     curl --include --request POST \
//       --url http://127.0.0.1:8080/CI/RunAllBuildsAndTestsV1/send \
//       --header 'Accept: application/json' \
//       --header 'Content-Type: application/json' \
//       --header 'idempotency-key: ' \
//       --data '{  "login": "jcgregorio",  "patchset": 13,  "pr": 7, "sha": "01482eb651c1881437dc8f9e928677222943e1dc" }'

package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	restate "github.com/restatedev/sdk-go"
	"github.com/restatedev/sdk-go/server"
	shared "go.goldmine.build/ci/go"
	"go.goldmine.build/go/common"
	"go.goldmine.build/go/git"
	"go.goldmine.build/go/git/provider/providers/gitapi"
	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/sklog"
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

	RestateURL string
}

// Flagset constructs a flag.FlagSet for the App.
func (s *ServerFlags) Flagset() *flag.FlagSet {
	fs := flag.NewFlagSet("ci-workflow", flag.ExitOnError)
	fs.StringVar(&s.Port, "port", ":8000", "Main UI address (e.g., ':8000').")
	fs.StringVar(&s.PromPort, "prom_port", ":20000", "Metrics service address (e.g., ':20000').")
	fs.StringVar(&s.PprofPort, "pprof_port", "", "PProf handler (e.g., ':9001'). PProf not enabled if the empty string (default).")
	fs.StringVar(&s.HealthzPort, "healthz_port", ":10000", "The port for health checks.")
	fs.StringVar(&s.CheckoutDir, "checkout_dir", "", "The file location of the git checkout.")

	fs.StringVar(&s.PatPath, "pat_path", "", "The file location of the git auth token in a file.")
	fs.StringVar(&s.Owner, "owner", "goldmine-build", "GitHub user or organization.")
	fs.StringVar(&s.Repo, "repo", "goldmine", "GitHub repo.")
	fs.StringVar(&s.Branch, "branch", "main", "GitHub repo branch.")

	fs.StringVar(&s.RestateURL, "restate_url", "https://restate-server.tail433733.ts.net", "The URL of the Restate UI.")

	return fs
}

var (
	flags  ServerFlags
	gitApi *gitapi.GitApi = nil

	// https://bazel.build/run/scripts#exit-codes
	bazelExitCodesForNonInfraErrors = []int{1, 3, 4}
)

type CI struct{}

func (c CI) RunAllBuildsAndTestsV1(ctx restate.Context, input shared.CIWorkflowArgs) error {
	sklog.Info("Checking out code.")

	// Always send an infra link.
	infraStatus(ctx, input, gitapi.Pending, "Running...")

	// Check out the code.
	checkout, err := git.NewCheckout(ctx, "https://github.com/goldmine-build/goldmine.git", flags.CheckoutDir)
	if err != nil {
		return infraError(ctx, input, err, "Failed checkout")
	}

	// Clean up any lingering files from the last run.
	if err = gitCommand(ctx, input, checkout, "reset", "--hard", "origin/main"); err != nil {
		return err
	}

	// Check out either the PR or a commit on main.
	if input.PRNumber > 0 {
		if err = gitCommand(ctx, input, checkout, "fetch", "origin", fmt.Sprintf("refs/pull/%d/head", input.PRNumber)); err != nil {
			return err
		}

		if err = gitCommand(ctx, input, checkout, "checkout", "FETCH_HEAD"); err != nil {
			return err
		}
	} else {
		if err = gitCommand(ctx, input, checkout, "fetch", "origin", "refs/heads/main"); err != nil {
			return err
		}

		if err = gitCommand(ctx, input, checkout, "checkout", input.SHA); err != nil {
			return err
		}
	}

	bazel, err := exec.LookPath("bazelisk")
	if err != nil {
		return skerr.Wrap(err)
	}

	sklog.Info("Sanity Check")
	err = runBazelCommand(ctx, input, "Sanity Check", bazel, "query", "//...")
	if err != nil {
		return err
	}

	sklog.Info("Build")
	err = runBazelCommand(ctx, input, "Build", bazel, "build", "//golden/...", "//perf/...", "//go/...")
	if err != nil {
		return err
	}

	sklog.Info("Test")
	err = runBazelCommand(ctx, input, "Test", bazel, "test", "//golden/modules/...", "//perf/modules/...", "//go/...")
	if err != nil {
		return err
	}

	// TODO Make this into a bazel command also?
	sklog.Info("UploadGoldResults")
	var cmd *exec.Cmd
	if input.PRNumber > 0 {
		cmd = exec.CommandContext(ctx, "./upload_to_gold/upload.sh", input.SHA, fmt.Sprintf("%d", input.PRNumber))
	} else {
		// Passing in an empty PR Number indicates this is on main and not in a PR.
		cmd = exec.CommandContext(ctx, "./upload_to_gold/upload.sh", input.SHA)
	}
	if b, err := cmd.CombinedOutput(); err != nil {
		sklog.Errorf("Failed to run upload.sh script: %s: %s", err, string(b))
		return infraError(ctx, input, err, "Infrastructure error trying to upload to Gold.")
	}
	sklog.Info("UploadGoldResults Complete")

	infraStatus(ctx, input, gitapi.Success, "Success.")

	return nil
}

func gitCommand(ctx restate.Context, input shared.CIWorkflowArgs, checkout *git.Checkout, args ...string) error {
	_, err := checkout.Git(ctx, args...)
	if err != nil {
		return infraError(ctx, input, err, fmt.Sprintf("Failed running: git %s", strings.Join(args, " ")))
	}
	return nil
}

func runBazelCommand(ctx restate.Context, input shared.CIWorkflowArgs, step string, bazel string, args ...string) error {
	cmd := exec.CommandContext(ctx, bazel, args...)
	// Point to the running emulators.
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, "COCKROACHDB_EMULATOR_HOST=localhost:8895", "PUBSUB_EMULATOR_HOST=localhost:8893")
	os.Chdir(filepath.Join(flags.CheckoutDir, flags.Repo))
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return skerr.Wrap(err)
	}
	err = cmd.Start()
	if err != nil {
		return infraError(ctx, input, err, "Infrastructure error on Start")
	}

	// Extract the link to the BuildBuddy run.
	link, err := findBuildBuddyLink(stderr)
	sklog.Infof("LINK: %q", link)
	// Keep reading from stderr and pipe that into the logs.
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			sklog.Info(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			sklog.Errorf("reading stderr: %s", err)
		}
	}()
	buildStatus(ctx, input, gitapi.Pending, link, step)

	if err := cmd.Wait(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			if slices.Contains(bazelExitCodesForNonInfraErrors, exitError.ProcessState.ExitCode()) {
				// The build or one or more tests failed.
				buildStatus(ctx, input, gitapi.Error, link, step)
			} else {
				// Something more fundamental broke.
				return infraError(ctx, input, err, "Infrastructure error while running")
			}
		} else {
			return infraError(ctx, input, err, "Infrastructure I/O error while running")
		}
	} else {
		buildStatus(ctx, input, gitapi.Success, link, step)
	}
	return nil
}

func main() {
	// Command line flags.
	common.InitWithMust(
		"github-ci-workflow",
		common.PrometheusOpt(&flags.PromPort),
		common.FlagSetOpt((&flags).Flagset()),
	)

	var err error
	ctx := context.Background()

	sklog.Info("Checking out code.")
	_, err = git.NewCheckout(ctx, "https://github.com/goldmine-build/goldmine.git", "tmp/emulators")
	if err != nil {
		sklog.Fatalf("Failed to check out code for emulators: %s", err)
	}

	bazel, err := exec.LookPath("bazelisk")
	if err != nil {
		sklog.Fatal(err)
	}

	go func() {
		// TODO - There a slight race here with the very first job that this
		// application accepts if this bazel command hasn't started already.

		// Start emulators, but don't wait for the launch to complete.
		emuCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		cmd := exec.CommandContext(emuCtx, bazel, "run", "//scripts/run_emulators", "start")
		cmd.Env = os.Environ()
		os.Chdir("/tmp/emulators/goldmine")
		b, err := cmd.CombinedOutput()
		if err != nil {
			sklog.Fatalf("Failed starting emulators: %s: %s", err, string(b))
		}
		sklog.Info("Emulators started")
	}()

	gitApi, err = gitapi.New(context.Background(), flags.PatPath, flags.Owner, flags.Repo, flags.Branch)
	if err != nil {
		sklog.Fatalf("Unable to create GitHub API: %s", err)
	}

	server := server.NewRestate().Bind(
		restate.Reflect(
			CI{},
			restate.WithAbortTimeout(30*time.Minute),
			restate.WithDocumentation("Goldmine CI Build and Test workflow.")))

	sklog.Fatal(server.Start(context.Background(), flags.Port))
}

func getRestateRequestPermalink(ctx restate.Context) string {
	// URLs for the invocations look like this:
	//
	//
	// https://restate-server.tail433733.ts.net/ui/invocations/inv_1eRfTha6XtFP4NKbOvV7i2k9b5coF1dmvy
	return flags.RestateURL + "/ui/invocations/" + ctx.Request().ID
}

func infraStatus(ctx restate.Context, input shared.CIWorkflowArgs, state gitapi.State, msg string) {
	err := gitApi.SetStatus(ctx, input.SHA, state, getRestateRequestPermalink(ctx), msg, "Infra")
	if err != nil {
		sklog.Errorf("Failed to set GitHub status: %s", err)
	}

}

func infraError(ctx restate.Context, input shared.CIWorkflowArgs, err error, format string, args ...interface{}) error {
	fullErrorMsg := fmt.Sprintf("%s: %s", fmt.Sprintf(format, args...), err)
	sklog.Error(fullErrorMsg)

	err = gitApi.SetStatus(ctx, input.SHA, gitapi.Error, getRestateRequestPermalink(ctx), fullErrorMsg, "Infra")
	if err != nil {
		sklog.Errorf("Failed to set GitHub status: %s", err)
	}
	return skerr.Wrap(err)
}

func buildStatus(ctx context.Context, input shared.CIWorkflowArgs, state gitapi.State, link string, msg string) {
	err := gitApi.SetStatus(ctx, input.SHA, state, link, msg, "CI")
	if err != nil {
		sklog.Errorf("Failed to set GitHub status: %s", err)
	}
}

const bazelStreamingTargetPrefix = "INFO: Streaming build results to: "

// Use streaming output and pull the BuildBuddy URL from the output and write that back to the GitHub PR.
// The line looks like:
//
//	INFO: Streaming build results to: https://app.buildbuddy.io/invocation/some-uuid-here
func findBuildBuddyLink(stderr io.ReadCloser) (string, error) {
	s := bufio.NewScanner(stderr)
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, bazelStreamingTargetPrefix) {
			link := strings.TrimSpace(line[len(bazelStreamingTargetPrefix):])
			sklog.Infof("link: %q", link)
			return link, nil
		}
	}
	return "", skerr.Fmt("BuildBuddy link not found")
}

// This will be the main workflow for the goldmine CI.
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

func (c CI) RunAllBuildsAndTestsV1(ctx restate.Context, input shared.TrybotWorkflowArgs) error {
	sklog.Info("Checking out code.")
	checkout, err := git.NewCheckout(ctx, "https://github.com/goldmine-build/goldmine.git", flags.CheckoutDir)
	if err != nil {
		return infraError(ctx, input, err, "Failed checkout")
	}

	_, err = checkout.Git(ctx, "reset", "--hard", "origin/main")
	if err != nil {
		return infraError(ctx, input, err, "Failed to reset --hard origin/main")
	}

	if input.PRNumber > 0 {
		refs := fmt.Sprintf("refs/pull/%d/head", input.PRNumber)
		_, err = checkout.Git(ctx, "fetch", "origin", refs)
		if err != nil {
			return infraError(ctx, input, err, "Failed to pull ref: %s", refs)
		}

		_, err = checkout.Git(ctx, "checkout", "FETCH_HEAD")
		if err != nil {
			return infraError(ctx, input, err, "Failed to checkout FETCH_HEAD")
		}
	} else {
		_, err = checkout.Git(ctx, "fetch", "origin", "refs/heads/main")
		if err != nil {
			return infraError(ctx, input, err, "Failed to pull ref: refs/heads/main")
		}

		_, err = checkout.Git(ctx, "checkout", input.SHA)
		if err != nil {
			return infraError(ctx, input, err, "Failed to checkout git hash: %s", input.SHA)
		}
	}

	bazel, err := exec.LookPath("bazelisk")
	if err != nil {
		return skerr.Wrap(err)
	}

	sklog.Info("Starting build and test.")
	cmd := exec.CommandContext(ctx, bazel, "test", "//golden/modules/...", "//perf/modules/...", "//go/...")

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
		return infraError(ctx, input, err, "Infrastructure error starting build")
	}

	link, err := findBuildBuddyLink(stderr)
	sklog.Infof("LINK: %q", link)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			sklog.Info(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			sklog.Errorf("reading stderr: %s", err)
		}
	}()
	buildStatus(ctx, input, gitapi.Pending, link, "Running tests")

	if err := cmd.Wait(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			if slices.Contains(bazelExitCodesForNonInfraErrors, exitError.ProcessState.ExitCode()) {
				// The build or one or more tests failed.
				buildStatus(ctx, input, gitapi.Error, link, "Builds/Tests failed")
			} else {
				// Something more fundamental broke.
				return infraError(ctx, input, err, "Infrastructure error trying to build")
			}
		}
	} else {
		buildStatus(ctx, input, gitapi.Success, link, "All Builds/Tests succeeded")
	}

	sklog.Info("UploadGoldResults")
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
		// TODO Don't bother trying to use Bazel for this, instead install cdb
		// and gcloud cmd line tools in the container.

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

func infraError(ctx restate.Context, input shared.TrybotWorkflowArgs, err error, format string, args ...interface{}) error {
	fullErrorMsg := fmt.Sprintf("%s: %s", fmt.Sprintf(format, args...), err)
	sklog.Error(fullErrorMsg)

	// URLs for the invocations look like this:
	//
	//
	// https://restate-server.tail433733.ts.net/ui/invocations/inv_1eRfTha6XtFP4NKbOvV7i2k9b5coF1dmvy
	err = gitApi.SetStatus(ctx, input.SHA, gitapi.Error, flags.RestateURL+"/ui/invocations/"+ctx.Request().ID, fullErrorMsg, "Infra")
	if err != nil {
		sklog.Errorf("Failed to set GitHub status: %s", err)
	}
	return skerr.Wrap(err)
}

func buildStatus(ctx context.Context, input shared.TrybotWorkflowArgs, state gitapi.State, link string, msg string) error {
	err := gitApi.SetStatus(ctx, input.SHA, state, link, msg, "CI")
	if err != nil {
		sklog.Errorf("Failed to set GitHub status: %s", err)
	}
	return nil
}

const bazelStreamingTargetPrefix = "INFO: Streaming build results to: "

// TODO Use streaming output and pull the BuildBuddy URL from the output and write that back to the GitHub PR.
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

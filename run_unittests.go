package main

/*
	This program runs all unit tests in the repository.
*/

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/fileutil"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/timer"
	"go.skia.org/infra/go/util"
)

const (
	// This gets filled out and printed when a test fails.
	TEST_FAILURE = `
============================= TEST FAILURE =============================
Test: %s

Command: %s

Error:
%s

Full output:
------------------------------------------------------------------------
%s
------------------------------------------------------------------------
`
)

var (
	// Error message shown when a required executable is not installed.
	ERR_NEED_INSTALL = "%s failed to run! Is it installed? Error: %v"

	// Directories with these names are skipped when searching for tests.
	NO_CRAWL_DIR_NAMES = []string{
		".git",
		".recipe_deps",
		"assets",
		"bower_components",
		"third_party",
		"node_modules",
	}

	// Directories with these paths, relative to the checkout root, are
	// skipped when searching for tests.
	NO_CRAWL_REL_PATHS = []string{
		"common",
	}

	POLYMER_PATHS = []string{
		"res/imp",
		"autoroll/res/imp",
		"fuzzer/res/imp",
		"status/res/imp",
	}

	// goTestRegexp is a regular expression used for finding the durations
	// of tests.
	goTestRegexp = regexp.MustCompile("--- (\\w+):\\s+(\\w+)\\s+\\((.+)\\)")

	// Flags.

	race = flag.Bool("race", false, "Whether or not to enable the race flag when running go tests.  This flag signals to only run go tests.")

	// writeTimings is a file in which to write the test timings in JSON
	// format.
	writeTimings = flag.String("write_timings", "", "JSON file in which to write the test timings.")

	// Every call to cmdTest uses a different KARMA_PORT.
	nextKarmaPort = 9876
)

// cmdTest returns a test which runs a command and fails if the command fails.
func cmdTest(cmd []string, cwd, name, testType string) *test {
	karmaPort := nextKarmaPort
	nextKarmaPort++
	return &test{
		Name: name,
		Cmd:  strings.Join(cmd, " "),
		run: func() (string, error) {
			command := exec.Command(cmd[0], cmd[1:]...)
			if cwd != "" {
				command.Dir = cwd
			}
			command.Env = append(os.Environ(), fmt.Sprintf("KARMA_PORT=%d", karmaPort))
			output, err := command.CombinedOutput()
			if err != nil {
				if _, err2 := exec.LookPath(cmd[0]); err2 != nil {
					return string(output), fmt.Errorf(ERR_NEED_INSTALL, cmd[0], err)
				}
			}
			return string(output), err
		},
		Type: testType,
	}
}

func polylintTest(cwd, fileName string) *test {
	cmd := []string{"polylint", "--no-recursion", "--root", cwd, "--input", fileName}
	return &test{
		Name: fmt.Sprintf("polylint in %s", filepath.Join(cwd, fileName)),
		Cmd:  strings.Join(cmd, " "),
		run: func() (string, error) {
			command := exec.Command(cmd[0], cmd[1:]...)
			outputBytes, err := command.Output()
			if err != nil {
				if _, err2 := exec.LookPath(cmd[0]); err2 != nil {
					return string(outputBytes), fmt.Errorf(ERR_NEED_INSTALL, cmd[0], err)
				}
				return string(outputBytes), err
			}

			unresolvedProblems := ""
			count := 0

			for s := bufio.NewScanner(bytes.NewBuffer(outputBytes)); s.Scan(); {
				badFileLine := s.Text()
				if !s.Scan() {
					return string(outputBytes), fmt.Errorf("Unexpected end of polylint output after %q:\n%s", badFileLine, string(outputBytes))
				}
				problemLine := s.Text()
				if !strings.Contains(unresolvedProblems, badFileLine) {
					unresolvedProblems = fmt.Sprintf("%s\n%s\n%s", unresolvedProblems, badFileLine, problemLine)
					count++
				}
			}

			if unresolvedProblems == "" {
				return "", nil
			}
			return "", fmt.Errorf("%d unresolved polylint problems:\n%s\n", count, unresolvedProblems)
		},
		Type: testutils.LARGE_TEST,
	}
}

// buildPolymerFolder runs the Makefile in the given folder.  This sets up the symbolic links so dependencies can be located for polylint.
func buildPolymerFolder(cwd string) error {
	cmd := cmdTest([]string{"make", "deps"}, cwd, fmt.Sprintf("Polymer build in %s", cwd), testutils.LARGE_TEST)
	return cmd.Run()
}

// polylintTestsForDir builds the folder once and then returns a list of tests for each Polymer file in the directory.  If the build fails, a dummy test is returned that prints an error message.
func polylintTestsForDir(cwd string, fileNames ...string) []*test {
	if err := buildPolymerFolder(cwd); err != nil {
		return []*test{
			{
				Name: filepath.Join(cwd, "make"),
				Cmd:  filepath.Join(cwd, "make"),
				run: func() (string, error) {
					return "", fmt.Errorf("Could not build Polymer files in %s: %s", cwd, err)
				},
				Type: testutils.LARGE_TEST,
			},
		}
	}
	tests := make([]*test, 0, len(fileNames))
	for _, name := range fileNames {
		tests = append(tests, polylintTest(cwd, name))
	}
	return tests
}

// findPolymerFiles returns all files that probably contain polymer content
// (i.e. end with sk.html) in a given directory.
func findPolymerFiles(dirPath string) []string {
	dir := fileutil.MustOpen(dirPath)
	files := make([]string, 0)
	for _, info := range fileutil.MustReaddir(dir) {
		if n := info.Name(); strings.HasSuffix(info.Name(), "sk.html") {
			files = append(files, n)
		}
	}
	return files
}

//polylintTests creates a list of *test from all directories in POLYMER_PATHS
func polylintTests() []*test {
	tests := make([]*test, 0)
	for _, path := range POLYMER_PATHS {
		tests = append(tests, polylintTestsForDir(path, findPolymerFiles(path)...)...)
	}
	return tests
}

// goTest returns a test which runs `go test` in the given cwd.
func goTest(cwd string, testType string, args ...string) *test {
	cmd := []string{"go", "test", "-v", "./go/...", "-p", "1", "-parallel", "1"}
	if *race {
		cmd = append(cmd, "-race")
	}
	cmd = append(cmd, args...)
	t := cmdTest(cmd, cwd, fmt.Sprintf("go tests (%s) in %s", testType, cwd), testType)

	// Go tests print out their own timings. Parse them to obtain individual
	// test times.
	t.duration = func() map[string]time.Duration {
		rv := map[string]time.Duration{}
		split := strings.Split(t.output, "\n")
		for _, line := range split {
			m := goTestRegexp.FindStringSubmatch(line)
			if len(m) == 4 {
				if m[1] == "PASS" || m[1] == "FAIL" {
					d, err := time.ParseDuration(m[3])
					if err != nil {
						sklog.Errorf("Got invalid test duration: %q", m[3])
						continue
					}
					rv[m[2]] = d
				}
			}
		}
		return rv
	}
	return t
}

// goTestSmall returns a test which runs `go test --small` in the given cwd.
func goTestSmall(cwd string) *test {
	return goTest(cwd, testutils.SMALL_TEST, "--small", "--timeout", testutils.TIMEOUT_SMALL)
}

// goTestMedium returns a test which runs `go test --medium` in the given cwd.
func goTestMedium(cwd string) *test {
	return goTest(cwd, testutils.MEDIUM_TEST, "--medium", "--timeout", testutils.TIMEOUT_MEDIUM)
}

// goTestLarge returns a test which runs `go test --large` in the given cwd.
func goTestLarge(cwd string) *test {
	return goTest(cwd, testutils.LARGE_TEST, "--large", "--timeout", testutils.TIMEOUT_LARGE)
}

// pythonTest returns a test which runs the given Python script and fails if
// the script fails.
func pythonTest(testPath string) *test {
	return cmdTest([]string{"python", testPath}, ".", path.Base(testPath), testutils.SMALL_TEST)
}

// Verify that "go generate ./..." produces no diffs.
func goGenerate() *test {
	cmd := []string{"go", "generate", "./..."}
	return &test{
		Name: "go generate",
		Cmd:  strings.Join(cmd, " "),
		run: func() (string, error) {
			// Run "git diff" to get a baseline.
			diff, err := exec.Command("git", "diff", "--no-ext-diff").CombinedOutput()
			if err != nil {
				return string(diff), fmt.Errorf("Failed to run git diff: %s", err)
			}

			// Run "go generate".
			command := exec.Command(cmd[0], cmd[1:]...)
			outputBytes, err := command.CombinedOutput()
			if err != nil {
				return string(outputBytes), fmt.Errorf("Failed to run go generate: %s", err)
			}

			// Run "git diff" again and assert that the diff didn't
			// change.
			diff2, err := exec.Command("git", "diff", "--no-ext-diff").CombinedOutput()
			if err != nil {
				return string(diff2), fmt.Errorf("Failed to run git diff: %s", err)
			}
			if string(diff) != string(diff2) {
				return fmt.Sprintf("Diff before:\n%s\n\nDiff after:\n%s", string(diff), string(diff2)), fmt.Errorf("go generate created new diffs!")
			}
			return "", nil
		},
		Type: testutils.LARGE_TEST,
	}
}

// test is a struct which represents a single test to run.
type test struct {
	// Name is the human-friendly name of the test.
	Name string

	// Cmd is the command to run.
	Cmd string

	// duration is a function which returns the duration(s) of the test(s).
	duration func() map[string]time.Duration

	// output contains the output from the command. It is only populated
	// after Run() is called.
	output string

	// run is a function used to run the test. It returns any error and the
	// output of the test.
	run func() (string, error)

	// totalTime is the duration of the test, populated after Run().
	totalTime time.Duration

	// Type is the small/medium/large categorization of the test.
	Type string
}

// Run executes the function for the given test and returns an error if it fails.
func (t *test) Run() error {
	if !util.In(t.Type, testutils.TEST_TYPES) {
		sklog.Fatalf("Test %q has invalid type %q", t.Name, t.Type)
	}
	if !testutils.ShouldRun(t.Type) {
		sklog.Infof("Not running %s tests; skipping %q", t.Type, t.Name)
		return nil
	}

	started := time.Now()
	defer func() {
		t.totalTime = time.Now().Sub(started)
	}()
	output, err := t.run()
	if err != nil {
		return fmt.Errorf(TEST_FAILURE, t.Name, t.Cmd, err, output)
	}
	t.output = output
	return nil
}

// Duration returns the duration(s) of the test(s) which ran.
func (t *test) Duration() map[string]time.Duration {
	if t.duration == nil {
		return map[string]time.Duration{t.Name: t.totalTime}
	}
	return t.duration()
}

// Find and run tests.
func main() {
	common.Init()

	// Ensure that we're actually going to run something.
	ok := false
	for _, tt := range testutils.TEST_TYPES {
		if testutils.ShouldRun(tt) {
			ok = true
		}
	}
	if !ok {
		sklog.Errorf("Must provide --small, --medium, and/or --large. This will cause an error in the future.")
	}

	defer timer.New("Finished").Stop()

	_, filename, _, _ := runtime.Caller(0)
	rootDir := path.Dir(filename)

	if *race {
		// Use alternative timeouts when --race is enabled because the tests
		// inherently take longer with the extra instrumentation.
		testutils.TIMEOUT_SMALL = testutils.TIMEOUT_RACE
		testutils.TIMEOUT_MEDIUM = testutils.TIMEOUT_RACE
		testutils.TIMEOUT_LARGE = testutils.TIMEOUT_RACE
	}

	// If we are running full tests make sure we have the latest
	// pdfium_test installed.
	if testutils.ShouldRun(testutils.MEDIUM_TEST) || testutils.ShouldRun(testutils.LARGE_TEST) {
		sklog.Info("Installing pdfium_test if necessary.")
		pdfiumInstall := path.Join(rootDir, "pdfium", "install_pdfium.sh")
		if err := exec.Command(pdfiumInstall).Run(); err != nil {
			sklog.Fatalf("Failed to install pdfium_test: %v", err)
		}
		sklog.Info("Latest pdfium_test installed successfully.")
	}

	// Gather all of the tests to run.
	sklog.Info("Searching for tests.")
	tests := []*test{goGenerate()}
	gotests := []*test{}

	// Search for Python tests and Go dirs to test in the repo.
	// These tests are blacklisted from running on our bots because they
	// depend on packages (django) which are not included with Python in
	// CIPD.
	pythonTestBlacklist := map[string]bool{
		"csv_comparer_test.py":          true,
		"json_summary_combiner_test.py": true,
	}
	if err := filepath.Walk(rootDir, func(p string, info os.FileInfo, err error) error {
		basename := path.Base(p)
		if info.IsDir() {
			// Skip some directories.
			for _, skip := range NO_CRAWL_DIR_NAMES {
				if basename == skip {
					return filepath.SkipDir
				}
			}
			for _, skip := range NO_CRAWL_REL_PATHS {
				if p == path.Join(rootDir, skip) {
					return filepath.SkipDir
				}
			}

			if basename == "go" {
				gotests = append(gotests, goTestSmall(path.Dir(p)))
				gotests = append(gotests, goTestMedium(path.Dir(p)))
				gotests = append(gotests, goTestLarge(path.Dir(p)))
			}
		}
		if strings.HasSuffix(basename, "_test.py") && !pythonTestBlacklist[basename] {
			tests = append(tests, pythonTest(p))
		}
		return nil
	}); err != nil {
		sklog.Fatal(err)
	}

	// Other tests.
	tests = append(tests, cmdTest([]string{"go", "vet", "./..."}, ".", "go vet", testutils.SMALL_TEST))
	tests = append(tests, cmdTest([]string{"gofmt", "-s", "-d"}, ".", "go simplify (gofmt -s -w .)", testutils.SMALL_TEST))
	tests = append(tests, cmdTest([]string{"errcheck", "-ignore", ":Close", "go.skia.org/infra/..."}, ".", "errcheck", testutils.MEDIUM_TEST))
	tests = append(tests, cmdTest([]string{"go", "run", "prober/go/build_probers_json5/main.go", "--srcdir=.", "--dest=/tmp/allprobers.json5"}, ".", "probers test", testutils.MEDIUM_TEST))
	tests = append(tests, cmdTest([]string{"make", "validate"}, "perf", "perf validator", testutils.MEDIUM_TEST))
	tests = append(tests, cmdTest([]string{"python", "infra/bots/recipes.py", "test", "run"}, ".", "recipes test", testutils.MEDIUM_TEST))
	tests = append(tests, cmdTest([]string{"go", "run", "infra/bots/gen_tasks.go", "--test"}, ".", "gen_tasks.go --test", testutils.SMALL_TEST))
	tests = append(tests, cmdTest([]string{"python", "go/testutils/uncategorized_tests.py"}, ".", "uncategorized tests", testutils.SMALL_TEST))
	tests = append(tests, cmdTest([]string{"make", "testci"}, "common-sk", "common-sk elements", testutils.MEDIUM_TEST))
	tests = append(tests, cmdTest([]string{"make", "testci"}, "named-fiddles", "named-fiddles elements", testutils.MEDIUM_TEST))
	tests = append(tests, cmdTest([]string{"make", "test"}, "push", "push elements", testutils.MEDIUM_TEST))

	if !*race {
		// put this behind a flag because polylintTests trys to build the polymer files
		tests = append(tests, polylintTests()...)
	}

	// TODO(stephan): Re-enable goimports once we can guarantee that go-generate
	// doesn't conflict with it.
	// Temporarily disabled.
	if false {
		goimportsCmd := []string{"goimports", "-l", "."}
		tests = append(tests, &test{
			Name: "goimports",
			Cmd:  strings.Join(goimportsCmd, " "),
			run: func() (string, error) {
				command := exec.Command(goimportsCmd[0], goimportsCmd[1:]...)
				output, err := command.Output()
				outStr := strings.Trim(string(output), "\n")
				if err != nil {
					if _, err2 := exec.LookPath(goimportsCmd[0]); err2 != nil {
						return outStr, fmt.Errorf(ERR_NEED_INSTALL, goimportsCmd[0], err)
					}
					// Sometimes goimports returns exit code 2, but gives no reason.
					if outStr != "" {
						return fmt.Sprintf("goimports output: %q", outStr), err
					}
				}
				diffFiles := strings.Split(outStr, "\n")
				if len(diffFiles) > 0 && !(len(diffFiles) == 1 && diffFiles[0] == "") {
					return outStr, fmt.Errorf("goimports found diffs in the following files:\n  - %s", strings.Join(diffFiles, ",\n  - "))
				}
				return "", nil

			},
			Type: testutils.MEDIUM_TEST,
		})
	}

	if *race {
		tests = gotests
	} else {
		tests = append(gotests, tests...)
	}

	// Run the tests.
	sklog.Infof("Found %d tests.", len(tests))
	errors := map[string]error{}

	if *race {
		// Do unit tests one at a time, as the -race can fail when done concurrently with a bunch of other stuff.
		for _, t := range gotests {
			if err := t.Run(); err != nil {
				errors[t.Name] = err
			}
			sklog.Infof("Finished %s", t.Name)
		}
	} else {

		var mutex sync.Mutex
		var wg sync.WaitGroup
		for _, t := range tests {
			wg.Add(1)
			go func(t *test) {
				defer wg.Done()
				if err := t.Run(); err != nil {
					mutex.Lock()
					errors[t.Name] = err
					mutex.Unlock()
				}
			}(t)
		}
		wg.Wait()
	}

	// Collect test durations.
	durations := map[string]time.Duration{}
	for _, t := range tests {
		for k, v := range t.Duration() {
			if _, ok := durations[k]; ok {
				sklog.Errorf("Duplicate test name %q; not keeping timing.", k)
				continue
			}
			durations[k] = v
		}
	}
	if *writeTimings != "" {
		b, err := json.MarshalIndent(durations, "", "  ")
		if err != nil {
			errors["encode output"] = err
		} else {
			if err := ioutil.WriteFile(*writeTimings, b, os.ModePerm); err != nil {
				errors["write output"] = err
			}
		}
	}
	if len(errors) > 0 {
		for _, e := range errors {
			sklog.Error(e)
		}
		os.Exit(1)
	}
	sklog.Info("All tests succeeded.")
}

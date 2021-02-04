package unittest

import (
	"flag"
	"fmt"
	"runtime"
	"strings"
	"time"

	"go.skia.org/infra/bazel/go/bazel"
	"go.skia.org/infra/go/emulators"
	"go.skia.org/infra/go/sktest"
)

const (
	SMALL_TEST  = "small"
	MEDIUM_TEST = "medium"
	LARGE_TEST  = "large"
	MANUAL_TEST = "manual"
)

var (
	small         = flag.Bool(SMALL_TEST, false, "Whether or not to run small tests.")
	medium        = flag.Bool(MEDIUM_TEST, false, "Whether or not to run medium tests.")
	large         = flag.Bool(LARGE_TEST, false, "Whether or not to run large tests.")
	manual        = flag.Bool(MANUAL_TEST, false, "Whether or not to run manual tests.")
	uncategorized = flag.Bool("uncategorized", false, "Only run uncategorized tests.")

	// DEFAULT_RUN indicates whether the given test type runs by default
	// when no filter flag is specified.
	DEFAULT_RUN = map[string]bool{
		SMALL_TEST:  true,
		MEDIUM_TEST: true,
		LARGE_TEST:  true,
		MANUAL_TEST: true,
	}

	TIMEOUT_SMALL  = "4s"
	TIMEOUT_MEDIUM = "15s"
	TIMEOUT_LARGE  = "4m"
	TIMEOUT_MANUAL = TIMEOUT_LARGE

	TIMEOUT_RACE = "5m"

	// TEST_TYPES lists all of the types of tests.
	TEST_TYPES = []string{
		SMALL_TEST,
		MEDIUM_TEST,
		LARGE_TEST,
		MANUAL_TEST,
	}
)

// ShouldRun determines whether the test should run based on the provided flags.
func ShouldRun(testType string) bool {
	if *uncategorized {
		return false
	}

	// Fallback if no test filter is specified.
	if !*small && !*medium && !*large && !*manual {
		return DEFAULT_RUN[testType]
	}

	switch testType {
	case SMALL_TEST:
		return *small
	case MEDIUM_TEST:
		return *medium
	case LARGE_TEST:
		return *large
	case MANUAL_TEST:
		return *manual
	}
	return false
}

// SmallTest is a function which should be called at the beginning of a small
// test: A test (under 2 seconds) with no dependencies on external databases,
// networks, etc.
func SmallTest(t sktest.TestingT) {
	if !ShouldRun(SMALL_TEST) {
		t.Skip("Not running small tests.")
	}
}

// MediumTest is a function which should be called at the beginning of an
// medium-sized test: a test (2-15 seconds) which has dependencies on external
// databases, networks, etc.
func MediumTest(t sktest.TestingT) {
	if !ShouldRun(MEDIUM_TEST) {
		t.Skip("Not running medium tests.")
	}
}

// LargeTest is a function which should be called at the beginning of a large
// test: a test (> 15 seconds) with significant reliance on external
// dependencies which makes it too slow or flaky to run as part of the normal
// test suite.
func LargeTest(t sktest.TestingT) {
	if !ShouldRun(LARGE_TEST) {
		t.Skip("Not running large tests.")
	}
}

// ManualTest is a function which should be called at the beginning of tests
// which shouldn't run on the bots due to excessive running time, external
// requirements, etc. These only run when the --manual flag is set.
func ManualTest(t sktest.TestingT) {
	// Find the caller's file name.
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed")
	}
	file := thisFile
	for skip := 0; file == thisFile; skip++ {
		var ok bool
		_, file, _, ok = runtime.Caller(skip)
		if !ok {
			t.Fatalf("runtime.Caller(%d) failed", skip)
		}
	}

	// Force the naming convention expected by our custom go_test Bazel macro.
	if !strings.HasSuffix(file, "_manual_test.go") {
		t.Fatalf(`Manual tests must be placed in files ending with "_manual_test.go", was: "%s"`, file)
	}

	if !ShouldRun(MANUAL_TEST) {
		t.Skip("Not running manual tests.")
	}
}

// FakeExeTest masks a test from the uncategorized tests check. See executil.go for
// more on what FakeTests are used for.
func FakeExeTest(t sktest.TestingT) {
	if *uncategorized {
		t.Skip(`This is to appease the "uncategorized tests" check`)
	}
}

// BazelOnlyTest is a function which should be called at the beginning of tests
// which should only run under Bazel (e.g. via "bazel test ...").
func BazelOnlyTest(t sktest.TestingT) {
	if !bazel.InBazel() {
		t.Skip("Not running Bazel tests from outside Bazel.")
	}
}

// LinuxOnlyTest is a function which should be called at the beginning of a test
// which should only run on Linux.
func LinuxOnlyTest(t sktest.TestingT) {
	if runtime.GOOS != "linux" {
		t.Skip("Not running Linux-only tests.")
	}
}

// RequiresBigTableEmulator should be called by any test case that requires the BigTable emulator.
//
// When running locally, the test case will fail if the corresponding environment variable is unset.
// When running under RBE, the first invocation of this function will start the emulator and set the
// appropriate environment variable, and any subsequent calls will reuse the emulator instance.
func RequiresBigTableEmulator(t sktest.TestingT) {
	requiresEmulator(t, emulators.BigTable, useTestSuiteSharedEmulatorInstanceUnderRBE)
}

// RequiresCockroachDB should be called by any test case that requires the CockroachDB emulator.
//
// When running locally, the test case will fail if the corresponding environment variable is unset.
// When running under RBE, the first invocation of this function will start the emulator and set the
// appropriate environment variable, and any subsequent calls will reuse the emulator instance.
//
// Note: The CockroachDB emulator is just a test-only, real CockroachDB instance. We refer to it as
// an emulator for consistency with the Google Cloud emulators.
func RequiresCockroachDB(t sktest.TestingT) {
	requiresEmulator(t, emulators.CockroachDB, useTestSuiteSharedEmulatorInstanceUnderRBE)
}

// RequiresDatastoreEmulator should be called by any test case that requires the Datastore emulator.
//
// When running locally, the test case will fail if the corresponding environment variable is unset.
// When running under RBE, the first invocation of this function will start the emulator and set the
// appropriate environment variable, and any subsequent calls will reuse the emulator instance.
func RequiresDatastoreEmulator(t sktest.TestingT) {
	requiresEmulator(t, emulators.Datastore, useTestSuiteSharedEmulatorInstanceUnderRBE)
}

// RequiresFirestoreEmulator should be called by any test case that requires the Firestore emulator.
//
// When running locally, the test case will fail if the corresponding environment variable is unset.
// When running under RBE, the first invocation of this function will start the emulator and set the
// appropriate environment variable, and any subsequent calls will reuse the emulator instance.
func RequiresFirestoreEmulator(t sktest.TestingT) {
	requiresEmulator(t, emulators.Firestore, useTestSuiteSharedEmulatorInstanceUnderRBE)
}

// RequiresFirestoreEmulatorWithTestCaseSpecificInstanceUnderRBE is equivalent to
// RequiresFirestoreEmulator, except that under Bazel and RBE, it will always launch a new emulator
// instance that will only be visible to the current test case. After the test case finishes, the
// emulator instance will *not* be reused by any subsequent test cases, and will eventually be
// killed after the test suite finishes running.
//
// It is safe for some test cases in a test suite to call RequiresFirestoreEmulator, and for some
// others to call RequiresFirestoreEmulatorWithTestCaseSpecificInstanceUnderRBE.
//
// This should only be used in the presence of hard-to-diagnose bugs that only occur under RBE.
func RequiresFirestoreEmulatorWithTestCaseSpecificInstanceUnderRBE(t sktest.TestingT) {
	requiresEmulator(t, emulators.Firestore, useTestCaseSpecificEmulatorInstanceUnderRBE)
}

// RequiresPubSubEmulator should be called by any test case that requires the PubSub emulator.
//
// When running locally, the test case will fail if the corresponding environment variable is unset.
// When running under RBE, the first invocation of this function will start the emulator and set the
// appropriate environment variable, and any subsequent calls will reuse the emulator instance.
func RequiresPubSubEmulator(t sktest.TestingT) {
	requiresEmulator(t, emulators.PubSub, useTestSuiteSharedEmulatorInstanceUnderRBE)
}

// emulatorScopeUnderRBE indicates whether the test case requesting an emulator can reuse an
// emulator instance started by an earlier test case, or whether it requires a test case-specific
// instance not visible to any other test cases.
//
// Note that this is ignored outside of Bazel and RBE, in which case, tests will always use the
// emulator instance pointed to by the corresponding *_EMULATOR_HOST environment variable.
type emulatorScopeUnderRBE string

const (
	useTestSuiteSharedEmulatorInstanceUnderRBE  = emulatorScopeUnderRBE("useTestSuiteSharedEmulatorInstanceUnderRBE")
	useTestCaseSpecificEmulatorInstanceUnderRBE = emulatorScopeUnderRBE("useTestCaseSpecificEmulatorInstanceUnderRBE")
)

// requiresEmulator fails the test when running outside of RBE if the corresponding *_EMULATOR_HOST
// environment variable wasn't set, or starts a new emulator instance under RBE if necessary.
func requiresEmulator(t sktest.TestingT, emulator emulators.Emulator, scope emulatorScopeUnderRBE) {
	if bazel.InRBE() {
		setUpEmulatorBazelRBEOnly(t, emulator, scope)
		return
	}

	// When running locally, the developer is responsible for running any necessary emulators.
	host := emulators.GetEmulatorHostEnvVar(emulator)
	if host == "" {
		t.Fatalf(`This test requires the %s emulator, which you can start with

    $ ./scripts/run_emulators/run_emulators start

and then set the environment variables it prints out.`, emulator)
	}
}

// testCaseSpecificEmulatorInstancesBazelRBEOnly keeps track of the test case-specific emulator
// instances started by the current test case. We allow at most one test case-specific instance of
// each emulator.
var testCaseSpecificEmulatorInstancesBazelRBEOnly = map[emulators.Emulator]bool{}

// setUpEmulatorBazelRBEOnly starts an instance of the given emulator, or reuses an existing
// instance if one was started by an earlier test case.
//
// If a test case-specific instance is requested, it will always start a new emulator instance,
// regardless of whether one was started by an earlier test case. The test case-specific instance
// will only be visible to the current test case (i.e. it will not be visible to any subsequent test
// cases).
//
// Test case-specific instances should only be used in the presence of hard-to-diagnose bugs that
// only occur under RBE.
func setUpEmulatorBazelRBEOnly(t sktest.TestingT, emulator emulators.Emulator, scope emulatorScopeUnderRBE) {
	if !bazel.InRBE() {
		panic("This function must only be called when running under Bazel and RBE.")
	}

	var wasEmulatorStarted bool

	switch scope {
	case useTestCaseSpecificEmulatorInstanceUnderRBE:
		// If the current test case requests a test case-specific instance of the same emulator more
		// than once, it's almost surely a bug, so we fail loudly.
		if testCaseSpecificEmulatorInstancesBazelRBEOnly[emulator] {
			t.Fatalf("A test-case specific instance of the %s emulator was already started.", emulator)
		}

		if err := emulators.StartAdHocEmulatorInstanceAndSetEmulatorHostEnvVarBazelRBEOnly(emulator); err != nil {
			t.Fatalf("Error starting a test case-specific instance of the %s emulator: %v", emulator, err)
		}
		wasEmulatorStarted = true
		testCaseSpecificEmulatorInstancesBazelRBEOnly[emulator] = true
	case useTestSuiteSharedEmulatorInstanceUnderRBE:
		// Start an emulator instance shared among all test cases. If the emulator was already started
		// by an earlier test case, then we'll reuse the emulator instance that's already running.
		var err error
		wasEmulatorStarted, err = emulators.StartEmulatorIfNotRunning(emulator)
		if err != nil {
			t.Fatalf("Error starting emulator: %v", err)
		}

		// Setting the corresponding *_EMULATOR_HOST environment variable is what effectively makes the
		// emulator visible to the test case.
		if err := emulators.SetEmulatorHostEnvVar(emulator); err != nil {
			t.Fatalf("Error setting emulator host environment variables: %v", err)
		}
	default:
		panic(fmt.Sprintf("Unknown emulatorScopeUnderRBE value: %s", scope))
	}

	// If the above code actually started the emulator, give the emulator time to boot.
	//
	// Empirically chosen: A delay of 3 seconds seems OK for all emulators; shorter delays tend to
	// cause flakes.
	if wasEmulatorStarted {
		time.Sleep(3 * time.Second)
	}

	t.Cleanup(func() {
		// By unsetting any *_EMULATOR_HOST environment variables set by the current test case, we
		// ensure that any subsequent test cases only "see" the emulators they request via any of the
		// unittest.Requires*Emulator functions. This makes dependencies on emulators explicit at the
		// test case level, and makes individual test cases more self-documenting.
		if err := emulators.UnsetAllEmulatorHostEnvVars(); err != nil {
			t.Fatalf("Error while unsetting the emulator host environment variables: %v", err)
		}

		// Allow subsequent test cases to start their own test case-specific instances of the emulator.
		if scope == useTestCaseSpecificEmulatorInstanceUnderRBE {
			testCaseSpecificEmulatorInstancesBazelRBEOnly[emulator] = false
		}
	})
}

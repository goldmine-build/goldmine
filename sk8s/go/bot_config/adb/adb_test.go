// Package adb is a simple wrapper around calling adb.
package adb

import (
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.skia.org/infra/go/executil"
	"go.skia.org/infra/go/testutils/unittest"
)

type cleanupFunc func()

const (
	adbShellGetPropSuccess = `[ro.product.manufacturer]: [asus]
[ro.product.model]: [Nexus 7]
[ro.product.name]: [razor]
`
	adbShellDumpSysBattery = `Current Battery Service state:
AC powered: true
USB powered: false
Wireless powered: false
Max charging current: 1500000
Max charging voltage: 5000000
Charge counter: 1928561
status: 2
health: 2
present: true
level: 75
scale: 100
voltage: 3997
temperature: 248
technology: Li-ion`

	nonZeroExitCode = 123
)

func TestRawProperties_HappyPath(t *testing.T) {
	unittest.SmallTest(t)

	ctx := executil.FakeTestsContext("Test_FakeExe_AdbShellGetProp_Success")

	a := New()
	got, err := a.RawProperties(ctx)
	require.NoError(t, err)
	assert.Equal(t, adbShellGetPropSuccess, got)
}

func TestRawProperties_ErrFromAdbNonZeroExitCode(t *testing.T) {
	unittest.SmallTest(t)

	ctx := executil.FakeTestsContext("Test_FakeExe_AdbShellGetProp_NonZeroExitCode")

	a := New()
	_, err := a.RawProperties(ctx)
	require.Error(t, err)
}

func TestRawProperties_EmptyOutputFromAdb(t *testing.T) {
	unittest.SmallTest(t)

	ctx := executil.FakeTestsContext("Test_FakeExe_AdbShellGetProp_EmptyOutput")

	a := New()
	got, err := a.RawProperties(ctx)
	assert.NoError(t, err)
	assert.Empty(t, got)
}

func Test_FakeExe_AdbShellGetProp_Success(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Equal(t, []string{"adb", "shell", "getprop"}, args)

	fmt.Print(adbShellGetPropSuccess)

	// Force exit so we don't get PASS in the output.
	os.Exit(0)
}

func Test_FakeExe_AdbShellGetProp_EmptyOutput(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	// Force exit so we don't get PASS in the output.
	os.Exit(0)
}

func Test_FakeExe_AdbShellGetProp_NonZeroExitCode(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	fmt.Fprintf(os.Stderr, "error: no devices/emulators found")

	os.Exit(nonZeroExitCode)
}

func TestRawDumpSys_HappyPath(t *testing.T) {
	unittest.SmallTest(t)

	ctx := executil.FakeTestsContext("Test_FakeExe_RawDumpSysBattery_Success")

	a := New()
	got, err := a.RawDumpSys(ctx, "battery")
	require.NoError(t, err)
	assert.Equal(t, adbShellDumpSysBattery, got)
}

func Test_FakeExe_RawDumpSysBattery_Success(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Equal(t, []string{"adb", "shell", "dumpsys", "battery"}, args)

	fmt.Print(adbShellDumpSysBattery)

	// Force exit so we don't get PASS in the output.
	os.Exit(0)
}

func TestRawDumpSys_ErrOnNonZeroExitCode(t *testing.T) {
	unittest.SmallTest(t)

	ctx := executil.FakeTestsContext("Test_FakeExe_RawDumpSys_NonZeroExitCode")

	a := New()
	_, err := a.RawDumpSys(ctx, "battery")
	require.Error(t, err)
	// Confirm that both the exit code and the adb stderr make it into the returned error.
	assert.Contains(t, err.Error(), fmt.Sprintf("exit status %d", nonZeroExitCode))
	assert.Contains(t, err.Error(), "error: no devices/emulators found")
}

func Test_FakeExe_RawDumpSys_NonZeroExitCode(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	fmt.Fprintf(os.Stderr, "error: no devices/emulators found")

	os.Exit(nonZeroExitCode)
}

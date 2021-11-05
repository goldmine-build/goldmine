package machine

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.skia.org/infra/go/executil"
	"go.skia.org/infra/go/testutils/noop"
	"go.skia.org/infra/go/testutils/unittest"
	"go.skia.org/infra/machine/go/machine"
	"go.skia.org/infra/machine/go/test_machine_monitor/adb"
	"go.skia.org/infra/machine/go/test_machine_monitor/ios"
	"go.skia.org/infra/machine/go/test_machine_monitor/ssh"
)

const (
	// For example values, see adb_test.go
	getPropPlaceholder        = "Placeholder get prop response"
	dumpSysBatteryPlaceholder = "Placeholder dumpsys battery response"
	dumpSysThermalPlaceholder = "Placeholder dumpsys thermal response"
	// This formatting matters because it is processed in adb.go
	adbUptimePlaceholder = "123.4 567.8"

	iOSVersionPlaceholder    = "99.9.9"
	iOSDeviceTypePlaceholder = "iPhone88,8"

	testUserIP = "root@skia-foobar-01"
	// This example was taken directly from a production ChromeOS device
	sampleChromeOSlsbrelease = `CHROMEOS_DEVSERVER=
CHROMEOS_RELEASE_APPID={9A3BE5D2-C3DC-4AE6-9943-E2C113895DC5}
CHROMEOS_RELEASE_BOARD=octopus-signed-mp-v23keys
CHROMEOS_RELEASE_BRANCH_NUMBER=56
CHROMEOS_RELEASE_BUILDER_PATH=octopus-release/R89-13729.56.0
CHROMEOS_RELEASE_BUILD_NUMBER=13729
CHROMEOS_RELEASE_BUILD_TYPE=Official Build
CHROMEOS_RELEASE_CHROME_MILESTONE=89
CHROMEOS_RELEASE_DESCRIPTION=13729.56.0 (Official Build) stable-channel octopus
CHROMEOS_RELEASE_KEYSET=mp-v23
CHROMEOS_RELEASE_NAME=Chrome OS
CHROMEOS_RELEASE_PATCH_NUMBER=0
CHROMEOS_RELEASE_TRACK=stable-channel
CHROMEOS_RELEASE_UNIBUILD=1
CHROMEOS_RELEASE_VERSION=13729.56.0
DEVICETYPE=CHROMEBOOK
GOOGLE_RELEASE=13729.56.0`

	// This example was taken directly from a production ChromeOS device
	sampleChromeOSUptime = "1234.5 5678.9"
)

func TestTryInterrogatingAndroidDevice_DeviceAttached_Success(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_AdbGetState_Success",
		"Test_FakeExe_ADBUptime_ReturnsPlaceholder",
		"Test_FakeExe_AdbShellGetProp_ReturnsPlaceholder",
		"Test_FakeExe_RawDumpSysBattery_ReturnsPlaceholder",
		"Test_FakeExe_RawDumpSysThermal_ReturnsPlaceholder",
	)

	m := &Machine{adb: adb.New()}
	actual, ok := m.tryInterrogatingAndroidDevice(ctx)
	assert.True(t, ok)
	assert.Equal(t, machine.Android{
		GetProp:               getPropPlaceholder,
		DumpsysBattery:        dumpSysBatteryPlaceholder,
		DumpsysThermalService: dumpSysThermalPlaceholder,
		Uptime:                123 * time.Second,
	}, actual)
}

func TestTryInterrogatingAndroidDevice_UptimeFails_DeviceConsideredNotAttached(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_ExitCodeOne",
	)

	m := &Machine{adb: adb.New()}
	_, ok := m.tryInterrogatingAndroidDevice(ctx)
	assert.False(t, ok)
}

func TestTryInterrogatingAndroidDevice_ThermalFails_PartialSuccess(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_AdbGetState_Success",
		"Test_FakeExe_ADBUptime_ReturnsPlaceholder",
		"Test_FakeExe_AdbShellGetProp_ReturnsPlaceholder",
		"Test_FakeExe_RawDumpSysBattery_ReturnsPlaceholder",
		"Test_FakeExe_ExitCodeOne",
	)

	m := &Machine{adb: adb.New()}
	actual, ok := m.tryInterrogatingAndroidDevice(ctx)
	assert.True(t, ok)
	assert.Equal(t, machine.Android{
		GetProp:        getPropPlaceholder,
		DumpsysBattery: dumpSysBatteryPlaceholder,
		Uptime:         123 * time.Second,
	}, actual)
}

func TestTryInterrogatingChromeOS_DeviceReachable_Success(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_SSHUptime_ReturnsPlaceholder",
		"Test_FakeExe_SSHLSBRelease_ReturnsPlaceholder",
	)
	sshFile := filepath.Join(t.TempDir(), "test.json")
	m := &Machine{
		ssh:                ssh.ExeImpl{},
		sshMachineLocation: sshFile,
		description:        machine.Description{SSHUserIP: testUserIP},
	}
	actual, ok := m.tryInterrogatingChromeOSDevice(ctx)
	assert.True(t, ok)
	assert.Equal(t, machine.ChromeOS{
		Channel:        "stable-channel",
		Milestone:      "89",
		ReleaseVersion: "13729.56.0",
		Uptime:         1234500 * time.Millisecond,
	}, actual)

	require.FileExists(t, sshFile)
	b, err := os.ReadFile(sshFile)
	require.NoError(t, err)
	const expected = `{
  "Comment": "This file is written to by test_machine_monitor. Do not edit by hand.",
  "user_ip": "root@skia-foobar-01"
}
`
	assert.Equal(t, expected, string(b))
}

func TestTryInterrogatingChromeOS_CatLSBReleaseFails_DeviceConsideredUnattached(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_SSHUptime_ReturnsPlaceholder",
		"Test_FakeExe_ExitCodeOne", // pretend LSBRelease failed
	)

	m := &Machine{ssh: ssh.ExeImpl{}, description: machine.Description{SSHUserIP: testUserIP}}
	_, ok := m.tryInterrogatingChromeOSDevice(ctx)
	assert.False(t, ok)
}

func TestTryInterrogatingChromeOS_NoSSHUserIP_ReturnFalse(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext() // Any exe call will panic

	m := &Machine{ssh: ssh.ExeImpl{}}
	_, ok := m.tryInterrogatingChromeOSDevice(ctx)
	assert.False(t, ok)
}

func TestTryInterrogatingChromeOS_UptimeFails_ReturnFalse(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_ExitCodeOne", // pretend uptime fails
	)

	m := &Machine{ssh: ssh.ExeImpl{}, description: machine.Description{SSHUserIP: testUserIP}}
	_, ok := m.tryInterrogatingChromeOSDevice(ctx)
	assert.False(t, ok)
}

func TestTryInterrogatingChromeOS_NoChromeOSData_AssumesNotAttached(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_SSHLSBRelease_ReturnsNonChromeOS",
	)

	m := &Machine{ssh: ssh.ExeImpl{}, description: machine.Description{SSHUserIP: testUserIP}}
	_, ok := m.tryInterrogatingChromeOSDevice(ctx)
	assert.False(t, ok)
}

func TestInterrogate_NoDeviceAttached_Success(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_ExitCodeOne", // No Android device
		"Test_FakeExe_ExitCodeOne", // No iOS device
	)

	m := &Machine{
		adb:              adb.New(),
		ios:              ios.New(),
		MachineID:        "some-machine",
		Version:          "some-version",
		runningTask:      true,
		startSwarming:    true,
		startTime:        time.Date(2021, time.September, 2, 2, 2, 2, 2, time.UTC),
		interrogateTimer: noop.Float64SummaryMetric{},
	}
	actual := m.interrogate(ctx)
	assert.Equal(t, machine.Event{
		EventType:           machine.EventTypeRawState,
		LaunchedSwarming:    true,
		RunningSwarmingTask: true,
		Host: machine.Host{
			Name:      "some-machine",
			Version:   "some-version",
			StartTime: time.Date(2021, time.September, 2, 2, 2, 2, 2, time.UTC),
		},
	}, actual)
}

func TestInterrogate_AndroidDeviceAttached_Success(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_AdbGetState_Success",
		"Test_FakeExe_ADBUptime_ReturnsPlaceholder",
		"Test_FakeExe_AdbShellGetProp_ReturnsPlaceholder",
		"Test_FakeExe_RawDumpSysBattery_ReturnsPlaceholder",
		"Test_FakeExe_RawDumpSysThermal_ReturnsPlaceholder",
	)

	m := &Machine{
		adb:              adb.New(),
		MachineID:        "some-machine",
		Version:          "some-version",
		runningTask:      true,
		startSwarming:    true,
		startTime:        time.Date(2021, time.September, 2, 2, 2, 2, 2, time.UTC),
		interrogateTimer: noop.Float64SummaryMetric{},
	}
	actual := m.interrogate(ctx)
	assert.Equal(t, machine.Event{
		EventType:           machine.EventTypeRawState,
		LaunchedSwarming:    true,
		RunningSwarmingTask: true,
		Host: machine.Host{
			Name:      "some-machine",
			Version:   "some-version",
			StartTime: time.Date(2021, time.September, 2, 2, 2, 2, 2, time.UTC),
		},
		Android: machine.Android{
			GetProp:               getPropPlaceholder,
			DumpsysBattery:        dumpSysBatteryPlaceholder,
			DumpsysThermalService: dumpSysThermalPlaceholder,
			Uptime:                123 * time.Second,
		},
	}, actual)
}

// Just make sure it can get into tryInterrogatingIOSDevice(), and test success while we're at it.
// Tests that call tryInterrogatingIOSDevice() directly cover the other cases.
func TestInterrogate_IOSDeviceAttached_Success(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_ExitCodeOne", // no Android attached
		"Test_FakeExe_IDeviceInfo_ReturnsDeviceType",
		"Test_FakeExe_IDeviceInfo_ReturnsOSVersion",
		"Test_FakeExe_IDeviceInfo_ReturnsGoodBatteryLevel",
	)

	timePlaceholder := time.Date(2021, time.September, 2, 2, 2, 2, 2, time.UTC)
	m := &Machine{
		adb:              adb.New(),
		ios:              ios.New(),
		MachineID:        "some-machine",
		Version:          "some-version",
		runningTask:      true,
		startSwarming:    true,
		startTime:        timePlaceholder,
		interrogateTimer: noop.Float64SummaryMetric{},
	}
	actual := m.interrogate(ctx)
	assert.Equal(t, machine.Event{
		EventType:           machine.EventTypeRawState,
		LaunchedSwarming:    true,
		RunningSwarmingTask: true,
		Host: machine.Host{
			Name:      "some-machine",
			Version:   "some-version",
			StartTime: timePlaceholder,
		},
		IOS: machine.IOS{
			OSVersion:  iOSVersionPlaceholder,
			DeviceType: iOSDeviceTypePlaceholder,
			Battery:    33,
		},
	}, actual)
}

func TestTryInterrogatingIOSDevice_OSVersionAndBatteryFail_StillSucceeds(t *testing.T) {
	unittest.SmallTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_IDeviceInfo_ReturnsDeviceType",
		"Test_FakeExe_ExitCodeOne", // OS version check goes kaboom.
		"Test_FakeExe_ExitCodeOne", // battery level too
	)
	m := &Machine{ios: ios.New()}
	actual, deviceIsAttached := m.tryInterrogatingIOSDevice(ctx)
	assert.True(t, deviceIsAttached)
	assert.Equal(t, machine.IOS{
		OSVersion:  "",
		DeviceType: iOSDeviceTypePlaceholder,
		Battery:    machine.BadBatteryLevel,
	}, actual)
}

func TestTryInterrogatingIOSDevice_DeviceTypeFails_DeviceConsideredUnattached(t *testing.T) {
	unittest.SmallTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_ExitCodeOne", // Device-type check fails.
	)
	m := &Machine{ios: ios.New()}
	_, deviceIsAttached := m.tryInterrogatingIOSDevice(ctx)
	assert.False(t, deviceIsAttached)
}

func TestInterrogate_ChromeOSDeviceAttached_Success(t *testing.T) {
	unittest.MediumTest(t)
	ctx := executil.FakeTestsContext(
		"Test_FakeExe_SSHUptime_ReturnsPlaceholder",
		"Test_FakeExe_SSHLSBRelease_ReturnsPlaceholder",
		// We found a device, no need to check for adb
	)
	sshFile := filepath.Join(t.TempDir(), "test.json")
	m := &Machine{
		ssh:                ssh.ExeImpl{},
		sshMachineLocation: sshFile,
		MachineID:          "some-machine",
		description: machine.Description{
			SSHUserIP: testUserIP,
		},
		Version:          "some-version",
		runningTask:      true,
		startSwarming:    true,
		startTime:        time.Date(2021, time.September, 2, 2, 2, 2, 2, time.UTC),
		interrogateTimer: noop.Float64SummaryMetric{},
	}
	actual := m.interrogate(ctx)
	assert.Equal(t, machine.Event{
		EventType:           machine.EventTypeRawState,
		LaunchedSwarming:    true,
		RunningSwarmingTask: true,
		Host: machine.Host{
			Name:      "some-machine",
			Version:   "some-version",
			StartTime: time.Date(2021, time.September, 2, 2, 2, 2, 2, time.UTC),
		},
		ChromeOS: machine.ChromeOS{
			Channel:        "stable-channel",
			Milestone:      "89",
			ReleaseVersion: "13729.56.0",
			Uptime:         1234500 * time.Millisecond,
		},
	}, actual)

	require.FileExists(t, sshFile)
	b, err := os.ReadFile(sshFile)
	require.NoError(t, err)
	const expected = `{
  "Comment": "This file is written to by test_machine_monitor. Do not edit by hand.",
  "user_ip": "root@skia-foobar-01"
}
`
	assert.Equal(t, expected, string(b))
}

func TestRebootDevice_AndroidDeviceAttached_Success(t *testing.T) {
	unittest.MediumTest(t)

	ctx := executil.FakeTestsContext(
		"Test_FakeExe_AdbReboot_Success",
	)

	m := &Machine{
		adb: adb.New(),
		description: machine.Description{
			Dimensions: machine.SwarmingDimensions{
				machine.DimAndroidDevices: []string{"sprout"},
			},
		},
	}

	require.NoError(t, m.RebootDevice(ctx))
	assert.Equal(t, 1, executil.FakeCommandsReturned(ctx))
}

func TestRebootDevice_AndroidDeviceAttached_ErrOnNonZeroExitCode(t *testing.T) {
	unittest.MediumTest(t)

	ctx := executil.FakeTestsContext(
		"Test_FakeExe_Reboot_NonZeroExitCode",
	)

	m := &Machine{
		adb: adb.New(),
		description: machine.Description{
			Dimensions: machine.SwarmingDimensions{
				machine.DimAndroidDevices: []string{"sprout"},
			},
		},
	}

	require.Error(t, m.RebootDevice(ctx))
	assert.Equal(t, 1, executil.FakeCommandsReturned(ctx))
}

func TestRebootDevice_NoErrorIfNoDevicesAttached(t *testing.T) {
	unittest.MediumTest(t)

	ctx := executil.FakeTestsContext() // Any exe call will panic

	m := &Machine{
		description: machine.Description{},
	}

	require.NoError(t, m.RebootDevice(ctx))
}

func TestRebootDevice_IOSDeviceAttached_Success(t *testing.T) {
	unittest.SmallTest(t)

	ctx := executil.FakeTestsContext("Test_FakeExe_IDeviceDiagnosticsReboot_Success")

	m := &Machine{
		ios: ios.New(),
		description: machine.Description{
			Dimensions: machine.SwarmingDimensions{
				machine.DimOS: []string{"iOS", "iOS-1.2.3"},
			},
		},
	}

	require.NoError(t, m.RebootDevice(ctx))
	assert.Equal(t, 1, executil.FakeCommandsReturned(ctx)) // Make sure it was really called.
}

func TestRebootDevice_ChromeOSDeviceAttached_Success(t *testing.T) {
	unittest.MediumTest(t)

	ctx := executil.FakeTestsContext(
		"Test_FakeExe_SSHReboot_Success",
	)

	m := &Machine{
		ssh: ssh.ExeImpl{},
		description: machine.Description{
			SSHUserIP: testUserIP,
		},
	}

	require.NoError(t, m.RebootDevice(ctx))
	assert.Equal(t, 1, executil.FakeCommandsReturned(ctx))
}

func TestRebootDevice_ChromeOSDeviceAttached_ErrOnNonZeroExitCode(t *testing.T) {
	unittest.MediumTest(t)

	ctx := executil.FakeTestsContext(
		"Test_FakeExe_ExitCodeOne",
	)

	m := &Machine{
		ssh: ssh.ExeImpl{},
		description: machine.Description{
			SSHUserIP: testUserIP,
		},
	}

	require.Error(t, m.RebootDevice(ctx))
	assert.Equal(t, 1, executil.FakeCommandsReturned(ctx))
}

func Test_FakeExe_ADBUptime_ReturnsPlaceholder(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}
	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Equal(t, []string{"adb", "shell", "cat", "/proc/uptime"}, args)

	fmt.Print(adbUptimePlaceholder)
	os.Exit(0)
}

func Test_FakeExe_AdbShellGetProp_ReturnsPlaceholder(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Equal(t, []string{"adb", "shell", "getprop"}, args)

	fmt.Print(getPropPlaceholder)
	os.Exit(0)
}

func Test_FakeExe_RawDumpSysBattery_ReturnsPlaceholder(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Equal(t, []string{"adb", "shell", "dumpsys", "battery"}, args)

	fmt.Print(dumpSysBatteryPlaceholder)
	os.Exit(0)
}

func Test_FakeExe_RawDumpSysThermal_ReturnsPlaceholder(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}
	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Equal(t, []string{"adb", "shell", "dumpsys", "thermalservice"}, args)

	fmt.Print(dumpSysThermalPlaceholder)
	os.Exit(0)
}

func Test_FakeExe_IDeviceInfo_ReturnsDeviceType(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}
	require.Equal(t, []string{"ideviceinfo", "-k", "ProductType"}, executil.OriginalArgs())

	fmt.Fprintln(os.Stderr, iOSDeviceTypePlaceholder)
	os.Exit(0)
}

func Test_FakeExe_IDeviceInfo_ReturnsOSVersion(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}
	require.Equal(t, []string{"ideviceinfo", "-k", "ProductVersion"}, executil.OriginalArgs())

	fmt.Fprintln(os.Stderr, iOSVersionPlaceholder)
	os.Exit(0)
}

func Test_FakeExe_IDeviceInfo_ReturnsGoodBatteryLevel(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}
	require.Equal(t, []string{"ideviceinfo", "--domain", "com.apple.mobile.battery", "-k", "BatteryCurrentCapacity"}, executil.OriginalArgs())

	fmt.Fprintln(os.Stderr, "33")
	os.Exit(0)
}

func Test_FakeExe_IDeviceDiagnosticsReboot_Success(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}
	require.Equal(t, []string{"idevicediagnostics", "restart"}, executil.OriginalArgs())
	os.Exit(0)
}

func Test_FakeExe_SSHLSBRelease_ReturnsPlaceholder(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}
	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Contains(t, args, "ssh")
	require.Contains(t, args, testUserIP)
	require.Contains(t, args, "/etc/lsb-release")

	fmt.Print(sampleChromeOSlsbrelease)
	os.Exit(0)
}

func Test_FakeExe_SSHLSBRelease_ReturnsNonChromeOS(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}
	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Contains(t, args, "ssh")
	require.Contains(t, args, testUserIP)
	require.Contains(t, args, "/etc/lsb-release")

	fmt.Print("FOO=bar")
	os.Exit(0)
}

func Test_FakeExe_SSHUptime_ReturnsPlaceholder(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}
	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Contains(t, args, "ssh")
	require.Contains(t, args, testUserIP)
	require.Contains(t, args, "/proc/uptime")

	fmt.Print(sampleChromeOSUptime)
	os.Exit(0)
}

func Test_FakeExe_ExitCodeOne(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	os.Exit(1)
}

func Test_FakeExe_AdbGetState_Success(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Equal(t, []string{"adb", "get-state"}, args)
	_, _ = fmt.Fprintf(os.Stderr, "device")

	// Force exit so we don't get PASS in the output.
	os.Exit(0)
}

func Test_FakeExe_AdbReboot_Success(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Equal(t, []string{"adb", "reboot"}, args)

	// Force exit so we don't get PASS in the output.
	os.Exit(0)
}

func Test_FakeExe_Reboot_NonZeroExitCode(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	_, _ = fmt.Fprintf(os.Stderr, "error: no devices/emulators found")

	os.Exit(127)
}

func Test_FakeExe_SSHReboot_Success(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}

	// Check the input arguments to make sure they were as expected.
	args := executil.OriginalArgs()
	require.Contains(t, args, "ssh")
	require.Contains(t, args, testUserIP)
	require.Contains(t, args, "reboot")

	// Force exit so we don't get PASS in the output.
	os.Exit(0)
}

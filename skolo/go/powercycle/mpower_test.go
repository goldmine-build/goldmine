package powercycle

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.skia.org/infra/go/executil"
	"go.skia.org/infra/go/testutils/unittest"
)

func TestMPowerClient_PowerCycle_Success(t *testing.T) {
	unittest.SmallTest(t)
	mp, err := newMPowerController(context.Background(), mpowerConfig(), false)
	require.NoError(t, err)

	ctx := executil.FakeTestsContext(
		"Test_FakeExe_MPowerSSHDisablePort7_Success", // We expect to see the port disabled...
		"Test_FakeExe_MPowerSSHEnablePort7_Success",  // then re-enabled.
	)
	err = mp.PowerCycle(ctx, testDeviceOne, time.Millisecond)
	assert.NoError(t, err)
	assert.Equal(t, 2, executil.FakeCommandsReturned(ctx))
}

// This is a fake executable used to assert that a correct call to disable port 7 of the mpower
// switch was made. It is invoked using executil.FakeTestsContext.
func Test_FakeExe_MPowerSSHDisablePort7_Success(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}
	args := executil.OriginalArgs()
	require.Equal(t, []string{"ssh", "-oKexAlgorithms=+diffie-hellman-group1-sha1", "-T", "ubnt@192.168.1.117"}, args)

	// We expect the command to be sent over standard in once the ssh connection is established.
	input, err := ioutil.ReadAll(os.Stdin)
	require.NoError(t, err)

	assert.Equal(t, "echo 0 > /proc/power/relay7\n", string(input))
}

// This is a fake executable used to assert that a correct call to enable port 7 of the mpower
// switch was made. It is invoked using executil.FakeTestsContext.
func Test_FakeExe_MPowerSSHEnablePort7_Success(t *testing.T) {
	unittest.FakeExeTest(t)
	if os.Getenv(executil.OverrideEnvironmentVariable) == "" {
		return
	}
	args := executil.OriginalArgs()
	require.Equal(t, []string{"ssh", "-oKexAlgorithms=+diffie-hellman-group1-sha1", "-T", "ubnt@192.168.1.117"}, args)

	// We expect the command to be sent over standard in once the ssh connection is established.
	input, err := ioutil.ReadAll(os.Stdin)
	require.NoError(t, err)

	assert.Equal(t, "echo 1 > /proc/power/relay7\n", string(input))
}

const (
	testDeviceOne = "skia-rpi-001-device"
	testDeviceTwo = "skia-rpi-002-device"
)

func mpowerConfig() *mPowerConfig {
	return &mPowerConfig{
		Address: "192.168.1.117",
		User:    "ubnt",
		DevPortMap: map[DeviceID]int{
			testDeviceOne: 7,
			testDeviceTwo: 8,
		},
	}
}

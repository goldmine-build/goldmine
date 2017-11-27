package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/testutils"
)

func mockGetCurrentHash() string {
	return "aabbccdd"
}

// execString is the command line that would have been run through exec.
var execString string

// testRun is a 'exec.Run' function to use for testing.
func testRun(cmd *exec.Command) error {
	execString = exec.DebugString(cmd)
	return nil
}

func TestRunContainer(t *testing.T) {
	testutils.SmallTest(t)
	// Now test local runs, first set up exec for testing.
	ctx := exec.NewContext(context.Background(), testRun)
	runner := New("/mnt/pd0/debugger", "/mnt/pd0/container", mockGetCurrentHash, false)
	err := runner.Start(ctx, 20003)
	assert.NoError(t, err)
	assert.NotNil(t, runner)
	assert.Equal(t, "sudo systemd-nspawn -D /mnt/pd0/container --read-only --machine debug20003 --bind-ro /mnt/pd0/debugger xargs --arg-file=/dev/null /mnt/pd0/debugger/versions/aabbccdd/skia/out/Release/skiaserve --port 20003 --hosted", execString)
}

func TestRunLocal(t *testing.T) {
	testutils.SmallTest(t)
	// Now test local runs, first set up exec for testing.
	ctx := exec.NewContext(context.Background(), testRun)
	runner := New("/mnt/pd0/debugger", "/mnt/pd0/container", mockGetCurrentHash, true)
	err := runner.Start(ctx, 20003)
	assert.NoError(t, err)
	assert.NotNil(t, runner)
	assert.Equal(t, "/mnt/pd0/debugger/versions/aabbccdd/skia/out/Release/skiaserve --port 20003 --source  --hosted", execString)
}

package cleanup

import (
	"testing"
	"time"

	assert "github.com/stretchr/testify/require"
	"go.skia.org/infra/go/testutils"
)

func TestCleanup(t *testing.T) {
	testutils.MediumTest(t)

	interval := 200 * time.Millisecond

	// Verify that both the tick and cleanup functions get called as
	// expected.
	count := 0
	cleanup := false
	Repeat(interval, func() {
		count++
		assert.False(t, cleanup)
	}, func() {
		assert.False(t, cleanup)
		cleanup = true
	})
	time.Sleep(10 * interval)
	Cleanup()
	assert.True(t, count >= 4)
	assert.True(t, cleanup)

	// Multiple registered funcs.
	reset()

	n := 5
	counts := make([]int, 0, n)
	cleanups := make([]bool, 0, n)
	for i := 0; i < n; i++ {
		counts = append(counts, 0)
		cleanups = append(cleanups, false)
	}
	for i := 0; i < n; i++ {
		idx := i
		Repeat(interval, func() {
			counts[idx]++
			assert.False(t, cleanups[idx])
		}, func() {
			assert.False(t, cleanups[idx])
			cleanups[idx] = true
		})
	}
	time.Sleep(10 * interval)
	Cleanup()
	for i := 0; i < n; i++ {
		assert.True(t, counts[i] >= 4)
		assert.True(t, cleanups[i])
	}
}

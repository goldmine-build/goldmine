package local

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.skia.org/infra/go/testutils/unittest"
)

func TestCache_New_Failure(t *testing.T) {
	unittest.SmallTest(t)
	_, err := New(-12)
	require.Error(t, err)
}

func TestCache_Exists_Success(t *testing.T) {
	unittest.SmallTest(t)
	c, err := New(12)
	require.NoError(t, err)

	c.Add("foo")
	ok := c.Exists("foo")
	assert.True(t, ok)
}

func TestCache_Exists_FalseOnMiss(t *testing.T) {
	unittest.SmallTest(t)
	c, err := New(12)
	require.NoError(t, err)

	ok := c.Exists("foo")
	assert.False(t, ok)
}

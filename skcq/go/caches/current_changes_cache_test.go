package caches

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"go.skia.org/infra/go/now"
	"go.skia.org/infra/go/testutils/unittest"
	db_mocks "go.skia.org/infra/skcq/go/db/mocks"
	"go.skia.org/infra/skcq/go/types"
)

func TestCurrentChangesCache(t *testing.T) {
	unittest.SmallTest(t)

	ctx := context.Background()
	cacheMap := map[string]*types.CurrentlyProcessingChange{}
	unixTime := int64(1598467386)
	testTime := time.Unix(unixTime, 0).UTC()
	changeEquivalentPatchset := "test/change"

	// Mock time now.
	ctx = context.WithValue(ctx, now.ContextKey, testTime)

	// Mock db.
	dbClient := &db_mocks.DB{}
	dbClient.On("GetCurrentChanges", ctx).Return(cacheMap, nil).Once()
	dbClient.On("PutCurrentChanges", ctx, cacheMap).Return(nil).Twice()

	// Test GetCurrentChangesCache.
	ccCache, err := GetCurrentChangesCache(ctx, dbClient)
	require.NoError(t, err)
	require.NotNil(t, ccCache)
	require.Len(t, ccCache.Get(), 0)

	// Test Add.
	startTime, newEntry, err := ccCache.Add(ctx, changeEquivalentPatchset, "subject", "owner", "repo", "branch", false, false, int64(123), int64(5))
	require.NoError(t, err)
	require.Equal(t, unixTime, startTime)
	require.True(t, newEntry)
	require.Len(t, ccCache.Get(), 1)

	// Test Remove.
	err = ccCache.Remove(ctx, changeEquivalentPatchset)
	require.NoError(t, err)
	require.Len(t, ccCache.Get(), 0)
}

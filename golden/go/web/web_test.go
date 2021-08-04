package web

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	lru "github.com/hashicorp/golang-lru"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"go.skia.org/infra/go/httputils"
	"go.skia.org/infra/go/now"
	"go.skia.org/infra/go/paramtools"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/testutils/unittest"
	"go.skia.org/infra/golden/go/blame"
	"go.skia.org/infra/golden/go/clstore"
	mock_clstore "go.skia.org/infra/golden/go/clstore/mocks"
	"go.skia.org/infra/golden/go/code_review"
	mock_crs "go.skia.org/infra/golden/go/code_review/mocks"
	ci "go.skia.org/infra/golden/go/continuous_integration"
	"go.skia.org/infra/golden/go/digest_counter"
	"go.skia.org/infra/golden/go/expectations"
	mock_expectations "go.skia.org/infra/golden/go/expectations/mocks"
	"go.skia.org/infra/golden/go/ignore"
	mock_ignore "go.skia.org/infra/golden/go/ignore/mocks"
	"go.skia.org/infra/golden/go/ignore/sqlignorestore"
	"go.skia.org/infra/golden/go/image/text"
	"go.skia.org/infra/golden/go/indexer"
	mock_indexer "go.skia.org/infra/golden/go/indexer/mocks"
	"go.skia.org/infra/golden/go/mocks"
	"go.skia.org/infra/golden/go/paramsets"
	mock_search "go.skia.org/infra/golden/go/search/mocks"
	"go.skia.org/infra/golden/go/search2"
	mock_search2 "go.skia.org/infra/golden/go/search2/mocks"
	"go.skia.org/infra/golden/go/sql"
	dks "go.skia.org/infra/golden/go/sql/datakitchensink"
	"go.skia.org/infra/golden/go/sql/schema"
	"go.skia.org/infra/golden/go/sql/sqltest"
	bug_revert "go.skia.org/infra/golden/go/testutils/data_bug_revert"
	one_by_five "go.skia.org/infra/golden/go/testutils/data_one_by_five"
	data "go.skia.org/infra/golden/go/testutils/data_three_devices"
	"go.skia.org/infra/golden/go/tiling"
	"go.skia.org/infra/golden/go/tjstore"
	mock_tjstore "go.skia.org/infra/golden/go/tjstore/mocks"
	"go.skia.org/infra/golden/go/types"
	"go.skia.org/infra/golden/go/web/frontend"
)

func TestStubbedAuthAs_OverridesLoginLogicWithHardCodedEmail(t *testing.T) {
	unittest.SmallTest(t)
	r := httptest.NewRequest(http.MethodGet, "/does/not/matter", nil)
	wh := Handlers{}
	assert.Equal(t, "", wh.loggedInAs(r))

	const fakeUser = "user@example.com"
	wh.testingAuthAs = fakeUser
	assert.Equal(t, fakeUser, wh.loggedInAs(r))
}

// TestNewHandlers_BaselineSubset_HasAllPieces_Success makes sure we can create a web.Handlers
// using the BaselineSubset of inputs.
func TestNewHandlers_BaselineSubset_HasAllPieces_Success(t *testing.T) {
	unittest.SmallTest(t)

	hc := HandlersConfig{
		GCSClient: &mocks.GCSClient{},
		DB:        &pgxpool.Pool{},
		ReviewSystems: []clstore.ReviewSystem{
			{
				ID:     "whatever",
				Store:  &mock_clstore.Store{},
				Client: &mock_crs.Client{},
			},
		},
	}
	_, err := NewHandlers(hc, BaselineSubset)
	require.NoError(t, err)
}

// TestNewHandlers_BaselineSubset_MissingPieces_Failure makes sure that if we omit values from
// HandlersConfig, NewHandlers returns an error.
func TestNewHandlers_BaselineSubset_MissingPieces_Failure(t *testing.T) {
	unittest.SmallTest(t)

	hc := HandlersConfig{}
	_, err := NewHandlers(hc, BaselineSubset)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be nil")

	hc = HandlersConfig{
		DB:        &pgxpool.Pool{},
		GCSClient: &mocks.GCSClient{},
	}
	_, err = NewHandlers(hc, BaselineSubset)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be empty")
}

// TestNewHandlers_FullFront_EndMissingPieces_Failure makes sure that if we omit values from
// HandlersConfig, NewHandlers returns an error.
// TODO(kjlubick) Add a case for FullFrontEnd with all pieces when we have mocks for all
//   remaining services.
func TestNewHandlers_FullFrontEnd_MissingPieces_Failure(t *testing.T) {
	unittest.SmallTest(t)

	hc := HandlersConfig{}
	_, err := NewHandlers(hc, FullFrontEnd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be nil")

	hc = HandlersConfig{
		GCSClient: &mocks.GCSClient{},
		DB:        &pgxpool.Pool{},
		ReviewSystems: []clstore.ReviewSystem{
			{
				ID:     "whatever",
				Store:  &mock_clstore.Store{},
				Client: &mock_crs.Client{},
			},
		},
	}
	_, err = NewHandlers(hc, FullFrontEnd)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be nil")
}

// TestComputeByBlame_OneUntriagedDigest_Success calculates the "byBlameEntries" for the
// entire BugRevert test data corpus, which has one seen untriaged digest. A byBlameEntry ("blame"
// or "blames" for short) points out which commits introduced untriaged digests.
func TestComputeByBlame_OneUntriagedDigest_Success(t *testing.T) {
	unittest.SmallTest(t)

	mi := &mock_indexer.IndexSource{}
	defer mi.AssertExpectations(t)

	commits := bug_revert.MakeTestCommits()
	// Go all the way to the end (bug_revert has 5 commits in it), which has cleared up all
	// untriaged digests except for FoxtrotUntriagedDigest
	fis := makeBugRevertIndex(len(commits))
	mi.On("GetIndex").Return(fis)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mi,
		},
	}

	output, err := wh.computeByBlame(context.Background(), "gm")
	require.NoError(t, err)

	assert.Equal(t, []frontend.ByBlameEntry{
		{
			GroupID:  bug_revert.ThirdCommitHash,
			NDigests: 1,
			NTests:   1,
			Commits:  []frontend.Commit{frontend.FromTilingCommit(commits[2])},
			AffectedTests: []frontend.TestRollup{
				{
					Test:         bug_revert.TestTwo,
					Num:          1,
					SampleDigest: bug_revert.FoxtrotUntriagedDigest,
				},
			},
		},
	}, output)
}

// TestComputeByBlame_MultipleUntriagedDigests_Success calculates the "byBlameEntries" for a
// truncated version of the bug_revert test data corpus.  This subset was chosen to have several
// untriaged digests that are easy to manually compute blames for to verify.
func TestComputeByBlame_MultipleUntriagedDigests_Success(t *testing.T) {
	unittest.SmallTest(t)

	mi := &mock_indexer.IndexSource{}
	defer mi.AssertExpectations(t)

	// We stop just before the "revert" in the fake data set, so it appears there are more untriaged
	// digests going on.
	fis := makeBugRevertIndex(bug_revert.RevertBugCommitIndex - 1)
	mi.On("GetIndex").Return(fis)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mi,
		},
	}

	output, err := wh.computeByBlame(context.Background(), "gm")
	require.NoError(t, err)

	commits := bug_revert.MakeTestCommits()
	assert.Equal(t, []frontend.ByBlameEntry{
		{
			GroupID:  bug_revert.SecondCommitHash,
			NDigests: 2,
			NTests:   2,
			Commits:  []frontend.Commit{frontend.FromTilingCommit(commits[1])},
			AffectedTests: []frontend.TestRollup{
				{
					Test:         bug_revert.TestOne,
					Num:          1,
					SampleDigest: bug_revert.BravoUntriagedDigest,
				},
				{
					Test:         bug_revert.TestTwo,
					Num:          1,
					SampleDigest: bug_revert.DeltaUntriagedDigest,
				},
			},
		},
		{
			GroupID:  bug_revert.ThirdCommitHash,
			NDigests: 1,
			NTests:   1,
			Commits:  []frontend.Commit{frontend.FromTilingCommit(commits[2])},
			AffectedTests: []frontend.TestRollup{
				{
					Test:         bug_revert.TestTwo,
					Num:          1,
					SampleDigest: bug_revert.FoxtrotUntriagedDigest,
				},
			},
		},
	}, output)
}

// makeBugRevertIndex returns a search index corresponding to a subset of the bug_revert_data
// (which currently has nothing ignored). We choose to use this instead of mocking
// out the SearchIndex, as per the advice in http://go/mocks#prefer-real-objects
// of "prefer to use real objects if possible". We have tests that verify these
// real objects work correctly, so we should feel safe to use them here.
func makeBugRevertIndex(endIndex int) *indexer.SearchIndex {
	tile := bug_revert.MakeTestTile()

	// Trim down the traces to end sooner (to make the data "more interesting")
	for _, trace := range tile.Traces {
		trace.Digests = trace.Digests[:endIndex]
	}
	tile.Commits = tile.Commits[:endIndex]

	cpxTile := tiling.NewComplexTile(tile)
	dc := digest_counter.New(tile)
	ps := paramsets.NewParamSummary(tile, dc)
	exp := &mock_expectations.Store{}
	exp.On("Get", testutils.AnyContext).Return(bug_revert.MakeTestExpectations(), nil).Maybe()

	b, err := blame.New(cpxTile.GetTile(types.ExcludeIgnoredTraces), bug_revert.MakeTestExpectations())
	if err != nil {
		panic(err) // this means our static data is horribly broken
	}

	si, err := indexer.SearchIndexForTesting(cpxTile, [2]digest_counter.DigestCounter{dc, dc}, [2]paramsets.ParamSummary{ps, ps}, exp, b)
	if err != nil {
		panic(err) // this means our static data is horribly broken
	}
	return si
}

// makeBugRevertIndex returns a search index corresponding to the bug_revert_data
// with the given ignores. Like makeBugRevertIndex, we return a real SearchIndex.
// If multiplier is > 1, duplicate traces will be added to the tile to make it artificially
// bigger.
func makeBugRevertIndexWithIgnores(ir []ignore.Rule, multiplier int) *indexer.SearchIndex {
	tile := bug_revert.MakeTestTile()
	add := make([]tiling.TracePair, 0, multiplier*len(tile.Traces))
	for i := 1; i < multiplier; i++ {
		for id, tr := range tile.Traces {
			newID := tiling.TraceID(fmt.Sprintf("%s,copy=%d", id, i))
			add = append(add, tiling.TracePair{ID: newID, Trace: tr})
		}
	}
	for _, tp := range add {
		tile.Traces[tp.ID] = tp.Trace
	}
	cpxTile := tiling.NewComplexTile(tile)

	subtile, combinedRules, err := ignore.FilterIgnored(tile, ir)
	if err != nil {
		panic(err) // this means our static data is horribly broken
	}
	cpxTile.SetIgnoreRules(subtile, combinedRules)
	dcInclude := digest_counter.New(tile)
	dcExclude := digest_counter.New(subtile)
	psInclude := paramsets.NewParamSummary(tile, dcInclude)
	psExclude := paramsets.NewParamSummary(subtile, dcExclude)
	exp := &mock_expectations.Store{}
	exp.On("Get", testutils.AnyContext).Return(bug_revert.MakeTestExpectations(), nil).Maybe()

	b, err := blame.New(cpxTile.GetTile(types.ExcludeIgnoredTraces), bug_revert.MakeTestExpectations())
	if err != nil {
		panic(err) // this means our static data is horribly broken
	}

	si, err := indexer.SearchIndexForTesting(cpxTile,
		[2]digest_counter.DigestCounter{dcExclude, dcInclude},
		[2]paramsets.ParamSummary{psExclude, psInclude}, exp, b)
	if err != nil {
		panic(err) // this means our static data is horribly broken
	}
	return si
}

// TestGetIngestedChangelists_AllChangelists_SunnyDay_Success tests the core functionality of
// listing all Changelists that have Gold results.
func TestGetIngestedChangelists_AllChangelists_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)

	mcls := &mock_clstore.Store{}
	defer mcls.AssertExpectations(t)

	const offset = 0
	const size = 50

	mcls.On("GetChangelists", testutils.AnyContext, clstore.SearchOptions{
		StartIdx: offset,
		Limit:    size,
	}).Return(makeCodeReviewCLs(), len(makeCodeReviewCLs()), nil)

	wh := Handlers{
		anonymousExpensiveQuota: rate.NewLimiter(rate.Inf, 1),
		HandlersConfig: HandlersConfig{
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID:          "gerrit",
					Store:       mcls,
					URLTemplate: "example.com/cl/%s#templates",
				},
			},
		},
	}

	cls := makeWebCLs()

	expectedResponse := frontend.ChangelistsResponse{
		Changelists: cls,
		ResponsePagination: httputils.ResponsePagination{
			Offset: offset,
			Size:   size,
			Total:  len(cls),
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v1/changelist?size=50", nil)
	wh.ChangelistsHandler(w, r)
	b, err := json.Marshal(expectedResponse)
	require.NoError(t, err)
	assertJSONResponseWas(t, http.StatusOK, string(b), w)
}

// TestGetIngestedChangelists_ActiveChangelists_SunnyDay_Success makes sure that we properly get
// only active Changelists, that is, Changelists which are open.
func TestGetIngestedChangelists_ActiveChangelists_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)

	mcls := &mock_clstore.Store{}
	defer mcls.AssertExpectations(t)

	const offset = 20
	const size = 30

	mcls.On("GetChangelists", testutils.AnyContext, clstore.SearchOptions{
		StartIdx:    offset,
		Limit:       size,
		OpenCLsOnly: true,
	}).Return(makeCodeReviewCLs(), 3, nil)

	wh := Handlers{
		anonymousExpensiveQuota: rate.NewLimiter(rate.Inf, 1),
		HandlersConfig: HandlersConfig{
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID:          "gerrit",
					Store:       mcls,
					URLTemplate: "example.com/cl/%s#templates",
				},
			},
		},
	}

	cls := makeWebCLs()

	expectedResponse := frontend.ChangelistsResponse{
		Changelists: cls,
		ResponsePagination: httputils.ResponsePagination{
			Offset: offset,
			Size:   size,
			Total:  len(cls),
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v1/changelist?offset=20&size=30&active=true", nil)
	wh.ChangelistsHandler(w, r)
	b, err := json.Marshal(expectedResponse)
	require.NoError(t, err)
	assertJSONResponseWas(t, http.StatusOK, string(b), w)
}

func makeCodeReviewCLs() []code_review.Changelist {
	return []code_review.Changelist{
		{
			SystemID: "1002",
			Owner:    "other@example.com",
			Status:   code_review.Open,
			Subject:  "new feature",
			Updated:  time.Date(2019, time.August, 27, 0, 0, 0, 0, time.UTC),
		},
		{
			SystemID: "1001",
			Owner:    "test@example.com",
			Status:   code_review.Landed,
			Subject:  "land gold",
			Updated:  time.Date(2019, time.August, 26, 0, 0, 0, 0, time.UTC),
		},
		{
			SystemID: "1000",
			Owner:    "test@example.com",
			Status:   code_review.Abandoned,
			Subject:  "gold experiment",
			Updated:  time.Date(2019, time.August, 25, 0, 0, 0, 0, time.UTC),
		},
	}
}

func makeWebCLs() []frontend.Changelist {
	return []frontend.Changelist{
		{
			System:   "gerrit",
			SystemID: "1002",
			Owner:    "other@example.com",
			Status:   "Open",
			Subject:  "new feature",
			Updated:  time.Date(2019, time.August, 27, 0, 0, 0, 0, time.UTC),
			URL:      "example.com/cl/1002#templates",
		},
		{
			System:   "gerrit",
			SystemID: "1001",
			Owner:    "test@example.com",
			Status:   "Landed",
			Subject:  "land gold",
			Updated:  time.Date(2019, time.August, 26, 0, 0, 0, 0, time.UTC),
			URL:      "example.com/cl/1001#templates",
		},
		{
			System:   "gerrit",
			SystemID: "1000",
			Owner:    "test@example.com",
			Status:   "Abandoned",
			Subject:  "gold experiment",
			Updated:  time.Date(2019, time.August, 25, 0, 0, 0, 0, time.UTC),
			URL:      "example.com/cl/1000#templates",
		},
	}
}

// TestGetClSummary_SunnyDay_Success represents a case where we have a CL that has 2 patchsets with
// data, PS with order 1, ps with order 4.
func TestGetClSummary_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)

	const expectedCLID = "1002"
	const gerritCRS = "gerrit"

	mcls := &mock_clstore.Store{}
	mtjs := &mock_tjstore.Store{}

	mcls.On("GetChangelist", testutils.AnyContext, expectedCLID).Return(makeCodeReviewCLs()[0], nil)
	mcls.On("GetPatchsets", testutils.AnyContext, expectedCLID).Return(makeCodeReviewPSs(), nil)
	mcls.On("System").Return("gerrit")

	psID := tjstore.CombinedPSID{
		CL:  expectedCLID,
		CRS: gerritCRS,
		PS:  "ps-1",
	}
	tj1 := []ci.TryJob{
		{
			SystemID:    "bb1",
			System:      "buildbucket",
			DisplayName: "Test-Build",
			Updated:     time.Date(2019, time.August, 27, 1, 0, 0, 0, time.UTC),
		},
	}
	mtjs.On("GetTryJobs", testutils.AnyContext, psID).Return(tj1, nil)

	psID = tjstore.CombinedPSID{
		CL:  expectedCLID,
		CRS: gerritCRS,
		PS:  "ps-4",
	}
	tj2 := []ci.TryJob{
		{
			SystemID:    "cirrus-7",
			System:      "cirrus",
			DisplayName: "Test-Build",
			Updated:     time.Date(2019, time.August, 27, 0, 15, 0, 0, time.UTC),
		},
		{
			SystemID:    "bb3",
			System:      "buildbucket",
			DisplayName: "Test-Code",
			Updated:     time.Date(2019, time.August, 27, 0, 20, 0, 0, time.UTC),
		},
	}
	mtjs.On("GetTryJobs", testutils.AnyContext, psID).Return(tj2, nil)

	gerritSystem := clstore.ReviewSystem{
		ID:          gerritCRS,
		Store:       mcls,
		URLTemplate: "example.com/cl/%s#templates",
	}

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			ReviewSystems: []clstore.ReviewSystem{gerritSystem},
			TryJobStore:   mtjs,
		},
	}

	cl, err := wh.getCLSummary(context.Background(), gerritSystem, expectedCLID)
	assert.NoError(t, err)
	assert.Equal(t, frontend.ChangelistSummary{
		CL:                makeWebCLs()[0], // matches expectedCLID
		NumTotalPatchsets: 4,
		Patchsets: []frontend.Patchset{
			{
				SystemID: "ps-1",
				Order:    1,
				TryJobs: []frontend.TryJob{
					{
						System:      "buildbucket",
						SystemID:    "bb1",
						DisplayName: "Test-Build",
						Updated:     time.Date(2019, time.August, 27, 1, 0, 0, 0, time.UTC),
						URL:         "https://cr-buildbucket.appspot.com/build/bb1",
					},
				},
			},
			{
				SystemID: "ps-4",
				Order:    4,
				TryJobs: []frontend.TryJob{
					{
						System:      "cirrus",
						SystemID:    "cirrus-7",
						DisplayName: "Test-Build",
						Updated:     time.Date(2019, time.August, 27, 0, 15, 0, 0, time.UTC),
						URL:         "https://cirrus-ci.com/task/cirrus-7",
					},
					{
						System:      "buildbucket",
						SystemID:    "bb3",
						DisplayName: "Test-Code",
						Updated:     time.Date(2019, time.August, 27, 0, 20, 0, 0, time.UTC),
						URL:         "https://cr-buildbucket.appspot.com/build/bb3",
					},
				},
			},
		},
	}, cl)
}

func makeCodeReviewPSs() []code_review.Patchset {
	// This data is arbitrary
	return []code_review.Patchset{
		{
			SystemID:     "ps-1",
			ChangelistID: "1002",
			Order:        1,
			GitHash:      "d6ac82ac4ee426b5ce2061f78cc02f9fe1db587e",
		},
		{
			SystemID:     "ps-4",
			ChangelistID: "1002",
			Order:        4,
			GitHash:      "45247158d641ece6318f2598fefecfce86a61ae0",
		},
	}
}

// TestTriage_SingleDigestOnMaster_Success tests a common case of a developer triaging a single
// test on the master branch.
func TestTriage_SingleDigestOnMaster_Success(t *testing.T) {
	unittest.SmallTest(t)

	mes := &mock_expectations.Store{}
	defer mes.AssertExpectations(t)

	const user = "user@example.com"

	mes.On("AddChange", testutils.AnyContext, []expectations.Delta{
		{
			Grouping: bug_revert.TestOne,
			Digest:   bug_revert.BravoUntriagedDigest,
			Label:    expectations.Negative,
		},
	}, user).Return(nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			ExpectationsStore: mes,
		},
	}

	tr := frontend.TriageRequest{
		TestDigestStatus: map[types.TestName]map[types.Digest]expectations.Label{
			bug_revert.TestOne: {
				bug_revert.BravoUntriagedDigest: expectations.Negative,
			},
		},
	}

	err := wh.triage(context.Background(), user, tr)
	assert.NoError(t, err)
}

func TestTriage_SingleDigestOnMaster_ImageMatchingAlgorithmSet_UsesAlgorithmNameAsAuthor(t *testing.T) {
	unittest.SmallTest(t)

	mes := &mock_expectations.Store{}
	defer mes.AssertExpectations(t)

	const user = "user@example.com"
	const algorithmName = "fuzzy"

	mes.On("AddChange", testutils.AnyContext, []expectations.Delta{
		{
			Grouping: bug_revert.TestOne,
			Digest:   bug_revert.BravoUntriagedDigest,
			Label:    expectations.Negative,
		},
	}, algorithmName).Return(nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			ExpectationsStore: mes,
		},
	}

	tr := frontend.TriageRequest{
		TestDigestStatus: map[types.TestName]map[types.Digest]expectations.Label{
			bug_revert.TestOne: {
				bug_revert.BravoUntriagedDigest: expectations.Negative,
			},
		},
		ImageMatchingAlgorithm: algorithmName,
	}

	err := wh.triage(context.Background(), user, tr)
	assert.NoError(t, err)
}

// TestTriage_SingleDigestOnCL_Success tests a common case of a developer triaging a single test on
// a Changelist.
func TestTriage_SingleDigestOnCL_Success(t *testing.T) {
	unittest.SmallTest(t)

	mes := &mock_expectations.Store{}
	clExp := &mock_expectations.Store{}
	defer clExp.AssertExpectations(t)

	const clID = "12345"
	const githubCRS = "github"
	const user = "user@example.com"

	mes.On("ForChangelist", clID, githubCRS).Return(clExp)

	clExp.On("AddChange", testutils.AnyContext, []expectations.Delta{
		{
			Grouping: bug_revert.TestOne,
			Digest:   bug_revert.BravoUntriagedDigest,
			Label:    expectations.Negative,
		},
	}, user).Return(nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			ExpectationsStore: mes,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID: githubCRS,
					// The rest is unused here
				},
			},
		},
	}

	tr := frontend.TriageRequest{
		CodeReviewSystem: githubCRS,
		ChangelistID:     clID,
		TestDigestStatus: map[types.TestName]map[types.Digest]expectations.Label{
			bug_revert.TestOne: {
				bug_revert.BravoUntriagedDigest: expectations.Negative,
			},
		},
	}

	err := wh.triage(context.Background(), user, tr)
	assert.NoError(t, err)
}

func TestTriage_SingleDigestOnCL_ImageMatchingAlgorithmSet_UsesAlgorithmNameAsAuthor(t *testing.T) {
	unittest.SmallTest(t)

	mes := &mock_expectations.Store{}
	clExp := &mock_expectations.Store{}
	defer mes.AssertExpectations(t)
	defer clExp.AssertExpectations(t)

	const clID = "12345"
	const githubCRS = "github"
	const user = "user@example.com"
	const algorithmName = "fuzzy"

	mes.On("ForChangelist", clID, githubCRS).Return(clExp)

	clExp.On("AddChange", testutils.AnyContext, []expectations.Delta{
		{
			Grouping: bug_revert.TestOne,
			Digest:   bug_revert.BravoUntriagedDigest,
			Label:    expectations.Negative,
		},
	}, algorithmName).Return(nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			ExpectationsStore: mes,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID: githubCRS,
				},
			},
		},
	}

	tr := frontend.TriageRequest{
		CodeReviewSystem: githubCRS,
		ChangelistID:     clID,
		TestDigestStatus: map[types.TestName]map[types.Digest]expectations.Label{
			bug_revert.TestOne: {
				bug_revert.BravoUntriagedDigest: expectations.Negative,
			},
		},
		ImageMatchingAlgorithm: algorithmName,
	}

	err := wh.triage(context.Background(), user, tr)
	assert.NoError(t, err)
}

// TestTriage_BulkTriageOnMaster_SunnyDay_Success tests the case of a developer triaging multiple
// tests at once (via bulk triage).
func TestTriage_BulkTriageOnMaster_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)

	mes := &mock_expectations.Store{}
	defer mes.AssertExpectations(t)

	const user = "user@example.com"

	matcher := mock.MatchedBy(func(delta []expectations.Delta) bool {
		assert.Contains(t, delta, expectations.Delta{
			Grouping: bug_revert.TestOne,
			Digest:   bug_revert.AlfaPositiveDigest,
			Label:    expectations.Untriaged,
		})
		assert.Contains(t, delta, expectations.Delta{
			Grouping: bug_revert.TestOne,
			Digest:   bug_revert.BravoUntriagedDigest,
			Label:    expectations.Negative,
		})
		assert.Contains(t, delta, expectations.Delta{
			Grouping: bug_revert.TestTwo,
			Digest:   bug_revert.CharliePositiveDigest,
			Label:    expectations.Positive,
		})
		assert.Contains(t, delta, expectations.Delta{
			Grouping: bug_revert.TestTwo,
			Digest:   bug_revert.DeltaUntriagedDigest,
			Label:    expectations.Negative,
		})
		return true
	})

	mes.On("AddChange", testutils.AnyContext, matcher, user).Return(nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			ExpectationsStore: mes,
		},
	}

	tr := frontend.TriageRequest{
		TestDigestStatus: map[types.TestName]map[types.Digest]expectations.Label{
			bug_revert.TestOne: {
				bug_revert.AlfaPositiveDigest:   expectations.Untriaged,
				bug_revert.BravoUntriagedDigest: expectations.Negative,
			},
			bug_revert.TestTwo: {
				bug_revert.CharliePositiveDigest: expectations.Positive,
				bug_revert.DeltaUntriagedDigest:  expectations.Negative,
				"digestWithNoClosestPositive":    "",
			},
		},
	}

	err := wh.triage(context.Background(), user, tr)
	assert.NoError(t, err)
}

// TestTriage_SingleLegacyDigestOnMaster_SunnyDay_Success tests a common case of a developer
// triaging a single test using the legacy code (which has "0" as key issue instead of empty string.
func TestTriage_SingleLegacyDigestOnMaster_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)

	mes := &mock_expectations.Store{}
	defer mes.AssertExpectations(t)

	const user = "user@example.com"

	mes.On("AddChange", testutils.AnyContext, []expectations.Delta{
		{
			Grouping: bug_revert.TestOne,
			Digest:   bug_revert.BravoUntriagedDigest,
			Label:    expectations.Negative,
		},
	}, user).Return(nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			ExpectationsStore: mes,
		},
	}

	tr := frontend.TriageRequest{
		ChangelistID: "0",
		TestDigestStatus: map[types.TestName]map[types.Digest]expectations.Label{
			bug_revert.TestOne: {
				bug_revert.BravoUntriagedDigest: expectations.Negative,
			},
		},
	}

	err := wh.triage(context.Background(), user, tr)
	assert.NoError(t, err)
}

// TestTriageLogHandler_MasterBranchNoDetails_SunnyDay_Success tests getting the triage log and
// converting them to the appropriate types.
func TestTriageLogHandler_MasterBranchNoDetails_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)

	mes := &mock_expectations.Store{}
	defer mes.AssertExpectations(t)

	wh := Handlers{
		anonymousExpensiveQuota: rate.NewLimiter(rate.Inf, 1),
		HandlersConfig: HandlersConfig{
			ExpectationsStore: mes,
		},
	}

	ts1 := time.Date(2019, time.October, 5, 4, 3, 2, 0, time.UTC)
	ts2 := time.Date(2019, time.October, 6, 7, 8, 9, 0, time.UTC)

	const offset = 10
	const size = 20

	mes.On("QueryLog", testutils.AnyContext, offset, size, false).Return([]expectations.TriageLogEntry{
		{
			ID:          "abc",
			ChangeCount: 1,
			User:        "user1@example.com",
			TS:          ts1,
			Details: []expectations.Delta{
				{
					Label:    expectations.Positive,
					Digest:   bug_revert.DeltaUntriagedDigest,
					Grouping: bug_revert.TestOne,
				},
			},
		},
		{
			ID:          "abc",
			ChangeCount: 2,
			User:        "user1@example.com",
			TS:          ts2,
			Details: []expectations.Delta{
				{
					Label:    expectations.Positive,
					Digest:   bug_revert.BravoUntriagedDigest,
					Grouping: bug_revert.TestOne,
				},
				{
					Label:    expectations.Negative,
					Digest:   bug_revert.CharliePositiveDigest,
					Grouping: bug_revert.TestOne,
				},
			},
		},
	}, offset+2, nil)

	expectedResponse := frontend.TriageLogResponse{
		Entries: []frontend.TriageLogEntry{
			{
				ID:          "abc",
				ChangeCount: 1,
				User:        "user1@example.com",
				TS:          ts1.Unix() * 1000,
				Details: []frontend.TriageDelta{
					{
						Label:    expectations.Positive,
						Digest:   bug_revert.DeltaUntriagedDigest,
						TestName: bug_revert.TestOne,
					},
				},
			},
			{
				ID:          "abc",
				ChangeCount: 2,
				User:        "user1@example.com",
				TS:          ts2.Unix() * 1000,
				Details: []frontend.TriageDelta{
					{
						Label:    expectations.Positive,
						Digest:   bug_revert.BravoUntriagedDigest,
						TestName: bug_revert.TestOne,
					},
					{
						Label:    expectations.Negative,
						Digest:   bug_revert.CharliePositiveDigest,
						TestName: bug_revert.TestOne,
					},
				},
			},
		},
		ResponsePagination: httputils.ResponsePagination{
			Offset: offset,
			Size:   size,
			Total:  12,
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/json/v1/triagelog?offset=%d&size=%d", offset, size), nil)
	wh.TriageLogHandler(w, r)
	b, err := json.Marshal(expectedResponse)
	require.NoError(t, err)
	assertJSONResponseWas(t, http.StatusOK, string(b), w)
}

// TestGetDigestsResponse_SunnyDay_Success tests the usual case of fetching digests for a given
// test in a given corpus.
func TestGetDigestsResponse_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)
	mi := &mock_indexer.IndexSource{}
	defer mi.AssertExpectations(t)

	// We stop just before the "revert" in the fake data set, so it appears there are more untriaged
	// digests going on.
	fis := makeBugRevertIndex(3)
	mi.On("GetIndex").Return(fis)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mi,
		},
	}

	dlr := wh.getDigestsResponse(string(bug_revert.TestOne), "todo")

	assert.Equal(t, frontend.DigestListResponse{
		Digests: []types.Digest{bug_revert.AlfaPositiveDigest, bug_revert.BravoUntriagedDigest},
	}, dlr)
}

// TestListIgnoreRules_NoCounts_SunnyDay_Success tests the case where we simply return the list of
// the current ignore rules, without counting any of the traces to which they apply.
func TestListIgnoreRules_NoCounts_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)

	mis := &mock_ignore.Store{}
	defer mis.AssertExpectations(t)

	mis.On("List", testutils.AnyContext).Return(makeIgnoreRules(), nil)

	wh := Handlers{
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
		HandlersConfig: HandlersConfig{
			IgnoreStore: mis,
		},
	}

	expectedResponse := frontend.IgnoresResponse{
		Rules: []frontend.IgnoreRule{
			{
				ID:        "1234",
				CreatedBy: "user@example.com",
				UpdatedBy: "user2@example.com",
				Expires:   firstRuleExpire,
				Query:     "device=delta",
				Note:      "Flaky driver",
			},
			{
				ID:        "5678",
				CreatedBy: "user2@example.com",
				UpdatedBy: "user@example.com",
				Expires:   secondRuleExpire,
				Query:     "name=test_two&source_type=gm",
				Note:      "Not ready yet",
			},
			{
				ID:        "-1",
				CreatedBy: "user3@example.com",
				UpdatedBy: "user3@example.com",
				Expires:   thirdRuleExpire,
				Query:     "matches=nothing",
				Note:      "Oops, this matches nothing",
			},
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v1/ignores", nil)
	wh.ListIgnoreRules(w, r)
	b, err := json.Marshal(expectedResponse)
	require.NoError(t, err)
	assertJSONResponseWas(t, http.StatusOK, string(b), w)
}

// TestListIgnoreRules_WithCounts_SunnyDay_Success tests the case where we get the list of current
// ignore rules and count the traces to which those rules apply.
func TestListIgnoreRules_WithCounts_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)

	mes := &mock_expectations.Store{}
	mi := &mock_indexer.IndexSource{}
	mis := &mock_ignore.Store{}
	defer mes.AssertExpectations(t)
	defer mi.AssertExpectations(t)
	defer mis.AssertExpectations(t)

	exp := bug_revert.MakeTestExpectations()
	// Pretending EchoPositiveDigest is untriaged makes the data a bit more interesting, in the sense
	// that we can observe differences between Count/ExclusiveCount and
	// UntriagedCount/ExclusiveUntriagedCount.
	exp.Set(bug_revert.TestTwo, bug_revert.EchoPositiveDigest, expectations.Untriaged)
	mes.On("Get", testutils.AnyContext).Return(exp, nil)

	fis := makeBugRevertIndexWithIgnores(makeIgnoreRules(), 1)
	mi.On("GetIndex").Return(fis)

	mis.On("List", testutils.AnyContext).Return(makeIgnoreRules(), nil)

	wh := Handlers{
		anonymousExpensiveQuota: rate.NewLimiter(rate.Inf, 1),
		HandlersConfig: HandlersConfig{
			ExpectationsStore: mes,
			IgnoreStore:       mis,
			Indexer:           mi,
		},
	}

	expectedResponse := frontend.IgnoresResponse{
		Rules: []frontend.IgnoreRule{
			{
				ID:                      "1234",
				CreatedBy:               "user@example.com",
				UpdatedBy:               "user2@example.com",
				Expires:                 firstRuleExpire,
				Query:                   "device=delta",
				Note:                    "Flaky driver",
				Count:                   2,
				ExclusiveCount:          1,
				UntriagedCount:          1,
				ExclusiveUntriagedCount: 0,
			},
			{
				ID:                      "5678",
				CreatedBy:               "user2@example.com",
				UpdatedBy:               "user@example.com",
				Expires:                 secondRuleExpire,
				Query:                   "name=test_two&source_type=gm",
				Note:                    "Not ready yet",
				Count:                   4,
				ExclusiveCount:          3,
				UntriagedCount:          2,
				ExclusiveUntriagedCount: 1,
			},
			{
				ID:                      "-1",
				CreatedBy:               "user3@example.com",
				UpdatedBy:               "user3@example.com",
				Expires:                 thirdRuleExpire,
				Query:                   "matches=nothing",
				Note:                    "Oops, this matches nothing",
				Count:                   0,
				ExclusiveCount:          0,
				UntriagedCount:          0,
				ExclusiveUntriagedCount: 0,
			},
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v1/ignores?counts=1", nil)
	wh.ListIgnoreRules(w, r)
	b, err := json.Marshal(expectedResponse)
	require.NoError(t, err)
	assertJSONResponseWas(t, http.StatusOK, string(b), w)
}

// TestListIgnoreRules_WithCountsOnBigTile_SunnyDay_NoRaceConditions uses an artificially bigger
// tile to process to make sure the counting code has no races in it when sharded.
func TestListIgnoreRules_WithCountsOnBigTile_SunnyDay_NoRaceConditions(t *testing.T) {
	unittest.SmallTest(t)

	mes := &mock_expectations.Store{}
	mi := &mock_indexer.IndexSource{}
	mis := &mock_ignore.Store{}
	defer mes.AssertExpectations(t)
	defer mi.AssertExpectations(t)
	defer mis.AssertExpectations(t)

	exp := bug_revert.MakeTestExpectations()
	// This makes the data a bit more interesting
	exp.Set(bug_revert.TestTwo, bug_revert.EchoPositiveDigest, expectations.Untriaged)
	mes.On("Get", testutils.AnyContext).Return(exp, nil)

	fis := makeBugRevertIndexWithIgnores(makeIgnoreRules(), 50)
	mi.On("GetIndex").Return(fis)

	mis.On("List", testutils.AnyContext).Return(makeIgnoreRules(), nil)

	wh := Handlers{
		anonymousExpensiveQuota: rate.NewLimiter(rate.Inf, 1),
		HandlersConfig: HandlersConfig{
			ExpectationsStore: mes,
			IgnoreStore:       mis,
			Indexer:           mi,
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v1/ignores?counts=1", nil)
	wh.ListIgnoreRules(w, r)
	responseBytes := assertJSONResponseAndReturnBody(t, http.StatusOK, w)
	response := frontend.IgnoresResponse{}
	require.NoError(t, json.Unmarshal(responseBytes, &response))

	// Just check the length, other unit tests will validate the correctness.
	assert.Len(t, response.Rules, 3)
}

// TestHandlersThatRequireLogin_NotLoggedIn_UnauthorizedError tests a list of handlers to make sure
// they return an Unauthorized status if attempted to be used without being logged in.
func TestHandlersThatRequireLogin_NotLoggedIn_UnauthorizedError(t *testing.T) {
	unittest.SmallTest(t)

	wh := Handlers{}

	test := func(name string, endpoint http.HandlerFunc) {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, requestURL, strings.NewReader("does not matter"))
			endpoint(w, r)

			resp := w.Result()
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		})
	}
	test("add", wh.AddIgnoreRule)
	test("update", wh.UpdateIgnoreRule)
	test("delete", wh.DeleteIgnoreRule)
	// TODO(kjlubick): check all handlers that need login, not just Ignores*
}

// TestHandlersWhichTakeJSON_BadInput_BadRequestError tests a list of handlers which take JSON as an
// input and make sure they all return a BadRequest response when given bad input.
func TestHandlersWhichTakeJSON_BadInput_BadRequestError(t *testing.T) {
	unittest.SmallTest(t)

	wh := Handlers{
		testingAuthAs: "test@google.com",
	}

	test := func(name string, endpoint http.HandlerFunc) {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, requestURL, strings.NewReader("invalid JSON"))
			endpoint(w, r)

			resp := w.Result()
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
	test("add", wh.AddIgnoreRule)
	test("update", wh.UpdateIgnoreRule)
	// TODO(kjlubick): check all handlers that process JSON
}

// TestAddIgnoreRule_SunnyDay_Success tests a typical case of adding an ignore rule (which ends
// up in the IgnoreStore).
func TestAddIgnoreRule_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)

	const user = "test@example.com"
	var fakeNow = time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	var oneWeekFromNow = time.Date(2020, time.January, 9, 3, 4, 5, 0, time.UTC)

	mis := &mock_ignore.Store{}
	defer mis.AssertExpectations(t)

	expectedRule := ignore.Rule{
		ID:        "",
		CreatedBy: user,
		UpdatedBy: user,
		Expires:   oneWeekFromNow,
		Query:     "a=b&c=d",
		Note:      "skbug:9744",
	}
	mis.On("Create", testutils.AnyContext, expectedRule).Return(nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			IgnoreStore: mis,
		},
		testingAuthAs: user,
	}
	w := httptest.NewRecorder()
	body := strings.NewReader(`{"duration": "1w", "filter": "a=b&c=d", "note": "skbug:9744"}`)
	r := httptest.NewRequest(http.MethodPost, requestURL, body)
	r = overwriteNow(r, fakeNow)
	wh.AddIgnoreRule(w, r)

	assertJSONResponseWas(t, http.StatusOK, `{"added":"true"}`, w)
}

// TestAddIgnoreRule_StoreFailure_InternalServerError tests the exceptional case where a rule
// fails to be added to the IgnoreStore).
func TestAddIgnoreRule_StoreFailure_InternalServerError(t *testing.T) {
	unittest.SmallTest(t)

	mis := &mock_ignore.Store{}
	defer mis.AssertExpectations(t)

	mis.On("Create", testutils.AnyContext, mock.Anything).Return(errors.New("firestore broke"))
	wh := Handlers{
		HandlersConfig: HandlersConfig{
			IgnoreStore: mis,
		},
		testingAuthAs: "test@google.com",
	}
	w := httptest.NewRecorder()
	body := strings.NewReader(`{"duration": "1w", "filter": "a=b&c=d", "note": "skbug:9744"}`)
	r := httptest.NewRequest(http.MethodPost, requestURL, body)
	r = mux.SetURLVars(r, map[string]string{"id": "12345"})
	wh.AddIgnoreRule(w, r)

	resp := w.Result()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// TestGetValidatedIgnoreRule_InvalidInput_Error tests several exceptional cases where an invalid
// rule is given to the handler.
func TestGetValidatedIgnoreRule_InvalidInput_Error(t *testing.T) {
	unittest.SmallTest(t)

	test := func(name, errorFragment, jsonInput string) {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, requestURL, strings.NewReader(jsonInput))
			_, _, err := getValidatedIgnoreRule(r)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), errorFragment)
		})
	}

	test("invalid JSON", "request JSON", "This should not be valid JSON")
	// There's an instagram joke here... #nofilter
	test("no filter", "supply a filter", `{"duration": "1w", "filter": "", "note": "skbug:9744"}`)
	test("no duration", "invalid duration", `{"duration": "", "filter": "a=b", "note": "skbug:9744"}`)
	test("invalid duration", "invalid duration", `{"duration": "bad", "filter": "a=b", "note": "skbug:9744"}`)
	test("filter too long", "Filter must be", string(makeJSONWithLongFilter(t)))
	test("note too long", "Note must be", string(makeJSONWithLongNote(t)))
}

// makeJSONWithLongFilter returns a []byte that is the encoded JSON of an otherwise valid
// IgnoreRuleBody, except it has a Filter which exceeds 10 KB.
func makeJSONWithLongFilter(t *testing.T) []byte {
	superLongFilter := frontend.IgnoreRuleBody{
		Duration: "1w",
		Filter:   strings.Repeat("a=b&", 10000),
		Note:     "really long filter",
	}
	superLongFilterBytes, err := json.Marshal(superLongFilter)
	require.NoError(t, err)
	return superLongFilterBytes
}

// makeJSONWithLongNote returns a []byte that is the encoded JSON of an otherwise valid
// IgnoreRuleBody, except it has a Note which exceeds 1 KB.
func makeJSONWithLongNote(t *testing.T) []byte {
	superLongFilter := frontend.IgnoreRuleBody{
		Duration: "1w",
		Filter:   "a=b",
		Note:     strings.Repeat("really long note ", 1000),
	}
	superLongFilterBytes, err := json.Marshal(superLongFilter)
	require.NoError(t, err)
	return superLongFilterBytes
}

// TestUpdateIgnoreRule_SunnyDay_Success tests a typical case of updating an ignore rule in
// IgnoreStore.
func TestUpdateIgnoreRule_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)

	const id = "12345"
	const user = "test@example.com"
	var fakeNow = time.Date(2020, time.January, 2, 3, 4, 5, 0, time.UTC)
	var oneWeekFromNow = time.Date(2020, time.January, 9, 3, 4, 5, 0, time.UTC)

	mis := &mock_ignore.Store{}
	defer mis.AssertExpectations(t)

	expectedRule := ignore.Rule{
		ID:        id,
		CreatedBy: user,
		UpdatedBy: user,
		Expires:   oneWeekFromNow,
		Query:     "a=b&c=d",
		Note:      "skbug:9744",
	}
	mis.On("Update", testutils.AnyContext, expectedRule).Return(nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			IgnoreStore: mis,
		},
		testingAuthAs: user,
	}
	w := httptest.NewRecorder()
	body := strings.NewReader(`{"duration": "1w", "filter": "a=b&c=d", "note": "skbug:9744"}`)
	r := httptest.NewRequest(http.MethodPost, requestURL, body)
	r = setID(r, id)
	r = overwriteNow(r, fakeNow)
	wh.UpdateIgnoreRule(w, r)

	assertJSONResponseWas(t, http.StatusOK, `{"updated":"true"}`, w)
}

// TestUpdateIgnoreRule_NoID_BadRequestError tests an exceptional case of attempting to update
// an ignore rule without providing an id for that ignore rule.
func TestUpdateIgnoreRule_NoID_BadRequestError(t *testing.T) {
	unittest.SmallTest(t)

	wh := Handlers{
		testingAuthAs: "test@google.com",
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, requestURL, strings.NewReader("doesn't matter"))
	wh.UpdateIgnoreRule(w, r)

	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestUpdateIgnoreRule_StoreFailure_InternalServerError tests an exceptional case of attempting
// to update an ignore rule in which there is an error returned by the IgnoreStore.
func TestUpdateIgnoreRule_StoreFailure_InternalServerError(t *testing.T) {
	unittest.SmallTest(t)
	mis := &mock_ignore.Store{}
	defer mis.AssertExpectations(t)

	mis.On("Update", testutils.AnyContext, mock.Anything).Return(errors.New("firestore broke"))
	wh := Handlers{
		HandlersConfig: HandlersConfig{
			IgnoreStore: mis,
		},
		testingAuthAs: "test@google.com",
	}
	w := httptest.NewRecorder()
	body := strings.NewReader(`{"duration": "1w", "filter": "a=b&c=d", "note": "skbug:9744"}`)
	r := httptest.NewRequest(http.MethodPost, requestURL, body)
	r = mux.SetURLVars(r, map[string]string{"id": "12345"})
	wh.UpdateIgnoreRule(w, r)

	resp := w.Result()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// TestDeleteIgnoreRule_RuleExists_SunnyDay_Success tests a typical case of deleting an ignore
// rule which exists in the IgnoreStore.
func TestDeleteIgnoreRule_RuleExists_SunnyDay_Success(t *testing.T) {
	unittest.SmallTest(t)

	const id = "12345"

	mis := &mock_ignore.Store{}
	defer mis.AssertExpectations(t)

	mis.On("Delete", testutils.AnyContext, id).Return(nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			IgnoreStore: mis,
		},
		testingAuthAs: "test@example.com",
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, requestURL, nil)
	r = setID(r, id)
	wh.DeleteIgnoreRule(w, r)

	assertJSONResponseWas(t, http.StatusOK, `{"deleted":"true"}`, w)
}

// TestDeleteIgnoreRule_NoID_InternalServerError tests an exceptional case of attempting to
// delete an ignore rule without providing an id for that ignore rule.
func TestDeleteIgnoreRule_NoID_InternalServerError(t *testing.T) {
	unittest.SmallTest(t)

	wh := Handlers{
		testingAuthAs: "test@google.com",
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, requestURL, strings.NewReader("doesn't matter"))
	wh.DeleteIgnoreRule(w, r)

	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

// TestDeleteIgnoreRule_StoreFailure_InternalServerError tests an exceptional case of attempting
// to delete an ignore rule in which there is an error returned by the IgnoreStore (note: There
// is no error returned from ignore.Store when deleting a rule which does not exist).
func TestDeleteIgnoreRule_StoreFailure_InternalServerError(t *testing.T) {
	unittest.SmallTest(t)

	const id = "12345"

	mis := &mock_ignore.Store{}
	defer mis.AssertExpectations(t)

	mis.On("Delete", testutils.AnyContext, id).Return(errors.New("firestore broke"))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			IgnoreStore: mis,
		},
		testingAuthAs: "test@example.com",
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, requestURL, nil)
	r = setID(r, id)
	wh.DeleteIgnoreRule(w, r)

	resp := w.Result()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestBaselineHandlerV2_PrimaryBranch_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, frontend.ExpectationsRouteV2, nil)

	expectedJSONResponse := `{"primary":{"circle":{"00000000000000000000000000000000":"negative","c01c01c01c01c01c01c01c01c01c01c0":"positive","c02c02c02c02c02c02c02c02c02c02c0":"positive"},"square":{"a01a01a01a01a01a01a01a01a01a01a0":"positive","a02a02a02a02a02a02a02a02a02a02a0":"positive","a03a03a03a03a03a03a03a03a03a03a0":"positive","a07a07a07a07a07a07a07a07a07a07a0":"positive","a08a08a08a08a08a08a08a08a08a08a0":"positive","a09a09a09a09a09a09a09a09a09a09a0":"negative"},"triangle":{"b01b01b01b01b01b01b01b01b01b01b0":"positive","b02b02b02b02b02b02b02b02b02b02b0":"positive","b03b03b03b03b03b03b03b03b03b03b0":"negative","b04b04b04b04b04b04b04b04b04b04b0":"negative"}}}`

	wh.BaselineHandlerV2(w, r)
	assertJSONResponseWas(t, http.StatusOK, expectedJSONResponse, w)
}

func TestBaselineHandlerV2_ValidChangelist_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID: dks.GerritCRS,
				},
			},
		},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, frontend.ExpectationsRouteV2+"?issue=CL_fix_ios&crs=gerrit", nil)

	// Note that DigestC06Pos_CL is here, but DigestC07Unt_CL is not because the latter is
	// untriaged (and thus omitted from the baseline).
	expectedJSONResponse := `{"primary":{"circle":{"00000000000000000000000000000000":"negative","c01c01c01c01c01c01c01c01c01c01c0":"positive","c02c02c02c02c02c02c02c02c02c02c0":"positive","c06c06c06c06c06c06c06c06c06c06c0":"positive"},"square":{"a01a01a01a01a01a01a01a01a01a01a0":"positive","a02a02a02a02a02a02a02a02a02a02a0":"positive","a03a03a03a03a03a03a03a03a03a03a0":"positive","a07a07a07a07a07a07a07a07a07a07a0":"positive","a08a08a08a08a08a08a08a08a08a08a0":"positive","a09a09a09a09a09a09a09a09a09a09a0":"negative"},"triangle":{"b02b02b02b02b02b02b02b02b02b02b0":"positive","b03b03b03b03b03b03b03b03b03b03b0":"negative","b04b04b04b04b04b04b04b04b04b04b0":"negative"}},"cl_id":"CL_fix_ios","crs":"gerrit"}`

	wh.BaselineHandlerV2(w, r)
	assertJSONResponseWas(t, http.StatusOK, expectedJSONResponse, w)
}

func TestBaselineHandlerV2_ValidChangelistWithNewTests_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID: dks.GerritCRS,
				},
				{
					ID: dks.GerritInternalCRS,
				},
			},
		},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, frontend.ExpectationsRouteV2+"?issue=CL_new_tests&crs=gerrit-internal", nil)

	// We expect to see data from the Seven Test and RoundRect Test.
	expectedJSONResponse := `{"primary":{"circle":{"00000000000000000000000000000000":"negative","c01c01c01c01c01c01c01c01c01c01c0":"positive","c02c02c02c02c02c02c02c02c02c02c0":"positive"},"round rect":{"e01e01e01e01e01e01e01e01e01e01e0":"positive","e02e02e02e02e02e02e02e02e02e02e0":"positive"},"seven":{"d01d01d01d01d01d01d01d01d01d01d0":"positive"},"square":{"a01a01a01a01a01a01a01a01a01a01a0":"positive","a02a02a02a02a02a02a02a02a02a02a0":"positive","a03a03a03a03a03a03a03a03a03a03a0":"positive","a07a07a07a07a07a07a07a07a07a07a0":"positive","a08a08a08a08a08a08a08a08a08a08a0":"positive","a09a09a09a09a09a09a09a09a09a09a0":"negative"},"triangle":{"b01b01b01b01b01b01b01b01b01b01b0":"positive","b02b02b02b02b02b02b02b02b02b02b0":"positive","b03b03b03b03b03b03b03b03b03b03b0":"negative","b04b04b04b04b04b04b04b04b04b04b0":"negative"}},"cl_id":"CL_new_tests","crs":"gerrit-internal"}`

	wh.BaselineHandlerV2(w, r)
	assertJSONResponseWas(t, http.StatusOK, expectedJSONResponse, w)
}

func TestBaselineHandlerV2_InvalidCRS_ReturnsError(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID: dks.GerritCRS,
				},
			},
		},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, frontend.ExpectationsRouteV2+"?issue=CL_fix_ios&crs=wrong", nil)

	wh.BaselineHandlerV2(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
	assert.Contains(t, w.Body.String(), "Invalid CRS")
}

func TestBaselineHandlerV2_NewCL_ReturnsPrimaryBaseline(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID: dks.GerritCRS,
				},
			},
		},
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, frontend.ExpectationsRouteV2+"?issue=NewCLID&crs=gerrit", nil)

	expectedJSONResponse := `{"primary":{"circle":{"00000000000000000000000000000000":"negative","c01c01c01c01c01c01c01c01c01c01c0":"positive","c02c02c02c02c02c02c02c02c02c02c0":"positive"},"square":{"a01a01a01a01a01a01a01a01a01a01a0":"positive","a02a02a02a02a02a02a02a02a02a02a0":"positive","a03a03a03a03a03a03a03a03a03a03a0":"positive","a07a07a07a07a07a07a07a07a07a07a0":"positive","a08a08a08a08a08a08a08a08a08a08a0":"positive","a09a09a09a09a09a09a09a09a09a09a0":"negative"},"triangle":{"b01b01b01b01b01b01b01b01b01b01b0":"positive","b02b02b02b02b02b02b02b02b02b02b0":"positive","b03b03b03b03b03b03b03b03b03b03b0":"negative","b04b04b04b04b04b04b04b04b04b04b0":"negative"}},"cl_id":"NewCLID","crs":"gerrit"}`

	wh.BaselineHandlerV2(w, r)
	assertJSONResponseWas(t, http.StatusOK, expectedJSONResponse, w)
}

// TestWhoami_NotLoggedIn_Success tests that /json/whoami returns the expected empty response when
// no user is logged in.
func TestWhoami_NotLoggedIn_Success(t *testing.T) {
	unittest.SmallTest(t)
	wh := Handlers{
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	wh.Whoami(w, r)
	assertJSONResponseWas(t, http.StatusOK, `{"whoami":""}`, w)
}

// TestWhoami_LoggedIn_Success tests that /json/whoami returns the email of the user that is
// currently logged in.
func TestWhoami_LoggedIn_Success(t *testing.T) {
	unittest.SmallTest(t)
	wh := Handlers{
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
		testingAuthAs:       "test@example.com",
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	wh.Whoami(w, r)
	assertJSONResponseWas(t, http.StatusOK, `{"whoami":"test@example.com"}`, w)
}

// TestLatestPositiveDigest_Success tests that /json/latestpositivedigest/{traceId} returns the
// most recent positive digest in the expected format.
//
// Note: We don't test the cases when the tile has no positive digests, or when the trace isn't
// found, because it both cases SearchIndexer method MostRecentPositiveDigest() will just return
// types.MissingDigest and a nil error.
func TestLatestPositiveDigest_Success(t *testing.T) {
	unittest.SmallTest(t)

	mockIndexSearcher := &mock_indexer.IndexSearcher{}
	mockIndexSearcher.AssertExpectations(t)
	mockIndexSource := &mock_indexer.IndexSource{}
	mockIndexSource.AssertExpectations(t)

	const traceId = tiling.TraceID(",foo=bar,")
	const digest = types.Digest("11111111111111111111111111111111")
	const expectedJSONResponse = `{"digest":"11111111111111111111111111111111"}`

	mockIndexSource.On("GetIndex").Return(mockIndexSearcher)
	mockIndexSearcher.On("MostRecentPositiveDigest", testutils.AnyContext, traceId).Return(digest, nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mockIndexSource,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{"traceId": string(traceId)})

	wh.LatestPositiveDigestHandler(w, r)
	assertJSONResponseWas(t, http.StatusOK, expectedJSONResponse, w)
}

// TestLatestPositiveDigest_SearchIndexerFailure_InternalServerError tests that
// /json/latestpositivedigest/{traceId} produces an internal server error when SearchIndexer method
// MostRecentPositiveDigest() returns a non-nil error.
func TestLatestPositiveDigest_SearchIndexerFailure_InternalServerError(t *testing.T) {
	unittest.SmallTest(t)

	mockIndexSearcher := &mock_indexer.IndexSearcher{}
	mockIndexSearcher.AssertExpectations(t)
	mockIndexSource := &mock_indexer.IndexSource{}
	mockIndexSource.AssertExpectations(t)

	const traceId = tiling.TraceID(",foo=bar,")

	mockIndexSource.On("GetIndex").Return(mockIndexSearcher)
	mockIndexSearcher.On("MostRecentPositiveDigest", testutils.AnyContext, traceId).Return(tiling.MissingDigest, errors.New("kaboom"))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mockIndexSource,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{"traceId": string(traceId)})

	wh.LatestPositiveDigestHandler(w, r)
	assert.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
	assert.Contains(t, w.Body.String(), "Could not retrieve most recent positive digest.")
}

func TestGetPerTraceDigestsByTestName_Success(t *testing.T) {
	unittest.SmallTest(t)

	mockIndexSearcher := &mock_indexer.IndexSearcher{}
	mockIndexSource := &mock_indexer.IndexSource{}

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mockIndexSource,
		},
		anonymousExpensiveQuota: rate.NewLimiter(rate.Inf, 1),
	}

	mockIndexSource.On("GetIndex").Return(mockIndexSearcher)
	mockIndexSearcher.On("SlicedTraces", types.IncludeIgnoredTraces, map[string][]string{
		types.CorpusField:     {"MyCorpus"},
		types.PrimaryKeyField: {"MyTest"},
	}).Return([]*tiling.TracePair{
		{
			ID: ",name=MyTest,foo=alpha,source_type=MyCorpus,",
			Trace: tiling.NewTrace([]types.Digest{
				"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			}, map[string]string{
				"name":        "MyTest",
				"foo":         "alpha",
				"source_type": "MyCorpus",
			}, nil),
		},
		{
			ID: ",name=MyTest,foo=beta,source_type=MyCorpus,",
			Trace: tiling.NewTrace([]types.Digest{
				"",
				"cccccccccccccccccccccccccccccccc",
			}, map[string]string{
				"name":        "MyTest",
				"foo":         "beta",
				"source_type": "MyCorpus",
			}, nil),
		},
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{
		"corpus":   "MyCorpus",
		"testName": "MyTest",
	})

	wh.GetPerTraceDigestsByTestName(w, r)
	const expectedResponse = `{",name=MyTest,foo=alpha,source_type=MyCorpus,":["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"],",name=MyTest,foo=beta,source_type=MyCorpus,":["","cccccccccccccccccccccccccccccccc"]}`
	assertJSONResponseWas(t, http.StatusOK, expectedResponse, w)
}

func TestParamsHandler_MasterBranch_Success(t *testing.T) {
	unittest.SmallTest(t)

	mockIndexSearcher := &mock_indexer.IndexSearcher{}
	mockIndexSource := &mock_indexer.IndexSource{}

	mockIndexSource.On("GetIndex").Return(mockIndexSearcher)

	cpxTile := tiling.NewComplexTile(&tiling.Tile{
		ParamSet: paramtools.ParamSet{
			types.CorpusField:     []string{"first_corpus", "second_corpus"},
			types.PrimaryKeyField: []string{"alpha_test", "beta_test", "gamma_test"},
			"os":                  []string{"Android XYZ"},
		},
		// Other fields should be ignored.
	})

	mockIndexSearcher.On("Tile").Return(cpxTile)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mockIndexSource,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	wh.ParamsHandler(w, r)
	const expectedResponse = `{"name":["alpha_test","beta_test","gamma_test"],"os":["Android XYZ"],"source_type":["first_corpus","second_corpus"]}`
	assertJSONResponseWas(t, http.StatusOK, expectedResponse, w)
}

func TestParamsHandler_ChangelistIndex_Success(t *testing.T) {
	unittest.SmallTest(t)

	mockIndexSource := &mock_indexer.IndexSource{}
	defer mockIndexSource.AssertExpectations(t) // want to make sure fallback happened

	const gerritCRS = "gerrit"
	const clID = "1234"

	clIdx := indexer.ChangelistIndex{
		ParamSet: paramtools.ParamSet{
			types.CorpusField:     []string{"first_corpus", "second_corpus"},
			types.PrimaryKeyField: []string{"alpha_test", "beta_test", "gamma_test"},
			"os":                  []string{"Android XYZ"},
		},
	}
	mockIndexSource.On("GetIndexForCL", gerritCRS, clID).Return(&clIdx)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mockIndexSource,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID: gerritCRS,
				},
			},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/paramset?changelist_id=1234&crs=gerrit", nil)
	wh.ParamsHandler(w, r)
	const expectedResponse = `{"name":["alpha_test","beta_test","gamma_test"],"os":["Android XYZ"],"source_type":["first_corpus","second_corpus"]}`
	assertJSONResponseWas(t, http.StatusOK, expectedResponse, w)
}

func TestParamsHandler_NoChangelistIndex_FallBackToMasterBranch(t *testing.T) {
	unittest.SmallTest(t)

	mockIndexSearcher := &mock_indexer.IndexSearcher{}
	mockIndexSource := &mock_indexer.IndexSource{}
	defer mockIndexSource.AssertExpectations(t) // want to make sure fallback happened

	const gerritCRS = "gerrit"
	const clID = "1234"

	mockIndexSource.On("GetIndex").Return(mockIndexSearcher)
	mockIndexSource.On("GetIndexForCL", gerritCRS, clID).Return(nil)

	cpxTile := tiling.NewComplexTile(&tiling.Tile{
		ParamSet: paramtools.ParamSet{
			types.CorpusField:     []string{"first_corpus", "second_corpus"},
			types.PrimaryKeyField: []string{"alpha_test", "beta_test", "gamma_test"},
			"os":                  []string{"Android XYZ"},
		},
		// Other fields should be ignored.
	})

	mockIndexSearcher.On("Tile").Return(cpxTile)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mockIndexSource,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID: gerritCRS,
				},
			},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/paramset?changelist_id=1234&crs=gerrit", nil)
	wh.ParamsHandler(w, r)
	const expectedResponse = `{"name":["alpha_test","beta_test","gamma_test"],"os":["Android XYZ"],"source_type":["first_corpus","second_corpus"]}`
	assertJSONResponseWas(t, http.StatusOK, expectedResponse, w)
}

func TestChangelistSearchRedirect_CLHasUntriagedDigests_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID: dks.GerritCRS,
				},
			},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/cl/gerrit/CL_fix_ios", nil)
	r = mux.SetURLVars(r, map[string]string{
		"system": dks.GerritCRS,
		"id":     dks.ChangelistIDThatAttemptsToFixIOS,
	})
	wh.ChangelistSearchRedirect(w, r)
	assert.Equal(t, http.StatusTemporaryRedirect, w.Code)
	headers := w.Header()
	assert.Equal(t, []string{"/search?issue=CL_fix_ios&crs=gerrit&patchsets=3&corpus=corners"}, headers["Location"])
}

func TestChangelistSearchRedirect_CLHasNoUntriagedDigests_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	existingData := dks.Build()
	existingData.SecondaryBranchValues = nil // remove all ingested data from CLs.
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID: dks.GerritCRS,
				},
			},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/cl/gerrit/CL_fix_ios", nil)
	r = mux.SetURLVars(r, map[string]string{
		"system": dks.GerritCRS,
		"id":     dks.ChangelistIDThatAttemptsToFixIOS,
	})
	wh.ChangelistSearchRedirect(w, r)
	assert.Equal(t, http.StatusTemporaryRedirect, w.Code)
	headers := w.Header()
	assert.Equal(t, []string{"/search?issue=CL_fix_ios&crs=gerrit&patchsets=3"}, headers["Location"])
}

func TestChangelistSearchRedirect_CLDoesNotExist_404Error(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID: dks.GerritCRS,
				},
			},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/cl/gerrit/1234", nil)
	r = mux.SetURLVars(r, map[string]string{
		"system": dks.GerritCRS,
		"id":     "1234",
	})
	wh.ChangelistSearchRedirect(w, r)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetActionableDigests_ReturnsCorrectResults(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}

	test := func(crs, clID, psID string, expected []corpusAndCount) {
		qPSID := sql.Qualify(crs, psID)
		corpora, err := wh.getActionableDigests(ctx, crs, clID, qPSID)
		require.NoError(t, err)
		assert.Equal(t, expected, corpora)
	}

	test(dks.GerritCRS, dks.ChangelistIDThatAttemptsToFixIOS, dks.PatchSetIDFixesIPadButNotIPhone,
		[]corpusAndCount{
			// DigestB01Pos has been incorrectly triaged on this CL as untriaged.
			{Corpus: dks.CornersCorpus, Count: 1},
			// DigestC07Unt_CL is produced by the iPad
			{Corpus: dks.RoundCorpus, Count: 1},
		})
	test(dks.GerritInternalCRS, dks.ChangelistIDThatAddsNewTests, dks.PatchsetIDAddsNewCorpus,
		[]corpusAndCount{
			// DigestC04Unt and DigestC03Unt are produced on this PS
			{Corpus: dks.RoundCorpus, Count: 2},
			// DigestBlank is produced by the text test on this PS
			{Corpus: dks.TextCorpus, Count: 1},
		})
	test(dks.GerritInternalCRS, dks.ChangelistIDThatAddsNewTests, dks.PatchsetIDAddsNewCorpusAndTest,
		[]corpusAndCount{
			// DigestC04Unt, DigestC03Unt, and DigestE03Unt_CL are produced on this PS
			{Corpus: dks.RoundCorpus, Count: 3},
			// The Text corpus no longer produces DigestBlank, but DigestD01Pos_CL
		})
}

func TestGetFlakyTracesData_ThresholdZero_ReturnAllTraces(t *testing.T) {
	unittest.SmallTest(t)

	mi := &mock_indexer.IndexSource{}
	defer mi.AssertExpectations(t)

	commits := bug_revert.MakeTestCommits()
	fis := makeBugRevertIndex(len(commits))
	mi.On("GetIndex").Return(fis)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mi,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{
		"minUniqueDigests": "0",
	})
	wh.GetFlakyTracesData(w, r)
	const expectedRV = `{"traces":[` +
		`{"trace_id":",device=gamma,name=test_two,source_type=gm,","unique_digests_count":4},` +
		`{"trace_id":",device=alpha,name=test_one,source_type=gm,","unique_digests_count":2},` +
		`{"trace_id":",device=alpha,name=test_two,source_type=gm,","unique_digests_count":2},` +
		`{"trace_id":",device=beta,name=test_one,source_type=gm,","unique_digests_count":2},` +
		`{"trace_id":",device=delta,name=test_one,source_type=gm,","unique_digests_count":2},` +
		`{"trace_id":",device=delta,name=test_two,source_type=gm,","unique_digests_count":2},` +
		`{"trace_id":",device=gamma,name=test_one,source_type=gm,","unique_digests_count":2},` +
		`{"trace_id":",device=beta,name=test_two,source_type=gm,","unique_digests_count":1}],` +
		`"tile_size":5,"num_flaky":8,"num_traces":8}`
	assertJSONResponseWas(t, 200, expectedRV, w)
}

func TestGetFlakyTracesData_NonZeroThreshold_ReturnsFlakyTracesAboveThreshold(t *testing.T) {
	unittest.SmallTest(t)

	mi := &mock_indexer.IndexSource{}
	defer mi.AssertExpectations(t)

	commits := bug_revert.MakeTestCommits()
	fis := makeBugRevertIndex(len(commits))
	mi.On("GetIndex").Return(fis)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mi,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{
		"minUniqueDigests": "4",
	})
	wh.GetFlakyTracesData(w, r)
	const expectedRV = `{"traces":[{"trace_id":",device=gamma,name=test_two,source_type=gm,","unique_digests_count":4}],"tile_size":5,"num_flaky":1,"num_traces":8}`
	assertJSONResponseWas(t, 200, expectedRV, w)
}

func TestGetFlakyTracesData_NoTracesAboveThreshold_ReturnsZeroTraces(t *testing.T) {
	unittest.SmallTest(t)

	mi := &mock_indexer.IndexSource{}

	commits := bug_revert.MakeTestCommits()
	fis := makeBugRevertIndex(len(commits))
	mi.On("GetIndex").Return(fis)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Indexer: mi,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	wh.GetFlakyTracesData(w, r)
	const expectedRV = `{"traces":null,"tile_size":5,"num_flaky":0,"num_traces":8}`
	assertJSONResponseWas(t, 200, expectedRV, w)
}

func TestListTestsHandler_ValidQueries_Success(t *testing.T) {
	unittest.SmallTest(t)

	test := func(name, targetURL, expectedJSON string) {
		t.Run(name, func(t *testing.T) {
			mi := &mock_indexer.IndexSource{}

			fis := makeBugRevertIndexWithIgnores(makeIgnoreRules(), 1)
			mi.On("GetIndex").Return(fis)

			wh := Handlers{
				HandlersConfig: HandlersConfig{
					Indexer: mi,
				},
				anonymousExpensiveQuota: rate.NewLimiter(rate.Inf, 1),
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, targetURL, nil)
			wh.ListTestsHandler(w, r)
			assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
		})
	}

	test("all GM tests at head from all traces", "/json/list?corpus=gm&include_ignored_traces=true",
		`{"tests":[{"name":"test_one","positive_digests":1,"negative_digests":0,"untriaged_digests":0,"total_digests":1},`+
			`{"name":"test_two","positive_digests":2,"negative_digests":0,"untriaged_digests":1,"total_digests":3}]}`)

	test("all GM tests for device delta at head from all traces", "/json/list?corpus=gm&trace_values=device%3Ddelta&include_ignored_traces=true",
		`{"tests":[{"name":"test_one","positive_digests":1,"negative_digests":0,"untriaged_digests":0,"total_digests":1},`+
			`{"name":"test_two","positive_digests":0,"negative_digests":0,"untriaged_digests":1,"total_digests":1}]}`)

	// Reminder that device delta and test_two match ignore rules
	test("all GM tests", "/json/list?corpus=gm",
		`{"tests":[{"name":"test_one","positive_digests":1,"negative_digests":0,"untriaged_digests":0,"total_digests":1}]}`)

	test("all GM tests at head", "/json/list?corpus=gm&at_head_only=true",
		`{"tests":[{"name":"test_one","positive_digests":1,"negative_digests":0,"untriaged_digests":0,"total_digests":1}]}`)

	test("all GM tests for device beta", "/json/list?corpus=gm&trace_values=device%3Dbeta",
		`{"tests":[{"name":"test_one","positive_digests":1,"negative_digests":0,"untriaged_digests":0,"total_digests":1}]}`)

	test("all GM tests for device delta at head", "/json/list?corpus=gm&trace_values=device%3Ddelta",
		`{"tests":[]}`)

	test("non existent corpus", "/json/list?corpus=notthere", `{"tests":[]}`)
}

func TestListTestsHandler_InvalidQueries_BadRequestError(t *testing.T) {
	unittest.SmallTest(t)

	test := func(name, targetURL string) {
		t.Run(name, func(t *testing.T) {
			mi := &mock_indexer.IndexSource{}

			fis := makeBugRevertIndexWithIgnores(makeIgnoreRules(), 1)
			mi.On("GetIndex").Return(fis)

			wh := Handlers{
				HandlersConfig: HandlersConfig{
					Indexer: mi,
				},
				anonymousExpensiveQuota: rate.NewLimiter(rate.Inf, 1),
			}
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, targetURL, nil)
			wh.ListTestsHandler(w, r)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		})
	}

	test("missing corpus", "/json/list")

	test("empty corpus", "/json/list?corpus=")

	test("invalid trace values", "/json/list?corpus=gm&trace_values=%zz")
}

func TestDiffHandler_Success(t *testing.T) {
	unittest.SmallTest(t)

	ms := &mock_search.SearchAPI{}

	const testAlpha = types.TestName("alpha")
	const leftDigest = types.Digest("11111111111111111111111111111111")
	const rightDigest = types.Digest("22222222222222222222222222222222")
	ms.On("DiffDigests", testutils.AnyContext, testAlpha, leftDigest, rightDigest, "", "").Return(&frontend.DigestComparison{
		// Arbitrary data from a search unit test.
		Left: frontend.LeftDiffInfo{
			Test:   testAlpha,
			Digest: leftDigest,
			Status: expectations.Untriaged,
			ParamSet: paramtools.ParamSet{
				"device":              []string{data.BullheadDevice},
				types.PrimaryKeyField: []string{string(data.AlphaTest)},
				types.CorpusField:     []string{"gm"},
				"ext":                 {data.PNGExtension},
			},
		},
		Right: frontend.SRDiffDigest{
			Digest:           rightDigest,
			Status:           expectations.Positive,
			NumDiffPixels:    13,
			PixelDiffPercent: 0.5,
			MaxRGBADiffs:     [4]int{8, 9, 10, 11},
			DimDiffer:        true,
			CombinedMetric:   4.2,
			ParamSet: paramtools.ParamSet{
				"device":              []string{data.AnglerDevice, data.CrosshatchDevice},
				types.PrimaryKeyField: []string{string(data.AlphaTest)},
				types.CorpusField:     []string{"gm"},
				"ext":                 {data.PNGExtension},
			},
		},
	}, nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			SearchAPI: ms,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v1/diff?test=alpha&left=11111111111111111111111111111111&right=22222222222222222222222222222222", nil)
	wh.DiffHandler(w, r)
	const expectedResponse = `{"left":{"test":"alpha","digest":"11111111111111111111111111111111","status":"untriaged","triage_history":null,"paramset":{"device":["bullhead"],"ext":["png"],"name":["test_alpha"],"source_type":["gm"]}},"right":{"numDiffPixels":13,"combinedMetric":4.2,"pixelDiffPercent":0.5,"maxRGBADiffs":[8,9,10,11],"dimDiffer":true,"digest":"22222222222222222222222222222222","status":"positive","paramset":{"device":["angler","crosshatch"],"ext":["png"],"name":["test_alpha"],"source_type":["gm"]}}}`
	assertJSONResponseWas(t, http.StatusOK, expectedResponse, w)
}

func TestImageHandler_SingleKnownImage_CorrectBytesReturned(t *testing.T) {
	unittest.SmallTest(t)

	mgc := &mocks.GCSClient{}
	mgc.On("GetImage", testutils.AnyContext, types.Digest("0123456789abcdef0123456789abcdef")).Return([]byte("some png bytes"), nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			GCSClient: mgc,
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/img/images/0123456789abcdef0123456789abcdef.png", nil)
	wh.ImageHandler(w, r)
	assertImageResponseWas(t, []byte("some png bytes"), w)
}

func TestImageHandler_SingleUnknownImage_404Returned(t *testing.T) {
	unittest.SmallTest(t)

	mgc := &mocks.GCSClient{}
	mgc.On("GetImage", testutils.AnyContext, mock.Anything).Return(nil, errors.New("unknown"))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			GCSClient: mgc,
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/img/images/0123456789abcdef0123456789abcdef.png", nil)
	wh.ImageHandler(w, r)
	assert.Equal(t, http.StatusNotFound, w.Result().StatusCode)
}

func TestImageHandler_TwoKnownImages_DiffReturned(t *testing.T) {
	unittest.SmallTest(t)

	image1 := loadAsPNGBytes(t, one_by_five.ImageOne)
	image2 := loadAsPNGBytes(t, one_by_five.ImageTwo)
	mgc := &mocks.GCSClient{}
	// These digests are arbitrary - they do not match the provided images.
	mgc.On("GetImage", testutils.AnyContext, types.Digest("11111111111111111111111111111111")).Return(image1, nil)
	mgc.On("GetImage", testutils.AnyContext, types.Digest("22222222222222222222222222222222")).Return(image2, nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			GCSClient: mgc,
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/img/diffs/11111111111111111111111111111111-22222222222222222222222222222222.png", nil)
	wh.ImageHandler(w, r)
	// The images are different in 1 channel per pixel. The first 4 pixels (lines) are a light
	// orange color, the last one is a light blue color (because it differs only in alpha).
	assertDiffImageWas(t, w, `! SKTEXTSIMPLE
1 5
0xfdd0a2ff
0xfdd0a2ff
0xfdd0a2ff
0xfdd0a2ff
0xc6dbefff`)
}

func TestImageHandler_OneUnknownImage_404Returned(t *testing.T) {
	unittest.SmallTest(t)

	image1 := loadAsPNGBytes(t, one_by_five.ImageOne)
	mgc := &mocks.GCSClient{}
	// These digests are arbitrary - they do not match the provided images.
	mgc.On("GetImage", testutils.AnyContext, types.Digest("11111111111111111111111111111111")).Return(image1, nil)
	mgc.On("GetImage", testutils.AnyContext, types.Digest("22222222222222222222222222222222")).Return(nil, errors.New("unknown"))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			GCSClient: mgc,
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/img/diffs/11111111111111111111111111111111-22222222222222222222222222222222.png", nil)
	wh.ImageHandler(w, r)
	assert.Equal(t, http.StatusNotFound, w.Result().StatusCode)
}

func TestImageHandler_TwoUnknownImages_404Returned(t *testing.T) {
	unittest.SmallTest(t)

	mgc := &mocks.GCSClient{}
	mgc.On("GetImage", testutils.AnyContext, types.Digest("11111111111111111111111111111111")).Return(nil, errors.New("unknown"))
	mgc.On("GetImage", testutils.AnyContext, types.Digest("22222222222222222222222222222222")).Return(nil, errors.New("unknown"))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			GCSClient: mgc,
		},
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/img/diffs/11111111111111111111111111111111-22222222222222222222222222222222.png", nil)
	wh.ImageHandler(w, r)
	assert.Equal(t, http.StatusNotFound, w.Result().StatusCode)
}

func TestImageHandler_InvalidRequest_404Returned(t *testing.T) {
	unittest.SmallTest(t)

	wh := Handlers{}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/img/diffs/not_valid.png", nil)
	wh.ImageHandler(w, r)
	assert.Equal(t, http.StatusNotFound, w.Result().StatusCode)
}

func TestImageHandler_InvalidImageFormat_404Returned(t *testing.T) {
	unittest.SmallTest(t)

	wh := Handlers{}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/img/images/0123456789abcdef0123456789abcdef.gif", nil)
	wh.ImageHandler(w, r)
	assert.Equal(t, http.StatusNotFound, w.Result().StatusCode)
}

func loadAsPNGBytes(t *testing.T, textImage string) []byte {
	img := text.MustToNRGBA(textImage)
	var buf bytes.Buffer
	require.NoError(t, encodeImg(&buf, img))
	return buf.Bytes()
}

func TestGetLinksBetween_SomeDiffMetricsExist_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))
	waitForSystemTime()
	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}

	links, err := wh.getLinksBetween(ctx, dks.DigestA01Pos, []types.Digest{
		dks.DigestA02Pos, dks.DigestA03Pos, dks.DigestA05Unt,
		"0123456789abcdef0123456789abcdef", // not a real digest
	})
	require.NoError(t, err)
	assert.Equal(t, map[types.Digest]float32{
		dks.DigestA02Pos: 56.25,
		dks.DigestA03Pos: 56.25,
		dks.DigestA05Unt: 3.125,
	}, links)
}

func TestGetLinksBetween_NoDiffMetricsExist_EmptyMapReturned(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))
	waitForSystemTime()
	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}

	links, err := wh.getLinksBetween(ctx, dks.DigestA01Pos, []types.Digest{
		"0123456789abcdef0123456789abcdef", // not a real digest
	})
	require.NoError(t, err)
	assert.Empty(t, links)
}

func TestChangelistSummaryHandler_ValidInput_CorrectJSONReturned(t *testing.T) {
	unittest.SmallTest(t)

	ms := &mock_search2.API{}
	ms.On("NewAndUntriagedSummaryForCL", testutils.AnyContext, "my-system_my_cl").Return(search2.NewAndUntriagedSummary{
		ChangelistID: "my_cl",
		PatchsetSummaries: []search2.PatchsetNewAndUntriagedSummary{{
			NewImages:            1,
			NewUntriagedImages:   2,
			TotalUntriagedImages: 3,
			PatchsetID:           "patchset1",
			PatchsetOrder:        1,
		}, {
			NewImages:            5,
			NewUntriagedImages:   6,
			TotalUntriagedImages: 7,
			PatchsetID:           "patchset8",
			PatchsetOrder:        8,
		}},
		LastUpdated: time.Date(2021, time.April, 1, 1, 1, 1, 0, time.UTC),
	}, nil)
	ms.On("ChangelistLastUpdated", testutils.AnyContext, "my-system_my_cl").Return(time.Date(2021, time.April, 1, 1, 1, 1, 0, time.UTC), nil)

	wh := initCaches(&Handlers{
		HandlersConfig: HandlersConfig{
			Search2API: ms,
			ReviewSystems: []clstore.ReviewSystem{{
				ID: "my-system",
			}},
		},
		anonymousGerritQuota: rate.NewLimiter(rate.Inf, 1),
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{
		"id":     "my_cl",
		"system": "my-system",
	})
	wh.ChangelistSummaryHandler(w, r)
	// Note this JSON had the patchsets sorted so the latest one is first.
	const expectedJSON = `{"changelist_id":"my_cl","patchsets":[{"new_images":5,"new_untriaged_images":6,"total_untriaged_images":7,"patchset_id":"patchset8","patchset_order":8},{"new_images":1,"new_untriaged_images":2,"total_untriaged_images":3,"patchset_id":"patchset1","patchset_order":1}],"outdated":false}`
	assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
}

func TestChangelistSummaryHandler_CachedValueStaleButUpdatesQuickly_ReturnsFreshResult(t *testing.T) {
	unittest.SmallTest(t)

	ms := &mock_search2.API{}
	// First call should have just one PS.
	ms.On("NewAndUntriagedSummaryForCL", testutils.AnyContext, "my-system_my_cl").Return(search2.NewAndUntriagedSummary{
		ChangelistID: "my_cl",
		PatchsetSummaries: []search2.PatchsetNewAndUntriagedSummary{{
			NewImages:            1,
			NewUntriagedImages:   2,
			TotalUntriagedImages: 3,
			PatchsetID:           "patchset1",
			PatchsetOrder:        1,
		}},
		LastUpdated: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
	}, nil).Once()
	// Second call should have two PS and the latest timestamp.
	ms.On("NewAndUntriagedSummaryForCL", testutils.AnyContext, "my-system_my_cl").Return(search2.NewAndUntriagedSummary{
		ChangelistID: "my_cl",
		PatchsetSummaries: []search2.PatchsetNewAndUntriagedSummary{{
			NewImages:            1,
			NewUntriagedImages:   2,
			TotalUntriagedImages: 3,
			PatchsetID:           "patchset1",
			PatchsetOrder:        1,
		}, {
			NewImages:            5,
			NewUntriagedImages:   6,
			TotalUntriagedImages: 7,
			PatchsetID:           "patchset8",
			PatchsetOrder:        8,
		}},
		LastUpdated: time.Date(2021, time.April, 1, 1, 1, 1, 0, time.UTC),
	}, nil).Once()
	ms.On("ChangelistLastUpdated", testutils.AnyContext, "my-system_my_cl").Return(time.Date(2021, time.April, 1, 1, 1, 1, 0, time.UTC), nil)

	wh := initCaches(&Handlers{
		HandlersConfig: HandlersConfig{
			Search2API: ms,
			ReviewSystems: []clstore.ReviewSystem{{
				ID: "my-system",
			}},
		},
		anonymousGerritQuota: rate.NewLimiter(rate.Inf, 1),
	})

	for i := 0; i < 10; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, requestURL, nil)
		r = mux.SetURLVars(r, map[string]string{
			"id":     "my_cl",
			"system": "my-system",
		})
		wh.ChangelistSummaryHandler(w, r)
		if i == 0 {
			continue
		}
		// Note this JSON had the patchsets sorted so the latest one is first.
		const expectedJSON = `{"changelist_id":"my_cl","patchsets":[{"new_images":5,"new_untriaged_images":6,"total_untriaged_images":7,"patchset_id":"patchset8","patchset_order":8},{"new_images":1,"new_untriaged_images":2,"total_untriaged_images":3,"patchset_id":"patchset1","patchset_order":1}],"outdated":false}`
		assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
	}
	ms.AssertExpectations(t)
}

func TestChangelistSummaryHandler_CachedValueStaleUpdatesSlowly_ReturnsStaleResult(t *testing.T) {
	unittest.SmallTest(t)

	ms := &mock_search2.API{}
	// First call should have just one PS.
	ms.On("NewAndUntriagedSummaryForCL", testutils.AnyContext, "my-system_my_cl").Return(search2.NewAndUntriagedSummary{
		ChangelistID: "my_cl",
		PatchsetSummaries: []search2.PatchsetNewAndUntriagedSummary{{
			NewImages:            1,
			NewUntriagedImages:   2,
			TotalUntriagedImages: 3,
			PatchsetID:           "patchset1",
			PatchsetOrder:        1,
		}},
		LastUpdated: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
	}, nil).Once()
	// Second call should have two PS and the latest timestamp.
	ms.On("NewAndUntriagedSummaryForCL", testutils.AnyContext, "my-system_my_cl").Return(func(context.Context, string) search2.NewAndUntriagedSummary {
		// This is longer than the time we wait before giving up and returning stale results.
		time.Sleep(2 * time.Second)
		return search2.NewAndUntriagedSummary{
			ChangelistID: "my_cl",
			PatchsetSummaries: []search2.PatchsetNewAndUntriagedSummary{{
				NewImages:            1,
				NewUntriagedImages:   2,
				TotalUntriagedImages: 3,
				PatchsetID:           "patchset1",
				PatchsetOrder:        1,
			}, {
				NewImages:            5,
				NewUntriagedImages:   6,
				TotalUntriagedImages: 7,
				PatchsetID:           "patchset8",
				PatchsetOrder:        8,
			}},
			LastUpdated: time.Date(2021, time.April, 1, 1, 1, 1, 0, time.UTC),
		}
	}, nil).Once()
	ms.On("ChangelistLastUpdated", testutils.AnyContext, "my-system_my_cl").Return(time.Date(2021, time.April, 1, 1, 1, 1, 0, time.UTC), nil)

	wh := initCaches(&Handlers{
		HandlersConfig: HandlersConfig{
			Search2API: ms,
			ReviewSystems: []clstore.ReviewSystem{{
				ID: "my-system",
			}},
		},
		anonymousGerritQuota: rate.NewLimiter(rate.Inf, 1),
	})

	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, requestURL, nil)
		r = mux.SetURLVars(r, map[string]string{
			"id":     "my_cl",
			"system": "my-system",
		})
		wh.ChangelistSummaryHandler(w, r)
		if i == 0 {
			continue
		}
		// Note this JSON is the first result marked as stale.
		const expectedJSON = `{"changelist_id":"my_cl","patchsets":[{"new_images":1,"new_untriaged_images":2,"total_untriaged_images":3,"patchset_id":"patchset1","patchset_order":1}],"outdated":true}`
		assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
	}
	ms.AssertExpectations(t)
}

func TestChangelistSummaryHandler_MissingCL_BadRequest(t *testing.T) {
	unittest.SmallTest(t)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			ReviewSystems: []clstore.ReviewSystem{{
				ID: "my-system",
			}},
		},
		anonymousGerritQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{
		"system": "my-system",
	})
	wh.ChangelistSummaryHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
}

func TestChangelistSummaryHandler_MissingSystem_BadRequest(t *testing.T) {
	unittest.SmallTest(t)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			ReviewSystems: []clstore.ReviewSystem{{
				ID: "my-system",
			}},
		},
		anonymousGerritQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{
		"id": "my_cl",
	})
	wh.ChangelistSummaryHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
}

func TestChangelistSummaryHandler_IncorrectSystem_BadRequest(t *testing.T) {
	unittest.SmallTest(t)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			ReviewSystems: []clstore.ReviewSystem{{
				ID: "my-system",
			}},
		},
		anonymousGerritQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{
		"id":     "my_cl",
		"system": "bad-system",
	})
	wh.ChangelistSummaryHandler(w, r)
	assert.Equal(t, http.StatusBadRequest, w.Result().StatusCode)
}

func TestChangelistSummaryHandler_SearchReturnsError_InternalServerError(t *testing.T) {
	unittest.SmallTest(t)

	ms := &mock_search2.API{}
	ms.On("ChangelistLastUpdated", testutils.AnyContext, "my-system_my_cl").Return(time.Time{}, errors.New("boom"))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Search2API: ms,
			ReviewSystems: []clstore.ReviewSystem{{
				ID: "my-system",
			}},
		},
		anonymousGerritQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{
		"id":     "my_cl",
		"system": "my-system",
	})
	wh.ChangelistSummaryHandler(w, r)
	assert.Equal(t, http.StatusInternalServerError, w.Result().StatusCode)
}

func TestStartCLCacheProcess_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := initCaches(&Handlers{
		HandlersConfig: HandlersConfig{
			Search2API: search2.New(db, 10),
			DB:         db,
		},
	})

	// Set the time to be a few days after both CLs in the sample data land.
	ctx = context.WithValue(ctx, now.ContextKey, time.Date(2020, time.December, 14, 0, 0, 0, 0, time.UTC))
	wh.startCLCacheProcess(ctx)
	require.Eventually(t, func() bool {
		return wh.clSummaryCache.Len() == 2
	}, 5*time.Second, 100*time.Millisecond)
	assert.True(t, wh.clSummaryCache.Contains("gerrit_CL_fix_ios"))
	assert.True(t, wh.clSummaryCache.Contains("gerrit-internal_CL_new_tests"))
}

func TestStatusHandler2_Success(t *testing.T) {
	unittest.SmallTest(t)

	wh := Handlers{statusCache: frontend.GUIStatus{
		LastCommit: frontend.Commit{
			ID:         "0000000110",
			Author:     dks.UserTwo,
			Subject:    "commit 110",
			Hash:       "f4412901bfb130a8774c0c719450d1450845f471",
			CommitTime: 1607644800, // "2020-12-11T00:00:00Z"
		},
		CorpStatus: []frontend.GUICorpusStatus{
			{
				Name:           dks.CornersCorpus,
				UntriagedCount: 0,
			},
			{
				Name:           dks.RoundCorpus,
				UntriagedCount: 3,
			},
		},
	}}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	wh.StatusHandler2(w, r)
	const expectedJSON = `{"lastCommit":{"commit_time":1607644800,"id":"0000000110","hash":"f4412901bfb130a8774c0c719450d1450845f471","author":"userTwo@example.com","message":"commit 110","cl_url":""},"corpStatus":[{"name":"corners","untriagedCount":0},{"name":"round","untriagedCount":3}]}`
	assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
}

func TestGetBlamesForUntriagedDigests_ValidInput_CorrectJSONReturned(t *testing.T) {
	unittest.SmallTest(t)

	ms := &mock_search2.API{}

	ms.On("GetBlamesForUntriagedDigests", testutils.AnyContext, "the_corpus").Return(search2.BlameSummaryV1{
		Ranges: []search2.BlameEntry{{
			CommitRange:           "000054321:000054322",
			TotalUntriagedDigests: 2,
			AffectedGroupings: []*search2.AffectedGrouping{{
				Grouping: paramtools.Params{
					types.CorpusField:     "the_corpus",
					types.PrimaryKeyField: "alpha",
				},
				UntriagedDigests: 1,
				SampleDigest:     "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			}, {
				Grouping: paramtools.Params{
					types.CorpusField:     "the_corpus",
					types.PrimaryKeyField: "beta",
				},
				UntriagedDigests: 1,
				SampleDigest:     "dddddddddddddddddddddddddddddddd",
			}},
			Commits: []frontend.Commit{{
				CommitTime: 12345678000,
				Hash:       "1234567890abcdef1234567890abcdef12345678",
				ID:         "000054321",
				Author:     "user1@example.com",
				Subject:    "Probably broke something",
			}, {
				CommitTime: 12345678900,
				Hash:       "4567890abcdef1234567890abcdef1234567890a",
				ID:         "000054322",
				Author:     "user2@example.com",
				Subject:    "Might not have broke anything",
			}},
		}}}, nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Search2API: ms,
		},
		anonymousExpensiveQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/byblame?query=source_type%3Dthe_corpus", nil)
	wh.ByBlameHandler2(w, r)
	const expectedJSON = `{"data":[{"groupID":"000054321:000054322","nDigests":2,"nTests":2,"affectedTests":[{"test":"alpha","num":1,"sample_digest":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},{"test":"beta","num":1,"sample_digest":"dddddddddddddddddddddddddddddddd"}],"commits":[{"commit_time":12345678000,"id":"000054321","hash":"1234567890abcdef1234567890abcdef12345678","author":"user1@example.com","message":"Probably broke something","cl_url":""},{"commit_time":12345678900,"id":"000054322","hash":"4567890abcdef1234567890abcdef1234567890a","author":"user2@example.com","message":"Might not have broke anything","cl_url":""}]}]}`
	assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
}

func TestClusterDiffHandler2_ValidInput_CorrectJSONReturned(t *testing.T) {
	unittest.SmallTest(t)

	ms := &mock_search2.API{}

	expectedOptions := search2.ClusterOptions{
		Grouping: paramtools.Params{
			types.CorpusField:     "infra",
			types.PrimaryKeyField: "infra-sk_paramset-sk_many-paramsets_no-titles",
		},
		Filters: paramtools.ParamSet{
			"build_system": []string{"bazel", "webpack"},
		},
		IncludePositiveDigests:  true,
		IncludeNegativeDigests:  false,
		IncludeUntriagedDigests: true,
	}

	ms.On("GetCluster", testutils.AnyContext, expectedOptions).Return(frontend.ClusterDiffResult{
		Nodes: []frontend.Node{
			{Digest: dks.DigestB01Pos, Status: expectations.Positive},
		},
		Links: []frontend.Link{},
		Test:  "my_test",
		ParamsetByDigest: map[types.Digest]paramtools.ParamSet{
			dks.DigestB01Pos: {
				"key1": []string{"value1", "value2"},
			},
		},
		ParamsetsUnion: paramtools.ParamSet{
			"key1": []string{"value1", "value2"},
		},
	}, nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Search2API: ms,
		},
		anonymousExpensiveQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	// Taken from a production request
	url := `/json/v2/clusterdiff?neg=false&pos=true&query=build_system%3Dbazel%26build_system%3Dwebpack%26name%3Dinfra-sk_paramset-sk_many-paramsets_no-titles&source_type=infra&unt=true`
	r := httptest.NewRequest(http.MethodGet, url, nil)
	wh.ClusterDiffHandler2(w, r)
	const expectedJSON = `{"nodes":[{"name":"b01b01b01b01b01b01b01b01b01b01b0","status":"positive"}],"links":[],"test":"my_test","paramsetByDigest":{"b01b01b01b01b01b01b01b01b01b01b0":{"key1":["value1","value2"]}},"paramsetsUnion":{"key1":["value1","value2"]}}`
	assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
}

func TestCommitsHandler2_CorrectJSONReturned(t *testing.T) {
	unittest.SmallTest(t)

	ms := &mock_search2.API{}

	ms.On("GetCommitsInWindow", testutils.AnyContext).Return([]frontend.Commit{{
		CommitTime: 100000000,
		ID:         "commit_1",
		Hash:       "aaaaaaaaaaaaaaaaaaaaaaaaa",
		Author:     "user@example.com",
		Subject:    "first commit",
	}, {
		CommitTime: 200000000,
		ID:         "commit_2",
		Hash:       "bbbbbbbbbbbbbbbbbbbbbbbbb",
		Author:     "user@example.com",
		Subject:    "second commit",
	}}, nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Search2API: ms,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	wh.CommitsHandler2(w, r)
	const expectedJSON = `[{"commit_time":100000000,"id":"commit_1","hash":"aaaaaaaaaaaaaaaaaaaaaaaaa","author":"user@example.com","message":"first commit","cl_url":""},{"commit_time":200000000,"id":"commit_2","hash":"bbbbbbbbbbbbbbbbbbbbbbbbb","author":"user@example.com","message":"second commit","cl_url":""}]`
	assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
}

func TestDigestListHandler2_CorrectJSONReturned(t *testing.T) {
	unittest.SmallTest(t)

	ms := &mock_search2.API{}

	expectedGrouping := paramtools.Params{
		types.PrimaryKeyField: "ThisIsTheOnlyTest",
		types.CorpusField:     "whatever",
	}

	ms.On("GetDigestsForGrouping", testutils.AnyContext, expectedGrouping).Return(frontend.DigestListResponse{
		Digests: []types.Digest{dks.DigestC01Pos, dks.DigestC02Pos}}, nil)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			Search2API: ms,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/digests?grouping=name%3DThisIsTheOnlyTest%26source_type%3Dwhatever", nil)
	wh.DigestListHandler2(w, r)
	const expectedJSON = `{"digests":["c01c01c01c01c01c01c01c01c01c01c0","c02c02c02c02c02c02c02c02c02c02c0"]}`
	assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
}

func TestDigestListHandler2_GroupingOmitted_Error(t *testing.T) {
	unittest.SmallTest(t)

	wh := Handlers{
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/digests", nil)
	wh.DigestListHandler2(w, r)
	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestGetGroupingForTest_GroupingExists_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}

	ps, err := wh.getGroupingForTest(ctx, dks.CircleTest)
	require.NoError(t, err)
	assert.Equal(t, paramtools.Params{
		types.CorpusField:     dks.RoundCorpus,
		types.PrimaryKeyField: dks.CircleTest,
	}, ps)
}

func TestGetGroupingForTest_GroupingDoesNotExist_ReturnsError(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}

	_, err := wh.getGroupingForTest(ctx, "this test does not exist")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rows in result")
}

func TestPatchsetsAndTryjobsForCL2_ExistingCL_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID:          dks.GerritInternalCRS,
					URLTemplate: "www.example.com/gerrit/%s",
				},
			},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/changelist/gerrit-internal/CL_fix_ios", nil)
	r = mux.SetURLVars(r, map[string]string{
		"system": dks.GerritInternalCRS,
		"id":     dks.ChangelistIDThatAddsNewTests,
	})
	wh.PatchsetsAndTryjobsForCL2(w, r)
	const expectedJSON = `{"cl":{"system":"gerrit-internal","id":"CL_new_tests","owner":"userTwo@example.com","status":"open","subject":"Increase test coverage","updated":"2020-12-12T09:20:33Z","url":"www.example.com/gerrit/CL_new_tests"},"patch_sets":[{"id":"gerrit-internal_PS_adds_new_corpus_and_test","order":4,"try_jobs":[{"id":"buildbucketInternal_tryjob_05_windows","name":"Test-Windows10.3-ALL","updated":"2020-12-12T09:00:00Z","system":"buildbucketInternal","url":"https://cr-buildbucket.appspot.com/build/buildbucketInternal_tryjob_05_windows"},{"id":"buildbucketInternal_tryjob_06_walleye","name":"Test-Walleye-ALL","updated":"2020-12-12T09:20:33Z","system":"buildbucketInternal","url":"https://cr-buildbucket.appspot.com/build/buildbucketInternal_tryjob_06_walleye"}]},{"id":"gerrit-internal_PS_adds_new_corpus","order":1,"try_jobs":[{"id":"buildbucketInternal_tryjob_04_windows","name":"Test-Windows10.3-ALL","updated":"2020-12-12T08:09:10Z","system":"buildbucketInternal","url":"https://cr-buildbucket.appspot.com/build/buildbucketInternal_tryjob_04_windows"}]}],"num_total_patch_sets":2}`
	assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
}

func TestPatchsetsAndTryjobsForCL2_InvalidCL_ReturnsErrorCode(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{
				{
					ID:          dks.GerritCRS,
					URLTemplate: "www.example.com/gerrit/%s",
				},
			},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/changelist/gerrit/not-a-real-cl", nil)
	r = mux.SetURLVars(r, map[string]string{
		"system": dks.GerritCRS,
		"id":     "not-a-real-cl",
	})
	wh.PatchsetsAndTryjobsForCL2(w, r)
	resp := w.Result()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestTriageLogHandler2_PrimaryBranch_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/triagelog", nil)
	wh.TriageLogHandler2(w, r)
	const expectedJSON = `{"offset":0,"size":20,"total":11,"entries":[` +
		`{"id":"4275c86b-d64a-ae38-d931-24ea9b94c551","name":"userFour@example.com","ts":1607691600000,"details":[{"grouping":{"name":"square","source_type":"corners"},"digest":"a09a09a09a09a09a09a09a09a09a09a0","label_before":"untriaged","label_after":"negative"}]},` +
		`{"id":"734d45d8-555a-aca5-6c55-c45039e43f89","name":"fuzzy","ts":1607685060000,"details":[{"grouping":{"name":"square","source_type":"corners"},"digest":"a08a08a08a08a08a08a08a08a08a08a0","label_before":"untriaged","label_after":"positive"}]},` +
		`{"id":"fe054e2f-822a-7e0c-3dfb-0e9586adffe4","name":"userThree@example.com","ts":1607595010000,"details":[{"grouping":{"name":"square","source_type":"corners"},"digest":"a07a07a07a07a07a07a07a07a07a07a0","label_before":"untriaged","label_after":"positive"}]},` +
		`{"id":"65693cef-0220-f0aa-3503-1d5df6548ac9","name":"userThree@example.com","ts":1591877595000,"details":[{"grouping":{"name":"circle","source_type":"round"},"digest":"00000000000000000000000000000000","label_before":"untriaged","label_after":"negative"}]},` +
		`{"id":"a23a2b37-344e-83a1-fc71-c72f8071280a","name":"userThree@example.com","ts":1591877594000,"details":[{"grouping":{"name":"square","source_type":"corners"},"digest":"a03a03a03a03a03a03a03a03a03a03a0","label_before":"untriaged","label_after":"positive"}]},` +
		`{"id":"c2b9779e-a0e7-9d48-7c91-0edfa48db809","name":"userOne@example.com","ts":1591518188000,"details":[{"grouping":{"name":"square","source_type":"corners"},"digest":"a01a01a01a01a01a01a01a01a01a01a0","label_before":"untriaged","label_after":"positive"},{"grouping":{"name":"square","source_type":"corners"},"digest":"a02a02a02a02a02a02a02a02a02a02a0","label_before":"untriaged","label_after":"positive"}]},` +
		`{"id":"f9adaa96-df23-2128-2120-53ea2d57536b","name":"userTwo@example.com","ts":1591517708000,"details":[{"grouping":{"name":"triangle","source_type":"corners"},"digest":"b04b04b04b04b04b04b04b04b04b04b0","label_before":"positive","label_after":"negative"}]},` +
		`{"id":"931323d9-926d-3a24-0350-6440a54d52cc","name":"userTwo@example.com","ts":1591517707000,"details":[{"grouping":{"name":"triangle","source_type":"corners"},"digest":"b04b04b04b04b04b04b04b04b04b04b0","label_before":"untriaged","label_after":"positive"}]},` +
		`{"id":"1d35d070-9ec6-1d0a-e7bd-1184870323b3","name":"userTwo@example.com","ts":1591517704000,"details":[{"grouping":{"name":"triangle","source_type":"corners"},"digest":"b03b03b03b03b03b03b03b03b03b03b0","label_before":"untriaged","label_after":"negative"}]},` +
		`{"id":"fbbe2efb-5fc0-bd3c-76fa-b52714bad960","name":"userOne@example.com","ts":1591517383000,"details":[{"grouping":{"name":"triangle","source_type":"corners"},"digest":"b01b01b01b01b01b01b01b01b01b01b0","label_before":"untriaged","label_after":"positive"},{"grouping":{"name":"triangle","source_type":"corners"},"digest":"b02b02b02b02b02b02b02b02b02b02b0","label_before":"untriaged","label_after":"positive"}]},` +
		`{"id":"94a63df2-33d3-97ad-f4d7-341f76ff8cb6","name":"userOne@example.com","ts":1591517350000,"details":[{"grouping":{"name":"circle","source_type":"round"},"digest":"c01c01c01c01c01c01c01c01c01c01c0","label_before":"untriaged","label_after":"positive"},{"grouping":{"name":"circle","source_type":"round"},"digest":"c02c02c02c02c02c02c02c02c02c02c0","label_before":"untriaged","label_after":"positive"}]}]}`
	assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
}

func TestTriageLogHandler2_RespectsPagination_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/triagelog?size=2&offset=1", nil)
	wh.TriageLogHandler2(w, r)
	const expectedJSON = `{"offset":1,"size":2,"total":11,"entries":[` +
		`{"id":"734d45d8-555a-aca5-6c55-c45039e43f89","name":"fuzzy","ts":1607685060000,"details":[{"grouping":{"name":"square","source_type":"corners"},"digest":"a08a08a08a08a08a08a08a08a08a08a0","label_before":"untriaged","label_after":"positive"}]},` +
		`{"id":"fe054e2f-822a-7e0c-3dfb-0e9586adffe4","name":"userThree@example.com","ts":1607595010000,"details":[{"grouping":{"name":"square","source_type":"corners"},"digest":"a07a07a07a07a07a07a07a07a07a07a0","label_before":"untriaged","label_after":"positive"}]}]}`
	assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
}

func TestTriageLogHandler2_ValidChangelist_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{
				{ID: dks.GerritCRS},
			},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/triagelog?crs=gerrit&changelist_id=CL_fix_ios", nil)
	wh.TriageLogHandler2(w, r)
	const expectedJSON = `{"offset":0,"size":20,"total":2,"entries":[` +
		`{"id":"f3d0959f-bb1d-aea6-050d-23022044eff3","name":"userOne@example.com","ts":1607576402000,"details":[{"grouping":{"name":"circle","source_type":"round"},"digest":"c06c06c06c06c06c06c06c06c06c06c0","label_before":"untriaged","label_after":"positive"}]},` +
		`{"id":"955d5de7-c792-e317-bd7b-069e55bd76df","name":"userOne@example.com","ts":1607576400000,"details":[{"grouping":{"name":"triangle","source_type":"corners"},"digest":"b01b01b01b01b01b01b01b01b01b01b0","label_before":"positive","label_after":"untriaged"}]}]}`
	assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
}

func TestTriageLogHandler2_InvalidChangelist_ReturnsEmptyEntries(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{
				{ID: dks.GerritCRS},
			},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/triagelog?crs=gerrit&changelist_id=not_real", nil)
	wh.TriageLogHandler2(w, r)
	const expectedJSON = `{"offset":0,"size":20,"total":0,"entries":[]}`
	assertJSONResponseWas(t, http.StatusOK, expectedJSON, w)
}

func TestUndoExpectationChanges_ExistingRecordOnPrimaryBranch_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	existingData := dks.Build()
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))

	// Find the record that triages DigestA01Pos and DigestA02Pos positive for the square test
	// on the primary branch. This record ID should be constant, but we look it up to avoid
	// test brittleness.
	var recordID uuid.UUID
	for _, record := range existingData.ExpectationRecords {
		if record.TriageTime.Format(time.RFC3339) == "2020-06-07T08:23:08Z" {
			recordID = record.ExpectationRecordID
		}
	}
	require.NotZero(t, recordID)
	undoTime := time.Date(2021, time.July, 4, 4, 4, 4, 0, time.UTC)
	const undoUser = "undo_user@example.com"
	_, squareGroupingID := sql.SerializeMap(paramtools.Params{
		types.CorpusField:     dks.CornersCorpus,
		types.PrimaryKeyField: dks.SquareTest,
	})

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}
	ctx = context.WithValue(ctx, now.ContextKey, undoTime)
	err := wh.undoExpectationChanges(ctx, recordID.String(), undoUser)
	require.NoError(t, err)

	row := db.QueryRow(ctx, `SELECT expectation_record_id FROM ExpectationRecords WHERE user_name = $1`, undoUser)
	var newRecordID uuid.UUID
	require.NoError(t, row.Scan(&newRecordID))

	records := sqltest.GetAllRows(ctx, t, db, "ExpectationRecords", &schema.ExpectationRecordRow{})
	assert.Contains(t, records, schema.ExpectationRecordRow{
		ExpectationRecordID: newRecordID,
		UserName:            undoUser,
		TriageTime:          undoTime,
		NumChanges:          2,
	})

	deltas := sqltest.GetAllRows(ctx, t, db, "ExpectationDeltas", &schema.ExpectationDeltaRow{})
	assert.Contains(t, deltas, schema.ExpectationDeltaRow{
		ExpectationRecordID: newRecordID,
		GroupingID:          squareGroupingID,
		Digest:              d(dks.DigestA01Pos),
		LabelBefore:         schema.LabelPositive,
		LabelAfter:          schema.LabelUntriaged,
	})
	assert.Contains(t, deltas, schema.ExpectationDeltaRow{
		ExpectationRecordID: newRecordID,
		GroupingID:          squareGroupingID,
		Digest:              d(dks.DigestA02Pos),
		LabelBefore:         schema.LabelPositive,
		LabelAfter:          schema.LabelUntriaged,
	})

	exps := sqltest.GetAllRows(ctx, t, db, "Expectations", &schema.ExpectationRow{})
	assert.Contains(t, exps, schema.ExpectationRow{
		GroupingID:          squareGroupingID,
		Digest:              d(dks.DigestA01Pos),
		Label:               schema.LabelUntriaged,
		ExpectationRecordID: &newRecordID,
	})
	assert.Contains(t, exps, schema.ExpectationRow{
		GroupingID:          squareGroupingID,
		Digest:              d(dks.DigestA02Pos),
		Label:               schema.LabelUntriaged,
		ExpectationRecordID: &newRecordID,
	})
}

func TestUndoExpectationChanges_ExistingRecordOnCL_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	existingData := dks.Build()
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))

	// Find the record that incorrectly triages DigestB01Pos on the CL CL_fix_ios
	var recordID uuid.UUID
	var expectedBranchName = "gerrit_CL_fix_ios"
	for _, record := range existingData.ExpectationRecords {
		if record.BranchName == nil || *record.BranchName != expectedBranchName {
			continue
		}
		if record.TriageTime.Format(time.RFC3339) == "2020-12-10T05:00:00Z" {
			recordID = record.ExpectationRecordID
		}
	}
	require.NotZero(t, recordID)
	undoTime := time.Date(2021, time.July, 4, 4, 4, 4, 0, time.UTC)
	const undoUser = "undo_user@example.com"
	_, triangleGroupingID := sql.SerializeMap(paramtools.Params{
		types.CorpusField:     dks.CornersCorpus,
		types.PrimaryKeyField: dks.TriangleTest,
	})

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}
	ctx = context.WithValue(ctx, now.ContextKey, undoTime)
	err := wh.undoExpectationChanges(ctx, recordID.String(), undoUser)
	require.NoError(t, err)

	row := db.QueryRow(ctx, `SELECT expectation_record_id FROM ExpectationRecords WHERE user_name = $1`, undoUser)
	var newRecordID uuid.UUID
	require.NoError(t, row.Scan(&newRecordID))

	records := sqltest.GetAllRows(ctx, t, db, "ExpectationRecords", &schema.ExpectationRecordRow{})
	assert.Contains(t, records, schema.ExpectationRecordRow{
		ExpectationRecordID: newRecordID,
		UserName:            undoUser,
		TriageTime:          undoTime,
		BranchName:          &expectedBranchName,
		NumChanges:          1,
	})

	deltas := sqltest.GetAllRows(ctx, t, db, "ExpectationDeltas", &schema.ExpectationDeltaRow{})
	assert.Contains(t, deltas, schema.ExpectationDeltaRow{
		ExpectationRecordID: newRecordID,
		GroupingID:          triangleGroupingID,
		Digest:              d(dks.DigestB01Pos),
		LabelBefore:         schema.LabelUntriaged,
		LabelAfter:          schema.LabelPositive,
	})

	exps := sqltest.GetAllRows(ctx, t, db, "SecondaryBranchExpectations", &schema.SecondaryBranchExpectationRow{})
	assert.Contains(t, exps, schema.SecondaryBranchExpectationRow{
		BranchName:          expectedBranchName,
		GroupingID:          triangleGroupingID,
		Digest:              d(dks.DigestB01Pos),
		Label:               schema.LabelPositive,
		ExpectationRecordID: newRecordID,
	})
}

func TestUndoExpectationChanges_UnknownID_ReturnsError(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}
	const undoUser = "undo_user@example.com"
	err := wh.undoExpectationChanges(ctx, "Not a valid ID", undoUser)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no expectation deltas")

	row := db.QueryRow(ctx, `SELECT expectation_record_id FROM ExpectationRecords WHERE user_name = $1`, undoUser)
	var notUsed uuid.UUID
	err = row.Scan(&notUsed)
	require.Error(t, err)
	assert.Equal(t, pgx.ErrNoRows, err)
}

func TestTriage2_SingleDigestOnPrimaryBranch_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	const user = "single_triage@example.com"
	fakeNow := time.Date(2021, time.July, 4, 4, 4, 4, 0, time.UTC)

	_, circleGroupingID := sql.SerializeMap(paramtools.Params{
		types.CorpusField:     dks.RoundCorpus,
		types.PrimaryKeyField: dks.CircleTest,
	})

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}

	tr := frontend.TriageRequest{
		TestDigestStatus: map[types.TestName]map[types.Digest]expectations.Label{
			dks.CircleTest: {
				dks.DigestC03Unt: expectations.Positive,
			},
		},
	}
	ctx = context.WithValue(ctx, now.ContextKey, fakeNow)
	require.NoError(t, wh.triage2(ctx, user, tr))

	latestRecord := sqltest.GetAllRows(ctx, t, db, "ExpectationRecords", &schema.ExpectationRecordRow{}).([]schema.ExpectationRecordRow)[0]
	newRecordID := latestRecord.ExpectationRecordID // randomly generated
	assert.Equal(t, schema.ExpectationRecordRow{
		ExpectationRecordID: newRecordID,
		UserName:            user,
		TriageTime:          fakeNow,
		NumChanges:          1,
	}, latestRecord)

	whereClause := `WHERE expectation_record_id = '` + newRecordID.String() + `'`
	newDeltas := sqltest.GetAllRows(ctx, t, db, "ExpectationDeltas", &schema.ExpectationDeltaRow{}, whereClause)
	assert.ElementsMatch(t, []schema.ExpectationDeltaRow{{
		ExpectationRecordID: newRecordID,
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestC03Unt),
		LabelBefore:         schema.LabelUntriaged,
		LabelAfter:          schema.LabelPositive,
	}}, newDeltas)

	exps := sqltest.GetAllRows(ctx, t, db, "Expectations", &schema.ExpectationRow{})
	assert.Contains(t, exps, schema.ExpectationRow{
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestC03Unt),
		Label:               schema.LabelPositive,
		ExpectationRecordID: &newRecordID,
	})
}

func TestTriage2_ImageMatchingAlgorithmSet_UsesAlgorithmNameAsAuthor(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	const user = "not_me@example.com"
	const algorithmName = "fuzzy"
	fakeNow := time.Date(2021, time.July, 4, 4, 4, 4, 0, time.UTC)

	_, circleGroupingID := sql.SerializeMap(paramtools.Params{
		types.CorpusField:     dks.RoundCorpus,
		types.PrimaryKeyField: dks.CircleTest,
	})

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}

	tr := frontend.TriageRequest{
		TestDigestStatus: map[types.TestName]map[types.Digest]expectations.Label{
			dks.CircleTest: {
				dks.DigestC03Unt: expectations.Positive,
			},
		},
		ImageMatchingAlgorithm: algorithmName,
	}
	ctx = context.WithValue(ctx, now.ContextKey, fakeNow)
	require.NoError(t, wh.triage2(ctx, user, tr))

	latestRecord := sqltest.GetAllRows(ctx, t, db, "ExpectationRecords", &schema.ExpectationRecordRow{}).([]schema.ExpectationRecordRow)[0]
	newRecordID := latestRecord.ExpectationRecordID // randomly generated
	assert.Equal(t, schema.ExpectationRecordRow{
		ExpectationRecordID: newRecordID,
		UserName:            algorithmName,
		TriageTime:          fakeNow,
		NumChanges:          1,
	}, latestRecord)

	whereClause := `WHERE expectation_record_id = '` + newRecordID.String() + `'`
	newDeltas := sqltest.GetAllRows(ctx, t, db, "ExpectationDeltas", &schema.ExpectationDeltaRow{}, whereClause)
	assert.ElementsMatch(t, []schema.ExpectationDeltaRow{{
		ExpectationRecordID: newRecordID,
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestC03Unt),
		LabelBefore:         schema.LabelUntriaged,
		LabelAfter:          schema.LabelPositive,
	}}, newDeltas)

	exps := sqltest.GetAllRows(ctx, t, db, "Expectations", &schema.ExpectationRow{})
	assert.Contains(t, exps, schema.ExpectationRow{
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestC03Unt),
		Label:               schema.LabelPositive,
		ExpectationRecordID: &newRecordID,
	})
}

func TestTriage2_BulkTriage_PrimaryBranch_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	const user = "bulk_triage@example.com"
	fakeNow := time.Date(2021, time.July, 4, 4, 4, 4, 0, time.UTC)
	// This recordID is what has DigestBlank triaged as negative. It should still be in place
	// after the bulk triage operation.
	existingRecordID, err := uuid.Parse("65693cef-0220-f0aa-3503-1d5df6548ac9")
	require.NoError(t, err)
	_, triangleGroupingID := sql.SerializeMap(paramtools.Params{
		types.CorpusField:     dks.CornersCorpus,
		types.PrimaryKeyField: dks.TriangleTest,
	})
	_, circleGroupingID := sql.SerializeMap(paramtools.Params{
		types.CorpusField:     dks.RoundCorpus,
		types.PrimaryKeyField: dks.CircleTest,
	})

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}

	tr := frontend.TriageRequest{
		TestDigestStatus: map[types.TestName]map[types.Digest]expectations.Label{
			dks.TriangleTest: {
				dks.DigestB01Pos: expectations.Untriaged,
				dks.DigestB02Pos: expectations.Negative,
			},
			dks.CircleTest: {
				dks.DigestC03Unt: expectations.Positive,
				dks.DigestBlank:  "", // pretend this has no closest, i.e. leave it unchanged.
			},
		},
	}
	ctx = context.WithValue(ctx, now.ContextKey, fakeNow)
	require.NoError(t, wh.triage2(ctx, user, tr))

	latestRecord := sqltest.GetAllRows(ctx, t, db, "ExpectationRecords", &schema.ExpectationRecordRow{}).([]schema.ExpectationRecordRow)[0]
	newRecordID := latestRecord.ExpectationRecordID // randomly generated
	assert.Equal(t, schema.ExpectationRecordRow{
		ExpectationRecordID: newRecordID,
		UserName:            user,
		TriageTime:          fakeNow,
		NumChanges:          3, // Only 3 deltas were applied
	}, latestRecord)

	whereClause := `WHERE expectation_record_id = '` + newRecordID.String() + `'`
	newDeltas := sqltest.GetAllRows(ctx, t, db, "ExpectationDeltas", &schema.ExpectationDeltaRow{}, whereClause)
	assert.ElementsMatch(t, []schema.ExpectationDeltaRow{{
		ExpectationRecordID: newRecordID,
		GroupingID:          triangleGroupingID,
		Digest:              d(dks.DigestB01Pos),
		LabelBefore:         schema.LabelPositive,
		LabelAfter:          schema.LabelUntriaged,
	}, {
		ExpectationRecordID: newRecordID,
		GroupingID:          triangleGroupingID,
		Digest:              d(dks.DigestB02Pos),
		LabelBefore:         schema.LabelPositive,
		LabelAfter:          schema.LabelNegative,
	}, {
		ExpectationRecordID: newRecordID,
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestC03Unt),
		LabelBefore:         schema.LabelUntriaged,
		LabelAfter:          schema.LabelPositive,
	}}, newDeltas)

	exps := sqltest.GetAllRows(ctx, t, db, "Expectations", &schema.ExpectationRow{})
	assert.Contains(t, exps, schema.ExpectationRow{
		GroupingID:          triangleGroupingID,
		Digest:              d(dks.DigestB01Pos),
		Label:               schema.LabelUntriaged,
		ExpectationRecordID: &newRecordID,
	})
	assert.Contains(t, exps, schema.ExpectationRow{
		GroupingID:          triangleGroupingID,
		Digest:              d(dks.DigestB02Pos),
		Label:               schema.LabelNegative,
		ExpectationRecordID: &newRecordID,
	})
	assert.Contains(t, exps, schema.ExpectationRow{
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestC03Unt),
		Label:               schema.LabelPositive,
		ExpectationRecordID: &newRecordID,
	})
	assert.Contains(t, exps, schema.ExpectationRow{
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestBlank),
		Label:               schema.LabelNegative, // unchanged
		ExpectationRecordID: &existingRecordID,    // unchanged
	})
}

func TestTriage2_BulkTriage_OnCL_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	const user = "single_triage@example.com"
	fakeNow := time.Date(2021, time.July, 4, 4, 4, 4, 0, time.UTC)
	expectedBranch := "gerrit_CL_fix_ios"
	// This is the ID associated with triaging DigestC01Pos as positive on the primary branch.
	existingID, err := uuid.Parse("94a63df2-33d3-97ad-f4d7-341f76ff8cb6")
	require.NoError(t, err)

	_, circleGroupingID := sql.SerializeMap(paramtools.Params{
		types.CorpusField:     dks.RoundCorpus,
		types.PrimaryKeyField: dks.CircleTest,
	})

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
	}

	tr := frontend.TriageRequest{
		TestDigestStatus: map[types.TestName]map[types.Digest]expectations.Label{
			dks.CircleTest: {
				dks.DigestC06Pos_CL: expectations.Negative,
				dks.DigestC01Pos:    expectations.Negative,
			},
		},
		CodeReviewSystem: dks.GerritCRS,
		ChangelistID:     dks.ChangelistIDThatAttemptsToFixIOS,
	}
	ctx = context.WithValue(ctx, now.ContextKey, fakeNow)
	require.NoError(t, wh.triage2(ctx, user, tr))

	latestRecord := sqltest.GetAllRows(ctx, t, db, "ExpectationRecords", &schema.ExpectationRecordRow{}).([]schema.ExpectationRecordRow)[0]
	newRecordID := latestRecord.ExpectationRecordID // randomly generated
	assert.Equal(t, schema.ExpectationRecordRow{
		ExpectationRecordID: newRecordID,
		BranchName:          &expectedBranch,
		UserName:            user,
		TriageTime:          fakeNow,
		NumChanges:          2,
	}, latestRecord)

	whereClause := `WHERE expectation_record_id = '` + newRecordID.String() + `'`
	newDeltas := sqltest.GetAllRows(ctx, t, db, "ExpectationDeltas", &schema.ExpectationDeltaRow{}, whereClause)
	assert.ElementsMatch(t, []schema.ExpectationDeltaRow{{
		ExpectationRecordID: newRecordID,
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestC06Pos_CL),
		LabelBefore:         schema.LabelUntriaged, // This state is pulled from the primary branch
		LabelAfter:          schema.LabelNegative,
	}, {
		ExpectationRecordID: newRecordID,
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestC01Pos),
		LabelBefore:         schema.LabelPositive,
		LabelAfter:          schema.LabelNegative,
	}}, newDeltas)

	clExps := sqltest.GetAllRows(ctx, t, db, "SecondaryBranchExpectations", &schema.SecondaryBranchExpectationRow{})
	assert.Contains(t, clExps, schema.SecondaryBranchExpectationRow{
		BranchName:          expectedBranch,
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestC06Pos_CL),
		Label:               schema.LabelNegative,
		ExpectationRecordID: newRecordID,
	})
	assert.Contains(t, clExps, schema.SecondaryBranchExpectationRow{
		BranchName:          expectedBranch,
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestC01Pos),
		Label:               schema.LabelNegative,
		ExpectationRecordID: newRecordID,
	})

	// Primary branch expectations stay the same
	exps := sqltest.GetAllRows(ctx, t, db, "Expectations", &schema.ExpectationRow{})
	assert.Contains(t, exps, schema.ExpectationRow{
		GroupingID:          circleGroupingID,
		Digest:              d(dks.DigestC01Pos),
		Label:               schema.LabelPositive,
		ExpectationRecordID: &existingID,
	})
}

func TestLatestPositiveDigest2_TracesExist_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	// Turn a JSON string into a tiling.TraceIDV2 by hashing and hex encoding it.
	tID := func(j string) tiling.TraceIDV2 {
		h := md5.Sum([]byte(j))
		return tiling.TraceIDV2(hex.EncodeToString(h[:]))
	}

	windows10dot2RGBSquare := tID(`{"color mode":"RGB","device":"QuadroP400","name":"square","os":"Windows10.2","source_type":"corners"}`)
	ipadGreyTriangle := tID(`{"color mode":"GREY","device":"iPad6,3","name":"triangle","os":"iOS","source_type":"corners"}`)
	iphoneRGBCircle := tID(`{"color mode":"RGB","device":"iPhone12,1","name":"circle","os":"iOS","source_type":"round"}`)
	windows10dot3RGBCircle := tID(`{"color mode":"RGB","device":"QuadroP400","name":"circle","os":"Windows10.3","source_type":"round"}`)

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	test := func(name string, traceID tiling.TraceIDV2, expectedDigest types.Digest) {
		t.Run(name, func(t *testing.T) {
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, requestURL, nil)
			r = mux.SetURLVars(r, map[string]string{"traceID": string(traceID)})

			wh.LatestPositiveDigestHandler2(w, r)
			expectedJSONResponse := `{"digest":"` + string(expectedDigest) + `"}`
			assertJSONResponseWas(t, http.StatusOK, expectedJSONResponse, w)
		})

	}

	test("positive at head", ipadGreyTriangle, dks.DigestB02Pos)
	test("positive then empty", windows10dot2RGBSquare, dks.DigestA01Pos)
	test("positive then negative", iphoneRGBCircle, dks.DigestC01Pos)

	// This trace exists, but has nothing positively triaged. So we return an empty digest.
	test("no positive digests", windows10dot3RGBCircle, "")
}

func TestLatestPositiveDigest2_InvalidTraceFormat_ReturnsError(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{"traceID": "this is formatted incorrectly"})

	wh.LatestPositiveDigestHandler2(w, r)
	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestLatestPositiveDigest2_TraceDoesNotExist_ReturnsEmptyDigest(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, requestURL, nil)
	r = mux.SetURLVars(r, map[string]string{"traceID": "1234567890abcdef1234567890abcdef"})

	wh.LatestPositiveDigestHandler2(w, r)
	expectedJSONResponse := `{"digest":""}`
	assertJSONResponseWas(t, http.StatusOK, expectedJSONResponse, w)
}

func TestGetChangelistsHandler2_AllChangelists_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))
	waitForSystemTime()

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{{
				ID:          dks.GerritCRS,
				URLTemplate: "example.com/%s/gerrit",
			}, {
				ID:          dks.GerritInternalCRS,
				URLTemplate: "example.com/%s/gerrit-internal",
			}},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/changelists?size=50", nil)
	wh.ChangelistsHandler2(w, r)
	const expectedResponse = `{"changelists":[{"system":"gerrit-internal","id":"CL_new_tests","owner":"userTwo@example.com","status":"open","subject":"Increase test coverage","updated":"2020-12-12T09:20:33Z","url":"example.com/CL_new_tests/gerrit-internal"},` +
		`{"system":"gerrit","id":"CL_fix_ios","owner":"userOne@example.com","status":"open","subject":"Fix iOS","updated":"2020-12-10T04:05:06Z","url":"example.com/CL_fix_ios/gerrit"},` +
		`{"system":"gerrit","id":"CLisabandoned","owner":"userOne@example.com","status":"abandoned","subject":"was abandoned","updated":"2020-06-06T06:06:00Z","url":"example.com/CLisabandoned/gerrit"},` +
		`{"system":"gerrit","id":"CLhaslanded","owner":"userTwo@example.com","status":"landed","subject":"was landed","updated":"2020-05-05T05:05:00Z","url":"example.com/CLhaslanded/gerrit"}],"offset":0,"size":50,"total":2147483647}`
	assertJSONResponseWas(t, http.StatusOK, expectedResponse, w)
}

func TestGetChangelistsHandler2_RespectsPagination_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))
	waitForSystemTime()

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{{
				ID:          dks.GerritCRS,
				URLTemplate: "example.com/%s/gerrit",
			}, {
				ID:          dks.GerritInternalCRS,
				URLTemplate: "example.com/%s/gerrit-internal",
			}},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/changelists?size=2&offset=1", nil)
	wh.ChangelistsHandler2(w, r)
	const expectedResponse = `{"changelists":[{"system":"gerrit","id":"CL_fix_ios","owner":"userOne@example.com","status":"open","subject":"Fix iOS","updated":"2020-12-10T04:05:06Z","url":"example.com/CL_fix_ios/gerrit"},` +
		`{"system":"gerrit","id":"CLisabandoned","owner":"userOne@example.com","status":"abandoned","subject":"was abandoned","updated":"2020-06-06T06:06:00Z","url":"example.com/CLisabandoned/gerrit"}],"offset":1,"size":2,"total":2147483647}`
	assertJSONResponseWas(t, http.StatusOK, expectedResponse, w)
}

func TestGetChangelistsHandler2_ActiveChangelists_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))
	waitForSystemTime()

	wh := Handlers{
		HandlersConfig: HandlersConfig{
			DB: db,
			ReviewSystems: []clstore.ReviewSystem{{
				ID:          dks.GerritCRS,
				URLTemplate: "example.com/%s/gerrit",
			}, {
				ID:          dks.GerritInternalCRS,
				URLTemplate: "example.com/%s/gerrit-internal",
			}},
		},
		anonymousCheapQuota: rate.NewLimiter(rate.Inf, 1),
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/changelists?active=true", nil)
	wh.ChangelistsHandler2(w, r)
	const expectedResponse = `{"changelists":[{"system":"gerrit-internal","id":"CL_new_tests","owner":"userTwo@example.com","status":"open","subject":"Increase test coverage","updated":"2020-12-12T09:20:33Z","url":"example.com/CL_new_tests/gerrit-internal"},` +
		`{"system":"gerrit","id":"CL_fix_ios","owner":"userOne@example.com","status":"open","subject":"Fix iOS","updated":"2020-12-10T04:05:06Z","url":"example.com/CL_fix_ios/gerrit"}],"offset":0,"size":20,"total":2147483647}`
	assertJSONResponseWas(t, http.StatusOK, expectedResponse, w)
}

func TestListIgnoreRules2_WithCounts_Success(t *testing.T) {
	unittest.LargeTest(t)

	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := Handlers{
		anonymousExpensiveQuota: rate.NewLimiter(rate.Inf, 1),
		HandlersConfig: HandlersConfig{
			DB:          db,
			IgnoreStore: sqlignorestore.New(db),
			WindowSize:  100,
		},
	}
	require.NoError(t, wh.updateIgnoredTracesCache(ctx))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/json/v2/ignores?counts=true", nil)
	wh.ListIgnoreRules2(w, r)
	const expectedResponse = `{"rules":[{"id":"b75cc985-efbd-9973-fa1a-05787f04f237","name":"userTwo@example.com","updatedBy":"userOne@example.com","expires":"2020-02-14T13:12:11Z","query":"device=Nokia4\u0026source_type=corners","note":"This rule has expired (and does not apply to anything)","countAll":0,"exclusiveCountAll":0,"count":0,"exclusiveCount":0},` +
		`{"id":"a210f5da-a114-0799-e102-870edaf5570e","name":"userTwo@example.com","updatedBy":"userOne@example.com","expires":"2030-12-30T15:16:17Z","query":"device=taimen\u0026name=square\u0026name=circle","note":"Taimen isn't drawing correctly enough yet","countAll":2,"exclusiveCountAll":2,"count":1,"exclusiveCount":1}]}`
	assertJSONResponseWas(t, http.StatusOK, expectedResponse, w)
}

func TestStartIgnoredTraceCacheProcess(t *testing.T) {
	unittest.LargeTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, dks.Build()))

	wh := &Handlers{
		HandlersConfig: HandlersConfig{
			DB:         db,
			WindowSize: 100,
		},
	}

	wh.startIgnoredTraceCacheProcess(ctx)
	require.Eventually(t, func() bool {
		wh.ignoredTracesCacheMutex.RLock()
		defer wh.ignoredTracesCacheMutex.RUnlock()
		return len(wh.ignoredTracesCache) == 2
	}, 5*time.Second, 100*time.Millisecond)
	wh.ignoredTracesCacheMutex.RLock()
	defer wh.ignoredTracesCacheMutex.RUnlock()
	// These match one of the two ignore rules in the sample data. The other ignore rule matches
	// nothing.
	assert.ElementsMatch(t, []ignoredTrace{{
		Keys: paramtools.Params{
			dks.ColorModeKey:      dks.RGBColorMode,
			dks.DeviceKey:         dks.TaimenDevice,
			types.PrimaryKeyField: dks.SquareTest,
			dks.OSKey:             dks.AndroidOS,
			types.CorpusField:     dks.CornersCorpus,
		},
		Label: expectations.Negative,
	}, {
		Keys: paramtools.Params{
			dks.ColorModeKey:      dks.RGBColorMode,
			dks.DeviceKey:         dks.TaimenDevice,
			types.PrimaryKeyField: dks.CircleTest,
			dks.OSKey:             dks.AndroidOS,
			types.CorpusField:     dks.RoundCorpus,
		},
		Label: expectations.Untriaged,
	}}, wh.ignoredTracesCache)
}

// Because we are calling our handlers directly, the target URL doesn't matter. The target URL
// would only matter if we were calling into the router, so it knew which handler to call.
const requestURL = "/does/not/matter"

var (
	// These dates are arbitrary and don't matter. The logic for determining if an alert has
	// "expired" is handled on the frontend.
	firstRuleExpire  = time.Date(2019, time.November, 30, 3, 4, 5, 0, time.UTC)
	secondRuleExpire = time.Date(2020, time.November, 30, 3, 4, 5, 0, time.UTC)
	thirdRuleExpire  = time.Date(2020, time.November, 27, 3, 4, 5, 0, time.UTC)
)

// d converts the given digest to its corresponding DigestBytes types. It panics on a failure.
func d(d types.Digest) schema.DigestBytes {
	b, err := sql.DigestToBytes(d)
	if err != nil {
		panic(err)
	}
	return b
}

func makeIgnoreRules() []ignore.Rule {
	return []ignore.Rule{
		{
			ID:        "1234",
			CreatedBy: "user@example.com",
			UpdatedBy: "user2@example.com",
			Expires:   firstRuleExpire,
			Query:     "device=delta",
			Note:      "Flaky driver",
		},
		{
			ID:        "5678",
			CreatedBy: "user2@example.com",
			UpdatedBy: "user@example.com",
			Expires:   secondRuleExpire,
			Query:     "name=test_two&source_type=gm",
			Note:      "Not ready yet",
		},
		{
			ID:        "-1",
			CreatedBy: "user3@example.com",
			UpdatedBy: "user3@example.com",
			Expires:   thirdRuleExpire,
			Query:     "matches=nothing",
			Note:      "Oops, this matches nothing",
		},
	}
}

// assertJSONResponseAndReturnBody asserts that the given ResponseRecorder was given the
// appropriate JSON and the expected status code, and returns the response body.
func assertJSONResponseAndReturnBody(t *testing.T, expectedStatusCode int, w *httptest.ResponseRecorder) []byte {
	resp := w.Result()
	assert.Equal(t, expectedStatusCode, resp.StatusCode)
	assert.Equal(t, jsonContentType, resp.Header.Get(contentTypeHeader))
	assert.Equal(t, allowAllOrigins, resp.Header.Get(accessControlHeader))
	assert.Equal(t, noSniffContent, resp.Header.Get(contentTypeOptionsHeader))
	respBody, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)
	return respBody
}

// assertJSONResponseWas asserts that the given ResponseRecorder was given the appropriate JSON
// headers and the expected status code and response body.
func assertJSONResponseWas(t *testing.T, expectedStatusCode int, expectedBody string, w *httptest.ResponseRecorder) {
	actualBody := assertJSONResponseAndReturnBody(t, expectedStatusCode, w)
	// The JSON encoder includes a newline "\n" at the end of the body, which is awkward to include
	// in the literals passed in above, so we add that here
	assert.Equal(t, expectedBody+"\n", string(actualBody))
}

func assertImageResponseWas(t *testing.T, expected []byte, w *httptest.ResponseRecorder) {
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	respBody, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, expected, respBody)
}

func assertDiffImageWas(t *testing.T, w *httptest.ResponseRecorder, expectedTextImage string) {
	resp := w.Result()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	respImg, err := decodeImg(resp.Body)
	require.NoError(t, err)
	var buf bytes.Buffer
	require.NoError(t, text.Encode(&buf, respImg))
	assert.Equal(t, expectedTextImage, buf.String())
}

// setID applies the ID mux.Var to a copy of the given request. In a normal server setting, mux will
// parse the given url with a string that indicates how to extract variables (e.g.
// '/json/ignores/save/{id}' and store those to the request's context. However, since we just call
// the handler directly, we need to set those variables ourselves.
func setID(r *http.Request, id string) *http.Request {
	return mux.SetURLVars(r, map[string]string{"id": id})
}

// waitForSystemTime waits for a time greater than the duration mentioned in "AS OF SYSTEM TIME"
// clauses in queries. This way, the queries will be accurate.
func waitForSystemTime() {
	time.Sleep(150 * time.Millisecond)
}

func initCaches(handlers *Handlers) *Handlers {
	clcache, err := lru.New(changelistSummaryCacheSize)
	if err != nil {
		panic(err)
	}
	handlers.clSummaryCache = clcache
	return handlers
}

// overwriteNow adds the provided time to the request's context (which is returned as a shallow
// copy of the original request).
func overwriteNow(r *http.Request, fakeNow time.Time) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), now.ContextKey, fakeNow))
}

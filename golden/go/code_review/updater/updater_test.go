package updater

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/testutils/unittest"
	"go.skia.org/infra/go/vcsinfo"
	"go.skia.org/infra/golden/go/clstore"
	mock_clstore "go.skia.org/infra/golden/go/clstore/mocks"
	"go.skia.org/infra/golden/go/code_review"
	mock_codereview "go.skia.org/infra/golden/go/code_review/mocks"
	"go.skia.org/infra/golden/go/expectations"
	mock_expectations "go.skia.org/infra/golden/go/expectations/mocks"
	"go.skia.org/infra/golden/go/types"
)

// TestUpdateSunnyDay checks a case in which three commits are seen, one of which we already know
// is landed and two more that are open with some CLExpectations.
func TestUpdateSunnyDay(t *testing.T) {
	unittest.SmallTest(t)

	mc := &mock_codereview.Client{}
	mes := &mock_expectations.Store{}
	mcs := &mock_clstore.Store{}
	alphaExp := &mock_expectations.Store{}
	betaExp := &mock_expectations.Store{}
	defer mes.AssertExpectations(t)
	defer mcs.AssertExpectations(t)

	commits := makeCommits()

	var alphaChanges expectations.Expectations
	alphaChanges.Set(someTest, digestOne, expectations.Negative)
	alphaDelta := expectations.AsDelta(&alphaChanges)

	var betaChanges expectations.Expectations
	betaChanges.Set(someTest, digestTwo, expectations.Positive)
	betaDelta := expectations.AsDelta(&betaChanges)

	// This data is all arbitrary.
	mc.On("GetChangelistIDForCommit", testutils.AnyContext, commits[0]).Return(landedCL, nil)
	mc.On("GetChangelistIDForCommit", testutils.AnyContext, commits[1]).Return(openCLAlpha, nil)
	mc.On("GetChangelist", testutils.AnyContext, openCLAlpha).Return(code_review.Changelist{
		SystemID: openCLAlpha,
		Status:   code_review.Landed, // the CRS says they are landed, but the store thinks not.
		Owner:    alphaAuthor,
		Updated:  time.Date(2019, time.May, 15, 14, 14, 12, 0, time.UTC),
	}, nil)
	mc.On("GetChangelistIDForCommit", testutils.AnyContext, commits[2]).Return(openCLBeta, nil)
	mc.On("GetChangelist", testutils.AnyContext, openCLBeta).Return(code_review.Changelist{
		SystemID: openCLBeta,
		Status:   code_review.Landed, // the CRS says they are landed, but the store thinks not.
		Owner:    betaAuthor,
		Updated:  time.Date(2019, time.May, 15, 14, 18, 12, 0, time.UTC),
	}, nil)

	mes.On("ForChangelist", openCLAlpha, githubCRS).Return(alphaExp)
	mes.On("ForChangelist", openCLBeta, githubCRS).Return(betaExp)
	mes.On("AddChange", testutils.AnyContext, alphaDelta, alphaAuthor).Return(nil)
	mes.On("AddChange", testutils.AnyContext, betaDelta, betaAuthor).Return(nil)

	alphaExp.On("Get", testutils.AnyContext).Return(&alphaChanges, nil)
	betaExp.On("Get", testutils.AnyContext).Return(&betaChanges, nil)

	mcs.On("GetChangelist", testutils.AnyContext, landedCL).Return(code_review.Changelist{
		SystemID: landedCL,
		Status:   code_review.Landed, // Already in the store as landed - should be skipped.
		Owner:    alphaAuthor,
	}, nil)
	mcs.On("GetChangelist", testutils.AnyContext, openCLAlpha).Return(code_review.Changelist{
		SystemID: openCLAlpha,
		Status:   code_review.Open, // the CRS says they are landed, but the store thinks not.
		Owner:    alphaAuthor,
	}, nil)
	mcs.On("GetChangelist", testutils.AnyContext, openCLBeta).Return(code_review.Changelist{
		SystemID: openCLBeta,
		Status:   code_review.Open, // the CRS says they are landed, but the store thinks not.
		Owner:    betaAuthor,
	}, nil)
	clChecker := func(cl code_review.Changelist) bool {
		if cl.SystemID == openCLAlpha || cl.SystemID == openCLBeta {
			require.Equal(t, code_review.Landed, cl.Status)
			require.NotZero(t, cl.Updated)
			require.Equal(t, time.May, cl.Updated.Month())
			return true
		}
		return false
	}
	mcs.On("PutChangelist", testutils.AnyContext, mock.MatchedBy(clChecker)).Return(nil).Twice()

	// Pretend we are configured for Gerrit and GitHub, and GitHub doesn't recognize any of these
	// CLs
	gerritClient := &mock_codereview.Client{}
	gerritClient.On("GetChangelistIDForCommit", testutils.AnyContext, mock.Anything).Return("", code_review.ErrNotFound)

	u := New(mes, []clstore.ReviewSystem{
		{
			ID:     gerritCRS,
			Client: gerritClient,
			// URLTemplate and Store not used here
		},
		{
			ID:     githubCRS,
			Client: mc,
			Store:  mcs,
			// URLTemplate not used here
		},
	})
	err := u.UpdateChangelistsAsLanded(context.Background(), commits)
	require.NoError(t, err)
}

// TestUpdateEmpty checks the common case of there being no CLExpectations
func TestUpdateEmpty(t *testing.T) {
	unittest.SmallTest(t)

	mc := &mock_codereview.Client{}
	mes := &mock_expectations.Store{}
	mcs := &mock_clstore.Store{}
	betaExp := &mock_expectations.Store{}
	defer mcs.AssertExpectations(t)

	commits := makeCommits()[2:]

	betaChanges := expectations.Expectations{}

	mc.On("GetChangelistIDForCommit", testutils.AnyContext, commits[0]).Return(openCLBeta, nil)
	mc.On("GetChangelist", testutils.AnyContext, openCLBeta).Return(code_review.Changelist{
		SystemID: openCLBeta,
		Status:   code_review.Landed,
		Owner:    betaAuthor,
		Updated:  time.Date(2019, time.May, 15, 14, 18, 12, 0, time.UTC),
	}, nil)

	mes.On("ForChangelist", openCLBeta, githubCRS).Return(betaExp)

	betaExp.On("Get", testutils.AnyContext).Return(&betaChanges, nil)

	mcs.On("GetChangelist", testutils.AnyContext, openCLBeta).Return(code_review.Changelist{
		SystemID: openCLBeta,
		Status:   code_review.Open, // the CRS says they are landed, but the store thinks not.
		Owner:    betaAuthor,
	}, nil)
	clChecker := func(cl code_review.Changelist) bool {
		if cl.SystemID == openCLBeta {
			require.Equal(t, code_review.Landed, cl.Status)
			require.NotZero(t, cl.Updated)
			require.Equal(t, time.May, cl.Updated.Month())
			return true
		}
		return false
	}
	mcs.On("PutChangelist", testutils.AnyContext, mock.MatchedBy(clChecker)).Return(nil).Once()

	u := New(mes, []clstore.ReviewSystem{
		{
			ID:     githubCRS,
			Client: mc,
			Store:  mcs,
			// URLTemplate not used here
		},
	})
	err := u.UpdateChangelistsAsLanded(context.Background(), commits)
	require.NoError(t, err)
}

// TestUpdateNoTryJobsSeen checks the common case of there being no TryJobs that uploaded data
// associated with this CL (thus, it won't be in clstore)
func TestUpdateNoTryJobsSeen(t *testing.T) {
	unittest.SmallTest(t)

	mc := &mock_codereview.Client{}
	mcs := &mock_clstore.Store{}

	commits := makeCommits()[2:]

	mc.On("GetChangelistIDForCommit", testutils.AnyContext, commits[0]).Return(openCLBeta, nil)

	mcs.On("GetChangelist", testutils.AnyContext, openCLBeta).Return(code_review.Changelist{}, clstore.ErrNotFound)

	u := New(nil, []clstore.ReviewSystem{
		{
			ID:     githubCRS,
			Client: mc,
			Store:  mcs,
			// URLTemplate not used here
		},
	})
	err := u.UpdateChangelistsAsLanded(context.Background(), commits)
	require.NoError(t, err)
}

// TestUpdateNoChangelist checks the exceptional case where a commit lands without being tied to
// a Changelist in any CRS (we should skip it and not crash).
func TestUpdateNoChangelist(t *testing.T) {
	unittest.SmallTest(t)

	mc := &mock_codereview.Client{}

	commits := makeCommits()[2:]
	mc.On("GetChangelistIDForCommit", testutils.AnyContext, commits[0]).Return("", code_review.ErrNotFound)

	u := New(nil, []clstore.ReviewSystem{
		{
			ID:     githubCRS,
			Client: mc,
			// Store and URLTemplate not used here
		},
		{
			ID:     gerritCRS,
			Client: mc,
			// Store and URLTemplate not used here
		},
	})
	err := u.UpdateChangelistsAsLanded(context.Background(), commits)
	require.NoError(t, err)
}

const (
	gerritCRS = "gerrit"
	githubCRS = "github"

	landedCL    = "11196d8aff4cd689c2e49336d12928a8bd23cdec"
	openCLAlpha = "aaa5f37f5bd91f1a7b3f080bf038af8e8fa4cab2"
	openCLBeta  = "bbb734d4127ab3fa7f8d08eec985e2336d5472a7"

	alphaAuthor = "user2@example.com"
	betaAuthor  = "user3@example.com"

	someTest  = types.TestName("some_test")
	digestOne = types.Digest("abc94d08ed22d21bc50cbe02da366b16")
	digestTwo = types.Digest("1232cfd382db585f297e31dbe9a0151f")
)

func makeCommits() []*vcsinfo.LongCommit {
	return []*vcsinfo.LongCommit{
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash: landedCL,
			},
			// All other fields are ignored
			Body: "Reviewed-on: https://skia-review.googlesource.com/c/skia/+/1landed",
		},
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash: openCLAlpha,
			},
			// All other fields are ignored
			Body: "Reviewed-on: https://skia-review.googlesource.com/c/skia/+/2open",
		},
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash: openCLBeta,
			},
			// All other fields are ignored
			Body: "Reviewed-on: https://skia-review.googlesource.com/c/skia/+/3open",
		},
	}
}

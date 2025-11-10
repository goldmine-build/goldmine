package main

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"testing"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/mohae/deepcopy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"go.goldmine.build/go/git/provider"
	provmocks "go.goldmine.build/go/git/provider/mocks"
	"go.goldmine.build/go/testutils"
	"go.goldmine.build/go/vcsinfo"
	"go.goldmine.build/golden/cmd/gitilesfollower/mocks"
	"go.goldmine.build/golden/go/config"
	dks "go.goldmine.build/golden/go/sql/datakitchensink"
	"go.goldmine.build/golden/go/sql/schema"
	"go.goldmine.build/golden/go/sql/sqltest"
	"go.goldmine.build/golden/go/types"
)

func setupForTest(t *testing.T) (context.Context, *pgxpool.Pool) {
	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	return ctx, db
}

// createGitProviderMock creates a mock GitProvider that returns the given commits
// when CommitsFromMostRecentGitHashToHead is called with the given startHash.
func createGitProviderMock(t *testing.T, startHash string, commits []provider.Commit) *provmocks.Provider {
	gitp := provmocks.NewProvider(t)
	gitp.On("CommitsFromMostRecentGitHashToHead", testutils.AnyContext, startHash, mock.Anything).Run(func(args mock.Arguments) {
		cb := args.Get(2).(provider.CommitProcessor)
		for _, c := range commits {
			cb(c)
		}
	}).Return(nil)
	return gitp
}

// ******************************************************
// Utilities that deduplicate code in the tests below.
// ******************************************************

// Three commits that are returned from the git provider mock.
var firstThreeCommitsForGitProviderMock = []provider.Commit{
	{
		GitHash:   "2222222222222222222222222222222222222222",
		Author:    "author 2",
		Subject:   "subject 2",
		Timestamp: time.Date(2021, time.February, 25, 10, 2, 0, 0, time.UTC).Unix(),
		Body:      "Reviewed-on: https://example.com/c/my-repo/+/000004",
	},
	{

		GitHash:   "3333333333333333333333333333333333333333",
		Author:    "author 3",
		Subject:   "subject 3",
		Timestamp: time.Date(2021, time.February, 25, 10, 3, 0, 0, time.UTC).Unix(),
		Body:      "Reviewed-on: https://example.com/c/my-repo/+/000003",
	},
	{
		GitHash:   "4444444444444444444444444444444444444444",
		Author:    "author 4",
		Subject:   "subject 4",
		Timestamp: time.Date(2021, time.February, 25, 10, 4, 0, 0, time.UTC).Unix(),
		Body:      "Reviewed-on: https://example.com/c/my-repo/+/000002",
	},
}

// How the existing data in the DB looks like for the three commits above.
var firstThreeCommitsAsSchemaRows = []schema.GitCommitRow{{
	GitHash:     "4444444444444444444444444444444444444444",
	CommitID:    "001000000003",
	CommitTime:  time.Date(2021, time.February, 25, 10, 4, 0, 0, time.UTC),
	AuthorEmail: "author 4",
	Subject:     "subject 4",
}, {
	GitHash:     "3333333333333333333333333333333333333333",
	CommitID:    "001000000002",
	CommitTime:  time.Date(2021, time.February, 25, 10, 3, 0, 0, time.UTC),
	AuthorEmail: "author 3",
	Subject:     "subject 3",
}, {
	GitHash:     "2222222222222222222222222222222222222222",
	CommitID:    "001000000001",
	CommitTime:  time.Date(2021, time.February, 25, 10, 2, 0, 0, time.UTC),
	AuthorEmail: "author 2",
	Subject:     "subject 2",
}}

// Asserts that the GitCommits table in the given db contains exactly the first
// three commits as defined in firstThreeCommitsForGitProviderMock.
func assertDBContainsFirstThreeCommits(t *testing.T, ctx context.Context, db *pgxpool.Pool) {
	actualRows := sqltest.GetAllRows(ctx, t, db, "GitCommits", &schema.GitCommitRow{}).([]schema.GitCommitRow)
	assert.Equal(t, firstThreeCommitsAsSchemaRows, actualRows)
}

// The existing data in the DB for the three commits above as a schema.Tables struct.
var firstThreeCommitsAsSchema = schema.Tables{GitCommits: firstThreeCommitsAsSchemaRows}

// A repoFollowerConfig that can be used in tests.
var rfc = repoFollowerConfig{
	Common: config.Common{
		GitRepoURL:    "https://example.com/my-repo.git",
		GitRepoBranch: "main",
	},
	SystemName:          "gerrit",
	ExtractionTechnique: ReviewedLine,
	InitialCommit:       "1111111111111111111111111111111111111111", // we expect this to not be used
}

// ******************************************************
// End Utilities
// ******************************************************

func TestUpdateCycle_EmptyDB_UsesInitialCommit(t *testing.T) {
	ctx, db := setupForTest(t)
	gitp := createGitProviderMock(t, "1111111111111111111111111111111111111111", firstThreeCommitsForGitProviderMock)
	require.NoError(t, updateCycle(ctx, db, gitp, rfc))

	assertDBContainsFirstThreeCommits(t, ctx, db)
	// The initial commit is not stored in the DB nor queried, but is implicitly has id
	// equal to initialID.

	// This cycle shouldn't touch the Changelists tables
	cls := sqltest.GetAllRows(ctx, t, db, "Changelists", &schema.ChangelistRow{}).([]schema.ChangelistRow)
	assert.Empty(t, cls)
}

func TestUpdateCycle_CommitsInDB_IncrementalUpdate(t *testing.T) {
	ctx, db := setupForTest(t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, firstThreeCommitsAsSchema))

	cbValues := []provider.Commit{
		{
			GitHash:   "5555555555555555555555555555555555555555",
			Author:    "author 5",
			Subject:   "subject 5",
			Timestamp: time.Date(2021, time.February, 25, 10, 5, 0, 0, time.UTC).Unix(),
		},
		{ // These are returned with the most recent commits first
			GitHash:   "6666666666666666666666666666666666666666",
			Author:    "author 6",
			Subject:   "subject 6",
			Timestamp: time.Date(2021, time.February, 25, 10, 6, 0, 0, time.UTC).Unix(),
		},
	}
	gitp := createGitProviderMock(t, "4444444444444444444444444444444444444444", cbValues)
	require.NoError(t, updateCycle(ctx, db, gitp, rfc))

	actualRows := sqltest.GetAllRows(ctx, t, db, "GitCommits", &schema.GitCommitRow{}).([]schema.GitCommitRow)
	assert.Equal(t, []schema.GitCommitRow{{
		GitHash:     "6666666666666666666666666666666666666666",
		CommitID:    "001000000005",
		CommitTime:  time.Date(2021, time.February, 25, 10, 6, 0, 0, time.UTC),
		AuthorEmail: "author 6",
		Subject:     "subject 6",
	}, {
		GitHash:     "5555555555555555555555555555555555555555",
		CommitID:    "001000000004",
		CommitTime:  time.Date(2021, time.February, 25, 10, 5, 0, 0, time.UTC),
		AuthorEmail: "author 5",
		Subject:     "subject 5",
	}, {
		GitHash:     "4444444444444444444444444444444444444444",
		CommitID:    "001000000003",
		CommitTime:  time.Date(2021, time.February, 25, 10, 4, 0, 0, time.UTC),
		AuthorEmail: "author 4",
		Subject:     "subject 4",
	}, {
		GitHash:     "3333333333333333333333333333333333333333",
		CommitID:    "001000000002",
		CommitTime:  time.Date(2021, time.February, 25, 10, 3, 0, 0, time.UTC),
		AuthorEmail: "author 3",
		Subject:     "subject 3",
	}, {
		GitHash:     "2222222222222222222222222222222222222222",
		CommitID:    "001000000001",
		CommitTime:  time.Date(2021, time.February, 25, 10, 2, 0, 0, time.UTC),
		AuthorEmail: "author 2",
		Subject:     "subject 2",
	}}, actualRows)
}

func TestUpdateCycle_NoNewCommits_NothingChanges(t *testing.T) {
	ctx, db := setupForTest(t)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, firstThreeCommitsAsSchema))
	gitp := createGitProviderMock(t, "4444444444444444444444444444444444444444", nil)
	require.NoError(t, updateCycle(ctx, db, gitp, rfc))
	assertDBContainsFirstThreeCommits(t, ctx, db)
}

func TestUpdateCycle_UpToDate_Success(t *testing.T) {
	ctx, db := setupForTest(t)
	gitp := createGitProviderMock(t, "1111111111111111111111111111111111111111", nil)
	existingData := schema.Tables{
		TrackingCommits: []schema.TrackingCommitRow{{
			Repo:        "https://example.com/my-repo.git",
			LastGitHash: "4444444444444444444444444444444444444444",
		}},
	}
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))

	require.NoError(t, updateCycle(ctx, db, gitp, rfc))

	actualRows := sqltest.GetAllRows(ctx, t, db, "TrackingCommits", &schema.TrackingCommitRow{}).([]schema.TrackingCommitRow)
	assert.Equal(t, []schema.TrackingCommitRow{{
		Repo:        "https://example.com/my-repo.git",
		LastGitHash: "4444444444444444444444444444444444444444",
	}}, actualRows)
}

func TestUpdateCycle_UnparsableCL_Success(t *testing.T) {
	ctx, db := setupForTest(t)

	// Modify the second commit so that its body doesn't match the expected pattern.
	commits := deepcopy.Copy(firstThreeCommitsForGitProviderMock).([]provider.Commit)
	commits[1].Body = "This body doesn't match the pattern!"

	gitp := createGitProviderMock(t, "1111111111111111111111111111111111111111", commits)
	require.NoError(t, updateCycle(ctx, db, gitp, rfc))

	assertDBContainsFirstThreeCommits(t, ctx, db)

	// This cycle shouldn't touch the Changelists tables
	cls := sqltest.GetAllRows(ctx, t, db, "Changelists", &schema.ChangelistRow{}).([]schema.ChangelistRow)
	assert.Empty(t, cls)
}

func TestUpdateCycle_CLsWithNoExpectationsLand_MarkedAsLanded(t *testing.T) {
	ctx, db := setupForTest(t)

	existingData := schema.Tables{
		TrackingCommits: []schema.TrackingCommitRow{{
			Repo:        "https://example.com/my-repo.git",
			LastGitHash: "2222222222222222222222222222222222222222",
		}}, Changelists: []schema.ChangelistRow{{
			ChangelistID:     "gerrit_000004",
			System:           "gerrit",
			Status:           schema.StatusOpen,
			OwnerEmail:       "whomever@example.com",
			Subject:          "subject 4",
			LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
		}, {
			ChangelistID:     "gerrit_000003",
			System:           "gerrit",
			Status:           schema.StatusOpen,
			OwnerEmail:       "user1@example.com",
			Subject:          "Revert commit 2",
			LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
		}},
	}
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))

	mgl := mocks.GitilesLogger{}
	mgl.On("Log", testutils.AnyContext, "main", mock.Anything).Return([]*vcsinfo.LongCommit{
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash: "4444444444444444444444444444444444444444",
				// The rest is ignored from Log
			},
		},
	}, nil)

	mgl.On("LogFirstParent", testutils.AnyContext, "2222222222222222222222222222222222222222", "4444444444444444444444444444444444444444").Return([]*vcsinfo.LongCommit{
		{ // These are returned with the most recent commits first
			ShortCommit: &vcsinfo.ShortCommit{
				Hash:    "4444444444444444444444444444444444444444",
				Author:  "author 4",
				Subject: "subject 4",
			},
			Body:      "Reviewed-on: https://example.com/c/my-repo/+/000004",
			Timestamp: time.Date(2021, time.February, 25, 10, 4, 0, 0, time.UTC),
		},
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash:    "3333333333333333333333333333333333333333",
				Author:  "author 3",
				Subject: "Revert commit 2",
			},
			Body: `Revert commit 2

Original change's description:
> Do something risky
>
> Change-Id: I5901f005c2758a92692e5cd70ba46a2b5ad797fd
> Reviewed-on: https://example.com/c/my-repo/+/000002
> Commit-Queue: User One <user1@google.com>
> Reviewed-by: User Two <user2@google.com>

TBR=user1@example.com

Reviewed-on: https://example.com/c/my-repo/+/000003
Reviewed-by: User One <user1@google.com>
Commit-Queue: User One <user1@google.com>`,
			Timestamp: time.Date(2021, time.February, 25, 10, 3, 0, 0, time.UTC),
		},
		// LogFirstParent excludes the first one mentioned.
	}, nil)

	gitp := createGitProviderMock(t, "1111111111111111111111111111111111111111", firstThreeCommitsForGitProviderMock)
	require.NoError(t, updateCycle(ctx, db, gitp, rfc))

	assertDBContainsFirstThreeCommits(t, ctx, db)

	actualRows := sqltest.GetAllRows(ctx, t, db, "TrackingCommits", &schema.TrackingCommitRow{}).([]schema.TrackingCommitRow)
	assert.Equal(t, []schema.TrackingCommitRow{{
		Repo:        "https://example.com/my-repo.git",
		LastGitHash: "4444444444444444444444444444444444444444",
	}}, actualRows)

	cls := sqltest.GetAllRows(ctx, t, db, "Changelists", &schema.ChangelistRow{}).([]schema.ChangelistRow)
	assert.Equal(t, []schema.ChangelistRow{{
		ChangelistID:     "gerrit_000003",
		System:           "gerrit",
		Status:           schema.StatusLanded,
		OwnerEmail:       "user1@example.com",
		Subject:          "Revert commit 2",
		LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
	}, {
		ChangelistID:     "gerrit_000004",
		System:           "gerrit",
		Status:           schema.StatusLanded,
		OwnerEmail:       "whomever@example.com",
		Subject:          "subject 4",
		LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
	}}, cls)
}

func TestCheckForLandedCycle_CLExpectations_MergedIntoPrimaryBranch(t *testing.T) {
	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	existingData := dks.Build()
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))

	clLandedTime := time.Date(2021, time.April, 1, 1, 1, 1, 0, time.UTC)

	gitp := createGitProviderMock(t, "0111011101110111011101110111011101110111", []provider.Commit{
		{
			GitHash:   "2222222222222222222222222222222222222222",
			Author:    dks.UserTwo,
			Subject:   "Increase test coverage",
			Body:      "Reviewed-on: https://example.com/c/my-repo/+/CL_new_tests",
			Timestamp: clLandedTime.Unix(),
		},
	})

	rfc2 := deepcopy.Copy(rfc).(repoFollowerConfig)
	rfc2.SystemName = dks.GerritInternalCRS

	require.NoError(t, updateCycle(ctx, db, gitp, rfc2))

	actualRows := sqltest.GetAllRows(ctx, t, db, "TrackingCommits", &schema.TrackingCommitRow{}).([]schema.TrackingCommitRow)
	assert.Equal(t, []schema.TrackingCommitRow{{
		Repo:        "https://example.com/my-repo.git",
		LastGitHash: "2222222222222222222222222222222222222222",
	}}, actualRows)

	cls := sqltest.GetAllRows(ctx, t, db, "Changelists", &schema.ChangelistRow{}).([]schema.ChangelistRow)
	assert.Equal(t, []schema.ChangelistRow{{
		ChangelistID:     "gerrit-internal_CL_new_tests",
		System:           dks.GerritInternalCRS,
		Status:           schema.StatusLanded, // updated
		OwnerEmail:       dks.UserTwo,
		Subject:          "Increase test coverage",
		LastIngestedData: time.Date(2020, time.December, 12, 9, 20, 33, 0, time.UTC),
	}, {
		ChangelistID:     "gerrit_CL_fix_ios",
		System:           dks.GerritCRS,
		Status:           schema.StatusOpen, // not touched
		OwnerEmail:       dks.UserOne,
		Subject:          "Fix iOS",
		LastIngestedData: time.Date(2020, time.December, 10, 4, 5, 6, 0, time.UTC),
	}, {
		ChangelistID:     "gerrit_CLdisallowtriaging",
		System:           dks.GerritCRS,
		Status:           schema.StatusOpen, // not touched
		OwnerEmail:       dks.UserOne,
		Subject:          "add test with disallow triaging",
		LastIngestedData: time.Date(2020, time.December, 12, 16, 0, 0, 0, time.UTC),
	}, {
		ChangelistID:     "gerrit_CLhaslanded",
		System:           dks.GerritCRS,
		Status:           schema.StatusLanded,
		OwnerEmail:       dks.UserTwo,
		Subject:          "was landed",
		LastIngestedData: time.Date(2020, time.May, 5, 5, 5, 0, 0, time.UTC),
	}, {
		ChangelistID:     "gerrit_CLisabandoned",
		System:           dks.GerritCRS,
		Status:           schema.StatusAbandoned,
		OwnerEmail:       dks.UserOne,
		Subject:          "was abandoned",
		LastIngestedData: time.Date(2020, time.June, 6, 6, 6, 0, 0, time.UTC),
	}, {
		ChangelistID:     "gerrit_CLmultipledatapoints",
		System:           dks.GerritCRS,
		Status:           schema.StatusOpen,
		OwnerEmail:       dks.UserOne,
		Subject:          "multiple datapoints",
		LastIngestedData: time.Date(2020, time.December, 12, 14, 0, 0, 0, time.UTC),
	}}, cls)

	records := sqltest.GetAllRows(ctx, t, db, "ExpectationRecords", &schema.ExpectationRecordRow{}).([]schema.ExpectationRecordRow)
	require.Len(t, records, len(existingData.ExpectationRecords)+2) // 2 users triaged on this CL
	user2RecordID := records[0].ExpectationRecordID
	user4RecordID := records[1].ExpectationRecordID
	assert.Equal(t, []schema.ExpectationRecordRow{{
		ExpectationRecordID: user2RecordID,
		BranchName:          nil,
		UserName:            dks.UserTwo,
		TriageTime:          clLandedTime,
		NumChanges:          2, // 2 of the users triages undid each other
	}, {
		ExpectationRecordID: user4RecordID,
		BranchName:          nil,
		UserName:            dks.UserFour,
		TriageTime:          clLandedTime,
		NumChanges:          1,
	}}, records[:2])

	deltas := sqltest.GetAllRows(ctx, t, db, "ExpectationDeltas", &schema.ExpectationDeltaRow{}).([]schema.ExpectationDeltaRow)
	assert.Contains(t, deltas, schema.ExpectationDeltaRow{
		ExpectationRecordID: user2RecordID,
		GroupingID:          h(roundRectGrouping),
		Digest:              d(dks.DigestE01Pos_CL),
		LabelBefore:         schema.LabelUntriaged,
		LabelAfter:          schema.LabelPositive,
	})
	assert.Contains(t, deltas, schema.ExpectationDeltaRow{
		ExpectationRecordID: user2RecordID,
		GroupingID:          h(roundRectGrouping),
		Digest:              d(dks.DigestE02Pos_CL),
		LabelBefore:         schema.LabelUntriaged,
		LabelAfter:          schema.LabelPositive,
	})
	assert.Contains(t, deltas, schema.ExpectationDeltaRow{
		ExpectationRecordID: user4RecordID,
		GroupingID:          h(sevenGrouping),
		Digest:              d(dks.DigestD01Pos_CL),
		LabelBefore:         schema.LabelUntriaged,
		LabelAfter:          schema.LabelPositive,
	}, deltas)

	expectations := sqltest.GetAllRows(ctx, t, db, "Expectations", &schema.ExpectationRow{}).([]schema.ExpectationRow)
	assert.Contains(t, expectations, schema.ExpectationRow{
		GroupingID:          h(roundRectGrouping),
		Digest:              d(dks.DigestE01Pos_CL),
		Label:               schema.LabelPositive,
		ExpectationRecordID: &user2RecordID,
	})
	assert.Contains(t, expectations, schema.ExpectationRow{
		GroupingID:          h(roundRectGrouping),
		Digest:              d(dks.DigestE02Pos_CL),
		Label:               schema.LabelPositive,
		ExpectationRecordID: &user2RecordID,
	})
	assert.Contains(t, expectations, schema.ExpectationRow{
		GroupingID:          h(sevenGrouping),
		Digest:              d(dks.DigestD01Pos_CL),
		Label:               schema.LabelPositive,
		ExpectationRecordID: &user4RecordID,
	})
}

/*


func TestCheckForLandedCycle_ExtractsCLFromSubject_Success(t *testing.T) {
	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	existingData := schema.Tables{
		TrackingCommits: []schema.TrackingCommitRow{{
			Repo:        "https://example.com/my-repo.git",
			LastGitHash: "2222222222222222222222222222222222222222",
		}}, Changelists: []schema.ChangelistRow{{
			ChangelistID:     "github_000004",
			System:           "github",
			Status:           schema.StatusOpen,
			OwnerEmail:       "whomever@example.com",
			Subject:          "subject 4",
			LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
		}, {
			ChangelistID:     "github_000003",
			System:           "github",
			Status:           schema.StatusOpen,
			OwnerEmail:       "user1@example.com",
			Subject:          `Revert "risky change (#000002)"`,
			LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
		}},
	}
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))

	mgl := mocks.GitilesLogger{}
	mgl.On("Log", testutils.AnyContext, "main", mock.Anything).Return([]*vcsinfo.LongCommit{
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash: "4444444444444444444444444444444444444444",
				// The rest is ignored from Log
			},
		},
	}, nil)

	mgl.On("LogFirstParent", testutils.AnyContext, "2222222222222222222222222222222222222222", "4444444444444444444444444444444444444444").Return([]*vcsinfo.LongCommit{
		{ // These are returned with the most recent commits first
			ShortCommit: &vcsinfo.ShortCommit{
				Hash:    "4444444444444444444444444444444444444444",
				Author:  "author 4",
				Subject: "subject 4 (#000004)",
			},
			Body:      "Does not matter",
			Timestamp: time.Date(2021, time.February, 25, 10, 4, 0, 0, time.UTC),
		},
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash:    "3333333333333333333333333333333333333333",
				Author:  "author 3",
				Subject: `Revert "risky change (#000002)" (#000003)`,
			},
			Body:      "Does not matter",
			Timestamp: time.Date(2021, time.February, 25, 10, 3, 0, 0, time.UTC),
		},
		// LogFirstParent excludes the first one mentioned.
	}, nil)

	mc := monitorConfig{
		RepoURL:             "https://example.com/my-repo.git",
		SystemName:          "github",
		branch:              "main",
		ExtractionTechnique: FromSubject,
		InitialCommit:       "1111111111111111111111111111111111111111", // should be ignored
	}
	require.NoError(t, checkForLandedCycle(ctx, db, &mgl, mc))

	actualRows := sqltest.GetAllRows(ctx, t, db, "TrackingCommits", &schema.TrackingCommitRow{}).([]schema.TrackingCommitRow)
	assert.Equal(t, []schema.TrackingCommitRow{{
		Repo:        "https://example.com/my-repo.git",
		LastGitHash: "4444444444444444444444444444444444444444",
	}}, actualRows)

	cls := sqltest.GetAllRows(ctx, t, db, "Changelists", &schema.ChangelistRow{}).([]schema.ChangelistRow)
	assert.Equal(t, []schema.ChangelistRow{{
		ChangelistID:     "github_000003",
		System:           "github",
		Status:           schema.StatusLanded,
		OwnerEmail:       "user1@example.com",
		Subject:          `Revert "risky change (#000002)"`, // unchanged
		LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
	}, {
		ChangelistID:     "github_000004",
		System:           "github",
		Status:           schema.StatusLanded,
		OwnerEmail:       "whomever@example.com",
		Subject:          "subject 4", // unchanged
		LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
	}}, cls)

	// This cycle shouldn't touch the GitCommits tables
	commits := sqltest.GetAllRows(ctx, t, db, "GitCommits", &schema.GitCommitRow{}).([]schema.GitCommitRow)
	assert.Empty(t, commits)
}

func TestCheckForLandedCycle_LegacyMode_StatusNotChanged(t *testing.T) {
	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	existingData := schema.Tables{
		TrackingCommits: []schema.TrackingCommitRow{{
			Repo:        "https://example.com/my-repo.git",
			LastGitHash: "2222222222222222222222222222222222222222",
		}}, Changelists: []schema.ChangelistRow{{
			ChangelistID:     "github_000004",
			System:           "github",
			Status:           schema.StatusOpen,
			OwnerEmail:       "whomever@example.com",
			Subject:          "subject 4",
			LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
		}, {
			ChangelistID:     "github_000003",
			System:           "github",
			Status:           schema.StatusOpen,
			OwnerEmail:       "user1@example.com",
			Subject:          `Revert "risky change (#000002)"`,
			LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
		}},
	}
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))

	mgl := mocks.GitilesLogger{}
	mgl.On("Log", testutils.AnyContext, "main", mock.Anything).Return([]*vcsinfo.LongCommit{
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash: "4444444444444444444444444444444444444444",
				// The rest is ignored from Log
			},
		},
	}, nil)

	mgl.On("LogFirstParent", testutils.AnyContext, "2222222222222222222222222222222222222222", "4444444444444444444444444444444444444444").Return([]*vcsinfo.LongCommit{
		{ // These are returned with the most recent commits first
			ShortCommit: &vcsinfo.ShortCommit{
				Hash:    "4444444444444444444444444444444444444444",
				Author:  "author 4",
				Subject: "subject 4 (#000004)",
			},
			Body:      "Does not matter",
			Timestamp: time.Date(2021, time.February, 25, 10, 4, 0, 0, time.UTC),
		},
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash:    "3333333333333333333333333333333333333333",
				Author:  "author 3",
				Subject: `Revert "risky change (#000002)" (#000003)`,
			},
			Body:      "Does not matter",
			Timestamp: time.Date(2021, time.February, 25, 10, 3, 0, 0, time.UTC),
		},
		// LogFirstParent excludes the first one mentioned.
	}, nil)

	mc := monitorConfig{
		RepoURL:             "https://example.com/my-repo.git",
		SystemName:          "github",
		branch:              "main",
		ExtractionTechnique: FromSubject,
		InitialCommit:       "1111111111111111111111111111111111111111", // should be ignored
		LegacyUpdaterInUse:  true,
	}
	require.NoError(t, checkForLandedCycle(ctx, db, &mgl, mc))

	actualRows := sqltest.GetAllRows(ctx, t, db, "TrackingCommits", &schema.TrackingCommitRow{}).([]schema.TrackingCommitRow)
	assert.Equal(t, []schema.TrackingCommitRow{{
		Repo:        "https://example.com/my-repo.git",
		LastGitHash: "4444444444444444444444444444444444444444",
	}}, actualRows)

	cls := sqltest.GetAllRows(ctx, t, db, "Changelists", &schema.ChangelistRow{}).([]schema.ChangelistRow)
	assert.Equal(t, []schema.ChangelistRow{{
		ChangelistID:     "github_000003",
		System:           "github",
		Status:           schema.StatusOpen, // not set
		OwnerEmail:       "user1@example.com",
		Subject:          `Revert "risky change (#000002)"`, // unchanged
		LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
	}, {
		ChangelistID:     "github_000004",
		System:           "github",
		Status:           schema.StatusOpen, // not set
		OwnerEmail:       "whomever@example.com",
		Subject:          "subject 4", // unchanged
		LastIngestedData: time.Date(2021, time.March, 1, 1, 1, 1, 0, time.UTC),
	}}, cls)

	// This cycle shouldn't touch the GitCommits tables
	commits := sqltest.GetAllRows(ctx, t, db, "GitCommits", &schema.GitCommitRow{}).([]schema.GitCommitRow)
	assert.Empty(t, commits)
}

func TestCheckForLandedCycle_TriageExistingData_Success(t *testing.T) {
	ctx := context.Background()
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	existingData := dks.Build()
	existingData.Expectations = append(existingData.Expectations, []schema.ExpectationRow{{
		GroupingID: h(roundRectGrouping),
		Digest:     d(dks.DigestE01Pos_CL),
		Label:      schema.LabelUntriaged,
	}, {
		GroupingID: h(roundRectGrouping),
		Digest:     d(dks.DigestE02Pos_CL),
		Label:      schema.LabelUntriaged,
	}, {
		GroupingID: h(sevenGrouping),
		Digest:     d(dks.DigestD01Pos_CL),
		Label:      schema.LabelPositive,
	}}...)
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))

	clLandedTime := time.Date(2021, time.April, 1, 1, 1, 1, 0, time.UTC)

	mgl := mocks.GitilesLogger{}
	mgl.On("Log", testutils.AnyContext, "main", mock.Anything).Return([]*vcsinfo.LongCommit{
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash: "2222222222222222222222222222222222222222",
				// The rest is ignored from Log
			},
		},
	}, nil)

	mgl.On("LogFirstParent", testutils.AnyContext, "1111111111111111111111111111111111111111", "2222222222222222222222222222222222222222").Return([]*vcsinfo.LongCommit{
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash:    "2222222222222222222222222222222222222222",
				Author:  dks.UserTwo,
				Subject: "Increase test coverage",
			},
			Body:      "Reviewed-on: https://example.com/c/my-repo/+/CL_new_tests",
			Timestamp: clLandedTime,
		},
		// LogFirstParent excludes the first one mentioned.
	}, nil)

	mc := monitorConfig{
		RepoURL:             "https://example.com/my-repo.git",
		SystemName:          dks.GerritInternalCRS,
		branch:              "main",
		ExtractionTechnique: ReviewedLine,
		InitialCommit:       "1111111111111111111111111111111111111111",
	}
	require.NoError(t, checkForLandedCycle(ctx, db, &mgl, mc))

	actualRows := sqltest.GetAllRows(ctx, t, db, "TrackingCommits", &schema.TrackingCommitRow{}).([]schema.TrackingCommitRow)
	assert.Equal(t, []schema.TrackingCommitRow{{
		Repo:        "https://example.com/my-repo.git",
		LastGitHash: "2222222222222222222222222222222222222222",
	}}, actualRows)

	cls := sqltest.GetAllRows(ctx, t, db, "Changelists", &schema.ChangelistRow{}).([]schema.ChangelistRow)
	assert.Equal(t, []schema.ChangelistRow{{
		ChangelistID:     "gerrit-internal_CL_new_tests",
		System:           dks.GerritInternalCRS,
		Status:           schema.StatusLanded, // updated
		OwnerEmail:       dks.UserTwo,
		Subject:          "Increase test coverage",
		LastIngestedData: time.Date(2020, time.December, 12, 9, 20, 33, 0, time.UTC),
	}, {
		ChangelistID:     "gerrit_CL_fix_ios",
		System:           dks.GerritCRS,
		Status:           schema.StatusOpen, // not touched
		OwnerEmail:       dks.UserOne,
		Subject:          "Fix iOS",
		LastIngestedData: time.Date(2020, time.December, 10, 4, 5, 6, 0, time.UTC),
	}, {
		ChangelistID:     "gerrit_CLdisallowtriaging",
		System:           dks.GerritCRS,
		Status:           schema.StatusOpen, // not touched
		OwnerEmail:       dks.UserOne,
		Subject:          "add test with disallow triaging",
		LastIngestedData: time.Date(2020, time.December, 12, 16, 0, 0, 0, time.UTC),
	}, {
		ChangelistID:     "gerrit_CLhaslanded",
		System:           dks.GerritCRS,
		Status:           schema.StatusLanded,
		OwnerEmail:       dks.UserTwo,
		Subject:          "was landed",
		LastIngestedData: time.Date(2020, time.May, 5, 5, 5, 0, 0, time.UTC),
	}, {
		ChangelistID:     "gerrit_CLisabandoned",
		System:           dks.GerritCRS,
		Status:           schema.StatusAbandoned,
		OwnerEmail:       dks.UserOne,
		Subject:          "was abandoned",
		LastIngestedData: time.Date(2020, time.June, 6, 6, 6, 0, 0, time.UTC),
	}, {
		ChangelistID:     "gerrit_CLmultipledatapoints",
		System:           dks.GerritCRS,
		Status:           schema.StatusOpen,
		OwnerEmail:       dks.UserOne,
		Subject:          "multiple datapoints",
		LastIngestedData: time.Date(2020, time.December, 12, 14, 0, 0, 0, time.UTC),
	}}, cls)

	records := sqltest.GetAllRows(ctx, t, db, "ExpectationRecords", &schema.ExpectationRecordRow{}).([]schema.ExpectationRecordRow)
	require.Len(t, records, len(existingData.ExpectationRecords)+2) // 2 users triaged on this CL
	user2RecordID := records[0].ExpectationRecordID
	user4RecordID := records[1].ExpectationRecordID
	assert.Equal(t, []schema.ExpectationRecordRow{{
		ExpectationRecordID: user2RecordID,
		BranchName:          nil,
		UserName:            dks.UserTwo,
		TriageTime:          clLandedTime,
		NumChanges:          2, // 2 of the users triages undid each other
	}, {
		ExpectationRecordID: user4RecordID,
		BranchName:          nil,
		UserName:            dks.UserFour,
		TriageTime:          clLandedTime,
		NumChanges:          1,
	}}, records[:2])

	deltas := sqltest.GetAllRows(ctx, t, db, "ExpectationDeltas", &schema.ExpectationDeltaRow{}).([]schema.ExpectationDeltaRow)
	assert.Contains(t, deltas, schema.ExpectationDeltaRow{
		ExpectationRecordID: user2RecordID,
		GroupingID:          h(roundRectGrouping),
		Digest:              d(dks.DigestE01Pos_CL),
		LabelBefore:         schema.LabelUntriaged,
		LabelAfter:          schema.LabelPositive,
	})
	assert.Contains(t, deltas, schema.ExpectationDeltaRow{
		ExpectationRecordID: user2RecordID,
		GroupingID:          h(roundRectGrouping),
		Digest:              d(dks.DigestE02Pos_CL),
		LabelBefore:         schema.LabelUntriaged,
		LabelAfter:          schema.LabelPositive,
	})
	assert.Contains(t, deltas, schema.ExpectationDeltaRow{
		ExpectationRecordID: user4RecordID,
		GroupingID:          h(sevenGrouping),
		Digest:              d(dks.DigestD01Pos_CL),
		LabelBefore:         schema.LabelUntriaged,
		LabelAfter:          schema.LabelPositive,
	}, deltas)

	expectations := sqltest.GetAllRows(ctx, t, db, "Expectations", &schema.ExpectationRow{}).([]schema.ExpectationRow)
	assert.Contains(t, expectations, schema.ExpectationRow{
		GroupingID:          h(roundRectGrouping),
		Digest:              d(dks.DigestE01Pos_CL),
		Label:               schema.LabelPositive,
		ExpectationRecordID: &user2RecordID,
	})
	assert.Contains(t, expectations, schema.ExpectationRow{
		GroupingID:          h(roundRectGrouping),
		Digest:              d(dks.DigestE02Pos_CL),
		Label:               schema.LabelPositive,
		ExpectationRecordID: &user2RecordID,
	})
	assert.Contains(t, expectations, schema.ExpectationRow{
		GroupingID:          h(sevenGrouping),
		Digest:              d(dks.DigestD01Pos_CL),
		Label:               schema.LabelPositive,
		ExpectationRecordID: &user4RecordID,
	})
}
*/
// h returns the MD5 hash of the provided string.
func h(s string) []byte {
	hash := md5.Sum([]byte(s))
	return hash[:]
}

// d returns the bytes associated with the hex-encoded digest string.
func d(digest types.Digest) []byte {
	if len(digest) != 2*md5.Size {
		panic("digest wrong length " + string(digest))
	}
	b, err := hex.DecodeString(string(digest))
	if err != nil {
		panic(err)
	}
	return b
}

const (
	roundRectGrouping = `{"name":"round rect","source_type":"round"}`
	sevenGrouping     = `{"name":"seven","source_type":"text"}`
)

func Test_extractReviewedLine(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		clBody string
		want   string
	}{
		{
			name:   "Standard Reviewed-on line",
			clBody: "Some commit message\n\nReviewed-on: https://example.com/c/my-repo/+/12345\nMore text",
			want:   "12345",
		},
		{
			name:   "No Reviewed-on line",
			clBody: "Some commit message without the keyword.",
			want:   "",
		},
		{
			name:   "Multiple Reviewed-on lines",
			clBody: "Reviewed-on: https://example.com/c/my-repo/+/11111\nReviewed-on: https://example.com/c/my-repo/+/22222",
			want:   "11111", // Expect the first one.
		},
		{
			name:   "Ignore if the Reviewed-on line is part of a revert message",
			clBody: "> Reviewed-on:    https://example.com/c/my-repo/+/33333   ",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractReviewedLine(tt.clBody)
			if got != tt.want {
				t.Errorf("extractReviewedLine() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_extractFromSubject(t *testing.T) {
	tests := []struct {
		name string // description of this test case
		// Named input parameters for target function.
		subject string
		want    string
	}{
		{
			name:    "Standard subject with CL number",
			subject: "Implement new feature (#6789)",
			want:    "6789",
		},
		{
			name:    "Subject without CL number",
			subject: "Fix critical bug",
			want:    "",
		},
		{
			name:    "Multiple CL numbers in subject",
			subject: "Revert \"Add experimental feature (#1234)\" (#5678)",
			want:    "5678", // Expect the last one.
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractFromSubject(tt.subject)
			if got != tt.want {
				t.Errorf("extractFromSubject() = %v, want %v", got, tt.want)
			}
		})
	}
}

package incremental

import (
	"context"
	"testing"
	"time"

	assert "github.com/stretchr/testify/require"
	"go.skia.org/infra/go/git/gitinfo"
	"go.skia.org/infra/go/git/repograph"
	git_testutils "go.skia.org/infra/go/git/testutils"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/go/vcsinfo"
	"go.skia.org/infra/task_scheduler/go/window"
)

func assertBranches(t *testing.T, gb *git_testutils.GitBuilder, actual map[string][]*gitinfo.GitBranch, expect map[string]string) {
	actualBranches := make(map[string]string, len(actual[gb.RepoUrl()]))
	for _, branch := range actual[gb.RepoUrl()] {
		actualBranches[branch.Name] = branch.Head
	}
	testutils.AssertDeepEqual(t, expect, actualBranches)
}

func assertCommits(t *testing.T, gb *git_testutils.GitBuilder, actual map[string][]*vcsinfo.LongCommit, expect []string) {
	actualMap := util.NewStringSet(nil)
	for _, c := range actual[gb.RepoUrl()] {
		actualMap[c.Hash] = true
	}
	expectMap := util.NewStringSet(expect)
	for _, c := range expect {
		expectMap[c] = true
	}
	testutils.AssertDeepEqual(t, expectMap, actualMap)
}

func TestIncrementalCommits(t *testing.T) {
	testutils.MediumTest(t)

	// Setup.
	ctx := context.Background()
	gb := git_testutils.GitInit(t, ctx)
	defer gb.Cleanup()
	c0 := gb.CommitGen(ctx, "file1")
	wd, cleanupWd := testutils.TempDir(t)
	defer cleanupWd()
	repo, err := repograph.NewGraph(ctx, gb.Dir(), wd)
	assert.NoError(t, err)
	repos := repograph.Map{
		gb.RepoUrl(): repo,
	}
	N := 100
	w, err := window.New(24*time.Hour, N, repos)
	assert.NoError(t, err)
	cc := newCommitsCache(repos)

	// Initial update. Expect a single branch with one commit.
	branches, commits, err := cc.Update(ctx, w, false, N)
	assert.NoError(t, err)
	assertBranches(t, gb, branches, map[string]string{
		"master": c0,
	})
	assertCommits(t, gb, commits, []string{c0})

	// Update again, with no new commits. Expect empty response.
	branches, commits, err = cc.Update(ctx, w, false, N)
	assert.NoError(t, err)
	assertBranches(t, gb, branches, map[string]string{})
	assertCommits(t, gb, commits, []string{})

	// Passing in reset=true should give us ALL commits and branches,
	// regardless of whether they're new.
	branches, commits, err = cc.Update(ctx, w, true, N)
	assert.NoError(t, err)
	assertBranches(t, gb, branches, map[string]string{
		"master": c0,
	})
	assertCommits(t, gb, commits, []string{c0})

	// Add some new commits.
	c1 := gb.CommitGen(ctx, "file1")
	c2 := gb.CommitGen(ctx, "file1")
	branches, commits, err = cc.Update(ctx, w, false, N)
	assert.NoError(t, err)
	assertBranches(t, gb, branches, map[string]string{
		"master": c2,
	})
	assertCommits(t, gb, commits, []string{c1, c2})

	// Add a new branch, with no commits.
	gb.CreateBranchTrackBranch(ctx, "branch2", "origin/master")
	branches, commits, err = cc.Update(ctx, w, false, N)
	assert.NoError(t, err)
	assertBranches(t, gb, branches, map[string]string{
		"master":  c2,
		"branch2": c2,
	})
	assertCommits(t, gb, commits, []string{})

	// Add a commit on the new branch.
	c3 := gb.CommitGen(ctx, "file2")
	branches, commits, err = cc.Update(ctx, w, false, N)
	assert.NoError(t, err)
	assertBranches(t, gb, branches, map[string]string{
		"master":  c2,
		"branch2": c3,
	})
	assertCommits(t, gb, commits, []string{c3})

	// Merge branch2 back into master. Note that, since there are no new
	// commits on master, this does not create a merge commit but just
	// updates HEAD of master to point at c3.
	gb.CheckoutBranch(ctx, "master")
	mergeCommit := gb.MergeBranch(ctx, "branch2")
	assert.Equal(t, c3, mergeCommit)
	branches, commits, err = cc.Update(ctx, w, false, N)
	assert.NoError(t, err)
	assertBranches(t, gb, branches, map[string]string{
		"master":  c3,
		"branch2": c3,
	})
	assertCommits(t, gb, commits, []string{})

	// Add a new branch. Add commits on both master and branch3.
	gb.CreateBranchTrackBranch(ctx, "branch3", "origin/master")
	c4 := gb.CommitGen(ctx, "file3")
	gb.CheckoutBranch(ctx, "master")
	c5 := gb.CommitGen(ctx, "file1")
	branches, commits, err = cc.Update(ctx, w, false, N)
	assert.NoError(t, err)
	assertBranches(t, gb, branches, map[string]string{
		"master":  c5,
		"branch2": c3,
		"branch3": c4,
	})
	assertCommits(t, gb, commits, []string{c4, c5})

	// Merge branch3 back into master. Because there are commits on both
	// branches, a merge commit will be created.
	c6 := gb.MergeBranch(ctx, "branch3")
	assert.NotEqual(t, c6, c4) // Ensure that we actually created a merge commit.
	branches, commits, err = cc.Update(ctx, w, false, N)
	assert.NoError(t, err)
	assertBranches(t, gb, branches, map[string]string{
		"master":  c6,
		"branch2": c3,
		"branch3": c4,
	})
	assertCommits(t, gb, commits, []string{c6})
}

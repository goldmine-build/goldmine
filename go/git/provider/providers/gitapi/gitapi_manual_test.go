package gitapi

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.goldmine.build/go/deepequal/assertdeep"
	"go.goldmine.build/go/git/provider"
	"go.goldmine.build/go/skerr"
)

func clientForTest(t *testing.T) (context.Context, *GitApi) {
	ctx := context.Background()
	ghProvider, err := New(
		ctx,
		"", // os.Getenv("HOME")+"/.gitpat",
		"octocat",
		"Hello-World",
		"test",
	)
	assert.NoError(t, err)
	return ctx, ghProvider
}

func TestCommitsFromMostRecentGitHashToHead_GoodHash_SuccessWithGoodResults(t *testing.T) {
	ctx, client := clientForTest(t)

	hashes := []string{}
	err := client.CommitsFromMostRecentGitHashToHead(ctx, "553c2077f0edc3d5dc5d17262f6aa498e69d6f8e", func(c provider.Commit) error {
		hashes = append(hashes, c.GitHash)
		return nil
	})
	assert.NoError(t, err)
	expected := []string{
		"762941318ee16e59dabbacb1b4049eec22f0d303",
		"7fd1a60b01f91b314f59955a4e4d4e80d8edf11d",
		"b3cbd5bbd7e81436d2eee04537ea2b4c0cad4cdf",
	}
	assertdeep.Equal(t, expected, hashes)
}

func TestCommitsFromMostRecentGitHashToHead_HashNotOnBranch_Failure(t *testing.T) {
	ctx, client := clientForTest(t)

	err := client.CommitsFromMostRecentGitHashToHead(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", func(c provider.Commit) error {
		return nil
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "422 No commit found for SHA")
}

func TestCommitsFromMostRecentGitHashToHead_CallbackReturnsError_ReturnsError(t *testing.T) {
	ctx, client := clientForTest(t)

	err := client.CommitsFromMostRecentGitHashToHead(ctx, "553c2077f0edc3d5dc5d17262f6aa498e69d6f8e", func(c provider.Commit) error {
		return skerr.Fmt("thinkgs went bad here")
	})
	assert.Error(t, err)
}

func TestGitHashesInRangeForFile_FileOnlyChangedOnLastCommit_OnlyLastCommitReturned(t *testing.T) {
	ctx, client := clientForTest(t)

	commits, err := client.GitHashesInRangeForFile(ctx, "553c2077f0edc3d5dc5d17262f6aa498e69d6f8e", "b3cbd5bbd7e81436d2eee04537ea2b4c0cad4cdf",
		"CONTRIBUTING.md")
	assert.NoError(t, err)
	assertdeep.Equal(t, commits, []string{"b3cbd5bbd7e81436d2eee04537ea2b4c0cad4cdf"})
}

func TestGitHashesInRangeForFile_UnknownFileName_EmptyListReturned(t *testing.T) {
	ctx, client := clientForTest(t)

	commits, err := client.GitHashesInRangeForFile(ctx, "553c2077f0edc3d5dc5d17262f6aa498e69d6f8e", "b3cbd5bbd7e81436d2eee04537ea2b4c0cad4cdf",
		"some-file-that-does-not-appear-in-the-repo.txt")
	assert.NoError(t, err)
	assert.Empty(t, commits)
}

func TestGitHashesInRangeForFile_BeginHashNotOnBranch_ReturnsError(t *testing.T) {
	ctx, client := clientForTest(t)

	commits, err := client.GitHashesInRangeForFile(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "b3cbd5bbd7e81436d2eee04537ea2b4c0cad4cdf",
		"some-file-that-does-not-appear-in-the-repo.txt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "422 No commit found for SHA")
	assert.Empty(t, commits)
}

func TestLogEntry_GoodHash_LogEntryReturned(t *testing.T) {
	ctx, client := clientForTest(t)
	s, err := client.LogEntry(ctx, "7fd1a60b01f91b314f59955a4e4d4e80d8edf11d")
	assert.NoError(t, err)
	assert.Equal(t, `commit 7fd1a60b01f91b314f59955a4e4d4e80d8edf11d
Author octocat@nowhere.com
Date 06 Mar 12 23:06 +0000

    Merge pull request #6 from Spaceghost/patch-1

New line at end of file.
`, s)
}

func TestLogEntry_UnknownHash_ErroReturned(t *testing.T) {
	ctx, client := clientForTest(t)
	_, err := client.LogEntry(ctx, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "422 No commit found for SHA")
}

func TestUpdate(t *testing.T) {
	ctx, client := clientForTest(t)
	err := client.Update(ctx)
	assert.NoError(t, err)
}

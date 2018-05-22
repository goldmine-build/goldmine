package blacklist

import (
	"context"
	"fmt"
	"io/ioutil"
	"path"
	"testing"

	"go.skia.org/infra/go/git/repograph"
	git_testutils "go.skia.org/infra/go/git/testutils"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/testutils"

	assert "github.com/stretchr/testify/require"
)

func TestAddRemove(t *testing.T) {
	testutils.SmallTest(t)
	// Setup.
	tmp, err := ioutil.TempDir("", "")
	assert.NoError(t, err)
	defer testutils.RemoveAll(t, tmp)
	assert.NoError(t, err)
	f := path.Join(tmp, "blacklist.json")
	b1, err := FromFile(f)
	assert.NoError(t, err)

	// Test.
	assert.Equal(t, len(DEFAULT_RULES), len(b1.Rules))
	r1 := &Rule{
		AddedBy:          "test@google.com",
		TaskSpecPatterns: []string{".*"},
		Name:             "My Rule",
	}
	assert.NoError(t, b1.addRule(r1))
	b2, err := FromFile(f)
	assert.NoError(t, err)
	testutils.AssertDeepEqual(t, b1, b2)

	assert.NoError(t, b1.RemoveRule(r1.Name))
	b2, err = FromFile(f)
	assert.NoError(t, err)
	testutils.AssertDeepEqual(t, b1, b2)
}

func TestRules(t *testing.T) {
	testutils.SmallTest(t)
	type testCase struct {
		taskSpec    string
		commit      string
		expectMatch bool
		msg         string
	}
	tests := []struct {
		rule  Rule
		cases []testCase
	}{
		{
			rule: Rule{
				AddedBy: "test@google.com",
				Name:    "Match all taskSpecs",
				TaskSpecPatterns: []string{
					".*",
				},
			},
			cases: []testCase{
				{
					taskSpec:    "My-TaskSpec",
					commit:      "abc123",
					expectMatch: true,
					msg:         "'.*' should match all taskSpecs.",
				},
			},
		},
		{
			rule: Rule{
				AddedBy: "test@google.com",
				Name:    "Match some taskSpecs",
				TaskSpecPatterns: []string{
					"My.*kSpec",
				},
			},
			cases: []testCase{
				{
					taskSpec:    "My-TaskSpec",
					commit:      "abc123",
					expectMatch: true,
					msg:         "Should match",
				},
				{
					taskSpec:    "Your-TaskSpec",
					commit:      "abc123",
					expectMatch: false,
					msg:         "Should not match",
				},
			},
		},
		{
			rule: Rule{
				AddedBy: "test@google.com",
				Name:    "Match one commit",
				TaskSpecPatterns: []string{
					".*",
				},
				Commits: []string{
					"abc123",
				},
			},
			cases: []testCase{
				{
					taskSpec:    "My-TaskSpec",
					commit:      "abc123",
					expectMatch: true,
					msg:         "Single commit match",
				},
				{
					taskSpec:    "My-TaskSpec",
					commit:      "def456",
					expectMatch: false,
					msg:         "Single commit does not match",
				},
			},
		},
		{
			rule: Rule{
				AddedBy:          "test@google.com",
				Name:             "Commit range",
				TaskSpecPatterns: []string{},
				Commits: []string{
					"abc123",
					"bbadbeef",
				},
			},
			cases: []testCase{
				{
					taskSpec:    "My-TaskSpec",
					commit:      "abc123",
					expectMatch: true,
					msg:         "Inside commit range (1)",
				},
				{
					taskSpec:    "My-TaskSpec",
					commit:      "bbadbeef",
					expectMatch: true,
					msg:         "Inside commit range (2)",
				},
				{
					taskSpec:    "My-TaskSpec",
					commit:      "def456",
					expectMatch: false,
					msg:         "Outside commit range",
				},
			},
		},
	}
	for _, test := range tests {
		for _, c := range test.cases {
			assert.Equal(t, c.expectMatch, test.rule.Match(c.taskSpec, c.commit), c.msg)
		}
	}
}

func setupTestRepo(t *testing.T) (context.Context, *git_testutils.GitBuilder, []string) {
	ctx := context.Background()
	gb := git_testutils.GitInit(t, ctx)
	commits := []string{}

	/*
	   * commit 8
	   |
	   *   commit 7
	   |\  Merge: 4 6
	   | |
	   | |     Merge branch 'mybranch'
	   | |
	   | * commit 6
	   | |
	   | * commit 5
	   | |
	   * | commit 4
	   | |
	   * | commit 3
	   | |
	   * | commit 2
	   |/
	   * commit 1
	   |
	   * commit 0
	*/

	// 0
	gb.Add(ctx, "a.txt", "")
	commits = append(commits, gb.Commit(ctx))

	// 1
	gb.Add(ctx, "a.txt", "\n")
	commits = append(commits, gb.Commit(ctx))

	// 2
	gb.Add(ctx, "a.txt", "\n\n")
	commits = append(commits, gb.Commit(ctx))

	// 3
	gb.Add(ctx, "a.txt", "\n\n\n")
	commits = append(commits, gb.Commit(ctx))

	// 4
	gb.Add(ctx, "a.txt", "\n\n\n\n")
	commits = append(commits, gb.Commit(ctx))

	// 5
	gb.CreateBranchAtCommit(ctx, "mybranch", commits[1])
	gb.Add(ctx, "b.txt", "\n\n")
	commits = append(commits, gb.Commit(ctx))

	// 6
	gb.Add(ctx, "b.txt", "\n\n\n")
	commits = append(commits, gb.Commit(ctx))

	// 7
	gb.CheckoutBranch(ctx, "master")
	commits = append(commits, gb.MergeBranch(ctx, "mybranch"))

	// 8
	gb.Add(ctx, "a.txt", "\n\n\n\n\n")
	commits = append(commits, gb.Commit(ctx))

	return ctx, gb, commits
}

func TestValidation(t *testing.T) {
	testutils.LargeTest(t)
	// Setup.
	ctx, gb, commits := setupTestRepo(t)
	defer gb.Cleanup()
	tmp, err := ioutil.TempDir("", "")
	assert.NoError(t, err)
	defer testutils.RemoveAll(t, tmp)
	repos := repograph.Map{}
	repo, err := repograph.NewGraph(ctx, gb.RepoUrl(), tmp)
	assert.NoError(t, err)
	repos[gb.RepoUrl()] = repo
	assert.NoError(t, repos.Update(ctx))

	// Test.
	tests := []struct {
		rule   Rule
		expect error
		msg    string
	}{
		{
			rule: Rule{
				AddedBy:          "",
				Name:             "My rule",
				TaskSpecPatterns: []string{".*"},
				Commits:          []string{},
			},
			expect: fmt.Errorf("Rules must have an AddedBy user."),
			msg:    "No AddedBy",
		},
		{
			rule: Rule{
				AddedBy:          "test@google.com",
				Name:             "",
				TaskSpecPatterns: []string{".*"},
				Commits:          []string{},
			},
			expect: fmt.Errorf("Rules must have a name."),
			msg:    "No Name",
		},
		{
			rule: Rule{
				AddedBy:          "test@google.com",
				Name:             "01234567890123456789012345678901234567890123456789",
				TaskSpecPatterns: []string{".*"},
				Commits:          []string{},
			},
			expect: nil,
			msg:    "Long Name",
		},
		{
			rule: Rule{
				AddedBy:          "test@google.com",
				Name:             "012345678901234567890123456789012345678901234567890",
				TaskSpecPatterns: []string{".*"},
				Commits:          []string{},
			},
			expect: fmt.Errorf("Rule names must be shorter than 50 characters. Use the Description field for detailed information."),
			msg:    "Too Long Name",
		},
		{
			rule: Rule{
				AddedBy:          "test@google.com",
				Name:             "My rule",
				TaskSpecPatterns: []string{},
				Commits:          []string{},
			},
			expect: fmt.Errorf("Rules must include a taskSpec pattern and/or a commit/range."),
			msg:    "No taskSpecs or commits",
		},
		{
			rule: Rule{
				AddedBy:          "test@google.com",
				Name:             "My rule",
				TaskSpecPatterns: []string{".*"},
				Commits:          []string{},
			},
			expect: nil,
			msg:    "One taskSpec pattern, no commits",
		},
		{
			rule: Rule{
				AddedBy: "test@google.com",
				Name:    "My rule",
				TaskSpecPatterns: []string{
					"A.*B",
					"[C|D]{42}",
					"[Your|My]-TaskSpec",
					"Test.*",
					"Some-TaskSpec",
				},
				Commits: []string{},
			},
			expect: nil,
			msg:    "Five taskSpec patterns, no commits",
		},
		{
			rule: Rule{
				AddedBy:          "test@google.com",
				Name:             "My rule",
				TaskSpecPatterns: []string{},
				Commits:          commits[8:9],
			},
			expect: nil,
			msg:    "One commit",
		},
		{
			rule: Rule{
				AddedBy:          "test@google.com",
				Name:             "My rule",
				TaskSpecPatterns: []string{},
				Commits: []string{
					commits[8][:38],
				},
			},
			expect: fmt.Errorf("Unable to find commit %s in any repo.", commits[8][:38]),
			msg:    "Invalid commit",
		},
		{
			rule: Rule{
				AddedBy:          "test@google.com",
				Name:             "My rule",
				TaskSpecPatterns: []string{},
				Commits:          commits[0:5],
			},
			expect: nil,
			msg:    "Five commits",
		},
	}
	for _, test := range tests {
		sklog.Infof(test.msg)
		assert.Equal(t, test.expect, ValidateRule(&test.rule, repos), test.msg)
	}
}

func TestCommitRange(t *testing.T) {
	testutils.LargeTest(t)
	// Setup.
	ctx, gb, commits := setupTestRepo(t)
	defer gb.Cleanup()
	tmp, err := ioutil.TempDir("", "")
	assert.NoError(t, err)
	defer testutils.RemoveAll(t, tmp)
	repos := repograph.Map{}
	repo, err := repograph.NewGraph(ctx, gb.RepoUrl(), tmp)
	assert.NoError(t, err)
	repos[gb.RepoUrl()] = repo
	assert.NoError(t, repos.Update(ctx))
	f := path.Join(tmp, "blacklist.json")
	b, err := FromFile(f)
	assert.NoError(t, err)

	// Test.

	// Create a commit range rule.
	startCommit := commits[0]
	endCommit := commits[6]
	rule, err := NewCommitRangeRule(ctx, "commit range", "test@google.com", "...", []string{}, startCommit, endCommit, repos)
	assert.NoError(t, err)
	err = b.AddRule(rule, repos)
	assert.NoError(t, err)

	// Ensure that we got the expected list of commits.
	assert.Equal(t, []string{
		commits[5],
		commits[1],
		commits[0],
	}, b.Rules["commit range"].Commits)

	// Test a few commits.
	tc := []struct {
		commit string
		expect bool
		msg    string
	}{
		{
			commit: "",
			expect: false,
			msg:    "empty commit does not match",
		},
		{
			commit: startCommit,
			expect: true,
			msg:    "startCommit matches",
		},
		{
			commit: endCommit,
			expect: false,
			msg:    "endCommit does not match",
		},
		{
			commit: commits[1],
			expect: true,
			msg:    "middle of range matches",
		},
		{
			commit: commits[8],
			expect: false,
			msg:    "out of range does not match",
		},
	}
	for _, c := range tc {
		assert.Equal(t, c.expect, b.Match("", c.commit))
	}
}

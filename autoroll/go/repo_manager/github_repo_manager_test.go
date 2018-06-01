package repo_manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	github_api "github.com/google/go-github/github"
	assert "github.com/stretchr/testify/require"

	"go.skia.org/infra/go/autoroll"
	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/git"
	git_testutils "go.skia.org/infra/go/git/testutils"
	"go.skia.org/infra/go/github"
	"go.skia.org/infra/go/mockhttpclient"
	"go.skia.org/infra/go/recipe_cfg"
	"go.skia.org/infra/go/testutils"
)

const (
	githubApiUrl = "https://api.github.com"
)

var (
	githubEmails = []string{"reviewer@chromium.org"}

	mockGithubUser = "superman"
	testPullNumber = 12345
)

func githubCfg() *GithubRepoManagerConfig {
	return &GithubRepoManagerConfig{
		DepotToolsRepoManagerConfig: DepotToolsRepoManagerConfig{
			CommonRepoManagerConfig: CommonRepoManagerConfig{
				ChildBranch:  "master",
				ChildPath:    childPath,
				ParentBranch: "master",
				Strategy:     ROLL_STRATEGY_BATCH,
			},
		},
	}
}

func TestGithubConfigValidation(t *testing.T) {
	testutils.SmallTest(t)

	cfg := githubCfg()
	cfg.ParentRepo = "repo" // Excluded from githubCfg.
	assert.NoError(t, cfg.Validate())

	// The only fields come from the nested Configs, so exclude them and
	// verify that we fail validation.
	cfg = &GithubRepoManagerConfig{}
	assert.Error(t, cfg.Validate())
}

func setupGithub(t *testing.T) (context.Context, string, *git_testutils.GitBuilder, []string, *git_testutils.GitBuilder, *exec.CommandCollector, func()) {
	wd, err := ioutil.TempDir("", "")
	assert.NoError(t, err)

	// Create child and parent repos.
	child := git_testutils.GitInit(t, context.Background())
	f := "somefile.txt"
	childCommits := make([]string, 0, 10)
	for i := 0; i < numChildCommits; i++ {
		childCommits = append(childCommits, child.CommitGen(context.Background(), f))
	}

	parent := git_testutils.GitInit(t, context.Background())
	parent.Add(context.Background(), "DEPS", fmt.Sprintf(`deps = {
  "%s": "%s@%s",
}`, childPath, child.RepoUrl(), childCommits[0]))
	parent.Commit(context.Background())

	mockRun := &exec.CommandCollector{}
	mockRun.SetDelegateRun(func(cmd *exec.Command) error {
		// Without this, the mock commands get confused with:
		// "Could not switch upstream branch from refs/remotes/remote/master to refs/remotes/origin/master"
		if strings.Contains(cmd.Name, "gclient") && (cmd.Args[0] == "sync" || cmd.Args[0] == "runhooks") {
			return nil
		}
		return exec.DefaultRun(cmd)
	})
	ctx := exec.NewContext(context.Background(), mockRun.Run)

	cleanup := func() {
		testutils.RemoveAll(t, wd)
		child.Cleanup()
		parent.Cleanup()
	}

	return ctx, wd, child, childCommits, parent, mockRun, cleanup
}

func setupFakeGithub(t *testing.T) (*github.GitHub, *mockhttpclient.URLMock) {
	urlMock := mockhttpclient.NewURLMock()

	// Mock /user endpoint.
	serializedUser, err := json.Marshal(&github_api.User{
		Login: &mockGithubUser,
	})
	assert.NoError(t, err)
	urlMock.MockOnce(githubApiUrl+"/user", mockhttpclient.MockGetDialogue(serializedUser))

	g, err := github.NewGitHub(context.Background(), "superman", "krypton", urlMock.Client(), "")
	assert.NoError(t, err)
	return g, urlMock
}

func mockGithubRequests(t *testing.T, urlMock *mockhttpclient.URLMock, from, to string, numCommits int) {
	// Mock /pulls endpoint.
	serializedPull, err := json.Marshal(&github_api.PullRequest{
		Number: &testPullNumber,
	})
	assert.NoError(t, err)
	reqType := "application/json"
	md := mockhttpclient.MockPostDialogueWithResponseCode(reqType, mockhttpclient.DONT_CARE_REQUEST, serializedPull, http.StatusCreated)
	urlMock.MockOnce(githubApiUrl+"/repos/superman/krypton/pulls", md)

	// Mock /comments endpoint.
	reqType = "application/json"
	reqBody := []byte(`{"body":"@reviewer : New roll has been created by fake.server.com"}
`)
	md = mockhttpclient.MockPostDialogueWithResponseCode(reqType, reqBody, nil, http.StatusCreated)
	urlMock.MockOnce(githubApiUrl+"/repos/superman/krypton/issues/12345/comments", md)
}

// TestGithubRepoManager tests all aspects of the GithubRepoManager except for CreateNewRoll.
func TestGithubRepoManager(t *testing.T) {
	testutils.LargeTest(t)

	ctx, wd, child, childCommits, parent, _, cleanup := setupGithub(t)
	defer cleanup()
	recipesCfg := filepath.Join(testutils.GetRepoRoot(t), recipe_cfg.RECIPE_CFG_PATH)

	g, _ := setupFakeGithub(t)
	cfg := githubCfg()
	cfg.ParentRepo = parent.RepoUrl()
	rm, err := NewGithubRepoManager(ctx, cfg, wd, g, recipesCfg, "fake.server.com")
	assert.NoError(t, err)
	assert.Equal(t, childCommits[0], rm.LastRollRev())
	assert.Equal(t, childCommits[len(childCommits)-1], rm.NextRollRev())

	// Test FullChildHash.
	for _, c := range childCommits {
		h, err := rm.FullChildHash(ctx, c[:12])
		assert.NoError(t, err)
		assert.Equal(t, c, h)
	}

	// Test update.
	lastCommit := child.CommitGen(context.Background(), "abc.txt")
	assert.NoError(t, rm.Update(ctx))
	assert.Equal(t, lastCommit, rm.NextRollRev())

	// RolledPast.
	rp, err := rm.RolledPast(ctx, childCommits[0])
	assert.NoError(t, err)
	assert.True(t, rp)
	for _, c := range childCommits[1:] {
		rp, err := rm.RolledPast(ctx, c)
		assert.NoError(t, err)
		assert.False(t, rp)
	}
	assert.Equal(t, mockGithubUser, rm.User())
}

func TestCreateNewGithubRoll(t *testing.T) {
	testutils.LargeTest(t)

	ctx, wd, child, childCommits, parent, _, cleanup := setupGithub(t)
	defer cleanup()
	recipesCfg := filepath.Join(testutils.GetRepoRoot(t), recipe_cfg.RECIPE_CFG_PATH)

	g, urlMock := setupFakeGithub(t)
	cfg := githubCfg()
	cfg.ParentRepo = parent.RepoUrl()
	rm, err := NewGithubRepoManager(ctx, cfg, wd, g, recipesCfg, "fake.server.com")
	assert.NoError(t, err)

	// Create a roll, assert that it's at tip of tree.
	mockGithubRequests(t, urlMock, rm.LastRollRev(), rm.NextRollRev(), rm.CommitsNotRolled())
	issue, err := rm.CreateNewRoll(ctx, rm.LastRollRev(), rm.NextRollRev(), githubEmails, cqExtraTrybots, false)
	assert.NoError(t, err)
	assert.Equal(t, issueNum, issue)

	p := git.GitDir(parent.Dir())
	head, err := p.GetBranchHead(ctx, ROLL_BRANCH)
	assert.NoError(t, err)
	lastUpload, err := p.Details(ctx, head)
	assert.NoError(t, err)
	from, to, err := autoroll.RollRev(lastUpload.Subject, func(h string) (string, error) {
		return git.GitDir(child.Dir()).RevParse(ctx, h)
	})
	assert.NoError(t, err)
	assert.Equal(t, childCommits[0], from)
	assert.Equal(t, childCommits[numChildCommits-1], to)
}

// Verify that we ran the PreUploadSteps.
func TestRanPreUploadStepsGithub(t *testing.T) {
	testutils.LargeTest(t)

	ctx, wd, _, _, parent, _, cleanup := setupGithub(t)
	defer cleanup()
	recipesCfg := filepath.Join(testutils.GetRepoRoot(t), recipe_cfg.RECIPE_CFG_PATH)

	g, urlMock := setupFakeGithub(t)
	cfg := githubCfg()
	cfg.ParentRepo = parent.RepoUrl()
	rm, err := NewGithubRepoManager(ctx, cfg, wd, g, recipesCfg, "fake.server.com")
	assert.NoError(t, err)
	ran := false
	rm.(*githubRepoManager).preUploadSteps = []PreUploadStep{
		func(context.Context, string) error {
			ran = true
			return nil
		},
	}

	// Create a roll, assert that we ran the PreUploadSteps.
	mockGithubRequests(t, urlMock, rm.LastRollRev(), rm.NextRollRev(), rm.CommitsNotRolled())
	_, createErr := rm.CreateNewRoll(ctx, rm.LastRollRev(), rm.NextRollRev(), githubEmails, cqExtraTrybots, false)
	assert.NoError(t, createErr)
	assert.True(t, ran)
}

// Verify that we fail when a PreUploadStep fails.
func TestErrorPreUploadStepsGithub(t *testing.T) {
	testutils.LargeTest(t)

	ctx, wd, _, _, parent, _, cleanup := setupGithub(t)
	defer cleanup()
	recipesCfg := filepath.Join(testutils.GetRepoRoot(t), recipe_cfg.RECIPE_CFG_PATH)

	g, urlMock := setupFakeGithub(t)
	cfg := githubCfg()
	cfg.ParentRepo = parent.RepoUrl()
	rm, err := NewGithubRepoManager(ctx, cfg, wd, g, recipesCfg, "fake.server.com")
	assert.NoError(t, err)
	ran := false
	expectedErr := errors.New("Expected error")
	rm.(*githubRepoManager).preUploadSteps = []PreUploadStep{
		func(context.Context, string) error {
			ran = true
			return expectedErr
		},
	}

	// Create a roll, assert that we ran the PreUploadSteps.
	mockGithubRequests(t, urlMock, rm.LastRollRev(), rm.NextRollRev(), rm.CommitsNotRolled())
	_, createErr := rm.CreateNewRoll(ctx, rm.LastRollRev(), rm.NextRollRev(), githubEmails, cqExtraTrybots, false)
	assert.Error(t, expectedErr, createErr)
	assert.True(t, ran)
}

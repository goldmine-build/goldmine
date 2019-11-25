package repo_manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	github_api "github.com/google/go-github/github"
	"github.com/stretchr/testify/require"
	"go.skia.org/infra/autoroll/go/codereview"
	"go.skia.org/infra/go/exec"
	git_testutils "go.skia.org/infra/go/git/testutils"
	"go.skia.org/infra/go/github"
	"go.skia.org/infra/go/mockhttpclient"
	"go.skia.org/infra/go/recipe_cfg"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/testutils/unittest"
)

func githubCR(t *testing.T, g *github.GitHub) codereview.CodeReview {
	rv, err := (&codereview.GithubConfig{
		RepoOwner:     "me",
		RepoName:      "my-repo",
		ChecksNum:     3,
		ChecksWaitFor: []string{"a", "b", "c"},
	}).Init(nil, g)
	require.NoError(t, err)
	return rv
}

func githubRmCfg() *GithubRepoManagerConfig {
	return &GithubRepoManagerConfig{
		CommonRepoManagerConfig: CommonRepoManagerConfig{
			ChildBranch:  "master",
			ChildPath:    "earth",
			ParentBranch: "master",
			ParentRepo:   "git@github.com:jorel/krypton.git",
		},
		ChildRepoURL: "git@github.com:superman/earth.git",
		RevisionFile: "dummy-file.txt",
	}
}

func TestGithubConfigValidation(t *testing.T) {
	unittest.SmallTest(t)

	cfg := githubRmCfg()
	require.NoError(t, cfg.Validate())

	// The only fields come from the nested Configs, so exclude them and
	// verify that we fail validation.
	cfg = &GithubRepoManagerConfig{}
	require.Error(t, cfg.Validate())
}

func setupGithub(t *testing.T) (context.Context, string, *git_testutils.GitBuilder, []string, *git_testutils.GitBuilder, *exec.CommandCollector, func()) {
	wd, err := ioutil.TempDir("", "")
	require.NoError(t, err)

	// Create child and parent repos.
	childPath := filepath.Join(wd, "github_repos", "earth")
	require.NoError(t, os.MkdirAll(childPath, 0755))
	child := git_testutils.GitInitWithDir(t, context.Background(), childPath)
	f := "somefile.txt"
	childCommits := make([]string, 0, 10)
	for i := 0; i < numChildCommits; i++ {
		childCommits = append(childCommits, child.CommitGen(context.Background(), f))
	}

	parentPath := filepath.Join(wd, "github_repos", "krypton")
	require.NoError(t, os.MkdirAll(parentPath, 0755))
	parent := git_testutils.GitInitWithDir(t, context.Background(), parentPath)
	parent.Add(context.Background(), "dummy-file.txt", fmt.Sprintf(`%s`, childCommits[0]))
	parent.Commit(context.Background())

	mockRun := &exec.CommandCollector{}
	mockRun.SetDelegateRun(func(ctx context.Context, cmd *exec.Command) error {
		if strings.Contains(cmd.Name, "git") {
			if cmd.Args[0] == "clone" || cmd.Args[0] == "fetch" {
				return nil
			}
			if cmd.Args[0] == "checkout" && cmd.Args[1] == "remote/master" {
				// Pretend origin is the remote branch for testing ease.
				cmd.Args[1] = "origin/master"
			}
		}
		return exec.DefaultRun(ctx, cmd)
	})
	ctx := exec.NewContext(context.Background(), mockRun.Run)

	cleanup := func() {
		testutils.RemoveAll(t, wd)
		child.Cleanup()
		parent.Cleanup()
	}

	return ctx, wd, child, childCommits, parent, mockRun, cleanup
}

func setupFakeGithub(t *testing.T, childCommits []string) (*github.GitHub, *mockhttpclient.URLMock) {
	urlMock := mockhttpclient.NewURLMock()

	// Mock /user endpoint.
	serializedUser, err := json.Marshal(&github_api.User{
		Login: &mockGithubUser,
		Email: &mockGithubUserEmail,
	})
	require.NoError(t, err)
	urlMock.MockOnce(githubApiUrl+"/user", mockhttpclient.MockGetDialogue(serializedUser))

	if childCommits != nil && len(childCommits) > 0 {
		// Mock getRawFile.
		urlMock.MockOnce("https://raw.githubusercontent.com/superman/krypton/master/dummy-file.txt", mockhttpclient.MockGetDialogue([]byte(childCommits[0])))
	}

	// Mock /issues endpoint for get and patch requests.
	serializedIssue, err := json.Marshal(&github_api.Issue{
		Labels: []github_api.Label{},
	})
	require.NoError(t, err)
	urlMock.MockOnce(githubApiUrl+"/repos/superman/krypton/issues/12345", mockhttpclient.MockGetDialogue(serializedIssue))
	patchRespBody := []byte(testutils.MarshalJSON(t, &github_api.PullRequest{}))
	patchReqType := "application/json"
	patchReqBody := []byte(`{"labels":["autoroller: commit"]}
`)
	patchMd := mockhttpclient.MockPatchDialogue(patchReqType, patchReqBody, patchRespBody)
	urlMock.MockOnce(githubApiUrl+"/repos/superman/krypton/issues/12345", patchMd)

	g, err := github.NewGitHub(context.Background(), "superman", "krypton", urlMock.Client())
	require.NoError(t, err)
	return g, urlMock
}

func mockGithubRequests(t *testing.T, urlMock *mockhttpclient.URLMock) {
	// Mock /pulls endpoint.
	serializedPull, err := json.Marshal(&github_api.PullRequest{
		Number: &testPullNumber,
	})
	require.NoError(t, err)
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
	unittest.LargeTest(t)

	ctx, wd, _, childCommits, _, _, cleanup := setupGithub(t)
	defer cleanup()
	recipesCfg := filepath.Join(testutils.GetRepoRoot(t), recipe_cfg.RECIPE_CFG_PATH)

	g, _ := setupFakeGithub(t, childCommits)
	cfg := githubRmCfg()
	rm, err := NewGithubRepoManager(ctx, cfg, wd, g, recipesCfg, "fake.server.com", nil, githubCR(t, g), false)
	require.NoError(t, err)
	lastRollRev, tipRev, notRolledRevs, err := rm.Update(ctx)
	require.NoError(t, err)
	require.Equal(t, childCommits[0], lastRollRev.Id)
	require.Equal(t, childCommits[len(childCommits)-1], tipRev.Id)
	require.Equal(t, len(childCommits)-1, len(notRolledRevs))
}

func TestCreateNewGithubRoll(t *testing.T) {
	unittest.LargeTest(t)

	ctx, wd, _, childCommits, _, _, cleanup := setupGithub(t)
	defer cleanup()
	recipesCfg := filepath.Join(testutils.GetRepoRoot(t), recipe_cfg.RECIPE_CFG_PATH)

	g, urlMock := setupFakeGithub(t, childCommits)
	cfg := githubRmCfg()
	rm, err := NewGithubRepoManager(ctx, cfg, wd, g, recipesCfg, "fake.server.com", nil, githubCR(t, g), false)
	require.NoError(t, err)
	lastRollRev, tipRev, notRolledRevs, err := rm.Update(ctx)
	require.NoError(t, err)

	// Create a roll.
	mockGithubRequests(t, urlMock)
	issue, err := rm.CreateNewRoll(ctx, lastRollRev, tipRev, notRolledRevs, emails, cqExtraTrybots, false)
	require.NoError(t, err)
	require.Equal(t, issueNum, issue)
}

// Verify that we ran the PreUploadSteps.
func TestRanPreUploadStepsGithub(t *testing.T) {
	unittest.LargeTest(t)

	ctx, wd, _, childCommits, _, _, cleanup := setupGithub(t)
	defer cleanup()
	recipesCfg := filepath.Join(testutils.GetRepoRoot(t), recipe_cfg.RECIPE_CFG_PATH)

	g, urlMock := setupFakeGithub(t, childCommits)
	cfg := githubRmCfg()
	rm, err := NewGithubRepoManager(ctx, cfg, wd, g, recipesCfg, "fake.server.com", nil, githubCR(t, g), false)
	require.NoError(t, err)
	lastRollRev, tipRev, notRolledRevs, err := rm.Update(ctx)
	require.NoError(t, err)
	ran := false
	rm.(*githubRepoManager).preUploadSteps = []PreUploadStep{
		func(context.Context, []string, *http.Client, string) error {
			ran = true
			return nil
		},
	}

	// Create a roll, assert that we ran the PreUploadSteps.
	mockGithubRequests(t, urlMock)
	_, createErr := rm.CreateNewRoll(ctx, lastRollRev, tipRev, notRolledRevs, emails, cqExtraTrybots, false)
	require.NoError(t, createErr)
	require.True(t, ran)
}

// Verify that we fail when a PreUploadStep fails.
func TestErrorPreUploadStepsGithub(t *testing.T) {
	unittest.LargeTest(t)

	ctx, wd, _, childCommits, _, _, cleanup := setupGithub(t)
	defer cleanup()
	recipesCfg := filepath.Join(testutils.GetRepoRoot(t), recipe_cfg.RECIPE_CFG_PATH)

	g, urlMock := setupFakeGithub(t, childCommits)
	cfg := githubRmCfg()
	rm, err := NewGithubRepoManager(ctx, cfg, wd, g, recipesCfg, "fake.server.com", nil, githubCR(t, g), false)
	require.NoError(t, err)
	lastRollRev, tipRev, notRolledRevs, err := rm.Update(ctx)
	require.NoError(t, err)
	ran := false
	expectedErr := errors.New("Expected error")
	rm.(*githubRepoManager).preUploadSteps = []PreUploadStep{
		func(context.Context, []string, *http.Client, string) error {
			ran = true
			return expectedErr
		},
	}

	// Create a roll, assert that we ran the PreUploadSteps.
	mockGithubRequests(t, urlMock)
	_, createErr := rm.CreateNewRoll(ctx, lastRollRev, tipRev, notRolledRevs, emails, cqExtraTrybots, false)
	require.Error(t, expectedErr, createErr)
	require.True(t, ran)
}

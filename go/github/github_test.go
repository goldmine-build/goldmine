package github

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/go-github/github"
	"github.com/gorilla/mux"
	assert "github.com/stretchr/testify/require"
	"go.skia.org/infra/go/mockhttpclient"
	"go.skia.org/infra/go/testutils"
)

func TestAddComment(t *testing.T) {
	testutils.SmallTest(t)
	reqType := "application/json"
	reqBody := []byte(`{"body":"test msg"}
`)
	r := mux.NewRouter()
	md := mockhttpclient.MockPostDialogueWithResponseCode(reqType, reqBody, nil, http.StatusCreated)
	r.Schemes("https").Host("api.github.com").Methods("POST").Path("/repos/kryptonians/krypton/issues/1234/comments").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient, "")
	assert.NoError(t, err)
	addCommentErr := githubClient.AddComment(1234, "test msg")
	assert.NoError(t, addCommentErr)
}

func TestGetAuthenticatedUser(t *testing.T) {
	testutils.SmallTest(t)
	r := mux.NewRouter()
	md := mockhttpclient.MockGetError("OK", http.StatusOK)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/user").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient, "")
	assert.NoError(t, err)
	_, getUserErr := githubClient.GetAuthenticatedUser()
	assert.NoError(t, getUserErr)
}

func TestGetPullRequest(t *testing.T) {
	testutils.SmallTest(t)
	respBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{State: &CLOSED_STATE}))
	r := mux.NewRouter()
	md := mockhttpclient.MockGetDialogue(respBody)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/repos/kryptonians/krypton/pulls/1234").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient, "")
	assert.NoError(t, err)
	pr, getPullErr := githubClient.GetPullRequest(1234)
	assert.NoError(t, getPullErr)
	assert.Equal(t, CLOSED_STATE, *pr.State)
}

func TestCreatePullRequest(t *testing.T) {
	testutils.SmallTest(t)
	reqType := "application/json"
	reqBody := []byte(`{"title":"title","head":"headBranch","base":"baseBranch","body":"testBody"}
`)
	number := 12345
	respBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{Number: &number}))
	r := mux.NewRouter()
	md := mockhttpclient.MockPostDialogueWithResponseCode(reqType, reqBody, respBody, http.StatusCreated)
	r.Schemes("https").Host("api.github.com").Methods("POST").Path("/repos/kryptonians/krypton/pulls").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient, "")
	assert.NoError(t, err)
	pullRequest, createPullErr := githubClient.CreatePullRequest("title", "baseBranch", "headBranch", "testBody")
	assert.NoError(t, createPullErr)
	assert.Equal(t, number, *pullRequest.Number)
}

func TestMergePullRequest(t *testing.T) {
	testutils.SmallTest(t)
	reqType := "application/json"
	reqBody := []byte(`{"commit_message":"test comment"}
`)
	r := mux.NewRouter()
	md := mockhttpclient.MockPutDialogue(reqType, reqBody, nil)
	r.Schemes("https").Host("api.github.com").Methods("PUT").Path("/repos/kryptonians/krypton/pulls/1234/merge").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient, "")
	assert.NoError(t, err)
	mergePullErr := githubClient.MergePullRequest(1234, "test comment")
	assert.NoError(t, mergePullErr)
}

func TestClosePullRequest(t *testing.T) {
	testutils.SmallTest(t)
	respBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{State: &CLOSED_STATE}))
	reqType := "application/json"
	reqBody := []byte(`{"state":"closed"}
`)
	r := mux.NewRouter()
	md := mockhttpclient.MockPatchDialogue(reqType, reqBody, respBody)
	r.Schemes("https").Host("api.github.com").Methods("PATCH").Path("/repos/kryptonians/krypton/pulls/1234").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient, "")
	assert.NoError(t, err)
	pr, closePullErr := githubClient.ClosePullRequest(1234)
	assert.NoError(t, closePullErr)
	assert.Equal(t, CLOSED_STATE, *pr.State)
}

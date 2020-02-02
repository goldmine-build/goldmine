package github

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/go-github/v29/github"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
	"go.skia.org/infra/go/mockhttpclient"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/testutils/unittest"
)

func TestAddComment(t *testing.T) {
	unittest.SmallTest(t)
	reqType := "application/json"
	reqBody := []byte(`{"body":"test msg"}
`)
	r := mux.NewRouter()
	md := mockhttpclient.MockPostDialogueWithResponseCode(reqType, reqBody, nil, http.StatusCreated)
	r.Schemes("https").Host("api.github.com").Methods("POST").Path("/repos/kryptonians/krypton/issues/1234/comments").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	addCommentErr := githubClient.AddComment(1234, "test msg")
	require.NoError(t, addCommentErr)
}

func TestGetAuthenticatedUser(t *testing.T) {
	unittest.SmallTest(t)
	r := mux.NewRouter()
	md := mockhttpclient.MockGetError("OK", http.StatusOK)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/user").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	_, getUserErr := githubClient.GetAuthenticatedUser()
	require.NoError(t, getUserErr)
}

func TestGetPullRequest(t *testing.T) {
	unittest.SmallTest(t)
	respBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{State: &CLOSED_STATE}))
	r := mux.NewRouter()
	md := mockhttpclient.MockGetDialogue(respBody)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/repos/kryptonians/krypton/pulls/1234").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	pr, getPullErr := githubClient.GetPullRequest(1234)
	require.NoError(t, getPullErr)
	require.Equal(t, CLOSED_STATE, *pr.State)
}

func TestCreatePullRequest(t *testing.T) {
	unittest.SmallTest(t)
	reqType := "application/json"
	reqBody := []byte(`{"title":"title","head":"headBranch","base":"baseBranch","body":"testBody"}
`)
	number := 12345
	respBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{Number: &number}))
	r := mux.NewRouter()
	md := mockhttpclient.MockPostDialogueWithResponseCode(reqType, reqBody, respBody, http.StatusCreated)
	r.Schemes("https").Host("api.github.com").Methods("POST").Path("/repos/kryptonians/krypton/pulls").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	pullRequest, createPullErr := githubClient.CreatePullRequest("title", "baseBranch", "headBranch", "testBody")
	require.NoError(t, createPullErr)
	require.Equal(t, number, *pullRequest.Number)
}

func TestMergePullRequest(t *testing.T) {
	unittest.SmallTest(t)
	reqType := "application/json"
	reqBody := []byte(`{"commit_message":"test comment","merge_method":"squash"}
`)
	r := mux.NewRouter()
	md := mockhttpclient.MockPutDialogue(reqType, reqBody, nil)
	r.Schemes("https").Host("api.github.com").Methods("PUT").Path("/repos/kryptonians/krypton/pulls/1234/merge").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	mergePullErr := githubClient.MergePullRequest(1234, "test comment", "squash")
	require.NoError(t, mergePullErr)
}

func TestClosePullRequest(t *testing.T) {
	unittest.SmallTest(t)
	respBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{State: &CLOSED_STATE}))
	reqType := "application/json"
	reqBody := []byte(`{"state":"closed"}
`)
	r := mux.NewRouter()
	md := mockhttpclient.MockPatchDialogue(reqType, reqBody, respBody)
	r.Schemes("https").Host("api.github.com").Methods("PATCH").Path("/repos/kryptonians/krypton/pulls/1234").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	pr, closePullErr := githubClient.ClosePullRequest(1234)
	require.NoError(t, closePullErr)
	require.Equal(t, CLOSED_STATE, *pr.State)
}

func TestGetLabelsRequest(t *testing.T) {
	unittest.SmallTest(t)
	label1Name := "test1"
	label2Name := "test2"
	label1 := github.Label{Name: &label1Name}
	label2 := github.Label{Name: &label2Name}
	respBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{Labels: []*github.Label{&label1, &label2}}))
	r := mux.NewRouter()
	md := mockhttpclient.MockGetDialogue(respBody)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/repos/kryptonians/krypton/issues/1234").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	labels, getLabelsErr := githubClient.GetLabels(1234)
	require.NoError(t, getLabelsErr)
	require.Equal(t, []string{label1Name, label2Name}, labels)
}

func TestAddLabelRequest(t *testing.T) {
	unittest.SmallTest(t)
	label1Name := "test1"
	label2Name := "test2"
	label1 := github.Label{Name: &label1Name}
	label2 := github.Label{Name: &label2Name}
	respBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{Labels: []*github.Label{&label1, &label2}}))
	r := mux.NewRouter()
	md := mockhttpclient.MockGetDialogue(respBody)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/repos/kryptonians/krypton/issues/1234").Handler(md)

	patchRespBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{}))
	patchReqType := "application/json"
	patchReqBody := []byte(`{"labels":["test1","test2","test3"]}
`)
	patchMd := mockhttpclient.MockPatchDialogue(patchReqType, patchReqBody, patchRespBody)
	r.Schemes("https").Host("api.github.com").Methods("PATCH").Path("/repos/kryptonians/krypton/issues/1234").Handler(patchMd)

	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	addLabelErr := githubClient.AddLabel(1234, "test3")
	require.NoError(t, addLabelErr)
}

func TestRemoveLabelRequest(t *testing.T) {
	unittest.SmallTest(t)
	label1Name := "test1"
	label2Name := "test2"
	label1 := github.Label{Name: &label1Name}
	label2 := github.Label{Name: &label2Name}
	respBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{Labels: []*github.Label{&label1, &label2}}))
	r := mux.NewRouter()
	md := mockhttpclient.MockGetDialogue(respBody)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/repos/kryptonians/krypton/issues/1234").Handler(md)

	patchRespBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{}))
	patchReqType := "application/json"
	patchReqBody := []byte(`{"labels":["test1"]}
`)
	patchMd := mockhttpclient.MockPatchDialogue(patchReqType, patchReqBody, patchRespBody)
	r.Schemes("https").Host("api.github.com").Methods("PATCH").Path("/repos/kryptonians/krypton/issues/1234").Handler(patchMd)

	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	removeLabelErr1 := githubClient.RemoveLabel(1234, "test2")
	require.NoError(t, removeLabelErr1)
}

func TestReplaceLabelRequest(t *testing.T) {
	unittest.SmallTest(t)
	label1Name := "test1"
	label2Name := "test2"
	label1 := github.Label{Name: &label1Name}
	label2 := github.Label{Name: &label2Name}
	respBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{Labels: []*github.Label{&label1, &label2}}))
	r := mux.NewRouter()
	md := mockhttpclient.MockGetDialogue(respBody)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/repos/kryptonians/krypton/issues/1234").Handler(md)

	patchRespBody := []byte(testutils.MarshalJSON(t, &github.PullRequest{}))
	patchReqType := "application/json"
	patchReqBody := []byte(`{"labels":["test2","test3"]}
`)
	patchMd := mockhttpclient.MockPatchDialogue(patchReqType, patchReqBody, patchRespBody)
	r.Schemes("https").Host("api.github.com").Methods("PATCH").Path("/repos/kryptonians/krypton/issues/1234").Handler(patchMd)

	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	removeLabelErr := githubClient.ReplaceLabel(1234, "test1", "test3")
	require.NoError(t, removeLabelErr)
}

func TestGetChecksRequest(t *testing.T) {
	unittest.SmallTest(t)

	// Mock out check-runs call.
	checkID1 := int64(100)
	checkName1 := "check1"
	check1 := github.CheckRun{ID: &checkID1, Name: &checkName1, StartedAt: &github.Timestamp{Time: time.Now()}}
	respBody := []byte(testutils.MarshalJSON(t, &github.ListCheckRunsResults{CheckRuns: []*github.CheckRun{&check1}}))
	r := mux.NewRouter()
	md := mockhttpclient.MockGetDialogue(respBody)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/repos/kryptonians/krypton/commits/abcd/check-runs").Handler(md)

	// Mock out status call.
	checkID2 := int64(200)
	checkName2 := "check2"
	pendingState := "pending"
	repoStatusCheck2 := github.RepoStatus{ID: &checkID2, Context: &checkName2, State: &pendingState}
	statusRespBody := []byte(testutils.MarshalJSON(t, &github.CombinedStatus{Statuses: []github.RepoStatus{repoStatusCheck2}}))
	mdStatus := mockhttpclient.MockGetDialogue(statusRespBody)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/repos/kryptonians/krypton/commits/abcd/status").Handler(mdStatus)

	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	checks, getChecksErr := githubClient.GetChecks("abcd")
	require.NoError(t, getChecksErr)
	require.Equal(t, 2, len(checks))
	require.Equal(t, checkID1, checks[0].ID)
	require.Equal(t, checkName1, checks[0].Name)
	require.Equal(t, checkID2, checks[1].ID)
	require.Equal(t, checkName2, checks[1].Name)
}

func TestReRequestLatestCheckSuite(t *testing.T) {
	unittest.SmallTest(t)

	// Mock out list check suites call.
	totalResults := int(1)
	checkSuiteID := int64(100)
	checkSuiteStatus := "failed"
	checkSuite := github.CheckSuite{ID: &checkSuiteID, Status: &checkSuiteStatus}
	respBody := []byte(testutils.MarshalJSON(t, &github.ListCheckSuiteResults{Total: &totalResults, CheckSuites: []*github.CheckSuite{&checkSuite}}))
	r := mux.NewRouter()
	listmd := mockhttpclient.MockGetDialogue(respBody)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/repos/kryptonians/krypton/commits/abcd/check-suites").Handler(listmd)

	// Mock out rerequest check suite call.
	rerequestmd := mockhttpclient.MockPostDialogueWithResponseCode("application/json", nil, nil, http.StatusCreated)
	r.Schemes("https").Host("api.github.com").Methods("POST").Path("/repos/kryptonians/krypton/check-suites/100/rerequest").Handler(rerequestmd)

	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	rerequestErr := githubClient.ReRequestLatestCheckSuite("abcd")
	require.NoError(t, rerequestErr)
}

func TestGetDescription(t *testing.T) {
	unittest.SmallTest(t)
	body := "test test test"
	respBody := []byte(testutils.MarshalJSON(t, &github.Issue{Body: &body}))
	r := mux.NewRouter()
	md := mockhttpclient.MockGetDialogue(respBody)
	r.Schemes("https").Host("api.github.com").Methods("GET").Path("/repos/kryptonians/krypton/issues/12345").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	desc, err := githubClient.GetDescription(12345)
	require.NoError(t, err)
	require.Equal(t, body, desc)
}

func TestReadRawFileRequest(t *testing.T) {
	unittest.SmallTest(t)
	respBody := []byte(`abcd`)
	r := mux.NewRouter()
	md := mockhttpclient.MockGetDialogue(respBody)
	r.Schemes("https").Host("raw.githubusercontent.com").Methods("GET").Path("/kryptonians/krypton/master/dummy/path/to/this.txt").Handler(md)
	httpClient := mockhttpclient.NewMuxClient(r)

	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	contents, readRawErr := githubClient.ReadRawFile("master", "/dummy/path/to/this.txt")
	require.NoError(t, readRawErr)
	require.Equal(t, "abcd", contents)
}

func TestGetFullHistoryUrl(t *testing.T) {
	unittest.SmallTest(t)
	httpClient := mockhttpclient.NewMuxClient(mux.NewRouter())
	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	fullHistoryUrl := githubClient.GetFullHistoryUrl("superman@krypton.com")
	require.Equal(t, "https://github.com/kryptonians/krypton/pulls/superman", fullHistoryUrl)
}

func TestGetIssueUrlBase(t *testing.T) {
	unittest.SmallTest(t)
	httpClient := mockhttpclient.NewMuxClient(mux.NewRouter())
	githubClient, err := NewGitHub(context.Background(), "kryptonians", "krypton", httpClient)
	require.NoError(t, err)
	issueUrlBase := githubClient.GetIssueUrlBase()
	require.Equal(t, "https://github.com/kryptonians/krypton/pull/", issueUrlBase)
}

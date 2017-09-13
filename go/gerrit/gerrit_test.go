package gerrit

import (
	"fmt"
	"testing"
	"time"

	assert "github.com/stretchr/testify/require"
	"go.skia.org/infra/go/testutils"
)

const (
	// Flip this boolean to run the below E2E Gerrit tests. Requires a valid
	// ~/.gitcookies file.
	RUN_GERRIT_TESTS = false
)

func skipTestIfRequired(t *testing.T) {
	if !RUN_GERRIT_TESTS {
		t.Skip("Skipping test due to RUN_GERRIT_TESTS=false")
	}
	testutils.LargeTest(t)
}

func TestGerritOwnerModifiedSearch(t *testing.T) {
	skipTestIfRequired(t)

	api, err := NewGerrit(GERRIT_SKIA_URL, DefaultGitCookiesPath(), nil)
	assert.NoError(t, err)

	t_delta := time.Now().Add(-10 * 24 * time.Hour)
	issues, err := api.Search(1, SearchModifiedAfter(t_delta), SearchOwner("rmistry@google.com"))
	assert.NoError(t, err)
	assert.True(t, len(issues) > 0)

	for _, issue := range issues {
		details, err := api.GetIssueProperties(issue.Issue)
		assert.NoError(t, err)
		assert.True(t, details.Updated.After(t_delta))
		assert.True(t, len(details.Patchsets) > 0)
		assert.Equal(t, "rmistry@google.com", details.Owner.Email)
	}

	issues, err = api.Search(2, SearchModifiedAfter(time.Now().Add(-time.Hour)))
	assert.NoError(t, err)
}

func TestGerritCommitSearch(t *testing.T) {
	skipTestIfRequired(t)

	api, err := NewGerrit(GERRIT_SKIA_URL, DefaultGitCookiesPath(), nil)
	assert.NoError(t, err)
	api.TurnOnAuthenticatedGets()

	issues, err := api.Search(1, SearchCommit("a2eb235a16ed430896cc54989e683cf930319eb7"))
	assert.NoError(t, err)
	assert.Equal(t, 1, len(issues))

	for _, issue := range issues {
		details, err := api.GetIssueProperties(issue.Issue)
		assert.NoError(t, err)
		assert.Equal(t, 5, len(details.Patchsets))
		assert.Equal(t, "rmistry@google.com", details.Owner.Email)
		assert.Equal(t, "I37876c6f62c85d0532b22dcf8bea8b4e7f4147c0", details.ChangeId)
		assert.True(t, details.Committed)
		assert.Equal(t, "Skia Gerrit 10k!", details.Subject)
	}
}

// It is a little different to test the poller. Enable this to turn on the
// poller and test removing issues from it.
func GerritPollerTest(t *testing.T) {
	skipTestIfRequired(t)

	api, err := NewGerrit(GERRIT_SKIA_URL, DefaultGitCookiesPath(), nil)
	assert.NoError(t, err)

	cache := NewCodeReviewCache(api, 10*time.Second, 3)
	fmt.Println(cache.Get(2386))
	c1 := ChangeInfo{
		Issue: 2386,
	}
	cache.Add(2386, &c1)
	fmt.Println(cache.Get(2386))
	time.Sleep(time.Hour)
}

func TestGetPatch(t *testing.T) {
	skipTestIfRequired(t)

	api, err := NewGerrit(GERRIT_SKIA_URL, DefaultGitCookiesPath(), nil)
	assert.NoError(t, err)

	patch, err := api.GetPatch(2370, "current")
	assert.NoError(t, err)
	expected := `

diff --git a/whitespace.txt b/whitespace.txt
index c0f0a49..d5733b3 100644
--- a/whitespace.txt
+++ b/whitespace.txt
@@ -1,4 +1,5 @@
 testing
+
  
 
 
`
	assert.Equal(t, expected, patch)
}

func TestAddComment(t *testing.T) {
	skipTestIfRequired(t)

	api, err := NewGerrit(GERRIT_SKIA_URL, DefaultGitCookiesPath(), nil)
	assert.NoError(t, err)

	changeInfo, err := api.GetIssueProperties(2370)
	assert.NoError(t, err)
	err = api.AddComment(changeInfo, "Testing API!!")
	assert.NoError(t, err)
}

func TestSendToDryRun(t *testing.T) {
	skipTestIfRequired(t)

	api, err := NewGerrit(GERRIT_SKIA_URL, DefaultGitCookiesPath(), nil)
	assert.NoError(t, err)

	// Send to dry run.
	changeInfo, err := api.GetIssueProperties(2370)
	assert.NoError(t, err)
	defaultLabelValue := changeInfo.Labels[COMMITQUEUE_LABEL].DefaultValue
	err = api.SendToDryRun(changeInfo, "Sending to dry run")
	assert.NoError(t, err)

	// Wait for a second for the above to take place.
	time.Sleep(time.Second)

	// Verify that the change was sent to dry run.
	changeInfo, err = api.GetIssueProperties(2370)
	assert.NoError(t, err)
	assert.Equal(t, 1, changeInfo.Labels[COMMITQUEUE_LABEL].All[0].Value)

	// Remove from dry run.
	err = api.RemoveFromCQ(changeInfo, "")
	assert.NoError(t, err)

	// Verify that the change was removed from dry run.
	changeInfo, err = api.GetIssueProperties(2370)
	assert.NoError(t, err)
	assert.Equal(t, defaultLabelValue, changeInfo.Labels[COMMITQUEUE_LABEL].All[0].Value)
}

func TestSendToCQ(t *testing.T) {
	skipTestIfRequired(t)

	api, err := NewGerrit(GERRIT_SKIA_URL, DefaultGitCookiesPath(), nil)
	assert.NoError(t, err)

	// Send to CQ.
	changeInfo, err := api.GetIssueProperties(2370)
	assert.NoError(t, err)
	defaultLabelValue := changeInfo.Labels[COMMITQUEUE_LABEL].DefaultValue
	err = api.SendToCQ(changeInfo, "Sending to CQ")
	assert.NoError(t, err)

	// Wait for a few seconds for the above to take place.
	time.Sleep(5 * time.Second)

	// Verify that the change was sent to CQ.
	changeInfo, err = api.GetIssueProperties(2370)
	assert.NoError(t, err)
	assert.Equal(t, 2, changeInfo.Labels[COMMITQUEUE_LABEL].All[0].Value)

	// Remove from CQ.
	err = api.RemoveFromCQ(changeInfo, "")
	assert.NoError(t, err)

	// Verify that the change was removed from CQ.
	changeInfo, err = api.GetIssueProperties(2370)
	assert.NoError(t, err)
	assert.Equal(t, defaultLabelValue, changeInfo.Labels[COMMITQUEUE_LABEL].All[0].Value)
}

func TestApprove(t *testing.T) {
	skipTestIfRequired(t)

	api, err := NewGerrit(GERRIT_SKIA_URL, DefaultGitCookiesPath(), nil)
	assert.NoError(t, err)

	// Approve.
	changeInfo, err := api.GetIssueProperties(2370)
	assert.NoError(t, err)
	defaultLabelValue := changeInfo.Labels[CODEREVIEW_LABEL].DefaultValue
	err = api.Approve(changeInfo, "LGTM")
	assert.NoError(t, err)

	// Wait for a second for the above to take place.
	time.Sleep(time.Second)

	// Verify that the change was approved.
	changeInfo, err = api.GetIssueProperties(2370)
	assert.NoError(t, err)
	assert.Equal(t, 1, changeInfo.Labels[CODEREVIEW_LABEL].All[0].Value)

	// Remove approval.
	err = api.NoScore(changeInfo, "")
	assert.NoError(t, err)

	// Verify that the change has no score.
	changeInfo, err = api.GetIssueProperties(2370)
	assert.NoError(t, err)
	assert.Equal(t, defaultLabelValue, changeInfo.Labels[CODEREVIEW_LABEL].All[0].Value)
}

func TestReadOnlyFailure(t *testing.T) {
	skipTestIfRequired(t)

	api, err := NewGerrit(GERRIT_SKIA_URL, "", nil)
	assert.NoError(t, err)

	// Approve.
	changeInfo, err := api.GetIssueProperties(2370)
	assert.NoError(t, err)
	err = api.Approve(changeInfo, "LGTM")
	assert.Error(t, err)
}

func TestDisApprove(t *testing.T) {
	skipTestIfRequired(t)

	api, err := NewGerrit(GERRIT_SKIA_URL, DefaultGitCookiesPath(), nil)
	assert.NoError(t, err)

	// DisApprove.
	changeInfo, err := api.GetIssueProperties(2370)
	assert.NoError(t, err)
	defaultLabelValue := changeInfo.Labels[CODEREVIEW_LABEL].DefaultValue
	err = api.DisApprove(changeInfo, "not LGTM")
	assert.NoError(t, err)

	// Wait for a second for the above to take place.
	time.Sleep(time.Second)

	// Verify that the change was disapproved.
	changeInfo, err = api.GetIssueProperties(2370)
	assert.NoError(t, err)
	assert.Equal(t, -1, changeInfo.Labels[CODEREVIEW_LABEL].All[0].Value)

	// Remove disapproval.
	err = api.NoScore(changeInfo, "")
	assert.NoError(t, err)

	// Verify that the change has no score.
	changeInfo, err = api.GetIssueProperties(2370)
	assert.NoError(t, err)
	assert.Equal(t, defaultLabelValue, changeInfo.Labels[CODEREVIEW_LABEL].All[0].Value)
}

func TestAbandon(t *testing.T) {
	skipTestIfRequired(t)

	api, err := NewGerrit(GERRIT_SKIA_URL, "", nil)
	assert.NoError(t, err)
	c1 := ChangeInfo{
		ChangeId: "Idb96a747c8446126f60fdf1adca361dbc2e539d5",
	}
	err = api.Abandon(&c1, "Abandoning this CL")
	assert.Error(t, err, "Got status 409 Conflict (409)")
}

func TestUrlAndExtractIssue(t *testing.T) {
	testutils.SmallTest(t)
	api, err := NewGerrit(GERRIT_SKIA_URL, "", nil)
	assert.NoError(t, err)
	assert.Equal(t, GERRIT_SKIA_URL, api.Url(0))
	url1 := api.Url(1234)
	assert.Equal(t, fmt.Sprintf("%s/c/%d", GERRIT_SKIA_URL, 1234), url1)
	found, ok := api.ExtractIssue(url1)
	assert.True(t, ok)
	assert.Equal(t, "1234", found)
	found, ok = api.ExtractIssue(fmt.Sprintf("%s/%d", GERRIT_SKIA_URL, 1234))
	assert.Equal(t, "", found)
	assert.False(t, ok)
	found, ok = api.ExtractIssue("random string")
	assert.Equal(t, "", found)
	assert.False(t, ok)
}

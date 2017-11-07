package google3

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	assert "github.com/stretchr/testify/require"
	"go.skia.org/infra/go/autoroll"
	git_testutils "go.skia.org/infra/go/git/testutils"
	"go.skia.org/infra/go/jsonutils"
	"go.skia.org/infra/go/testutils"
)

func setup(t *testing.T) (*AutoRoller, []string, func()) {
	testutils.LargeTest(t)
	gb := git_testutils.GitInit(t)
	commits := []string{gb.CommitGen("a.txt"), gb.CommitGen("a.txt"), gb.CommitGen("a.txt")}
	tmpDir, cleanup := testutils.TempDir(t)
	a, err := NewAutoRoller(tmpDir, gb.RepoUrl(), "master")
	assert.NoError(t, err)
	a.Start(time.Second, time.Second)
	return a, commits, func() {
		cleanup()
		gb.Cleanup()
	}
}

func makeIssue(num int64, commit string) *autoroll.AutoRollIssue {
	now := time.Now().UTC()
	return &autoroll.AutoRollIssue{
		Closed:      false,
		Committed:   false,
		CommitQueue: true,
		Created:     now,
		Issue:       num,
		Modified:    now,
		Patchsets:   nil,
		Result:      autoroll.ROLL_RESULT_IN_PROGRESS,
		RollingFrom: "prevrev",
		RollingTo:   commit,
		Subject:     fmt.Sprintf("%d", num),
		TryResults: []*autoroll.TryResult{
			&autoroll.TryResult{
				Builder:  "Test Summary",
				Category: autoroll.TRYBOT_CATEGORY_CQ,
				Created:  now,
				Result:   "",
				Status:   autoroll.TRYBOT_STATUS_STARTED,
				Url:      "http://example.com/",
			},
		},
	}
}

func closeIssue(issue *autoroll.AutoRollIssue, result string) {
	issue.Closed = true
	issue.CommitQueue = false
	issue.Modified = time.Now().UTC()
	issue.Result = result
	issue.TryResults[0].Status = autoroll.TRYBOT_STATUS_COMPLETED
	issue.TryResults[0].Result = autoroll.TRYBOT_RESULT_FAILURE
	if result == autoroll.ROLL_RESULT_SUCCESS {
		issue.Committed = true
		issue.TryResults[0].Result = autoroll.TRYBOT_RESULT_SUCCESS
	}
}

func TestStatus(t *testing.T) {
	a, commits, cleanup := setup(t)
	defer cleanup()

	issue1 := makeIssue(1, commits[0])
	assert.NoError(t, a.AddOrUpdateIssue(issue1, http.MethodPost))
	closeIssue(issue1, autoroll.ROLL_RESULT_SUCCESS)
	assert.NoError(t, a.AddOrUpdateIssue(issue1, http.MethodPut))

	assert.NoError(t, a.UpdateStatus("", true))
	status := a.GetStatus(true)
	assert.Equal(t, 0, status.NumFailedRolls)
	assert.Equal(t, 2, status.NumNotRolledCommits)
	assert.Equal(t, issue1.RollingTo, status.LastRollRev)
	assert.Nil(t, status.CurrentRoll)
	testutils.AssertDeepEqual(t, issue1, status.LastRoll)
	testutils.AssertDeepEqual(t, []*autoroll.AutoRollIssue{issue1}, status.Recent)

	issue2 := makeIssue(2, commits[2])
	assert.NoError(t, a.AddOrUpdateIssue(issue2, http.MethodPost))
	closeIssue(issue2, autoroll.ROLL_RESULT_FAILURE)
	assert.NoError(t, a.AddOrUpdateIssue(issue2, http.MethodPut))

	issue3 := makeIssue(3, commits[2])
	assert.NoError(t, a.AddOrUpdateIssue(issue3, http.MethodPost))
	closeIssue(issue3, autoroll.ROLL_RESULT_FAILURE)
	assert.NoError(t, a.AddOrUpdateIssue(issue3, http.MethodPut))

	issue4 := makeIssue(4, commits[2])
	assert.NoError(t, a.AddOrUpdateIssue(issue4, http.MethodPost))

	recent := []*autoroll.AutoRollIssue{issue4, issue3, issue2, issue1}
	assert.NoError(t, a.UpdateStatus("error message", false))
	status = a.GetStatus(true)
	assert.Equal(t, 2, status.NumFailedRolls)
	assert.Equal(t, 2, status.NumNotRolledCommits)
	assert.Equal(t, issue1.RollingTo, status.LastRollRev)
	assert.Equal(t, "error message", status.Error)
	testutils.AssertDeepEqual(t, issue4, status.CurrentRoll)
	testutils.AssertDeepEqual(t, issue3, status.LastRoll)
	testutils.AssertDeepEqual(t, recent, status.Recent)

	// Test preserving error.
	assert.NoError(t, a.UpdateStatus("", true))
	status = a.GetStatus(true)
	assert.Equal(t, "error message", status.Error)

	// Test that sensitive data is cleared.
	for _, i := range recent {
		i.Issue = 0
		i.Subject = ""
		i.TryResults = nil
	}
	status = a.GetStatus(false)
	assert.Equal(t, 2, status.NumFailedRolls)
	assert.Equal(t, 2, status.NumNotRolledCommits)
	assert.Equal(t, issue1.RollingTo, status.LastRollRev)
	assert.Equal(t, "", status.Error)
	testutils.AssertDeepEqual(t, issue4, status.CurrentRoll)
	testutils.AssertDeepEqual(t, issue3, status.LastRoll)
	testutils.AssertDeepEqual(t, recent, status.Recent)
}

func TestAddOrUpdateIssue(t *testing.T) {
	a, commits, cleanup := setup(t)
	defer cleanup()

	issue1 := makeIssue(1, commits[0])
	assert.NoError(t, a.AddOrUpdateIssue(issue1, http.MethodPost))
	closeIssue(issue1, autoroll.ROLL_RESULT_SUCCESS)
	assert.NoError(t, a.AddOrUpdateIssue(issue1, http.MethodPut))

	// Test adding an issue that is already closed.
	issue2 := makeIssue(2, commits[1])
	closeIssue(issue2, autoroll.ROLL_RESULT_SUCCESS)
	assert.NoError(t, a.AddOrUpdateIssue(issue2, http.MethodPut))
	assert.NoError(t, a.UpdateStatus("", true))
	testutils.AssertDeepEqual(t, []*autoroll.AutoRollIssue{issue2, issue1}, a.GetStatus(true).Recent)

	// Test adding a two issues without closing the first one.
	issue3 := makeIssue(3, commits[2])
	assert.NoError(t, a.AddOrUpdateIssue(issue3, http.MethodPost))
	issue4 := makeIssue(4, commits[2])
	assert.NoError(t, a.AddOrUpdateIssue(issue4, http.MethodPost))
	assert.NoError(t, a.UpdateStatus("", true))
	issue3.Closed = true
	issue3.Result = autoroll.ROLL_RESULT_FAILURE
	testutils.AssertDeepEqual(t, []*autoroll.AutoRollIssue{issue4, issue3, issue2, issue1}, a.GetStatus(true).Recent)

	// Test both situations at the same time.
	issue5 := makeIssue(5, commits[2])
	closeIssue(issue5, autoroll.ROLL_RESULT_SUCCESS)
	assert.NoError(t, a.AddOrUpdateIssue(issue5, http.MethodPut))
	assert.NoError(t, a.UpdateStatus("", true))
	issue4.Closed = true
	issue4.Result = autoroll.ROLL_RESULT_FAILURE
	testutils.AssertDeepEqual(t, []*autoroll.AutoRollIssue{issue5, issue4, issue3, issue2, issue1}, a.GetStatus(true).Recent)
}

func makeRoll(now time.Time) Roll {
	return Roll{
		ChangeListNumber: 1,
		Closed:           false,
		Created:          jsonutils.Time(now),
		Modified:         jsonutils.Time(now),
		Result:           autoroll.ROLL_RESULT_IN_PROGRESS,
		RollingTo:        "rev",
		RollingFrom:      "prevrev",
		Subject:          "1",
		Submitted:        false,
		TestSummaryUrl:   "http://example.com/",
	}
}

func TestRollAsIssue(t *testing.T) {
	testutils.SmallTest(t)

	expected := makeIssue(1, "rev")
	now := expected.Created
	roll := makeRoll(now)

	actual, err := roll.AsIssue()
	assert.NoError(t, err)
	testutils.AssertDeepEqual(t, expected, actual)

	roll.TestSummaryUrl = ""
	savedTryResults := expected.TryResults
	expected.TryResults = []*autoroll.TryResult{}
	actual, err = roll.AsIssue()
	assert.NoError(t, err)
	testutils.AssertDeepEqual(t, expected, actual)

	roll.Closed = true
	expected.Closed = true
	expected.CommitQueue = false
	roll.Result = autoroll.ROLL_RESULT_FAILURE
	expected.Result = autoroll.ROLL_RESULT_FAILURE
	roll.TestSummaryUrl = "http://example.com/"
	expected.TryResults = savedTryResults
	expected.TryResults[0].Result = autoroll.TRYBOT_RESULT_FAILURE
	expected.TryResults[0].Status = autoroll.TRYBOT_STATUS_COMPLETED
	actual, err = roll.AsIssue()
	assert.NoError(t, err)
	testutils.AssertDeepEqual(t, expected, actual)

	roll.Submitted = true
	roll.Result = autoroll.ROLL_RESULT_SUCCESS
	expected.Committed = true
	expected.Result = autoroll.ROLL_RESULT_SUCCESS
	expected.TryResults[0].Result = autoroll.TRYBOT_RESULT_SUCCESS
	actual, err = roll.AsIssue()
	assert.NoError(t, err)
	testutils.AssertDeepEqual(t, expected, actual)

	roll = makeRoll(now)
	roll.Created = jsonutils.Time{}
	_, err = roll.AsIssue()
	assert.EqualError(t, err, "Missing parameter.")

	roll = makeRoll(now)
	roll.RollingFrom = ""
	_, err = roll.AsIssue()
	assert.EqualError(t, err, "Missing parameter.")

	roll = makeRoll(now)
	roll.RollingTo = ""
	_, err = roll.AsIssue()
	assert.EqualError(t, err, "Missing parameter.")

	roll = makeRoll(now)
	roll.Closed = true
	_, err = roll.AsIssue()
	assert.EqualError(t, err, "Inconsistent parameters: result must be set.")

	roll = makeRoll(now)
	roll.Submitted = true
	_, err = roll.AsIssue()
	assert.EqualError(t, err, "Inconsistent parameters: submitted but not closed.")

	roll = makeRoll(now)
	roll.Result = ""
	_, err = roll.AsIssue()
	assert.EqualError(t, err, "Unsupported value for result.")

	roll = makeRoll(now)
	roll.TestSummaryUrl = ":http//example.com"
	_, err = roll.AsIssue()
	assert.EqualError(t, err, "Invalid testSummaryUrl parameter.")
}

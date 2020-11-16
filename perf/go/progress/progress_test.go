// Package progress is for tracking the progress of long running tasks on the
// backend in a way that can be reflected in the UI.
//
// We have multiple long running queries like /frame/start and /dryrun/start
// that start those long running processes and we need to give feedback to the
// user on how they are proceeding.
//
// The dryrun progress information contains different info with different stages
// and steps.
//
//   Step: 1/1
//   Query: "sub_result=max_rss_mb"
//   Looking for regressions in query results.
//     Commit: 51643
//     Details: "Filtered Traces: Num Before: 95 Num After: 92 Delta: 3"
//
// The Progress type is for tracking a single long running process, and Tracker
// keeps track of multiple Progresses.
package progress

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.skia.org/infra/go/testutils/unittest"
)

func TestProgress_AddMessage_MessageAppearsInJSON(t *testing.T) {
	unittest.SmallTest(t)
	p := New()
	p.Message("foo", "bar")
	var buf bytes.Buffer
	require.NoError(t, p.JSON(&buf))
	assert.Equal(t, "{\"status\":\"Running\",\"messages\":[{\"key\":\"foo\",\"value\":\"bar\"}],\"url\":\"\"}\n", buf.String())
}

func TestProgress_AddTwoMessages_MessagesAppearInJSONInTheOrderTheyWereAdded(t *testing.T) {
	unittest.SmallTest(t)
	p := New()
	p.Message("foo", "bar")
	p.Message("fizz", "buzz")
	var buf bytes.Buffer
	require.NoError(t, p.JSON(&buf))
	assert.Equal(t, "{\"status\":\"Running\",\"messages\":[{\"key\":\"foo\",\"value\":\"bar\"},{\"key\":\"fizz\",\"value\":\"buzz\"}],\"url\":\"\"}\n", buf.String())
}

func TestProgress_UpdateAnExistingMessage_MessageIsUpdatedInJSON(t *testing.T) {
	unittest.SmallTest(t)
	p := New()
	p.Message("foo", "bar")
	p.Message("foo", "buzz")
	assert.Equal(t, SerializedProgress{
		Status: Running,
		Messsages: []*Message{
			{
				Key:   "foo",
				Value: "buzz",
			},
		},
	}, p.state)
}

type testResults struct {
	SomeResult string `json:"some_result"`
}

func TestProgress_FinishProcess_StatusChangesToFinished(t *testing.T) {
	unittest.SmallTest(t)
	p := New()
	p.Finished(testResults{SomeResult: "foo"})
	assert.Equal(t, SerializedProgress{
		Status:    Finished,
		Messsages: []*Message{},
		Results: testResults{
			SomeResult: "foo",
		},
	}, p.state)
	assert.Equal(t, Finished, p.Status())
}

func TestProgress_CallError_StatusChangesToError(t *testing.T) {
	unittest.SmallTest(t)
	p := New()
	p.Error()
	assert.Equal(t, SerializedProgress{
		Status:    Error,
		Messsages: []*Message{},
	}, p.state)
	assert.Equal(t, Error, p.Status())
}

func TestProgress_SetIntermediateResult_ResultAppearsButStatusStaysRunning(t *testing.T) {
	unittest.SmallTest(t)
	p := New()
	p.IntermediateResult(testResults{SomeResult: "foo"})
	assert.Equal(t, SerializedProgress{
		Status:    Running,
		Messsages: []*Message{},
		Results: testResults{
			SomeResult: "foo",
		},
	}, p.state)
	assert.Equal(t, Running, p.Status())
}

func TestProgress_CallURL_URLIsSet(t *testing.T) {
	unittest.SmallTest(t)
	p := New()
	const url = "/_/next"
	p.URL(url)
	assert.Equal(t, SerializedProgress{
		Status:    Running,
		Messsages: []*Message{},
		URL:       url,
	}, p.state)
	assert.Equal(t, Running, p.Status())
}

func TestProgress_RequestForJSONWithUnSerializableResult_ReturnsError(t *testing.T) {
	unittest.SmallTest(t)

	tr, err := NewTracker("/foo/")
	require.NoError(t, err)
	p := New()
	p.Finished(make(chan int)) // Not JSON serializable.
	tr.Add(p)

	var buf bytes.Buffer
	require.Error(t, p.JSON(&buf))
}

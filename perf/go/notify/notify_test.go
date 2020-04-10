package notify

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.skia.org/infra/go/testutils/unittest"
	"go.skia.org/infra/perf/go/alerts"
)

type emailMock struct {
	from               string
	to                 []string
	subject            string
	body               string
	threadingReference string
}

func (e *emailMock) Send(from string, to []string, subject string, body string, threadingReference string) (string, error) {
	e.from = from
	e.to = to
	e.subject = subject
	e.body = body
	e.threadingReference = threadingReference
	return "", nil
}

func TestExampleSend(t *testing.T) {
	unittest.SmallTest(t)

	e := &emailMock{}
	n := New(e, "https://perf.skia.org")
	alert := &alerts.Alert{
		Alert:       "someone@example.org, someother@example.com ",
		DisplayName: "MyAlert",
	}
	err := n.ExampleSend(alert)
	assert.NoError(t, err)
	assert.Equal(t, []string{"someone@example.org", "someother@example.com"}, e.to)
	assert.Equal(t, fromAddress, e.from)
	assert.Equal(t, "MyAlert - Regression found for \"Re-enable opList dependency tracking\"", e.subject)
	assert.Equal(t, "<b>Alert</b><br><br>\n<p>\n\tA Perf Regression has been found at:\n</p>\n<p style=\"padding: 1em;\">\n\t<a href=\"https://perf.skia.org/g/t/d261e1075a93677442fdf7fe72aba7e583863664\">https://perf.skia.org/g/t/d261e1075a93677442fdf7fe72aba7e583863664</a>\n</p>\n<p>\n  For:\n</p>\n<p style=\"padding: 1em;\">\n  <a href=\"https://skia.googlesource.com/skia/&#43;/d261e1075a93677442fdf7fe72aba7e583863664\">https://skia.googlesource.com/skia/&#43;/d261e1075a93677442fdf7fe72aba7e583863664</a>\n</p>\n<p>\n\tWith 10 matching traces.\n</p>", e.body)
}

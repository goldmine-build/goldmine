package cid

import (
	"io/ioutil"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.skia.org/infra/go/git/gitinfo"
	"go.skia.org/infra/go/ingestion"
	"go.skia.org/infra/go/mockhttpclient"
	"go.skia.org/infra/go/rietveld"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/go/vcsinfo"

	"github.com/stretchr/testify/assert"
)

var (
	// TEST_COMMITS are the commits we are considering. It needs to contain at
	// least all the commits referenced in the test file.
	TEST_COMMITS = []*vcsinfo.LongCommit{
		{
			ShortCommit: &vcsinfo.ShortCommit{
				Hash:    "fe4a4029a080bc955e9588d05a6cd9eb490845d4",
				Subject: "Really big code change",
			},
			Timestamp: time.Now().Add(-time.Second * 10).Round(time.Second),
			Branches:  map[string]bool{"master": true},
		},
	}
)

func TestCommitID(t *testing.T) {
	testutils.SmallTest(t)
	c := &CommitID{
		Offset: 51,
		Source: "master",
	}
	assert.Equal(t, "master-000001.bdb", c.Filename())
	assert.Equal(t, "master-000051", c.ID())

	c = &CommitID{
		Offset: 0,
		Source: "https://codereview.chromium.org/2251213006",
	}
	assert.Equal(t, "https___codereview_chromium_org_2251213006-000000.bdb", c.Filename())
	assert.Equal(t, "https___codereview_chromium_org_2251213006-000000", c.ID())
}

func TestFromIssue(t *testing.T) {
	testutils.SmallTest(t)
	b, err := ioutil.ReadFile(filepath.Join("testdata", "rietveld_response.txt"))
	assert.NoError(t, err)
	m := mockhttpclient.NewURLMock()
	m.Mock("https://codereview.chromium.org/api/1467533002", mockhttpclient.MockGetDialogue(b))
	m.Mock("https://chromium-cq-status.appspot.com/v2/patch-summary/codereview.chromium.org/2320153002/840001", mockhttpclient.MockGetDialogue([]byte("{}")))

	review := rietveld.New("https://codereview.chromium.org", m.Client())
	commitID, err := FromIssue(review, "1467533002", "40001")
	assert.NoError(t, err)

	expected := &CommitID{
		Source: "https://codereview.chromium.org/1467533002",
		Offset: 2,
	}
	assert.Equal(t, expected, commitID)

	commitID, err = FromIssue(review, "999999999", "40001")
	assert.Error(t, err)
	assert.Nil(t, commitID)
}

func TestFromHash(t *testing.T) {
	testutils.SmallTest(t)
	vcs := ingestion.MockVCS(TEST_COMMITS, nil)
	commitID, err := FromHash(vcs, "fe4a4029a080bc955e9588d05a6cd9eb490845d4")
	assert.NoError(t, err)

	expected := &CommitID{
		Source: "master",
		Offset: 0,
	}
	assert.Equal(t, expected, commitID)

	commitID, err = FromHash(vcs, "not-a-valid-hash")
	assert.Error(t, err)
	assert.Nil(t, commitID)
}

func TestLookup(t *testing.T) {
	testutils.SmallTest(t)
	b, err := ioutil.ReadFile(filepath.Join("testdata", "rietveld_response.txt"))
	assert.NoError(t, err)
	m := mockhttpclient.NewURLMock()
	m.Mock("https://codereview.chromium.org/api/1467533002", mockhttpclient.MockGetDialogue(b))

	b, err = ioutil.ReadFile(filepath.Join("testdata", "rietveld_patchset_response.txt"))
	assert.NoError(t, err)
	m.Mock("https://codereview.chromium.org/api/1467533002/40001", mockhttpclient.MockGetDialogue(b))
	m.Mock("https://chromium-cq-status.appspot.com/v2/patch-summary/codereview.chromium.org/2320153002/840001", mockhttpclient.MockGetDialogue([]byte("{}")))

	review := rietveld.New("https://codereview.chromium.org", m.Client())

	tr := util.NewTempRepo()
	defer tr.Cleanup()

	git, err := gitinfo.NewGitInfo(filepath.Join(tr.Dir, "testrepo"), false, false)
	if err != nil {
		t.Fatal(err)
	}
	lookup := New(git, review, "https://skia.googlesource.com/skia")
	assert.NotNil(t, lookup)

	cids := []*CommitID{
		{
			Source: "master",
			Offset: 1,
		},
		{
			Source: "https://codereview.chromium.org/1467533002",
			Offset: 2,
		},
	}

	details, err := lookup.Lookup(cids)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(details))

	expectedDetails := []*CommitDetail{
		{
			CommitID: CommitID{
				Offset: 1,
				Source: "master",
			},
			Author:    "Joe Gregorio (jcgregorio@google.com)",
			Message:   "ab8d7b6 -  2y 15w - Test Commit 1",
			URL:       "https://skia.googlesource.com/skia/+/ab8d7b6872097732a27c459bb226683cdb4695bd",
			Hash:      "ab8d7b6872097732a27c459bb226683cdb4695bd",
			Timestamp: 1407642093,
		},
		{
			CommitID: CommitID{
				Offset: 2,
				Source: "https://codereview.chromium.org/1467533002",
			},
			Author:    "mtklein_C",
			Message:   "Iss: 1467533002 Patch: 40001 - GN: Android perf/tests Committed: foo.  ",
			URL:       "https://codereview.chromium.org/1467533002",
			Hash:      "",
			Timestamp: 1448988012,
		},
	}
	// Message will change based on the passage of time. Test what we can, then
	// copy the actual Message into the expected Message.
	assert.True(t, strings.HasPrefix(details[0].Message, "ab8d7b6 -"))
	assert.True(t, strings.HasSuffix(details[0].Message, " - Test Commit 1"))
	expectedDetails[0].Message = details[0].Message

	assert.Equal(t, expectedDetails, details)

	cids[0].Offset = -1
	details, err = lookup.Lookup(cids)
	assert.Error(t, err)
}

func TestParseLogLine(t *testing.T) {
	testutils.SmallTest(t)
	s := "1476870603 e8f0a7b986f1e5583c9bc162efcdd92fd6430549 joel.liang@arm.com Generate Signed Distance Field directly from vector path"
	var index int = 3
	entry, err := parseLogLine(s, &index, nil)
	assert.NoError(t, err)
	expected := &cacheEntry{
		author:  "joel.liang@arm.com",
		subject: "Generate Signed Distance Field directly from vector path",
		hash:    "e8f0a7b986f1e5583c9bc162efcdd92fd6430549",
		ts:      1476870603,
	}
	assert.Equal(t, expected, entry)
	assert.Equal(t, 4, index)

	// No subject.
	s = "1476870603 e8f0a7b986f1e5583c9bc162efcdd92fd6430549 joel.liang@arm.com"
	entry, err = parseLogLine(s, &index, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Failed to parse parts")
	assert.Equal(t, 4, index)

	// Invalid timestamp.
	s = "1476870ZZZ e8f0a7b986f1e5583c9bc162efcdd92fd6430549 joel.liang@arm.com Generate Signed Distance Field directly from vector path"
	entry, err = parseLogLine(s, &index, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "Can't parse timestamp")
	assert.Equal(t, 4, index)
}

func TestFromID(t *testing.T) {
	testutils.SmallTest(t)
	testCases := []struct {
		value    string
		expected *CommitID
		err      bool
		message  string
	}{
		{
			value: "master-000051",
			expected: &CommitID{
				Offset: 51,
				Source: "master",
			},
			err:     false,
			message: "Simple",
		},
		{
			value:    "some_trybot-000051",
			expected: nil,
			err:      true,
			message:  "TryBot should fail",
		},
		{
			value:    "master-notanint",
			expected: nil,
			err:      true,
			message:  "Fail parse int",
		},
		{
			value:    "invalid",
			expected: nil,
			err:      true,
			message:  "no dashes",
		},
		{
			value:    "in-val-id",
			expected: nil,
			err:      true,
			message:  "too many dashes",
		},
	}

	for _, tc := range testCases {
		got, err := FromID(tc.value)
		assert.Equal(t, tc.err, err != nil, tc.message)
		assert.Equal(t, tc.expected, got, tc.message)
	}

}

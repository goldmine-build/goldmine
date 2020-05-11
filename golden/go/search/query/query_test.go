package query

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"go.skia.org/infra/go/paramtools"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/testutils/unittest"
)

// TestParseQuery spot checks the parsing of a string and makes sure the object produced
// is consistent.
func TestParseQuery(t *testing.T) {
	unittest.SmallTest(t)

	q := &Search{}
	err := clearParseQuery(q, "fdiffmax=-1&fref=false&frgbamax=-1&head=true&include=false&issue=2370153003&limit=50&match=gamma_correct&match=name&metric=combined&neg=false&pos=false&query=source_type%3Dgm&sort=desc&unt=true")
	require.NoError(t, err)

	require.Equal(t, &Search{
		Metric:         "combined",
		Sort:           "desc",
		Match:          []string{"gamma_correct", "name"},
		BlameGroupID:   "",
		Pos:            false,
		Neg:            false,
		Head:           true,
		Unt:            true,
		IncludeIgnores: false,
		QueryStr:       "",
		TraceValues: paramtools.ParamSet{
			"source_type": []string{"gm"},
		},
		RQueryStr:     "",
		RTraceValues:  paramtools.ParamSet{},
		ChangeListID:  "2370153003",
		PatchSetsStr:  "",
		PatchSets:     []int64(nil),
		IncludeMaster: false,
		FCommitBegin:  "",
		FCommitEnd:    "",
		FRGBAMin:      0,
		FRGBAMax:      -1,
		FDiffMax:      -1,
		FGroupTest:    "",
		FRef:          false,
		Offset:        0,
		Limit:         50,
		NoDiff:        false,
	}, q)
}

// TestParseSearchValidList checks a list of queries from live data
// processes as valid.
func TestParseSearchValidList(t *testing.T) {
	unittest.SmallTest(t)

	// Load the list of of live queries.
	contents, err := testutils.ReadFile("valid_queries.txt")
	require.NoError(t, err)

	queries := strings.Split(contents, "\n")

	for _, qStr := range queries {
		assertQueryValidity(t, true, qStr)
	}
}

// TestParseSearchInvalidList checks a list of queries from live data
// processes as invalid.
func TestParseSearchInvalidList(t *testing.T) {
	unittest.SmallTest(t)

	// Load the list of of live queries.
	contents, err := testutils.ReadFile("invalid_queries.txt")
	require.NoError(t, err)

	queries := strings.Split(contents, "\n")

	for _, qStr := range queries {
		assertQueryValidity(t, false, qStr)
	}
}

func assertQueryValidity(t *testing.T, isCorrect bool, qStr string) {
	assertFn := require.NoError
	if !isCorrect {
		assertFn = require.Error
	}
	q := &Search{}
	assertFn(t, clearParseQuery(q, qStr), qStr)
}

func clearParseQuery(q *Search, qStr string) error {
	*q = Search{}
	r, err := http.NewRequest("GET", "/?"+qStr, nil)
	if err != nil {
		return err
	}
	return ParseSearch(r, q)
}

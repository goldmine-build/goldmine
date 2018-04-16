package db

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"go.skia.org/infra/go/testutils"
)

func TestJobSearch(t *testing.T) {
	testutils.SmallTest(t)

	now := time.Now()

	j := makeFullJob(now)
	j.Name = "Build-Win-Clang-x86_64-Debug-Vulkan"

	emptyParams := func() *JobSearchParams {
		return &JobSearchParams{
			TimeStart: now.Add(-1 * time.Hour),
			TimeEnd:   now.Add(1 * time.Hour),
		}
	}
	isForce := new(bool)
	*isForce = j.IsForce
	matchParams := func() *JobSearchParams {
		return &JobSearchParams{
			RepoState: RepoState{
				Patch: Patch{
					Issue:    j.Issue,
					Patchset: j.Patchset,
					Server:   j.Server,
				},
				Repo:     j.Repo,
				Revision: j.Revision,
			},
			BuildbucketBuildId: j.BuildbucketBuildId,
			IsForce:            isForce,
			Name:               j.Name,
			Status:             j.Status,
			TimeStart:          now.Add(-1 * time.Hour),
			TimeEnd:            now.Add(1 * time.Hour),
		}
	}

	checkMatches := func(p *JobSearchParams) {
		jobs, err := matchJobs([]*Job{j}, p)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(jobs))
		testutils.AssertDeepEqual(t, j, jobs[0])
	}
	checkNoMatch := func(p *JobSearchParams) {
		jobs, err := matchJobs([]*Job{j}, p)
		assert.NoError(t, err)
		assert.Equal(t, 0, len(jobs))
	}

	// Sanity check: both emptyParams and matchParams should match.
	checkMatches(matchParams())
	checkMatches(emptyParams())
	checkNoMatch(&JobSearchParams{})

	// Check each individual parameter.

	// Issue
	p := emptyParams()
	p.Issue = j.Issue
	checkMatches(p)
	p = matchParams()
	p.Issue = "bogus"
	checkNoMatch(p)

	// Patchset
	p = emptyParams()
	p.Patchset = j.Patchset
	checkMatches(p)
	p = matchParams()
	p.Patchset = "bogus"
	checkNoMatch(p)

	// Server
	p = emptyParams()
	p.Server = j.Server
	checkMatches(p)
	p = matchParams()
	p.Server = "bogus"
	checkNoMatch(p)

	// Repo
	p = emptyParams()
	p.Repo = j.Repo
	checkMatches(p)
	p = matchParams()
	p.Repo = "bogus"
	checkNoMatch(p)

	// Revision
	p = emptyParams()
	p.Revision = j.Revision
	checkMatches(p)
	p = matchParams()
	p.Revision = "bogus"
	checkNoMatch(p)

	// BuildbucketBuildId
	p = emptyParams()
	p.BuildbucketBuildId = j.BuildbucketBuildId
	checkMatches(p)
	p = matchParams()
	p.BuildbucketBuildId = 999991
	checkNoMatch(p)

	// IsForce
	p = emptyParams()
	testIsForce := new(bool)
	*testIsForce = j.IsForce
	p.IsForce = testIsForce
	checkMatches(p)
	p = matchParams()
	*testIsForce = false
	p.IsForce = testIsForce
	checkNoMatch(p)

	// Name
	p = emptyParams()
	p.Name = j.Name
	checkMatches(p)
	p.Name = j.Name[:3] + ".*"
	checkMatches(p)
	p = matchParams()
	p.Name = "bogus"
	checkNoMatch(p)
	p = matchParams()
	p.Name = "^T.*"
	checkNoMatch(p)
	p.Name = "((("
	_, err := matchJobs([]*Job{}, p)
	assert.EqualError(t, err, "error parsing regexp: missing closing ): `(((`")

	// Status
	p = emptyParams()
	p.Status = j.Status
	checkMatches(p)
	p = matchParams()
	p.Status = "bogus"
	checkNoMatch(p)

	// Check time periods.

	// Inclusive TimeStart.
	p = matchParams()
	p.TimeStart = j.Created
	checkMatches(p)

	// j.Created just before p.TimeStart.
	p.TimeStart = j.Created.Add(time.Millisecond)
	checkNoMatch(p)

	// Non-inclusive TimeEnd.
	p = matchParams()
	p.TimeEnd = j.Created
	checkNoMatch(p)

	// j.Created Just before TimeEnd.
	p.TimeEnd = j.Created.Add(time.Millisecond)
	checkMatches(p)
}

func TestJobSearchParamsJson(t *testing.T) {
	testutils.SmallTest(t)

	decode := func(j string) *JobSearchParams {
		var rv JobSearchParams
		assert.NoError(t, json.Unmarshal([]byte(j), &rv))
		return &rv
	}
	testIsForce := new(bool)
	*testIsForce = true
	p := &JobSearchParams{}
	testutils.AssertDeepEqual(t, p, decode(`{}`))

	p.IsForce = testIsForce
	testutils.AssertDeepEqual(t, p, decode(`{"is_force": true}`))

	*p.IsForce = false
	testutils.AssertDeepEqual(t, p, decode(`{"is_force": false}`))
}

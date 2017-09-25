package incremental

import (
	"io/ioutil"
	"testing"
	"time"

	assert "github.com/stretchr/testify/require"

	"go.skia.org/infra/go/git/gitinfo"
	"go.skia.org/infra/go/git/repograph"
	git_testutils "go.skia.org/infra/go/git/testutils"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/vcsinfo"
	"go.skia.org/infra/task_scheduler/go/db"
	"go.skia.org/infra/task_scheduler/go/window"
)

func setup(t *testing.T) (string, *IncrementalCache, repograph.Map, db.DB, *git_testutils.GitBuilder, func()) {
	testutils.MediumTest(t)
	taskDb := db.NewInMemoryTaskDB()
	commentDb := &db.CommentBox{}
	d := db.NewDB(taskDb, nil, commentDb)

	gb := git_testutils.GitInit(t)
	c0 := gb.CommitGen("dummy")
	workdir, err := ioutil.TempDir("", "")
	assert.NoError(t, err)
	repo, err := repograph.NewGraph(gb.Dir(), workdir)
	assert.NoError(t, err)
	repos := repograph.Map{
		gb.RepoUrl(): repo,
	}
	assert.NoError(t, repos.Update())

	initialTask := &db.Task{
		Created:    time.Now(),
		DbModified: time.Now(),
		Id:         "0",
		TaskKey: db.TaskKey{
			RepoState: db.RepoState{
				Repo:     gb.RepoUrl(),
				Revision: c0,
			},
			Name: "DummyTask",
		},
	}
	assert.NoError(t, taskDb.PutTask(initialTask))

	w, err := window.New(24*time.Hour, 100, repos)
	assert.NoError(t, err)

	cache, err := NewIncrementalCache(d, w, repos, 100, "https://swarming", "https://task-scheduler")
	assert.NoError(t, err)

	return workdir, cache, repos, d, gb, func() {
		testutils.RemoveAll(t, workdir)
		gb.Cleanup()
	}
}

func update(t *testing.T, repo string, c *IncrementalCache, ts time.Time) (*Update, time.Time) {
	assert.NoError(t, c.Update(false))
	now := time.Now()
	u, err := c.Get(repo, ts, 100)
	assert.NoError(t, err)
	return u, now
}

func TestIncrementalCache(t *testing.T) {
	_, cache, repos, taskDb, gb, cleanup := setup(t)
	defer cleanup()

	repoUrl := ""
	for r, _ := range repos {
		repoUrl = r
		break
	}

	// Verify the initial state.
	assert.Equal(t, 1, len(cache.updates[repoUrl]))
	ts := time.Now()
	ts0 := ts // Used later.
	u, err := cache.GetAll(repoUrl, 100)
	assert.NoError(t, err)
	startOver := new(bool)
	*startOver = true
	assert.Equal(t, 1, len(u.BranchHeads))
	assert.Equal(t, map[string][]*CommitComment(nil), u.CommitComments)
	assert.Equal(t, 1, len(u.Commits))
	assert.Equal(t, startOver, u.StartOver)
	assert.Equal(t, "https://swarming", u.SwarmingUrl)
	assert.Equal(t, map[string]map[string][]*TaskComment(nil), u.TaskComments)
	assert.Equal(t, 1, len(u.Tasks))
	assert.Equal(t, "https://task-scheduler", u.TaskSchedulerUrl)
	assert.Equal(t, map[string][]*TaskSpecComment(nil), u.TaskSpecComments)

	// Add different types of elements, one by one, and verify that they
	// are represented in new updates.

	// Modify the task.
	t0, err := taskDb.GetTaskById(u.Tasks[0].Id)
	assert.NoError(t, err)
	t0.Status = db.TASK_STATUS_SUCCESS
	assert.NoError(t, taskDb.PutTask(t0))
	u, ts = update(t, repoUrl, cache, ts)
	// Expect a mostly-empty update with just the updated task.
	assert.Equal(t, []*gitinfo.GitBranch(nil), u.BranchHeads)
	assert.Equal(t, map[string][]*CommitComment(nil), u.CommitComments)
	assert.Equal(t, []*vcsinfo.LongCommit(nil), u.Commits)
	assert.Equal(t, (*bool)(nil), u.StartOver)
	assert.Equal(t, "", u.SwarmingUrl)
	assert.Equal(t, map[string]map[string][]*TaskComment(nil), u.TaskComments)
	assert.Equal(t, 1, len(u.Tasks))
	assert.Equal(t, "", u.TaskSchedulerUrl)
	assert.Equal(t, map[string][]*TaskSpecComment(nil), u.TaskSpecComments)

	// Add a TaskComment.
	tc := db.TaskComment{
		Repo:      t0.Repo,
		Revision:  t0.Revision,
		Name:      t0.Name,
		Timestamp: time.Now(),
		TaskId:    t0.Id,
		User:      "me",
		Message:   "here's a task comment.",
	}
	assert.NoError(t, taskDb.PutTaskComment(&tc))
	u, ts = update(t, repoUrl, cache, ts)
	// Expect a mostly-empty update with just the new TaskComment.
	assert.Equal(t, []*gitinfo.GitBranch(nil), u.BranchHeads)
	assert.Equal(t, map[string][]*CommitComment(nil), u.CommitComments)
	assert.Equal(t, []*vcsinfo.LongCommit(nil), u.Commits)
	assert.Equal(t, (*bool)(nil), u.StartOver)
	assert.Equal(t, "", u.SwarmingUrl)
	testutils.AssertDeepEqual(t, tc, u.TaskComments[t0.Revision][t0.Name][0].TaskComment)
	assert.Equal(t, []*Task(nil), u.Tasks)
	assert.Equal(t, "", u.TaskSchedulerUrl)
	assert.Equal(t, map[string][]*TaskSpecComment(nil), u.TaskSpecComments)

	// Verify that both the task from the previous update AND the
	// TaskComment appear if we request an earlier timestamp.
	u, err = cache.Get(repoUrl, ts0, 100)
	assert.Equal(t, []*gitinfo.GitBranch(nil), u.BranchHeads)
	assert.Equal(t, map[string][]*CommitComment(nil), u.CommitComments)
	assert.Equal(t, []*vcsinfo.LongCommit(nil), u.Commits)
	assert.Equal(t, (*bool)(nil), u.StartOver)
	assert.Equal(t, "", u.SwarmingUrl)
	testutils.AssertDeepEqual(t, tc, u.TaskComments[t0.Revision][t0.Name][0].TaskComment)
	assert.Equal(t, 1, len(u.Tasks))
	assert.Equal(t, "", u.TaskSchedulerUrl)
	assert.Equal(t, map[string][]*TaskSpecComment(nil), u.TaskSpecComments)

	// CommitComment.
	cc := db.CommitComment{
		Repo:          t0.Repo,
		Revision:      t0.Revision,
		Timestamp:     time.Now(),
		User:          "me",
		IgnoreFailure: true,
		Message:       "here's a commit comment",
	}
	assert.NoError(t, taskDb.PutCommitComment(&cc))
	u, ts = update(t, repoUrl, cache, ts)
	// Expect a mostly-empty update with just the new CommitComment.
	assert.Equal(t, []*gitinfo.GitBranch(nil), u.BranchHeads)
	testutils.AssertDeepEqual(t, cc, u.CommitComments[t0.Revision][0].CommitComment)
	assert.Equal(t, []*vcsinfo.LongCommit(nil), u.Commits)
	assert.Equal(t, (*bool)(nil), u.StartOver)
	assert.Equal(t, "", u.SwarmingUrl)
	assert.Equal(t, map[string]map[string][]*TaskComment(nil), u.TaskComments)
	assert.Equal(t, []*Task(nil), u.Tasks)
	assert.Equal(t, "", u.TaskSchedulerUrl)
	assert.Equal(t, map[string][]*TaskSpecComment(nil), u.TaskSpecComments)

	// TaskSpecComment.
	tsc := db.TaskSpecComment{
		Repo:          t0.Repo,
		Name:          t0.Name,
		Timestamp:     time.Now(),
		User:          "me",
		Flaky:         true,
		IgnoreFailure: true,
		Message:       "here's a task spec comment",
	}
	assert.NoError(t, taskDb.PutTaskSpecComment(&tsc))
	u, ts = update(t, repoUrl, cache, ts)
	// Expect a mostly-empty update with just the new TaskSpecComment.
	assert.Equal(t, []*gitinfo.GitBranch(nil), u.BranchHeads)
	assert.Equal(t, map[string][]*CommitComment(nil), u.CommitComments)
	assert.Equal(t, []*vcsinfo.LongCommit(nil), u.Commits)
	assert.Equal(t, (*bool)(nil), u.StartOver)
	assert.Equal(t, "", u.SwarmingUrl)
	assert.Equal(t, map[string]map[string][]*TaskComment(nil), u.TaskComments)
	assert.Equal(t, []*Task(nil), u.Tasks)
	assert.Equal(t, "", u.TaskSchedulerUrl)
	testutils.AssertDeepEqual(t, tsc, u.TaskSpecComments[t0.Name][0].TaskSpecComment)

	// Add a new commit.
	gb.CommitGen("dummy")
	u, ts = update(t, repoUrl, cache, ts)
	// Expect a mostly-empty update with just the new commit and the branch heads..
	assert.Equal(t, 1, len(u.BranchHeads))
	assert.Equal(t, map[string][]*CommitComment(nil), u.CommitComments)
	assert.Equal(t, 1, len(u.Commits))
	assert.Equal(t, (*bool)(nil), u.StartOver)
	assert.Equal(t, "", u.SwarmingUrl)
	assert.Equal(t, map[string]map[string][]*TaskComment(nil), u.TaskComments)
	assert.Equal(t, []*Task(nil), u.Tasks)
	assert.Equal(t, "", u.TaskSchedulerUrl)
	assert.Equal(t, map[string][]*TaskSpecComment(nil), u.TaskSpecComments)

	// This will cause the cache to reload from scratch.
	assert.NoError(t, cache.Update(true))
	// Expect the update to contain ALL of the information we've seen so
	// far, even though we're requesting the most recent.
	u, ts = update(t, repoUrl, cache, ts)
	assert.Equal(t, 1, len(u.BranchHeads))
	testutils.AssertDeepEqual(t, cc, u.CommitComments[t0.Revision][0].CommitComment)
	assert.Equal(t, 2, len(u.Commits))
	assert.Equal(t, startOver, u.StartOver)
	assert.Equal(t, "https://swarming", u.SwarmingUrl)
	testutils.AssertDeepEqual(t, tc, u.TaskComments[t0.Revision][t0.Name][0].TaskComment)
	assert.Equal(t, 1, len(u.Tasks))
	assert.Equal(t, "https://task-scheduler", u.TaskSchedulerUrl)
	testutils.AssertDeepEqual(t, tsc, u.TaskSpecComments[t0.Name][0].TaskSpecComment)
}

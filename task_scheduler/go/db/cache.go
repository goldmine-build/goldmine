package db

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"go.skia.org/infra/go/git/repograph"
	"go.skia.org/infra/task_scheduler/go/window"

	"go.skia.org/infra/go/sklog"
)

const (
	// Allocate enough space for this many tasks.
	TASKS_INIT_CAPACITY = 60000
)

type TaskCache interface {

	// GetTask returns the task with the given ID, or an error if no such task exists.
	GetTask(string) (*Task, error)

	// GetTaskMaybeExpired does the same as GetTask but tries to dig into
	// the DB in case the Task is old enough to have scrolled out of the
	// cache window.
	GetTaskMaybeExpired(string) (*Task, error)

	// GetTaskForCommit retrieves the task with the given name which ran at the
	// given commit, or nil if no such task exists.
	GetTaskForCommit(string, string, string) (*Task, error)

	// GetTasksByKey returns the tasks with the given TaskKey, sorted
	// by creation time.
	GetTasksByKey(*TaskKey) ([]*Task, error)

	// GetTasksForCommits retrieves all tasks which included[1] each of the
	// given commits. Returns a map whose keys are commit hashes and values are
	// sub-maps whose keys are task spec names and values are tasks.
	//
	// 1) Blamelist calculation is outside the scope of the taskCache, but the
	//    implied assumption here is that there is at most one task for each
	//    task spec which has a given commit in its blamelist. The user is
	//    responsible for inserting tasks into the database so that this invariant
	//    is maintained. Generally, a more recent task will "steal" commits from an
	//    earlier task's blamelist, if the blamelists overlap. There are three
	//    cases to consider:
	//       1. The newer task ran at a newer commit than the older task. Its
	//          blamelist consists of all commits not covered by the previous task,
	//          and therefore does not overlap with the older task's blamelist.
	//       2. The newer task ran at the same commit as the older task. Its
	//          blamelist is the same as the previous task's blamelist, and
	//          therefore it "steals" all commits from the previous task, whose
	//          blamelist becomes empty.
	//       3. The newer task ran at a commit which was in the previous task's
	//          blamelist. Its blamelist consists of the commits in the previous
	//          task's blamelist which it also covered. Those commits move out of
	//          the previous task's blamelist and into the newer task's blamelist.
	GetTasksForCommits(string, []string) (map[string]map[string]*Task, error)

	// GetTasksFromDateRange retrieves all tasks which were created in the given
	// date range.
	GetTasksFromDateRange(from time.Time, to time.Time) ([]*Task, error)

	// KnownTaskName returns true iff the given task name has been seen
	// before for a non-forced, non-tryjob run.
	KnownTaskName(string, string) bool

	// UnfinishedTasks returns a list of tasks which were not finished at
	// the time of the last cache update. Fake tasks are not included.
	UnfinishedTasks() ([]*Task, error)

	// Update loads new tasks from the database.
	Update() error
}

type taskCache struct {
	db TaskReader
	// map[repo_name][task_spec_name]Task.Created for most recent Task.
	knownTaskNames map[string]map[string]time.Time
	mtx            sync.RWMutex
	queryId        string
	tasks          map[string]*Task
	// map[repo_name][commit_hash][task_spec_name]*Task
	tasksByCommit map[string]map[string]map[string]*Task
	// map[TaskKey]map[task_id]*Task
	tasksByKey map[TaskKey]map[string]*Task
	// tasksByTime is sorted by Task.Created.
	tasksByTime []*Task
	timeWindow  *window.Window
	unfinished  map[string]*Task
}

// See documentation for TaskCache interface.
func (c *taskCache) GetTask(id string) (*Task, error) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()

	if t, ok := c.tasks[id]; ok {
		return t.Copy(), nil
	}
	return nil, ErrNotFound
}

// See documentation for TaskCache interface.
func (c *taskCache) GetTaskMaybeExpired(id string) (*Task, error) {
	t, err := c.GetTask(id)
	if err == nil {
		return t, nil
	} else if err != ErrNotFound {
		return nil, err
	}
	// Fall back to searching the DB.
	t, err = c.db.GetTaskById(id)
	if err != nil {
		return nil, err
	} else if t == nil {
		return nil, ErrNotFound
	}
	return t, nil
}

// See documentation for TaskCache interface.
func (c *taskCache) GetTasksByKey(k *TaskKey) ([]*Task, error) {
	if !k.Valid() {
		return nil, fmt.Errorf("TaskKey is invalid: %v", k)
	}

	c.mtx.RLock()
	defer c.mtx.RUnlock()

	tasks := c.tasksByKey[*k]
	rv := make([]*Task, 0, len(tasks))
	for _, t := range tasks {
		rv = append(rv, t.Copy())
	}
	sort.Sort(TaskSlice(rv))
	return rv, nil
}

// See documentation for TaskCache interface.
func (c *taskCache) GetTasksForCommits(repo string, commits []string) (map[string]map[string]*Task, error) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()

	rv := make(map[string]map[string]*Task, len(commits))
	commitMap := c.tasksByCommit[repo]
	for _, commit := range commits {
		if tasks, ok := commitMap[commit]; ok {
			rv[commit] = make(map[string]*Task, len(tasks))
			for k, v := range tasks {
				rv[commit][k] = v.Copy()
			}
		} else {
			rv[commit] = map[string]*Task{}
		}
	}
	return rv, nil
}

// searchTaskSlice returns the index in tasks of the first Task whose Created
// time is >= ts.
func searchTaskSlice(tasks []*Task, ts time.Time) int {
	return sort.Search(len(tasks), func(i int) bool {
		return !tasks[i].Created.Before(ts)
	})
}

// See documentation for TaskCache interface.
func (c *taskCache) GetTasksFromDateRange(from time.Time, to time.Time) ([]*Task, error) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	fromIdx := searchTaskSlice(c.tasksByTime, from)
	toIdx := searchTaskSlice(c.tasksByTime, to)
	rv := make([]*Task, toIdx-fromIdx)
	for i, task := range c.tasksByTime[fromIdx:toIdx] {
		rv[i] = task.Copy()
	}
	return rv, nil
}

// See documentation for TaskCache interface.
func (c *taskCache) KnownTaskName(repo, name string) bool {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	_, ok := c.knownTaskNames[repo][name]
	return ok
}

// See documentation for TaskCache interface.
func (c *taskCache) GetTaskForCommit(repo, commit, name string) (*Task, error) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()

	commitMap, ok := c.tasksByCommit[repo]
	if !ok {
		return nil, nil
	}
	if tasks, ok := commitMap[commit]; ok {
		if t, ok := tasks[name]; ok {
			return t.Copy(), nil
		}
	}
	return nil, nil
}

// See documentation for TaskCache interface.
func (c *taskCache) UnfinishedTasks() ([]*Task, error) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()

	rv := make([]*Task, 0, len(c.unfinished))
	for _, t := range c.unfinished {
		rv = append(rv, t.Copy())
	}
	return rv, nil
}

// removeFromTasksByCommit removes task (which must be a previously-inserted
// Task, not a new Task) from c.tasksByCommit for all of task.Commits. Assumes
// the caller holds a lock.
func (c *taskCache) removeFromTasksByCommit(task *Task) {
	if commitMap, ok := c.tasksByCommit[task.Repo]; ok {
		for _, commit := range task.Commits {
			// Shouldn't be necessary to check other.Id == task.Id, but being paranoid.
			if other, ok := commitMap[commit][task.Name]; ok && other.Id == task.Id {
				delete(commitMap[commit], task.Name)
				if len(commitMap[commit]) == 0 {
					delete(commitMap, commit)
				}
			}
		}
	}

}

// expireTasks removes data from c whose Created time is before the beginning
// of the Window. Assumes the caller holds a lock. This is a helper for
// expireAndUpdate.
func (c *taskCache) expireTasks() {
	for repoUrl, nameMap := range c.knownTaskNames {
		for name, ts := range nameMap {
			if !c.timeWindow.TestTime(repoUrl, ts) {
				delete(nameMap, name)
			}
		}
	}
	for i, task := range c.tasksByTime {
		if c.timeWindow.TestTime(task.Repo, task.Created) {
			c.tasksByTime = c.tasksByTime[i:]
			return
		}

		// Tasks by ID.
		delete(c.tasks, task.Id)

		// Tasks by commit.
		c.removeFromTasksByCommit(task)

		// Tasks by key.
		byKey, ok := c.tasksByKey[task.TaskKey]
		if ok {
			delete(byKey, task.Id)
			if len(byKey) == 0 {
				delete(c.tasksByKey, task.TaskKey)
			}
		}

		// Tasks by time.
		c.tasksByTime[i] = nil // Allow GC.

		// Unfinished tasks.
		if _, ok := c.unfinished[task.Id]; ok {
			sklog.Warningf("Found unfinished task that is so old it is being expired. %#v", task)
			delete(c.unfinished, task.Id)
		}
	}
	if len(c.tasksByTime) > 0 {
		sklog.Warningf("All tasks expired because they are outside the window.")
		c.tasksByTime = nil
	}
}

// insertOrUpdateTask inserts task into the cache if it is a new task, or
// updates the existing entries if not. Assumes the caller holds a lock. This is
// a helper for expireAndUpdate.
func (c *taskCache) insertOrUpdateTask(task *Task) {
	old, isUpdate := c.tasks[task.Id]

	// Insert the new task into the main map.
	c.tasks[task.Id] = task

	// Insert into tasksByKey.
	byKey, ok := c.tasksByKey[task.TaskKey]
	if !ok {
		byKey = map[string]*Task{}
		c.tasksByKey[task.TaskKey] = byKey
	}
	byKey[task.Id] = task

	if isUpdate {
		// If we already know about this task, the blamelist might have changed, so
		// we need to remove it from tasksByCommit and re-insert where needed.
		c.removeFromTasksByCommit(old)
	}
	// Insert the task into tasksByCommits.
	commitMap, ok := c.tasksByCommit[task.Repo]
	if !ok {
		commitMap = map[string]map[string]*Task{}
		c.tasksByCommit[task.Repo] = commitMap
	}
	for _, commit := range task.Commits {
		if _, ok := commitMap[commit]; !ok {
			commitMap[commit] = map[string]*Task{}
		}
		commitMap[commit][task.Name] = task
	}

	if isUpdate {
		// Loop in case there are multiple tasks with the same Created time.
		for i := searchTaskSlice(c.tasksByTime, task.Created); i < len(c.tasksByTime); i++ {
			other := c.tasksByTime[i]
			if other.Id == task.Id {
				c.tasksByTime[i] = task
				break
			}
			if !other.Created.Equal(task.Created) {
				panic(fmt.Sprintf("taskCache inconsistent; c.tasks contains task not in c.tasksByTime. old: %v, task: %v", old, task))
			}
		}
	} else {
		// If profiling indicates this code is slow or GCs too much, see
		// https://skia.googlesource.com/buildbot/+/0cf94832dd57f0e7b5b9f1b28546181d15dbbbc6
		// for a different implementation.
		// Most common case is that the new task should be inserted at the end.
		if len(c.tasksByTime) == 0 {
			c.tasksByTime = append(make([]*Task, 0, TASKS_INIT_CAPACITY), task)
		} else if lastTask := c.tasksByTime[len(c.tasksByTime)-1]; !task.Created.Before(lastTask.Created) {
			c.tasksByTime = append(c.tasksByTime, task)
		} else {
			insertIdx := searchTaskSlice(c.tasksByTime, task.Created)
			// Extend size by one:
			c.tasksByTime = append(c.tasksByTime, nil)
			// Move later elements out of the way:
			copy(c.tasksByTime[insertIdx+1:], c.tasksByTime[insertIdx:])
			// Assign at the correct index:
			c.tasksByTime[insertIdx] = task
		}
	}

	// Unfinished tasks.
	if !task.Done() && !task.Fake() {
		c.unfinished[task.Id] = task
	} else if isUpdate {
		delete(c.unfinished, task.Id)
	}

	// Known task names.
	if !task.IsForceRun() && !task.IsTryJob() {
		if nameMap, ok := c.knownTaskNames[task.Repo]; ok {
			if ts, ok := nameMap[task.Name]; !ok || ts.Before(task.Created) {
				nameMap[task.Name] = task.Created
			}
		} else {
			c.knownTaskNames[task.Repo] = map[string]time.Time{task.Name: task.Created}
		}
	}
}

// expireAndUpdate removes Tasks outside the window from the cache and inserts
// new/updated tasks into the cache. Assumes the caller holds a lock. Assumes
// tasks are sorted by Created timestamp.
func (c *taskCache) expireAndUpdate(tasks []*Task) {
	c.expireTasks()
	for _, t := range tasks {
		if c.timeWindow.TestTime(t.Repo, t.Created) {
			c.insertOrUpdateTask(t.Copy())
		}
	}
}

// reset re-initializes c. Assumes the caller holds a lock.
func (c *taskCache) reset() error {
	if c.queryId != "" {
		c.db.StopTrackingModifiedTasks(c.queryId)
	}
	queryId, err := c.db.StartTrackingModifiedTasks()
	if err != nil {
		return err
	}
	start := c.timeWindow.EarliestStart()
	now := time.Now()
	sklog.Infof("Reading Tasks from %s to %s.", start, now)
	tasks, err := c.db.GetTasksFromDateRange(start, now)
	if err != nil {
		c.db.StopTrackingModifiedTasks(queryId)
		return err
	}
	c.knownTaskNames = map[string]map[string]time.Time{}
	c.queryId = queryId
	c.tasks = map[string]*Task{}
	c.tasksByCommit = map[string]map[string]map[string]*Task{}
	c.tasksByKey = map[TaskKey]map[string]*Task{}
	c.unfinished = map[string]*Task{}
	c.expireAndUpdate(tasks)
	return nil
}

// See documentation for TaskCache interface.
func (c *taskCache) Update() error {
	newTasks, err := c.db.GetModifiedTasks(c.queryId)
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if IsUnknownId(err) {
		sklog.Warningf("Connection to db lost; re-initializing cache from scratch.")
		if err := c.reset(); err != nil {
			return err
		}
		return nil
	} else if err != nil {
		return err
	}
	c.expireAndUpdate(newTasks)
	return nil
}

// NewTaskCache returns a local cache which provides more convenient views of
// task data than the database can provide.
func NewTaskCache(db TaskReader, timeWindow *window.Window) (TaskCache, error) {
	tc := &taskCache{
		db:         db,
		timeWindow: timeWindow,
	}
	if err := tc.reset(); err != nil {
		return nil, err
	}
	return tc, nil
}

type JobCache interface {
	// GetJob returns the job with the given ID, or an error if no such job exists.
	GetJob(string) (*Job, error)

	// GetJobMaybeExpired does the same as GetJob but tries to dig into the
	// DB in case the Job is old enough to have scrolled out of the cache
	// window.
	GetJobMaybeExpired(string) (*Job, error)

	// ScheduledJobsForCommit indicates whether or not we triggered any jobs
	// for the given repo/commit.
	ScheduledJobsForCommit(string, string) (bool, error)

	// UnfinishedJobs returns a list of jobs which were not finished at
	// the time of the last cache update.
	UnfinishedJobs() ([]*Job, error)

	// Update loads new jobs from the database.
	Update() error
}

// GetRevisionTimestamp is a function signature that retrieves the timestamp of
// a revision. NewJobCache accepts this type rather than repograph.Map to aide
// testing.
type GetRevisionTimestamp func(repo, revision string) (time.Time, error)

type jobCache struct {
	db                   JobDB
	getRevisionTimestamp GetRevisionTimestamp
	mtx                  sync.RWMutex
	queryId              string
	jobs                 map[string]*Job
	timeWindow           *window.Window
	triggeredForCommit   map[string]map[string]bool
	unfinished           map[string]*Job
}

// getJobTimestamp returns the timestamp of a Job for purposes of cache
// expiration.
func (c *jobCache) getJobTimestamp(job *Job) time.Time {
	return job.Created
}

// See documentation for JobCache interface.
func (c *jobCache) GetJob(id string) (*Job, error) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()

	if j, ok := c.jobs[id]; ok {
		return j.Copy(), nil
	}
	return nil, ErrNotFound
}

// See documentation for JobCache interface.
func (c *jobCache) GetJobMaybeExpired(id string) (*Job, error) {
	j, err := c.GetJob(id)
	if err == nil {
		return j, nil
	}
	if err != ErrNotFound {
		return nil, err
	}
	return c.db.GetJobById(id)
}

// See documentation for JobCache interface.
func (c *jobCache) ScheduledJobsForCommit(repo, rev string) (bool, error) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()
	return c.triggeredForCommit[repo][rev], nil
}

// See documentation for JobCache interface.
func (c *jobCache) UnfinishedJobs() ([]*Job, error) {
	c.mtx.RLock()
	defer c.mtx.RUnlock()

	rv := make([]*Job, 0, len(c.unfinished))
	for _, t := range c.unfinished {
		rv = append(rv, t.Copy())
	}
	return rv, nil
}

// expireJobs removes data from c where getJobTimestamp or getRevisionTimestamp
// is before start. Assumes the caller holds a lock. This is a helper for
// expireAndUpdate.
func (c *jobCache) expireJobs() {
	expiredUnfinishedCount := 0
	for _, job := range c.jobs {
		if !c.timeWindow.TestTime(job.Repo, c.getJobTimestamp(job)) {
			delete(c.jobs, job.Id)
			delete(c.unfinished, job.Id)
			if !job.Done() {
				expiredUnfinishedCount++
			}
		}
	}
	if expiredUnfinishedCount > 0 {
		sklog.Infof("Expired %d unfinished jobs created before window.", expiredUnfinishedCount)
	}
	for repo, revMap := range c.triggeredForCommit {
		for rev := range revMap {
			ts, err := c.getRevisionTimestamp(repo, rev)
			if err != nil {
				sklog.Error(err)
				continue
			}
			if !c.timeWindow.TestTime(repo, ts) {
				delete(revMap, rev)
			}
		}
	}
}

// insertOrUpdateJob inserts the new/updated job into the cache. Assumes the
// caller holds a lock. This is a helper for expireAndUpdate.
func (c *jobCache) insertOrUpdateJob(job *Job) {
	// Insert the new job into the main map.
	c.jobs[job.Id] = job

	// ScheduledJobsForCommit.
	if !job.IsForce && !job.IsTryJob() {
		if _, ok := c.triggeredForCommit[job.Repo]; !ok {
			c.triggeredForCommit[job.Repo] = map[string]bool{}
		}
		c.triggeredForCommit[job.Repo][job.Revision] = true
	}

	// Unfinished jobs.
	if job.Done() {
		delete(c.unfinished, job.Id)
	} else {
		c.unfinished[job.Id] = job
	}
}

// expireAndUpdate removes Jobs before the window and inserts the
// new/updated jobs into the cache. Assumes the caller holds a lock.
func (c *jobCache) expireAndUpdate(jobs []*Job) {
	c.expireJobs()
	for _, job := range jobs {
		ts := c.getJobTimestamp(job)
		if !c.timeWindow.TestTime(job.Repo, ts) {
			//sklog.Warningf("Updated job %s after expired. getJobTimestamp returned %s. %#v", job.Id, ts, job)
		} else {
			c.insertOrUpdateJob(job.Copy())
		}
	}
}

// reset re-initializes c. Assumes the caller holds a lock.
func (c *jobCache) reset() error {
	if c.queryId != "" {
		c.db.StopTrackingModifiedJobs(c.queryId)
	}
	queryId, err := c.db.StartTrackingModifiedJobs()
	if err != nil {
		return err
	}
	now := time.Now()
	start := c.timeWindow.EarliestStart()
	sklog.Infof("Reading Jobs from %s to %s.", start, now)
	jobs, err := c.db.GetJobsFromDateRange(start, now)
	if err != nil {
		c.db.StopTrackingModifiedJobs(queryId)
		return err
	}
	c.queryId = queryId
	c.jobs = map[string]*Job{}
	c.triggeredForCommit = map[string]map[string]bool{}
	c.unfinished = map[string]*Job{}
	c.expireAndUpdate(jobs)
	return nil
}

// See documentation for JobCache interface.
func (c *jobCache) Update() error {
	newJobs, err := c.db.GetModifiedJobs(c.queryId)
	c.mtx.Lock()
	defer c.mtx.Unlock()
	if IsUnknownId(err) {
		sklog.Warningf("Connection to db lost; re-initializing cache from scratch.")
		if err := c.reset(); err != nil {
			return err
		}
		return nil
	} else if err != nil {
		return err
	}
	c.expireAndUpdate(newJobs)
	return nil
}

// NewJobCache returns a local cache which provides more convenient views of
// job data than the database can provide.
func NewJobCache(db JobDB, timeWindow *window.Window, getRevisionTimestamp GetRevisionTimestamp) (JobCache, error) {
	tc := &jobCache{
		db:                   db,
		getRevisionTimestamp: getRevisionTimestamp,
		timeWindow:           timeWindow,
	}
	if err := tc.reset(); err != nil {
		return nil, err
	}
	return tc, nil
}

// GitRepoGetRevisionTimestamp returns a GetRevisionTimestamp function that gets
// the revision timestamp from repos, which maps repo name to *repograph.Graph.
func GitRepoGetRevisionTimestamp(repos repograph.Map) GetRevisionTimestamp {
	return func(repo, revision string) (time.Time, error) {
		r, ok := repos[repo]
		if !ok {
			return time.Time{}, fmt.Errorf("Unknown repo %s", repo)
		}
		c := r.Get(revision)
		if c == nil {
			return time.Time{}, fmt.Errorf("Unknown commit %s@%s", repo, revision)
		}
		return c.Timestamp, nil
	}
}

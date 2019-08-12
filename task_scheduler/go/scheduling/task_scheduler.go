package scheduling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"path"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	multierror "github.com/hashicorp/go-multierror"
	swarming_api "go.chromium.org/luci/common/api/swarming/swarming/v1"
	"go.chromium.org/luci/common/isolated"
	"go.skia.org/infra/go/cleanup"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/gcs"
	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/git/repograph"
	"go.skia.org/infra/go/isolate"
	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/swarming"
	"go.skia.org/infra/go/timeout"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/task_scheduler/go/blacklist"
	"go.skia.org/infra/task_scheduler/go/cacher"
	"go.skia.org/infra/task_scheduler/go/db"
	"go.skia.org/infra/task_scheduler/go/db/cache"
	"go.skia.org/infra/task_scheduler/go/db/firestore"
	"go.skia.org/infra/task_scheduler/go/isolate_cache"
	"go.skia.org/infra/task_scheduler/go/specs"
	"go.skia.org/infra/task_scheduler/go/syncer"
	"go.skia.org/infra/task_scheduler/go/task_cfg_cache"
	"go.skia.org/infra/task_scheduler/go/tryjobs"
	"go.skia.org/infra/task_scheduler/go/types"
	"go.skia.org/infra/task_scheduler/go/window"
	"golang.org/x/oauth2"
	"golang.org/x/sync/errgroup"
)

const (
	// Manually-forced jobs have high priority.
	CANDIDATE_SCORE_FORCE_RUN = 100.0

	// Try jobs have high priority, equal to building at HEAD when we're
	// 5 commits behind.
	CANDIDATE_SCORE_TRY_JOB = 10.0

	// When retrying a try job task that has failed, prioritize the retry
	// lower than tryjob tasks that haven't run yet.
	CANDIDATE_SCORE_TRY_JOB_RETRY_MULTIPLIER = 0.75

	// When bisecting or retrying a task that failed or had a mishap, add a bonus
	// to the raw score.
	//
	// A value of 0.75 means that a retry scores higher than a bisecting a
	// successful task with a blamelist of 2 commits, but lower than testing new
	// commits or bisecting successful tasks with blamelist of 3 or
	// more. Bisecting a failure with a blamelist of 2 commits scores the same as
	// bisecting a successful task with a blamelist of 4 commits.
	CANDIDATE_SCORE_FAILURE_OR_MISHAP_BONUS = 0.75

	// MAX_BLAMELIST_COMMITS is the maximum number of commits which are
	// allowed in a task blamelist before we stop tracing commit history.
	MAX_BLAMELIST_COMMITS = 500

	// Measurement name for task candidate counts by dimension set.
	MEASUREMENT_TASK_CANDIDATE_COUNT = "task_candidate_count"

	NUM_TOP_CANDIDATES = 50

	// To avoid errors resulting from DB transaction size limits, we
	// restrict the number of tasks triggered per TaskSpec (we insert tasks
	// into the DB in chunks by TaskSpec) to half of the DB transaction size
	// limit (since we may need to update an existing whose blamelist was
	// split by the new task).
	SCHEDULING_LIMIT_PER_TASK_SPEC = firestore.MAX_TRANSACTION_DOCS / 2

	GCS_MAIN_LOOP_DIAGNOSTICS_DIR = "MainLoop"
	GCS_DIAGNOSTICS_WRITE_TIMEOUT = 60 * time.Second
)

var (
	// Don't schedule on these branches.
	// WARNING: Any commit reachable from any of these branches will be
	// skipped. So, for example, if you fork a branch from head of master
	// and immediately blacklist it, no tasks will be scheduled for any
	// commits on master up to the branch point.
	// TODO(borenet): An alternative would be to only follow the first
	// parent for merge commits. That way, we could remove the checks which
	// cause this issue but still blacklist the branch as expected. The
	// downside is that we'll miss commits in the case where we fork a
	// branch, merge it back, and delete the new branch head.
	BRANCH_BLACKLIST = map[string][]string{
		common.REPO_SKIA_INTERNAL: {
			"skia-master",
		},
	}

	ERR_BLAMELIST_DONE = errors.New("ERR_BLAMELIST_DONE")
)

// TaskScheduler is a struct used for scheduling tasks on bots.
type TaskScheduler struct {
	bl                  *blacklist.Blacklist
	busyBots            *busyBots
	cacher              *cacher.Cacher
	candidateMetrics    map[string]metrics2.Int64Metric
	candidateMetricsMtx sync.Mutex
	db                  db.DB
	depotToolsDir       string
	diagClient          gcs.GCSClient
	diagInstance        string
	isolateCache        *isolate_cache.Cache
	isolateClient       *isolate.Client
	jCache              cache.JobCache
	lastScheduled       time.Time // protected by queueMtx.

	// TODO(benjaminwagner): newTasks probably belongs in the TaskCfgCache.
	newTasks    map[types.RepoState]util.StringSet
	newTasksMtx sync.RWMutex

	pendingInsert    map[string]bool
	pendingInsertMtx sync.RWMutex

	pools        []string
	pubsubCount  metrics2.Counter
	pubsubTopic  string
	queue        []*taskCandidate // protected by queueMtx.
	queueMtx     sync.RWMutex
	repos        repograph.Map
	swarming     swarming.ApiClient
	syncer       *syncer.Syncer
	taskCfgCache *task_cfg_cache.TaskCfgCache
	tCache       cache.TaskCache
	// testWaitGroup keeps track of any goroutines the TaskScheduler methods
	// create so that tests can ensure all goroutines finish before asserting.
	testWaitGroup         sync.WaitGroup
	timeDecayAmt24Hr      float64
	triggeredCount        metrics2.Counter
	tryjobs               *tryjobs.TryJobIntegrator
	updateUnfinishedCount metrics2.Counter
	window                *window.Window
	workdir               string
}

func NewTaskScheduler(ctx context.Context, d db.DB, bl *blacklist.Blacklist, period time.Duration, numCommits int, workdir, host string, repos repograph.Map, isolateClient *isolate.Client, swarmingClient swarming.ApiClient, c *http.Client, timeDecayAmt24Hr float64, buildbucketApiUrl, trybotBucket string, projectRepoMapping map[string]string, pools []string, pubsubTopic, depotTools string, gerrit gerrit.GerritInterface, btProject, btInstance string, ts oauth2.TokenSource, diagClient gcs.GCSClient, diagInstance string) (*TaskScheduler, error) {
	// Repos must be updated before window is initialized; otherwise the repos may be uninitialized,
	// resulting in the window being too short, causing the caches to be loaded with incomplete data.
	for _, r := range repos {
		if err := r.Update(ctx); err != nil {
			return nil, fmt.Errorf("Failed initial repo sync: %s", err)
		}
	}
	w, err := window.New(period, numCommits, repos)
	if err != nil {
		return nil, fmt.Errorf("Failed to create window: %s", err)
	}

	// Create caches.
	tCache, err := cache.NewTaskCache(d, w)
	if err != nil {
		return nil, fmt.Errorf("Failed to create TaskCache: %s", err)
	}

	jCache, err := cache.NewJobCache(d, w, cache.GitRepoGetRevisionTimestamp(repos))
	if err != nil {
		return nil, fmt.Errorf("Failed to create JobCache: %s", err)
	}

	taskCfgCache, err := task_cfg_cache.NewTaskCfgCache(ctx, repos, btProject, btInstance, ts)
	if err != nil {
		return nil, fmt.Errorf("Failed to create TaskCfgCache: %s", err)
	}
	sc := syncer.New(ctx, repos, depotTools, workdir, syncer.DEFAULT_NUM_WORKERS)
	isolateCache, err := isolate_cache.New(ctx, btProject, btInstance, ts)
	if err != nil {
		return nil, fmt.Errorf("Failed to create isolate cache: %s", err)
	}
	chr := cacher.New(sc, taskCfgCache, isolateClient, isolateCache)

	tryjobs, err := tryjobs.NewTryJobIntegrator(buildbucketApiUrl, trybotBucket, host, c, d, jCache, projectRepoMapping, repos, taskCfgCache, chr, gerrit)
	if err != nil {
		return nil, fmt.Errorf("Failed to create TryJobIntegrator: %s", err)
	}
	s := &TaskScheduler{
		bl:                    bl,
		busyBots:              newBusyBots(),
		cacher:                chr,
		candidateMetrics:      map[string]metrics2.Int64Metric{},
		db:                    d,
		depotToolsDir:         depotTools,
		diagClient:            diagClient,
		diagInstance:          diagInstance,
		isolateCache:          isolateCache,
		isolateClient:         isolateClient,
		jCache:                jCache,
		newTasks:              map[types.RepoState]util.StringSet{},
		newTasksMtx:           sync.RWMutex{},
		pendingInsert:         map[string]bool{},
		pools:                 pools,
		pubsubCount:           metrics2.GetCounter("task_scheduler_pubsub_handler"),
		pubsubTopic:           pubsubTopic,
		queue:                 []*taskCandidate{},
		queueMtx:              sync.RWMutex{},
		repos:                 repos,
		swarming:              swarmingClient,
		syncer:                sc,
		taskCfgCache:          taskCfgCache,
		tCache:                tCache,
		timeDecayAmt24Hr:      timeDecayAmt24Hr,
		triggeredCount:        metrics2.GetCounter("task_scheduler_triggered_count"),
		tryjobs:               tryjobs,
		updateUnfinishedCount: metrics2.GetCounter("task_scheduler_update_unfinished_tasks_count"),
		window:                w,
		workdir:               workdir,
	}
	if err := s.initCaches(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Close cleans up resources used by the TaskScheduler.
func (s *TaskScheduler) Close() error {
	if err := s.syncer.Close(); err != nil {
		return err
	}
	if err := s.taskCfgCache.Close(); err != nil {
		return err
	}
	return s.isolateCache.Close()
}

// Start initiates the TaskScheduler's goroutines for scheduling tasks. beforeMainLoop
// will be run before each scheduling iteration.
func (s *TaskScheduler) Start(ctx context.Context, enableTryjobs bool, beforeMainLoop func()) {
	if enableTryjobs {
		s.tryjobs.Start(ctx)
	}
	lvScheduling := metrics2.NewLiveness("last_successful_task_scheduling")
	cleanup.Repeat(5*time.Second, func(_ context.Context) {
		// Explicitly ignore the passed-in context; this allows us to
		// finish the current scheduling cycle even if the context is
		// canceled, which helps prevent "orphaned" tasks which were
		// triggered on Swarming but were not inserted into the DB.
		ctx := context.Background()
		sklog.Infof("Running beforeMainLoop()")
		beforeMainLoop()
		sklog.Infof("beforeMainLoop() finished.")
		if err := s.MainLoop(ctx); err != nil {
			sklog.Errorf("Failed to run the task scheduler: %s", err)
		} else {
			lvScheduling.Reset()
		}
	}, nil)
	lvUpdateUnfinishedTasks := metrics2.NewLiveness("last_successful_tasks_update")
	go util.RepeatCtx(5*time.Minute, ctx, func(ctx context.Context) {
		if err := s.updateUnfinishedTasks(); err != nil {
			sklog.Errorf("Failed to run periodic tasks update: %s", err)
		} else {
			lvUpdateUnfinishedTasks.Reset()
		}
	})
	lvUpdateRepos := metrics2.NewLiveness("last_successful_repo_update")
	cleanup.Repeat(10*time.Second, func(_ context.Context) {
		// Explicitly ignore the passed-in context; this allows us to
		// continue running even if the context is canceled, which helps
		// to prevent partial insertions of new jobs.
		ctx := context.Background()
		if err := s.updateRepos(ctx); err != nil {
			sklog.Errorf("Failed to update repos: %s", err)
		} else {
			lvUpdateRepos.Reset()
		}
	}, nil)
}

// initCaches ensures that all of the RepoStates we care about are present
// in the various caches.
func (s *TaskScheduler) initCaches(ctx context.Context) error {
	defer metrics2.FuncTimer().Stop()

	sklog.Infof("Initializing caches...")
	defer sklog.Infof("Done initializing caches.")

	// Some existing jobs may not have been cached by Cacher already, eg.
	// because of poorly-timed process restarts. Go through the unfinished
	// jobs and cache them if necessary.
	if err := s.jCache.Update(); err != nil {
		return fmt.Errorf("Failed to update job cache: %s", err)
	}
	unfinishedJobs, err := s.jCache.UnfinishedJobs()
	if err != nil {
		return err
	}
	repoStatesToCache := map[types.RepoState]bool{}
	for _, job := range unfinishedJobs {
		repoStatesToCache[job.RepoState] = true
	}

	// Also cache the repo states for all commits in range.
	for repoUrl, repo := range s.repos {
		if err := s.recurseAllBranches(ctx, repoUrl, repo, func(_ string, _ *repograph.Graph, c *repograph.Commit) error {
			repoStatesToCache[types.RepoState{
				Repo:     repoUrl,
				Revision: c.Hash,
			}] = true
			return nil
		}); err != nil {
			return err
		}
	}

	// Actually cache the RepoStates.
	var g errgroup.Group
	for rs := range repoStatesToCache {
		rs := rs // https://golang.org/doc/faq#closures_and_goroutines
		g.Go(func() error {
			if _, err := s.cacher.GetOrCacheRepoState(ctx, rs); err != nil {
				return fmt.Errorf("Failed to cache RepoState: %s", err)
			}
			return nil
		})
	}
	return g.Wait()
}

// TaskSchedulerStatus is a struct which provides status information about the
// TaskScheduler.
type TaskSchedulerStatus struct {
	LastScheduled time.Time        `json:"last_scheduled"`
	TopCandidates []*taskCandidate `json:"top_candidates"`
}

// Status returns the current status of the TaskScheduler.
func (s *TaskScheduler) Status() *TaskSchedulerStatus {
	defer metrics2.FuncTimer().Stop()
	s.queueMtx.RLock()
	defer s.queueMtx.RUnlock()
	n := NUM_TOP_CANDIDATES
	if len(s.queue) < n {
		n = len(s.queue)
	}
	return &TaskSchedulerStatus{
		LastScheduled: s.lastScheduled,
		TopCandidates: s.queue[:n],
	}
}

// TaskCandidateSearchTerms includes fields used for searching task candidates.
type TaskCandidateSearchTerms struct {
	types.TaskKey
	Dimensions []string `json:"dimensions"`
}

// SearchQueue returns all task candidates in the queue which match the given
// TaskKey. Any blank fields are considered to be wildcards.
func (s *TaskScheduler) SearchQueue(q *TaskCandidateSearchTerms) []*taskCandidate {
	s.queueMtx.RLock()
	defer s.queueMtx.RUnlock()
	rv := []*taskCandidate{}
	for _, c := range s.queue {
		// TODO(borenet): I wish there was a better way to do this.
		if q.ForcedJobId != "" && c.ForcedJobId != q.ForcedJobId {
			continue
		}
		if q.Name != "" && c.Name != q.Name {
			continue
		}
		if q.Repo != "" && c.Repo != q.Repo {
			continue
		}
		if q.Revision != "" && c.Revision != q.Revision {
			continue
		}
		if q.Issue != "" && c.Issue != q.Issue {
			continue
		}
		if q.PatchRepo != "" && c.PatchRepo != q.PatchRepo {
			continue
		}
		if q.Patchset != "" && c.Patchset != q.Patchset {
			continue
		}
		if q.Server != "" && c.Server != q.Server {
			continue
		}
		if len(q.Dimensions) > 0 {
			ok := true
			for _, d := range q.Dimensions {
				if !util.In(d, c.TaskSpec.Dimensions) {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
		}
		rv = append(rv, c)
	}
	return rv
}

// MaybeTriggerPeriodicJobs triggers all periodic jobs with the given trigger
// name, if those jobs haven't already been triggered.
func (s *TaskScheduler) MaybeTriggerPeriodicJobs(ctx context.Context, triggerName string) error {
	// We'll search the jobs we've already triggered to ensure that we don't
	// trigger the same jobs multiple times in a day/week/whatever. Search a
	// window that is not quite the size of the trigger interval, to allow
	// for lag time.
	end := time.Now()
	var start time.Time
	if triggerName == specs.TRIGGER_NIGHTLY {
		start = end.Add(-23 * time.Hour)
	} else if triggerName == specs.TRIGGER_WEEKLY {
		// Note that if the cache window is less than a week, this start
		// time isn't going to work as expected. However, we only really
		// expect to need to debounce periodic triggers for a short
		// window, so anything longer than a few minutes would probably
		// be enough, and the ~4 days we normally keep in the cache
		// should be more than sufficient.
		start = end.Add(-6 * 24 * time.Hour)
	} else {
		sklog.Warningf("Ignoring unknown periodic trigger %q", triggerName)
		return nil
	}

	// Find the job specs matching the trigger and create Job instances.
	jobs := []*types.Job{}
	for repoUrl, repo := range s.repos {
		master := repo.Get("master")
		if master == nil {
			return fmt.Errorf("Failed to retrieve branch 'master' for %s", repoUrl)
		}
		rs := types.RepoState{
			Repo:     repoUrl,
			Revision: master.Hash,
		}
		cfg, err := s.taskCfgCache.Get(ctx, rs)
		if err != nil {
			return fmt.Errorf("Failed to retrieve TaskCfg from %s: %s", repoUrl, err)
		}
		for name, js := range cfg.Jobs {
			if js.Trigger == triggerName {
				job, err := s.taskCfgCache.MakeJob(ctx, rs, name)
				if err != nil {
					return fmt.Errorf("Failed to create job: %s", err)
				}
				jobs = append(jobs, job)
			}
		}
	}
	if len(jobs) == 0 {
		return nil
	}

	// Filter out any jobs which we've already triggered. Generally, we'd
	// expect to have triggered all of the jobs or none of them, but there
	// might be circumstances which caused us to trigger a partial set.
	names := make([]string, 0, len(jobs))
	for _, job := range jobs {
		names = append(names, job.Name)
	}
	existing, err := s.jCache.GetMatchingJobsFromDateRange(names, start, end)
	if err != nil {
		return err
	}
	jobsToInsert := make([]*types.Job, 0, len(jobs))
	for _, job := range jobs {
		var prev *types.Job = nil
		for _, existingJob := range existing[job.Name] {
			if !existingJob.IsTryJob() && !existingJob.IsForce {
				// Pick an arbitrary pre-existing job for logging.
				prev = existingJob
				break
			}
		}
		if prev == nil {
			jobsToInsert = append(jobsToInsert, job)
		} else {
			sklog.Warningf("Already triggered a job for %s (eg. id %s at %s); not triggering again.", job.Name, prev.Id, prev.Created)
		}
	}
	if len(jobsToInsert) == 0 {
		return nil
	}

	// Insert the new jobs into the DB.
	if err := s.db.PutJobsInChunks(jobsToInsert); err != nil {
		return fmt.Errorf("Failed to add periodic jobs: %s", err)
	}
	sklog.Infof("Created %d periodic jobs for trigger %q", len(jobs), triggerName)
	return nil
}

// TriggerJob adds the given Job to the database and returns its ID.
func (s *TaskScheduler) TriggerJob(ctx context.Context, repo, commit, jobName string) (string, error) {
	j, err := s.taskCfgCache.MakeJob(ctx, types.RepoState{
		Repo:     repo,
		Revision: commit,
	}, jobName)
	if err != nil {
		return "", err
	}
	j.IsForce = true
	if err := s.db.PutJob(j); err != nil {
		return "", err
	}
	sklog.Infof("Created manually-triggered Job %q", j.Id)
	return j.Id, nil
}

// CancelJob cancels the given Job if it is not already finished.
func (s *TaskScheduler) CancelJob(id string) (*types.Job, error) {
	// TODO(borenet): Prevent concurrent update of the Job.
	j, err := s.jCache.GetJobMaybeExpired(id)
	if err != nil {
		return nil, err
	}
	if j.Done() {
		return nil, fmt.Errorf("Job %s is already finished with status %s", id, j.Status)
	}
	j.Status = types.JOB_STATUS_CANCELED
	s.jobFinished(j)
	if err := s.db.PutJob(j); err != nil {
		return nil, err
	}
	return j, s.jCache.Update()
}

// ComputeBlamelist computes the blamelist for a new task, specified by name,
// repo, and revision. Returns the list of commits covered by the task, and any
// previous task which part or all of the blamelist was "stolen" from (see
// below). There are three cases:
//
// 1. The new task tests commits which have not yet been tested. Trace commit
//    history, accumulating commits until we find commits which have been tested
//    by previous tasks.
//
// 2. The new task runs at the same commit as a previous task. This is a retry,
//    so the entire blamelist of the previous task is "stolen".
//
// 3. The new task runs at a commit which is in a previous task's blamelist, but
//    no task has run at the same commit. This is a bisect. Trace commit
//    history, "stealing" commits from the previous task until we find a commit
//    which was covered by a *different* previous task.
//
// Args:
//   - cache:      TaskCache instance.
//   - repo:       repograph.Graph instance corresponding to the repository of the task.
//   - taskName:   Name of the task.
//   - repoName:   Name of the repository for the task.
//   - revision:   Revision at which the task would run.
//   - commitsBuf: Buffer for use as scratch space.
func ComputeBlamelist(ctx context.Context, cache cache.TaskCache, repo *repograph.Graph, taskName, repoName string, revision *repograph.Commit, commitsBuf []*repograph.Commit, newTasks map[types.RepoState]util.StringSet) ([]string, *types.Task, error) {
	commitsBuf = commitsBuf[:0]
	var stealFrom *types.Task

	// Run the helper function to recurse on commit history.
	if err := revision.Recurse(func(commit *repograph.Commit) error {
		// Determine whether any task already includes this commit.
		prev, err := cache.GetTaskForCommit(repoName, commit.Hash, taskName)
		if err != nil {
			return err
		}

		// If the blamelist is too large, just use a single commit.
		if len(commitsBuf) > MAX_BLAMELIST_COMMITS {
			commitsBuf = append(commitsBuf[:0], revision)
			//sklog.Warningf("Found too many commits for %s @ %s; using single-commit blamelist.", taskName, revision.Hash)
			return ERR_BLAMELIST_DONE
		}

		// If we're stealing commits from a previous task but the current
		// commit is not in any task's blamelist, we must have scrolled past
		// the beginning of the tasks. Just return.
		if prev == nil && stealFrom != nil {
			return repograph.ErrStopRecursing
		}

		// If a previous task already included this commit, we have to make a decision.
		if prev != nil {
			// If this Task's Revision is already included in a different
			// Task, then we're either bisecting or retrying a task. We'll
			// "steal" commits from the previous Task's blamelist.
			if len(commitsBuf) == 0 {
				stealFrom = prev

				// Another shortcut: If our Revision is the same as the
				// Revision of the Task we're stealing commits from,
				// ie. both tasks ran at the same commit, then this is a
				// retry. Just steal all of the commits without doing
				// any more work.
				if stealFrom.Revision == revision.Hash {
					commitsBuf = commitsBuf[:0]
					for _, c := range stealFrom.Commits {
						ptr := repo.Get(c)
						if ptr == nil {
							return fmt.Errorf("No such commit: %q", c)
						}
						commitsBuf = append(commitsBuf, ptr)
					}
					return ERR_BLAMELIST_DONE
				}
			}
			if stealFrom == nil || prev.Id != stealFrom.Id {
				// If we've hit a commit belonging to a different task,
				// we're done.
				return repograph.ErrStopRecursing
			}
		}

		// Add the commit.
		commitsBuf = append(commitsBuf, commit)

		// If the task is new at this commit, stop now.
		rs := types.RepoState{
			Repo:     repoName,
			Revision: commit.Hash,
		}
		if newTasks[rs][taskName] {
			sklog.Infof("Task Spec %s was added in %s; stopping blamelist calculation.", taskName, commit.Hash)
			return repograph.ErrStopRecursing
		}

		// Recurse on the commit's parents.
		return nil

	}); err != nil && err != ERR_BLAMELIST_DONE {
		return nil, nil, err
	}

	rv := make([]string, 0, len(commitsBuf))
	for _, c := range commitsBuf {
		rv = append(rv, c.Hash)
	}
	return rv, stealFrom, nil
}

type taskSchedulerMainLoopDiagnostics struct {
	StartTime  time.Time                           `json:"startTime"`
	EndTime    time.Time                           `json:"endTime"`
	Error      string                              `json:"error,omitEmpty"`
	Candidates []*taskCandidate                    `json:"candidates"`
	FreeBots   []*swarming_api.SwarmingRpcsBotInfo `json:"freeBots"`
}

// writeMainLoopDiagnosticsToGCS writes JSON containing allCandidates and
// freeBots to GCS. If called in a goroutine, the arguments may not be modified.
func writeMainLoopDiagnosticsToGCS(ctx context.Context, start time.Time, end time.Time, diagClient gcs.GCSClient, diagInstance string, allCandidates map[types.TaskKey]*taskCandidate, freeBots []*swarming_api.SwarmingRpcsBotInfo, scheduleErr error) error {
	defer metrics2.FuncTimer().Stop()
	candidateSlice := make([]*taskCandidate, 0, len(allCandidates))
	for _, c := range allCandidates {
		candidateSlice = append(candidateSlice, c)
	}
	sort.Sort(taskCandidateSlice(candidateSlice))
	content := taskSchedulerMainLoopDiagnostics{
		StartTime:  start.UTC(),
		EndTime:    end.UTC(),
		Candidates: candidateSlice,
		FreeBots:   freeBots,
	}
	if scheduleErr != nil {
		content.Error = scheduleErr.Error()
	}
	filenameBase := start.UTC().Format("20060102T150405.000000000Z")
	path := path.Join(diagInstance, GCS_MAIN_LOOP_DIAGNOSTICS_DIR, filenameBase+".json")
	ctx, cancel := context.WithTimeout(ctx, GCS_DIAGNOSTICS_WRITE_TIMEOUT)
	defer cancel()
	return gcs.WithWriteFileGzip(diagClient, ctx, path, func(w io.Writer) error {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(&content)
	})
}

// findTaskCandidatesForJobs returns the set of all taskCandidates needed by all
// currently-unfinished jobs.
func (s *TaskScheduler) findTaskCandidatesForJobs(ctx context.Context, unfinishedJobs []*types.Job) (map[types.TaskKey]*taskCandidate, error) {
	defer metrics2.FuncTimer().Stop()

	// Get the repo+commit+taskspecs for each job.
	candidates := map[types.TaskKey]*taskCandidate{}
	for _, j := range unfinishedJobs {
		if !s.window.TestTime(j.Repo, j.Created) {
			continue
		}

		// If git history was changed, we should avoid running jobs at
		// orphaned commits.
		if s.repos[j.Repo].Get(j.Revision) == nil {
			continue
		}

		// Add task candidates for this job.
		for tsName := range j.Dependencies {
			key := j.MakeTaskKey(tsName)
			c, ok := candidates[key]
			if !ok {
				spec, err := s.taskCfgCache.GetTaskSpec(ctx, j.RepoState, tsName)
				if err != nil {
					// Log an error but don't block scheduling due to transient errors.
					sklog.Errorf("Failed to obtain task spec: %s", err)
					// TODO(borenet): We should check specs.ErrorIsPermanent and
					// consider canceling the job since we can never fulfill it.
					continue
				}
				c = &taskCandidate{
					// NB: Because multiple Jobs may share a Task, the BuildbucketBuildId
					// could be inherited from any matching Job. Therefore, this should be
					// used for non-critical, informational purposes only.
					BuildbucketBuildId: j.BuildbucketBuildId,
					Jobs:               nil,
					TaskKey:            key,
					TaskSpec:           spec,
				}
				candidates[key] = c
			}
			c.AddJob(j)
		}
	}
	sklog.Infof("Found %d task candidates for %d unfinished jobs.", len(candidates), len(unfinishedJobs))
	return candidates, nil
}

// filterTaskCandidates reduces the set of taskCandidates to the ones we might
// actually want to run and organizes them by repo and TaskSpec name.
func (s *TaskScheduler) filterTaskCandidates(preFilterCandidates map[types.TaskKey]*taskCandidate) (map[string]map[string][]*taskCandidate, error) {
	defer metrics2.FuncTimer().Stop()

	candidatesBySpec := map[string]map[string][]*taskCandidate{}
	total := 0
	skippedByBlacklist := map[string]int{}
	for _, c := range preFilterCandidates {
		// Reject blacklisted tasks.
		if rule := s.bl.MatchRule(c.Name, c.Revision); rule != "" {
			skippedByBlacklist[rule]++
			c.GetDiagnostics().Filtering = &taskCandidateFilteringDiagnostics{BlacklistedByRule: rule}
			continue
		}

		// Reject tasks for too-old commits, as long as they aren't try jobs.
		if !c.IsTryJob() {
			if in, err := s.window.TestCommitHash(c.Repo, c.Revision); err != nil {
				return nil, err
			} else if !in {
				c.GetDiagnostics().Filtering = &taskCandidateFilteringDiagnostics{RevisionTooOld: true}
				continue
			}
		}
		// We shouldn't duplicate pending, in-progress,
		// or successfully completed tasks.
		prevTasks, err := s.tCache.GetTasksByKey(&c.TaskKey)
		if err != nil {
			return nil, err
		}
		var previous *types.Task
		if len(prevTasks) > 0 {
			// Just choose the last (most recently created) previous
			// Task.
			previous = prevTasks[len(prevTasks)-1]
		}
		if previous != nil {
			if previous.Status == types.TASK_STATUS_PENDING || previous.Status == types.TASK_STATUS_RUNNING {
				c.GetDiagnostics().Filtering = &taskCandidateFilteringDiagnostics{SupersededByTask: previous.Id}
				continue
			}
			if previous.Success() {
				c.GetDiagnostics().Filtering = &taskCandidateFilteringDiagnostics{SupersededByTask: previous.Id}
				continue
			}
			// The attempt counts are only valid if the previous
			// attempt we're looking at is the last attempt for this
			// TaskSpec. Fortunately, TaskCache.GetTasksByKey sorts
			// by creation time, and we've selected the last of the
			// results.
			maxAttempts := c.TaskSpec.MaxAttempts
			if maxAttempts == 0 {
				maxAttempts = specs.DEFAULT_TASK_SPEC_MAX_ATTEMPTS
			}
			// Special case for tasks created before arbitrary
			// numbers of attempts were possible.
			previousAttempt := previous.Attempt
			if previousAttempt == 0 && previous.RetryOf != "" {
				previousAttempt = 1
			}
			if previousAttempt >= maxAttempts-1 {
				previousIds := make([]string, 0, len(prevTasks))
				for _, t := range prevTasks {
					previousIds = append(previousIds, t.Id)
				}
				c.GetDiagnostics().Filtering = &taskCandidateFilteringDiagnostics{PreviousAttempts: previousIds}
				continue
			}
			c.Attempt = previousAttempt + 1
			c.RetryOf = previous.Id
		}

		// Don't consider candidates whose dependencies are not met.
		depsMet, idsToHashes, err := c.allDepsMet(s.tCache)
		if err != nil {
			return nil, err
		}
		if !depsMet {
			// c.Filtering set in allDepsMet.
			continue
		}
		hashes := make([]string, 0, len(idsToHashes))
		parentTaskIds := make([]string, 0, len(idsToHashes))
		for id, hash := range idsToHashes {
			hashes = append(hashes, hash)
			parentTaskIds = append(parentTaskIds, id)
		}
		c.IsolatedHashes = hashes
		sort.Strings(parentTaskIds)
		c.ParentTaskIds = parentTaskIds

		candidates, ok := candidatesBySpec[c.Repo]
		if !ok {
			candidates = map[string][]*taskCandidate{}
			candidatesBySpec[c.Repo] = candidates
		}
		candidates[c.Name] = append(candidates[c.Name], c)
		total++
	}
	for rule, numSkipped := range skippedByBlacklist {
		diagLink := fmt.Sprintf("https://console.cloud.google.com/storage/browser/skia-task-scheduler-diagnostics/%s?project=google.com:skia-corp", path.Join(s.diagInstance, GCS_MAIN_LOOP_DIAGNOSTICS_DIR))
		sklog.Infof("Skipped %d candidates due to blacklist rule %q. See details in diagnostics at %s.", numSkipped, rule, diagLink)
	}
	sklog.Infof("Filtered to %d candidates in %d spec categories.", total, len(candidatesBySpec))
	return candidatesBySpec, nil
}

// processTaskCandidate computes the remaining information about the task
// candidate, eg. blamelists and scoring.
func (s *TaskScheduler) processTaskCandidate(ctx context.Context, c *taskCandidate, now time.Time, cache *cacheWrapper, commitsBuf []*repograph.Commit, diag *taskCandidateScoringDiagnostics) error {
	if len(c.Jobs) == 0 {
		// Log an error and return to allow scheduling other tasks.
		sklog.Errorf("taskCandidate has no Jobs: %#v", c)
		c.Score = 0
		return nil
	}

	// Formula for priority is 1 - (1-<job1 priority>)(1-<job2 priority>)...(1-<jobN priority>).
	inversePriorityProduct := 1.0
	for _, j := range c.Jobs {
		jobPriority := specs.DEFAULT_JOB_SPEC_PRIORITY
		if j.Priority <= 1 && j.Priority > 0 {
			jobPriority = j.Priority
		}
		inversePriorityProduct *= 1 - jobPriority
	}
	priority := 1 - inversePriorityProduct
	diag.Priority = priority

	// Use the earliest Job's Created time, which will maximize priority for older forced/try jobs.
	earliestJob := c.Jobs[0]
	diag.JobCreatedHours = now.Sub(earliestJob.Created).Hours()

	if c.IsTryJob() {
		c.Score = CANDIDATE_SCORE_TRY_JOB + now.Sub(earliestJob.Created).Hours()
		// Proritize each subsequent attempt lower than the previous attempt.
		for i := 0; i < c.Attempt; i++ {
			c.Score *= CANDIDATE_SCORE_TRY_JOB_RETRY_MULTIPLIER
		}
		c.Score *= priority
		return nil
	}

	// Compute blamelist.
	repo, ok := s.repos[c.Repo]
	if !ok {
		return fmt.Errorf("No such repo: %s", c.Repo)
	}
	revision := repo.Get(c.Revision)
	if revision == nil {
		return fmt.Errorf("No such commit %s in %s.", c.Revision, c.Repo)
	}
	var stealingFrom *types.Task
	var commits []string
	if !s.window.TestTime(c.Repo, revision.Timestamp) {
		// If the commit has scrolled out of our window, don't bother computing
		// a blamelist.
		commits = []string{}
	} else {
		var err error
		commits, stealingFrom, err = ComputeBlamelist(ctx, cache, repo, c.Name, c.Repo, revision, commitsBuf, s.newTasks)
		if err != nil {
			return err
		}
	}
	c.Commits = commits
	if stealingFrom != nil {
		c.StealingFromId = stealingFrom.Id
	}
	if len(c.Commits) > 0 && !util.In(c.Revision, c.Commits) {
		sklog.Errorf("task candidate %s @ %s doesn't have its own revision in its blamelist: %v", c.Name, c.Revision, c.Commits)
	}

	if c.IsForceRun() {
		c.Score = CANDIDATE_SCORE_FORCE_RUN + now.Sub(earliestJob.Created).Hours()
		c.Score *= priority
		return nil
	}

	// Score the candidate.
	// The score for a candidate is based on the "testedness" increase
	// provided by running the task.
	stoleFromCommits := 0
	stoleFromStatus := types.TASK_STATUS_SUCCESS
	if stealingFrom != nil {
		stoleFromCommits = len(stealingFrom.Commits)
		stoleFromStatus = stealingFrom.Status
	}
	diag.StoleFromCommits = stoleFromCommits
	score := testednessIncrease(len(c.Commits), stoleFromCommits)
	diag.TestednessIncrease = score

	// Add a bonus when retrying or backfilling failures and mishaps.
	if stoleFromStatus == types.TASK_STATUS_FAILURE || stoleFromStatus == types.TASK_STATUS_MISHAP {
		score += CANDIDATE_SCORE_FAILURE_OR_MISHAP_BONUS
	}

	// Scale the score by other factors, eg. time decay.
	decay, err := s.timeDecayForCommit(now, revision)
	if err != nil {
		return err
	}
	diag.TimeDecay = decay
	score *= decay
	score *= priority

	c.Score = score
	return nil
}

// Process the task candidates.
func (s *TaskScheduler) processTaskCandidates(ctx context.Context, candidates map[string]map[string][]*taskCandidate, now time.Time) ([]*taskCandidate, error) {
	defer metrics2.FuncTimer().Stop()

	// Get newly-added task specs by repo state.
	if err := s.updateAddedTaskSpecs(ctx); err != nil {
		return nil, err
	}

	s.newTasksMtx.RLock()
	defer s.newTasksMtx.RUnlock()

	processed := make(chan *taskCandidate)
	errs := make(chan error)
	wg := sync.WaitGroup{}
	for _, cs := range candidates {
		for _, c := range cs {
			wg.Add(1)
			go func(candidates []*taskCandidate) {
				defer wg.Done()
				cache := newCacheWrapper(s.tCache)
				commitsBuf := make([]*repograph.Commit, 0, MAX_BLAMELIST_COMMITS)
				for {
					// Find the best candidate.
					idx := -1
					var orig *taskCandidate
					var best *taskCandidate
					var bestDiag *taskCandidateScoringDiagnostics
					for i, candidate := range candidates {
						c := candidate.CopyNoDiagnostics()
						diag := &taskCandidateScoringDiagnostics{}
						if err := s.processTaskCandidate(ctx, c, now, cache, commitsBuf, diag); err != nil {
							errs <- err
							return
						}
						if best == nil || c.Score > best.Score {
							orig = candidate
							best = c
							bestDiag = diag
							idx = i
						}
					}
					if best == nil {
						return
					}
					// Use the original candidate since we're holding on to that pointer for diagnostics.
					*orig = *best
					best = orig
					best.GetDiagnostics().Scoring = bestDiag

					processed <- best
					t := best.MakeTask()
					t.Id = best.MakeId()
					cache.insert(t)
					if best.StealingFromId != "" {
						stoleFrom, err := cache.GetTask(best.StealingFromId)
						if err != nil {
							errs <- err
							return
						}
						stole := util.NewStringSet(best.Commits)
						oldC := util.NewStringSet(stoleFrom.Commits)
						newC := oldC.Complement(stole)
						commits := make([]string, 0, len(newC))
						for c := range newC {
							commits = append(commits, c)
						}
						stoleFrom.Commits = commits
						cache.insert(stoleFrom)
					}
					candidates = append(candidates[:idx], candidates[idx+1:]...)
				}
			}(c)
		}
	}
	go func() {
		wg.Wait()
		close(processed)
		close(errs)
	}()
	rvCandidates := []*taskCandidate{}
	rvErrs := []error{}
	for {
		select {
		case c, ok := <-processed:
			if ok {
				rvCandidates = append(rvCandidates, c)
			} else {
				processed = nil
			}
		case err, ok := <-errs:
			if ok {
				rvErrs = append(rvErrs, err)
			} else {
				errs = nil
			}
		}
		if processed == nil && errs == nil {
			break
		}
	}

	if len(rvErrs) != 0 {
		return nil, rvErrs[0]
	}
	sort.Sort(taskCandidateSlice(rvCandidates))
	return rvCandidates, nil
}

// flatten all the dimensions in 'dims' into a single valued map.
func flatten(dims map[string]string) map[string]string {
	keys := make([]string, 0, len(dims))
	for key := range dims {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ret := make([]string, 0, 2*len(dims))
	for _, key := range keys {
		ret = append(ret, key, dims[key])
	}
	return map[string]string{"dimensions": strings.Join(ret, " ")}
}

// recordCandidateMetrics generates metrics for candidates by dimension sets.
func (s *TaskScheduler) recordCandidateMetrics(candidates map[string]map[string][]*taskCandidate) {
	defer metrics2.FuncTimer().Stop()

	// Generate counts. These maps are keyed by the MD5 hash of the
	// candidate's TaskSpec's dimensions.
	counts := map[string]int64{}
	dimensions := map[string]map[string]string{}
	for _, byRepo := range candidates {
		for _, bySpec := range byRepo {
			for _, c := range bySpec {
				parseDims, err := swarming.ParseDimensions(c.TaskSpec.Dimensions)
				if err != nil {
					sklog.Errorf("Failed to parse dimensions: %s", err)
					continue
				}
				dims := make(map[string]string, len(parseDims))
				for k, v := range parseDims {
					// Just take the first value for each dimension.
					dims[k] = v[0]
				}
				k, err := util.MD5Sum(dims)
				if err != nil {
					sklog.Errorf("Failed to create metrics key: %s", err)
					continue
				}
				dimensions[k] = dims
				counts[k]++
			}
		}
	}
	// Report the data.
	s.candidateMetricsMtx.Lock()
	defer s.candidateMetricsMtx.Unlock()
	for k, count := range counts {
		metric, ok := s.candidateMetrics[k]
		if !ok {
			metric = metrics2.GetInt64Metric(MEASUREMENT_TASK_CANDIDATE_COUNT, flatten(dimensions[k]))
			s.candidateMetrics[k] = metric
		}
		metric.Update(count)
	}
	for k, metric := range s.candidateMetrics {
		_, ok := counts[k]
		if !ok {
			metric.Update(0)
			delete(s.candidateMetrics, k)
		}
	}
}

// regenerateTaskQueue obtains the set of all eligible task candidates, scores
// them, and prepares them to be triggered. The second return value contains
// all candidates.
func (s *TaskScheduler) regenerateTaskQueue(ctx context.Context, now time.Time) ([]*taskCandidate, map[types.TaskKey]*taskCandidate, error) {
	defer metrics2.FuncTimer().Stop()

	// Find the unfinished Jobs.
	unfinishedJobs, err := s.jCache.UnfinishedJobs()
	if err != nil {
		return nil, nil, err
	}

	// Find TaskSpecs for all unfinished Jobs.
	preFilterCandidates, err := s.findTaskCandidatesForJobs(ctx, unfinishedJobs)
	if err != nil {
		return nil, nil, err
	}

	// Filter task candidates.
	candidates, err := s.filterTaskCandidates(preFilterCandidates)
	if err != nil {
		return nil, nil, err
	}

	// Record the number of task candidates per dimension set.
	s.recordCandidateMetrics(candidates)

	// Process the remaining task candidates.
	queue, err := s.processTaskCandidates(ctx, candidates, now)
	if err != nil {
		return nil, nil, err
	}

	return queue, preFilterCandidates, nil
}

// getCandidatesToSchedule matches the list of free Swarming bots to task
// candidates in the queue and returns the candidates which should be run.
// Assumes that the tasks are sorted in decreasing order by score.
func getCandidatesToSchedule(bots []*swarming_api.SwarmingRpcsBotInfo, tasks []*taskCandidate) []*taskCandidate {
	defer metrics2.FuncTimer().Stop()
	// Create a bots-by-swarming-dimension mapping.
	botsByDim := map[string]util.StringSet{}
	for _, b := range bots {
		for _, dim := range b.Dimensions {
			for _, val := range dim.Value {
				d := fmt.Sprintf("%s:%s", dim.Key, val)
				if _, ok := botsByDim[d]; !ok {
					botsByDim[d] = util.StringSet{}
				}
				botsByDim[d][b.BotId] = true
			}
		}
	}
	// BotIds that have been used by previous candidates.
	usedBots := util.StringSet{}
	// Map BotId to the candidates that could have used that bot. In the
	// case that no bots are available for a candidate, map concatenated
	// dimensions to candidates.
	botToCandidates := map[string][]*taskCandidate{}

	// Match bots to tasks.
	// TODO(borenet): Some tasks require a more specialized bot. We should
	// match so that less-specialized tasks don't "steal" more-specialized
	// bots which they don't actually need.
	rv := make([]*taskCandidate, 0, len(bots))
	countByTaskSpec := make(map[string]int, len(bots))
	for _, c := range tasks {
		diag := &taskCandidateSchedulingDiagnostics{}
		c.GetDiagnostics().Scheduling = diag
		// Don't exceed SCHEDULING_LIMIT_PER_TASK_SPEC.
		if countByTaskSpec[c.Name] == SCHEDULING_LIMIT_PER_TASK_SPEC {
			sklog.Warningf("Too many tasks to schedule for %s; not scheduling more than %d", c.Name, SCHEDULING_LIMIT_PER_TASK_SPEC)
			diag.OverSchedulingLimitPerTaskSpec = true
			continue
		}
		// TODO(borenet): Make this threshold configurable.
		if c.Score <= 0.0 {
			sklog.Warningf("candidate %s @ %s has a score of %2f; skipping (%d commits).", c.Name, c.Revision, c.Score, len(c.Commits))
			diag.ScoreBelowThreshold = true
			continue
		}

		// For each dimension of the task, find the set of bots which matches.
		matches := util.StringSet{}
		for i, d := range c.TaskSpec.Dimensions {
			if i == 0 {
				matches = matches.Union(botsByDim[d])
			} else {
				matches = matches.Intersect(botsByDim[d])
			}
		}

		// Set of candidates that could have used the same bots.
		similarCandidates := map[*taskCandidate]struct{}{}
		var lowestScoreSimilarCandidate *taskCandidate
		addCandidates := func(key string) {
			candidates := botToCandidates[key]
			for _, candidate := range candidates {
				similarCandidates[candidate] = struct{}{}
			}
			if len(candidates) > 0 {
				lastCandidate := candidates[len(candidates)-1]
				if lowestScoreSimilarCandidate == nil || lowestScoreSimilarCandidate.Score > lastCandidate.Score {
					lowestScoreSimilarCandidate = lastCandidate
				}
			}
			botToCandidates[key] = append(candidates, c)
		}

		// Choose a particular bot to mark as used. Sort by ID so that the choice is deterministic.
		var chosenBot string
		if len(matches) > 0 {
			diag.MatchingBots = matches.Keys()
			sort.Strings(diag.MatchingBots)
			for botId := range matches {
				if (chosenBot == "" || botId < chosenBot) && !usedBots[botId] {
					chosenBot = botId
				}
				addCandidates(botId)
			}
		} else {
			diag.NoBotsAvailable = true
			diag.MatchingBots = nil
			// Use sorted concatenated dimensions instead of botId as the key.
			dims := util.CopyStringSlice(c.TaskSpec.Dimensions)
			sort.Strings(dims)
			addCandidates(strings.Join(dims, ","))
		}
		diag.NumHigherScoreSimilarCandidates = len(similarCandidates)
		if lowestScoreSimilarCandidate != nil {
			diag.LastSimilarCandidate = &lowestScoreSimilarCandidate.TaskKey
		}

		if chosenBot != "" {
			// We're going to run this task.
			diag.Selected = true
			usedBots[chosenBot] = true

			// Add the task to the scheduling list.
			rv = append(rv, c)
			countByTaskSpec[c.Name]++
		}
	}
	sort.Sort(taskCandidateSlice(rv))
	return rv
}

// isolateCandidates uploads inputs for the taskCandidates to the Isolate
// server. Returns the list of candidates which were successfully isolated,
// with their IsolatedInput hash set, and any error which occurred. Note that
// the successful candidates AND an error may both be returned if some were
// successfully isolated but others failed.
func (s *TaskScheduler) isolateCandidates(ctx context.Context, candidates []*taskCandidate) ([]*taskCandidate, error) {
	defer metrics2.FuncTimer().Stop()

	isolatedCandidates := make([]*taskCandidate, 0, len(candidates))
	isolatedFiles := make([]*isolated.Isolated, 0, len(candidates))
	var errs *multierror.Error
	for _, c := range candidates {
		isolatedFile, err := s.isolateCache.Get(ctx, c.RepoState, c.TaskSpec.Isolate)
		if err != nil {
			errStr := err.Error()
			c.GetDiagnostics().Triggering = &taskCandidateTriggeringDiagnostics{IsolateError: errStr}
			errs = multierror.Append(errs, fmt.Errorf("Failed to obtain cached isolate: %s", errStr))
			continue
		}
		for _, inc := range c.IsolatedHashes {
			isolatedFile.Includes = append(isolatedFile.Includes, isolated.HexDigest(inc))
		}
		isolatedFiles = append(isolatedFiles, isolatedFile)
		isolatedCandidates = append(isolatedCandidates, c)
	}
	hashes, err := s.isolateClient.ReUploadIsolatedFiles(ctx, isolatedFiles)
	if err != nil {
		return nil, fmt.Errorf("Failed to re-upload Isolated files: %s", err)
	}
	if len(hashes) != len(isolatedFiles) {
		return nil, fmt.Errorf("ReUploadIsolatedFiles returned incorrect number of hashes (%d but wanted %d)", len(hashes), len(isolatedFiles))
	}
	for idx, c := range isolatedCandidates {
		c.IsolatedInput = hashes[idx]
	}

	return isolatedCandidates, errs.ErrorOrNil()
}

// triggerTasks triggers the given slice of tasks to run on Swarming and returns
// a channel of the successfully-triggered tasks which is closed after all tasks
// have been triggered or failed. Each failure is sent to errCh.
func (s *TaskScheduler) triggerTasks(candidates []*taskCandidate, errCh chan<- error) <-chan *types.Task {
	defer metrics2.FuncTimer().Stop()
	triggered := make(chan *types.Task)
	var wg sync.WaitGroup
	for _, candidate := range candidates {
		wg.Add(1)
		go func(candidate *taskCandidate) {
			defer wg.Done()
			t := candidate.MakeTask()
			diag := &taskCandidateTriggeringDiagnostics{}
			candidate.GetDiagnostics().Triggering = diag
			recordErr := func(context string, err error) {
				err = fmt.Errorf("%s: %s", context, err)
				diag.TriggerError = err.Error()
				errCh <- err
			}
			if err := s.db.AssignId(t); err != nil {
				recordErr("Failed to assign id", err)
				return
			}
			diag.TaskId = t.Id
			req, err := candidate.MakeTaskRequest(t.Id, s.isolateClient.ServerURL(), s.pubsubTopic)
			if err != nil {
				recordErr("Failed to make task request", err)
				return
			}
			s.pendingInsertMtx.Lock()
			s.pendingInsert[t.Id] = true
			s.pendingInsertMtx.Unlock()
			var resp *swarming_api.SwarmingRpcsTaskRequestMetadata
			if err := timeout.Run(func() error {
				var err error
				resp, err = s.swarming.TriggerTask(req)
				return err
			}, time.Minute); err != nil {
				s.pendingInsertMtx.Lock()
				delete(s.pendingInsert, t.Id)
				s.pendingInsertMtx.Unlock()
				recordErr("Failed to trigger task", err)
				return
			}
			created, err := swarming.ParseTimestamp(resp.Request.CreatedTs)
			if err != nil {
				recordErr("Failed to parse Swarming timestamp", err)
				return
			}
			t.Created = created
			t.SwarmingTaskId = resp.TaskId
			// The task may have been de-duplicated.
			if resp.TaskResult != nil && resp.TaskResult.State == swarming.TASK_STATE_COMPLETED {
				if _, err := t.UpdateFromSwarming(resp.TaskResult); err != nil {
					recordErr("Failed to update de-duplicated task", err)
					return
				}
				// Override the timestamps. For de-duplicated
				// tasks, the Started and Finished timestamps
				// will be *before* the Created timestamp, which
				// would be misleading. Setting them to equal
				// the Created timestamp makes it appear that
				// the task took no time, which is effectively
				// the case.
				t.Started = t.Created
				t.Finished = t.Created
			}
			triggered <- t
		}(candidate)
	}
	go func() {
		wg.Wait()
		close(triggered)
	}()
	return triggered
}

// scheduleTasks queries for free Swarming bots and triggers tasks according
// to relative priorities in the queue.
func (s *TaskScheduler) scheduleTasks(ctx context.Context, bots []*swarming_api.SwarmingRpcsBotInfo, queue []*taskCandidate) error {
	defer metrics2.FuncTimer().Stop()
	// Match free bots with tasks.
	candidates := getCandidatesToSchedule(bots, queue)

	// Isolate the tasks.
	isolated, isolateErr := s.isolateCandidates(ctx, candidates)
	if isolateErr != nil && len(isolated) == 0 {
		return isolateErr
	}

	// Setup the error channel.
	errs := []error{}
	if isolateErr != nil {
		errs = append(errs, isolateErr)
	}
	errCh := make(chan error)
	var errWg sync.WaitGroup
	errWg.Add(1)
	go func() {
		defer errWg.Done()
		for err := range errCh {
			errs = append(errs, err)
		}
	}()

	// Trigger Swarming tasks.
	triggered := s.triggerTasks(isolated, errCh)

	// Collect the tasks we triggered.
	numTriggered := 0
	insert := map[string]map[string][]*types.Task{}
	for t := range triggered {
		byRepo, ok := insert[t.Repo]
		if !ok {
			byRepo = map[string][]*types.Task{}
			insert[t.Repo] = byRepo
		}
		byRepo[t.Name] = append(byRepo[t.Name], t)
		numTriggered++
	}
	close(errCh)
	errWg.Wait()

	if len(insert) > 0 {
		// Insert the newly-triggered tasks into the DB.
		err := s.addTasks(ctx, insert)

		// Remove the tasks from the pending map, regardless of whether
		// we successfully inserted into the DB.
		s.pendingInsertMtx.Lock()
		for _, byRepo := range insert {
			for _, byName := range byRepo {
				for _, t := range byName {
					delete(s.pendingInsert, t.Id)
				}
			}
		}
		s.pendingInsertMtx.Unlock()

		// Handle failure/success.
		if err != nil {
			errs = append(errs, fmt.Errorf("Triggered tasks but failed to insert into DB: %s", err))
		} else {
			// Organize the triggered task by TaskKey.
			remove := make(map[types.TaskKey]*types.Task, numTriggered)
			for _, byRepo := range insert {
				for _, byName := range byRepo {
					for _, t := range byName {
						remove[t.TaskKey] = t
					}
				}
			}
			if len(remove) != numTriggered {
				return fmt.Errorf("Number of tasks to remove from the queue (%d) differs from the number of tasks triggered (%d)", len(remove), numTriggered)
			}

			// Remove the tasks from the queue.
			newQueue := make([]*taskCandidate, 0, len(queue)-numTriggered)
			for _, c := range queue {
				if _, ok := remove[c.TaskKey]; !ok {
					newQueue = append(newQueue, c)
				}
			}

			// Note; if regenerateQueue and scheduleTasks are ever decoupled so that
			// the queue is reused by multiple runs of scheduleTasks, we'll need to
			// address the fact that some candidates may still have their
			// StoleFromId pointing to candidates which have been triggered and
			// removed from the queue. In that case, we should just need to write a
			// loop which updates those candidates to use the IDs of the newly-
			// inserted Tasks in the database rather than the candidate ID.

			sklog.Infof("Triggered %d tasks on %d bots (%d in queue).", numTriggered, len(bots), len(queue))
			queue = newQueue
		}
	} else {
		sklog.Infof("Triggered no tasks (%d in queue, %d bots available)", len(queue), len(bots))
	}
	s.triggeredCount.Inc(int64(numTriggered))
	s.queueMtx.Lock()
	defer s.queueMtx.Unlock()
	s.queue = queue
	s.lastScheduled = time.Now()

	if len(errs) > 0 {
		rvErr := "Got failures: "
		for _, e := range errs {
			rvErr += fmt.Sprintf("\n%s\n", e)
		}
		return fmt.Errorf(rvErr)
	}
	return nil
}

// recurseAllBranches runs the given func on every commit on all branches, with
// some Task Scheduler-specific exceptions.
func (s *TaskScheduler) recurseAllBranches(ctx context.Context, repoUrl string, repo *repograph.Graph, fn func(string, *repograph.Graph, *repograph.Commit) error) error {
	blacklistBranches := BRANCH_BLACKLIST[repoUrl]
	blacklistCommits := make(map[*repograph.Commit]string, len(blacklistBranches))
	for _, b := range blacklistBranches {
		c := repo.Get(b)
		if c != nil {
			blacklistCommits[c] = b
		}
	}
	if err := repo.RecurseAllBranches(func(c *repograph.Commit) error {
		if blacklistBranch, ok := blacklistCommits[c]; ok {
			sklog.Infof("Skipping blacklisted branch %q", blacklistBranch)
			return repograph.ErrStopRecursing
		}
		for head, blacklistBranch := range blacklistCommits {
			isAncestor, err := repo.IsAncestor(c.Hash, head.Hash)
			if err != nil {
				return err
			} else if isAncestor {
				sklog.Infof("Skipping blacklisted branch %q (--is-ancestor)", blacklistBranch)
				return repograph.ErrStopRecursing
			}
		}
		if !s.window.TestCommit(repoUrl, c) {
			return repograph.ErrStopRecursing
		}
		return fn(repoUrl, repo, c)
	}); err != nil {
		return err
	}
	return nil
}

// gatherNewJobs finds and returns Jobs for all new commits, keyed by RepoState.
func (s *TaskScheduler) gatherNewJobs(ctx context.Context, repoUrl string, repo *repograph.Graph) ([]*types.Job, error) {
	defer metrics2.FuncTimer().Stop()

	// Find all new Jobs for all new commits.
	newJobs := []*types.Job{}
	if err := s.recurseAllBranches(ctx, repoUrl, repo, func(repoUrl string, r *repograph.Graph, c *repograph.Commit) error {
		// If this commit isn't in scheduling range, stop recursing.
		if !s.window.TestCommit(repoUrl, c) {
			return repograph.ErrStopRecursing
		}

		rs := types.RepoState{
			Repo:     repoUrl,
			Revision: c.Hash,
		}
		cfg, err := s.cacher.GetOrCacheRepoState(ctx, rs)
		if err != nil {
			if specs.ErrorIsPermanent(err) {
				// If we return an error here, we'll never
				// recover from bad commits.
				// TODO(borenet): We should consider canceling the jobs
				// (how?) since we can never fulfill them.
				sklog.Errorf("Failed to obtain new jobs due to permanent error: %s", err)
				return nil
			}
			return err
		}
		alreadyScheduledAllJobs := true
		for name, spec := range cfg.Jobs {
			shouldRun := false
			if !util.In(spec.Trigger, specs.PERIODIC_TRIGGERS) {
				if spec.Trigger == specs.TRIGGER_ANY_BRANCH {
					shouldRun = true
				} else if spec.Trigger == specs.TRIGGER_MASTER_ONLY {
					isAncestor, err := r.IsAncestor(c.Hash, "master")
					if err != nil {
						return err
					} else if isAncestor {
						shouldRun = true
					}
				}
			}
			if shouldRun {
				prevJobs, err := s.jCache.GetJobsByRepoState(name, rs)
				if err != nil {
					return err
				}
				alreadyScheduled := false
				for _, prev := range prevJobs {
					// We don't need to check whether it's a
					// try job because a try job wouldn't
					// match the RepoState passed into
					// GetJobsByRepoState.
					if !prev.IsForce {
						alreadyScheduled = true
					}
				}
				if !alreadyScheduled {
					alreadyScheduledAllJobs = false
					j, err := s.taskCfgCache.MakeJob(ctx, rs, name)
					if err != nil {
						// We shouldn't get ErrNoSuchEntry due to the
						// call to s.cacher.GetOrCacheRepoState above,
						// but we check the error and don't propagate
						// it, just in case.
						if err == task_cfg_cache.ErrNoSuchEntry {
							sklog.Errorf("Got ErrNoSuchEntry after a successful call to GetOrCacheRepoState! Job %s; RepoState: %+v", name, rs)
							continue
						}
						return err
					}
					newJobs = append(newJobs, j)
				}
			}
		}
		// If we'd already scheduled all of the jobs for this commit,
		// stop recursing, under the assumption that we've already
		// scheduled all of the jobs for the ones before it.
		if alreadyScheduledAllJobs {
			return repograph.ErrStopRecursing
		}
		if c.Hash == "50537e46e4f0999df0a4707b227000cfa8c800ff" {
			// Stop recursing here, since Jobs were added
			// in this commit and previous commits won't be
			// valid.
			return repograph.ErrStopRecursing
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// Reverse the new jobs list, so that if we fail to insert all of the
	// jobs (eg. because the process is interrupted), the algorithm above
	// will find the missing jobs and we'll pick up where we left off.
	for a, b := 0, len(newJobs)-1; a < b; a, b = a+1, b-1 {
		newJobs[a], newJobs[b] = newJobs[b], newJobs[a]
	}
	return newJobs, nil
}

// updateAddedTaskSpecs updates the mapping of RepoStates to the new task specs
// they added.
func (s *TaskScheduler) updateAddedTaskSpecs(ctx context.Context) error {
	defer metrics2.FuncTimer().Stop()
	repoStates := []types.RepoState{}
	for repoUrl, repo := range s.repos {
		if err := s.recurseAllBranches(ctx, repoUrl, repo, func(repoUrl string, r *repograph.Graph, c *repograph.Commit) error {
			repoStates = append(repoStates, types.RepoState{
				Repo:     repoUrl,
				Revision: c.Hash,
			})
			return nil
		}); err != nil {
			return err
		}
	}
	newTasks, err := s.taskCfgCache.GetAddedTaskSpecsForRepoStates(ctx, repoStates)
	if err != nil {
		return err
	}
	s.newTasksMtx.Lock()
	defer s.newTasksMtx.Unlock()
	s.newTasks = newTasks
	return nil
}

// MainLoop runs a single end-to-end task scheduling loop.
func (s *TaskScheduler) MainLoop(ctx context.Context) error {
	defer metrics2.FuncTimer().Stop()

	sklog.Infof("Task Scheduler MainLoop starting...")
	start := time.Now()

	var getSwarmingBotsErr error
	var wg sync.WaitGroup

	var bots []*swarming_api.SwarmingRpcsBotInfo
	wg.Add(1)
	go func() {
		defer wg.Done()

		var err error
		bots, err = getFreeSwarmingBots(s.swarming, s.busyBots, s.pools)
		if err != nil {
			getSwarmingBotsErr = err
			return
		}

	}()

	if err := s.tCache.Update(); err != nil {
		return fmt.Errorf("Failed to update task cache: %s", err)
	}

	if err := s.jCache.Update(); err != nil {
		return fmt.Errorf("Failed to update job cache: %s", err)
	}

	if err := s.updateUnfinishedJobs(); err != nil {
		return fmt.Errorf("Failed to update unfinished jobs: %s", err)
	}

	if err := s.bl.Update(); err != nil {
		return fmt.Errorf("Failed to update blacklist: %s", err)
	}

	// Regenerate the queue.
	sklog.Infof("Task Scheduler regenerating the queue...")
	queue, allCandidates, err := s.regenerateTaskQueue(ctx, start)
	if err != nil {
		return fmt.Errorf("Failed to regenerate task queue: %s", err)
	}

	wg.Wait()
	if getSwarmingBotsErr != nil {
		return fmt.Errorf("Failed to retrieve free Swarming bots: %s", getSwarmingBotsErr)
	}

	sklog.Infof("Task Scheduler scheduling tasks...")
	err = s.scheduleTasks(ctx, bots, queue)

	// An error from scheduleTasks can indicate a partial error; write diagnostics
	// in either case.
	if s.diagClient != nil {
		end := time.Now()
		s.testWaitGroup.Add(1)
		go func() {
			defer s.testWaitGroup.Done()
			util.LogErr(writeMainLoopDiagnosticsToGCS(ctx, start, end, s.diagClient, s.diagInstance, allCandidates, bots, err))
		}()
	}

	if err != nil {
		return fmt.Errorf("Failed to schedule tasks: %s", err)
	}

	sklog.Infof("Task Scheduler MainLoop finished.")
	return nil
}

// updateRepos syncs the scheduler's repos and inserts any new jobs into the
// database.
func (s *TaskScheduler) updateRepos(ctx context.Context) error {
	defer metrics2.FuncTimer().Stop()
	var newJobs []*types.Job
	if err := s.repos.UpdateWithCallback(ctx, func(repoUrl string, g *repograph.Graph) error {
		newJobsForRepo, err := s.gatherNewJobs(ctx, repoUrl, g)
		if err != nil {
			return err
		}
		newJobs = append(newJobs, newJobsForRepo...)
		return nil
	}); err != nil {
		return err
	}
	if err := s.window.Update(); err != nil {
		return err
	}
	if err := s.taskCfgCache.Cleanup(ctx, time.Now().Sub(s.window.EarliestStart())); err != nil {
		return fmt.Errorf("Failed to Cleanup TaskCfgCache: %s", err)
	}
	if err := s.db.PutJobsInChunks(newJobs); err != nil {
		return fmt.Errorf("Failed to insert new jobs into the DB: %s", err)
	}
	return s.jCache.Update()
}

// QueueLen returns the length of the queue.
func (s *TaskScheduler) QueueLen() int {
	s.queueMtx.RLock()
	defer s.queueMtx.RUnlock()
	return len(s.queue)
}

// timeDecay24Hr computes a linear time decay amount for the given duration,
// given the requested decay amount at 24 hours.
func timeDecay24Hr(decayAmt24Hr float64, elapsed time.Duration) float64 {
	return math.Max(1.0-(1.0-decayAmt24Hr)*(float64(elapsed)/float64(24*time.Hour)), 0.0)
}

// timeDecayForCommit computes a multiplier for a task candidate score based
// on how long ago the given commit landed. This allows us to prioritize more
// recent commits.
func (s *TaskScheduler) timeDecayForCommit(now time.Time, commit *repograph.Commit) (float64, error) {
	if s.timeDecayAmt24Hr == 1.0 {
		// Shortcut for special case.
		return 1.0, nil
	}
	rv := timeDecay24Hr(s.timeDecayAmt24Hr, now.Sub(commit.Timestamp))
	// TODO(benjaminwagner): Change to an exponential decay to prevent
	// zero/negative scores.
	//if rv == 0.0 {
	//	sklog.Warningf("timeDecayForCommit is zero. Now: %s, Commit: %s ts %s, TimeDecay: %2f\nDetails: %v", now, commit.Hash, commit.Timestamp, s.timeDecayAmt24Hr, commit)
	//}
	return rv, nil
}

func (ts *TaskScheduler) GetBlacklist() *blacklist.Blacklist {
	return ts.bl
}

// testedness computes the total "testedness" of a set of commits covered by a
// task whose blamelist included N commits. The "testedness" of a task spec at a
// given commit is defined as follows:
//
// -1.0    if no task has ever included this commit for this task spec.
// 1.0     if a task was run for this task spec AT this commit.
// 1.0 / N if a task for this task spec has included this commit, where N is
//         the number of commits included in the task.
//
// This function gives the sum of the testedness for a blamelist of N commits.
func testedness(n int) float64 {
	if n < 0 {
		// This should never happen.
		sklog.Errorf("Task score function got a blamelist with %d commits", n)
		return -1.0
	} else if n == 0 {
		// Zero commits have zero testedness.
		return 0.0
	} else if n == 1 {
		return 1.0
	} else {
		return 1.0 + float64(n-1)/float64(n)
	}
}

// testednessIncrease computes the increase in "testedness" obtained by running
// a task with the given blamelist length which may have "stolen" commits from
// a previous task with a different blamelist length. To do so, we compute the
// "testedness" for every commit affected by the task,  before and after the
// task would run. We subtract the "before" score from the "after" score to
// obtain the "testedness" increase at each commit, then sum them to find the
// total increase in "testedness" obtained by running the task.
func testednessIncrease(blamelistLength, stoleFromBlamelistLength int) float64 {
	// Invalid inputs.
	if blamelistLength <= 0 || stoleFromBlamelistLength < 0 {
		return -1.0
	}

	if stoleFromBlamelistLength == 0 {
		// This task covers previously-untested commits. Previous testedness
		// is -1.0 for each commit in the blamelist.
		beforeTestedness := float64(-blamelistLength)
		afterTestedness := testedness(blamelistLength)
		return afterTestedness - beforeTestedness
	} else if blamelistLength == stoleFromBlamelistLength {
		// This is a retry. It provides no testedness increase, so shortcut here
		// rather than perform the math to obtain the same answer.
		return 0.0
	} else {
		// This is a bisect/backfill.
		beforeTestedness := testedness(stoleFromBlamelistLength)
		afterTestedness := testedness(blamelistLength) + testedness(stoleFromBlamelistLength-blamelistLength)
		return afterTestedness - beforeTestedness
	}
}

// getFreeSwarmingBots returns a slice of free swarming bots.
func getFreeSwarmingBots(s swarming.ApiClient, busy *busyBots, pools []string) ([]*swarming_api.SwarmingRpcsBotInfo, error) {
	defer metrics2.FuncTimer().Stop()

	// Query for free Swarming bots and pending Swarming tasks in all pools.
	var wg sync.WaitGroup
	bots := []*swarming_api.SwarmingRpcsBotInfo{}
	pending := []*swarming_api.SwarmingRpcsTaskResult{}
	errs := []error{}
	var mtx sync.Mutex
	t := time.Time{}
	for _, pool := range pools {
		// Free bots.
		wg.Add(1)
		go func(pool string) {
			defer wg.Done()
			b, err := s.ListFreeBots(pool)
			mtx.Lock()
			defer mtx.Unlock()
			if err != nil {
				errs = append(errs, err)
			} else {
				bots = append(bots, b...)
			}
		}(pool)

		// Pending tasks.
		wg.Add(1)
		go func(pool string) {
			defer wg.Done()
			t, err := s.ListTaskResults(t, t, []string{fmt.Sprintf("pool:%s", pool)}, "PENDING", false)
			mtx.Lock()
			defer mtx.Unlock()
			if err != nil {
				errs = append(errs, err)
			} else {
				pending = append(pending, t...)
			}
		}(pool)
	}

	wg.Wait()
	if len(errs) > 0 {
		return nil, fmt.Errorf("Got errors loading bots and tasks from Swarming: %v", errs)
	}

	rv := make([]*swarming_api.SwarmingRpcsBotInfo, 0, len(bots))
	for _, bot := range bots {
		if bot.IsDead {
			continue
		}
		if bot.Quarantined {
			continue
		}
		if bot.TaskId != "" {
			continue
		}
		rv = append(rv, bot)
	}
	busy.RefreshTasks(pending)
	return busy.Filter(rv), nil
}

// updateUnfinishedTasks queries Swarming for all unfinished tasks and updates
// their status in the DB.
func (s *TaskScheduler) updateUnfinishedTasks() error {
	defer metrics2.FuncTimer().Stop()
	// Update the TaskCache.
	if err := s.tCache.Update(); err != nil {
		return err
	}

	tasks, err := s.tCache.UnfinishedTasks()
	if err != nil {
		return err
	}
	sort.Sort(types.TaskSlice(tasks))

	// Query Swarming for all unfinished tasks.
	sklog.Infof("Querying states of %d unfinished tasks.", len(tasks))
	ids := make([]string, 0, len(tasks))
	for _, t := range tasks {
		ids = append(ids, t.SwarmingTaskId)
	}
	states, err := s.swarming.GetStates(ids)
	if err != nil {
		return err
	}
	finished := make([]*types.Task, 0, len(states))
	for idx, task := range tasks {
		state := states[idx]
		if state != swarming.TASK_STATE_PENDING && state != swarming.TASK_STATE_RUNNING {
			finished = append(finished, task)
		}
	}

	// Update any newly-finished tasks.
	if len(finished) > 0 {
		sklog.Infof("Updating %d newly-finished tasks.", len(finished))
		var wg sync.WaitGroup
		errs := make([]error, len(tasks))
		for i, t := range finished {
			wg.Add(1)
			go func(idx int, t *types.Task) {
				defer wg.Done()
				swarmTask, err := s.swarming.GetTask(t.SwarmingTaskId, false)
				if err != nil {
					errs[idx] = fmt.Errorf("Failed to update unfinished task; failed to get updated task from swarming: %s", err)
					return
				}
				modified, err := db.UpdateDBFromSwarmingTask(s.db, swarmTask)
				if err != nil {
					errs[idx] = fmt.Errorf("Failed to update unfinished task: %s", err)
					return
				} else if modified {
					s.updateUnfinishedCount.Inc(1)
				}
			}(i, t)
		}
		wg.Wait()
		for _, err := range errs {
			if err != nil {
				return err
			}
		}
	}

	return s.tCache.Update()
}

// jobFinished marks the Job as finished.
func (s *TaskScheduler) jobFinished(j *types.Job) {
	j.Finished = time.Now()
}

// updateUnfinishedJobs updates all not-yet-finished Jobs to determine if their
// state has changed.
func (s *TaskScheduler) updateUnfinishedJobs() error {
	defer metrics2.FuncTimer().Stop()
	jobs, err := s.jCache.UnfinishedJobs()
	if err != nil {
		return err
	}

	modifiedJobs := make([]*types.Job, 0, len(jobs))
	modifiedTasks := make(map[string]*types.Task, len(jobs))
	for _, j := range jobs {
		tasks, err := s.getTasksForJob(j)
		if err != nil {
			return err
		}
		summaries := make(map[string][]*types.TaskSummary, len(tasks))
		for k, v := range tasks {
			cpy := make([]*types.TaskSummary, 0, len(v))
			for _, t := range v {
				if existing := modifiedTasks[t.Id]; existing != nil {
					t = existing
				}
				cpy = append(cpy, t.MakeTaskSummary())
				// The Jobs list is always sorted.
				oldLen := len(t.Jobs)
				t.Jobs = util.InsertStringSorted(t.Jobs, j.Id)
				if len(t.Jobs) > oldLen {
					modifiedTasks[t.Id] = t
				}
			}
			summaries[k] = cpy
		}
		if !reflect.DeepEqual(summaries, j.Tasks) {
			j.Tasks = summaries
			j.Status = j.DeriveStatus()
			if j.Done() {
				s.jobFinished(j)
			}
			modifiedJobs = append(modifiedJobs, j)
		}
	}
	if len(modifiedTasks) > 0 {
		tasks := make([]*types.Task, 0, len(modifiedTasks))
		for _, t := range modifiedTasks {
			tasks = append(tasks, t)
		}
		if err := s.db.PutTasksInChunks(tasks); err != nil {
			return err
		} else if err := s.tCache.Update(); err != nil {
			return err
		}
	}
	if len(modifiedJobs) > 0 {
		if err := s.db.PutJobsInChunks(modifiedJobs); err != nil {
			return err
		} else if err := s.jCache.Update(); err != nil {
			return err
		}
	}
	return nil
}

// getTasksForJob finds all Tasks for the given Job. It returns the Tasks
// in a map keyed by name.
func (s *TaskScheduler) getTasksForJob(j *types.Job) (map[string][]*types.Task, error) {
	tasks := map[string][]*types.Task{}
	for d := range j.Dependencies {
		key := j.MakeTaskKey(d)
		gotTasks, err := s.tCache.GetTasksByKey(&key)
		if err != nil {
			return nil, err
		}
		tasks[d] = gotTasks
	}
	return tasks, nil
}

// GetJob returns the given Job, plus the dimensions for the task specs which
// are part of the Job, keyed by name.
func (s *TaskScheduler) GetJob(ctx context.Context, id string) (*types.Job, map[string][]string, error) {
	job, err := s.jCache.GetJobMaybeExpired(id)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to retrieve Job: %s", err)
	}
	cfg, err := s.taskCfgCache.Get(ctx, job.RepoState)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to retrieve Tasks cfg: %s", err)
	}
	taskSpecs := make(map[string][]string, len(job.Dependencies))
	for taskName := range job.Dependencies {
		taskSpec, ok := cfg.Tasks[taskName]
		if !ok {
			return nil, nil, fmt.Errorf("Job %s (%s) points to unknown task %q at repo state: %+v", job.Id, job.Name, taskName, job.RepoState)
		}
		taskSpecs[taskName] = taskSpec.Dimensions
	}
	return job, taskSpecs, nil
}

// GetTask returns the given Task.
func (s *TaskScheduler) GetTask(id string) (*types.Task, error) {
	return s.tCache.GetTaskMaybeExpired(id)
}

// addTasksSingleTaskSpec computes the blamelist for each task in tasks, all of
// which must have the same Repo and Name fields, and inserts/updates them in
// the TaskDB. Also adjusts blamelists of existing tasks.
func (s *TaskScheduler) addTasksSingleTaskSpec(ctx context.Context, tasks []*types.Task) error {
	sort.Sort(types.TaskSlice(tasks))
	cache := newCacheWrapper(s.tCache)
	repoName := tasks[0].Repo
	taskName := tasks[0].Name
	repo, ok := s.repos[repoName]
	if !ok {
		return fmt.Errorf("No such repo: %s", repoName)
	}

	commitsBuf := make([]*repograph.Commit, 0, MAX_BLAMELIST_COMMITS)
	updatedTasks := map[string]*types.Task{}
	for _, task := range tasks {
		if task.Repo != repoName || task.Name != taskName {
			return fmt.Errorf("Mismatched Repo or Name: %v", tasks)
		}
		if task.Id == "" {
			if err := s.db.AssignId(task); err != nil {
				return err
			}
		}
		if task.IsTryJob() {
			updatedTasks[task.Id] = task
			continue
		}
		// Compute blamelist.
		revision := repo.Get(task.Revision)
		if revision == nil {
			return fmt.Errorf("No such commit %s in %s.", task.Revision, task.Repo)
		}
		if !s.window.TestTime(task.Repo, revision.Timestamp) {
			return fmt.Errorf("Can not add task %s with revision %s (at %s) before window start.", task.Id, task.Revision, revision.Timestamp)
		}
		commits, stealingFrom, err := ComputeBlamelist(ctx, cache, repo, task.Name, task.Repo, revision, commitsBuf, s.newTasks)
		if err != nil {
			return err
		}
		task.Commits = commits
		if len(task.Commits) > 0 && !util.In(task.Revision, task.Commits) {
			sklog.Errorf("task %s (%s @ %s) doesn't have its own revision in its blamelist: %v", task.Id, task.Name, task.Revision, task.Commits)
		}
		updatedTasks[task.Id] = task
		cache.insert(task)
		if stealingFrom != nil && stealingFrom.Id != task.Id {
			stole := util.NewStringSet(commits)
			oldC := util.NewStringSet(stealingFrom.Commits)
			newC := oldC.Complement(stole)
			if len(newC) == 0 {
				stealingFrom.Commits = nil
			} else {
				newCommits := make([]string, 0, len(newC))
				for c := range newC {
					newCommits = append(newCommits, c)
				}
				stealingFrom.Commits = newCommits
			}
			updatedTasks[stealingFrom.Id] = stealingFrom
			cache.insert(stealingFrom)
		}
	}
	putTasks := make([]*types.Task, 0, len(updatedTasks))
	for _, task := range updatedTasks {
		putTasks = append(putTasks, task)
	}
	return s.db.PutTasks(putTasks)
}

// addTasks inserts the given tasks into the TaskDB, updating blamelists. The
// provided Tasks should have all fields initialized except for Commits, which
// will be overwritten, and optionally Id, which will be assigned if necessary.
// addTasks updates existing Tasks' blamelists, if needed. The provided map
// groups Tasks by repo and TaskSpec name. May return error on partial success.
// May modify Commits and Id of argument tasks on error.
func (s *TaskScheduler) addTasks(ctx context.Context, taskMap map[string]map[string][]*types.Task) error {
	type queueItem struct {
		Repo string
		Name string
	}
	queue := map[queueItem]bool{}
	for repo, byName := range taskMap {
		for name, tasks := range byName {
			if len(tasks) == 0 {
				continue
			}
			queue[queueItem{
				Repo: repo,
				Name: name,
			}] = true
		}
	}

	s.newTasksMtx.RLock()
	defer s.newTasksMtx.RUnlock()

	for i := 0; i < db.NUM_RETRIES; i++ {
		if len(queue) == 0 {
			return nil
		}
		if err := s.tCache.Update(); err != nil {
			return err
		}

		done := make(chan queueItem)
		errs := make(chan error, len(queue))
		wg := sync.WaitGroup{}
		for item := range queue {
			wg.Add(1)
			go func(item queueItem, tasks []*types.Task) {
				defer wg.Done()
				if err := s.addTasksSingleTaskSpec(ctx, tasks); err != nil {
					if !db.IsConcurrentUpdate(err) {
						errs <- fmt.Errorf("Error adding tasks for %s (in repo %s): %s", item.Name, item.Repo, err)
					}
				} else {
					done <- item
				}
			}(item, taskMap[item.Repo][item.Name])
		}
		go func() {
			wg.Wait()
			close(done)
			close(errs)
		}()
		for item := range done {
			delete(queue, item)
		}
		rvErrs := []error{}
		for err := range errs {
			sklog.Error(err)
			rvErrs = append(rvErrs, err)
		}
		if len(rvErrs) != 0 {
			return rvErrs[0]
		}
	}

	if len(queue) > 0 {
		return fmt.Errorf("addTasks: %d consecutive ErrConcurrentUpdate; %d of %d TaskSpecs failed. %#v", db.NUM_RETRIES, len(queue), len(taskMap), queue)
	}
	return nil
}

// HandleSwarmingPubSub loads the given Swarming task ID from Swarming and
// updates the associated types.Task in the database. Returns a bool indicating
// whether the pubsub message should be acknowledged.
func (s *TaskScheduler) HandleSwarmingPubSub(msg *swarming.PubSubTaskMessage) bool {
	s.pubsubCount.Inc(1)
	if msg.UserData == "" {
		// This message is invalid. ACK it to make it go away.
		return true
	}

	// If the task has been triggered but not yet inserted into the DB, NACK
	// the message so that we'll receive it later.
	s.pendingInsertMtx.RLock()
	isPending := s.pendingInsert[msg.UserData]
	s.pendingInsertMtx.RUnlock()
	if isPending {
		sklog.Debugf("Received pub/sub message for task which hasn't yet been inserted into the db: %s (%s); not ack'ing message; will try again later.", msg.SwarmingTaskId, msg.UserData)
		return false
	}

	// Obtain the Swarming task data.
	res, err := s.swarming.GetTask(msg.SwarmingTaskId, false)
	if err != nil {
		sklog.Errorf("pubsub: Failed to retrieve task from Swarming: %s", err)
		return true
	}
	// Skip unfinished tasks.
	if res.CompletedTs == "" {
		return true
	}
	// Update the task in the DB.
	if _, err := db.UpdateDBFromSwarmingTask(s.db, res); err != nil {
		// TODO(borenet): Some of these cases should never be hit, after all tasks
		// start supplying the ID in msg.UserData. We should be able to remove the logic.
		if err == db.ErrNotFound {
			id, err := swarming.GetTagValue(res, types.SWARMING_TAG_ID)
			if err != nil {
				id = "<MISSING ID TAG>"
			}
			created, err := swarming.ParseTimestamp(res.CreatedTs)
			if err != nil {
				sklog.Errorf("Failed to parse timestamp: %s; %s", res.CreatedTs, err)
				return true
			}
			if time.Now().Sub(created) < 2*time.Minute {
				sklog.Infof("Failed to update task %q: No such task ID: %q. Less than two minutes old; try again later.", msg.SwarmingTaskId, id)
				return false
			}
			sklog.Errorf("Failed to update task %q: No such task ID: %q", msg.SwarmingTaskId, id)
			return true
		} else if err == db.ErrUnknownId {
			expectedSwarmingTaskId := "<unknown>"
			id, err := swarming.GetTagValue(res, types.SWARMING_TAG_ID)
			if err != nil {
				id = "<MISSING ID TAG>"
			} else {
				t, err := s.db.GetTaskById(id)
				if err != nil {
					sklog.Errorf("Failed to update task %q; mismatched ID and failed to retrieve task from DB: %s", msg.SwarmingTaskId, err)
					return true
				} else {
					expectedSwarmingTaskId = t.SwarmingTaskId
				}
			}
			sklog.Errorf("Failed to update task %q: Task %s has a different Swarming task ID associated with it: %s", msg.SwarmingTaskId, id, expectedSwarmingTaskId)
			return true
		} else {
			sklog.Errorf("Failed to update task %q: %s", msg.SwarmingTaskId, err)
			return true
		}
	}
	return true
}

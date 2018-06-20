package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"go.skia.org/infra/go/depot_tools"
	"go.skia.org/infra/go/sklog"

	"go.skia.org/infra/go/git/repograph"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/task_scheduler/go/db"
	"go.skia.org/infra/task_scheduler/go/specs"
)

// tasksPerCommitCache is a struct used for caching the number of task specs
// for various commits.
type tasksPerCommitCache struct {
	cached map[db.RepoState]int
	mtx    sync.Mutex
	period time.Duration
	repos  repograph.Map
	tcc    *specs.TaskCfgCache
}

// newTasksPerCommitCache returns a tasksPerCommitCache instance.
func newTasksPerCommitCache(ctx context.Context, workdir, recipesCfgFile string, repos repograph.Map, period time.Duration) (*tasksPerCommitCache, error) {
	wd := path.Join(workdir, "tasksPerCommitCache")
	if _, err := os.Stat(wd); err != nil {
		if os.IsNotExist(err) {
			if err := os.Mkdir(wd, os.ModePerm); err != nil {
				return nil, err
			}
		} else {
			return nil, fmt.Errorf("There is a problem with the workdir: %s", err)
		}
	}
	depotTools, err := depot_tools.GetDepotTools(ctx, wd, recipesCfgFile)
	if err != nil {
		return nil, err
	}
	gitCache := path.Join(wd, "cache")
	tcc, err := specs.NewTaskCfgCache(ctx, repos, depotTools, gitCache, 3)
	if err != nil {
		return nil, err
	}
	c := &tasksPerCommitCache{
		cached: map[db.RepoState]int{},
		period: period,
		repos:  repos,
		tcc:    tcc,
	}
	go util.RepeatCtx(time.Minute, ctx, func() {
		if err := c.update(ctx); err != nil {
			sklog.Errorf("Failed to update tasksPerCommitCache: %s", err)
		}
	})
	return c, nil
}

// Get returns the number of tasks expected to run at the given commit.
func (c *tasksPerCommitCache) Get(ctx context.Context, rs db.RepoState) (int, error) {
	c.mtx.Lock()
	defer c.mtx.Unlock()

	if _, ok := c.cached[rs]; !ok {
		// Find the number of TaskSpecs expected to run at this commit.
		cfg, err := c.tcc.ReadTasksCfg(ctx, rs)
		if err != nil {
			return 0, err
		}
		tasksForCommit := make(map[string]bool, len(cfg.Tasks))
		var recurse func(string)
		recurse = func(taskSpec string) {
			if tasksForCommit[taskSpec] {
				return
			}
			tasksForCommit[taskSpec] = true
			for _, d := range cfg.Tasks[taskSpec].Dependencies {
				recurse(d)
			}
		}
		for _, job := range cfg.Jobs {
			if job.Trigger == "" {
				for _, taskSpec := range job.TaskSpecs {
					recurse(taskSpec)
				}
			}
		}
		c.cached[rs] = len(tasksForCommit)
	}
	return c.cached[rs], nil
}

// update pulls down new commits and evicts old entries from the cache.
func (c *tasksPerCommitCache) update(ctx context.Context) error {
	c.mtx.Lock()
	defer c.mtx.Unlock()
	start := time.Now().Add(-c.period)
	for rs := range c.cached {
		repo, ok := c.repos[rs.Repo]
		if !ok {
			return fmt.Errorf("No such repo: %s", rs.Repo)
		}
		commit := repo.Get(rs.Revision)
		if commit == nil {
			return fmt.Errorf("No such commit: %s in repo %s", rs.Revision, rs.Repo)
		}
		if commit.Timestamp.Before(start) {
			delete(c.cached, rs)
		}
	}
	return c.tcc.Cleanup(c.period)
}

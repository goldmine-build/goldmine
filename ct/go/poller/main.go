/*
	The Cluster Telemetry poller checks for new pending tasks by polling the Cluster Telemetry
	frontend. Pending tasks are picked up according to the order they were added to CTFE.
	When picked up, tasks are immediately executed. There could be multiple tasks running at the
	same time.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.skia.org/infra/go/sklog"

	"go.skia.org/infra/ct/go/ct_autoscaler"
	"go.skia.org/infra/ct/go/ctfe/admin_tasks"
	"go.skia.org/infra/ct/go/ctfe/capture_skps"
	"go.skia.org/infra/ct/go/ctfe/chromium_analysis"
	"go.skia.org/infra/ct/go/ctfe/chromium_builds"
	"go.skia.org/infra/ct/go/ctfe/chromium_perf"
	"go.skia.org/infra/ct/go/ctfe/lua_scripts"
	"go.skia.org/infra/ct/go/ctfe/metrics_analysis"
	"go.skia.org/infra/ct/go/ctfe/pixel_diff"
	"go.skia.org/infra/ct/go/ctfe/task_common"
	"go.skia.org/infra/ct/go/frontend"
	"go.skia.org/infra/ct/go/master_scripts/master_common"
	ctutil "go.skia.org/infra/ct/go/util"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/metrics2"
	skutil "go.skia.org/infra/go/util"
)

// flags
var (
	promPort     = flag.String("prom_port", ":20000", "Metrics service address (e.g., ':20000')")
	pollInterval = flag.Duration("poll_interval", 30*time.Second, "How often to poll CTFE for new pending tasks.")
	// Mutex that controls updating and building of the local checkout.
	repoMtx = sync.Mutex{}
	// Map that holds all picked up tasks. Used to ensure same task is not picked up more than once.
	pickedUpTasks = map[string]string{}
	// Mutex that controls access to the above map.
	tasksMtx = sync.Mutex{}
)

// Runs "git pull; make all".
func updateAndBuild(ctx context.Context) error {
	repoMtx.Lock()
	defer repoMtx.Unlock()
	makefilePath := ctutil.CtTreeDir

	// TODO(benjaminwagner): Should this also do 'go get -u ...' and/or 'gclient sync'?
	err := exec.Run(ctx, &exec.Command{
		Name:      "git",
		Args:      []string{"pull"},
		Dir:       makefilePath,
		Timeout:   ctutil.GIT_PULL_TIMEOUT,
		LogStdout: true,
		LogStderr: true,
	})
	if err != nil {
		return err
	}
	return exec.Run(ctx, &exec.Command{
		Name:      "make",
		Args:      []string{"all"},
		Dir:       makefilePath,
		Timeout:   ctutil.MAKE_ALL_TIMEOUT,
		LogStdout: true,
		LogStderr: true,
	})
}

type GetPatchFunc func(patchId string) (string, error)

// Specifies the methods that poll requires for each type of task.
type Task interface {
	GetTaskName() string
	GetCommonCols() *task_common.CommonCols
	// Writes any files required by the task and then uses exec.Run to run the task command.
	Execute(ctx context.Context, getPatchFunc GetPatchFunc) error
	// Returns the corresponding UpdateTaskVars instance of this Task. The
	// returned instance is not populated.
	GetUpdateTaskVars() task_common.UpdateTaskVars
	// Whether the task needs to run on GCE workers.
	RunsOnGCEWorkers() bool
}

// Generates a hopefully-unique ID for this execution of this task.
func runId(task Task) string {
	return strings.SplitN(task.GetCommonCols().Username, "@", 2)[0] + "-" + ctutil.GetCurrentTs()
}

// Define frontend.ChromiumAnalysisDatastoreTask here so we can add methods.
type ChromiumAnalysisTask struct {
	chromium_analysis.DatastoreTask
}

func (task *ChromiumAnalysisTask) Execute(ctx context.Context, getPatchFunc GetPatchFunc) error {
	runId := runId(task)
	for fileSuffix, patch := range map[string]string{
		".chromium.patch":      task.ChromiumPatch,
		".skia.patch":          task.SkiaPatch,
		".v8.patch":            task.V8Patch,
		".catapult.patch":      task.CatapultPatch,
		".custom_webpages.csv": task.CustomWebpages,
	} {
		// Add an extra newline at the end because git sometimes rejects patches due to
		// missing newlines.
		patch = patch + "\n"
		patchPath := filepath.Join(os.TempDir(), runId+fileSuffix)
		if err := ioutil.WriteFile(patchPath, []byte(patch), 0666); err != nil {
			return err
		}
		defer skutil.Remove(patchPath)
	}
	return exec.Run(ctx, &exec.Command{
		Name: "run_chromium_analysis_on_workers",
		Args: []string{
			"--emails=" + task.Username,
			"--description=" + task.Description,
			"--task_id=" + strconv.FormatInt(task.DatastoreKey.ID, 10),
			"--pageset_type=" + task.PageSets,
			"--benchmark_name=" + task.Benchmark,
			"--benchmark_extra_args=" + task.BenchmarkArgs,
			"--browser_extra_args=" + task.BrowserArgs,
			"--run_in_parallel=" + strconv.FormatBool(task.RunInParallel),
			"--target_platform=" + task.Platform,
			"--run_on_gce=" + strconv.FormatBool(task.RunsOnGCEWorkers()),
			"--match_stdout_txt=" + task.MatchStdoutTxt,
			"--run_id=" + runId,
			"--logtostderr",
			fmt.Sprintf("--local=%t", *master_common.Local),
		},
	})
}

// Define frontend.ChromiumPerfDatastoreTask here so we can add methods.
type ChromiumPerfTask struct {
	chromium_perf.DatastoreTask
}

func (task *ChromiumPerfTask) Execute(ctx context.Context, getPatchFunc GetPatchFunc) error {
	runId := runId(task)
	// TODO(benjaminwagner): Since run_chromium_perf_on_workers only reads these in order to
	// upload to Google Storage, eventually we should move the upload step here to avoid writing
	// to disk.
	for fileSuffix, patch := range map[string]string{
		".chromium.patch":      task.ChromiumPatch,
		".skia.patch":          task.SkiaPatch,
		".v8.patch":            task.V8Patch,
		".catapult.patch":      task.CatapultPatch,
		".custom_webpages.csv": task.CustomWebpages,
	} {
		// Add an extra newline at the end because git sometimes rejects patches due to
		// missing newlines.
		patch = patch + "\n"
		patchPath := filepath.Join(os.TempDir(), runId+fileSuffix)
		if err := ioutil.WriteFile(patchPath, []byte(patch), 0666); err != nil {
			return err
		}
		defer skutil.Remove(patchPath)
	}
	return exec.Run(ctx, &exec.Command{
		Name: "run_chromium_perf_on_workers",
		Args: []string{
			"--emails=" + task.Username,
			"--description=" + task.Description,
			"--task_id=" + strconv.FormatInt(task.DatastoreKey.ID, 10),
			"--pageset_type=" + task.PageSets,
			"--benchmark_name=" + task.Benchmark,
			"--benchmark_extra_args=" + task.BenchmarkArgs,
			"--browser_extra_args_nopatch=" + task.BrowserArgsNoPatch,
			"--browser_extra_args_withpatch=" + task.BrowserArgsWithPatch,
			"--repeat_benchmark=" + strconv.FormatInt(task.RepeatRuns, 10),
			"--run_in_parallel=" + strconv.FormatBool(task.RunInParallel),
			"--target_platform=" + task.Platform,
			"--run_on_gce=" + strconv.FormatBool(task.RunsOnGCEWorkers()),
			"--run_id=" + runId,
			"--logtostderr",
			fmt.Sprintf("--local=%t", *master_common.Local),
		},
	})
}

// Define frontend.MetricsAnalysisDatastoreTask here so we can add methods.
type MetricsAnalysisTask struct {
	metrics_analysis.DatastoreTask
}

func (task *MetricsAnalysisTask) Execute(ctx context.Context, getPatchFunc GetPatchFunc) error {
	runId := runId(task)
	for fileSuffix, patch := range map[string]string{
		".chromium.patch": task.ChromiumPatch,
		".catapult.patch": task.CatapultPatch,
		".traces.csv":     task.CustomTraces,
	} {
		// Add an extra newline at the end because git sometimes rejects patches due to
		// missing newlines.
		patch = patch + "\n"
		patchPath := filepath.Join(os.TempDir(), runId+fileSuffix)
		if err := ioutil.WriteFile(patchPath, []byte(patch), 0666); err != nil {
			return err
		}
		defer skutil.Remove(patchPath)
	}
	return exec.Run(ctx, &exec.Command{
		Name: "metrics_analysis_on_workers",
		Args: []string{
			"--emails=" + task.Username,
			"--description=" + task.Description,
			"--task_id=" + strconv.FormatInt(task.DatastoreKey.ID, 10),
			"--metric_name=" + task.MetricName,
			"--analysis_output_link=" + task.AnalysisOutputLink,
			"--benchmark_extra_args=" + task.BenchmarkArgs,
			"--run_id=" + runId,
			"--logtostderr",
			fmt.Sprintf("--local=%t", *master_common.Local),
		},
	})
}

// Define frontend.PixelDiffDatastoreTask here so we can add methods.
type PixelDiffTask struct {
	pixel_diff.DatastoreTask
}

func (task *PixelDiffTask) Execute(ctx context.Context, getPatchFunc GetPatchFunc) error {
	runId := runId(task)
	for fileSuffix, patchGSPath := range map[string]string{
		".chromium.patch":      task.ChromiumPatchGSPath,
		".skia.patch":          task.SkiaPatchGSPath,
		".custom_webpages.csv": task.CustomWebpagesGSPath,
	} {
		patch, err := getPatchFunc(patchGSPath)
		if err != nil {
			return err
		}
		// Add an extra newline at the end because git sometimes rejects patches due to
		// missing newlines.
		patch = patch + "\n"
		patchPath := filepath.Join(os.TempDir(), runId+fileSuffix)
		if err := ioutil.WriteFile(patchPath, []byte(patch), 0666); err != nil {
			return err
		}
		defer skutil.Remove(patchPath)
	}
	return exec.Run(ctx, &exec.Command{
		Name: "pixel_diff_on_workers",
		Args: []string{
			"--emails=" + task.Username,
			"--description=" + task.Description,
			"--task_id=" + strconv.FormatInt(task.DatastoreKey.ID, 10),
			"--pageset_type=" + task.PageSets,
			"--benchmark_extra_args=" + task.BenchmarkArgs,
			"--browser_extra_args_nopatch=" + task.BrowserArgsNoPatch,
			"--browser_extra_args_withpatch=" + task.BrowserArgsWithPatch,
			"--run_on_gce=" + strconv.FormatBool(task.RunsOnGCEWorkers()),
			"--run_id=" + runId,
			"--logtostderr",
			fmt.Sprintf("--local=%t", *master_common.Local),
		},
	})
}

// Define frontend.CaptureSkpsDatastoreTask here so we can add methods.
type CaptureSkpsTask struct {
	capture_skps.DatastoreTask
}

func (task *CaptureSkpsTask) Execute(ctx context.Context, getPatchFunc GetPatchFunc) error {
	runId := runId(task)
	chromiumBuildDir := ctutil.ChromiumBuildDir(task.ChromiumRev, task.SkiaRev, "")
	return exec.Run(ctx, &exec.Command{
		Name: "capture_skps_on_workers",
		Args: []string{
			"--emails=" + task.Username,
			"--description=" + task.Description,
			"--task_id=" + strconv.FormatInt(task.DatastoreKey.ID, 10),
			"--pageset_type=" + task.PageSets,
			"--chromium_build=" + chromiumBuildDir,
			"--target_platform=Linux",
			"--run_on_gce=" + strconv.FormatBool(task.RunsOnGCEWorkers()),
			"--run_id=" + runId,
			"--logtostderr",
			fmt.Sprintf("--local=%t", *master_common.Local),
		},
	})
}

// Define frontend.LuaScriptDatastoreTask here so we can add methods.
type LuaScriptTask struct {
	lua_scripts.DatastoreTask
}

func (task *LuaScriptTask) Execute(ctx context.Context, getPatchFunc GetPatchFunc) error {
	runId := runId(task)
	chromiumBuildDir := ctutil.ChromiumBuildDir(task.ChromiumRev, task.SkiaRev, "")
	// TODO(benjaminwagner): Since run_lua_on_workers only reads the lua script in order to
	// upload to Google Storage, eventually we should move the upload step here to avoid writing
	// to disk. Not sure if we can/should do the same for the aggregator script.
	luaScriptName := runId + ".lua"
	luaScriptPath := filepath.Join(os.TempDir(), luaScriptName)
	if err := ioutil.WriteFile(luaScriptPath, []byte(task.LuaScript), 0666); err != nil {
		return err
	}
	defer skutil.Remove(luaScriptPath)
	if task.LuaAggregatorScript != "" {
		luaAggregatorName := runId + ".aggregator"
		luaAggregatorPath := filepath.Join(os.TempDir(), luaAggregatorName)
		if err := ioutil.WriteFile(luaAggregatorPath, []byte(task.LuaAggregatorScript), 0666); err != nil {
			return err
		}
		defer skutil.Remove(luaAggregatorPath)
	}
	return exec.Run(ctx, &exec.Command{
		Name: "run_lua_on_workers",
		Args: []string{
			"--emails=" + task.Username,
			"--description=" + task.Description,
			"--task_id=" + strconv.FormatInt(task.DatastoreKey.ID, 10),
			"--pageset_type=" + task.PageSets,
			"--chromium_build=" + chromiumBuildDir,
			"--run_on_gce=" + strconv.FormatBool(task.RunsOnGCEWorkers()),
			"--run_id=" + runId,
			"--logtostderr",
			fmt.Sprintf("--local=%t", *master_common.Local),
		},
	})
}

// Define frontend.ChromiumBuildDatastoreTask here so we can add methods.
type ChromiumBuildTask struct {
	chromium_builds.DatastoreTask
}

func (task *ChromiumBuildTask) Execute(ctx context.Context, getPatchFunc GetPatchFunc) error {
	runId := runId(task)
	// We do not pass --run_on_gce to the below because build tasks always run
	// on GCE builders not GCE workers or bare-metal machines.
	return exec.Run(ctx, &exec.Command{
		Name: "build_chromium",
		Args: []string{
			"--emails=" + task.Username,
			"--task_id=" + strconv.FormatInt(task.DatastoreKey.ID, 10),
			"--run_id=" + runId,
			"--target_platform=Linux",
			"--chromium_hash=" + task.ChromiumRev,
			"--skia_hash=" + task.SkiaRev,
			"--logtostderr",
			fmt.Sprintf("--local=%t", *master_common.Local),
		},
	})
}

// Define frontend.RecreatePageSetsDatastoreTask here so we can add methods.
type RecreatePageSetsTask struct {
	admin_tasks.RecreatePageSetsDatastoreTask
}

func (task *RecreatePageSetsTask) Execute(ctx context.Context, getPatchFunc GetPatchFunc) error {
	runId := runId(task)
	return exec.Run(ctx, &exec.Command{
		Name: "create_pagesets_on_workers",
		Args: []string{
			"--emails=" + task.Username,
			"--task_id=" + strconv.FormatInt(task.DatastoreKey.ID, 10),
			"--run_on_gce=" + strconv.FormatBool(task.RunsOnGCEWorkers()),
			"--run_id=" + runId,
			"--pageset_type=" + task.PageSets,
			"--logtostderr",
			fmt.Sprintf("--local=%t", *master_common.Local),
		},
	})
}

// Define frontend.RecreateWebpageArchivesDatastoreTask here so we can add methods.
type RecreateWebpageArchivesTask struct {
	admin_tasks.RecreateWebpageArchivesDatastoreTask
}

func (task *RecreateWebpageArchivesTask) Execute(ctx context.Context, getPatchFunc GetPatchFunc) error {
	runId := runId(task)
	return exec.Run(ctx, &exec.Command{
		Name: "capture_archives_on_workers",
		Args: []string{
			"--emails=" + task.Username,
			"--task_id=" + strconv.FormatInt(task.DatastoreKey.ID, 10),
			"--run_on_gce=" + strconv.FormatBool(task.RunsOnGCEWorkers()),
			"--run_id=" + runId,
			"--pageset_type=" + task.PageSets,
			"--logtostderr",
			fmt.Sprintf("--local=%t", *master_common.Local),
		},
	})
}

// Returns a poller Task containing the given task_common.Task, or nil if otherTask is nil.
func asPollerTask(ctx context.Context, otherTask task_common.Task) Task {
	if otherTask == nil {
		return nil
	}
	switch t := otherTask.(type) {
	case *admin_tasks.RecreatePageSetsDatastoreTask:
		return &RecreatePageSetsTask{RecreatePageSetsDatastoreTask: *t}
	case *admin_tasks.RecreateWebpageArchivesDatastoreTask:
		return &RecreateWebpageArchivesTask{RecreateWebpageArchivesDatastoreTask: *t}
	case *capture_skps.DatastoreTask:
		return &CaptureSkpsTask{DatastoreTask: *t}
	case *chromium_analysis.DatastoreTask:
		return &ChromiumAnalysisTask{DatastoreTask: *t}
	case *chromium_builds.DatastoreTask:
		return &ChromiumBuildTask{DatastoreTask: *t}
	case *chromium_perf.DatastoreTask:
		return &ChromiumPerfTask{DatastoreTask: *t}
	case *lua_scripts.DatastoreTask:
		return &LuaScriptTask{DatastoreTask: *t}
	case *metrics_analysis.DatastoreTask:
		return &MetricsAnalysisTask{DatastoreTask: *t}
	case *pixel_diff.DatastoreTask:
		return &PixelDiffTask{DatastoreTask: *t}
	default:
		sklog.Errorf("Missing case for %T in asPollerTask", otherTask)
		return nil
	}
}

// Notifies the frontend that task failed.
func updateWebappTaskSetFailed(task Task) error {
	updateVars := task.GetUpdateTaskVars()
	updateVars.GetUpdateTaskCommonVars().Id = task.GetCommonCols().DatastoreKey.ID
	updateVars.GetUpdateTaskCommonVars().SetCompleted(false)
	return frontend.UpdateWebappTaskV2(updateVars)
}

// pollAndExecOnce looks for the oldest pending task in CTFE. If one is found, then
// the local checkout is synced and built, and the picked up task is started in a
// go routine. The function returns without waiting for the task to finish and the
// WaitGroup of the goroutine is returned to the caller. The caller can then call
// wg.Wait() if they would like to wait for the task to finish.
func pollAndExecOnce(ctx context.Context, autoscaler ct_autoscaler.ICTAutoscaler) *sync.WaitGroup {
	pending, err := frontend.GetOldestPendingTaskV2()
	var wg sync.WaitGroup
	if err != nil {
		sklog.Error(err)
		return &wg
	}
	task := asPollerTask(ctx, pending)
	if task == nil {
		return &wg
	}

	taskId := fmt.Sprintf("%s.%d", task.GetTaskName(), task.GetCommonCols().DatastoreKey.ID)
	tasksMtx.Lock()
	_, exists := pickedUpTasks[taskId]
	tasksMtx.Unlock()
	if exists {
		return &wg
	}
	tasksMtx.Lock()
	pickedUpTasks[taskId] = "1"
	tasksMtx.Unlock()

	if task.RunsOnGCEWorkers() {
		if err := autoscaler.RegisterGCETask(taskId); err != nil {
			sklog.Errorf("Error when registering GCE task in CT autoscaler: %s", err)
			return &wg
		}
	}

	sklog.Infof("Preparing to execute task %s", taskId)
	if err = updateAndBuild(ctx); err != nil {
		sklog.Error(err)
		if task.RunsOnGCEWorkers() {
			if err := autoscaler.UnregisterGCETask(taskId); err != nil {
				sklog.Errorf("Error when unregistering GCE task in CT autoscaler: %s", err)
			}
		}
		return &wg
	}
	sklog.Infof("Executing task %s", taskId)
	// Increment the WaitGroup counter.
	wg.Add(1)
	go func() {
		// Decrement the counter when the goroutine completes.
		defer wg.Done()
		if err = task.Execute(ctx, ctutil.GetPatchFromStorage); err == nil {
			sklog.Infof("Completed task %s", taskId)
		} else {
			sklog.Errorf("Task %s failed: %s", taskId, err)
			if err := updateWebappTaskSetFailed(task); err != nil {
				sklog.Error(err)
			}
		}
		tasksMtx.Lock()
		delete(pickedUpTasks, taskId)
		tasksMtx.Unlock()

		if task.RunsOnGCEWorkers() {
			if err := autoscaler.UnregisterGCETask(taskId); err != nil {
				sklog.Errorf("Error when unregistering GCE task in CT autoscaler: %s", err)
			}
		}
	}()
	// Return the WaitGroup to allow some callers to call wg.Wait()
	return &wg
}

func main() {
	defer common.LogPanic()
	master_common.InitWithMetrics2("ct-poller", promPort)

	autoscaler, err := ct_autoscaler.NewCTAutoscaler()
	if err != nil {
		sklog.Fatalf("Could not instantiate the CT autoscaler: %s", err)
	}
	healthyGauge := metrics2.GetInt64Metric("healthy")

	// Terminate all tasks which were in running state when the poller was restarted.
	// See skbug.com/7062.
	if err := frontend.TerminateRunningTasks(); err != nil {
		sklog.Fatalf("Could not terminate running tasks: %s", err)
	}

	// Run immediately, since pollTick will not fire until after pollInterval.
	ctx := context.Background()
	pollAndExecOnce(ctx, autoscaler)
	for range time.Tick(*pollInterval) {
		healthyGauge.Update(1)
		pollAndExecOnce(ctx, autoscaler)
		// Sleeping for a second to avoid the small probability of ending up
		// with 2 tasks with the same runID. For context see
		// https://skia-review.googlesource.com/c/26941/8/ct/go/poller/main.go#96
		time.Sleep(time.Second)
	}
}

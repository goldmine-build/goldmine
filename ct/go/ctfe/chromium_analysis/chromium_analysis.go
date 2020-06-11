/*
	Handlers and types specific to Chromium analysis tasks.
*/

package chromium_analysis

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"cloud.google.com/go/datastore"
	"github.com/gorilla/mux"
	"go.skia.org/infra/ct/go/ctfe/task_common"
	ctfeutil "go.skia.org/infra/ct/go/ctfe/util"
	ctutil "go.skia.org/infra/ct/go/util"
	"go.skia.org/infra/go/ds"
	"go.skia.org/infra/go/email"
	"go.skia.org/infra/go/httputils"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/swarming"
	skutil "go.skia.org/infra/go/util"
	"google.golang.org/api/iterator"
)

var (
	addTaskTemplate     *template.Template = nil
	runsHistoryTemplate *template.Template = nil

	httpClient = httputils.NewTimeoutClient()
)

func ReloadTemplates(resourcesDir string) {
	addTaskTemplate = template.Must(template.ParseFiles(
		filepath.Join(resourcesDir, "templates/chromium_analysis.html"),
		filepath.Join(resourcesDir, "templates/header.html"),
		filepath.Join(resourcesDir, "templates/titlebar.html"),
	))
	runsHistoryTemplate = template.Must(template.ParseFiles(
		filepath.Join(resourcesDir, "templates/chromium_analysis_runs_history.html"),
		filepath.Join(resourcesDir, "templates/header.html"),
		filepath.Join(resourcesDir, "templates/titlebar.html"),
	))
}

type DatastoreTask struct {
	task_common.CommonCols

	Benchmark            string
	PageSets             string
	IsTestPageSet        bool
	BenchmarkArgs        string
	BrowserArgs          string
	Description          string
	CustomWebpagesGSPath string
	ChromiumPatchGSPath  string
	SkiaPatchGSPath      string
	CatapultPatchGSPath  string
	BenchmarkPatchGSPath string
	V8PatchGSPath        string
	RunInParallel        bool
	Platform             string
	RunOnGCE             bool
	RawOutput            string
	ValueColumnName      string
	MatchStdoutTxt       string
	ChromiumHash         string
	ApkGsPath            string
	TelemetryIsolateHash string
	CCList               []string
	TaskPriority         int
	GroupName            string
}

func (task DatastoreTask) GetTaskName() string {
	return "ChromiumAnalysis"
}

func (task DatastoreTask) GetDescription() string {
	return task.Description
}

func (task *DatastoreTask) GetPopulatedAddTaskVars() (task_common.AddTaskVars, error) {
	taskVars := &AddTaskVars{}
	taskVars.Username = task.Username
	taskVars.TsAdded = ctutil.GetCurrentTs()
	taskVars.RepeatAfterDays = strconv.FormatInt(task.RepeatAfterDays, 10)
	taskVars.Benchmark = task.Benchmark
	taskVars.PageSets = task.PageSets
	taskVars.BenchmarkArgs = task.BenchmarkArgs
	taskVars.BrowserArgs = task.BrowserArgs
	taskVars.Description = task.Description

	var err error
	taskVars.CustomWebpages, err = ctutil.GetPatchFromStorage(task.CustomWebpagesGSPath)
	if err != nil {
		return nil, fmt.Errorf("Could not read from %s: %s", task.CustomWebpagesGSPath, err)
	}
	taskVars.ChromiumPatch, err = ctutil.GetPatchFromStorage(task.ChromiumPatchGSPath)
	if err != nil {
		return nil, fmt.Errorf("Could not read from %s: %s", task.ChromiumPatchGSPath, err)
	}
	taskVars.SkiaPatch, err = ctutil.GetPatchFromStorage(task.SkiaPatchGSPath)
	if err != nil {
		return nil, fmt.Errorf("Could not read from %s: %s", task.SkiaPatchGSPath, err)
	}
	taskVars.CatapultPatch, err = ctutil.GetPatchFromStorage(task.CatapultPatchGSPath)
	if err != nil {
		return nil, fmt.Errorf("Could not read from %s: %s", task.CatapultPatchGSPath, err)
	}
	taskVars.BenchmarkPatch, err = ctutil.GetPatchFromStorage(task.BenchmarkPatchGSPath)
	if err != nil {
		return nil, fmt.Errorf("Could not read from %s: %s", task.BenchmarkPatchGSPath, err)
	}
	taskVars.V8Patch, err = ctutil.GetPatchFromStorage(task.V8PatchGSPath)
	if err != nil {
		return nil, fmt.Errorf("Could not read from %s: %s", task.V8PatchGSPath, err)
	}

	taskVars.RunInParallel = task.RunInParallel
	taskVars.Platform = task.Platform
	taskVars.RunOnGCE = task.RunOnGCE
	taskVars.ValueColumnName = task.ValueColumnName
	taskVars.MatchStdoutTxt = task.MatchStdoutTxt
	taskVars.ChromiumHash = task.ChromiumHash
	taskVars.ApkGsPath = task.ApkGsPath
	taskVars.TelemetryIsolateHash = task.TelemetryIsolateHash
	taskVars.CCList = task.CCList
	taskVars.TaskPriority = strconv.Itoa(task.TaskPriority)
	taskVars.GroupName = task.GroupName
	return taskVars, nil
}

func (task DatastoreTask) GetResultsLink() string {
	return task.RawOutput
}

func (task DatastoreTask) RunsOnGCEWorkers() bool {
	return task.RunOnGCE && task.Platform != ctutil.PLATFORM_ANDROID
}

func (task DatastoreTask) GetDatastoreKind() ds.Kind {
	return ds.CHROMIUM_ANALYSIS_TASKS
}

func (task DatastoreTask) Query(it *datastore.Iterator) (interface{}, error) {
	tasks := []*DatastoreTask{}
	for {
		t := &DatastoreTask{}
		_, err := it.Next(t)
		if err == iterator.Done {
			break
		} else if err != nil {
			return nil, fmt.Errorf("Failed to retrieve list of tasks: %s", err)
		}
		tasks = append(tasks, t)
	}

	return tasks, nil
}

func (task DatastoreTask) Get(c context.Context, key *datastore.Key) (task_common.Task, error) {
	t := &DatastoreTask{}
	if err := ds.DS.Get(c, key, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (task DatastoreTask) TriggerSwarmingTaskAndMail(ctx context.Context, swarmingClient swarming.ApiClient) error {
	runID := task_common.GetRunID(&task)
	emails := task_common.GetEmailRecipients(task.Username, task.CCList)
	cmd := []string{
		"cipd_bin_packages/luci-auth",
		"context",
		"--",
		"bin/run_chromium_analysis_on_workers",
		"-logtostderr",
		"--pageset_type=" + task.PageSets,
		"--benchmark_name=" + task.Benchmark,
		"--benchmark_extra_args=" + task.BenchmarkArgs,
		"--browser_extra_args=" + task.BrowserArgs,
		"--run_in_parallel=" + strconv.FormatBool(task.RunInParallel),
		"--target_platform=" + task.Platform,
		"--run_on_gce=" + strconv.FormatBool(task.RunsOnGCEWorkers()),
		"--match_stdout_txt=" + task.MatchStdoutTxt,
		"--chromium_hash=" + task.ChromiumHash,
		"--run_id=" + runID,
		"--task_priority=" + strconv.Itoa(task.TaskPriority),
		"--group_name=" + task.GroupName,
		"--chromium_patch_gs_path=" + task.ChromiumPatchGSPath,
		"--apk_gs_path=" + task.ApkGsPath,
		"--telemetry_isolate_hash=" + task.TelemetryIsolateHash,
		"--skia_patch_gs_path=" + task.SkiaPatchGSPath,
		"--v8_patch_gs_path=" + task.V8PatchGSPath,
		"--catapult_patch_gs_path=" + task.CatapultPatchGSPath,
		"--custom_webpages_csv_gs_path=" + task.CustomWebpagesGSPath,
		"--value_column_name=" + task.ValueColumnName,
	}

	sTaskID, err := ctutil.TriggerMasterScriptSwarmingTask(ctx, runID, "run_chromium_analysis_on_workers", ctutil.CHROMIUM_ANALYSIS_MASTER_ISOLATE, task_common.ServiceAccountFile, task.Platform, false, cmd, swarmingClient)
	if err != nil {
		return fmt.Errorf("Could not trigger master script for run_chromium_analysis_on_workers with cmd %v: %s", cmd, err)
	}
	// Mark task as started in datastore.
	if err := task_common.UpdateTaskSetStarted(ctx, runID, sTaskID, &task); err != nil {
		return fmt.Errorf("Could not mark task as started in datastore: %s", err)
	}
	// Send start email.
	skutil.LogErr(ctfeutil.SendTaskStartEmail(task.DatastoreKey.ID, emails, "Chromium analysis", runID, task.Description, fmt.Sprintf("Triggered %s benchmark on %s %s pageset.", task.Benchmark, task.Platform, task.PageSets)))
	return nil
}

func (task DatastoreTask) SendCompletionEmail(ctx context.Context, completedSuccessfully bool) error {
	runID := task_common.GetRunID(&task)
	emails := task_common.GetEmailRecipients(task.Username, task.CCList)
	emailSubject := fmt.Sprintf("Cluster telemetry chromium analysis task has completed (#%d)", task.DatastoreKey.ID)
	failureHtml := ""
	viewActionMarkup := ""
	ctPerfHtml := ""
	var err error

	if completedSuccessfully {
		if viewActionMarkup, err = email.GetViewActionMarkup(task.RawOutput, "View Results", "Direct link to the CSV results"); err != nil {
			return fmt.Errorf("Failed to get view action markup: %s", err)
		}
		ctPerfHtml = ctfeutil.GetCTPerfEmailHtml(task.GroupName)
	} else {
		emailSubject += " with failures"
		failureHtml = ctfeutil.GetFailureEmailHtml(runID)
		if viewActionMarkup, err = email.GetViewActionMarkup(fmt.Sprintf(ctutil.SWARMING_RUN_ID_ALL_TASKS_LINK_TEMPLATE, runID), "View Failure", "Direct link to the swarming logs"); err != nil {
			return fmt.Errorf("Failed to get view action markup: %s", err)
		}
	}

	// Instantiate GcsUtil object and use to calculate number of archives
	gs, err := ctutil.NewGcsUtil(nil)
	if err != nil {
		return fmt.Errorf("Could not instantiate gsutil object: %s", err)
	}
	totalArchivedWebpages, err := ctutil.GetArchivesNum(gs, task.BenchmarkArgs, task.PageSets)
	if err != nil {
		sklog.Errorf("Error when calculating number of archives: %s", err)
		totalArchivedWebpages = -1
	}
	archivedWebpagesText := ""
	if totalArchivedWebpages != -1 {
		archivedWebpagesText = fmt.Sprintf(" %d WPR archives were used.", totalArchivedWebpages)
	}

	bodyTemplate := `
	The chromium analysis %s benchmark task on %s pageset has completed. %s.<br/>
	Run description: %s<br/>
	%s
	%s
	The CSV output is <a href='%s'>here</a>.%s<br/>
	The patch(es) you specified are here:
	<a href='%s'>chromium</a>/<a href='%s'>skia</a>/<a href='%s'>v8</a>/<a href='%s'>catapult</a>
	<br/>
	Custom webpages (if specified) are <a href='%s'>here</a>.
	<br/><br/>
	You can schedule more runs <a href='%s'>here</a>.
	<br/><br/>
	Thanks!
	`
	chromiumPatchLink := ctutil.GCS_HTTP_LINK + path.Join(ctutil.GCSBucketName, task.ChromiumPatchGSPath)
	skiaPatchLink := ctutil.GCS_HTTP_LINK + path.Join(ctutil.GCSBucketName, task.SkiaPatchGSPath)
	v8PatchLink := ctutil.GCS_HTTP_LINK + path.Join(ctutil.GCSBucketName, task.V8PatchGSPath)
	catapultPatchLink := ctutil.GCS_HTTP_LINK + path.Join(ctutil.GCSBucketName, task.CatapultPatchGSPath)
	customWebpagesLink := ctutil.GCS_HTTP_LINK + path.Join(ctutil.GCSBucketName, task.CustomWebpagesGSPath)
	emailBody := fmt.Sprintf(bodyTemplate, task.Benchmark, task.PageSets, ctfeutil.GetSwarmingLogsLink(runID), task.Description, failureHtml, ctPerfHtml, task.RawOutput, archivedWebpagesText, chromiumPatchLink, skiaPatchLink, v8PatchLink, catapultPatchLink, customWebpagesLink, task_common.WebappURL+ctfeutil.CHROMIUM_ANALYSIS_URI)
	if err := ctfeutil.SendEmailWithMarkup(emails, emailSubject, emailBody, viewActionMarkup); err != nil {
		return fmt.Errorf("Error while sending email: %s", err)
	}
	return nil
}

func (task *DatastoreTask) SetCompleted(success bool) {
	if success {
		runID := task_common.GetRunID(task)
		task.RawOutput = ctutil.GetAnalysisOutputLink(runID)
	}
	task.TsCompleted = ctutil.GetCurrentTsInt64()
	task.Failure = !success
	task.TaskDone = true
}

func addTaskView(w http.ResponseWriter, r *http.Request) {
	ctfeutil.ExecuteSimpleTemplate(addTaskTemplate, w, r)
}

type AddTaskVars struct {
	task_common.AddTaskCommonVars

	Benchmark            string   `json:"benchmark"`
	PageSets             string   `json:"page_sets"`
	CustomWebpages       string   `json:"custom_webpages"`
	BenchmarkArgs        string   `json:"benchmark_args"`
	BrowserArgs          string   `json:"browser_args"`
	Description          string   `json:"desc"`
	ChromiumPatch        string   `json:"chromium_patch"`
	SkiaPatch            string   `json:"skia_patch"`
	CatapultPatch        string   `json:"catapult_patch"`
	BenchmarkPatch       string   `json:"benchmark_patch"`
	V8Patch              string   `json:"v8_patch"`
	RunInParallel        bool     `json:"run_in_parallel"`
	Platform             string   `json:"platform"`
	RunOnGCE             bool     `json:"run_on_gce"`
	ValueColumnName      string   `json:"value_column_name"`
	MatchStdoutTxt       string   `json:"match_stdout_txt"`
	ChromiumHash         string   `json:"chromium_hash"`
	ApkGsPath            string   `json:"apk_gs_path"`
	TelemetryIsolateHash string   `json:"telemetry_isolate_hash"`
	CCList               []string `json:"cc_list"`
	TaskPriority         string   `json:"task_priority"`
	GroupName            string   `json:"group_name"`
}

func (task *AddTaskVars) GetDatastoreKind() ds.Kind {
	return ds.CHROMIUM_ANALYSIS_TASKS
}

func (task *AddTaskVars) GetPopulatedDatastoreTask(ctx context.Context) (task_common.Task, error) {
	if task.Benchmark == "" ||
		task.PageSets == "" ||
		task.Platform == "" ||
		task.Description == "" {
		return nil, fmt.Errorf("Invalid parameters")
	}
	if task.GroupName != "" && len(task.GroupName) >= ctfeutil.MAX_GROUPNAME_LEN {
		return nil, fmt.Errorf("Please limit group names to less than %d characters", ctfeutil.MAX_GROUPNAME_LEN)
	}
	customWebpagesSlice, err := ctfeutil.GetQualifiedCustomWebpages(task.CustomWebpages, task.BenchmarkArgs)
	if err != nil {
		return nil, err
	}
	customWebpages := strings.Join(customWebpagesSlice, ",")
	customWebpagesGSPath, err := ctutil.SavePatchToStorage(customWebpages)
	if err != nil {
		return nil, fmt.Errorf("Could not save custom webpages to storage: %s", err)
	}

	chromiumPatchGSPath, err := ctutil.SavePatchToStorage(task.ChromiumPatch)
	if err != nil {
		return nil, fmt.Errorf("Could not save chromium patch to storage: %s", err)
	}
	skiaPatchGSPath, err := ctutil.SavePatchToStorage(task.SkiaPatch)
	if err != nil {
		return nil, fmt.Errorf("Could not save skia patch to storage: %s", err)
	}
	catapultPatchGSPath, err := ctutil.SavePatchToStorage(task.CatapultPatch)
	if err != nil {
		return nil, fmt.Errorf("Could not save catapult patch to storage: %s", err)
	}
	benchmarkPatchGSPath, err := ctutil.SavePatchToStorage(task.BenchmarkPatch)
	if err != nil {
		return nil, fmt.Errorf("Could not save benchmark patch to storage: %s", err)
	}
	v8PatchGSPath, err := ctutil.SavePatchToStorage(task.V8Patch)
	if err != nil {
		return nil, fmt.Errorf("Could not save v8 patch to storage: %s", err)
	}

	t := &DatastoreTask{
		Benchmark:     task.Benchmark,
		PageSets:      task.PageSets,
		IsTestPageSet: task.PageSets == ctutil.PAGESET_TYPE_DUMMY_1k || task.PageSets == ctutil.PAGESET_TYPE_MOBILE_DUMMY_1k,
		BenchmarkArgs: task.BenchmarkArgs,
		BrowserArgs:   task.BrowserArgs,
		Description:   task.Description,

		CustomWebpagesGSPath: customWebpagesGSPath,
		ChromiumPatchGSPath:  chromiumPatchGSPath,
		SkiaPatchGSPath:      skiaPatchGSPath,
		CatapultPatchGSPath:  catapultPatchGSPath,
		BenchmarkPatchGSPath: benchmarkPatchGSPath,
		V8PatchGSPath:        v8PatchGSPath,

		RunInParallel:        task.RunInParallel,
		Platform:             task.Platform,
		RunOnGCE:             task.RunOnGCE,
		ValueColumnName:      task.ValueColumnName,
		MatchStdoutTxt:       task.MatchStdoutTxt,
		ChromiumHash:         task.ChromiumHash,
		ApkGsPath:            task.ApkGsPath,
		TelemetryIsolateHash: task.TelemetryIsolateHash,
		CCList:               task.CCList,
		GroupName:            task.GroupName,
	}
	taskPriority, err := strconv.Atoi(task.TaskPriority)
	if err != nil {
		return nil, fmt.Errorf("%s is not int: %s", task.TaskPriority, err)
	}
	if taskPriority == 0 {
		// This should only happen for repeating tasks that were created before
		// support for task priorities was added to CT.
		// Triggering tasks with 0 priority fails in swarming with
		// "priority 0 can only be used for terminate request"
		// Override it to the medium priority.
		taskPriority = ctutil.TASKS_PRIORITY_MEDIUM
	}
	t.TaskPriority = taskPriority
	return t, nil
}

func addTaskHandler(w http.ResponseWriter, r *http.Request) {
	task_common.AddTaskHandler(w, r, &AddTaskVars{})
}

func getTasksHandler(w http.ResponseWriter, r *http.Request) {
	task_common.GetTasksHandler(&DatastoreTask{}, w, r)
}

func deleteTaskHandler(w http.ResponseWriter, r *http.Request) {
	task_common.DeleteTaskHandler(&DatastoreTask{}, w, r)
}

func redoTaskHandler(w http.ResponseWriter, r *http.Request) {
	task_common.RedoTaskHandler(&DatastoreTask{}, w, r)
}

func runsHistoryView(w http.ResponseWriter, r *http.Request) {
	ctfeutil.ExecuteSimpleTemplate(runsHistoryTemplate, w, r)
}

func AddHandlers(externalRouter *mux.Router) {
	externalRouter.HandleFunc("/"+ctfeutil.CHROMIUM_ANALYSIS_URI, addTaskView).Methods("GET")
	externalRouter.HandleFunc("/"+ctfeutil.CHROMIUM_ANALYSIS_RUNS_URI, runsHistoryView).Methods("GET")

	externalRouter.HandleFunc("/"+ctfeutil.ADD_CHROMIUM_ANALYSIS_TASK_POST_URI, addTaskHandler).Methods("POST")
	externalRouter.HandleFunc("/"+ctfeutil.GET_CHROMIUM_ANALYSIS_TASKS_POST_URI, getTasksHandler).Methods("POST")
	externalRouter.HandleFunc("/"+ctfeutil.DELETE_CHROMIUM_ANALYSIS_TASK_POST_URI, deleteTaskHandler).Methods("POST")
	externalRouter.HandleFunc("/"+ctfeutil.REDO_CHROMIUM_ANALYSIS_TASK_POST_URI, redoTaskHandler).Methods("POST")
}

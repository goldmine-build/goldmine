/*
	Handlers and types specific to Metrics analysis tasks.
*/

package metrics_analysis

import (
	"context"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"text/template"

	"cloud.google.com/go/datastore"
	"github.com/gorilla/mux"
	"go.skia.org/infra/ct/go/ctfe/chromium_analysis"
	"go.skia.org/infra/ct/go/ctfe/task_common"
	ctfeutil "go.skia.org/infra/ct/go/ctfe/util"
	ctutil "go.skia.org/infra/ct/go/util"
	"go.skia.org/infra/go/ds"
	"go.skia.org/infra/go/email"
	"go.skia.org/infra/go/httputils"
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
		filepath.Join(resourcesDir, "templates/metrics_analysis.html"),
		filepath.Join(resourcesDir, "templates/header.html"),
		filepath.Join(resourcesDir, "templates/titlebar.html"),
	))
	runsHistoryTemplate = template.Must(template.ParseFiles(
		filepath.Join(resourcesDir, "templates/metrics_analysis_runs_history.html"),
		filepath.Join(resourcesDir, "templates/header.html"),
		filepath.Join(resourcesDir, "templates/titlebar.html"),
	))
}

type DatastoreTask struct {
	task_common.CommonCols

	MetricName          string
	AnalysisTaskId      string
	AnalysisOutputLink  string
	BenchmarkArgs       string
	Description         string
	CustomTracesGSPath  string
	ChromiumPatchGSPath string
	CatapultPatchGSPath string
	RawOutput           string
	ValueColumnName     string
	CCList              []string
	TaskPriority        int
}

func (task DatastoreTask) GetTaskName() string {
	return "MetricsAnalysis"
}

func (task DatastoreTask) GetPopulatedAddTaskVars() (task_common.AddTaskVars, error) {
	taskVars := &AddTaskVars{}
	taskVars.Username = task.Username
	taskVars.TsAdded = ctutil.GetCurrentTs()
	taskVars.RepeatAfterDays = strconv.FormatInt(task.RepeatAfterDays, 10)
	taskVars.MetricName = task.MetricName
	taskVars.AnalysisTaskId = task.AnalysisTaskId
	taskVars.AnalysisOutputLink = task.AnalysisOutputLink
	taskVars.ValueColumnName = task.ValueColumnName
	taskVars.BenchmarkArgs = task.BenchmarkArgs
	taskVars.Description = task.Description
	taskVars.CCList = task.CCList
	taskVars.TaskPriority = strconv.Itoa(task.TaskPriority)

	var err error
	taskVars.CustomTraces, err = ctutil.GetPatchFromStorage(task.CustomTracesGSPath)
	if err != nil {
		return nil, fmt.Errorf("Could not read from %s: %s", task.CustomTracesGSPath, err)
	}
	taskVars.ChromiumPatch, err = ctutil.GetPatchFromStorage(task.ChromiumPatchGSPath)
	if err != nil {
		return nil, fmt.Errorf("Could not read from %s: %s", task.ChromiumPatchGSPath, err)
	}
	taskVars.CatapultPatch, err = ctutil.GetPatchFromStorage(task.CatapultPatchGSPath)
	if err != nil {
		return nil, fmt.Errorf("Could not read from %s: %s", task.CatapultPatchGSPath, err)
	}

	return taskVars, nil
}

func (task DatastoreTask) GetResultsLink() string {
	return task.RawOutput
}

func (task DatastoreTask) RunsOnGCEWorkers() bool {
	return true
}

func (task DatastoreTask) GetDatastoreKind() ds.Kind {
	return ds.METRICS_ANALYSIS_TASKS
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

func (task DatastoreTask) TriggerSwarmingTaskAndMail(ctx context.Context) error {
	runID := task_common.GetRunID(&task)
	emails := task_common.GetEmailRecipients(task.Username, task.CCList)
	isolateArgs := map[string]string{
		"METRIC_NAME":               task.MetricName,
		"ANALYSIS_OUTPUT_LINK":      task.AnalysisOutputLink,
		"BENCHMARK_ARGS":            task.BenchmarkArgs,
		"VALUE_COLUMN_NAME":         task.ValueColumnName,
		"RUN_ID":                    runID,
		"TASK_PRIORITY":             strconv.Itoa(task.TaskPriority),
		"CHROMIUM_PATCH_GS_PATH":    task.ChromiumPatchGSPath,
		"CATAPULT_PATCH_GS_PATH":    task.CatapultPatchGSPath,
		"CUSTOM_TRACES_CSV_GS_PATH": task.CustomTracesGSPath,
	}

	sTaskID, err := ctutil.TriggerMasterScriptSwarmingTask(ctx, runID, "metrics_analysis_on_workers", ctutil.METRICS_ANALYSIS_MASTER_ISOLATE, task_common.ServiceAccountFile, ctutil.PLATFORM_LINUX, false, isolateArgs)
	if err != nil {
		return fmt.Errorf("Could not trigger master script for metrics_analysis_on_workers with isolate args %v: %s", isolateArgs, err)
	}
	// Mark task as started in datastore.
	if err := task_common.UpdateTaskSetStarted(ctx, runID, sTaskID, &task); err != nil {
		return fmt.Errorf("Could not mark task as started in datastore: %s", err)
	}
	// Send start email.
	skutil.LogErr(ctutil.SendTaskStartEmail(task.DatastoreKey.ID, emails, "Metrics analysis", runID, task.Description, ""))
	return nil
}

func (task DatastoreTask) SendCompletionEmail(ctx context.Context, completedSuccessfully bool) error {
	runID := task_common.GetRunID(&task)
	emails := task_common.GetEmailRecipients(task.Username, task.CCList)
	emailSubject := fmt.Sprintf("Metrics analysis cluster telemetry task has completed (#%d)", task.DatastoreKey.ID)
	failureHtml := ""
	viewActionMarkup := ""
	var err error

	if completedSuccessfully {
		if viewActionMarkup, err = email.GetViewActionMarkup(task.RawOutput, "View Results", "Direct link to the CSV results"); err != nil {
			return fmt.Errorf("Failed to get view action markup: %s", err)
		}
	} else {
		emailSubject += " with failures"
		failureHtml = ctutil.GetFailureEmailHtml(runID)
		if viewActionMarkup, err = email.GetViewActionMarkup(fmt.Sprintf(ctutil.SWARMING_RUN_ID_ALL_TASKS_LINK_TEMPLATE, runID), "View Failure", "Direct link to the swarming logs"); err != nil {
			return fmt.Errorf("Failed to get view action markup: %s", err)
		}
	}

	bodyTemplate := `
	The metrics analysis task has completed. %s.<br/>
	Run description: %s<br/>
	%s
	The CSV output is <a href='%s'>here</a>.<br/>
	The patch(es) you specified are here:
	<a href='%s'>chromium</a>/<a href='%s'>catapult</a>
	<br/>
	Traces used for this run are <a href='%s'>here</a>.
	<br/><br/>
	You can schedule more runs <a href='%s'>here</a>.
	<br/><br/>
	Thanks!
	`
	chromiumPatchLink := ctutil.GCS_HTTP_LINK + path.Join(ctutil.GCSBucketName, task.ChromiumPatchGSPath)
	catapultPatchLink := ctutil.GCS_HTTP_LINK + path.Join(ctutil.GCSBucketName, task.CatapultPatchGSPath)
	tracesLink := ctutil.GCS_HTTP_LINK + path.Join(ctutil.GCSBucketName, task.CustomTracesGSPath)
	emailBody := fmt.Sprintf(bodyTemplate, ctutil.GetSwarmingLogsLink(runID), task.Description, failureHtml, task.RawOutput, chromiumPatchLink, catapultPatchLink, tracesLink, task_common.WebappURL+ctfeutil.METRICS_ANALYSIS_URI)
	if err := ctutil.SendEmailWithMarkup(emails, emailSubject, emailBody, viewActionMarkup); err != nil {
		return fmt.Errorf("Error while sending email: %s", err)
	}
	return nil
}

func (task *DatastoreTask) SetCompleted(success bool) {
	if success {
		runID := task_common.GetRunID(task)
		task.RawOutput = ctutil.GetMetricsAnalysisOutputLink(runID)
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

	MetricName         string   `json:"metric_name"`
	CustomTraces       string   `json:"custom_traces"`
	AnalysisTaskId     string   `json:"analysis_task_id"`
	AnalysisOutputLink string   `json:"analysis_output_link"`
	BenchmarkArgs      string   `json:"benchmark_args"`
	Description        string   `json:"desc"`
	ChromiumPatch      string   `json:"chromium_patch"`
	CatapultPatch      string   `json:"catapult_patch"`
	ValueColumnName    string   `json:"value_column_name"`
	CCList             []string `json:"cc_list"`
	TaskPriority       string   `json:"task_priority"`
}

func (task *AddTaskVars) GetDatastoreKind() ds.Kind {
	return ds.METRICS_ANALYSIS_TASKS
}

func (task *AddTaskVars) GetPopulatedDatastoreTask(ctx context.Context) (task_common.Task, error) {
	if task.MetricName == "" {
		return nil, fmt.Errorf("Must specify metric name")
	}
	if task.CustomTraces == "" && task.AnalysisTaskId == "" {
		return nil, fmt.Errorf("Must specify one of custom traces or analysis task id")
	}
	if task.Description == "" {
		return nil, fmt.Errorf("Must specify description")
	}

	if task.AnalysisTaskId != "" && task.AnalysisTaskId != "0" {
		// Get analysis output link from analysis task id.
		key := ds.NewKey(ds.CHROMIUM_ANALYSIS_TASKS)
		id, err := strconv.ParseInt(task.AnalysisTaskId, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("%s is not an int64: %s", task.AnalysisTaskId, err)
		}
		key.ID = id
		analysisTask := &chromium_analysis.DatastoreTask{}
		if err := ds.DS.Get(ctx, key, analysisTask); err != nil {
			return nil, fmt.Errorf("Unable to find requested analysis task id.")
		}
		task.AnalysisOutputLink = analysisTask.RawOutput
	}

	customTracesGSPath, err := ctutil.SavePatchToStorage(task.CustomTraces)
	if err != nil {
		return nil, fmt.Errorf("Could not save custom traces to storage: %s", err)
	}
	chromiumPatchGSPath, err := ctutil.SavePatchToStorage(task.ChromiumPatch)
	if err != nil {
		return nil, fmt.Errorf("Could not save chromium patch to storage: %s", err)
	}
	catapultPatchGSPath, err := ctutil.SavePatchToStorage(task.CatapultPatch)
	if err != nil {
		return nil, fmt.Errorf("Could not save catapult patch to storage: %s", err)
	}

	t := &DatastoreTask{
		MetricName:         task.MetricName,
		AnalysisTaskId:     task.AnalysisTaskId,
		AnalysisOutputLink: task.AnalysisOutputLink,
		ValueColumnName:    task.ValueColumnName,
		BenchmarkArgs:      task.BenchmarkArgs,
		Description:        task.Description,
		CCList:             task.CCList,

		CustomTracesGSPath:  customTracesGSPath,
		ChromiumPatchGSPath: chromiumPatchGSPath,
		CatapultPatchGSPath: catapultPatchGSPath,
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
	externalRouter.HandleFunc("/"+ctfeutil.METRICS_ANALYSIS_URI, addTaskView).Methods("GET")
	externalRouter.HandleFunc("/"+ctfeutil.METRICS_ANALYSIS_RUNS_URI, runsHistoryView).Methods("GET")

	externalRouter.HandleFunc("/"+ctfeutil.ADD_METRICS_ANALYSIS_TASK_POST_URI, addTaskHandler).Methods("POST")
	externalRouter.HandleFunc("/"+ctfeutil.GET_METRICS_ANALYSIS_TASKS_POST_URI, getTasksHandler).Methods("POST")
	externalRouter.HandleFunc("/"+ctfeutil.DELETE_METRICS_ANALYSIS_TASK_POST_URI, deleteTaskHandler).Methods("POST")
	externalRouter.HandleFunc("/"+ctfeutil.REDO_METRICS_ANALYSIS_TASK_POST_URI, redoTaskHandler).Methods("POST")
}

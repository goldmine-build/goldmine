/*
	Handlers and types specific to Chromium analysis tasks.
*/

package chromium_analysis

import (
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/gorilla/mux"

	"go.skia.org/infra/ct/go/ctfe/task_common"
	ctfeutil "go.skia.org/infra/ct/go/ctfe/util"
	"go.skia.org/infra/ct/go/db"
	ctutil "go.skia.org/infra/ct/go/util"
	"go.skia.org/infra/go/httputils"
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

type DBTask struct {
	task_common.CommonCols

	Benchmark      string         `db:"benchmark"`
	PageSets       string         `db:"page_sets"`
	CustomWebpages string         `db:"custom_webpages"`
	BenchmarkArgs  string         `db:"benchmark_args"`
	BrowserArgs    string         `db:"browser_args"`
	Description    string         `db:"description"`
	ChromiumPatch  string         `db:"chromium_patch"`
	CatapultPatch  string         `db:"catapult_patch"`
	BenchmarkPatch string         `db:"benchmark_patch"`
	V8Patch        string         `db:"v8_patch"`
	RunInParallel  bool           `db:"run_in_parallel"`
	Platform       string         `db:"platform"`
	RunOnGCE       bool           `db:"run_on_gce"`
	RawOutput      sql.NullString `db:"raw_output"`
	MatchStdoutTxt string         `db:"match_stdout_txt"`
}

func (task DBTask) GetTaskName() string {
	return "ChromiumAnalysis"
}

func (dbTask DBTask) GetPopulatedAddTaskVars() task_common.AddTaskVars {
	taskVars := &AddTaskVars{}
	taskVars.Username = dbTask.Username
	taskVars.TsAdded = ctutil.GetCurrentTs()
	taskVars.RepeatAfterDays = strconv.FormatInt(dbTask.RepeatAfterDays, 10)
	taskVars.Benchmark = dbTask.Benchmark
	taskVars.PageSets = dbTask.PageSets
	taskVars.CustomWebpages = dbTask.CustomWebpages
	taskVars.BenchmarkArgs = dbTask.BenchmarkArgs
	taskVars.BrowserArgs = dbTask.BrowserArgs
	taskVars.Description = dbTask.Description
	taskVars.ChromiumPatch = dbTask.ChromiumPatch
	taskVars.CatapultPatch = dbTask.CatapultPatch
	taskVars.BenchmarkPatch = dbTask.BenchmarkPatch
	taskVars.V8Patch = dbTask.V8Patch
	taskVars.RunInParallel = dbTask.RunInParallel
	taskVars.Platform = dbTask.Platform
	taskVars.RunOnGCE = dbTask.RunOnGCE
	taskVars.MatchStdoutTxt = dbTask.MatchStdoutTxt
	return taskVars
}

func (task DBTask) GetResultsLink() string {
	if task.RawOutput.Valid {
		return task.RawOutput.String
	} else {
		return ""
	}
}

func (task DBTask) GetUpdateTaskVars() task_common.UpdateTaskVars {
	return &UpdateVars{}
}

func (task DBTask) RunsOnGCEWorkers() bool {
	return task.RunOnGCE && task.Platform != ctutil.PLATFORM_ANDROID
}

func (task DBTask) TableName() string {
	return db.TABLE_CHROMIUM_ANALYSIS_TASKS
}

func (task DBTask) Select(query string, args ...interface{}) (interface{}, error) {
	result := []DBTask{}
	err := db.DB.Select(&result, query, args...)
	return result, err
}

func addTaskView(w http.ResponseWriter, r *http.Request) {
	ctfeutil.ExecuteSimpleTemplate(addTaskTemplate, w, r)
}

type AddTaskVars struct {
	task_common.AddTaskCommonVars

	Benchmark      string `json:"benchmark"`
	PageSets       string `json:"page_sets"`
	CustomWebpages string `json:"custom_webpages"`
	BenchmarkArgs  string `json:"benchmark_args"`
	BrowserArgs    string `json:"browser_args"`
	Description    string `json:"desc"`
	ChromiumPatch  string `json:"chromium_patch"`
	CatapultPatch  string `json:"catapult_patch"`
	BenchmarkPatch string `json:"benchmark_patch"`
	V8Patch        string `json:"v8_patch"`
	RunInParallel  bool   `json:"run_in_parallel"`
	Platform       string `json:"platform"`
	RunOnGCE       bool   `json:"run_on_gce"`
	MatchStdoutTxt string `json:"match_stdout_txt"`
}

func (task *AddTaskVars) GetInsertQueryAndBinds() (string, []interface{}, error) {
	if task.Benchmark == "" ||
		task.PageSets == "" ||
		task.Platform == "" ||
		task.Description == "" {
		return "", nil, fmt.Errorf("Invalid parameters")
	}
	customWebpages, err := ctfeutil.GetQualifiedCustomWebpages(task.CustomWebpages, task.BenchmarkArgs)
	if err != nil {
		return "", nil, err
	}
	if err := ctfeutil.CheckLengths([]ctfeutil.LengthCheck{
		{Name: "benchmark", Value: task.Benchmark, Limit: 100},
		{Name: "platform", Value: task.Platform, Limit: 100},
		{Name: "page_sets", Value: task.PageSets, Limit: 100},
		{Name: "benchmark_args", Value: task.BenchmarkArgs, Limit: 255},
		{Name: "browser_args", Value: task.BrowserArgs, Limit: 255},
		{Name: "desc", Value: task.Description, Limit: 255},
		{Name: "custom_webpages", Value: strings.Join(customWebpages, ","), Limit: db.LONG_TEXT_MAX_LENGTH},
		{Name: "chromium_patch", Value: task.ChromiumPatch, Limit: db.LONG_TEXT_MAX_LENGTH},
		{Name: "catapult_patch", Value: task.CatapultPatch, Limit: db.LONG_TEXT_MAX_LENGTH},
		{Name: "benchmark_patch", Value: task.BenchmarkPatch, Limit: db.LONG_TEXT_MAX_LENGTH},
		{Name: "v8_patch", Value: task.V8Patch, Limit: db.LONG_TEXT_MAX_LENGTH},
		{Name: "match_stdout_txt", Value: task.MatchStdoutTxt, Limit: db.LONG_TEXT_MAX_LENGTH},
	}); err != nil {
		return "", nil, err
	}
	runInParallel := 0
	if task.RunInParallel {
		runInParallel = 1
	}
	runOnGCE := 0
	if task.RunOnGCE {
		runOnGCE = 1
	}
	return fmt.Sprintf("INSERT INTO %s (username,benchmark,page_sets,custom_webpages,benchmark_args,browser_args,description,chromium_patch,catapult_patch,benchmark_patch,v8_patch,ts_added,repeat_after_days,run_in_parallel,platform,run_on_gce,match_stdout_txt) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?);",
			db.TABLE_CHROMIUM_ANALYSIS_TASKS),
		[]interface{}{
			task.Username,
			task.Benchmark,
			task.PageSets,
			strings.Join(customWebpages, ","),
			task.BenchmarkArgs,
			task.BrowserArgs,
			task.Description,
			task.ChromiumPatch,
			task.CatapultPatch,
			task.BenchmarkPatch,
			task.V8Patch,
			task.TsAdded,
			task.RepeatAfterDays,
			runInParallel,
			task.Platform,
			runOnGCE,
			task.MatchStdoutTxt,
		},
		nil
}

func addTaskHandler(w http.ResponseWriter, r *http.Request) {
	task_common.AddTaskHandler(w, r, &AddTaskVars{})
}

func getTasksHandler(w http.ResponseWriter, r *http.Request) {
	task_common.GetTasksHandler(&DBTask{}, w, r)
}

type UpdateVars struct {
	task_common.UpdateTaskCommonVars

	RawOutput sql.NullString
}

func (vars *UpdateVars) UriPath() string {
	return ctfeutil.UPDATE_CHROMIUM_ANALYSIS_TASK_POST_URI
}

func (task *UpdateVars) GetUpdateExtraClausesAndBinds() ([]string, []interface{}, error) {
	if err := ctfeutil.CheckLengths([]ctfeutil.LengthCheck{
		{Name: "RawOutput", Value: task.RawOutput.String, Limit: 255},
	}); err != nil {
		return nil, nil, err
	}
	clauses := []string{}
	args := []interface{}{}
	if task.RawOutput.Valid {
		clauses = append(clauses, "raw_output = ?")
		args = append(args, task.RawOutput.String)
	}
	return clauses, args, nil
}

func updateTaskHandler(w http.ResponseWriter, r *http.Request) {
	task_common.UpdateTaskHandler(&UpdateVars{}, db.TABLE_CHROMIUM_ANALYSIS_TASKS, w, r)
}

func deleteTaskHandler(w http.ResponseWriter, r *http.Request) {
	task_common.DeleteTaskHandler(&DBTask{}, w, r)
}

func redoTaskHandler(w http.ResponseWriter, r *http.Request) {
	task_common.RedoTaskHandler(&DBTask{}, w, r)
}

func runsHistoryView(w http.ResponseWriter, r *http.Request) {
	ctfeutil.ExecuteSimpleTemplate(runsHistoryTemplate, w, r)
}

func AddHandlers(r *mux.Router) {
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.CHROMIUM_ANALYSIS_URI, "GET", addTaskView)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.CHROMIUM_ANALYSIS_RUNS_URI, "GET", runsHistoryView)

	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.ADD_CHROMIUM_ANALYSIS_TASK_POST_URI, "POST", addTaskHandler)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.GET_CHROMIUM_ANALYSIS_TASKS_POST_URI, "POST", getTasksHandler)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.DELETE_CHROMIUM_ANALYSIS_TASK_POST_URI, "POST", deleteTaskHandler)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.REDO_CHROMIUM_ANALYSIS_TASK_POST_URI, "POST", redoTaskHandler)

	// Do not add force login handler for update methods. They use webhooks for authentication.
	r.HandleFunc("/"+ctfeutil.UPDATE_CHROMIUM_ANALYSIS_TASK_POST_URI, updateTaskHandler).Methods("POST")
}

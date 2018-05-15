/*
	Handlers and types specific to capturing SKP repositories.
*/

package capture_skps

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"text/template"

	"github.com/gorilla/mux"
	"go.skia.org/infra/go/sklog"

	"go.skia.org/infra/ct/go/ctfe/chromium_builds"
	"go.skia.org/infra/ct/go/ctfe/task_common"
	ctfeutil "go.skia.org/infra/ct/go/ctfe/util"
	"go.skia.org/infra/ct/go/db"
	ctutil "go.skia.org/infra/ct/go/util"
)

var (
	addTaskTemplate     *template.Template = nil
	runsHistoryTemplate *template.Template = nil
)

func ReloadTemplates(resourcesDir string) {
	addTaskTemplate = template.Must(template.ParseFiles(
		filepath.Join(resourcesDir, "templates/capture_skps.html"),
		filepath.Join(resourcesDir, "templates/header.html"),
		filepath.Join(resourcesDir, "templates/titlebar.html"),
	))
	runsHistoryTemplate = template.Must(template.ParseFiles(
		filepath.Join(resourcesDir, "templates/capture_skp_runs_history.html"),
		filepath.Join(resourcesDir, "templates/header.html"),
		filepath.Join(resourcesDir, "templates/titlebar.html"),
	))
}

type DBTask struct {
	task_common.CommonCols

	PageSets    string `db:"page_sets"`
	ChromiumRev string `db:"chromium_rev"`
	SkiaRev     string `db:"skia_rev"`
	Description string `db:"description"`
}

func (task DBTask) GetTaskName() string {
	return "CaptureSkps"
}

func (task DBTask) GetResultsLink() string {
	return ""
}

func (dbTask DBTask) GetPopulatedAddTaskVars() task_common.AddTaskVars {
	taskVars := &AddTaskVars{}
	taskVars.Username = dbTask.Username
	taskVars.TsAdded = ctutil.GetCurrentTs()
	taskVars.RepeatAfterDays = strconv.FormatInt(dbTask.RepeatAfterDays, 10)
	taskVars.PageSets = dbTask.PageSets
	taskVars.ChromiumBuild.ChromiumRev = dbTask.ChromiumRev
	taskVars.ChromiumBuild.SkiaRev = dbTask.SkiaRev
	taskVars.Description = dbTask.Description
	return taskVars
}

func (task DBTask) GetUpdateTaskVars() task_common.UpdateTaskVars {
	return &UpdateVars{}
}

func (task DBTask) RunsOnGCEWorkers() bool {
	// Capture SKP tasks need to run on bare-metal machines because they have
	// the right font packages installed.
	return false
}

func (task DBTask) TableName() string {
	return db.TABLE_CAPTURE_SKPS_TASKS
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

	PageSets      string                 `json:"page_sets"`
	ChromiumBuild chromium_builds.DBTask `json:"chromium_build"`
	Description   string                 `json:"desc"`
}

func (task *AddTaskVars) GetInsertQueryAndBinds() (string, []interface{}, error) {
	if task.PageSets == "" ||
		task.ChromiumBuild.ChromiumRev == "" ||
		task.ChromiumBuild.SkiaRev == "" ||
		task.Description == "" {
		return "", nil, fmt.Errorf("Invalid parameters")
	}
	if err := chromium_builds.Validate(task.ChromiumBuild); err != nil {
		return "", nil, err
	}
	if err := ctfeutil.CheckLengths([]ctfeutil.LengthCheck{
		{Name: "page_sets", Value: task.PageSets, Limit: 100},
		{Name: "desc", Value: task.Description, Limit: 255},
	}); err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("INSERT INTO %s (username,page_sets,chromium_rev,skia_rev,description,ts_added, repeat_after_days) VALUES (?,?,?,?,?,?,?);",
			db.TABLE_CAPTURE_SKPS_TASKS),
		[]interface{}{
			task.Username,
			task.PageSets,
			task.ChromiumBuild.ChromiumRev,
			task.ChromiumBuild.SkiaRev,
			task.Description,
			task.TsAdded,
			task.RepeatAfterDays,
		},
		nil
}

func addTaskHandler(w http.ResponseWriter, r *http.Request) {
	task_common.AddTaskHandler(w, r, &AddTaskVars{})
}

func getTasksHandler(w http.ResponseWriter, r *http.Request) {
	task_common.GetTasksHandler(&DBTask{}, w, r)
}

// Validate that the given skpRepository exists in the DB.
func Validate(skpRepository DBTask) error {
	rowCount := []int{}
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE page_sets = ? AND chromium_rev = ? AND skia_rev = ? AND ts_completed IS NOT NULL AND failure = 0", db.TABLE_CAPTURE_SKPS_TASKS)
	if err := db.DB.Select(&rowCount, query, skpRepository.PageSets, skpRepository.ChromiumRev, skpRepository.SkiaRev); err != nil || len(rowCount) < 1 || rowCount[0] == 0 {
		sklog.Info(err)
		return fmt.Errorf("Unable to validate skp_repository parameter %v", skpRepository)
	}
	return nil
}

type UpdateVars struct {
	task_common.UpdateTaskCommonVars
}

func (vars *UpdateVars) UriPath() string {
	return ctfeutil.UPDATE_CAPTURE_SKPS_TASK_POST_URI
}

func (task *UpdateVars) GetUpdateExtraClausesAndBinds() ([]string, []interface{}, error) {
	return nil, nil, nil
}

func updateTaskHandler(w http.ResponseWriter, r *http.Request) {
	task_common.UpdateTaskHandler(&UpdateVars{}, db.TABLE_CAPTURE_SKPS_TASKS, w, r)
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
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.CAPTURE_SKPS_URI, "GET", addTaskView)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.CAPTURE_SKPS_RUNS_URI, "GET", runsHistoryView)

	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.ADD_CAPTURE_SKPS_TASK_POST_URI, "POST", addTaskHandler)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.GET_CAPTURE_SKPS_TASKS_POST_URI, "POST", getTasksHandler)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.DELETE_CAPTURE_SKPS_TASK_POST_URI, "POST", deleteTaskHandler)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.REDO_CAPTURE_SKPS_TASK_POST_URI, "POST", redoTaskHandler)

	// Do not add force login handler for update methods. They use webhooks for authentication.
	r.HandleFunc("/"+ctfeutil.UPDATE_CAPTURE_SKPS_TASK_POST_URI, updateTaskHandler).Methods("POST")
}

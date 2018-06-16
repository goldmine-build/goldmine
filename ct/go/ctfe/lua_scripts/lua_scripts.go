/*
	Handlers and types specific to running Lua scripts.
*/

package lua_scripts

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"text/template"

	"cloud.google.com/go/datastore"
	"github.com/gorilla/mux"
	"google.golang.org/api/iterator"

	"go.skia.org/infra/ct/go/ctfe/capture_skps"
	"go.skia.org/infra/ct/go/ctfe/task_common"
	ctfeutil "go.skia.org/infra/ct/go/ctfe/util"
	ctutil "go.skia.org/infra/ct/go/util"
	"go.skia.org/infra/go/ds"
)

var (
	addTaskTemplate     *template.Template = nil
	runsHistoryTemplate *template.Template = nil
)

func ReloadTemplates(resourcesDir string) {
	addTaskTemplate = template.Must(template.ParseFiles(
		filepath.Join(resourcesDir, "templates/lua_scripts.html"),
		filepath.Join(resourcesDir, "templates/header.html"),
		filepath.Join(resourcesDir, "templates/titlebar.html"),
	))
	runsHistoryTemplate = template.Must(template.ParseFiles(
		filepath.Join(resourcesDir, "templates/lua_script_runs_history.html"),
		filepath.Join(resourcesDir, "templates/header.html"),
		filepath.Join(resourcesDir, "templates/titlebar.html"),
	))
}

type DatastoreTask struct {
	task_common.CommonCols

	PageSets            string
	IsTestPageSet       bool
	ChromiumRev         string
	SkiaRev             string
	LuaScript           string `datastore:",noindex"`
	LuaAggregatorScript string `datastore:",noindex"`
	Description         string
	ScriptOutput        string
	AggregatedOutput    string
}

func (task DatastoreTask) GetTaskName() string {
	return "LuaScript"
}

func (task DatastoreTask) GetResultsLink() string {
	if task.AggregatedOutput != "" {
		return task.AggregatedOutput
	} else if task.ScriptOutput != "" {
		return task.ScriptOutput
	}
	return ""
}

func (task DatastoreTask) GetPopulatedAddTaskVars() (task_common.AddTaskVars, error) {
	taskVars := &AddTaskVars{}
	taskVars.Username = task.Username
	taskVars.TsAdded = ctutil.GetCurrentTs()
	taskVars.RepeatAfterDays = strconv.FormatInt(task.RepeatAfterDays, 10)

	taskVars.SkpRepository.ChromiumRev = task.ChromiumRev
	taskVars.SkpRepository.SkiaRev = task.SkiaRev
	taskVars.SkpRepository.PageSets = task.PageSets

	taskVars.LuaScript = task.LuaScript
	taskVars.LuaAggregatorScript = task.LuaAggregatorScript
	taskVars.Description = task.Description
	return taskVars, nil
}

func (task DatastoreTask) GetUpdateTaskVars() task_common.UpdateTaskVars {
	return &UpdateVars{}
}

func (task DatastoreTask) RunsOnGCEWorkers() bool {
	return true
}

func (task DatastoreTask) GetDatastoreKind() ds.Kind {
	return ds.LUA_SCRIPT_TASKS
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

func addTaskView(w http.ResponseWriter, r *http.Request) {
	ctfeutil.ExecuteSimpleTemplate(addTaskTemplate, w, r)
}

type AddTaskVars struct {
	task_common.AddTaskCommonVars

	SkpRepository       capture_skps.DatastoreTask `json:"skp_repository"`
	LuaScript           string                     `json:"lua_script"`
	LuaAggregatorScript string                     `json:"lua_aggregator_script"`
	Description         string                     `json:"desc"`
}

func (task *AddTaskVars) GetDatastoreKind() ds.Kind {
	return ds.LUA_SCRIPT_TASKS
}

func (task *AddTaskVars) GetPopulatedDatastoreTask(ctx context.Context) (task_common.Task, error) {
	if task.SkpRepository.PageSets == "" ||
		task.SkpRepository.ChromiumRev == "" ||
		task.SkpRepository.SkiaRev == "" ||
		task.LuaScript == "" ||
		task.Description == "" {
		return nil, fmt.Errorf("Invalid parameters")
	}
	if err := capture_skps.Validate(ctx, task.SkpRepository); err != nil {
		return nil, err
	}

	t := &DatastoreTask{
		PageSets:            task.SkpRepository.PageSets,
		IsTestPageSet:       task.SkpRepository.PageSets == ctutil.PAGESET_TYPE_DUMMY_1k,
		ChromiumRev:         task.SkpRepository.ChromiumRev,
		SkiaRev:             task.SkpRepository.SkiaRev,
		LuaScript:           task.LuaScript,
		LuaAggregatorScript: task.LuaAggregatorScript,
		Description:         task.Description,
	}
	return t, nil
}

func addTaskHandler(w http.ResponseWriter, r *http.Request) {
	task_common.AddTaskHandler(w, r, &AddTaskVars{})
}

func getTasksHandler(w http.ResponseWriter, r *http.Request) {
	task_common.GetTasksHandler(&DatastoreTask{}, w, r)
}

type UpdateVars struct {
	task_common.UpdateTaskCommonVars
	ScriptOutput     string
	AggregatedOutput string
}

func (vars *UpdateVars) UriPath() string {
	return ctfeutil.UPDATE_LUA_SCRIPT_TASK_POST_URI
}

func (vars *UpdateVars) UpdateExtraFields(t task_common.Task) error {
	task := t.(*DatastoreTask)
	if vars.ScriptOutput != "" {
		task.ScriptOutput = vars.ScriptOutput
	}
	if vars.AggregatedOutput != "" {
		task.AggregatedOutput = vars.AggregatedOutput
	}
	return nil
}

func updateTaskHandler(w http.ResponseWriter, r *http.Request) {
	task_common.UpdateTaskHandler(&UpdateVars{}, &DatastoreTask{}, w, r)
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

func AddHandlers(r *mux.Router) {
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.LUA_SCRIPT_URI, "GET", addTaskView)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.LUA_SCRIPT_RUNS_URI, "GET", runsHistoryView)

	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.ADD_LUA_SCRIPT_TASK_POST_URI, "POST", addTaskHandler)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.GET_LUA_SCRIPT_TASKS_POST_URI, "POST", getTasksHandler)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.DELETE_LUA_SCRIPT_TASK_POST_URI, "POST", deleteTaskHandler)
	ctfeutil.AddForceLoginHandler(r, "/"+ctfeutil.REDO_LUA_SCRIPT_TASK_POST_URI, "POST", redoTaskHandler)

	// Do not add force login handler for update methods. They use webhooks for authentication.
	r.HandleFunc("/"+ctfeutil.UPDATE_LUA_SCRIPT_TASK_POST_URI, updateTaskHandler).Methods("POST")
}

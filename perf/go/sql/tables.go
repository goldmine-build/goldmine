package sql

//go:generate bazelisk run --config=mayberemote //:go -- run ./tosql

import (
	alertschema "go.goldmine.build/perf/go/alerts/sqlalertstore/schema"
	gitschema "go.goldmine.build/perf/go/git/schema"
	graphsshortcutschema "go.goldmine.build/perf/go/graphsshortcut/graphsshortcutstore/schema"
	regressionschema "go.goldmine.build/perf/go/regression/sqlregressionstore/schema"
	shortcutschema "go.goldmine.build/perf/go/shortcut/sqlshortcutstore/schema"
	traceschema "go.goldmine.build/perf/go/tracestore/sqltracestore/schema"
)

// Tables represents the full schema of the SQL database.
type Tables struct {
	Alerts          []alertschema.AlertSchema
	Commits         []gitschema.Commit
	GraphsShortcuts []graphsshortcutschema.GraphsShortcutSchema
	ParamSets       []traceschema.ParamSetsSchema
	Postings        []traceschema.PostingsSchema
	Regressions     []regressionschema.RegressionSchema
	Shortcuts       []shortcutschema.ShortcutSchema
	SourceFiles     []traceschema.SourceFilesSchema
	TraceValues     []traceschema.TraceValuesSchema
}

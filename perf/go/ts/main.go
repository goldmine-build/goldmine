// Program to generate TypeScript definition files for Golang structs that are
// serialized to JSON for the web UI.
//
//go:generate bazelisk run --config=mayberemote //:go -- run . -o ../../modules/json/index.ts
package main

import (
	"flag"
	"io"

	"go.goldmine.build/go/git/provider"
	"go.goldmine.build/go/go2ts"
	"go.goldmine.build/go/paramtools"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/go/util"
	"go.goldmine.build/perf/go/alerts"
	"go.goldmine.build/perf/go/clustering2"
	"go.goldmine.build/perf/go/config"
	"go.goldmine.build/perf/go/dryrun"
	"go.goldmine.build/perf/go/frontend"
	"go.goldmine.build/perf/go/graphsshortcut"
	"go.goldmine.build/perf/go/ingest/format"
	"go.goldmine.build/perf/go/notifytypes"
	"go.goldmine.build/perf/go/pinpoint"
	"go.goldmine.build/perf/go/pivot"
	"go.goldmine.build/perf/go/progress"
	"go.goldmine.build/perf/go/regression"
	"go.goldmine.build/perf/go/stepfit"
	"go.goldmine.build/perf/go/trybot/results"
	"go.goldmine.build/perf/go/types"
	"go.goldmine.build/perf/go/ui/frame"
)

type unionAndName struct {
	v        interface{}
	typeName string
}

func addMultipleUnions(generator *go2ts.Go2TS, unions []unionAndName) {
	for _, u := range unions {
		generator.AddUnionWithName(u.v, u.typeName)
	}
}

func main() {
	var outputPath = flag.String("o", "", "Path to the output TypeScript file.")
	flag.Parse()

	generator := go2ts.New()
	generator.GenerateNominalTypes = true
	generator.AddIgnoreNil(paramtools.Params{})
	generator.AddIgnoreNil(paramtools.ParamSet{})
	generator.AddIgnoreNil(paramtools.ReadOnlyParamSet{})
	generator.AddIgnoreNil(types.TraceSet{})

	generator.AddUnionToNamespace(pivot.AllOperations, "pivot")
	generator.AddToNamespace(pivot.Request{}, "pivot")

	generator.AddMultiple(generator,
		alerts.Alert{},
		alerts.AlertsStatus{},
		clustering2.ClusterSummary{},
		clustering2.ValuePercent{},
		config.Favorites{},
		config.QueryConfig{},
		dryrun.RegressionAtCommit{},
		frame.FrameRequest{},
		frame.FrameResponse{},
		frontend.AlertUpdateResponse{},
		frontend.CIDHandlerResponse{},
		frontend.ClusterStartResponse{},
		frontend.CommitDetailsRequest{},
		frontend.CountHandlerRequest{},
		frontend.CountHandlerResponse{},
		frontend.GetGraphsShortcutRequest{},
		frontend.RangeRequest{},
		frontend.RegressionRangeRequest{},
		frontend.RegressionRangeResponse{},
		frontend.ShiftRequest{},
		frontend.ShiftResponse{},
		frontend.SkPerfConfig{},
		frontend.TriageRequest{},
		frontend.TriageResponse{},
		frontend.TryBugRequest{},
		frontend.TryBugResponse{},
		graphsshortcut.GraphsShortcut{},
		pinpoint.CreateBisectRequest{},
		pinpoint.CreateBisectResponse{},
		provider.Commit{},
		regression.FullSummary{},
		regression.RegressionDetectionRequest{},
		regression.RegressionDetectionResponse{},
		regression.TriageStatus{},
		results.TryBotRequest{},
		results.TryBotResponse{},
	)

	// TODO(jcgregorio) Switch to generator.AddMultipleUnionToNamespace().
	addMultipleUnions(generator, []unionAndName{
		{alerts.AllConfigState, "ConfigState"},
		{alerts.AllDirections, "Direction"},
		{frame.AllRequestType, "RequestType"},
		{frontend.AllRegressionSubset, "Subset"},
		{regression.AllProcessState, "ProcessState"},
		{regression.AllStatus, "Status"},
		{stepfit.AllStepFitStatus, "StepFitStatus"},
		{types.AllClusterAlgos, "ClusterAlgo"},
		{types.AllStepDetections, "StepDetection"},
		{results.AllRequestKind, "TryBotRequestKind"},
		{frame.AllResponseDisplayModes, "FrameResponseDisplayMode"},
		{notifytypes.AllNotifierTypes, "NotifierTypes"},
		{config.AllTraceFormats, "TraceFormat"},
		{types.AllAlertActions, "AlertAction"},
	})

	generator.AddUnionToNamespace(progress.AllStatus, "progress")
	generator.AddToNamespace(progress.SerializedProgress{}, "progress")

	generator.AddToNamespace(format.Format{}, "ingest")

	err := util.WithWriteFile(*outputPath, func(w io.Writer) error {
		return generator.Render(w)
	})
	if err != nil {
		sklog.Fatal(err)
	}
}

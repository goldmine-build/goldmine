// Program to generate TypeScript definition files for Goland structs that are
// serialized to JSON for the web UI.
package main

import (
	"io"

	"github.com/skia-dev/go2ts"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/perf/go/alerts"
	"go.skia.org/infra/perf/go/clustering2"
	"go.skia.org/infra/perf/go/dataframe"
	"go.skia.org/infra/perf/go/dryrun"
	"go.skia.org/infra/perf/go/frontend"
	perfgit "go.skia.org/infra/perf/go/git"
	"go.skia.org/infra/perf/go/regression"
	"go.skia.org/infra/perf/go/regression/continuous"
	"go.skia.org/infra/perf/go/stepfit"
	"go.skia.org/infra/perf/go/trybot/results"
	"go.skia.org/infra/perf/go/types"
)

type unionAndName struct {
	v        interface{}
	typeName string
}

func addMultipleUnions(generator *go2ts.Go2TS, unions []unionAndName) error {
	for _, u := range unions {
		if err := generator.AddUnionWithName(u.v, u.typeName); err != nil {
			return err
		}
	}
	return nil
}

func main() {
	generator := go2ts.New()
	err := generator.AddMultiple(generator,
		alerts.Alert{},
		alerts.AlertsStatus{},
		clustering2.ClusterSummary{},
		clustering2.ValuePercent{},
		dataframe.FrameRequest{},
		dataframe.FrameResponse{},
		dryrun.DryRunStatus{},
		dryrun.StartDryRunResponse{},
		frontend.AlertUpdateResponse{},
		frontend.ClusterStartResponse{},
		frontend.ClusterStatus{},
		frontend.CommitDetailsRequest{},
		frontend.CountHandlerRequest{},
		frontend.CountHandlerResponse{},
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
		perfgit.Commit{},
		continuous.Current{},
		regression.FullSummary{},
		regression.RegressionDetectionRequest{},
		regression.TriageStatus{},
		results.TryBotRequest{},
		results.TryBotResponse{},
	)
	if err != nil {
		sklog.Fatal(err)
	}

	// TODO(jcgregorio) Switch to generator.AddMultipleUnions() once all the
	// names are harmonized between backend and frontend.
	err = addMultipleUnions(generator, []unionAndName{
		{alerts.AllConfigState, "ConfigState"},
		{alerts.AllDirections, "Direction"},
		{dataframe.AllRequestType, "RequestType"},
		{frontend.AllRegressionSubset, "Subset"},
		{regression.AllProcessState, "ProcessState"},
		{regression.AllStatus, "Status"},
		{stepfit.AllStepFitStatus, "StepFitStatus"},
		{types.AllClusterAlgos, "ClusterAlgo"},
		{types.AllStepDetections, "StepDetection"},
		{results.AllRequestKind, "TryBotRequestKind"},
	})
	err = util.WithWriteFile("./modules/json/index.ts", func(w io.Writer) error {
		return generator.Render(w)
	})
	if err != nil {
		sklog.Fatal(err)
	}
}

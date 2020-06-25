// Program to generate TypeScript definition files for Goland structs that are
// serialized to JSON for the web UI.
package main

import (
	"io"

	"github.com/skia-dev/go2ts"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/perf/go/alerts"
	"go.skia.org/infra/perf/go/cid"
	"go.skia.org/infra/perf/go/clustering2"
	"go.skia.org/infra/perf/go/dataframe"
	"go.skia.org/infra/perf/go/frontend"
	"go.skia.org/infra/perf/go/regression"
	"go.skia.org/infra/perf/go/stepfit"
	"go.skia.org/infra/perf/go/types"
)

func addMultiple(generator *go2ts.Go2TS, instances []interface{}) error {
	for _, inst := range instances {
		err := generator.Add(inst)
		if err != nil {
			return err
		}
	}
	return nil
}

func main() {
	generator := go2ts.New()
	err := addMultiple(generator, []interface{}{
		clustering2.ValuePercent{},
		frontend.CountHandlerRequest{},
		frontend.CountHandlerResponse{},
		frontend.CommitDetailsRequest{},
		cid.CommitDetail{},
		clustering2.ClusterSummary{},
		regression.TriageStatus{},
		dataframe.FrameResponse{},
		alerts.Alert{},
	})
	if err != nil {
		sklog.Fatal(err)
	}
	err = generator.AddUnionWithName(regression.AllStatus, "Status")
	if err != nil {
		sklog.Fatal(err)
	}
	err = generator.AddUnionWithName(types.AllClusterAlgos, "ClusterAlgo")
	if err != nil {
		sklog.Fatal(err)
	}
	err = generator.AddUnionWithName(stepfit.AllStepFitStatus, "StepFitStatus")
	if err != nil {
		sklog.Fatal(err)
	}
	err = util.WithWriteFile("./modules/json/index.ts", func(w io.Writer) error {
		return generator.Render(w)
	})
	if err != nil {
		sklog.Fatal(err)
	}
}

// The diffcalculator processes diffs. It continuously looks for work to do either
// on the primary branch or on secondary branches (CLs) and computes the diffs
// for them.
package main

import (
	"context"
	"time"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/cmd/diffcalculator/impl"
	"go.goldmine.build/golden/go/config"
)

const (
	// An arbitrary amount.
	maxSQLConnections = 20

	// The GCS folder that contains the images, named by their digests.
	imgFolder = "dm-images-v1"

	calculateCLDataProportion = 0.8

	primaryBranchStalenessThreshold = time.Minute

	diffCalculationTimeout = 10 * time.Minute

	groupingCacheSize = 100_000
)

var flags config.ServerFlags

func main() {
	ctx := context.Background()
	common.InitWithMust(
		"periodictasks",
		common.PrometheusOpt(&flags.PromPort),
		common.FlagSetOpt((&flags).Flagset()),
	)

	if flags.Hang {
		sklog.Info("Hanging")
		select {}
	}

	cfg, err := config.LoadConfigFromJSON5(flags.ConfigPath)
	if err != nil {
		sklog.Fatalf("Reading config: %s", err)
	}
	sklog.Infof("Loaded config %#v", cfg)

	impl.DiffCalculatorMain(ctx, cfg, flags)
}

// The diffcalculator processes diffs. It continuously looks for work to do either
// on the primary branch or on secondary branches (CLs) and computes the diffs
// for them.
package main

import (
	"context"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/cmd/diffcalculator/impl"
	"go.goldmine.build/golden/go/config"
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

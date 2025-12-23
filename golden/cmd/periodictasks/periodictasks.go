package main

import (
	"context"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/cmd/periodictasks/impl"
	"go.goldmine.build/golden/go/config"
)

var flags config.ServerFlags

func main() {
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

	ctx := context.Background()
	impl.PeriodicTasksMain(ctx, cfg, flags)
}

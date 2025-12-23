package main

import (
	"context"
	"flag"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/cmd/periodictasks/impl"
	"go.goldmine.build/golden/go/config"
)

func main() {
	// Command line flags.
	var (
		configPath = flag.String("config", "", "Path to the json5 file containing the configuration.")
		hang       = flag.Bool("hang", false, "Stop and do nothing after reading the flags. Good for debugging containers.")
	)

	// Parse the options. So we can configure logging.
	flag.Parse()

	if *hang {
		sklog.Info("Hanging")
		select {}
	}

	cfg, err := config.LoadConfigFromJSON5(*configPath)
	if err != nil {
		sklog.Fatalf("Reading config: %s", err)
	}
	sklog.Infof("Loaded config %#v", cfg)

	common.InitWithMust(
		"periodictasks",
		common.PrometheusOpt(&cfg.PromPort),
	)

	ctx := context.Background()
	impl.PeriodicTasksMain(ctx, cfg)
}

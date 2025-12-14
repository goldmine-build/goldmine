// gold_ingestion is the server process that runs an arbitrary number of
// ingesters and stores them to the appropriate backends.
package main

import (
	"context"
	"flag"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/cmd/gold_ingestion/impl"
	"go.goldmine.build/golden/go/config"
)

func main() {
	// Command line flags.
	var (
		configPath = flag.String("config", "", "Path to the json5 file containing the instance configuration.")
		hang       = flag.Bool("hang", false, "Stop and do nothing after reading the flags. Good for debugging containers.")
	)

	// Parse the options. So we can configure logging.
	flag.Parse()

	if *hang {
		sklog.Info("Hanging")
		select {}
	}

	var cfg config.Common
	cfg, err := config.LoadConfigFromJSON5(*configPath)
	if err != nil {
		sklog.Fatalf("Reading config: %s", err)
	}
	sklog.Infof("Loaded config %#v", cfg)

	common.InitWithMust(
		"gold-ingestion",
		common.PrometheusOpt(&cfg.PromPort),
	)
	// We expect there to be a lot of ingestion work, so we sample 1% of them to avoid incurring
	// too much overhead.
	//	if err := tracing.Initialize(0.01, isc.SQLDatabaseName); err != nil {
	//		sklog.Fatalf("Could not set up tracing: %s", err)
	//	}

	ctx := context.Background()
	impl.IngestionMain(ctx, cfg)
}

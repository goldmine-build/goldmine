// gold_ingestion is the server process that runs an arbitrary number of
// ingesters and stores them to the appropriate backends.
package main

import (
	"context"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/cmd/gold_ingestion/impl"
	"go.goldmine.build/golden/go/config"
)

var flags config.ServerFlags

func main() {
	common.InitWithMust(
		"gold-ingestion",
		common.PrometheusOpt(&flags.PromPort),
		common.FlagSetOpt((&flags).Flagset()),
	)

	if flags.Hang {
		sklog.Info("Hanging")
		select {}
	}

	var cfg config.Common
	cfg, err := config.LoadConfigFromJSON5(flags.ConfigPath)
	if err != nil {
		sklog.Fatalf("Reading config: %s", err)
	}
	sklog.Infof("Loaded config %#v", cfg)

	// We expect there to be a lot of ingestion work, so we sample 1% of them to avoid incurring
	// too much overhead.
	//	if err := tracing.Initialize(0.01, isc.SQLDatabaseName); err != nil {
	//		sklog.Fatalf("Could not set up tracing: %s", err)
	//	}

	ctx := context.Background()
	impl.IngestionMain(ctx, cfg, flags)
}

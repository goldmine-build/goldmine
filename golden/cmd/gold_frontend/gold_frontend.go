// The goldfrontend executable is the process that exposes a RESTful API used by the JS frontend.
package main

import (
	"context"
	"os"
	"path/filepath"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/cmd/gold_frontend/impl"
	"go.goldmine.build/golden/go/config"
)

var flags config.ServerFlags

func main() {
	// Initialize service.
	_, appName := filepath.Split(os.Args[0])
	common.InitWithMust(
		appName,
		common.PrometheusOpt(&flags.PromPort),
		common.FlagSetOpt((&flags).Flagset()),
	)

	if flags.Hang {
		sklog.Info("Hanging")
		select {}
	}

	ctx := context.Background()

	// Load the config file.
	cfg, err := config.LoadConfigFromJSON5(flags.ConfigPath)
	if err != nil {
		sklog.Fatalf("Reading config: %s", err)
	}

	impl.FrontendMain(ctx, cfg, flags)

}

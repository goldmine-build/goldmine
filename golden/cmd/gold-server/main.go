package main

import (
	"encoding/json"
	"net/http"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/httputils"
	"go.goldmine.build/go/profsrv"
	"go.goldmine.build/go/sklog"
	diffcalc "go.goldmine.build/golden/cmd/diffcalculator/impl"
	frontend "go.goldmine.build/golden/cmd/gold_frontend/impl"
	ingestion "go.goldmine.build/golden/cmd/gold_ingestion/impl"
	periodic "go.goldmine.build/golden/cmd/periodictasks/impl"
	"go.goldmine.build/golden/go/config"
	"go.goldmine.build/golden/go/services"
	"golang.org/x/net/context"
)

var flags config.ServerFlags

func main() {
	// Command line flags.
	common.InitWithMust(
		"gold-server",
		common.PrometheusOpt(&flags.PromPort),
		common.FlagSetOpt((&flags).Flagset()),
	)

	activeServices, err := services.Validate(flags.ServicesFlag)
	if err != nil {
		sklog.Fatal(err)
	}

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

	cfgAsJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		sklog.Fatal(err)
	}
	sklog.Infof("Loaded config\n %s", cfgAsJSON)

	// Start pprof services.
	profsrv.Start(flags.PprofPort)

	// Each service will sklog.Fatal if they fail, so we don't need extra error
	// handling here.
	for _, s := range activeServices {
		// Start each service.
		sklog.Infof("Starting service: %q", s)
		switch s {
		case services.Ingester:
			go ingestion.IngestionMain(ctx, cfg, flags)
		case services.Periodic:
			periodic.PeriodicTasksMain(ctx, cfg, flags)
		case services.Frontend:
			go frontend.FrontendMain(ctx, cfg, flags)
		case services.DiffCalc:
			go diffcalc.DiffCalculatorMain(ctx, cfg, flags)
		}
	}

	// Handle healthz.
	http.HandleFunc("/healthz", httputils.ReadyHandleFunc)
	sklog.Fatal(http.ListenAndServe(flags.HealthzPort, nil))
}

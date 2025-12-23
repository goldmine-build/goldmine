package main

import (
	"encoding/json"
	"flag"
	"net/http"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/httputils"
	"go.goldmine.build/go/profsrv"
	"go.goldmine.build/go/sklog"
	ingestion "go.goldmine.build/golden/cmd/gold_ingestion/impl"
	periodic "go.goldmine.build/golden/cmd/periodictasks/impl"
	"go.goldmine.build/golden/go/config"
	"go.goldmine.build/golden/go/services"
	"golang.org/x/net/context"
)

func main() {
	// Command line flags.
	var (
		configPath   = flag.String("config", "", "Path to the json5 file containing the instance configuration.")
		servicesFlag = common.NewMultiStringFlag("services", nil, "The list of services to run. If not provided then all services wil be run.")
		hang         = flag.Bool("hang", false, "Stop and do nothing after reading the flags. Good for debugging containers.")
		promPort     = flag.String("prom_port", ":20000", "Metrics service address (e.g., ':20000')")
		pprofPort    = flag.String("pprof_port", "", "PProf handler (e.g., ':9001'). PProf not enabled if the empty string (default).")
		healthzPort  = flag.String("healthz", ":10000", "Port that handles the healthz endpoint.")
	)

	common.InitWithMust(
		"gold-server",
		common.PrometheusOpt(promPort),
	)

	activeServices, err := services.Validate(*servicesFlag)
	if err != nil {
		sklog.Fatal(err)
	}

	if *hang {
		sklog.Info("Hanging")
		select {}
	}

	ctx := context.Background()

	// Load the config file.
	cfg, err := config.LoadConfigFromJSON5(*configPath)
	if err != nil {
		sklog.Fatalf("Reading config: %s", err)
	}

	cfgAsJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		sklog.Fatal(err)
	}
	sklog.Infof("Loaded config\n %s", cfgAsJSON)

	// Start pprof services.
	profsrv.Start(*pprofPort)

	// Each service will sklog.Fatal if they fail, so we don't need extra error
	// handling here.
	for _, s := range activeServices {
		// Start each service.
		sklog.Infof("Starting service: %q", s)
		switch s {
		case services.Ingester:
			go ingestion.IngestionMain(ctx, cfg)
		case services.Periodic:
			periodic.PeriodicTasksMain(ctx, cfg)
		}

	}

	// Handle healthz.
	http.HandleFunc("/healthz", httputils.ReadyHandleFunc)
	sklog.Fatal(http.ListenAndServe(*healthzPort, nil))
}

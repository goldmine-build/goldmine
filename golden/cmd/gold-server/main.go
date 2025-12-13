package main

import (
	"encoding/json"
	"flag"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/go/config"
	"go.goldmine.build/golden/go/services"
)

func main() {
	// Command line flags.
	var (
		configPath   = flag.String("config", "", "Path to the json5 file containing the instance configuration.")
		servicesFlag = common.NewMultiStringFlag("services", nil, "The list of services to run. If not provided then all services wil be run.")
		hang         = flag.Bool("hang", false, "Stop and do nothing after reading the flags. Good for debugging containers.")
		promPort     = flag.String("prom_port", ":20000", "Metrics service address (e.g., ':10110')")
	)

	common.InitWithMust(
		"gold-server",
		common.PrometheusOpt(promPort),
	)

	services, err := services.Validate(*servicesFlag)
	if err != nil {
		sklog.Fatal(err)
	}

	if *hang {
		sklog.Info("Hanging")
		select {}
	}

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

	for _, s := range services {
		// Start each service.
		sklog.Infof("Starting service: %q", s)
	}
}

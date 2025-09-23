// perfserver is the single executable that contains the sub-commands that make
// up a running Perf system, including the web ui, the ingestion process, and
// the regression detection process.
package main

import (
	"context"
	"fmt"
	"os"

	cli "github.com/urfave/cli/v2"
	"go.goldmine.build/go/metrics2"
	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/go/sklog/sklogimpl"
	"go.goldmine.build/go/sklog/stdlogging"
	"go.goldmine.build/go/urfavecli"
	"go.goldmine.build/perf/go/config"
	"go.goldmine.build/perf/go/config/validate"
	"go.goldmine.build/perf/go/frontend"
	"go.goldmine.build/perf/go/ingest/process"
	"go.goldmine.build/perf/go/maintenance"
)

func main() {
	var clusterFlags config.FrontendFlags
	var frontendFlags config.FrontendFlags
	var ingestFlags config.IngestFlags
	var maintenanceFlags config.MaintenanceFlags

	cli.MarkdownDocTemplate = urfavecli.MarkdownDocTemplate

	cliApp := &cli.App{
		Name:  "perfserver",
		Usage: "Command line tool that runs the various components of Perf.",
		Before: func(c *cli.Context) error {
			// Log to stdout.
			sklogimpl.SetLogger(stdlogging.New(os.Stdout))

			return nil
		},
		Commands: []*cli.Command{
			{
				Name:        "frontend",
				Usage:       "The main web UI.",
				Description: "Runs the process that serves the web UI for Perf.",
				Flags:       (&frontendFlags).AsCliFlags(false),
				Action: func(c *cli.Context) error {
					urfavecli.LogFlags(c)
					f, err := frontend.New(&frontendFlags)
					if err != nil {
						return err
					}
					f.Serve()
					return nil
				},
			},
			{
				Name:        "maintenance",
				Usage:       "Starts maintenance tasks.",
				Description: "Runs maintenance tasks that require running from a singleton for each instance.",
				Flags:       (&maintenanceFlags).AsCliFlags(),
				Action: func(c *cli.Context) error {
					urfavecli.LogFlags(c)
					instanceConfig, schemaViolations, err := validate.InstanceConfigFromFile(maintenanceFlags.ConfigFilename)
					if err != nil {
						for _, v := range schemaViolations {
							sklog.Error(v)
						}
						return err
					}
					if maintenanceFlags.ConnectionString != "" {
						instanceConfig.DataStoreConfig.ConnectionString = maintenanceFlags.ConnectionString
					}

					metrics2.InitPrometheus(maintenanceFlags.PromPort)

					return maintenance.Start(context.Background(), maintenanceFlags, instanceConfig)
				},
			},
			{
				Name:        "ingest",
				Usage:       "Run the ingestion process.",
				Description: "Continuously imports files as they arrive from the configured ingestion sources and populates the TraceStore with that data.",
				Flags:       (&ingestFlags).AsCliFlags(),
				Action: func(c *cli.Context) error {
					urfavecli.LogFlags(c)
					instanceConfig, schemaViolations, err := validate.InstanceConfigFromFile(ingestFlags.ConfigFilename)
					if err != nil {
						for _, v := range schemaViolations {
							sklog.Error(v)
						}
						return err
					}
					if ingestFlags.ConnectionString != "" {
						instanceConfig.DataStoreConfig.ConnectionString = ingestFlags.ConnectionString
					}

					metrics2.InitPrometheus(ingestFlags.PromPort)

					return process.Start(context.Background(), ingestFlags.Local, ingestFlags.NumParallelIngesters, instanceConfig)
				},
			},
			{
				Name:        "cluster",
				Usage:       "Run the regression detection process.",
				Description: "Continuously runs over all the configured alerts and looks for regressions as new data arrives.",
				Flags:       (&clusterFlags).AsCliFlags(true),
				Action: func(c *cli.Context) error {
					urfavecli.LogFlags(c)
					f, err := frontend.New(&clusterFlags)
					if err != nil {
						return err
					}
					f.Serve()
					return nil
				},
			},
			{
				Name:  "markdown",
				Usage: "Generates markdown help for perfserver.",
				Action: func(c *cli.Context) error {
					body, err := c.App.ToMarkdown()
					if err != nil {
						return skerr.Wrap(err)
					}
					fmt.Println(body)
					return nil
				},
			},
		},
	}

	err := cliApp.Run(os.Args)
	if err != nil {
		fmt.Printf("\nError: %s\n", err.Error())
		os.Exit(2)
	}
}

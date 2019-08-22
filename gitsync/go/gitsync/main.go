package main

import (
	"context"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/flynn/json5"
	"go.skia.org/infra/gitsync/go/watcher"
	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/gitauth"
	"go.skia.org/infra/go/gitstore/bt_gitstore"
	"go.skia.org/infra/go/httputils"
	"go.skia.org/infra/go/human"
	"go.skia.org/infra/go/sklog"
)

// This server watches a list of git repos for changes and syncs the meta data of all commits
// to a BigTable backed datastore.

// Default config/flag values
var defaultConf = gitSyncConfig{
	BTInstanceID:      "production",
	BTTableID:         "git-repos2",
	BTWriteGoroutines: bt_gitstore.DefaultWriteGoroutines,
	HttpPort:          ":9091",
	Local:             false,
	ProjectID:         "skia-public",
	PromPort:          ":20000",
	RepoURLs:          []string{},
	RefreshInterval:   human.JSONDuration(10 * time.Minute),
}

func main() {
	// config holds the configuration values either from flags or from parsing the config files.
	config := defaultConf

	// Flags that cause the flags below to be disregarded.
	configFile := flag.String("config", "", "Disregard flags and load the configuration from this JSON5 config file. The keys and types of the config file match the flags.")
	runInit := flag.Bool("init", false, "Initialize the BigTable instance and quit. This should be run with a different different user who has admin rights.")
	gcsBucket := flag.String("gcs_bucket", "", "GCS bucket used for temporary storage during ingestion.")
	gcsPath := flag.String("gcs_path", "", "GCS path used for temporary storage during ingestion.")

	// Define flags that map to field in the configuration struct.
	flag.StringVar(&config.BTInstanceID, "bt_instance", defaultConf.BTInstanceID, "Big Table instance")
	flag.StringVar(&config.BTTableID, "bt_table", defaultConf.BTTableID, "BigTable table ID")
	flag.IntVar(&config.BTWriteGoroutines, "bt_write_goroutines", defaultConf.BTWriteGoroutines, "Number of goroutines to use when writing to BigTable.")
	flag.StringVar(&config.HttpPort, "http_port", defaultConf.HttpPort, "The http port where ready-ness endpoints are served.")
	flag.BoolVar(&config.Local, "local", defaultConf.Local, "Running locally if true. As opposed to in production.")
	flag.StringVar(&config.ProjectID, "project", defaultConf.ProjectID, "ID of the GCP project")
	flag.StringVar(&config.PromPort, "prom_port", defaultConf.PromPort, "Metrics service address (e.g., ':10110')")
	common.MultiStringFlagVar(&config.RepoURLs, "repo_url", defaultConf.RepoURLs, "Repo url")
	flag.DurationVar((*time.Duration)(&config.RefreshInterval), "refresh", time.Duration(defaultConf.RefreshInterval), "Interval in which to poll git and refresh the GitStore.")

	common.InitWithMust(
		"gitsync",
		common.PrometheusOpt(&config.PromPort),
		common.MetricsLoggingOpt(),
	)
	defer common.Defer()

	// If a configuration file was given we load it into config.
	if *configFile != "" {
		confBytes, err := ioutil.ReadFile(*configFile)
		if err != nil {
			sklog.Fatalf("Error reading config file %s: %s", *configFile, err)
		}

		if err := json5.Unmarshal(confBytes, &config); err != nil {
			sklog.Fatalf("Error parsing config file %s: %s", *configFile, err)
		}
	}

	// Dump the configuration since it might be different than the flags that are dumped by default.
	sklog.Infof("\n\n  Effective configuration: \n%s \n", config.String())

	// Configure the bigtable instance.
	btConfig := &bt_gitstore.BTConfig{
		ProjectID:       config.ProjectID,
		InstanceID:      config.BTInstanceID,
		TableID:         config.BTTableID,
		WriteGoroutines: config.BTWriteGoroutines,
	}

	// Initialize bigtable if invoked with --init and quit.
	// This should be invoked with a user that has admin privileges, so that the production user that
	// wants to write to the instance does not need admin privileges.
	if *runInit {
		if err := bt_gitstore.InitBT(btConfig); err != nil {
			sklog.Fatalf("Error initializing BT: %s", err)
		}
		sklog.Infof("BigTable instance %s and table %s in project %s initialized.", btConfig.InstanceID, btConfig.TableID, btConfig.ProjectID)
		return
	}

	// Make sure we have at least one repo configured.
	if len(config.RepoURLs) == 0 {
		sklog.Fatalf("At least one repository URL must be configured.")
	}

	// TODO(stephana): Pass the token source explicitly to the BigTable related functions below.

	// Create token source.
	ts, err := auth.NewDefaultTokenSource(false, auth.SCOPE_USERINFO_EMAIL, auth.SCOPE_GERRIT)
	if err != nil {
		sklog.Fatalf("Problem setting up default token source: %s", err)
	}

	// Set up Git authentication if a service account email was set.
	gitcookiesPath := ""
	if !config.Local {
		// Use the gitcookie created by the gitauth package.
		gitcookiesPath = "/tmp/gitcookies"
		sklog.Infof("Writing gitcookies to %s", gitcookiesPath)
		if _, err := gitauth.New(ts, gitcookiesPath, true, ""); err != nil {
			sklog.Fatalf("Failed to create git cookie updater: %s", err)
		}
		sklog.Infof("Git authentication set up successfully.")
	}

	// Start all repo watchers.
	ctx := context.Background()
	for _, repoURL := range config.RepoURLs {
		if err := watcher.Start(ctx, btConfig, repoURL, gitcookiesPath, *gcsBucket, *gcsPath, time.Duration(config.RefreshInterval)); err != nil {
			sklog.Fatalf("Error initializing repo watcher: %s", err)
		}
	}

	// Set up the http handler to indicate ready-ness and start serving.
	http.HandleFunc("/healthz", httputils.ReadyHandleFunc)
	sklog.Infof("Listening on port: %s", config.HttpPort)
	log.Fatal(http.ListenAndServe(config.HttpPort, nil))
}

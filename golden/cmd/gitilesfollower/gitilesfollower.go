// The gitilesfollower executable monitors the repo we are tracking using gitiles. It fills in
// the GitCommits table, specifically making a mapping between a GitHash and CommitID. The CommitID
// is based on the "index" of the commit (i.e. how many commits since the initial commit).
//
// This will be used by all clients that have their tests in the same repo as the code under test.
// Clients with more complex repo structures, will need to have an alternate way of linking
// commit_id to git_hash.
package main

import (
	"context"
	"flag"
	"net/http"

	"github.com/jackc/pgx/v4/pgxpool"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/httputils"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/cmd/gitilesfollower/impl"
	"go.goldmine.build/golden/go/config"
	"go.goldmine.build/golden/go/sql"
)

const (
	// Arbitrary number
	maxSQLConnections = 4
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
		"gitilesfollower",
		common.PrometheusOpt(&cfg.PromPort),
	)
	//	if err := tracing.Initialize(1, cfg.SQLDatabaseName); err != nil {
	//		sklog.Fatalf("Could not set up tracing: %s", err)
	//	}

	ctx := context.Background()
	db := mustInitSQLDatabase(ctx, cfg)

	if _, err := impl.StartGitFollower(ctx, cfg, db); err != nil {
		sklog.Fatalf("Could not start gitiles follower: %s", err)
	}

	sklog.Infof("Initial update complete")
	http.HandleFunc("/healthz", httputils.ReadyHandleFunc)
	sklog.Fatal(http.ListenAndServe(cfg.ReadyPort, nil))
}

func mustInitSQLDatabase(ctx context.Context, cfg config.Common) *pgxpool.Pool {
	if cfg.SQLDatabaseName == "" {
		sklog.Fatalf("Must have SQL Database Information")
	}
	url := sql.GetConnectionURL(cfg.SQLConnection, cfg.SQLDatabaseName)
	conf, err := pgxpool.ParseConfig(url)
	if err != nil {
		sklog.Fatalf("error getting postgres config %s: %s", url, err)
	}

	conf.MaxConns = maxSQLConnections
	db, err := pgxpool.ConnectConfig(ctx, conf)
	if err != nil {
		sklog.Fatalf("error connecting to the database: %s", err)
	}
	sklog.Infof("Connected to SQL database %s", cfg.SQLDatabaseName)
	return db
}

package db

import (
	"context"

	"github.com/jackc/pgx/v4/pgxpool"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/go/config"
	"go.goldmine.build/golden/go/sql"
)

const (
	// Arbitrary number
	maxSQLConnections = 20
)

func MustInitSQLDatabase(ctx context.Context, cfg config.Common) *pgxpool.Pool {
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

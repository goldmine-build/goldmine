package db

import (
	"context"
	"sync"

	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/go/config"
	"go.goldmine.build/golden/go/sql"
)

const (
	// Arbitrary number
	maxSQLConnections = 20
)

var (
	db     *pgxpool.Pool = nil
	dbOnce sync.Once
)

// MustInitSQLDatabase initializes a SQL database. If there are any errors, it
// will panic via sklog.Fatal.
func MustInitSQLDatabase(ctx context.Context, cfg config.Common, logSQLQueries bool) *pgxpool.Pool {
	dbOnce.Do(func() {
		db = mustInitSQLDatabaseImpl(ctx, cfg, logSQLQueries)
	})
	return db
}

// crdbLogger logs all SQL statements sent to the database.
type crdbLogger struct{}

func (l crdbLogger) Log(ctx context.Context, level pgx.LogLevel, msg string, data map[string]interface{}) {
	sklog.Infof("[pgxpool %s] %q\n%+v\n", level, msg, data)
}

// mustInitSQLDatabase initializes a SQL database. If there are any errors, it
// will panic via sklog.Fatal.
func mustInitSQLDatabaseImpl(ctx context.Context, cfg config.Common, logSQLQueries bool) *pgxpool.Pool {
	if cfg.SQLDatabaseName == "" {
		sklog.Fatalf("Must have SQL Database Information")
	}
	url := sql.GetConnectionURL(cfg.SQLConnection, cfg.SQLDatabaseName)
	conf, err := pgxpool.ParseConfig(url)
	if err != nil {
		sklog.Fatalf("error getting postgres config %s: %s", url, err)
	}
	if logSQLQueries && cfg.Local {
		conf.ConnConfig.Logger = crdbLogger{}
	}
	conf.MaxConns = maxSQLConnections
	db, err := pgxpool.ConnectConfig(ctx, conf)
	if err != nil {
		sklog.Fatalf("error connecting to the database: %s", err)
	}
	sklog.Infof("Connected to SQL database %s", cfg.SQLDatabaseName)
	return db
}

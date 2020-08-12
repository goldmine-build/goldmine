// Package builders builds objects from config.InstanceConfig objects.
//
// These are functions separate from config.InstanceConfig so that we don't end
// up with cyclical import issues.
package builders

import (
	"context"
	"runtime"
	"strings"

	"cloud.google.com/go/bigtable"
	"cloud.google.com/go/datastore"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	_ "github.com/jackc/pgx/v4/stdlib" // pgx Go sql
	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/ds"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/perf/go/alerts"
	"go.skia.org/infra/perf/go/alerts/dsalertstore"
	"go.skia.org/infra/perf/go/alerts/sqlalertstore"
	"go.skia.org/infra/perf/go/cid"
	"go.skia.org/infra/perf/go/config"
	"go.skia.org/infra/perf/go/file"
	"go.skia.org/infra/perf/go/file/dirsource"
	"go.skia.org/infra/perf/go/file/gcssource"
	perfgit "go.skia.org/infra/perf/go/git"
	"go.skia.org/infra/perf/go/regression"
	"go.skia.org/infra/perf/go/regression/dsregressionstore"
	"go.skia.org/infra/perf/go/regression/sqlregressionstore"
	"go.skia.org/infra/perf/go/shortcut"
	"go.skia.org/infra/perf/go/shortcut/dsshortcutstore"
	"go.skia.org/infra/perf/go/shortcut/sqlshortcutstore"
	"go.skia.org/infra/perf/go/tracestore"
	"go.skia.org/infra/perf/go/tracestore/btts"
	"go.skia.org/infra/perf/go/tracestore/sqltracestore"
	"google.golang.org/api/option"
)

// pgxLogAdaptor allows bubbling pgx logs up into our application.
type pgxLogAdaptor struct{}

// Log a message at the given level with data key/value pairs. data may be nil.
func (pgxLogAdaptor) Log(ctx context.Context, level pgx.LogLevel, msg string, data map[string]interface{}) {
	switch level {
	case pgx.LogLevelTrace:
	case pgx.LogLevelDebug:
	case pgx.LogLevelInfo:
	case pgx.LogLevelWarn:
		sklog.Warningf("pgx - %s %v", msg, data)
	case pgx.LogLevelError:
		sklog.Errorf("pgx - %s %v", msg, data)
	case pgx.LogLevelNone:
	}
}

// newCockroachDBFromConfig opens an existing CockroachDB database.
//
// No migrations are applied automatically, they must be applied by the
// 'migrate' command line application. See COCKROACHDB.md for more details.
func newCockroachDBFromConfig(ctx context.Context, instanceConfig *config.InstanceConfig) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(instanceConfig.DataStoreConfig.ConnectionString)
	if err != nil {
		return nil, skerr.Wrapf(err, "Failed to parse database config: %q", instanceConfig.DataStoreConfig.ConnectionString)
	}
	cfg.MaxConns = int32(runtime.NumCPU())
	cfg.ConnConfig.Logger = pgxLogAdaptor{}
	return pgxpool.ConnectConfig(ctx, cfg)
}

// NewPerfGitFromConfig return a new perfgit.Git for the given instanceConfig.
//
// The instance created does not poll by default, callers need to call
// StartBackgroundPolling().
func NewPerfGitFromConfig(ctx context.Context, local bool, instanceConfig *config.InstanceConfig) (*perfgit.Git, error) {
	if instanceConfig.DataStoreConfig.ConnectionString == "" {
		return nil, skerr.Fmt("A connection_string must always be supplied.")
	}

	switch instanceConfig.DataStoreConfig.DataStoreType {
	case config.GCPDataStoreType:
		if strings.HasPrefix(instanceConfig.DataStoreConfig.ConnectionString, "postgresql://") {
			// This is a temporary path as we migrate away from BigTable to
			// CockroachDB. The first small step in that migration is to host the
			// perfgit Commits table on CockroachDB, which has no analog in the
			// "gcs" world.
		} else {
			return nil, skerr.Fmt("unknown connection_string: Must begin with postgresql://.")
		}
	case config.CockroachDBDataStoreType:
	default:
		return nil, skerr.Fmt("Unknown datastore_type: %q", instanceConfig.DataStoreConfig.DataStoreType)
	}

	// Now create the appropriate db.
	db, err := newCockroachDBFromConfig(ctx, instanceConfig)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	g, err := perfgit.New(ctx, local, db, instanceConfig)
	if err != nil {
		return nil, skerr.Wrap(err)
	}

	return g, nil
}

// NewTraceStoreFromConfig creates a new TraceStore from the InstanceConfig.
//
// If local is true then we aren't running in production.
func NewTraceStoreFromConfig(ctx context.Context, local bool, instanceConfig *config.InstanceConfig) (tracestore.TraceStore, error) {
	switch instanceConfig.DataStoreConfig.DataStoreType {
	case config.GCPDataStoreType:
		ts, err := auth.NewDefaultTokenSource(local, bigtable.Scope)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		traceStore, err := btts.NewBigTableTraceStoreFromConfig(ctx, instanceConfig, ts, false)
		if err != nil {
			return nil, skerr.Wrapf(err, "Failed to open BigTable trace store.")
		}
		return traceStore, nil
	case config.CockroachDBDataStoreType:
		db, err := newCockroachDBFromConfig(ctx, instanceConfig)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		return sqltracestore.New(db, instanceConfig.DataStoreConfig)
	}
	return nil, skerr.Fmt("Unknown datastore type: %q", instanceConfig.DataStoreConfig.DataStoreType)
}

func initCloudDatastoreOnce(ctx context.Context, local bool, instanceConfig *config.InstanceConfig) error {
	if ds.DS != nil {
		sklog.Infof("Cloud Datastore has already been initialized.")
		return nil
	}

	sklog.Info("About to create token source.")
	ts, err := auth.NewDefaultTokenSource(local, datastore.ScopeDatastore)
	if err != nil {
		return skerr.Wrapf(err, "Failed to get TokenSource")
	}

	sklog.Info("About to init datastore.")
	if err := ds.InitWithOpt(instanceConfig.DataStoreConfig.Project, instanceConfig.DataStoreConfig.Namespace, option.WithTokenSource(ts)); err != nil {
		return skerr.Wrapf(err, "Failed to init Cloud Datastore")
	}
	return nil
}

// NewAlertStoreFromConfig creates a new alerts.Store from the InstanceConfig.
func NewAlertStoreFromConfig(ctx context.Context, local bool, instanceConfig *config.InstanceConfig) (alerts.Store, error) {
	switch instanceConfig.DataStoreConfig.DataStoreType {
	case config.GCPDataStoreType:
		if err := initCloudDatastoreOnce(ctx, local, instanceConfig); err != nil {
			return nil, skerr.Wrap(err)
		}
		return dsalertstore.New(), nil
	case config.CockroachDBDataStoreType:
		db, err := newCockroachDBFromConfig(ctx, instanceConfig)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		return sqlalertstore.New(db)
	}
	return nil, skerr.Fmt("Unknown datastore type: %q", instanceConfig.DataStoreConfig.DataStoreType)
}

// NewRegressionStoreFromConfig creates a new regression.RegressionStore from
// the InstanceConfig.
//
// If local is true then we aren't running in production.
func NewRegressionStoreFromConfig(ctx context.Context, local bool, cidl *cid.CommitIDLookup, instanceConfig *config.InstanceConfig) (regression.Store, error) {
	switch instanceConfig.DataStoreConfig.DataStoreType {
	case config.GCPDataStoreType:
		if err := initCloudDatastoreOnce(ctx, local, instanceConfig); err != nil {
			return nil, skerr.Wrap(err)
		}

		lookup := func(ctx context.Context, c *cid.CommitID) (*cid.CommitDetail, error) {
			details, err := cidl.Lookup(ctx, []*cid.CommitID{c})
			if err != nil {
				return nil, skerr.Wrap(err)
			}
			return details[0], nil
		}
		return dsregressionstore.NewRegressionStoreDS(lookup), nil
	case config.CockroachDBDataStoreType:
		db, err := newCockroachDBFromConfig(ctx, instanceConfig)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		return sqlregressionstore.New(db)
	}
	return nil, skerr.Fmt("Unknown datastore type: %q", instanceConfig.DataStoreConfig.DataStoreType)
}

// NewShortcutStoreFromConfig creates a new shortcut.Store from the
// InstanceConfig.
func NewShortcutStoreFromConfig(ctx context.Context, local bool, instanceConfig *config.InstanceConfig) (shortcut.Store, error) {
	switch instanceConfig.DataStoreConfig.DataStoreType {
	case config.GCPDataStoreType:
		if err := initCloudDatastoreOnce(ctx, local, instanceConfig); err != nil {
			return nil, skerr.Wrap(err)
		}

		return dsshortcutstore.New(), nil
	case config.CockroachDBDataStoreType:
		db, err := newCockroachDBFromConfig(ctx, instanceConfig)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		return sqlshortcutstore.New(db)
	}
	return nil, skerr.Fmt("Unknown datastore type: %q", instanceConfig.DataStoreConfig.DataStoreType)
}

// NewSourceFromConfig creates a new file.Source from the InstanceConfig.
//
// If local is true then we aren't running in production.
func NewSourceFromConfig(ctx context.Context, instanceConfig *config.InstanceConfig, local bool) (file.Source, error) {
	switch instanceConfig.IngestionConfig.SourceConfig.SourceType {
	case config.GCSSourceType:
		return gcssource.New(ctx, instanceConfig, local)
	case config.DirSourceType:
		n := len(instanceConfig.IngestionConfig.SourceConfig.Sources)
		if n != 1 {
			return nil, skerr.Fmt("For a source_type of 'dir' there must be a single entry for 'sources', found %d.", n)
		}
		return dirsource.New(instanceConfig.IngestionConfig.SourceConfig.Sources[0])
	default:
		return nil, skerr.Fmt("Unknown source_type: %q", instanceConfig.IngestionConfig.SourceConfig.SourceType)
	}
}

package config

import (
	"encoding/json"
	"io"

	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/util"
)

const (
	// MaxSampleTracesPerCluster  is the maximum number of traces stored in a
	// ClusterSummary.
	MaxSampleTracesPerCluster = 50

	// MinStdDev is the smallest standard deviation we will normalize, smaller
	// than this and we presume it's a standard deviation of zero.
	MinStdDev = 0.001

	// GotoRange is the number of commits on either side of a target commit we
	// will display when going through the goto redirector.
	GotoRange = 10
)

// DataStoreType determines what type of datastore to build. Applies to
// tracestore.Store, alerts.Store, regression.Store, and shortcut.Store.
type DataStoreType string

const (
	// GCPDataStoreType is for datastores in a Google Cloud Project, i.e.
	// BigTable for tracestore.Store, and the rest in Cloud Datastore..
	GCPDataStoreType DataStoreType = "gcp"

	// SQLite3DataStoreType is for storing all data in an SQLite3 database.
	SQLite3DataStoreType DataStoreType = "sqlite3"

	// CockroachDBDataStoreType is for storing all data in a CockroachDB database.
	CockroachDBDataStoreType DataStoreType = "cockroachdb"
)

// DataStoreConfig is the configuration for how Perf stores data.
type DataStoreConfig struct {
	// DataStoreType determines what type of datastore to build. This value will
	// determine how the rest of the DataStoreConfig values are interpreted.
	DataStoreType DataStoreType `json:"datastore_type"`

	// If the datastore type is 'sqlite3' this value is a filename of the
	// database.
	//
	// If the datastore type is 'cockroachdb' then this value is a connection
	// string of the form "postgres://...". See
	// https://www.cockroachlabs.com/docs/stable/connection-parameters.html for
	// more details.
	//
	// If the datastore type is 'gcs' then this value is a filename where
	// the sqlite database that caches git information should be stored.
	//
	// In addition, for 'cockroachdb' databases, the database name given in the
	// connection string must exist and the user given in the connection string
	// must have rights to create, delete, and alter tables as Perf will do
	// database migrations on startup.
	ConnectionString string `json:"connection_string"`

	// TileSize is the size of each tile in commits. This value is used for all
	// datastore types.
	TileSize int32 `json:"tile_size"`

	// Project is the Google Cloud Project name. This value is only used for
	// 'gcp' datastore types.
	Project string `json:"project"`

	// Instance is the name of the BigTable instance. This value is only used
	// for 'gcp' datastore types.
	Instance string `json:"instance"`

	// Table is the name of the table in BigTable to use. This value is only
	// used for 'gcp' datastore types.
	Table string `json:"table"`

	// Shards is the number of shards to break up all trace data into.
	Shards int32 `json:"shards"`

	// Namespace is the Google Cloud Datastore namespace that alerts,
	// regressions, and shortcuts should use. This value is only used for 'gcp'
	// datastore types.
	Namespace string `json:"namespace"`
}

// SourceType determines what type of file.Source to build from a SourceConfig.
type SourceType string

const (
	// GCSSourceType is for Google Cloud Storage.
	GCSSourceType SourceType = "gcs"

	// DirSourceType is for a local filesystem directory and is only appropriate
	// for tests and demo mode.
	DirSourceType SourceType = "dir"
)

// SourceConfig is the config for where ingestable files come from.
type SourceConfig struct {
	// SourceType is the type of file.Source to use. This value will determine
	// how the rest of the SourceConfig values are interpreted.
	SourceType SourceType `json:"source_type"`

	// Project is the Google Cloud Project name. Only used for source of type
	// "gcs".
	Project string `json:"project"`

	// Topic is the PubSub topic when new files arrive to be ingested. Only used
	// for source of type "gcs".
	Topic string `json:"topic"`

	// Sources is the list of sources of data files. For a source of "gcs" this
	// is a list of Google Cloud Storage URLs, e.g.
	// "gs://skia-perf/nano-json-v1". For a source of type "dir" is must only
	// have a single entry and be populated with a local filesystem directory
	// name.
	Sources []string `json:"sources"`

	// RejectIfNameMatches is a regex. If it matches the file.Name then the file
	// will be ignored. Leave the empty string to disable rejection.
	RejectIfNameMatches string `json:"reject_if_name_matches"`

	// AcceptIfNameMatches is a regex. If it matches the file.Name the file will
	// be processed. Leave the empty string to accept all files.
	AcceptIfNameMatches string `json:"accept_if_name_matches"`
}

// IngestionConfig is the configuration for how source files are ingested into
// being traces in a TraceStore.
type IngestionConfig struct {
	// SourceConfig is the config for where files to ingest come from.
	SourceConfig SourceConfig `json:"source_config"`

	// Branches, if populated then restrict to ingesting just these branches.
	Branches []string `json:"branches"`

	// FileIngestionTopicName is the PubSub topic name we should use if doing
	// event driven regression detection. The ingesters use this to know where
	// to emit events to, and the clusterers use this to know where to make a
	// subscription.
	//
	// Should only be turned on for instances that have a huge amount of data,
	// i.e. >500k traces, and that have sparse data.
	//
	// This should really go away, IngestionConfig should be used to build
	// an interface that ingests files and optionally provides a channel
	// of events when a file is ingested.
	FileIngestionTopicName string `json:"file_ingestion_pubsub_topic_name"`
}

// GitAuthType is the type of authentication Git should use, if any.
type GitAuthType string

const (
	// GitAuthNone implies no authentication is needed when cloning/pulling a
	// Git repo, i.e. it is public. The value is the empty string so that the
	// default is no authentication.
	GitAuthNone GitAuthType = ""

	// GitAuthGerrit is for repos that are hosted by Gerrit and require
	// authentication. This setting implies that a
	// GOOGLE_APPLICATION_CREDENTIALS environment variable will be set and the
	// associated service account has read access to the Gerrit repo.
	GitAuthGerrit GitAuthType = "gerrit"
)

// GitRepoConfig is the config for the git repo.
type GitRepoConfig struct {
	// The type of authentication the repo requires.
	GitAuthType GitAuthType `json:"git_auth_type"`

	// URL that the Git repo is fetched from.
	URL string `json:"url"`

	// The directory into which the repo should be checked out.
	Dir string `json:"dir"`

	// DebouceCommitURL signals if a link to a Git commit needs to be specially
	// dereferenced. That is, some repos are synthetic and just contain a single
	// file that changes, with a commit message that is a URL that points to the
	// true source of information. If this value is true then links to commits
	// need to be debounced and use the commit message instead.
	DebouceCommitURL bool `json:"debounce_commit_url"`
}

// InstanceConfig contains all the info needed by btts.BigTableTraceStore.
//
// May eventually move to a separate config file.
type InstanceConfig struct {
	// URL is the root URL at which this instance is available, for example: "https://example.com".
	URL string `json:"URL"`

	DataStoreConfig DataStoreConfig `json:"data_store_config"`
	IngestionConfig IngestionConfig `json:"ingestion_config"`
	GitRepoConfig   GitRepoConfig   `json:"git_repo_config"`
}

// InstanceConfigFromFile returns the deserialized JSON of an InstanceConfig found in filename.
func InstanceConfigFromFile(filename string) (*InstanceConfig, error) {
	var instanceConfig InstanceConfig

	err := util.WithReadFile(filename, func(r io.Reader) error {
		return json.NewDecoder(r).Decode(&instanceConfig)
	})
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	return &instanceConfig, nil
}

// Config is the currently running config.
var Config *InstanceConfig

// Init loads the selected config by name.
func Init(filename string) error {
	cfg, err := InstanceConfigFromFile(filename)
	if err != nil {
		return skerr.Wrap(err)
	}
	Config = cfg
	return nil
}

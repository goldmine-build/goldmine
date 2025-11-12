package config

import (
	"io"
	"reflect"

	"github.com/flynn/json5"
	"go.goldmine.build/go/config"
	"go.goldmine.build/go/git/provider"
	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/util"
)

type IngestionServerConfig struct {

	// As of 2019, the primary way to ingest data is event-driven. That is, when
	// new files are put into a GCS bucket, PubSub fires an event and that is the
	// primary way for an ingester to be notified about a file.
	// The 2 parameters below configure the manual polling of the source, which
	// is a backup way to ingest data in the unlikely case that a PubSub event is
	// dropped (PubSub will try and re-try to send events for up to seven days by default).
	// BackupPollInterval is how often to do a scan.
	BackupPollInterval config.Duration `json:"backup_poll_interval"`

	// BackupPollScope is how far back in time to scan. It should be longer than BackupPollInterval.
	BackupPollScope config.Duration `json:"backup_poll_scope"`

	// IngestionFilesTopic is the PubSub topic on which messages will be placed that correspond
	// to files to ingest.
	IngestionFilesTopic string `json:"ingestion_files_topic"`

	// IngestionSubscription is the subscription ID used by all replicas. By setting the
	// subscriber ID to be the same on all replicas, only one of the replicas will get each
	// event (usually). We like our subscription names to be unique and keyed to the instance,
	// for easier following up on "Why are there so many backed up messages?"
	IngestionSubscription string `json:"ingestion_subscription"`

	// FilesProcessedInParallel controls how many goroutines are used to process PubSub messages.
	// The default is 4, but if instances are handling lots of small files, this can be increased.
	FilesProcessedInParallel int `json:"files_processed_in_parallel" optional:"true"`

	// PrimaryBranchConfig describes how the primary branch ingestion should be configured.
	PrimaryBranchConfig IngesterConfig `json:"primary_branch_config"`

	// PubSubFetchSize is how many worker messages to ask PubSub for. This defaults to 10, but for
	// instances that have many small files ingested, this can be higher for better utilization
	// and throughput.
	PubSubFetchSize int `json:"pubsub_fetch_size" optional:"true"`
}

// IngesterConfig is the configuration for a single ingester.
type IngesterConfig struct {

	// Type describes the backend type of the ingester.
	Type string `json:"type"`

	// Source is where the ingester will read files from.
	Source GCSSourceConfig `json:"gcs_source"`

	// ExtraParams help configure the ingester and are specific to the backend type.
	ExtraParams map[string]string `json:"extra_configuration"`
}

// GCSSourceConfig is the configuration needed to ingest from files in a GCS bucket.
type GCSSourceConfig struct {
	Bucket string `json:"bucket"`
	Prefix string `json:"prefix"`
}

type ExtractionTechnique string

const (
	// ReviewedLine corresponds to looking for a Reviewed-on line in the commit message.
	ReviewedLine = ExtractionTechnique("ReviewedLine")

	// FromSubject corresponds to looking at the title for a CL ID in square brackets.
	FromSubject = ExtractionTechnique("FromSubject")
)

type RepoFollowerConfig struct {

	// InitialCommit that we will use if there are no existing commits in the DB. It will be counted
	// like a "commit zero", which we actually assign to commit 1 billion in case we need to go back
	// in time, we can sort our commit_ids without resorting to negative numbers.
	InitialCommit string `json:"initial_commit"`

	// ExtractionTechnique codifies the methods for linking (via a commit message/body) to a CL.
	ExtractionTechnique ExtractionTechnique `json:"extraction_technique"`

	// SystemName is the abbreviation that is given to a given CodeReviewSystem.
	SystemName string `json:"system_name"`

	// LegacyUpdaterInUse indicates the status of the CLs should not be changed because the source
	// of truth for expectations is still Firestore, which is controlled by gold_frontend.
	// This should be able to be removed after the SQL migration is complete.
	LegacyUpdaterInUse bool `json:"legacy_updater_in_use"`

	// PollPeriod is how often we should poll the source of truth.
	PollPeriod config.Duration `json:"poll_period"`
}

// The Common struct is a set of configuration values that are the same across all instances.
// Not all instances will use every field in Common, but every field in Common is used in at least
// two instances (otherwise, it can be deferred to the config specific to its only user). Common
// should be embedded in all configs specific to a given instance (aka. "Specific Configs").
// If a field is defined in both Common and a given specific config, there will be problems, so
// don't do that.
type Common struct {
	// One or more code review systems that we support linking to / commenting on, etc. Used also to
	// identify valid CLs when ingesting data.
	CodeReviewSystems []CodeReviewSystem `json:"code_review_systems"`

	// Google Cloud Storage bucket name.
	GCSBucket string `json:"gcs_bucket"`

	// The primary branch of the git repo to track, e.g. "main".
	GitRepoBranch string `json:"git_repo_branch"`

	// The URL to the git repo that this instance tracks.
	GitRepoURL string `json:"git_repo_url"`

	// GitAuthType is the type of authentication the repo requires. Defaults to
	// GitAuthNone.
	GitAuthType provider.GitAuthType `json:"git_auth_type,omitempty"`

	// Provider is the method used to interrogate git repos.
	Provider provider.GitProvider `json:"provider"`

	// GCS path, where the known hashes file should be stored. Format: <bucket>/<path>.
	KnownHashesGCSPath string `json:"known_hashes_gcs_path"`

	// Metrics service address (e.g., ':20000')
	PromPort string `json:"prom_port"`

	// Project ID that houses the pubsub topic.
	PubsubProjectID string `json:"pubsub_project_id"`

	// The port to provide a web handler for /healthz and any other web requests.
	ReadyPort string `json:"ready_port"`

	// URL where this app is hosted.
	SiteURL string `json:"site_url"`

	// SQL username, host and port; typically root@localhost:26234 or root@gold-cockroachdb:26234
	SQLConnection string `json:"sql_connection" optional:"true"`

	// SQL Database name; typically the instance id. e.g. 'flutter', 'skia', etc
	SQLDatabaseName string `json:"sql_database" optional:"true"`

	// TracingProportion overrides the per-service default, which is handy for debugging.
	TracingProportion float64 `json:"tracing_proportion" optional:"true"`

	// Number of recent commits to include in the sliding window of data analysis. Also called the
	// tile size.
	WindowSize int `json:"window_size"`

	// If provided (e.g. ":9002"), a port serving performance-related and other debugging RPCS will
	// be opened up. This RPC will not require authentication.
	DebugPort string `json:"debug_port" optional:"true"`

	// If running locally (not in production).
	Local bool `json:"local"`

	// GroupingParamKeysByCorpus is a map from corpus name to the list of keys that comprise the
	// corpus' grouping.
	GroupingParamKeysByCorpus map[string][]string `json:"grouping_param_keys_by_corpus"`

	// RepoFollowerConfig contains settings specific to the repo follower, i.e. the commits ingestion.
	RepoFollowerConfig RepoFollowerConfig `json:"repo_follower_config"`

	// IngestionServerConfig contains settings specific to the ingestion server.
	IngestionServerConfig IngestionServerConfig `json:"ingestion_server_config"`
}

// CodeReviewSystem represents the details needed to interact with a CodeReviewSystem (e.g.
// "gerrit", "github")
type CodeReviewSystem struct {
	// ID is how this CRS will be identified via query arguments and ingestion data. This is arbitrary
	// and can be used to distinguish between and internal and public version (e.g. "gerrit-internal")
	ID string `json:"id"`

	// Specifies the APIs/code needed to interact ("gerrit", "github").
	Flavor string `json:"flavor"`

	// A URL with %s where a CL ID should be placed to complete it.
	URLTemplate string `json:"url_template"`

	// URL of the Gerrit instance (if any) where we retrieve CL metadata.
	GerritURL string `json:"gerrit_url" optional:"true"`

	// Filepath to file containing GitHub token (if this instance needs to talk to GitHub).
	GitHubCredPath string `json:"github_cred_path" optional:"true"`

	// User and repo of GitHub project to connect to (if any), e.g. google/skia
	GitHubRepo string `json:"github_repo" optional:"true"`
}

// LoadFromJSON5 reads the contents of path and tries to decode the JSON5 there into the provided
// struct. The passed in struct pointer is expected to have "json" struct tags for all fields.
// An error will be returned if any non-struct, non-bool field is its zero value *unless* it is
// tagged with `optional:"true"`.
func LoadFromJSON5(dst interface{}, commonConfigPath, specificConfigPath *string) error {
	// Elem() dereferences a pointer or panics.
	rType := reflect.TypeOf(dst).Elem()
	if rType.Kind() != reflect.Struct {
		return skerr.Fmt("Input must be a pointer to a struct, got %T", dst)
	}
	err := util.WithReadFile(*commonConfigPath, func(r io.Reader) error {
		return json5.NewDecoder(r).Decode(&dst)
	})
	if err != nil {
		return skerr.Wrapf(err, "reading common config at %s", *commonConfigPath)
	}
	err = util.WithReadFile(*specificConfigPath, func(r io.Reader) error {
		return json5.NewDecoder(r).Decode(&dst)
	})
	if err != nil {
		return skerr.Wrapf(err, "reading specific config at %s", *specificConfigPath)
	}

	rValue := reflect.Indirect(reflect.ValueOf(dst))
	return checkRequired(rValue)
}

func LoadConfigFromJSON5(configPath string) (Common, error) {
	var ret Common
	err := util.WithReadFile(configPath, func(r io.Reader) error {
		return json5.NewDecoder(r).Decode(&ret)
	})
	if err != nil {
		return ret, skerr.Wrapf(err, "reading config at %s", configPath)
	}
	return ret, nil
}

// checkRequired returns an error if any non-struct, non-bool fields of the given value have a zero
// value *unless* they have an optional tag with value true.
func checkRequired(rValue reflect.Value) error {
	rType := rValue.Type()
	for i := 0; i < rValue.NumField(); i++ {
		field := rType.Field(i)
		if field.Type.Kind() == reflect.Struct {
			if err := checkRequired(rValue.Field(i)); err != nil {
				return err
			}
			continue
		}
		if field.Type.Kind() == reflect.Bool {
			// For ease of use, booleans aren't compared against their zero value, since that would
			// effectively make them required to be true always.
			continue
		}
		isJSON := field.Tag.Get("json")
		if isJSON == "" {
			// don't validate struct values w/o json tags (e.g. config.Duration.Duration).
			continue
		}
		isOptional := field.Tag.Get("optional")
		if isOptional == "true" {
			continue
		}
		// defaults to being required
		if rValue.Field(i).IsZero() {
			return skerr.Fmt("Required %s to be non-zero", field.Name)
		}
	}
	return nil
}

// Duration allows us to supply a duration as a human readable string.
type Duration = config.Duration

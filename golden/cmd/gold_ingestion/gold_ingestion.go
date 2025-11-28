// gold_ingestion is the server process that runs an arbitrary number of
// ingesters and stores them to the appropriate backends.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/storage"
	"github.com/jackc/pgx/v4/pgxpool"
	"go.opencensus.io/trace"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/httputils"
	"go.goldmine.build/go/metrics2"
	"go.goldmine.build/go/now"
	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/go/util"
	"go.goldmine.build/golden/cmd/gitilesfollower/impl"
	"go.goldmine.build/golden/go/config"
	"go.goldmine.build/golden/go/ingestion"
	"go.goldmine.build/golden/go/ingestion/sqlingestionstore"
	"go.goldmine.build/golden/go/ingestion_processors"
	"go.goldmine.build/golden/go/sql"
)

const (
	// Arbitrarily picked.
	maxSQLConnections = 20
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
		"gold-ingestion",
		common.PrometheusOpt(&cfg.PromPort),
	)
	// We expect there to be a lot of ingestion work, so we sample 1% of them to avoid incurring
	// too much overhead.
	//	if err := tracing.Initialize(0.01, isc.SQLDatabaseName); err != nil {
	//		sklog.Fatalf("Could not set up tracing: %s", err)
	//	}

	ctx := context.Background()

	if cfg.SQLDatabaseName == "" {
		sklog.Fatalf("Must have SQL database config")
	}
	url := sql.GetConnectionURL(cfg.SQLConnection, cfg.SQLDatabaseName)
	conf, err := pgxpool.ParseConfig(url)
	if err != nil {
		sklog.Fatalf("error getting postgres config %s: %s", url, err)
	}

	conf.MaxConns = maxSQLConnections
	sqlDB, err := pgxpool.ConnectConfig(ctx, conf)
	if err != nil {
		sklog.Fatalf("error connecting to the database: %s", err)
	}
	ingestionStore := sqlingestionstore.New(sqlDB)
	sklog.Infof("Using new SQL ingestion store")

	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		sklog.Fatalf("Could not create GCS Client")
	}
	checkForNewCommits, err := impl.StartGitFollower(ctx, cfg, sqlDB)
	if err != nil {
		sklog.Fatalf("Could not start gitiles follower: %s", err)
	}

	primaryBranchProcessor, src, err := getPrimaryBranchIngester(ctx, cfg.IngestionServerConfig.PrimaryBranchConfig, gcsClient, sqlDB, checkForNewCommits)
	if err != nil {
		sklog.Fatalf("Setting up primary branch ingestion: %s", err)
	}
	sourcesToScan := []ingestion.FileSearcher{src}

	var secondaryBranchLiveness metrics2.Liveness
	tryjobProcessor, src, err := getSecondaryBranchIngester(ctx, isc.SecondaryBranchConfig, gcsClient, client, sqlDB)
	if err != nil {
		sklog.Fatalf("Setting up secondary branch ingestion: %s", err)
	}
	if src != nil {
		sourcesToScan = append(sourcesToScan, src)
		secondaryBranchLiveness = metrics2.NewLiveness("gold_ingestion", map[string]string{
			"metric": "since_last_successful_streaming_result",
			"source": "secondary_branch",
		})
	}

	pss := &pubSubSource{
		IngestionStore:         ingestionStore,
		PrimaryBranchProcessor: primaryBranchProcessor,
		TryjobProcessor:        tryjobProcessor,
		PrimaryBranchStreamingLiveness: metrics2.NewLiveness("gold_ingestion", map[string]string{
			"metric": "since_last_successful_streaming_result",
			"source": "primary_branch",
		}),
		SecondaryBranchStreamingLiveness: secondaryBranchLiveness,
		SuccessCounter:                   metrics2.GetCounter("gold_ingestion_success"),
		FailedCounter:                    metrics2.GetCounter("gold_ingestion_failure"),
	}

	go func() {
		// Wait at least 5 seconds for the pubsub connection to be initialized before saying
		// we are healthy.
		time.Sleep(5 * time.Second)
		http.HandleFunc("/healthz", httputils.ReadyHandleFunc)
		sklog.Fatal(http.ListenAndServe(cfg.ReadyPort, nil))
	}()

	startBackupPolling(ctx, cfg, sourcesToScan, pss)
	startMetrics(ctx, pss)

	sklog.Fatalf("Listening for files to ingest %s", listen(ctx, cfg, pss))
}

func getPrimaryBranchIngester(ctx context.Context, conf config.IngesterConfig, gcsClient *storage.Client, db *pgxpool.Pool, checkForNewCommits impl.CheckForNewCommitsFunc) (ingestion.Processor, ingestion.FileSearcher, error) {
	src := &ingestion.GCSSource{
		Client: gcsClient,
		Bucket: conf.Source.Bucket,
		Prefix: conf.Source.Prefix,
	}
	if ok := src.Validate(); !ok {
		return nil, nil, skerr.Fmt("Invalid GCS Source %#v", src)
	}

	var primaryBranchProcessor ingestion.Processor
	if conf.Type == ingestion_processors.SQLPrimaryBranch {
		sqlProcessor := ingestion_processors.PrimaryBranchSQL(src, conf.ExtraParams, db, checkForNewCommits)
		sqlProcessor.MonitorCacheMetrics(ctx)
		primaryBranchProcessor = sqlProcessor
		sklog.Infof("Configured SQL primary branch ingestion")
	} else {
		return nil, nil, skerr.Fmt("unknown ingestion backend: %q", conf.Type)
	}
	return primaryBranchProcessor, src, nil
}

// listen begins listening to the PubSub topic with the configured PubSub subscription. It will
// fail if the topic or subscription have not been created or PubSub fails.
func listen(ctx context.Context, cfg config.Common, p *pubSubSource) error {
	psc, err := pubsub.NewClient(ctx, cfg.PubsubProjectID)
	if err != nil {
		return skerr.Wrapf(err, "initializing pubsub client for project %s", cfg.PubsubProjectID)
	}

	// Check that the topic exists. Fail if it does not.
	t := psc.Topic(cfg.IngestionServerConfig.IngestionFilesTopic)
	if exists, err := t.Exists(ctx); err != nil {
		return skerr.Wrapf(err, "checking for existing topic %s", cfg.IngestionServerConfig.IngestionFilesTopic)
	} else if !exists {
		return skerr.Fmt("Diff work topic %s does not exist in project %s", cfg.IngestionServerConfig.IngestionFilesTopic, cfg.PubsubProjectID)
	}

	// Check that the subscription exists. Fail if it does not.
	sub := psc.Subscription(cfg.IngestionServerConfig.IngestionSubscription)
	if exists, err := sub.Exists(ctx); err != nil {
		return skerr.Wrapf(err, "checking for existing subscription %s", cfg.IngestionServerConfig.IngestionSubscription)
	} else if !exists {
		return skerr.Fmt("subscription %s does not exist in project %s", cfg.IngestionServerConfig.IngestionSubscription, cfg.PubsubProjectID)
	}

	// This is a limit of how many messages to fetch when PubSub has no work. Waiting for PubSub
	// to give us messages can take a second or two, so we choose a small, but not too small
	// batch size.
	if cfg.IngestionServerConfig.PubSubFetchSize == 0 {
		sub.ReceiveSettings.MaxOutstandingMessages = 10
	} else {
		sub.ReceiveSettings.MaxOutstandingMessages = cfg.IngestionServerConfig.PubSubFetchSize
	}

	if cfg.IngestionServerConfig.FilesProcessedInParallel == 0 {
		sub.ReceiveSettings.NumGoroutines = 4
	} else {
		sub.ReceiveSettings.NumGoroutines = cfg.IngestionServerConfig.FilesProcessedInParallel
	}

	// Blocks until context cancels or PubSub fails in a non retryable way.
	return skerr.Wrap(sub.Receive(ctx, p.ingestFromPubSubMessage))
}

type pubSubSource struct {
	IngestionStore         ingestion.Store
	PrimaryBranchProcessor ingestion.Processor
	TryjobProcessor        ingestion.Processor

	// PrimaryBranchStreamingLiveness lets us have a metric to monitor the successful
	// streaming of data. It will be reset after each successful ingestion of a file from
	// the primary branch.
	PrimaryBranchStreamingLiveness metrics2.Liveness

	// SecondaryBranchStreamingLiveness lets us have a metric to monitor the successful
	// streaming of data. It will be reset after each successful ingestion of a file from
	// the secondary branch.
	SecondaryBranchStreamingLiveness metrics2.Liveness

	SuccessCounter metrics2.Counter
	FailedCounter  metrics2.Counter

	// busy is either 0 or non-zero depending on if this ingestion is working or not. This
	// allows us to gather data on wall-clock utilization.
	busy int64
}

// ingestFromPubSubMessage takes in a PubSub message and looks for a fileName specified as
// the "objectId" Attribute on the message. This is how file names are provided from GCS
// on file changes. https://cloud.google.com/storage/docs/pubsub-notifications#attributes
// It will either Nack or Ack the message depending on if there was a retryable error or not.
func (p *pubSubSource) ingestFromPubSubMessage(ctx context.Context, msg *pubsub.Message) {
	ctx, span := trace.StartSpan(ctx, "ingestion_ingestFromPubSubMessage")
	defer span.End()
	atomic.AddInt64(&p.busy, 1)
	fileName := msg.Attributes["objectId"]
	if shouldAck := p.ingestFile(ctx, fileName); shouldAck {
		msg.Ack()
	} else {
		msg.Nack()
	}
	atomic.AddInt64(&p.busy, -1)
}

// ingestFile ingests the file and returns true if the ingestion was successful or it got
// a non-retryable error. It returns false if it got a retryable error.
func (p *pubSubSource) ingestFile(ctx context.Context, name string) bool {
	if !strings.HasSuffix(name, ".json") {
		return true
	}
	if p.PrimaryBranchProcessor.HandlesFile(name) {
		err := p.PrimaryBranchProcessor.Process(ctx, name)
		if skerr.Unwrap(err) == ingestion.ErrRetryable {
			sklog.Warningf("Got retryable error for primary branch data for file %s", name)
			p.FailedCounter.Inc(1)
			return false
		}
		// TODO(kjlubick) Processors should mark the SourceFiles table as ingested, not here.
		if err := p.IngestionStore.SetIngested(ctx, name, now.Now(ctx)); err != nil {
			sklog.Errorf("Could not write to ingestion store: %s", err)
			// We'll continue anyway. The IngestionStore is not a big deal.
		}
		if err != nil {
			sklog.Errorf("Got non-retryable error for primary branch data for file %s: %s", name, err)
			p.FailedCounter.Inc(1)
			return true
		}
		p.PrimaryBranchStreamingLiveness.Reset()
		p.SuccessCounter.Inc(1)
		return true
	}
	// TODO(kjlubick) Processors should mark the SourceFiles table as ingested, not here.
	if err := p.IngestionStore.SetIngested(ctx, name, time.Now()); err != nil {
		sklog.Errorf("Could not write to ingestion store: %s", err)
		// We'll continue anyway. The IngestionStore is not a big deal.
	}
	p.SuccessCounter.Inc(1)
	return true
}

func startBackupPolling(ctx context.Context, cfg config.Common, sourcesToScan []ingestion.FileSearcher, pss *pubSubSource) {
	if cfg.IngestionServerConfig.BackupPollInterval.Duration <= 0 {
		sklog.Infof("Skipping backup polling")
		return
	}

	pollingLiveness := metrics2.NewLiveness("gold_ingestion", map[string]string{
		"metric": "since_last_successful_poll",
		"source": "combined",
	})

	go util.RepeatCtx(ctx, cfg.IngestionServerConfig.BackupPollInterval.Duration, func(ctx context.Context) {
		ctx, span := trace.StartSpan(ctx, "ingestion_backupPollingCycle", trace.WithSampler(trace.AlwaysSample()))
		defer span.End()
		startTime, endTime := getTimesToPoll(ctx, cfg.IngestionServerConfig.BackupPollScope.Duration)
		totalIgnored, totalProcessed := 0, 0
		sklog.Infof("Starting backup polling for %d sources in time range [%s,%s]", len(sourcesToScan), startTime, endTime)
		for _, src := range sourcesToScan {
			ignored, processed := 0, 0
			files := src.SearchForFiles(ctx, startTime, endTime)
			for _, f := range files {
				ok, err := pss.IngestionStore.WasIngested(ctx, f)
				if err != nil {
					sklog.Errorf("Could not check ingestion store: %s", err)
				}
				if ok {
					ignored++
					continue
				}
				processed++
				pss.ingestFile(ctx, f)
			}
			srcName := "<unknown>"
			// Failure to do this can cause a race condition in tests.
			if stringer, ok := src.(fmt.Stringer); ok {
				srcName = stringer.String()
			}
			sklog.Infof("backup polling for %s processed/ignored: %d/%d", srcName, processed, ignored)
			totalIgnored += ignored
			totalProcessed += processed
		}
		pollingLiveness.Reset()
		sklog.Infof("Total backup polling [%s,%s] processed/ignored: %d/%d", startTime, endTime, totalProcessed, totalIgnored)
	})
}

func getTimesToPoll(ctx context.Context, duration time.Duration) (time.Time, time.Time) {
	endTS := now.Now(ctx).UTC()
	return endTS.Add(-duration), endTS
}

func startMetrics(ctx context.Context, pss *pubSubSource) {
	// This metric will let us get a sense of how well-utilized this processor is. It reads the
	// busy int of the processor (which is 0 when not busy) and increments the counter if the
	// int is non-zero.
	// Because we are updating the counter once per second, we can use rate() [which computes deltas
	// per second] on this counter to get a number between 0 and 1 to indicate wall-clock
	// utilization. Hopefully, this lets us know if we need to add more replicas.
	go func() {
		busy := metrics2.GetCounter("goldingestion_busy_pulses")
		for range time.Tick(time.Second) {
			if err := ctx.Err(); err != nil {
				return
			}
			i := atomic.LoadInt64(&pss.busy)
			if i > 0 {
				busy.Inc(1)
			}
		}
	}()
}

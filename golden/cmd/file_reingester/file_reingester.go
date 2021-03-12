// Executable file_reingester will scan through all the files in a GCS bucket and create synthetic
// pubsub events to cause the files to be re-ingested.
package main

import (
	"context"
	"flag"
	"time"

	"cloud.google.com/go/pubsub"
	"cloud.google.com/go/storage"

	"go.skia.org/infra/go/fileutil"
	"go.skia.org/infra/go/gcs"
	"go.skia.org/infra/go/sklog"
)

func main() {
	var (
		ingesterTopic = flag.String("ingester_topic", "", "Pubsub topic on which to generate synthetic events.")
		projectID     = flag.String("project_id", "skia-public", "GCP project ID.")

		srcBucket  = flag.String("src_bucket", "", "Source bucket to ingest files from.")
		srcRootDir = flag.String("src_root_dir", "dm-json-v1", "Source root directory to ingest files in.")

		// In the early days, there was several invalid entries, because they did not specify
		// gitHash. Starting re-ingesting Skia on October 1, 2014 seems to be around when
		// the data is correct.
		startYear  = flag.Int("start_year", 2019, "year to start ingesting")
		startMonth = flag.Int("start_month", 1, "month to start ingesting")
		startDay   = flag.Int("start_day", 1, "day to start ingesting (at midnight UTC)")
	)
	flag.Parse()

	ctx := context.Background()
	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		sklog.Fatalf("Failed to create GCS client: %s", err)
	}

	psc, err := pubsub.NewClient(ctx, *projectID)
	if err != nil {
		sklog.Fatalf("Could not make pubsub client for project %q: %s", *projectID, err)
	}

	// Check that the topic exists. Fail if it does not.
	topic := psc.Topic(*ingesterTopic)
	if exists, err := topic.Exists(ctx); err != nil {
		sklog.Fatalf("Error checking for existing topic %q: %s", *ingesterTopic, err)
	} else if !exists {
		sklog.Fatalf("topic %s does not exist in project %s", *ingesterTopic, *projectID)
	}

	sklog.Infof("starting scanning %q in project %s", *ingesterTopic, *projectID)

	beginning := time.Date(*startYear, time.Month(*startMonth), *startDay, 0, 0, 0, 0, time.UTC)

	dirs := fileutil.GetHourlyDirs(*srcRootDir, beginning, time.Now())
	for _, dir := range dirs {
		sklog.Infof("Directory: %q", dir)
		var last *pubsub.PublishResult
		err := gcs.AllFilesInDir(gcsClient, *srcBucket, dir, func(item *storage.ObjectAttrs) {
			last = publishSyntheticStorageEvent(ctx, topic, item.Bucket, item.Name)
		})
		if err != nil {
			sklog.Warningf("Error while processing dir %s: %s", dir, err)
		}
		if last != nil {
			_, err := last.Get(context.Background())
			if err != nil {
				sklog.Fatalf("Could not publish: %s", err)
			} else {
				sklog.Debugf("Published something for %s", dir)
			}
		}
	}

	sklog.Infof("waiting for messages to publish")
	topic.Stop()
	sklog.Infof("done")
}

func publishSyntheticStorageEvent(ctx context.Context, topic *pubsub.Topic, bucket, fileName string) *pubsub.PublishResult {
	return topic.Publish(ctx, &pubsub.Message{
		// These are the important attributes read for ingestion.
		// https://cloud.google.com/storage/docs/pubsub-notifications#attributes
		Attributes: map[string]string{
			"bucketId": bucket,
			"objectId": fileName,
		},
		Data: nil, // We don't currently read anything from Data.
	})
}

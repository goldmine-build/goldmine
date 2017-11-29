package swarming_metrics

/*
	Package swarming_metrics generates metrics from Swarming data.
*/

import (
	"bytes"
	"context"
	"encoding/gob"
	"fmt"
	"path"
	"strconv"
	"strings"
	"time"

	swarming_api "go.chromium.org/luci/common/api/swarming/swarming/v1"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/metrics2/events"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/swarming"
	"go.skia.org/infra/go/taskname"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/perf/go/ingestcommon"
	"go.skia.org/infra/perf/go/perfclient"
)

const (
	MEASUREMENT_SWARMING_TASKS = "swarming_task_events"
	STREAM_SWARMING_TASKS      = "swarming-tasks"
)

var (
	DIMENSION_WHITELIST = util.NewStringSet([]string{
		"os",
		"model",
		"cpu",
		"gpu",
		"device_type",
		"device_os",
	})

	errNoValue = fmt.Errorf("no value")
)

// loadSwarmingTasks loads the Swarming tasks which were created within the
// given time range, plus any tasks we're explicitly told to load. Inserts all
// completed tasks into the EventDB and perf. Then, it returns any unfinished
// tasks so that they can be revisited later.
func loadSwarmingTasks(s swarming.ApiClient, edb events.EventDB, perfClient perfclient.ClientInterface, tnp taskname.TaskNameParser, lastLoad, now time.Time, revisit []string) ([]string, error) {
	sklog.Info("Loading swarming tasks.")

	// TODO(borenet): Load tasks for all pools we care about, including
	// internal.
	tasks, err := s.ListSkiaTasks(lastLoad, now)
	if err != nil {
		return nil, err
	}
	for _, id := range revisit {
		task, err := s.GetTaskMetadata(id)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	revisitLater := []string{}
	loaded := 0
	for _, t := range tasks {
		// Don't include de-duped tasks, as they'll skew the metrics down.
		if t.TaskResult.DedupedFrom != "" {
			continue
		}

		// Only include finished tasks. This includes completed success
		// and completed failures.
		if t.TaskResult.State != swarming.TASK_STATE_COMPLETED {
			// Check back on Pending/Running tasks
			if t.TaskResult.State == swarming.TASK_STATE_PENDING ||
				t.TaskResult.State == swarming.TASK_STATE_RUNNING {
				revisitLater = append(revisitLater, t.TaskId)
			}
			continue
		}

		if err := reportDurationToPerf(t, perfClient, now, tnp); err != nil {
			sklog.Errorf("Error reporting task duration to perf: %s", err)
			revisitLater = append(revisitLater, t.TaskId)
		}

		var buf bytes.Buffer
		if err := gob.NewEncoder(&buf).Encode(t); err != nil {
			return nil, fmt.Errorf("Failed to serialize Swarming task: %s", err)
		}
		created, err := swarming.Created(t)
		if err != nil {
			return nil, fmt.Errorf("Failed to parse Created time: %s", err)
		}
		if err := edb.Insert(&events.Event{
			Stream:    STREAM_SWARMING_TASKS,
			Timestamp: created,
			Data:      buf.Bytes(),
		}); err != nil {
			return nil, fmt.Errorf("Failed to insert event: %s", err)
		}
		loaded++
	}
	sklog.Infof("... loaded %d swarming tasks.", loaded)
	return revisitLater, nil
}

func reportDurationToPerf(t *swarming_api.SwarmingRpcsTaskRequestMetadata, perfClient perfclient.ClientInterface, now time.Time, tnp taskname.TaskNameParser) error {

	// Pull taskName from tags, because the task name could be changed (e.g. retries)
	// and that would make ParseTaskName not happy.
	taskName := ""
	taskRevision := ""
	repo := ""
	for _, tag := range t.Request.Tags {
		if strings.HasPrefix(tag, "sk_revision") {
			taskRevision = strings.SplitN(tag, ":", 2)[1]
		}
		if strings.HasPrefix(tag, "sk_name") {
			taskName = strings.SplitN(tag, ":", 2)[1]
		}
		if strings.HasPrefix(tag, "sk_repo") {
			repo = strings.SplitN(tag, ":", 2)[1]
		}
	}
	if repo != common.REPO_SKIA {
		// The schema parser only supports the Skia repo, not, for example, the Infra repo
		// which would also show up here.
		return nil
	}
	parsed, err := tnp.ParseTaskName(taskName)
	if err != nil {
		sklog.Errorf("Could not parse task name of %s: %s", taskName, err)
		// return nil here instead of error because the calling code will attempt to
		// retry errors. Presumably parsing the task name would always fail.
		return nil
	}
	if t.TaskResult.InternalFailure {
		// Skip bots that died because of infra reasons (e.g. bot lost power)
		return nil
	}
	parsed["failure"] = strconv.FormatBool(t.TaskResult.Failure)

	durations := ingestcommon.BenchResults{
		"task_duration": {
			"task_step_s":        t.TaskResult.Duration,
			"all_overhead_s":     t.TaskResult.PerformanceStats.BotOverhead,
			"isolate_overhead_s": t.TaskResult.PerformanceStats.IsolatedDownload.Duration + t.TaskResult.PerformanceStats.IsolatedUpload.Duration,
			"total_s":            t.TaskResult.Duration + t.TaskResult.PerformanceStats.BotOverhead,
		},
	}
	toReport := ingestcommon.BenchData{
		Hash: taskRevision,
		Key:  parsed,
		Results: map[string]ingestcommon.BenchResults{
			taskName: durations,
		},
	}

	sklog.Debugf("Reporting that %s had these durations: %#v ms", taskName, durations)

	if err := perfClient.PushToPerf(now, taskName, "task_duration", toReport); err != nil {
		return fmt.Errorf("Ran into error while pushing task duration to perf: %s", err)
	}
	return nil

}

// decodeTasks decodes a slice of events.Event into a slice of Swarming task
// metadata.
func decodeTasks(ev []*events.Event) ([]*swarming_api.SwarmingRpcsTaskRequestMetadata, error) {
	rv := make([]*swarming_api.SwarmingRpcsTaskRequestMetadata, 0, len(ev))
	for _, e := range ev {
		var t swarming_api.SwarmingRpcsTaskRequestMetadata
		if err := gob.NewDecoder(bytes.NewBuffer(e.Data)).Decode(&t); err != nil {
			return nil, err
		}
		rv = append(rv, &t)
	}
	return rv, nil
}

// addMetric adds a dynamic metric to the given event stream with the given
// metric name. It aggregates the data points returned by the given helper
// function and computes the mean for each tag set. This simplifies the addition
// of metrics in StartSwarmingTaskMetrics.
func addMetric(s *events.EventStream, metric string, period time.Duration, fn func(*swarming_api.SwarmingRpcsTaskRequestMetadata) (int64, error)) error {
	tags := map[string]string{
		"metric": metric,
	}
	f := func(ev []*events.Event) ([]map[string]string, []float64, error) {
		sklog.Infof("Computing value(s) for metric %q", metric)
		if len(ev) == 0 {
			return []map[string]string{}, []float64{}, nil
		}
		tasks, err := decodeTasks(ev)
		if err != nil {
			return nil, nil, err
		}
		tagSets := map[string]map[string]string{}
		totals := map[string]int64{}
		counts := map[string]int{}
		for _, t := range tasks {
			val, err := fn(t)
			if err == errNoValue {
				continue
			}
			if err != nil {
				return nil, nil, err
			}
			tags := map[string]string{
				"task_name": t.TaskResult.Name,
			}
			for d := range DIMENSION_WHITELIST {
				tags[d] = ""
			}
			for _, dim := range t.Request.Properties.Dimensions {
				if _, ok := DIMENSION_WHITELIST[dim.Key]; ok {
					tags[dim.Key] = dim.Value
				}
			}
			key, err := util.MD5Params(tags)
			if err != nil {
				return nil, nil, err
			}
			tagSets[key] = tags
			totals[key] += val
			counts[key]++
		}
		tagSetsList := make([]map[string]string, 0, len(tagSets))
		vals := make([]float64, 0, len(tagSets))
		for key, tags := range tagSets {
			tagSetsList = append(tagSetsList, tags)
			vals = append(vals, float64(totals[key])/float64(counts[key]))
		}
		return tagSetsList, vals, nil
	}
	return s.DynamicMetric(tags, period, f)
}

// taskDuration returns the duration of the task in milliseconds.
func taskDuration(t *swarming_api.SwarmingRpcsTaskRequestMetadata) (int64, error) {
	completedTime, err := swarming.Completed(t)
	if err != nil {
		return 0.0, err
	}
	startTime, err := swarming.Started(t)
	if err != nil {
		return 0.0, err
	}
	return int64(completedTime.Sub(startTime).Seconds() * float64(1000.0)), nil
}

// taskPendingTime returns the pending time of the task in milliseconds.
func taskPendingTime(t *swarming_api.SwarmingRpcsTaskRequestMetadata) (int64, error) {
	createdTime, err := swarming.Created(t)
	if err != nil {
		return 0.0, err
	}
	startTime, err := swarming.Started(t)
	if err != nil {
		return 0.0, err
	}
	return int64(startTime.Sub(createdTime).Seconds() * float64(1000.0)), nil
}

// taskOverheadBot returns the bot overhead for the task in milliseconds.
func taskOverheadBot(t *swarming_api.SwarmingRpcsTaskRequestMetadata) (int64, error) {
	if t.TaskResult.PerformanceStats == nil {
		return 0, errNoValue
	} else {
		return int64(t.TaskResult.PerformanceStats.BotOverhead * float64(1000.0)), nil
	}
}

// taskOverheadUpload returns the upload overhead for the task in milliseconds.
func taskOverheadUpload(t *swarming_api.SwarmingRpcsTaskRequestMetadata) (int64, error) {
	if t.TaskResult.PerformanceStats == nil {
		return 0, errNoValue
	} else if t.TaskResult.PerformanceStats.IsolatedUpload == nil {
		return 0, errNoValue
	} else {
		return int64(t.TaskResult.PerformanceStats.IsolatedUpload.Duration * float64(1000.0)), nil
	}
}

// taskOverheadDownload returns the download overhead for the task in milliseconds.
func taskOverheadDownload(t *swarming_api.SwarmingRpcsTaskRequestMetadata) (int64, error) {
	if t.TaskResult.PerformanceStats == nil {
		return 0, errNoValue
	} else if t.TaskResult.PerformanceStats.IsolatedDownload == nil {
		return 0, errNoValue
	} else {
		return int64(t.TaskResult.PerformanceStats.IsolatedDownload.Duration * float64(1000.0)), nil
	}
}

// setupMetrics creates the event metrics for Swarming tasks.
func setupMetrics(workdir string) (events.EventDB, *events.EventMetrics, error) {
	edb, err := events.NewEventDB(path.Join(workdir, "swarming-tasks.bdb"))
	if err != nil {
		return nil, nil, err
	}
	em, err := events.NewEventMetrics(edb, MEASUREMENT_SWARMING_TASKS)
	if err != nil {
		return nil, nil, err
	}
	s := em.GetEventStream(STREAM_SWARMING_TASKS)

	// Add metrics.
	for _, period := range []time.Duration{24 * time.Hour, 7 * 24 * time.Hour} {
		// Duration.
		if err := addMetric(s, "duration", period, taskDuration); err != nil {
			return nil, nil, err
		}

		// Pending time.
		if err := addMetric(s, "pending-time", period, taskPendingTime); err != nil {
			return nil, nil, err
		}

		// Overhead (bot).
		if err := addMetric(s, "overhead-bot", period, taskOverheadBot); err != nil {
			return nil, nil, err
		}

		// Overhead (upload).
		if err := addMetric(s, "overhead-upload", period, taskOverheadUpload); err != nil {
			return nil, nil, err
		}

		// Overhead (download).
		if err := addMetric(s, "overhead-download", period, taskOverheadDownload); err != nil {
			return nil, nil, err
		}
	}
	return edb, em, nil
}

// startLoadingTasks initiates the goroutine which periodically loads Swarming
// tasks into the EventDB.
func startLoadingTasks(swarm swarming.ApiClient, ctx context.Context, edb events.EventDB, perfClient perfclient.ClientInterface, tnp taskname.TaskNameParser) {
	// Start collecting the metrics.
	lv := metrics2.NewLiveness("last_successful_swarming_task_metrics")
	lastLoad := time.Now().Add(-2 * time.Minute)
	revisitTasks := []string{}
	go util.RepeatCtx(10*time.Minute, ctx, func() {
		now := time.Now()
		revisit, err := loadSwarmingTasks(swarm, edb, perfClient, tnp, lastLoad, now, revisitTasks)
		if err != nil {
			sklog.Errorf("Failed to load swarming tasks into metrics: %s", err)
		} else {
			lastLoad = now
			revisitTasks = revisit
			lv.Reset()
		}
	})
}

// StartSwarmingTaskMetrics initiates a goroutine which loads Swarming task
// results and computes metrics.
func StartSwarmingTaskMetrics(workdir string, swarm swarming.ApiClient, ctx context.Context, perfClient perfclient.ClientInterface, tnp taskname.TaskNameParser) error {
	edb, em, err := setupMetrics(workdir)
	if err != nil {
		return err
	}
	em.Start(ctx)
	startLoadingTasks(swarm, ctx, edb, perfClient, tnp)
	return nil
}

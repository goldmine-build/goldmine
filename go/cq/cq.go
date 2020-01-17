// Package cq provides tools for interacting with the CQ tools.
package cq

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/golang/protobuf/proto"
	"github.com/golang/protobuf/ptypes"
	buildbucketpb "go.chromium.org/luci/buildbucket/proto"
	"go.chromium.org/luci/cq/api/config/v2"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/gitiles"
	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
)

const (
	CQ_CFG_FILE = "commit-queue.cfg"
	CQ_CFG_REF  = "infra/config"

	MASTER_REF = "refs/heads/master"

	// Constants for in-flight metrics.
	INFLIGHT_METRIC_NAME     = "in_flight"
	INFLIGHT_TRYBOT_DURATION = "trybot_duration"
	INFLIGHT_TRYBOT_NUM      = "trybot_num"
	INFLIGHT_WAITING_IN_CQ   = "waiting_in_cq"

	// Constants for landed metrics.
	LANDED_METRIC_NAME     = "after_commit"
	LANDED_TRYBOT_DURATION = "trybot_duration"
	LANDED_TOTAL_DURATION  = "total_duration"

	// Thresholds after which errors are logged.
	CQ_TRYBOT_DURATION_SECS_THRESHOLD = 2700
	CQ_TRYBOTS_COUNT_THRESHOLD        = 50
)

var (
	// Slice of all known presubmit bot names.
	PRESUBMIT_BOTS = []string{"skia_presubmit-Trybot"}

	// Mutext to control access to the slice of CQ trybots.
	cqTryBotsMutex sync.RWMutex
)

// NewClient creates a new client for interacting with CQ tools.
func NewClient(gerritClient *gerrit.Gerrit, cqTryBotsFunc GetCQTryBotsFn, metricName string) (*Client, error) {
	cqTryBots, err := cqTryBotsFunc()
	if err != nil {
		return nil, err
	}
	return &Client{gerritClient, util.NewStringSet(cqTryBots), cqTryBotsFunc, metricName}, err
}

// GetCQTryBotsFn is an interface for returing the CQ trybots of a project.
type GetCQTryBotsFn func() ([]string, error)

type Client struct {
	gerritClient  *gerrit.Gerrit
	cqTryBots     util.StringSet
	cqTryBotsFunc GetCQTryBotsFn
	metricName    string
}

// GetSkiaCQTryBots is a Skia implementation of GetCQTryBotsFn.
func GetSkiaCQTryBots() ([]string, error) {
	cfg, err := GetCQConfig(gitiles.NewRepo(common.REPO_SKIA, nil))
	if err != nil {
		return nil, err
	}
	return GetCQTryBots(cfg, MASTER_REF)
}

// GetSkiaInfraCQTryBots is a Skia Infra implementation of GetCQTryBotsFn.
func GetSkiaInfraCQTryBots() ([]string, error) {
	cfg, err := GetCQConfig(gitiles.NewRepo(common.REPO_SKIA_INFRA, nil))
	if err != nil {
		return nil, err
	}
	return GetCQTryBots(cfg, MASTER_REF)
}

// MatchConfigGroup returns the ConfigGroup, ConfigGroup_Gerrit, and
// ConfigGroup_Gerrit_Project which match the given full ref name, or nil if
// there is no matching ConfigGroup.
func MatchConfigGroup(cqCfg *config.Config, ref string) (*config.ConfigGroup, *config.ConfigGroup_Gerrit, *config.ConfigGroup_Gerrit_Project, error) {
	for _, configGroup := range cqCfg.GetConfigGroups() {
		for _, g := range configGroup.GetGerrit() {
			for _, p := range g.GetProjects() {
				for _, r := range p.GetRefRegexp() {
					m, err := regexp.MatchString(r, ref)
					if err != nil {
						return nil, nil, nil, fmt.Errorf("Error when compiling %s: %s", r, err)
					}
					if m {
						// Found the ref we were looking for.
						return configGroup, g, p, nil
					}
				}
			}
		}
	}
	return nil, nil, nil, nil
}

// GetCQConfig returns the Config for the given repo.
func GetCQConfig(repo *gitiles.Repo) (*config.Config, error) {
	var buf bytes.Buffer
	if err := repo.ReadFileAtRef(context.Background(), CQ_CFG_FILE, CQ_CFG_REF, &buf); err != nil {
		return nil, err
	}
	var cqCfg config.Config
	if err := proto.UnmarshalText(buf.String(), &cqCfg); err != nil {
		return nil, err
	}
	return &cqCfg, nil
}

// GetCQTryBots is a convenience method for retrieving the list of CQ trybots
// from a Config.
func GetCQTryBots(cqCfg *config.Config, ref string) ([]string, error) {
	tryJobs := []string{}
	configGroup, _, _, err := MatchConfigGroup(cqCfg, ref)
	if err != nil {
		return nil, err
	}
	if configGroup != nil {
		// Found the ref we were looking for.
		for _, builder := range configGroup.GetVerifiers().GetTryjob().GetBuilders() {
			if builder.GetExperimentPercentage() > 0 && builder.GetExperimentPercentage() < 100 {
				// Exclude experimental builders, unless running for all CLs.
				continue
			}
			if builder.IncludableOnly {
				// Exclude builders which have been specified only for "Cq-Include-Trybots".
				continue
			}
			if util.ContainsAny(builder.GetName(), PRESUBMIT_BOTS) {
				// Exclude presubmit bots because they could fail or be delayed
				// due to factors such as owners approval and other project
				// specific checks.
				continue
			}
			// Strip out the bucket and use only the builder name.
			// Eg: chromium/try/mac_chromium_compile_dbg_ng -> mac_chromium_compile_dbg_ng
			builderName := filepath.Base(builder.GetName())
			tryJobs = append(tryJobs, builderName)
		}
	}

	sklog.Infof("The list of CQ trybots is: %s", tryJobs)
	return tryJobs, nil
}

// RefreshCQTryBots refreshes the slice of CQ trybots on the instance. Access
// to the trybots is protected by a RWMutex.
// Trybots that no longer exist in the new list will have their metrics purged.
func (c *Client) RefreshCQTryBots() error {
	tryBots, err := c.cqTryBotsFunc()
	if err != nil {
		return err
	}
	tryBotsSet := util.NewStringSet(tryBots)

	cqTryBotsMutex.Lock()
	defer cqTryBotsMutex.Unlock()
	// Gather all trybots that no longer exist and purge their metrics.
	deletedTryBots := c.cqTryBots.Complement(tryBotsSet)
	if err := c.purgeMetrics(deletedTryBots); err != nil {
		return fmt.Errorf("Could not purge metrics of %s: %s", deletedTryBots, err)
	}

	c.cqTryBots = tryBotsSet
	return nil
}

// ReportCQStats reports all relevant stats for the specified Gerrit change.
// Note: Different stats are reported depending on whether the change has been
// merged or not.
func (c *Client) ReportCQStats(ctx context.Context, change int64) error {
	changeInfo, err := c.gerritClient.GetIssueProperties(ctx, change)
	if err != nil {
		return err
	}
	patchsetIds := changeInfo.GetPatchsetIDs()
	latestPatchsetId := patchsetIds[len(patchsetIds)-1]
	if changeInfo.Committed {
		// TODO(rmistry): The last patchset in Gerrit does not contain trybot
		// information so we have to look at the one immediately before it.
		// This will be fixed with crbug.com/634944.
		latestPatchsetId = patchsetIds[len(patchsetIds)-2]
	}

	builds, err := c.gerritClient.GetTrybotResults(ctx, change, latestPatchsetId)
	if err != nil {
		return err
	}
	// Consider only CQ bots.
	cqBuilds := []*buildbucketpb.Build{}
	for _, b := range builds {
		if c.isCQTryBot(b.Builder.Builder) {
			cqBuilds = append(cqBuilds, b)
		}
	}
	gerritURL := fmt.Sprintf("%s/c/%d/%d", gerrit.GERRIT_SKIA_URL, change, latestPatchsetId)
	if len(cqBuilds) == 0 {
		sklog.Infof("No trybot results were found for %s", gerritURL)
		return nil
	}

	sklog.Infof("Starting processing %s. Merged status: %t", gerritURL, changeInfo.Committed)

	if changeInfo.Committed {
		c.ReportCQStatsForLandedCL(cqBuilds, gerritURL)
	} else {
		c.ReportCQStatsForInFlightCL(cqBuilds, gerritURL)
	}
	return nil
}

// ReportCQStatsForLandedCL reports the following metrics for the specified
// change and patchsetID:
// * The total time the change spent waiting for CQ trybots to complete.
// * The time each CQ trybot took to complete.
func (c *Client) ReportCQStatsForLandedCL(cqBuilds []*buildbucketpb.Build, gerritURL string) {
	endTimeOfCQBots := time.Time{}
	maximumTrybotDuration := int64(0)
	for _, b := range cqBuilds {
		createdTime, err := ptypes.Timestamp(b.CreateTime)
		if err != nil {
			sklog.Errorf("Failed to convert timestamp for %d; skipping: %s", b.Id, err)
			continue
		}
		createdTime = createdTime.UTC()
		if b.EndTime == nil {
			sklog.Warningf("Skipping %s on %s. The correct completed time has not shown up in Buildbucket yet.", b.Builder.Builder, gerritURL)
			continue
		}
		completedTime, err := ptypes.Timestamp(b.EndTime)
		if err != nil {
			sklog.Errorf("Failed to convert timestamp for %d; skipping: %s", b.Id, err)
			continue
		}
		completedTime = completedTime.UTC()
		if endTimeOfCQBots.Before(completedTime) {
			endTimeOfCQBots = completedTime
		}

		duration := int64(completedTime.Sub(createdTime).Seconds())
		sklog.Infof("%s was created at %s by %s and completed at %s. Total duration: %d", b.Builder.Builder, createdTime, gerritURL, completedTime, duration)
		landedTrybotDurationMetric := c.getLandedTrybotDurationMetric(b.Builder.Builder)
		landedTrybotDurationMetric.Update(duration)

		if duration > maximumTrybotDuration {
			maximumTrybotDuration = duration
		}
	}

	sklog.Infof("Maximum trybot duration for %s: %d", gerritURL, maximumTrybotDuration)
	sklog.Infof("Furthest completion time for %s: %s", gerritURL, endTimeOfCQBots)
	landedTotalDurationMetric := metrics2.GetInt64Metric(fmt.Sprintf("%s_%s_%s", c.metricName, LANDED_METRIC_NAME, LANDED_TOTAL_DURATION), map[string]string{})
	landedTotalDurationMetric.Update(maximumTrybotDuration)
}

// ReportCQStatsForInFlightCL reports the following metrics for the specified
// change and patchsetID:
// * How long CQ trybots have been running for.
// * How many CQ trybots have been triggered.
func (c *Client) ReportCQStatsForInFlightCL(cqBuilds []*buildbucketpb.Build, gerritURL string) {
	totalTriggeredCQBots := int(0)
	currentTime := time.Now()
	for _, b := range cqBuilds {
		totalTriggeredCQBots++

		createdTime, err := ptypes.Timestamp(b.CreateTime)
		if err != nil {
			sklog.Errorf("Failed to convert timestamp for %d; skipping: %s", b.Id, err)
			continue
		}
		createdTime = createdTime.UTC()
		if b.EndTime != nil {
			completedTime, err := ptypes.Timestamp(b.EndTime)
			if err != nil {
				sklog.Errorf("Failed to convert timestamp for %d; skipping: %s", b.Id, err)
				continue
			}
			completedTime = completedTime.UTC()
			if time.Hour*24 < time.Now().UTC().Sub(completedTime) {
				// The build has completed more than a day ago. Do not include it
				// in totalTriggeredCQBots. See skbug.com/7340.
				totalTriggeredCQBots--
			}
			// The build has completed so move on.
			continue
		}

		duration := int64(currentTime.Sub(createdTime).Seconds())
		if duration > CQ_TRYBOT_DURATION_SECS_THRESHOLD {
			sklog.Errorf("CQTrybotDurationError: %s was triggered by %s and is still running after %d seconds. Threshold is %d seconds.", b.Builder.Builder, gerritURL, duration, CQ_TRYBOT_DURATION_SECS_THRESHOLD)
		}
		inflightTrybotDurationMetric := c.getInflightTrybotDurationMetric(b.Builder.Builder)
		inflightTrybotDurationMetric.Update(duration)
	}

	cqTryBotsMutex.RLock()
	cqTryBotsMutex.RUnlock()
	if totalTriggeredCQBots > CQ_TRYBOTS_COUNT_THRESHOLD {
		sklog.Errorf("CQCLsCountError: %d trybots have been triggered by %s. Threshold is %d trybots.", totalTriggeredCQBots, gerritURL, CQ_TRYBOTS_COUNT_THRESHOLD)
	}
	trybotNumDurationMetric := metrics2.GetInt64Metric(fmt.Sprintf("%s_%s_%s", c.metricName, INFLIGHT_METRIC_NAME, INFLIGHT_TRYBOT_NUM), map[string]string{})
	trybotNumDurationMetric.Update(int64(totalTriggeredCQBots))
}

func (c *Client) purgeMetrics(tryBots util.StringSet) error {
	for _, b := range tryBots.Keys() {
		inflightTrybotDurationMetric := c.getInflightTrybotDurationMetric(b)
		if err := inflightTrybotDurationMetric.Delete(); err != nil {
			return fmt.Errorf("Could not delete inflight trybot metric: %s", err)
		}
		landedTrybotDurationMetric := c.getLandedTrybotDurationMetric(b)
		if err := landedTrybotDurationMetric.Delete(); err != nil {
			return fmt.Errorf("Could not delete landed trybot metric: %s", err)
		}
		sklog.Infof("Deleted inflight and landed metrics of %s", b)
	}
	return nil
}

func (c *Client) getInflightTrybotDurationMetric(tryBot string) metrics2.Int64Metric {
	metricName := fmt.Sprintf("%s_%s_%s", c.metricName, INFLIGHT_METRIC_NAME, INFLIGHT_TRYBOT_DURATION)
	tags := map[string]string{
		"trybot": tryBot,
	}
	return metrics2.GetInt64Metric(metricName, tags)
}

func (c *Client) getLandedTrybotDurationMetric(tryBot string) metrics2.Int64Metric {
	metricName := fmt.Sprintf("%s_%s_%s", c.metricName, LANDED_METRIC_NAME, LANDED_TRYBOT_DURATION)
	tags := map[string]string{
		"trybot": tryBot,
	}
	return metrics2.GetInt64Metric(metricName, tags)
}

func (c *Client) isCQTryBot(builderName string) bool {
	cqTryBotsMutex.RLock()
	isCQTrybot := c.cqTryBots[builderName]
	cqTryBotsMutex.RUnlock()
	return isCQTrybot
}

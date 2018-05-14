// run_chromium_perf_on_workers is an application that runs the specified telemetry
// benchmark on all CT workers and uploads the results to Google Storage. The
// requester is emailed when the task is done.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.skia.org/infra/ct/go/ctfe/chromium_perf"
	"go.skia.org/infra/ct/go/frontend"
	"go.skia.org/infra/ct/go/master_scripts/master_common"
	"go.skia.org/infra/ct/go/util"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/email"
	"go.skia.org/infra/go/sklog"
	skutil "go.skia.org/infra/go/util"
)

const (
	MAX_PAGES_PER_SWARMING_BOT = 100
)

var (
	emails                    = flag.String("emails", "", "The comma separated email addresses to notify when the task is picked up and completes.")
	description               = flag.String("description", "", "The description of the run as entered by the requester.")
	taskID                    = flag.Int64("task_id", -1, "The key of the CT task in CTFE. The task will be updated when it is started and also when it completes.")
	pagesetType               = flag.String("pageset_type", "", "The type of pagesets to use. Eg: 10k, Mobile10k, All.")
	benchmarkName             = flag.String("benchmark_name", "", "The telemetry benchmark to run on the workers.")
	benchmarkExtraArgs        = flag.String("benchmark_extra_args", "", "The extra arguments that are passed to the specified benchmark.")
	browserExtraArgsNoPatch   = flag.String("browser_extra_args_nopatch", "", "The extra arguments that are passed to the browser while running the benchmark for the nopatch case.")
	browserExtraArgsWithPatch = flag.String("browser_extra_args_withpatch", "", "The extra arguments that are passed to the browser while running the benchmark for the withpatch case.")
	repeatBenchmark           = flag.Int("repeat_benchmark", 1, "The number of times the benchmark should be repeated. For skpicture_printer benchmark this value is always 1.")
	runInParallel             = flag.Bool("run_in_parallel", false, "Run the benchmark by bringing up multiple chrome instances in parallel.")
	targetPlatform            = flag.String("target_platform", util.PLATFORM_ANDROID, "The platform the benchmark will run on (Android / Linux).")
	runOnGCE                  = flag.Bool("run_on_gce", true, "Run on Linux GCE instances.")
	runID                     = flag.String("run_id", "", "The unique run id (typically requester + timestamp).")
	varianceThreshold         = flag.Float64("variance_threshold", 0.0, "The variance threshold to use when comparing the resultant CSV files.")
	discardOutliers           = flag.Float64("discard_outliers", 0.0, "The percentage of outliers to discard when comparing the result CSV files.")

	taskCompletedSuccessfully = false

	htmlOutputLink      = util.MASTER_LOGSERVER_LINK
	skiaPatchLink       = util.MASTER_LOGSERVER_LINK
	chromiumPatchLink   = util.MASTER_LOGSERVER_LINK
	v8PatchLink         = util.MASTER_LOGSERVER_LINK
	catapultPatchLink   = util.MASTER_LOGSERVER_LINK
	benchmarkPatchLink  = util.MASTER_LOGSERVER_LINK
	customWebpagesLink  = util.MASTER_LOGSERVER_LINK
	noPatchOutputLink   = util.MASTER_LOGSERVER_LINK
	withPatchOutputLink = util.MASTER_LOGSERVER_LINK
)

func sendEmail(recipients []string) {
	// Send completion email.
	emailSubject := fmt.Sprintf("Cluster telemetry chromium perf task has completed (#%d)", *taskID)
	failureHtml := ""
	viewActionMarkup := ""
	var err error

	if taskCompletedSuccessfully {
		if viewActionMarkup, err = email.GetViewActionMarkup(htmlOutputLink, "View Results", "Direct link to the HTML results"); err != nil {
			sklog.Errorf("Failed to get view action markup: %s", err)
			return
		}
	} else {
		emailSubject += " with failures"
		failureHtml = util.GetFailureEmailHtml(*runID)
		if viewActionMarkup, err = email.GetViewActionMarkup(fmt.Sprintf(util.SWARMING_RUN_ID_ALL_TASKS_LINK_TEMPLATE, *runID), "View Failure", "Direct link to the swarming logs"); err != nil {
			sklog.Errorf("Failed to get view action markup: %s", err)
			return
		}
	}
	bodyTemplate := `
	The chromium perf %s benchmark task on %s pageset has completed. %s.<br/>
	Run description: %s<br/>
	%s
	The HTML output with differences between the base run and the patch run is <a href='%s'>here</a>.<br/>
	The patch(es) you specified are here:
	<a href='%s'>chromium</a>/<a href='%s'>skia</a>/<a href='%s'>v8</a>/<a href='%s'>catapult</a>
	<br/>
	Custom webpages (if specified) are <a href='%s'>here</a>.
	<br/><br/>
	You can schedule more runs <a href='%s'>here</a>.
	<br/><br/>
	Thanks!
	`
	emailBody := fmt.Sprintf(bodyTemplate, *benchmarkName, *pagesetType, util.GetSwarmingLogsLink(*runID), *description, failureHtml, htmlOutputLink, chromiumPatchLink, skiaPatchLink, v8PatchLink, catapultPatchLink, customWebpagesLink, frontend.ChromiumPerfTasksWebapp)
	if err := util.SendEmailWithMarkup(recipients, emailSubject, emailBody, viewActionMarkup); err != nil {
		sklog.Errorf("Error while sending email: %s", err)
		return
	}
}

func updateWebappTask() {
	vars := chromium_perf.UpdateVars{}
	vars.Id = *taskID
	vars.SetCompleted(taskCompletedSuccessfully)
	vars.Results = sql.NullString{String: htmlOutputLink, Valid: true}
	vars.NoPatchRawOutput = sql.NullString{String: noPatchOutputLink, Valid: true}
	vars.WithPatchRawOutput = sql.NullString{String: withPatchOutputLink, Valid: true}
	skutil.LogErr(frontend.UpdateWebappTaskV2(&vars))
}

func main() {
	defer common.LogPanic()
	master_common.Init("run_chromium_perf")

	ctx := context.Background()

	// Send start email.
	emailsArr := util.ParseEmails(*emails)
	emailsArr = append(emailsArr, util.CtAdmins...)
	if len(emailsArr) == 0 {
		sklog.Error("At least one email address must be specified")
		return
	}
	skutil.LogErr(frontend.UpdateWebappTaskSetStarted(&chromium_perf.UpdateVars{}, *taskID, *runID))
	skutil.LogErr(util.SendTaskStartEmail(*taskID, emailsArr, "Chromium perf", *runID, *description))
	// Ensure webapp is updated and email is sent even if task fails.
	defer updateWebappTask()
	defer sendEmail(emailsArr)
	// Cleanup dirs after run completes.
	defer skutil.RemoveAll(filepath.Join(util.StorageDir, util.ChromiumPerfRunsDir, *runID))
	// Finish with glog flush and how long the task took.
	defer util.TimeTrack(time.Now(), "Running chromium perf task on workers")
	defer sklog.Flush()

	if *pagesetType == "" {
		sklog.Error("Must specify --pageset_type")
		return
	}
	if *benchmarkName == "" {
		sklog.Error("Must specify --benchmark_name")
		return
	}
	if *runID == "" {
		sklog.Error("Must specify --run_id")
		return
	}
	if *description == "" {
		sklog.Error("Must specify --description")
		return
	}

	// Instantiate GcsUtil object.
	gs, err := util.NewGcsUtil(nil)
	if err != nil {
		sklog.Errorf("Could not instantiate gsutil object: %s", err)
		return
	}
	remoteOutputDir := filepath.Join(util.ChromiumPerfRunsDir, *runID)

	// Copy the patches and custom webpages to Google Storage.
	skiaPatchName := *runID + ".skia.patch"
	chromiumPatchName := *runID + ".chromium.patch"
	v8PatchName := *runID + ".v8.patch"
	catapultPatchName := *runID + ".catapult.patch"
	benchmarkPatchName := *runID + ".benchmark.patch"
	customWebpagesName := *runID + ".custom_webpages.csv"
	for _, patchName := range []string{skiaPatchName, chromiumPatchName, v8PatchName, catapultPatchName, benchmarkPatchName, customWebpagesName} {
		if err := gs.UploadFile(patchName, os.TempDir(), remoteOutputDir); err != nil {
			sklog.Errorf("Could not upload %s to %s: %s", patchName, remoteOutputDir, err)
			return
		}
	}
	skiaPatchLink = util.GCS_HTTP_LINK + filepath.Join(util.GCSBucketName, remoteOutputDir, skiaPatchName)
	chromiumPatchLink = util.GCS_HTTP_LINK + filepath.Join(util.GCSBucketName, remoteOutputDir, chromiumPatchName)
	v8PatchLink = util.GCS_HTTP_LINK + filepath.Join(util.GCSBucketName, remoteOutputDir, v8PatchName)
	catapultPatchLink = util.GCS_HTTP_LINK + filepath.Join(util.GCSBucketName, remoteOutputDir, catapultPatchName)
	benchmarkPatchLink = util.GCS_HTTP_LINK + filepath.Join(util.GCSBucketName, remoteOutputDir, benchmarkPatchName)
	customWebpagesLink = util.GCS_HTTP_LINK + filepath.Join(util.GCSBucketName, remoteOutputDir, customWebpagesName)

	// Check if the patches have any content to decide if we need one or two chromium builds.
	localPatches := []string{filepath.Join(os.TempDir(), chromiumPatchName), filepath.Join(os.TempDir(), skiaPatchName), filepath.Join(os.TempDir(), v8PatchName)}
	remotePatches := []string{filepath.Join(remoteOutputDir, chromiumPatchName), filepath.Join(remoteOutputDir, skiaPatchName), filepath.Join(remoteOutputDir, v8PatchName)}
	var chromiumBuildNoPatch, chromiumBuildWithPatch string
	if util.PatchesAreEmpty(localPatches) {
		// Create only one chromium build.
		chromiumBuilds, err := util.TriggerBuildRepoSwarmingTask(
			ctx, "build_chromium", *runID, "chromium", *targetPlatform, []string{}, remotePatches,
			/*singlebuild*/ true, 3*time.Hour, 1*time.Hour)
		if err != nil {
			sklog.Errorf("Error encountered when swarming build repo task: %s", err)
			return
		}
		if len(chromiumBuilds) != 1 {
			sklog.Errorf("Expected 1 build but instead got %d: %v.", len(chromiumBuilds), chromiumBuilds)
			return
		}
		chromiumBuildNoPatch = chromiumBuilds[0]
		chromiumBuildWithPatch = chromiumBuilds[0]

	} else {
		// Create the two required chromium builds (with patch and without the patch).
		chromiumBuilds, err := util.TriggerBuildRepoSwarmingTask(
			ctx, "build_chromium", *runID, "chromium", *targetPlatform, []string{}, remotePatches,
			/*singlebuild*/ false, 3*time.Hour, 1*time.Hour)
		if err != nil {
			sklog.Errorf("Error encountered when swarming build repo task: %s", err)
			return
		}
		if len(chromiumBuilds) != 2 {
			sklog.Errorf("Expected 2 builds but instead got %d: %v.", len(chromiumBuilds), chromiumBuilds)
			return
		}
		chromiumBuildNoPatch = chromiumBuilds[0]
		chromiumBuildWithPatch = chromiumBuilds[1]
	}

	// Parse out the Chromium and Skia hashes.
	chromiumHash, skiaHash := util.GetHashesFromBuild(chromiumBuildNoPatch)

	// Archive, trigger and collect swarming tasks.
	isolateExtraArgs := map[string]string{
		"CHROMIUM_BUILD_NOPATCH":       chromiumBuildNoPatch,
		"CHROMIUM_BUILD_WITHPATCH":     chromiumBuildWithPatch,
		"RUN_ID":                       *runID,
		"BENCHMARK":                    *benchmarkName,
		"BENCHMARK_ARGS":               *benchmarkExtraArgs,
		"BROWSER_EXTRA_ARGS_NOPATCH":   *browserExtraArgsNoPatch,
		"BROWSER_EXTRA_ARGS_WITHPATCH": *browserExtraArgsWithPatch,
		"REPEAT_BENCHMARK":             strconv.Itoa(*repeatBenchmark),
		"RUN_IN_PARALLEL":              strconv.FormatBool(*runInParallel),
		"TARGET_PLATFORM":              *targetPlatform,
	}
	customWebPagesFilePath := filepath.Join(os.TempDir(), customWebpagesName)
	numPages, err := util.GetNumPages(*pagesetType, customWebPagesFilePath)
	if err != nil {
		sklog.Errorf("Error encountered when calculating number of pages: %s", err)
		return
	}
	// Determine hard timeout according to the number of times benchmark should be repeated.
	// Cap it off at the max allowable hours.
	var hardTimeout = time.Duration(skutil.MinInt(12**repeatBenchmark, util.MAX_SWARMING_HARD_TIMEOUT_HOURS)) * time.Hour
	// Calculate the max pages to run per bot.
	maxPagesPerBot := util.GetMaxPagesPerBotValue(*benchmarkExtraArgs, MAX_PAGES_PER_SWARMING_BOT)
	numSlaves, err := util.TriggerSwarmingTask(ctx, *pagesetType, "chromium_perf", util.CHROMIUM_PERF_ISOLATE, *runID, hardTimeout, 1*time.Hour, util.USER_TASKS_PRIORITY, maxPagesPerBot, numPages, isolateExtraArgs, *runOnGCE, util.GetRepeatValue(*benchmarkExtraArgs, *repeatBenchmark), []string{} /* isolateDeps */)
	if err != nil {
		sklog.Errorf("Error encountered when swarming tasks: %s", err)
		return
	}

	// If "--output-format=csv" is specified then merge all CSV files and upload.
	runIDNoPatch := fmt.Sprintf("%s-nopatch", *runID)
	runIDWithPatch := fmt.Sprintf("%s-withpatch", *runID)
	noOutputSlaves := []string{}
	pathToPyFiles := util.GetPathToPyFiles(false)
	for _, run := range []string{runIDNoPatch, runIDWithPatch} {
		if strings.Contains(*benchmarkExtraArgs, "--output-format=csv") {
			if noOutputSlaves, err = util.MergeUploadCSVFiles(ctx, run, pathToPyFiles, gs, numPages, maxPagesPerBot, true /* handleStrings */, util.GetRepeatValue(*benchmarkExtraArgs, *repeatBenchmark)); err != nil {
				sklog.Errorf("Unable to merge and upload CSV files for %s: %s", run, err)
			}
			// Cleanup created dir after the run completes.
			defer skutil.RemoveAll(filepath.Join(util.StorageDir, util.BenchmarkRunsDir, run))
		}
	}
	// If the number of noOutputSlaves is the same as the total number of triggered slaves then consider the run failed.
	if len(noOutputSlaves) == numSlaves {
		sklog.Errorf("All %d slaves produced no output", numSlaves)
		return
	}

	totalArchivedWebpages, err := util.GetArchivesNum(gs, *benchmarkExtraArgs, *pagesetType)
	if err != nil {
		sklog.Errorf("Error when calculating number of archives: %s", err)
		return
	}

	// Compare the resultant CSV files using csv_comparer.py
	noPatchCSVPath := filepath.Join(util.StorageDir, util.BenchmarkRunsDir, runIDNoPatch, runIDNoPatch+".output")
	withPatchCSVPath := filepath.Join(util.StorageDir, util.BenchmarkRunsDir, runIDWithPatch, runIDWithPatch+".output")
	htmlOutputDir := filepath.Join(util.StorageDir, util.ChromiumPerfRunsDir, *runID, "html")
	skutil.MkdirAll(htmlOutputDir, 0700)
	htmlRemoteDir := filepath.Join(remoteOutputDir, "html")
	htmlOutputLinkBase := util.GCS_HTTP_LINK + filepath.Join(util.GCSBucketName, htmlRemoteDir) + "/"
	htmlOutputLink = htmlOutputLinkBase + "index.html"
	noPatchOutputLink = util.GCS_HTTP_LINK + filepath.Join(util.GCSBucketName, util.BenchmarkRunsDir, runIDNoPatch, "consolidated_outputs", runIDNoPatch+".output")
	withPatchOutputLink = util.GCS_HTTP_LINK + filepath.Join(util.GCSBucketName, util.BenchmarkRunsDir, runIDWithPatch, "consolidated_outputs", runIDWithPatch+".output")
	// Construct path to the csv_comparer python script.
	pathToCsvComparer := filepath.Join(pathToPyFiles, "csv_comparer.py")
	args := []string{
		pathToCsvComparer,
		"--csv_file1=" + noPatchCSVPath,
		"--csv_file2=" + withPatchCSVPath,
		"--output_html=" + htmlOutputDir,
		"--variance_threshold=" + strconv.FormatFloat(*varianceThreshold, 'f', 2, 64),
		"--discard_outliers=" + strconv.FormatFloat(*discardOutliers, 'f', 2, 64),
		"--absolute_url=" + htmlOutputLinkBase,
		"--requester_email=" + *emails,
		"--skia_patch_link=" + skiaPatchLink,
		"--chromium_patch_link=" + chromiumPatchLink,
		"--benchmark_patch_link=" + benchmarkPatchLink,
		"--description=" + *description,
		"--raw_csv_nopatch=" + noPatchOutputLink,
		"--raw_csv_withpatch=" + withPatchOutputLink,
		"--num_repeated=" + strconv.Itoa(*repeatBenchmark),
		"--target_platform=" + *targetPlatform,
		"--browser_args_nopatch=" + *browserExtraArgsNoPatch,
		"--browser_args_withpatch=" + *browserExtraArgsWithPatch,
		"--pageset_type=" + *pagesetType,
		"--chromium_hash=" + chromiumHash,
		"--skia_hash=" + skiaHash,
		"--missing_output_slaves=" + strings.Join(noOutputSlaves, " "),
		"--logs_link_prefix=" + fmt.Sprintf(util.SWARMING_RUN_ID_TASK_LINK_PREFIX_TEMPLATE, *runID, "chromium_perf_"),
		"--total_archives=" + strconv.Itoa(totalArchivedWebpages),
	}
	// TODO(rmistry): Remove the below debugging stmt.
	sklog.Errorf("Args of csv_comparer.py: %v", args)
	err = util.ExecuteCmd(ctx, "python", args, []string{}, util.CSV_COMPARER_TIMEOUT, nil, nil)
	if err != nil {
		sklog.Errorf("Error running csv_comparer.py: %s", err)
		return
	}

	// Copy the HTML files to Google Storage.
	if err := gs.UploadDir(htmlOutputDir, htmlRemoteDir, true); err != nil {
		sklog.Errorf("Could not upload %s to %s: %s", htmlOutputDir, htmlRemoteDir, err)
		return
	}

	taskCompletedSuccessfully = true
}

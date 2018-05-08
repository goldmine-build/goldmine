// capture_skps_on_workers is an application that captures SKPs of the
// specified patchset type on all CT workers and uploads the results to Google
// Storage. The requester is emailed when the task is done.
package main

import (
	"context"
	"flag"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"go.skia.org/infra/ct/go/ctfe/capture_skps"
	"go.skia.org/infra/ct/go/frontend"
	"go.skia.org/infra/ct/go/master_scripts/master_common"
	"go.skia.org/infra/ct/go/util"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/sklog"
	skutil "go.skia.org/infra/go/util"
)

const (
	MAX_PAGES_PER_SWARMING_BOT_CAPTURE_SKPS = 100
	// TODO(rmistry): Change back to 10000 once swarming can handle >10k pending tasks.
	MAX_PAGES_PER_SWARMING_BOT_CAPTURE_SKPS_FROM_PDFS = 50000
)

var (
	emails         = flag.String("emails", "", "The comma separated email addresses to notify when the task is picked up and completes.")
	description    = flag.String("description", "", "The description of the run as entered by the requester.")
	taskID         = flag.Int64("task_id", -1, "The key of the CT task in CTFE. The task will be updated when it is started and also when it completes.")
	pagesetType    = flag.String("pageset_type", "", "The type of pagesets to use. Eg: 10k, Mobile10k, All.")
	chromiumBuild  = flag.String("chromium_build", "", "The chromium build to use for this capture SKPs run.")
	targetPlatform = flag.String("target_platform", util.PLATFORM_LINUX, "The platform the benchmark will run on (Android / Linux).")
	runOnGCE       = flag.Bool("run_on_gce", true, "Run on Linux GCE instances.")
	runID          = flag.String("run_id", "", "The unique run id (typically requester + timestamp).")

	taskCompletedSuccessfully = false
)

func sendEmail(recipients []string) {
	// Send completion email.
	emailSubject := fmt.Sprintf("Capture SKPs cluster telemetry task has completed (%s)", *runID)
	failureHtml := ""
	if !taskCompletedSuccessfully {
		emailSubject += " with failures"
		failureHtml = util.GetFailureEmailHtml(*runID)
	}
	bodyTemplate := `
	The Capture SKPs task on %s pageset has completed. %s.<br/>
	Run description: %s<br/>
	%s
	You can schedule more runs <a href="%s">here</a>.<br/><br/>
	Thanks!
	`
	emailBody := fmt.Sprintf(bodyTemplate, *pagesetType, util.GetSwarmingLogsLink(*runID), *description, failureHtml, frontend.CaptureSKPsTasksWebapp)
	if err := util.SendEmail(recipients, emailSubject, emailBody); err != nil {
		sklog.Errorf("Error while sending email: %s", err)
		return
	}
}

func updateWebappTask() {
	vars := capture_skps.UpdateVars{}
	vars.Id = *taskID
	vars.SetCompleted(taskCompletedSuccessfully)
	skutil.LogErr(frontend.UpdateWebappTaskV2(&vars))
}

func main() {
	defer common.LogPanic()
	master_common.Init("capture_skps")

	ctx := context.Background()

	// Send start email.
	emailsArr := util.ParseEmails(*emails)
	emailsArr = append(emailsArr, util.CtAdmins...)
	if len(emailsArr) == 0 {
		sklog.Error("At least one email address must be specified")
		return
	}
	skutil.LogErr(frontend.UpdateWebappTaskSetStarted(&capture_skps.UpdateVars{}, *taskID, *runID))
	skutil.LogErr(util.SendTaskStartEmail(*taskID, emailsArr, "Capture SKPs", *runID, *description))
	// Ensure webapp is updated and completion email is sent even if task
	// fails.
	defer updateWebappTask()
	defer sendEmail(emailsArr)

	// Finish with glog flush and how long the task took.
	defer util.TimeTrack(time.Now(), "Running capture skps task on workers")
	defer sklog.Flush()

	if *pagesetType == "" {
		sklog.Error("Must specify --pageset_type")
		return
	}
	if *chromiumBuild == "" {
		sklog.Error("Must specify --chromium_build")
		return
	}
	if *runID == "" {
		sklog.Error("Must specify --run_id")
		return
	}

	isolateFile := util.CAPTURE_SKPS_ISOLATE
	maxPages := MAX_PAGES_PER_SWARMING_BOT_CAPTURE_SKPS
	hardTimeout := 3 * time.Hour
	ioTimeout := 1 * time.Hour
	if strings.Contains(strings.ToUpper(*pagesetType), "PDF") {
		// For PDF pagesets use the capture_skps_from_pdfs worker script.
		isolateFile = util.CAPTURE_SKPS_FROM_PDFS_ISOLATE
		maxPages = MAX_PAGES_PER_SWARMING_BOT_CAPTURE_SKPS_FROM_PDFS
		hardTimeout = 12 * time.Hour
		ioTimeout = hardTimeout // PDFs do not output any logs thus the ioTimeout must be the same as the hardTimeout.

		// TODO(rmistry): Uncomment when ready to capture SKPs.
		// TODO(rmistry): Replace the below block with:
		// buildRemoteDir, err := util.TriggerBuildRepoSwarmingTask("build_pdfium", *runID, "pdfium", "Linux", []string{}, []string{filepath.Join(remoteOutputDir, chromiumPatchName)}, /*singleBuild*/ true, 2*time.Hour, 1*time.Hour)
		// if err != nil {
		//	sklog.Errorf("Error encountered when swarming build repo task: %s", err)
		//	return
		// }
		//
		//// Sync PDFium and build pdfium_test binary which will be used by the worker script.
		//if err := util.SyncDir(util.PDFiumTreeDir); err != nil {
		//	sklog.Errorf("Could not sync PDFium: %s", err)
		//	return
		//}
		//if err := util.BuildPDFium(); err != nil {
		//	sklog.Errorf("Could not build PDFium: %s", err)
		//	return
		//}
		//// Copy pdfium_test to Google Storage.
		//pdfiumLocalDir := path.Join(util.PDFiumTreeDir, "out", "Debug")
		//pdfiumRemoteDir := path.Join(util.BINARIES_DIR_NAME, *chromiumBuild)
		//// Instantiate GcsUtil object.
		//gs, err := util.NewGcsUtil(nil)
		//if err != nil {
		//	sklog.Error(err)
		//	return
		//}
		//if err := gs.UploadFile(util.BINARY_PDFIUM_TEST, pdfiumLocalDir, pdfiumRemoteDir); err != nil {
		//	sklog.Errorf("Could not upload %s to %s: %s", util.BINARY_PDFIUM_TEST, pdfiumRemoteDir, err)
		//	return
		//}
	}

	// Empty the remote dir before the workers upload to it.
	gs, err := util.NewGcsUtil(nil)
	if err != nil {
		sklog.Error(err)
		return
	}
	skpGCSBaseDir := filepath.Join(util.SWARMING_DIR_NAME, util.SKPS_DIR_NAME, *pagesetType, *chromiumBuild)
	skutil.LogErr(gs.DeleteRemoteDir(skpGCSBaseDir))
	if strings.Contains(strings.ToUpper(*pagesetType), "PDF") {
		pdfGSBaseDir := filepath.Join(util.SWARMING_DIR_NAME, util.PDFS_DIR_NAME, *pagesetType, *chromiumBuild)
		skutil.LogErr(gs.DeleteRemoteDir(pdfGSBaseDir))
	}

	// Archive, trigger and collect swarming tasks.
	isolateExtraArgs := map[string]string{
		"CHROMIUM_BUILD": *chromiumBuild,
		"RUN_ID":         *runID,
	}
	if _, err := util.TriggerSwarmingTask(ctx, *pagesetType, "capture_skps", isolateFile, *runID, hardTimeout, ioTimeout, util.ADMIN_TASKS_PRIORITY, maxPages, util.PagesetTypeToInfo[*pagesetType].NumPages, isolateExtraArgs, *runOnGCE, 1); err != nil {
		sklog.Errorf("Error encountered when swarming tasks: %s", err)
		return
	}

	taskCompletedSuccessfully = true
}

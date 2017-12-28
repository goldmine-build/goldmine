// Utility that contains methods for both CT master and worker scripts.
package util

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/isolate"
	"go.skia.org/infra/go/swarming"
	"go.skia.org/infra/go/util"

	"go.skia.org/infra/go/sklog"
)

const (
	MAX_SYNC_TRIES = 3

	TS_FORMAT = "20060102150405"

	REMOVE_INVALID_SKPS_WORKER_POOL = 20

	MAX_SIMULTANEOUS_SWARMING_TASKS_PER_RUN = 10000
)

func TimeTrack(start time.Time, name string) {
	elapsed := time.Since(start)
	sklog.Infof("===== %s took %s =====", name, elapsed)
}

// ExecuteCmd calls ExecuteCmdWithConfigurableLogging with logStdout and logStderr set to true.
func ExecuteCmd(ctx context.Context, binary string, args, env []string, timeout time.Duration, stdout, stderr io.Writer) error {
	return ExecuteCmdWithConfigurableLogging(ctx, binary, args, env, timeout, stdout, stderr, true, true)
}

// ExecuteCmdWithConfigurableLogging executes the specified binary with the specified args and env.
// Stdout and Stderr are written to stdout and stderr respectively if specified. If not specified
// then Stdout and Stderr will be outputted only to sklog.
func ExecuteCmdWithConfigurableLogging(ctx context.Context, binary string, args, env []string, timeout time.Duration, stdout, stderr io.Writer, logStdout, logStderr bool) error {
	return exec.Run(ctx, &exec.Command{
		Name:        binary,
		Args:        args,
		Env:         env,
		InheritPath: true,
		Timeout:     timeout,
		LogStdout:   logStdout,
		Stdout:      stdout,
		LogStderr:   logStderr,
		Stderr:      stderr,
	})
}

// SyncDir runs "git pull" and "gclient sync" on the specified directory.
// The revisions map enforces revision/hash for the solutions with the format
// branch@rev.
func SyncDir(ctx context.Context, dir string, revisions map[string]string, additionalArgs []string) error {
	err := os.Chdir(dir)
	if err != nil {
		return fmt.Errorf("Could not chdir to %s: %s", dir, err)
	}

	for i := 0; i < MAX_SYNC_TRIES; i++ {
		if i > 0 {
			sklog.Warningf("%d. retry for syncing %s", i, dir)
		}

		err = syncDirStep(ctx, revisions, additionalArgs)
		if err == nil {
			break
		}
		sklog.Errorf("Error syncing %s: %s", dir, err)
	}

	if err != nil {
		sklog.Errorf("Failed to sync %s after %d attempts", dir, MAX_SYNC_TRIES)
	}
	return err
}

func syncDirStep(ctx context.Context, revisions map[string]string, additionalArgs []string) error {
	err := ExecuteCmd(ctx, BINARY_GIT, []string{"pull"}, []string{}, GIT_PULL_TIMEOUT, nil, nil)
	if err != nil {
		return fmt.Errorf("Error running git pull: %s", err)
	}
	syncCmd := []string{"sync", "--force"}
	syncCmd = append(syncCmd, additionalArgs...)
	for branch, rev := range revisions {
		syncCmd = append(syncCmd, "--revision")
		syncCmd = append(syncCmd, fmt.Sprintf("%s@%s", branch, rev))
	}
	err = ExecuteCmd(ctx, BINARY_GCLIENT, syncCmd, []string{}, GCLIENT_SYNC_TIMEOUT, nil, nil)
	if err != nil {
		return fmt.Errorf("Error running gclient sync: %s", err)
	}
	return nil
}

// BuildSkiaSKPInfo builds "skpinfo" in the Skia trunk directory.
func BuildSkiaSKPInfo(ctx context.Context) error {
	if err := os.Chdir(SkiaTreeDir); err != nil {
		return fmt.Errorf("Could not chdir to %s: %s", SkiaTreeDir, err)
	}
	// Run "bin/fetch-gn".
	util.LogErr(ExecuteCmd(ctx, "bin/fetch-gn", []string{}, []string{}, FETCH_GN_TIMEOUT, nil,
		nil))
	// Run "gn gen out/Release --args=...".
	if err := ExecuteCmd(ctx, "buildtools/linux64/gn", []string{"gen", "out/Release", "--args=is_debug=false"}, os.Environ(), GN_GEN_TIMEOUT, nil, nil); err != nil {
		return fmt.Errorf("Error while running gn: %s", err)
	}
	// Run "ninja -C out/Release -j100 skpinfo".
	// Use the full system env when building.
	args := []string{"-C", "out/Release", "-j100", BINARY_SKPINFO}
	return ExecuteCmd(ctx, filepath.Join(DepotToolsDir, "ninja"), args, os.Environ(), NINJA_TIMEOUT, nil, nil)
}

// BuildSkiaLuaPictures builds "lua_pictures" in the Skia trunk directory.
func BuildSkiaLuaPictures(ctx context.Context) error {
	if err := os.Chdir(SkiaTreeDir); err != nil {
		return fmt.Errorf("Could not chdir to %s: %s", SkiaTreeDir, err)
	}
	// Run "bin/fetch-gn".
	util.LogErr(ExecuteCmd(ctx, "bin/fetch-gn", []string{}, []string{}, FETCH_GN_TIMEOUT, nil,
		nil))
	// Run "gn gen out/Release --args=...".
	if err := ExecuteCmd(ctx, "buildtools/linux64/gn", []string{"gen", "out/Release", "--args=is_debug=false skia_use_lua=true"}, os.Environ(), GN_GEN_TIMEOUT, nil, nil); err != nil {
		return fmt.Errorf("Error while running gn: %s", err)
	}
	// Run "ninja -C out/Release -j100 lua_pictures".
	// Use the full system env when building.
	args := []string{"-C", "out/Release", "-j100", BINARY_LUA_PICTURES}
	return ExecuteCmd(ctx, filepath.Join(DepotToolsDir, "ninja"), args, os.Environ(), NINJA_TIMEOUT, nil, nil)
}

// BuildPDFium builds "pdfium_test" in the PDFium repo directory.
func BuildPDFium(ctx context.Context) error {
	if err := os.Chdir(PDFiumTreeDir); err != nil {
		return fmt.Errorf("Could not chdir to %s: %s", SkiaTreeDir, err)
	}

	// Run "build/gyp_pdfium"
	if err := ExecuteCmd(ctx, path.Join("build_gyp", "gyp_pdfium"), []string{},
		[]string{"GYP_DEFINES=\"pdf_use_skia=1\"", "CPPFLAGS=\"-Wno-error\""}, GYP_PDFIUM_TIMEOUT, nil, nil); err != nil {
		return err
	}

	// Build pdfium_test.
	return ExecuteCmd(ctx, BINARY_NINJA, []string{"-C", "out/Debug", BINARY_PDFIUM_TEST},
		[]string{}, NINJA_TIMEOUT, nil, nil)
}

// ResetCheckout resets the specified Git checkout.
func ResetCheckout(ctx context.Context, dir, resetTo string) error {
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("Could not chdir to %s: %s", dir, err)
	}
	// Clear out remnants of incomplete rebases from .git/rebase-apply.
	rebaseArgs := []string{"rebase", "--abort"}
	util.LogErr(ExecuteCmd(ctx, BINARY_GIT, rebaseArgs, []string{}, GIT_REBASE_TIMEOUT, nil, nil))
	// Make sure we are on master branch and not stuck in a rebase branch for whatever reason.
	branchArgs := []string{"checkout", "master"}
	util.LogErr(ExecuteCmd(ctx, BINARY_GIT, branchArgs, []string{}, GIT_BRANCH_TIMEOUT, nil, nil))
	// Run "git reset --hard HEAD"
	resetArgs := []string{"reset", "--hard", resetTo}
	util.LogErr(ExecuteCmd(ctx, BINARY_GIT, resetArgs, []string{}, GIT_RESET_TIMEOUT, nil, nil))
	// Run "git clean -f -d"
	cleanArgs := []string{"clean", "-f", "-d"}
	util.LogErr(ExecuteCmd(ctx, BINARY_GIT, cleanArgs, []string{}, GIT_CLEAN_TIMEOUT, nil, nil))

	return nil
}

// ApplyPatch applies a patch to a Git checkout.
func ApplyPatch(ctx context.Context, patch, dir string) error {
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("Could not chdir to %s: %s", dir, err)
	}
	// Run "git apply --index -p1 --verbose --ignore-whitespace
	//      --ignore-space-change ${PATCH_FILE}"
	args := []string{"apply", "--index", "-p1", "--verbose", "--ignore-whitespace", "--ignore-space-change", patch}
	return ExecuteCmd(ctx, BINARY_GIT, args, []string{}, GIT_APPLY_TIMEOUT, nil, nil)
}

// CleanTmpDir deletes all tmp files from the caller because telemetry tends to
// generate a lot of temporary artifacts there and they take up root disk space.
func CleanTmpDir() {
	files, _ := ioutil.ReadDir(os.TempDir())
	for _, f := range files {
		util.RemoveAll(filepath.Join(os.TempDir(), f.Name()))
	}
}

func GetTimeFromTs(formattedTime string) time.Time {
	t, _ := time.Parse(TS_FORMAT, formattedTime)
	return t
}

func GetCurrentTs() string {
	return time.Now().UTC().Format(TS_FORMAT)
}

// Returns channel that contains all pageset file names without the timestamp
// file and pyc files.
func GetClosedChannelOfPagesets(fileInfos []os.FileInfo) chan string {
	pagesetsChannel := make(chan string, len(fileInfos))
	for _, fileInfo := range fileInfos {
		pagesetName := fileInfo.Name()
		pagesetBaseName := filepath.Base(pagesetName)
		if filepath.Ext(pagesetBaseName) == ".pyc" {
			// Ignore .pyc files.
			continue
		}
		pagesetsChannel <- pagesetName
	}
	close(pagesetsChannel)
	return pagesetsChannel
}

// Running benchmarks in parallel leads to multiple chrome instances coming up
// at the same time, when there are crashes chrome processes stick around which
// can severely impact the machine's performance. To stop this from
// happening chrome zombie processes are periodically killed.
func ChromeProcessesCleaner(ctx context.Context, locker sync.Locker, chromeCleanerTimer time.Duration) {
	for range time.Tick(chromeCleanerTimer) {
		sklog.Info("The chromeProcessesCleaner goroutine has started")
		sklog.Info("Waiting for all existing tasks to complete before killing zombie chrome processes")
		locker.Lock()
		util.LogErr(ExecuteCmd(ctx, "pkill", []string{"-9", "chrome"}, []string{}, PKILL_TIMEOUT, nil, nil))
		locker.Unlock()
	}
}

// Contains the data included in CT pagesets.
type PagesetVars struct {
	// A comma separated list of URLs.
	UrlsList string `json:"urls_list"`
	// Will be either "mobile" or "desktop".
	UserAgent string `json:"user_agent"`
	// The location of the web page's WPR data file.
	ArchiveDataFile string `json:"archive_data_file"`
}

func ReadPageset(pagesetPath string) (PagesetVars, error) {
	decodedPageset := PagesetVars{}
	pagesetContent, err := os.Open(pagesetPath)
	defer util.Close(pagesetContent)
	if err != nil {
		return decodedPageset, fmt.Errorf("Could not read %s: %s", pagesetPath, err)
	}
	if err := json.NewDecoder(pagesetContent).Decode(&decodedPageset); err != nil {
		return decodedPageset, fmt.Errorf("Could not JSON decode %s: %s", pagesetPath, err)
	}
	return decodedPageset, nil
}

// ValidateSKPs moves all root_dir/index/dir_name/*.skp into the root_dir/index
// and validates them. SKPs that fail validation are logged and deleted.
func ValidateSKPs(ctx context.Context, pathToSkps, pathToPyFiles string) error {
	// This slice will be used to run remove_invalid_skp.py.
	skps := []string{}
	// List all directories in pathToSkps and copy out the skps.
	indexDirs, err := filepath.Glob(path.Join(pathToSkps, "*"))
	if err != nil {
		return fmt.Errorf("Unable to read %s: %s", pathToSkps, err)
	}
	for _, indexDir := range indexDirs {
		index := path.Base(indexDir)
		skpFileInfos, err := ioutil.ReadDir(indexDir)
		if err != nil {
			return fmt.Errorf("Unable to read %s: %s", indexDir, err)
		}
		for _, fileInfo := range skpFileInfos {
			if !fileInfo.IsDir() {
				// We are only interested in directories.
				continue
			}
			skpName := fileInfo.Name()
			// Find the largest layer in this directory.
			layerInfos, err := ioutil.ReadDir(filepath.Join(pathToSkps, index, skpName))
			if err != nil {
				sklog.Errorf("Unable to read %s: %s", filepath.Join(pathToSkps, index, skpName), err)
			}
			if len(layerInfos) > 0 {
				largestLayerInfo := layerInfos[0]
				for _, layerInfo := range layerInfos {
					if layerInfo.Size() > largestLayerInfo.Size() {
						largestLayerInfo = layerInfo
					}
				}
				// Only save SKPs greater than 6000 bytes. Less than that are probably
				// malformed.
				if largestLayerInfo.Size() > 6000 {
					layerPath := filepath.Join(pathToSkps, index, skpName, largestLayerInfo.Name())
					destSKP := filepath.Join(pathToSkps, index, skpName+".skp")
					util.Rename(layerPath, destSKP)
					skps = append(skps, destSKP)
				} else {
					sklog.Warningf("Skipping %s because size was less than 6000 bytes", skpName)
				}
			}
			// We extracted what we needed from the directory, now delete it.
			util.RemoveAll(filepath.Join(pathToSkps, index, skpName))
		}
	}

	// Create channel that contains all SKP file paths. This channel will
	// be consumed by the worker pool below to run remove_invalid_skp.py in
	// parallel.
	skpsChannel := make(chan string, len(skps))
	for _, skp := range skps {
		skpsChannel <- skp
	}
	close(skpsChannel)

	sklog.Info("Calling remove_invalid_skp.py")
	// Sync Skia tree. Specify --nohooks otherwise this step could log errors.
	util.LogErr(SyncDir(ctx, SkiaTreeDir, map[string]string{}, []string{"--nohooks"}))
	// Build tools.
	util.LogErr(BuildSkiaSKPInfo(ctx))
	// Run remove_invalid_skp.py in parallel goroutines.
	// Construct path to the python script.
	pathToRemoveSKPs := filepath.Join(pathToPyFiles, "remove_invalid_skp.py")
	pathToSKPInfo := filepath.Join(SkiaTreeDir, "out", "Release", BINARY_SKPINFO)

	var wg sync.WaitGroup

	// Loop through workers in the worker pool.
	for i := 0; i < REMOVE_INVALID_SKPS_WORKER_POOL; i++ {
		// Increment the WaitGroup counter.
		wg.Add(1)

		// Create and run a goroutine closure that captures SKPs.
		go func(i int) {
			// Decrement the WaitGroup counter when the goroutine completes.
			defer wg.Done()

			for skpPath := range skpsChannel {
				args := []string{
					pathToRemoveSKPs,
					"--path_to_skp=" + skpPath,
					"--path_to_skpinfo=" + pathToSKPInfo,
				}
				sklog.Infof("Executing remove_invalid_skp.py with goroutine#%d", i+1)
				// Execute the command with stdout not logged. It otherwise outputs
				// tons of log msgs.
				util.LogErr(exec.Run(ctx, &exec.Command{
					Name:        "python",
					Args:        args,
					Env:         []string{},
					InheritPath: true,
					Timeout:     REMOVE_INVALID_SKPS_TIMEOUT,
					LogStdout:   false,
					Stdout:      nil,
					LogStderr:   true,
					Stderr:      nil,
				}))
			}
		}(i)
	}

	// Wait for all spawned goroutines to complete.
	wg.Wait()

	return nil
}

// GetStartRange returns the range worker should start processing at based on its num and how many
// pages it is allowed to process.
func GetStartRange(workerNum, numPagesPerBot int) int {
	return ((workerNum - 1) * numPagesPerBot) + 1
}

// GetNumPagesPerBot returns the number of web pages each worker should process.
func GetNumPagesPerBot(repeatValue, maxPagesPerBot int) int {
	return int(math.Ceil(float64(maxPagesPerBot) / float64(repeatValue)))
}

// TriggerSwarmingTask returns the number of triggered tasks and an error (if any).
func TriggerSwarmingTask(ctx context.Context, pagesetType, taskPrefix, isolateName, runID string, hardTimeout, ioTimeout time.Duration, priority, maxPagesPerBot, numPages int, isolateExtraArgs map[string]string, runOnGCE bool, repeatValue int) (int, error) {
	// Instantiate the swarming client.
	workDir, err := ioutil.TempDir(StorageDir, "swarming_work_")
	if err != nil {
		return 0, fmt.Errorf("Could not get temp dir: %s", err)
	}
	s, err := swarming.NewSwarmingClient(ctx, workDir, swarming.SWARMING_SERVER_PRIVATE, isolate.ISOLATE_SERVER_URL_PRIVATE)
	if err != nil {
		// Cleanup workdir.
		if err := os.RemoveAll(workDir); err != nil {
			sklog.Errorf("Could not cleanup swarming work dir: %s", err)
		}
		return 0, fmt.Errorf("Could not instantiate swarming client: %s", err)
	}
	defer s.Cleanup()
	// Create isolated.gen.json files from tasks.
	genJSONs := []string{}
	// Get path to isolate files.
	_, currentFile, _, _ := runtime.Caller(0)
	pathToIsolates := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(currentFile))), "isolates")
	numPagesPerBot := GetNumPagesPerBot(repeatValue, maxPagesPerBot)
	numTasks := int(math.Ceil(float64(numPages) / float64(numPagesPerBot)))
	for i := 1; i <= numTasks; i++ {
		isolateArgs := map[string]string{
			"START_RANGE":  strconv.Itoa(GetStartRange(i, numPagesPerBot)),
			"NUM":          strconv.Itoa(numPagesPerBot),
			"PAGESET_TYPE": pagesetType,
		}
		// Add isolateExtraArgs (if specified) into the isolateArgs.
		for k, v := range isolateExtraArgs {
			isolateArgs[k] = v
		}
		taskName := fmt.Sprintf("%s_%d", taskPrefix, i)
		genJSON, err := s.CreateIsolatedGenJSON(path.Join(pathToIsolates, isolateName), s.WorkDir, "linux", taskName, isolateArgs, []string{})
		if err != nil {
			return numTasks, fmt.Errorf("Could not create isolated.gen.json for task %s: %s", taskName, err)
		}
		genJSONs = append(genJSONs, genJSON)
	}

	// Batcharchive the tasks. Do not batcharchive more than 1000 at a time.
	tasksToHashes := map[string]string{}
	for i := 0; i < len(genJSONs); i += 1000 {
		startRange := i
		endRange := util.MinInt(len(genJSONs), i+1000)
		t, err := s.BatchArchiveTargets(ctx, genJSONs[startRange:endRange], BATCHARCHIVE_TIMEOUT)
		if err != nil {
			return numTasks, fmt.Errorf("Could not batch archive targets: %s", err)
		}
		// Add the above map to tasksToHashes.
		for k, v := range t {
			tasksToHashes[k] = v
		}
		// Sleep for a sec to give the swarming server some time to recuperate.
		time.Sleep(time.Second)
	}

	if len(genJSONs) != len(tasksToHashes) {
		return numTasks, fmt.Errorf("len(genJSONs) was %d and len(tasksToHashes) was %d", len(genJSONs), len(tasksToHashes))
	}
	var dimensions map[string]string
	if runOnGCE {
		dimensions = GCE_WORKER_DIMENSIONS
	} else {
		dimensions = GOLO_WORKER_DIMENSIONS
	}

	// The channel where batches of tasks to be triggered and collected will be sent to.
	chTasks := make(chan map[string]string)
	// Kick off one goroutine to populate the above channel.
	go func() {
		defer close(chTasks)
		tmpMap := map[string]string{}
		for task, hash := range tasksToHashes {
			if len(tmpMap) >= MAX_SIMULTANEOUS_SWARMING_TASKS_PER_RUN {
				// Add the map to the channel.
				chTasks <- tmpMap
				// Reinitialize the temporary map.
				tmpMap = map[string]string{}
			}
			tmpMap[task] = hash
		}
		chTasks <- tmpMap
	}()

	// Trigger and collect swarming tasks.
	for taskMap := range chTasks {
		// Save all retried tasks so that we can collect them at the end.
		retriedTasks := []*swarming.SwarmingTask{}
		// Trigger swarming using the isolate hashes.
		tasks, err := s.TriggerSwarmingTasks(ctx, taskMap, dimensions, map[string]string{"runid": runID}, priority, 7*24*time.Hour, hardTimeout, ioTimeout, false, true, getServiceAccount(dimensions))
		if err != nil {
			return numTasks, fmt.Errorf("Could not trigger swarming tasks: %s", err)
		}
		// Collect all tasks and retrigger the ones that fail.
		for _, task := range tasks {
			if _, _, err := task.Collect(ctx, s); err != nil {
				sklog.Errorf("task %s failed: %s", task.Title, err)
				sklog.Infof("Retrying task %s", task.Title)
				t, err := s.TriggerSwarmingTasks(ctx, map[string]string{task.Title: tasksToHashes[task.Title]}, dimensions, map[string]string{"runid": runID}, priority, 7*24*time.Hour, hardTimeout, ioTimeout, false, true, getServiceAccount(dimensions))
				if err != nil {
					return numTasks, fmt.Errorf("Could not trigger retry of swarming tasks: %s", err)
				}
				retriedTasks = append(retriedTasks, t...)
				continue
			}
		}

		if len(retriedTasks) > 0 {
			// Collect all retried tasks and log the ones that fail.
			for _, task := range retriedTasks {
				if _, _, err := task.Collect(ctx, s); err != nil {
					sklog.Errorf("task %s failed inspite of a retry: %s", task.Title, err)
					continue
				}
			}
		}

	}

	return numTasks, nil
}

// getServiceAccount returns the service account that should be used when triggering swarming tasks.
func getServiceAccount(dimensions map[string]string) string {
	serviceAccount := ""
	if util.MapsEqual(dimensions, GCE_WORKER_DIMENSIONS) || util.MapsEqual(dimensions, GCE_LINUX_BUILDER_DIMENSIONS) || util.MapsEqual(dimensions, GCE_ANDROID_BUILDER_DIMENSIONS) {
		// GCE bots need to use "bot". See skbug.com/6611.
		serviceAccount = "bot"
	}
	return serviceAccount
}

// GetPathToPyFiles returns the location of CT's python scripts.
func GetPathToPyFiles(runOnSwarming bool) string {
	if runOnSwarming {
		return filepath.Join(filepath.Dir(filepath.Dir(os.Args[0])), "src", "go.skia.org", "infra", "ct", "py")
	} else {
		_, currentFile, _, _ := runtime.Caller(0)
		return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(currentFile))), "py")
	}
}

func MergeUploadCSVFiles(ctx context.Context, runID, pathToPyFiles string, gs *GcsUtil, totalPages, maxPagesPerBot int, handleStrings bool, repeatValue int) ([]string, error) {
	localOutputDir := filepath.Join(StorageDir, BenchmarkRunsDir, runID)
	util.MkdirAll(localOutputDir, 0700)
	noOutputSlaves := []string{}
	// Copy outputs from all slaves locally.
	numPagesPerBot := GetNumPagesPerBot(repeatValue, maxPagesPerBot)
	numTasks := int(math.Ceil(float64(totalPages) / float64(numPagesPerBot)))
	for i := 1; i <= numTasks; i++ {
		startRange := GetStartRange(i, numPagesPerBot)
		workerLocalOutputPath := filepath.Join(localOutputDir, strconv.Itoa(startRange)+".csv")
		workerRemoteOutputPath := filepath.Join(BenchmarkRunsDir, runID, strconv.Itoa(startRange), "outputs", runID+".output")
		respBody, err := gs.GetRemoteFileContents(workerRemoteOutputPath)
		if err != nil {
			sklog.Errorf("Could not fetch %s: %s", workerRemoteOutputPath, err)
			noOutputSlaves = append(noOutputSlaves, strconv.Itoa(i))
			continue
		}
		defer util.Close(respBody)
		out, err := os.Create(workerLocalOutputPath)
		if err != nil {
			return noOutputSlaves, fmt.Errorf("Unable to create file %s: %s", workerLocalOutputPath, err)
		}
		defer util.Close(out)
		defer util.Remove(workerLocalOutputPath)
		if _, err = io.Copy(out, respBody); err != nil {
			return noOutputSlaves, fmt.Errorf("Unable to copy to file %s: %s", workerLocalOutputPath, err)
		}
		// If an output is less than 20 bytes that means something went wrong on the slave.
		outputInfo, err := out.Stat()
		if err != nil {
			return noOutputSlaves, fmt.Errorf("Unable to stat file %s: %s", workerLocalOutputPath, err)
		}
		if outputInfo.Size() <= 20 {
			sklog.Errorf("Output file was less than 20 bytes %s: %s", workerLocalOutputPath, err)
			noOutputSlaves = append(noOutputSlaves, strconv.Itoa(i))
			continue
		}
	}
	// Call csv_merger.py to merge all results into a single results CSV.
	pathToCsvMerger := filepath.Join(pathToPyFiles, "csv_merger.py")
	outputFileName := runID + ".output"
	args := []string{
		pathToCsvMerger,
		"--csv_dir=" + localOutputDir,
		"--output_csv_name=" + filepath.Join(localOutputDir, outputFileName),
	}
	if handleStrings {
		args = append(args, "--handle_strings")
	}
	err := ExecuteCmd(ctx, "python", args, []string{}, CSV_MERGER_TIMEOUT, nil, nil)
	if err != nil {
		return noOutputSlaves, fmt.Errorf("Error running csv_merger.py: %s", err)
	}
	// Copy the output file to Google Storage.
	remoteOutputDir := filepath.Join(BenchmarkRunsDir, runID, "consolidated_outputs")
	if err := gs.UploadFile(outputFileName, localOutputDir, remoteOutputDir); err != nil {
		return noOutputSlaves, fmt.Errorf("Unable to upload %s to %s: %s", outputFileName, remoteOutputDir, err)
	}
	return noOutputSlaves, nil
}

// GetRepeatValue returns the defaultValue if "--pageset-repeat" is not specified in benchmarkArgs.
func GetRepeatValue(benchmarkArgs string, defaultValue int) int {
	return GetIntFlagValue(benchmarkArgs, PAGESET_REPEAT_FLAG, defaultValue)
}

// GetRunBenchmarkTimeoutValue returns the defaultValue if "--run_benchmark_timeout" is not specified in benchmarkArgs.
func GetRunBenchmarkTimeoutValue(benchmarkArgs string, defaultValue int) int {
	return GetIntFlagValue(benchmarkArgs, RUN_BENCHMARK_TIMEOUT_FLAG, defaultValue)
}

// GetMaxPagesPerBotValue returns the defaultValue if "--max-pages-per-bot" is not specified in benchmarkArgs.
func GetMaxPagesPerBotValue(benchmarkArgs string, defaultValue int) int {
	return GetIntFlagValue(benchmarkArgs, MAX_PAGES_PER_BOT, defaultValue)
}

// GetNumAnalysisRetriesValue returns the defaultValue if "--num-analysis-retries" is not specified in benchmarkArgs.
func GetNumAnalysisRetriesValue(benchmarkArgs string, defaultValue int) int {
	return GetIntFlagValue(benchmarkArgs, NUM_ANALYSIS_RETRIES, defaultValue)
}

// GetIntFlagValue returns the defaultValue if the specified flag name is not in benchmarkArgs.
func GetIntFlagValue(benchmarkArgs, flagName string, defaultValue int) int {
	if strings.Contains(benchmarkArgs, flagName) {
		r := regexp.MustCompile(flagName + `[= ](\d+)`)
		m := r.FindStringSubmatch(benchmarkArgs)
		if len(m) != 0 {
			ret, err := strconv.Atoi(m[1])
			if err != nil {
				return defaultValue
			}
			return ret
		}
	}
	// If we reached here then return the default Value.
	return defaultValue
}

func RemoveFlagsFromArgs(benchmarkArgs string, flags ...string) string {
	for _, f := range flags {
		re, err := regexp.Compile(fmt.Sprintf(`\s*%s(=[[:alnum:]]*)?\s*`, f))
		if err != nil {
			sklog.Warningf("Could not compile flag regex with %s: %s", f, err)
			continue
		}
		benchmarkArgs = re.ReplaceAllString(benchmarkArgs, " ")
	}
	// Remove extra whitespace.
	return strings.Join(strings.Fields(benchmarkArgs), " ")
}

// RunBenchmark runs the specified benchmark with the specified arguments. It prints the output of
// the run_benchmark command and also returns the output incase the caller needs to do any
// post-processing on it. Incase of any errors the output will be empty.
func RunBenchmark(ctx context.Context, fileInfoName, pathToPagesets, pathToPyFiles, localOutputDir, chromiumBuildName, chromiumBinary, runID, browserExtraArgs, benchmarkName, targetPlatform, benchmarkExtraArgs, pagesetType string, defaultRepeatValue int) (string, error) {
	pagesetBaseName := filepath.Base(fileInfoName)
	if filepath.Ext(pagesetBaseName) == ".pyc" {
		// Ignore .pyc files.
		return "", nil
	}
	// Read the pageset.
	pagesetName := strings.TrimSuffix(pagesetBaseName, filepath.Ext(pagesetBaseName))
	pagesetPath := filepath.Join(pathToPagesets, fileInfoName)
	decodedPageset, err := ReadPageset(pagesetPath)
	if err != nil {
		return "", fmt.Errorf("Could not read %s: %s", pagesetPath, err)
	}
	sklog.Infof("===== Processing %s for %s =====", pagesetPath, runID)
	args := []string{
		filepath.Join(TelemetryBinariesDir, BINARY_RUN_BENCHMARK),
		benchmarkName,
		"--also-run-disabled-tests",
		"--user-agent=" + decodedPageset.UserAgent,
		"--urls-list=" + decodedPageset.UrlsList,
		"--archive-data-file=" + decodedPageset.ArchiveDataFile,
	}
	// Need to capture output for all benchmarks.
	outputDirArgValue := filepath.Join(localOutputDir, pagesetName)
	args = append(args, "--output-dir="+outputDirArgValue)
	// Figure out which browser and device should be used.
	if targetPlatform == PLATFORM_ANDROID {
		if err := InstallChromeAPK(ctx, chromiumBuildName); err != nil {
			return "", fmt.Errorf("Error while installing APK: %s", err)
		}
		args = append(args, "--browser=android-chromium")
	} else {
		args = append(args, "--browser=exact", "--browser-executable="+chromiumBinary)
		args = append(args, "--device=desktop")
	}

	// Calculate the timeout.
	timeoutSecs := GetRunBenchmarkTimeoutValue(benchmarkExtraArgs, PagesetTypeToInfo[pagesetType].RunChromiumPerfTimeoutSecs)
	repeatBenchmark := GetRepeatValue(benchmarkExtraArgs, defaultRepeatValue)
	if repeatBenchmark > 0 {
		args = append(args, fmt.Sprintf("%s=%d", PAGESET_REPEAT_FLAG, repeatBenchmark))
		// Increase the timeoutSecs if repeats are used.
		timeoutSecs = timeoutSecs * repeatBenchmark
	}
	sklog.Infof("Using %d seconds for timeout", timeoutSecs)

	// Remove from benchmarkExtraArgs "special" flags that are recognized by CT but not
	// by the run_benchmark script.
	benchmarkExtraArgs = RemoveFlagsFromArgs(benchmarkExtraArgs, RUN_BENCHMARK_TIMEOUT_FLAG, MAX_PAGES_PER_BOT, NUM_ANALYSIS_RETRIES)
	// Split benchmark args if not empty and append to args.
	if benchmarkExtraArgs != "" {
		args = append(args, strings.Fields(benchmarkExtraArgs)...)
	}

	// Add browserArgs if not empty to args.
	if browserExtraArgs != "" {
		args = append(args, "--extra-browser-args="+browserExtraArgs)
	}
	// Set the PYTHONPATH to the pagesets and the telemetry dirs.
	env := []string{
		fmt.Sprintf("PYTHONPATH=%s:%s:%s:%s:$PYTHONPATH", pathToPagesets, TelemetryBinariesDir, TelemetrySrcDir, CatapultSrcDir),
		"DISPLAY=:0",
	}
	// Append the original environment as well.
	for _, e := range os.Environ() {
		env = append(env, e)
	}

	// Create buffer for capturing the stdout and stderr of the benchmark run.
	var b bytes.Buffer
	if _, err := b.WriteString(fmt.Sprintf("========== Stdout and stderr for %s ==========\n", pagesetPath)); err != nil {
		return "", fmt.Errorf("Error writing to output buffer: %s", err)
	}
	if err := ExecuteCmdWithConfigurableLogging(ctx, "python", args, env, time.Duration(timeoutSecs)*time.Second, &b, &b, false, false); err != nil {
		if targetPlatform == PLATFORM_ANDROID {
			// Kill the port-forwarder to start from a clean slate.
			util.LogErr(ExecuteCmdWithConfigurableLogging(ctx, "pkill", []string{"-f", "forwarder_host"}, []string{}, PKILL_TIMEOUT, &b, &b, false, false))
		}
		output, getErr := getRunBenchmarkOutput(b, pagesetPath)
		util.LogErr(getErr)
		fmt.Println(output)
		return "", fmt.Errorf("Run benchmark command failed with: %s", err)
	}
	output, err := getRunBenchmarkOutput(b, pagesetPath)
	if err != nil {
		return "", fmt.Errorf("Could not get run benchmark output: %s", err)
	}
	// Print the output and return.
	fmt.Println(output)
	return output, nil
}

func getRunBenchmarkOutput(b bytes.Buffer, pagesetPath string) (string, error) {
	if _, err := b.WriteString("===================="); err != nil {
		return "", fmt.Errorf("Error writing to output buffer: %s", err)
	}
	return b.String(), nil
}

func MergeUploadCSVFilesOnWorkers(ctx context.Context, localOutputDir, pathToPyFiles, runID, remoteDir string, gs *GcsUtil, startRange int, handleStrings bool, pageRankToAdditionalFields map[string]map[string]string) error {
	// Move all results into a single directory.
	fileInfos, err := ioutil.ReadDir(localOutputDir)
	if err != nil {
		return fmt.Errorf("Unable to read %s: %s", localOutputDir, err)
	}
	for _, fileInfo := range fileInfos {
		if !fileInfo.IsDir() {
			continue
		}
		outputFile := filepath.Join(localOutputDir, fileInfo.Name(), "results.csv")
		newFile := filepath.Join(localOutputDir, fmt.Sprintf("%s.csv", fileInfo.Name()))
		if err := os.Rename(outputFile, newFile); err != nil {
			sklog.Errorf("Could not rename %s to %s: %s", outputFile, newFile, err)
			continue
		}
		// Add the rank of the page to the CSV file.
		headers, values, err := getRowsFromCSV(newFile)
		if err != nil {
			sklog.Errorf("Could not read %s: %s", newFile, err)
			continue
		}
		pageRank := fileInfo.Name()
		pageNameWithRank := ""
		for i := range headers {
			for j := range values {
				if headers[i] == "stories" {
					pageNameWithRank = fmt.Sprintf("%s (#%s)", values[j][i], pageRank)
					values[j][i] = pageNameWithRank
				}
			}
		}
		// Add additionalFields (if any) to the output CSV.
		if additionalFields, ok := pageRankToAdditionalFields[fileInfo.Name()]; ok {
			for h, v := range additionalFields {
				valueLine := make([]string, len(headers))
				for i := range headers {
					if headers[i] == "name" {
						valueLine[i] = h
					} else if headers[i] == "avg" {
						valueLine[i] = v
					} else if headers[i] == "stories" {
						valueLine[i] = pageNameWithRank
					} else {
						valueLine[i] = ""
					}
				}
				values = append(values, valueLine)
			}
		}
		if err := writeRowsToCSV(newFile, headers, values); err != nil {
			sklog.Errorf("Could not write to %s: %s", newFile, err)
			continue
		}
	}
	// Call csv_pivot_table_merger.py to merge all results into a single results CSV.
	pathToCsvMerger := filepath.Join(pathToPyFiles, "csv_pivot_table_merger.py")
	outputFileName := runID + ".output"
	args := []string{
		pathToCsvMerger,
		"--csv_dir=" + localOutputDir,
		"--output_csv_name=" + filepath.Join(localOutputDir, outputFileName),
	}
	if handleStrings {
		args = append(args, "--handle_strings")
	}
	err = ExecuteCmd(ctx, "python", args, []string{}, CSV_PIVOT_TABLE_MERGER_TIMEOUT, nil, nil)
	if err != nil {
		return fmt.Errorf("Error running csv_pivot_table_merger.py: %s", err)
	}
	// Copy the output file to Google Storage.
	remoteOutputDir := filepath.Join(remoteDir, strconv.Itoa(startRange), "outputs")
	if err := gs.UploadFile(outputFileName, localOutputDir, remoteOutputDir); err != nil {
		return fmt.Errorf("Unable to upload %s to %s: %s", outputFileName, remoteOutputDir, err)
	}
	return nil
}

func getRowsFromCSV(csvPath string) ([]string, [][]string, error) {
	csvFile, err := os.Open(csvPath)
	defer util.Close(csvFile)
	if err != nil {
		return nil, nil, fmt.Errorf("Could not open %s: %s", csvPath, err)
	}
	reader := csv.NewReader(csvFile)
	reader.FieldsPerRecord = -1
	rawCSVdata, err := reader.ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("Could not read %s: %s", csvPath, err)
	}
	if len(rawCSVdata) < 2 {
		return nil, nil, fmt.Errorf("No data in %s", csvPath)
	}
	return rawCSVdata[0], rawCSVdata[1:], nil
}

func writeRowsToCSV(csvPath string, headers []string, values [][]string) error {
	csvFile, err := os.OpenFile(csvPath, os.O_WRONLY, 666)
	defer util.Close(csvFile)
	if err != nil {
		return fmt.Errorf("Could not open %s: %s", csvPath, err)
	}
	writer := csv.NewWriter(csvFile)
	defer writer.Flush()
	// Write the headers.
	if err := writer.Write(headers); err != nil {
		return fmt.Errorf("Could not write to %s: %s", csvPath, err)
	}
	// Write all values.
	for _, row := range values {
		if err := writer.Write(row); err != nil {
			return fmt.Errorf("Could not write to %s: %s", csvPath, err)
		}
	}
	return nil
}

// TriggerBuildRepoSwarmingTask creates a isolated.gen.json file using BUILD_REPO_ISOLATE,
// archives it, and triggers it's swarming task. The swarming task will run the build_repo
// worker script which will return a list of remote build directories.
func TriggerBuildRepoSwarmingTask(ctx context.Context, taskName, runID, repo, targetPlatform string, hashes, patches []string, singleBuild bool, hardTimeout, ioTimeout time.Duration) ([]string, error) {
	// Instantiate the swarming client.
	workDir, err := ioutil.TempDir(StorageDir, "swarming_work_")
	if err != nil {
		return nil, fmt.Errorf("Could not get temp dir: %s", err)
	}
	s, err := swarming.NewSwarmingClient(ctx, workDir, swarming.SWARMING_SERVER_PRIVATE, isolate.ISOLATE_SERVER_URL_PRIVATE)
	if err != nil {
		// Cleanup workdir.
		if err := os.RemoveAll(workDir); err != nil {
			sklog.Errorf("Could not cleanup swarming work dir: %s", err)
		}
		return nil, fmt.Errorf("Could not instantiate swarming client: %s", err)
	}
	defer s.Cleanup()
	// Create isolated.gen.json.
	// Get path to isolate files.
	_, currentFile, _, _ := runtime.Caller(0)
	pathToIsolates := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(currentFile))), "isolates")
	isolateArgs := map[string]string{
		"RUN_ID":          runID,
		"REPO":            repo,
		"HASHES":          strings.Join(hashes, ","),
		"PATCHES":         strings.Join(patches, ","),
		"SINGLE_BUILD":    strconv.FormatBool(singleBuild),
		"TARGET_PLATFORM": targetPlatform,
	}
	genJSON, err := s.CreateIsolatedGenJSON(path.Join(pathToIsolates, BUILD_REPO_ISOLATE), s.WorkDir, "linux", taskName, isolateArgs, []string{})
	if err != nil {
		return nil, fmt.Errorf("Could not create isolated.gen.json for task %s: %s", taskName, err)
	}
	// Batcharchive the task.
	tasksToHashes, err := s.BatchArchiveTargets(ctx, []string{genJSON}, BATCHARCHIVE_TIMEOUT)
	if err != nil {
		return nil, fmt.Errorf("Could not batch archive target: %s", err)
	}
	// Trigger swarming using the isolate hash.
	var dimensions map[string]string
	if targetPlatform == "Android" {
		dimensions = GCE_ANDROID_BUILDER_DIMENSIONS
	} else {
		dimensions = GCE_LINUX_BUILDER_DIMENSIONS
	}
	tasks, err := s.TriggerSwarmingTasks(ctx, tasksToHashes, dimensions, map[string]string{"runid": runID}, swarming.RECOMMENDED_PRIORITY, 2*24*time.Hour, hardTimeout, ioTimeout, false, true, getServiceAccount(dimensions))
	if err != nil {
		return nil, fmt.Errorf("Could not trigger swarming task: %s", err)
	}
	if len(tasks) != 1 {
		return nil, fmt.Errorf("Expected a single task instead got: %v", tasks)
	}
	// Collect all tasks and log the ones that fail.
	task := tasks[0]
	_, outputDir, err := task.Collect(ctx, s)
	if err != nil {
		return nil, fmt.Errorf("task %s failed: %s", task.Title, err)
	}
	outputFile := filepath.Join(outputDir, BUILD_OUTPUT_FILENAME)
	contents, err := ioutil.ReadFile(outputFile)
	if err != nil {
		return nil, fmt.Errorf("Could not read outputfile %s: %s", outputFile, err)
	}
	return strings.Split(string(contents), ","), nil
}

func DownloadPatch(localPath, remotePath string, gs *GcsUtil) (int64, error) {
	respBody, err := gs.GetRemoteFileContents(remotePath)
	if err != nil {
		return -1, fmt.Errorf("Could not fetch %s: %s", remotePath, err)
	}
	defer util.Close(respBody)
	f, err := os.Create(localPath)
	if err != nil {
		return -1, fmt.Errorf("Could not create %s: %s", localPath, err)
	}
	defer util.Close(f)
	written, err := io.Copy(f, respBody)
	if err != nil {
		return -1, fmt.Errorf("Could not write to %s: %s", localPath, err)
	}
	return written, nil
}

// RemoveCatapultLockFiles cleans up any leftover "pseudo_lock" files from the
// catapult repo. See skbug.com/5919#c16 for context.
func RemoveCatapultLockFiles(catapultSrcDir string) error {
	visit := func(path string, f os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if f.IsDir() {
			return nil
		}
		if filepath.Ext(f.Name()) == ".pseudo_lock" {
			if err := os.Remove(path); err != nil {
				return err
			}

		}
		return nil
	}
	return filepath.Walk(catapultSrcDir, visit)
}

func DownloadAndApplyPatch(ctx context.Context, patchName, localDir, remotePatchesDir, checkout string, gs *GcsUtil) error {
	patchLocalPath := filepath.Join(localDir, patchName)
	patchRemotePath := filepath.Join(remotePatchesDir, patchName)
	written, err := DownloadPatch(patchLocalPath, patchRemotePath, gs)
	if err != nil {
		return fmt.Errorf("Could not download %s: %s", patchRemotePath, err)
	}
	// Apply patch to the local checkout.
	if written > 10 {
		if err := ApplyPatch(ctx, patchLocalPath, checkout); err != nil {
			return fmt.Errorf("Could not apply patch in %s: %s", checkout, err)
		}
	}
	return nil
}

// GetArchivesNum returns the number of archives for the specified pagesetType.
// -1 is returned if USE_LIVE_SITES_FLAGS is specified or if there is an error.
func GetArchivesNum(gs *GcsUtil, benchmarkArgs, pagesetType string) (int, error) {
	if strings.Contains(benchmarkArgs, USE_LIVE_SITES_FLAGS) {
		return -1, nil
	}
	// Calculate the number of archives the workers worked with.
	archivesRemoteDir := filepath.Join(SWARMING_DIR_NAME, WEB_ARCHIVES_DIR_NAME, pagesetType)
	totalArchiveArtifacts, err := gs.GetRemoteDirCount(archivesRemoteDir)
	if err != nil {
		return -1, fmt.Errorf("Could not find archives in %s: %s", archivesRemoteDir, err)
	}
	// Each archive has a JSON file, a WPR file and a WPR.sha1 file.
	return totalArchiveArtifacts / 3, nil
}

// GetHashesFromBuild returns the Chromium and Skia hashes from a CT build string.
// Example build string: try-27af50f-d5dcd58-rmistry-20151026102511-nopatch.
func GetHashesFromBuild(chromiumBuild string) (string, string) {
	tokens := strings.Split(chromiumBuild, "-")
	return tokens[1], tokens[2]
}

// GetNumPages returns the number of specified custom webpages. If Custom
// webpages are not specified then the number of pages associated with the
// pageset type is returned.
func GetNumPages(pagesetType, customWebPagesFilePath string) (int, error) {
	customPages, err := GetCustomPages(customWebPagesFilePath)
	if err != nil {
		return PagesetTypeToInfo[pagesetType].NumPages, err
	}
	if len(customPages) == 0 {
		return PagesetTypeToInfo[pagesetType].NumPages, nil
	}
	return len(customPages), nil
}

// GetCustomPages returns the specified custom webpages. If Custom
// webpages are not specified then it returns an empty slice.
func GetCustomPages(customWebPagesFilePath string) ([]string, error) {
	csvFile, err := os.Open(customWebPagesFilePath)
	if err != nil {
		return nil, err
	}
	defer util.Close(csvFile)
	reader := csv.NewReader(csvFile)
	customPages := []string{}
	for {
		records, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		for _, record := range records {
			if strings.TrimSpace(record) == "" {
				continue
			}
			customPages = append(customPages, record)
		}
	}
	return customPages, nil
}

func GetCustomPagesWithinRange(startRange, num int, customWebpages []string) []string {
	startIndex := startRange - 1
	endIndex := util.MinInt(startIndex+num, len(customWebpages))
	return customWebpages[startIndex:endIndex]
}

func CreateCustomPagesets(webpages []string, pagesetsDir, targetPlatform string) error {
	// Empty the local dir.
	util.RemoveAll(pagesetsDir)
	// Create the local dir.
	util.MkdirAll(pagesetsDir, 0700)
	// Figure out which user agent to use.
	var userAgent string
	if targetPlatform == PLATFORM_ANDROID {
		userAgent = "mobile"
	} else {
		userAgent = "desktop"
	}
	for i, w := range webpages {
		pagesetPath := filepath.Join(pagesetsDir, fmt.Sprintf("%d.py", i+1))
		if err := WritePageset(pagesetPath, userAgent, DEFAULT_CUSTOM_PAGE_ARCHIVEPATH, w); err != nil {
			return err
		}
	}
	return nil
}

func GetBasePixelDiffRemoteDir(runID string) (string, error) {
	// Parse the CT runID to extract YYYY/MM/DD/HH
	regex := regexp.MustCompile(`[a-z]+-([0-9]{4})([0-9]{2})([0-9]{2})([0-9]{2})[0-9]{4}`)
	matches := regex.FindStringSubmatch(runID)
	if len(matches) != 5 {
		return "", fmt.Errorf("Could not parse runID %s with the regex %q", runID, regex.String())
	}
	return filepath.Join(PixelDiffRunsDir, matches[1], matches[2], matches[3], matches[4], runID), nil
}

func GetRankFromPageset(pagesetFileName string) (int, error) {
	// All CT pagesets are of the form [rank].py so just stripping out the
	// extension should give us the rank of the pageset.
	var extension = filepath.Ext(pagesetFileName)
	rank := pagesetFileName[0 : len(pagesetFileName)-len(extension)]
	return strconv.Atoi(rank)
}

type Pageset struct {
	UserAgent       string `json:"user_agent"`
	ArchiveDataFile string `json:"archive_data_file"`
	UrlsList        string `json:"urls_list"`
}

func WritePageset(filePath, userAgent, archiveFilePath, url string) error {
	pageSet := Pageset{
		UserAgent:       userAgent,
		ArchiveDataFile: archiveFilePath,
		UrlsList:        url,
	}
	b, err := json.Marshal(pageSet)
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(filePath, b, 0644); err != nil {
		return err
	}
	return nil
}

type TimeoutTracker struct {
	timeoutCounter      int
	timeoutCounterMutex sync.Mutex
}

func (t *TimeoutTracker) Increment() {
	t.timeoutCounterMutex.Lock()
	defer t.timeoutCounterMutex.Unlock()
	t.timeoutCounter++
}

func (t *TimeoutTracker) Reset() {
	t.timeoutCounterMutex.Lock()
	defer t.timeoutCounterMutex.Unlock()
	t.timeoutCounter = 0
}

func (t *TimeoutTracker) Read() int {
	t.timeoutCounterMutex.Lock()
	defer t.timeoutCounterMutex.Unlock()
	return t.timeoutCounter
}

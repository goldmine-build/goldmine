// Application that builds chromium with or without patches and uploads the build
// to Google Storage.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"go.skia.org/infra/ct/go/master_scripts/master_common"
	"go.skia.org/infra/ct/go/util"
	"go.skia.org/infra/go/sklog"
)

var (
	targetPlatform = flag.String("target_platform", util.PLATFORM_ANDROID, "The platform the benchmark will run on (Android / Linux).")
	chromiumHash   = flag.String("chromium_hash", "", "The Chromium commit hash the checkout should be synced to. If not specified then Chromium's ToT hash is used.")
	skiaHash       = flag.String("skia_hash", "", "The Skia commit hash the checkout should be synced to. If not specified then Skia's LKGR hash is used (the hash in Chromium's DEPS file).")
)

func buildChromium() error {
	master_common.Init("build_chromium")

	ctx := context.Background()

	// Finish with glog flush and how long the task took.
	defer util.TimeTrack(time.Now(), "Running build chromium")
	defer sklog.Flush()

	if *chromiumHash == "" {
		return errors.New("Must specify --chromium_hash")
	}
	if *skiaHash == "" {
		return errors.New("Must specify --skia_hash")
	}

	// Create the required chromium build.
	// Note: chromium_builds.CreateChromiumBuildOnSwarming specifies the
	//       "-DSK_WHITELIST_SERIALIZED_TYPEFACES" flag only when *runID is empty.
	//       Since builds created by this master script will be consumed only by the
	//       capture_skps tasks (which require that flag) specify runID as empty here.
	chromiumBuilds, err := util.TriggerBuildRepoSwarmingTask(ctx, "build_chromium", "", "chromium", "Linux", "", []string{*chromiumHash, *skiaHash}, []string{}, []string{}, true /*singleBuild*/, *master_common.Local, 3*time.Hour, 1*time.Hour)
	if err != nil {
		return fmt.Errorf("Error encountered when swarming build repo task: %s", err)
	}
	if len(chromiumBuilds) != 1 {
		return fmt.Errorf("Expected 1 build but instead got %d: %v", len(chromiumBuilds), chromiumBuilds)
	}

	return nil
}

func main() {
	retCode := 0
	if err := buildChromium(); err != nil {
		sklog.Errorf("Error while running build chromium: %s", err)
		retCode = 255
	}
	os.Exit(retCode)
}

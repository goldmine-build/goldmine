package google_cloud_sdk

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"

	"go.goldmine.build/bazel/go/bazel"
	"go.goldmine.build/go/skerr"
)

// FindGcloud returns the path to the `gcloud` binary downloaded by Bazel.
//
// Calling this function from any Go package will automatically establish a Bazel dependency on the
// corresponding external Bazel repository.
func FindGcloud() (string, error) {
	if !bazel.InBazel() {
		return exec.LookPath("gcloud")
	}
	if runtime.GOOS == "linux" {
		fmt.Println(bazel.RunfilesDir())
		return filepath.Join(bazel.RunfilesDir(), "..", "+google_cloud_sdk_ext+google_cloud_sdk_ext", "google-cloud-sdk", "bin", "gcloud"), nil
	}
	return "", skerr.Fmt("unsupported runtime.GOOS: %q", runtime.GOOS)
}

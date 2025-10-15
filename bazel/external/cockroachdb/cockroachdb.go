package cockroachdb

import (
	"os/exec"
	"path/filepath"
	"runtime"

	"go.goldmine.build/bazel/go/bazel"
	"go.goldmine.build/go/skerr"
)

// FindCockroach returns the path to the `cockroach` binary downloaded by Bazel.
//
// Calling this function from any Go package will automatically establish a Bazel dependency on the
// corresponding external Bazel repository.
func FindCockroach() (string, error) {
	if !bazel.InBazel() {
		return exec.LookPath("cockroach")
	}
	if runtime.GOOS == "linux" {
		return filepath.Join(bazel.RunfilesDir(), "..", "+cockroachdb_cli_ext+cockroachdb_cli_ext", "cockroachdb", "cockroach"), nil
	}
	return "", skerr.Fmt("unsupported runtime.GOOS: %q", runtime.GOOS)
}

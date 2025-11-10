package repo_root

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"go.goldmine.build/bazel/go/bazel"
	"go.goldmine.build/go/git/git_common"
	"go.goldmine.build/go/skerr"
)

// Get returns the path to the workspace's root directory.
//
// Under Bazel, it returns the path to the runfiles directory. Test targets must
// include any required files under their "data" attribute for said files to be
// included in the runfiles directory.
//
// Outside of Bazel, it returns the path to the repo checkout's root directory.
// Note that this will return an error if the CWD is not inside a checkout, so
// this cannot run on production servers.
func Get() (string, error) {
	ctx := context.Background()
	if bazel.InBazelTest() {
		return bazel.RunfilesDir(), nil
	}

	// Find the path to the git executable, which might be relative to working dir.
	gitFullPath, _, _, err := git_common.FindGit(ctx)
	if err != nil {
		return "", skerr.Wrapf(err, "Failed to find git.")
	}

	// Force the path to be absolute.
	gitFullPath, err = filepath.Abs(gitFullPath)
	if err != nil {
		return "", skerr.Wrapf(err, "Failed to get absolute path to git.")
	}

	cmd := exec.CommandContext(ctx, gitFullPath, "rev-parse", "--show-toplevel")
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", skerr.Wrapf(err, "No repo root found; are we running inside a checkout?: %s - %s", err, string(b))
	}
	return strings.TrimSpace(string(b)), nil
}

// GetLocal returns the path to the root of the current Git repo. Only intended
// to be run on a developer machine, inside a Git checkout.
func GetLocal() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", skerr.Wrap(err)
	}
	for {
		gitDir := filepath.Join(cwd, ".git")
		if _, err := os.Stat(gitDir); err == nil {
			return cwd, nil
		}
		newCwd, err := filepath.Abs(filepath.Join(cwd, ".."))
		if err != nil {
			return "", skerr.Wrap(err)
		}
		if newCwd == cwd {
			return "", skerr.Fmt("No repo root found up to %s; are we running inside a checkout?", cwd)
		}
		cwd = newCwd
	}
}

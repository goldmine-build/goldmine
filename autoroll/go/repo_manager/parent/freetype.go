package parent

import (
	"bytes"
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"go.skia.org/infra/autoroll/go/config_vars"
	"go.skia.org/infra/autoroll/go/repo_manager/common/gitiles_common"
	"go.skia.org/infra/autoroll/go/repo_manager/common/version_file_common"
	"go.skia.org/infra/autoroll/go/revision"
	"go.skia.org/infra/go/depot_tools/deps_parser"
	"go.skia.org/infra/go/git"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/util"
)

const (
	FtReadmePath         = "third_party/freetype/README.chromium"
	FtReadmeVersionTmpl  = "%sVersion: %s"
	FtReadmeRevisionTmpl = "%sRevision: %s"

	FtIncludeSrc  = "include/freetype/config"
	FtIncludeDest = "third_party/freetype/include/freetype-custom-config"
)

var (
	FtReadmeVersionRegex  = regexp.MustCompile(fmt.Sprintf(FtReadmeVersionTmpl, "(?m)^", ".*"))
	FtReadmeRevisionRegex = regexp.MustCompile(fmt.Sprintf(FtReadmeRevisionTmpl, "(?m)^", ".*"))

	FtIncludesToMerge = []string{
		"ftoption.h",
		"ftconfig.h",
	}
)

func NewFreeTypeParent(ctx context.Context, c GitilesDEPSConfig, reg *config_vars.Registry, workdir string, client *http.Client, serverURL string) (*gitilesParent, error) {
	getLastRollRev := gitilesFileGetLastRollRevFunc(version_file_common.VersionFileConfig{
		ID:   c.Dep,
		Path: deps_parser.DepsFileName,
	})

	localChildRepo, err := git.NewRepo(ctx, c.ChildRepo, workdir)
	if err != nil {
		return nil, err
	}
	getChangesHelper := gitilesFileGetChangesForRollFunc(version_file_common.DependencyConfig{
		VersionFileConfig: version_file_common.VersionFileConfig{
			ID:   c.Dep,
			Path: deps_parser.DepsFileName,
		},
		TransitiveDeps: c.TransitiveDeps,
	})
	getChangesForRoll := func(ctx context.Context, parentRepo *gitiles_common.GitilesRepo, baseCommit string, from, to *revision.Revision, rolling []*revision.Revision) (map[string]string, []*version_file_common.TransitiveDepUpdate, error) {
		// Get the DEPS changes via gitilesDEPSGetChangesForRollFunc.
		changes, transitiveDeps, err := getChangesHelper(ctx, parentRepo, baseCommit, from, to, rolling)
		if err != nil {
			return nil, nil, skerr.Wrap(err)
		}

		// Update README.chromium.
		if err := localChildRepo.Update(ctx); err != nil {
			return nil, nil, skerr.Wrap(err)
		}
		ftVersion, err := localChildRepo.Git(ctx, "describe", "--long", to.Id)
		if err != nil {
			return nil, nil, skerr.Wrap(err)
		}
		ftVersion = strings.TrimSpace(ftVersion)
		var buf bytes.Buffer
		if err := parentRepo.ReadFileAtRef(ctx, FtReadmePath, baseCommit, &buf); err != nil {
			return nil, nil, skerr.Wrap(err)
		}
		oldReadmeContents := buf.String()
		newReadmeContents := FtReadmeVersionRegex.ReplaceAllString(oldReadmeContents, fmt.Sprintf(FtReadmeVersionTmpl, "", ftVersion))
		newReadmeContents = FtReadmeRevisionRegex.ReplaceAllString(newReadmeContents, fmt.Sprintf(FtReadmeRevisionTmpl, "", to.Id))
		if newReadmeContents != oldReadmeContents {
			changes[FtReadmePath] = newReadmeContents
		}

		// Merge includes.
		for _, include := range FtIncludesToMerge {
			if err := mergeInclude(ctx, include, from.Id, to.Id, baseCommit, changes, parentRepo, localChildRepo); err != nil {
				return nil, nil, skerr.Wrap(err)
			}
		}

		// Check modules.cfg. Give up if it has changed.
		diff, err := localChildRepo.Git(ctx, "diff", "--name-only", git.LogFromTo(from.Id, to.Id))
		if err != nil {
			return nil, nil, err
		}
		if strings.Contains(diff, "modules.cfg") {
			return nil, nil, skerr.Fmt("modules.cfg has been modified; cannot roll automatically.")
		}
		return changes, transitiveDeps, nil
	}
	return newGitiles(ctx, c.GitilesConfig, reg, client, serverURL, getLastRollRev, getChangesForRoll)
}

// Perform a three-way merge for this header file in a temporary dir. Adds the
// new contents to the changes map.
func mergeInclude(ctx context.Context, include, from, to, baseCommit string, changes map[string]string, parentRepo *gitiles_common.GitilesRepo, localChildRepo *git.Repo) error {
	wd, err := ioutil.TempDir("", "")
	if err != nil {
		return err
	}
	defer util.RemoveAll(wd)

	gd := git.GitDir(wd)
	_, err = gd.Git(ctx, "init")

	// Obtain the current version of the file in the parent repo.
	parentHeader := path.Join(FtIncludeDest, include)
	dest := filepath.Join(wd, include)
	var buf bytes.Buffer
	if err := parentRepo.ReadFileAtRef(ctx, parentHeader, baseCommit, &buf); err != nil {
		return err
	}
	oldParentContents := buf.String()
	if err != nil {
		return err
	}
	if err := ioutil.WriteFile(dest, buf.Bytes(), os.ModePerm); err != nil {
		return err
	}
	if _, err := gd.Git(ctx, "add", dest); err != nil {
		return err
	}
	if _, err := gd.Git(ctx, "commit", "-m", "fake"); err != nil {
		return err
	}

	// Obtain the old version of the file in the child repo.
	ftHeader := path.Join(FtIncludeSrc, include)
	oldChildContents, err := localChildRepo.GetFile(ctx, ftHeader, from)
	if err != nil {
		return err
	}
	oldPath := filepath.Join(wd, "old")
	if err := ioutil.WriteFile(oldPath, []byte(oldChildContents), os.ModePerm); err != nil {
		return err
	}

	// Obtain the new version of the file in the child repo.
	newChildContents, err := localChildRepo.GetFile(ctx, ftHeader, to)
	if err != nil {
		return err
	}
	newPath := filepath.Join(wd, "new")
	if err := ioutil.WriteFile(newPath, []byte(newChildContents), os.ModePerm); err != nil {
		return err
	}

	// Perform the merge.
	if _, err := gd.Git(ctx, "merge-file", dest, oldPath, newPath); err != nil {
		return err
	}

	// Read the resulting contents.
	newParentContents, err := ioutil.ReadFile(dest)
	if err != nil {
		return err
	}
	if string(newParentContents) != string(oldParentContents) {
		changes[parentHeader] = string(newParentContents)
	}
	return nil
}

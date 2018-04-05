package repo_manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"go.skia.org/infra/go/depot_tools"
	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/gcs"
	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"google.golang.org/api/option"
)

const (
	FUCHSIA_SDK_GS_BUCKET = "fuchsia"
	FUCHSIA_SDK_GS_PATH   = "sdk"

	FUCHSIA_SDK_VERSION_FILE_PATH = "build/fuchsia/sdk.sha1"

	FUCHSIA_SDK_COMMIT_MSG_TMPL = `Roll Fuchsia SDK from %s to %s

` + COMMIT_MSG_FOOTER_TMPL
)

var (
	NewFuchsiaSDKRepoManager func(context.Context, *FuchsiaSDKRepoManagerConfig, string, *gerrit.Gerrit, string, string, *http.Client) (RepoManager, error) = newFuchsiaSDKRepoManager
)

// fuchsiaSDKVersion corresponds to one version of the Fuchsia SDK.
type fuchsiaSDKVersion struct {
	Timestamp time.Time
	Version   string
}

// Return true iff this fuchsiaSDKVersion is newer than the other.
func (a *fuchsiaSDKVersion) Greater(b *fuchsiaSDKVersion) bool {
	return a.Timestamp.After(b.Timestamp)
}

type fuchsiaSDKVersionSlice []*fuchsiaSDKVersion

func (s fuchsiaSDKVersionSlice) Len() int {
	return len(s)
}

func (s fuchsiaSDKVersionSlice) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

// We sort newest to oldest.
func (s fuchsiaSDKVersionSlice) Less(i, j int) bool {
	return s[i].Greater(s[j])
}

// Shorten the Fuchsia SDK version hash.
func fuchsiaSDKShortVersion(long string) string {
	return long[:12]
}

// FuchsiaSDKRepoManagerConfig provides configuration for the Fuchia SDK
// RepoManager.
type FuchsiaSDKRepoManagerConfig struct {
	DepotToolsRepoManagerConfig
}

// Validate the config.
func (c *FuchsiaSDKRepoManagerConfig) Validate() error {
	if c.Strategy != ROLL_STRATEGY_FUCHSIA_SDK {
		return errors.New("No custom strategy allowed for Fuchsia SDK RepoManager.")
	}
	return c.DepotToolsRepoManagerConfig.Validate()
}

// fuchsiaSDKRepoManager is a RepoManager which rolls the Fuchsia SDK version
// into Chromium. Unlike other rollers, there is no child repo to sync; the
// version number is obtained from Google Cloud Storage.
type fuchsiaSDKRepoManager struct {
	*depotToolsRepoManager
	commitsNotRolled int // Protected by infoMtx.
	gcs              gcs.GCSClient
	gsPath           string
	lastRollRev      *fuchsiaSDKVersion // Protected by infoMtx.
	nextRollRev      *fuchsiaSDKVersion // Protected by infoMtx.
	versionFile      string
	versions         []*fuchsiaSDKVersion // Protected by infoMtx.
}

// Return a fuchsiaSDKRepoManager instance.
func newFuchsiaSDKRepoManager(ctx context.Context, c *FuchsiaSDKRepoManagerConfig, workdir string, g *gerrit.Gerrit, recipeCfgFile, serverURL string, authClient *http.Client) (RepoManager, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	storageClient, err := storage.NewClient(ctx, option.WithHTTPClient(authClient))
	if err != nil {
		return nil, err
	}

	drm, err := newDepotToolsRepoManager(ctx, c.DepotToolsRepoManagerConfig, path.Join(workdir, "repo_manager"), recipeCfgFile, serverURL, g)
	if err != nil {
		return nil, err
	}
	rv := &fuchsiaSDKRepoManager{
		depotToolsRepoManager: drm,
		gcs:         gcs.NewGCSClient(storageClient, FUCHSIA_SDK_GS_BUCKET),
		gsPath:      FUCHSIA_SDK_GS_PATH,
		versionFile: path.Join(drm.parentDir, FUCHSIA_SDK_VERSION_FILE_PATH),
	}
	return rv, rv.Update(ctx)
}

// See documentation for RepoManager interface.
func (rm *fuchsiaSDKRepoManager) CreateNewRoll(ctx context.Context, from, to string, emails []string, cqExtraTrybots string, dryRun bool) (int64, error) {
	rm.repoMtx.Lock()
	defer rm.repoMtx.Unlock()

	// Clean the checkout, get onto a fresh branch.
	if err := rm.cleanParent(ctx); err != nil {
		return 0, err
	}
	if _, err := exec.RunCwd(ctx, rm.parentDir, "git", "checkout", "-b", ROLL_BRANCH, "-t", fmt.Sprintf("origin/%s", rm.parentBranch), "-f"); err != nil {
		return 0, err
	}

	// Defer some more cleanup.
	defer func() {
		util.LogErr(rm.cleanParent(ctx))
	}()

	// Create the roll CL.
	if _, err := exec.RunCwd(ctx, rm.parentDir, "git", "config", "user.name", rm.user); err != nil {
		return 0, err
	}
	if _, err := exec.RunCwd(ctx, rm.parentDir, "git", "config", "user.email", rm.user); err != nil {
		return 0, err
	}

	// Write the file.
	if err := ioutil.WriteFile(rm.versionFile, []byte(to), os.ModePerm); err != nil {
		return 0, err
	}

	// Commit.
	commitMsg := fmt.Sprintf(FUCHSIA_SDK_COMMIT_MSG_TMPL, fuchsiaSDKShortVersion(from), fuchsiaSDKShortVersion(to), rm.serverURL)
	if _, err := exec.RunCommand(ctx, &exec.Command{
		Dir:  rm.parentDir,
		Env:  depot_tools.Env(rm.depotTools),
		Name: "git",
		Args: []string{"commit", "-a", "-m", commitMsg},
	}); err != nil {
		return 0, err
	}

	// Upload the CL.
	uploadCmd := &exec.Command{
		Dir:     rm.parentDir,
		Env:     depot_tools.Env(rm.depotTools),
		Name:    "git",
		Args:    []string{"cl", "upload", "--bypass-hooks", "-f", "-v", "-v"},
		Timeout: 2 * time.Minute,
	}
	if dryRun {
		uploadCmd.Args = append(uploadCmd.Args, "--cq-dry-run")
	} else {
		uploadCmd.Args = append(uploadCmd.Args, "--use-commit-queue")
	}
	uploadCmd.Args = append(uploadCmd.Args, "--gerrit")
	tbr := "\nTBR="
	if emails != nil && len(emails) > 0 {
		emailStr := strings.Join(emails, ",")
		tbr += emailStr
		uploadCmd.Args = append(uploadCmd.Args, "--send-mail", "--cc", emailStr)
	}
	commitMsg += tbr
	uploadCmd.Args = append(uploadCmd.Args, "-m", commitMsg)

	// Upload the CL.
	sklog.Infof("Running command: git %s", strings.Join(uploadCmd.Args, " "))
	if _, err := exec.RunCommand(ctx, uploadCmd); err != nil {
		return 0, err
	}

	// Obtain the issue number.
	tmp, err := ioutil.TempDir("", "")
	if err != nil {
		return 0, err
	}
	defer util.RemoveAll(tmp)
	jsonFile := path.Join(tmp, "issue.json")
	if _, err := exec.RunCommand(ctx, &exec.Command{
		Dir:  rm.parentDir,
		Env:  depot_tools.Env(rm.depotTools),
		Name: "git",
		Args: []string{"cl", "issue", fmt.Sprintf("--json=%s", jsonFile)},
	}); err != nil {
		return 0, err
	}
	f, err := os.Open(jsonFile)
	if err != nil {
		return 0, err
	}
	var issue issueJson
	if err := json.NewDecoder(f).Decode(&issue); err != nil {
		return 0, err
	}
	return issue.Issue, nil
}

// See documentation for RepoManager interface.
func (rm *fuchsiaSDKRepoManager) Update(ctx context.Context) error {
	// Sync the projects.
	rm.repoMtx.Lock()
	defer rm.repoMtx.Unlock()

	if err := rm.createAndSyncParent(ctx); err != nil {
		return fmt.Errorf("Could not create and sync parent repo: %s", err)
	}

	// Read the file to determine the last roll rev.
	lastRollRevBytes, err := ioutil.ReadFile(rm.versionFile)
	if err != nil {
		return err
	}
	lastRollRevStr := strings.TrimSpace(string(lastRollRevBytes))

	// Get the available SDK versions.
	availableVersions := []*fuchsiaSDKVersion{}
	if err := rm.gcs.AllFilesInDirectory(ctx, rm.gsPath, func(item *storage.ObjectAttrs) {
		vSplit := strings.Split(item.Name, "/")
		availableVersions = append(availableVersions, &fuchsiaSDKVersion{
			Timestamp: item.Updated,
			Version:   vSplit[len(vSplit)-1],
		})
	}); err != nil {
		return err
	}
	if len(availableVersions) == 0 {
		return fmt.Errorf("No matching items found.")
	}
	sort.Sort(fuchsiaSDKVersionSlice(availableVersions))

	// Get the next roll rev.
	nextRollRev := availableVersions[0]

	// Find the last roll rev in the list of available versions.
	lastIdx := -1
	for idx, v := range availableVersions {
		if v.Version == lastRollRevStr {
			lastIdx = idx
		}
	}
	if lastIdx == -1 {
		return fmt.Errorf("Last roll rev %q not found in available versions. Not-rolled count will be wrong.", lastRollRevStr)
	}

	rm.infoMtx.Lock()
	defer rm.infoMtx.Unlock()
	rm.lastRollRev = availableVersions[lastIdx]
	rm.nextRollRev = nextRollRev
	// Versions are in reverse chronological order, so the next roll rev is
	// the first in the list. Therefore the index of the last roll rev is
	// the same as the number of revs we have not yet rolled.
	rm.commitsNotRolled = lastIdx
	rm.versions = availableVersions
	return nil
}

// See documentation for RepoManager interface.
func (rm *fuchsiaSDKRepoManager) FullChildHash(ctx context.Context, ver string) (string, error) {
	rm.infoMtx.RLock()
	defer rm.infoMtx.RUnlock()
	for _, v := range rm.versions {
		if strings.HasPrefix(v.Version, ver) {
			return v.Version, nil
		}
	}
	return "", fmt.Errorf("Unable to find version: %s", ver)
}

// See documentation for RepoManager interface.
func (r *fuchsiaSDKRepoManager) LastRollRev() string {
	r.infoMtx.RLock()
	defer r.infoMtx.RUnlock()
	return r.lastRollRev.Version
}

// See documentation for RepoManager interface.
func (r *fuchsiaSDKRepoManager) NextRollRev() string {
	r.infoMtx.RLock()
	defer r.infoMtx.RUnlock()
	return r.nextRollRev.Version
}

// See documentation for RepoManager interface.
func (rm *fuchsiaSDKRepoManager) RolledPast(ctx context.Context, ver string) (bool, error) {
	// TODO(borenet): Use a map?
	var testVer *fuchsiaSDKVersion
	for _, v := range rm.versions {
		if v.Version == ver {
			testVer = v
		}
	}
	if testVer == nil {
		return false, fmt.Errorf("Unknown version: %s", ver)
	}
	rm.infoMtx.RLock()
	defer rm.infoMtx.RUnlock()
	return !testVer.Greater(rm.lastRollRev), nil
}

// See documentation for RepoManager interface.
func (rm *fuchsiaSDKRepoManager) CommitsNotRolled() int {
	rm.infoMtx.RLock()
	defer rm.infoMtx.RUnlock()
	return rm.commitsNotRolled
}

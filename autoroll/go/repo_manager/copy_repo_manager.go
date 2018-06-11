package repo_manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"go.skia.org/infra/go/depot_tools"
	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/git"
	"go.skia.org/infra/go/issues"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
)

const (
	COPY_COMMIT_MSG = `Roll %s %s (%d commits)

%s/+log/%s

%s

%s
`
	COPY_VERSION_HASH_FILE = "version.sha1"
)

var (
	// Use this function to instantiate a RepoManager. This is able to be
	// overridden for testing.
	NewCopyRepoManager func(context.Context, *CopyRepoManagerConfig, string, *gerrit.Gerrit, string, string) (RepoManager, error) = newCopyRepoManager
)

// CopyRepoManagerConfig provides configuration for the copy
// RepoManager.
type CopyRepoManagerConfig struct {
	DepotToolsRepoManagerConfig

	// ChildRepo is the URL of the child repo.
	ChildRepo string `json:"childRepo"`

	// Optional fields.

	// Whitelist indicates which files and directories to copy from the
	// child repo into the parent repo. If not specified, the whole repo
	// is copied.
	Whitelist []string `json:"whitelist"`
}

// Validate the config.
func (c *CopyRepoManagerConfig) Validate() error {
	if c.ChildRepo == "" {
		return fmt.Errorf("ChildRepo is required.")
	}
	return c.DepotToolsRepoManagerConfig.Validate()
}

type copyRepoManager struct {
	*depotToolsRepoManager
	childRepoUrl string
	includeLog   bool
	versionFile  string
	whitelist    []string
}

// newCopyRepoManager returns a RepoManager instance which rolls a dependency
// which is copied directly into a subdir of the parent repo.
func newCopyRepoManager(ctx context.Context, c *CopyRepoManagerConfig, workdir string, g *gerrit.Gerrit, recipeCfgFile, serverURL string) (RepoManager, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	drm, err := newDepotToolsRepoManager(ctx, c.DepotToolsRepoManagerConfig, path.Join(workdir, "repo_manager"), recipeCfgFile, serverURL, g)
	if err != nil {
		return nil, err
	}
	childRepo, err := git.NewCheckout(ctx, c.ChildRepo, workdir)
	if err != nil {
		return nil, err
	}
	drm.childRepo = childRepo
	rm := &copyRepoManager{
		depotToolsRepoManager: drm,
		childRepoUrl:          c.ChildRepo,
		includeLog:            true, // TODO(borenet): Consider adding IncludeLog to the config.
		versionFile:           path.Join(drm.childDir, COPY_VERSION_HASH_FILE),
		whitelist:             c.Whitelist,
	}
	return rm, nil
}

// See documentation for RepoManager interface.
func (rm *copyRepoManager) Update(ctx context.Context) error {
	// Sync the projects.
	rm.repoMtx.Lock()
	defer rm.repoMtx.Unlock()
	if err := rm.createAndSyncParent(ctx); err != nil {
		return fmt.Errorf("Could not create and sync parent repo: %s", err)
	}

	// In this type of repo manager, the child repo is managed separately
	// from the parent.
	if err := rm.childRepo.Update(ctx); err != nil {
		return fmt.Errorf("Failed to update child repo: %s", err)
	}

	// Get the last roll revision.
	lastRollRevBytes, err := ioutil.ReadFile(rm.versionFile)
	if err != nil {
		return fmt.Errorf("Failed to read %s: %s", rm.versionFile, err)
	}
	lastRollRev := strings.TrimSpace(string(lastRollRevBytes))

	// Find the number of not-rolled child repo commits.
	notRolled, err := rm.getCommitsNotRolled(ctx, lastRollRev)
	if err != nil {
		return err
	}

	// Get the next roll revision.
	nextRollRev, err := rm.getNextRollRev(ctx, notRolled, lastRollRev)
	if err != nil {
		return err
	}

	rm.infoMtx.Lock()
	defer rm.infoMtx.Unlock()
	rm.lastRollRev = lastRollRev
	rm.nextRollRev = nextRollRev
	rm.commitsNotRolled = len(notRolled)
	return nil
}

// See documentation for RepoManager interface.
func (rm *copyRepoManager) CreateNewRoll(ctx context.Context, from, to string, emails []string, cqExtraTrybots string, dryRun bool) (int64, error) {
	rm.repoMtx.Lock()
	defer rm.repoMtx.Unlock()

	// Clean the checkout, get onto a fresh branch.
	if err := rm.cleanParent(ctx); err != nil {
		return 0, err
	}
	parentRepo := git.GitDir(rm.parentDir)
	if _, err := parentRepo.Git(ctx, "checkout", "-b", ROLL_BRANCH, "-t", fmt.Sprintf("origin/%s", rm.parentBranch), "-f"); err != nil {
		return 0, err
	}

	// Defer some more cleanup.
	defer func() {
		util.LogErr(rm.cleanParent(ctx))
	}()

	// List the revisions in the roll.
	commits, err := rm.childRepo.RevList(ctx, fmt.Sprintf("%s..%s", from, to))
	if err != nil {
		return 0, fmt.Errorf("Failed to list revisions: %s", err)
	}

	if _, err := parentRepo.Git(ctx, "config", "user.name", rm.user); err != nil {
		return 0, err
	}
	if _, err := parentRepo.Git(ctx, "config", "user.email", rm.user); err != nil {
		return 0, err
	}

	// Find relevant bugs.
	bugs := []string{}
	monorailProject := issues.REPO_PROJECT_MAPPING[rm.parentRepo]
	if monorailProject == "" {
		sklog.Warningf("Found no entry in issues.REPO_PROJECT_MAPPING for %q", rm.parentRepo)
	} else {
		for _, c := range commits {
			d, err := rm.childRepo.Details(ctx, c)
			if err != nil {
				return 0, fmt.Errorf("Failed to obtain commit details: %s", err)
			}
			b := util.BugsFromCommitMsg(d.Body)
			for _, bug := range b[monorailProject] {
				bugs = append(bugs, fmt.Sprintf("%s:%s", monorailProject, bug))
			}
		}
	}

	// Roll the dependency.
	if _, err := rm.childRepo.Git(ctx, "reset", "--hard", to); err != nil {
		return 0, err
	}
	childFullPath := path.Join(rm.workdir, rm.childPath)
	childRelPath, err := filepath.Rel(parentRepo.Dir(), childFullPath)
	if err != nil {
		return 0, err
	}
	if _, err := parentRepo.Git(ctx, "rm", "-r", childRelPath); err != nil {
		return 0, err
	}
	if err := os.MkdirAll(path.Dir(childFullPath), os.ModePerm); err != nil {
		return 0, err
	}
	if len(rm.whitelist) > 0 {
		for _, w := range rm.whitelist {
			src := path.Join(rm.childRepo.Dir(), w)
			dst := path.Join(childFullPath, w)
			dstDir := path.Dir(dst)
			if err := os.MkdirAll(dstDir, os.ModePerm); err != nil {
				return 0, err
			}
			if _, err := exec.RunCwd(ctx, rm.workdir, "cp", "-rT", src, dst); err != nil {
				return 0, err
			}
		}
	} else {
		if _, err := exec.RunCwd(ctx, rm.workdir, "cp", "-rT", rm.childRepo.Dir(), childFullPath); err != nil {
			return 0, err
		}
	}
	if err := os.RemoveAll(path.Join(childFullPath, ".git")); err != nil {
		return 0, err
	}
	if err := ioutil.WriteFile(rm.versionFile, []byte(to), os.ModePerm); err != nil {
		return 0, err
	}
	if _, err := parentRepo.Git(ctx, "add", childFullPath); err != nil {
		return 0, err
	}

	// Get list of changes.
	changeSummaryBlob := ""
	if rm.includeLog {
		changeSummaries := []string{}
		for _, c := range commits {
			d, err := rm.childRepo.Details(ctx, c)
			if err != nil {
				return 0, err
			}
			changeSummary := fmt.Sprintf("%s %s %s", d.Timestamp.Format("2006-01-02"), AUTHOR_EMAIL_RE.FindStringSubmatch(d.Author)[1], d.Subject)
			changeSummaries = append(changeSummaries, changeSummary)
		}
		changeSummaryBlob = strings.Join(changeSummaries, "\n")
	}

	// Build the commit message.
	commitRange := fmt.Sprintf("%s..%s", from[:12], to[:12])
	commitMsg := fmt.Sprintf(COPY_COMMIT_MSG, rm.childPath, commitRange, len(commits), rm.childRepoUrl, commitRange, changeSummaryBlob, fmt.Sprintf(COMMIT_MSG_FOOTER_TMPL, rm.serverURL))
	if cqExtraTrybots != "" {
		commitMsg += "\n" + fmt.Sprintf(TMPL_CQ_INCLUDE_TRYBOTS, cqExtraTrybots)
	}
	if _, err := parentRepo.Git(ctx, "commit", "-a", "-m", commitMsg); err != nil {
		return 0, err
	}

	// Run the pre-upload steps.
	for _, s := range rm.PreUploadSteps() {
		if err := s(ctx, rm.parentDir); err != nil {
			return 0, fmt.Errorf("Failed pre-upload step: %s", err)
		}
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

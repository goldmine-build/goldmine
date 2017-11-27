package repo_manager

import (
	"context"
	"fmt"
	"os"
	"os/user"
	"path"
	"regexp"
	"strings"

	"go.skia.org/infra/go/git"
	"go.skia.org/infra/go/sklog"

	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/util"
)

const (
	SERVICE_ACCOUNT      = "31977622648@project.gserviceaccount.com"
	UPSTREAM_REMOTE_NAME = "remote"
	REPO_BRANCH_NAME     = "merge"
)

var (
	// Use this function to instantiate a NewAndroidRepoManager. This is able to be
	// overridden for testing.
	NewAndroidRepoManager func(context.Context, string, string, string, string, gerrit.GerritInterface, NextRollStrategy, []string, string) (RepoManager, error) = newAndroidRepoManager

	IGNORE_MERGE_CONFLICT_FILES = []string{"include/config/SkUserConfig.h"}

	FILES_GENERATED_BY_GN_TO_GP = []string{"include/config/SkUserConfig.h", "Android.bp"}

	AUTHOR_EMAIL_RE = regexp.MustCompile(".* \\((.*)\\)")
)

// androidRepoManager is a struct used by Android AutoRoller for managing checkouts.
type androidRepoManager struct {
	*commonRepoManager
	repoUrl                 string
	repoToolPath            string
	gitCookieAuthDaemonPath string
	authDaemonRunning       bool
}

func newAndroidRepoManager(ctx context.Context, workdir, parentBranch, childPath, childBranch string, g gerrit.GerritInterface, strategy NextRollStrategy, preUploadStepNames []string, serverURL string) (RepoManager, error) {
	user, err := user.Current()
	if err != nil {
		return nil, err
	}
	repoToolPath := path.Join(user.HomeDir, "bin", "repo")
	gitCookieAuthDaemonPath := path.Join(user.HomeDir, "gcompute-tools", "git-cookie-authdaemon")
	wd := path.Join(workdir, "android_repo")
	childDir := path.Join(wd, childPath)
	childRepo := &git.Checkout{GitDir: git.GitDir(childDir)}

	preUploadSteps, err := GetPreUploadSteps(preUploadStepNames)
	if err != nil {
		return nil, err
	}

	r := &androidRepoManager{
		commonRepoManager: &commonRepoManager{
			parentBranch:   parentBranch,
			childDir:       childDir,
			childPath:      childPath,
			childRepo:      childRepo,
			childBranch:    childBranch,
			preUploadSteps: preUploadSteps,
			serverURL:      serverURL,
			strategy:       strategy,
			user:           SERVICE_ACCOUNT,
			workdir:        wd,
			g:              g,
		},
		repoUrl:                 g.GetRepoUrl(),
		repoToolPath:            repoToolPath,
		gitCookieAuthDaemonPath: gitCookieAuthDaemonPath,
		authDaemonRunning:       false,
	}

	// TODO(borenet): This update can be extremely expensive. Consider
	// moving it out of the startup critical path.
	return r, r.Update(ctx)
}

// Update syncs code in the relevant repositories.
func (r *androidRepoManager) Update(ctx context.Context) error {
	// Sync the projects.
	r.repoMtx.Lock()
	defer r.repoMtx.Unlock()

	if !r.authDaemonRunning {
		go func() {
			r.authDaemonRunning = true
			// Authenticate before trying to update repo.
			if _, err := exec.RunCwd(ctx, r.childDir, r.gitCookieAuthDaemonPath); err != nil {
				util.LogErr(err)
			}
			r.authDaemonRunning = false
		}()
	}

	// Create the working directory if needed.
	if _, err := os.Stat(r.workdir); err != nil {
		if err := os.MkdirAll(r.workdir, 0755); err != nil {
			return err
		}
	}

	// Run repo init and sync commands.
	if _, err := exec.RunCwd(ctx, r.workdir, r.repoToolPath, "init", "-u", fmt.Sprintf("%s/a/platform/manifest", r.repoUrl), "-g", "all,-notdefault,-darwin", "-b", r.parentBranch); err != nil {
		return err
	}
	// Sync only the child path and the repohooks directory (needed to upload changes).
	if _, err := exec.RunCwd(ctx, r.workdir, r.repoToolPath, "sync", r.childPath, "tools/repohooks", "-j32"); err != nil {
		return err
	}

	// Fix the review config to a URL which will work outside prod.
	if _, err := exec.RunCwd(ctx, r.childRepo.Dir(), "git", "config", "remote.goog.review", fmt.Sprintf("%s/", r.repoUrl)); err != nil {
		return err
	}

	// Check to see whether there is an upstream yet.
	remoteOutput, err := exec.RunCwd(ctx, r.childRepo.Dir(), "git", "remote", "show")
	if err != nil {
		return err
	}
	if !strings.Contains(remoteOutput, UPSTREAM_REMOTE_NAME) {
		if _, err := exec.RunCwd(ctx, r.childRepo.Dir(), "git", "remote", "add", UPSTREAM_REMOTE_NAME, common.REPO_SKIA); err != nil {
			return err
		}
	}

	// Get the last roll revision.
	lastRollRev, err := r.getLastRollRev(ctx)
	if err != nil {
		return err
	}

	// Get the next roll revision.
	nextRollRev, err := r.strategy.GetNextRollRev(ctx, r.childRepo, lastRollRev)
	if err != nil {
		return err
	}

	// Find the number of not-rolled child repo commits.
	notRolled, err := r.GetCommitsNotRolled(ctx, lastRollRev)
	if err != nil {
		return err
	}

	r.infoMtx.Lock()
	defer r.infoMtx.Unlock()
	r.lastRollRev = lastRollRev
	r.nextRollRev = nextRollRev
	r.commitsNotRolled = notRolled
	return nil
}

// getLastRollRev returns the commit hash of the last-completed DEPS roll.
func (r *androidRepoManager) getLastRollRev(ctx context.Context) (string, error) {
	output, err := exec.RunCwd(ctx, r.childRepo.Dir(), "git", "merge-base", fmt.Sprintf("refs/remotes/remote/%s", r.childBranch), fmt.Sprintf("refs/remotes/goog/%s", r.parentBranch))
	if err != nil {
		return "", err
	}
	return strings.TrimRight(output, "\n"), nil
}

// FullChildHash returns the full hash of the given short hash or ref in the
// child repo.
func (r *androidRepoManager) FullChildHash(ctx context.Context, shortHash string) (string, error) {
	r.repoMtx.RLock()
	defer r.repoMtx.RUnlock()
	return r.childRepo.FullHash(ctx, shortHash)
}

// LastRollRev returns the last-rolled child commit.
func (r *androidRepoManager) LastRollRev() string {
	r.infoMtx.RLock()
	defer r.infoMtx.RUnlock()
	return r.lastRollRev
}

// RolledPast determines whether DEPS has rolled past the given commit.
func (r *androidRepoManager) RolledPast(ctx context.Context, hash string) (bool, error) {
	r.repoMtx.RLock()
	defer r.repoMtx.RUnlock()
	return r.childRepo.IsAncestor(ctx, hash, r.lastRollRev)
}

// NextRollRev returns the revision of the next roll.
func (r *androidRepoManager) NextRollRev() string {
	r.infoMtx.RLock()
	defer r.infoMtx.RUnlock()
	return r.nextRollRev
}

// abortMerge aborts the current merge in the child repo.
func (r *androidRepoManager) abortMerge(ctx context.Context) error {
	_, err := exec.RunCwd(ctx, r.childRepo.Dir(), "git", "merge", "--abort")
	return err
}

// abandonRepoBranch abandons the repo branch.
func (r *androidRepoManager) abandonRepoBranch(ctx context.Context) error {
	_, err := exec.RunCwd(ctx, r.childRepo.Dir(), r.repoToolPath, "abandon", REPO_BRANCH_NAME)
	return err
}

// getChangeNumForHash returns the corresponding change number for the provided commit hash by querying Gerrit's search API.
func (r *androidRepoManager) getChangeForHash(hash string) (*gerrit.ChangeInfo, error) {
	issues, err := r.g.Search(1, gerrit.SearchCommit(hash))
	if err != nil {
		return nil, err
	}
	return r.g.GetIssueProperties(issues[0].Issue)
}

// setTopic sets a topic using the name of the child repo and the change number.
// Example: skia_merge_1234
func (r *androidRepoManager) setTopic(changeNum int64) error {
	topic := fmt.Sprintf("%s_merge_%d", path.Base(r.childDir), changeNum)
	return r.g.SetTopic(topic, changeNum)
}

// setChangeLabels sets the appropriate labels on the Gerrit change.
// It uses the Gerrit REST API to set the following labels on the change:
// * Code-Review=2
// * Autosubmit=1 (if dryRun=false else 0 is set)
// * Presubmit-Ready=1
func (r *androidRepoManager) setChangeLabels(change *gerrit.ChangeInfo, dryRun bool) error {
	labelValues := map[string]interface{}{
		gerrit.CODEREVIEW_LABEL:      "2",
		gerrit.PRESUBMIT_READY_LABEL: "1",
	}
	if dryRun {
		labelValues[gerrit.AUTOSUBMIT_LABEL] = gerrit.AUTOSUBMIT_LABEL_NONE
	} else {
		labelValues[gerrit.AUTOSUBMIT_LABEL] = gerrit.AUTOSUBMIT_LABEL_SUBMIT
	}
	return r.g.SetReview(change, "Roller setting labels to auto-land change.", labelValues)
}

func ExtractBugNumbers(line string) map[string]bool {
	bugs := map[string]bool{}
	re := regexp.MustCompile("(?m)^(BUG|Bug) *[ :=] *b/([0-9]+) *$")
	out := re.FindAllStringSubmatch(line, -1)
	for _, m := range out {
		bugs[m[2]] = true
	}
	return bugs
}

func ExtractTestLines(line string) []string {
	testLines := []string{}
	re := regexp.MustCompile("(?m)^Test: *(.*) *$")
	out := re.FindAllString(line, -1)
	for _, m := range out {
		testLines = append(testLines, m)
	}
	return testLines
}

// CreateNewRoll creates and uploads a new Android roll to the given commit.
// Returns the change number of the uploaded roll.
func (r *androidRepoManager) CreateNewRoll(ctx context.Context, from, to string, emails []string, cqExtraTrybots string, dryRun bool) (int64, error) {
	r.repoMtx.Lock()
	defer r.repoMtx.Unlock()

	// Update the upstream remote.
	if _, err := exec.RunCwd(ctx, r.childDir, "git", "fetch", UPSTREAM_REMOTE_NAME); err != nil {
		return 0, err
	}

	// Create the roll CL.

	cr := r.childRepo
	commits, err := cr.RevList(ctx, fmt.Sprintf("%s..%s", from, to))
	if err != nil {
		return 0, fmt.Errorf("Failed to list revisions: %s", err)
	}

	// Start the merge.

	if _, err := exec.RunCwd(ctx, r.childDir, "git", "merge", to, "--no-commit"); err != nil {
		// Check to see if this was a merge conflict with IGNORE_MERGE_CONFLICT_FILES.
		conflictsOutput, conflictsErr := exec.RunCwd(ctx, r.childDir, "git", "diff", "--name-only", "--diff-filter=U")
		if conflictsErr != nil || conflictsOutput == "" {
			util.LogErr(conflictsErr)
			return 0, fmt.Errorf("Failed to roll to %s. Needs human investigation: %s", to, err)
		}
		for _, conflict := range strings.Split(conflictsOutput, "\n") {
			if conflict == "" {
				continue
			}
			ignoreConflict := false
			for _, ignore := range IGNORE_MERGE_CONFLICT_FILES {
				if conflict == ignore {
					ignoreConflict = true
					sklog.Infof("Ignoring conflict in %s", conflict)
					break
				}
			}
			if !ignoreConflict {
				util.LogErr(r.abortMerge(ctx))
				return 0, fmt.Errorf("Failed to roll to %s. Conflicts in %s: %s", to, conflictsOutput, err)
			}
		}
	}

	// Install GN.
	if _, syncErr := exec.RunCwd(ctx, r.childDir, "./bin/sync"); syncErr != nil {
		// Sync may return errors, but this is ok.
	}
	if _, fetchGNErr := exec.RunCwd(ctx, r.childDir, "./bin/fetch-gn"); fetchGNErr != nil {
		return 0, fmt.Errorf("Failed to install GN: %s", fetchGNErr)
	}

	// Generate and add files created by gn/gn_to_bp.py
	gnEnv := []string{fmt.Sprintf("PATH=%s/:%s", path.Join(r.childDir, "bin"), os.Getenv("PATH"))}
	_, gnToBpErr := exec.RunCommand(ctx, &exec.Command{
		Env:  gnEnv,
		Dir:  r.childDir,
		Name: "python",
		Args: []string{"-c", "from gn import gn_to_bp"},
	})
	if gnToBpErr != nil {
		util.LogErr(r.abortMerge(ctx))
		return 0, fmt.Errorf("Failed to run gn_to_bp: %s", gnToBpErr)
	}
	for _, genFile := range FILES_GENERATED_BY_GN_TO_GP {
		if _, err := exec.RunCwd(ctx, r.childDir, "git", "add", genFile); err != nil {
			return 0, err
		}
	}

	// Run the pre-upload steps.
	for _, s := range r.PreUploadSteps() {
		if err := s(ctx, r.workdir); err != nil {
			return 0, fmt.Errorf("Failed pre-upload step: %s", err)
		}
	}

	// Create a new repo branch.
	if _, repoBranchErr := exec.RunCwd(ctx, r.childDir, r.repoToolPath, "start", REPO_BRANCH_NAME, "."); repoBranchErr != nil {
		util.LogErr(r.abortMerge(ctx))
		return 0, fmt.Errorf("Failed to create repo branch: %s", repoBranchErr)
	}

	// Get list of changes.
	changeSummaries := []string{}
	for _, c := range commits {
		d, err := cr.Details(ctx, c)
		if err != nil {
			return 0, err
		}
		changeSummary := fmt.Sprintf("%s %s %s", d.Timestamp.Format("2006-01-02"), AUTHOR_EMAIL_RE.FindStringSubmatch(d.Author)[1], d.Subject)
		changeSummaries = append(changeSummaries, changeSummary)
	}

	// Create commit message.
	commitRange := fmt.Sprintf("%s..%s", from[:9], to[:9])
	childRepoName := path.Base(r.childDir)
	commitMsg := fmt.Sprintf(
		`Roll %s %s (%d commits)

https://%s.googlesource.com/%s.git/+log/%s

%s

%s

Test: Presubmit checks will test this change.
Exempt-From-Owner-Approval: The autoroll bot does not require owner approval.
`, r.childPath, commitRange, len(commits), childRepoName, childRepoName, commitRange, strings.Join(changeSummaries, "\n"), fmt.Sprintf(COMMIT_MSG_FOOTER_TMPL, r.serverURL))

	// Loop through all commits:
	// * Collect all bugs from b/xyz to add the commit message later.
	// * Add all 'Test: ' lines to the commit message.
	emailMap := map[string]bool{}
	bugMap := map[string]bool{}
	for _, c := range commits {
		d, err := cr.Details(ctx, c)
		if err != nil {
			return 0, err
		}
		// Extract out the email if it is a Googler.
		matches := AUTHOR_EMAIL_RE.FindStringSubmatch(d.Author)
		if strings.HasSuffix(matches[1], "@google.com") {
			emailMap[matches[1]] = true
		}
		// Extract out any bugs
		for k, v := range ExtractBugNumbers(d.Body) {
			bugMap[k] = v
		}
		// Extract out the Test lines and directly add them to the commit
		// message.
		for _, tl := range ExtractTestLines(d.Body) {
			commitMsg += fmt.Sprintf("\n%s", tl)
		}

	}
	// Create a single bug line and append it to the commit message.
	if len(bugMap) > 0 {
		bugs := []string{}
		for b := range bugMap {
			bugs = append(bugs, b)
		}
		commitMsg += fmt.Sprintf("\nBug: %s", strings.Join(bugs, ", "))
	}

	if r.parentBranch != "master" {
		// If the parent branch is not master then:
		// Add all authors of merged changes to the email list. We do not do this
		// for the master branch because developers would get spammed due to multiple
		// rolls a day. Release branch rolls run rarely and developers should be
		// aware that their changes are being rolled there.
		for e := range emailMap {
			emails = append(emails, e)
		}
	}
	emailStr := strings.Join(emails, ",")

	// Commit the change with the above message.
	if _, commitErr := exec.RunCwd(ctx, r.childDir, "git", "commit", "-m", commitMsg); commitErr != nil {
		util.LogErr(r.abandonRepoBranch(ctx))
		return 0, fmt.Errorf("Nothing to merge; did someone already merge %s?: %s", commitRange, commitErr)
	}

	// Bypass the repo upload prompt by setting autoupload config to true.
	if _, configErr := exec.RunCwd(ctx, r.childDir, "git", "config", fmt.Sprintf("review.%s/.autoupload", r.repoUrl), "true"); configErr != nil {
		util.LogErr(r.abandonRepoBranch(ctx))
		return 0, fmt.Errorf("Could not set autoupload config: %s", configErr)
	}

	// Upload the CL to Gerrit.
	uploadCommand := &exec.Command{
		Name: r.repoToolPath,
		Args: []string{"upload", fmt.Sprintf("--re=%s", emailStr), "--verify"},
		Dir:  r.childDir,
		// The below is to bypass the blocking
		// "ATTENTION: You are uploading an unusually high number of commits."
		// prompt which shows up when a merge contains more than 5 commits.
		Stdin: strings.NewReader("yes"),
	}
	if _, uploadErr := exec.RunCommand(ctx, uploadCommand); uploadErr != nil {
		util.LogErr(r.abandonRepoBranch(ctx))
		return 0, fmt.Errorf("Could not upload to Gerrit: %s", uploadErr)
	}

	// Get latest hash to find Gerrit change number with.
	commitHashOutput, revParseErr := exec.RunCwd(ctx, r.childDir, "git", "rev-parse", "HEAD")
	if revParseErr != nil {
		util.LogErr(r.abandonRepoBranch(ctx))
		return 0, revParseErr
	}
	commitHash := strings.Split(commitHashOutput, "\n")[0]
	// We no longer need the local branch. Abandon the repo.
	util.LogErr(r.abandonRepoBranch(ctx))

	// Get the change number.
	change, err := r.getChangeForHash(commitHash)
	if err != nil {
		util.LogErr(r.abandonRepoBranch(ctx))
		return 0, err
	}
	// Set the topic of the merge change.
	if err := r.setTopic(change.Issue); err != nil {
		return 0, err
	}
	// Set labels.
	if err := r.setChangeLabels(change, dryRun); err != nil {
		// Only throw exception here if parentBranch is master. This is
		// because other branches will not have permissions setup for the
		// bot to run CR+2.
		if r.parentBranch != "master" {
			sklog.Warningf("Could not set labels on %d: %s", change.Issue, err)
			sklog.Warningf("Not throwing error because %s branch is not master", r.parentBranch)
		} else {
			return 0, err
		}
	}

	return change.Issue, nil
}

func (r *androidRepoManager) User() string {
	return r.user
}

func (r *androidRepoManager) GetCommitsNotRolled(ctx context.Context, lastRollRev string) (int, error) {
	output, err := r.childRepo.Git(ctx, "ls-remote", UPSTREAM_REMOTE_NAME, fmt.Sprintf("refs/heads/%s", r.childBranch), "-1")
	if err != nil {
		return -1, err
	}
	head := strings.Split(output, "\t")[0]
	notRolled := 0
	if head != lastRollRev {
		commits, err := r.childRepo.RevList(ctx, fmt.Sprintf("%s..%s", lastRollRev, head))
		if err != nil {
			return -1, err
		}
		notRolled = len(commits)
	}
	return notRolled, nil
}

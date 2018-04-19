package roller

import (
	"context"
	"fmt"
	"time"

	github_api "github.com/google/go-github/github"

	"go.skia.org/infra/autoroll/go/recent_rolls"
	"go.skia.org/infra/autoroll/go/repo_manager"
	"go.skia.org/infra/autoroll/go/state_machine"
	"go.skia.org/infra/go/autoroll"
	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/github"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/travisci"
)

type RollImpl interface {
	state_machine.RollCLImpl

	// Insert the roll into the DB.
	InsertIntoDB() error
}

func retrieveGerritIssue(ctx context.Context, g *gerrit.Gerrit, rm repo_manager.RepoManager, rollIntoAndroid bool, issueNum int64) (*gerrit.ChangeInfo, *autoroll.AutoRollIssue, error) {
	info, err := g.GetIssueProperties(issueNum)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to get issue properties: %s", err)
	}
	a, err := autoroll.FromGerritChangeInfo(info, func(h string) (string, error) {
		return rm.FullChildHash(ctx, h)
	}, rollIntoAndroid)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to convert issue format: %s", err)
	}
	tryResults, err := autoroll.GetTryResultsFromGerrit(g, a)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to retrieve try results: %s", err)
	}
	a.TryResults = tryResults
	return info, a, nil
}

// gerritRoll is an implementation of RollImpl.
type gerritRoll struct {
	ci           *gerrit.ChangeInfo
	issue        *autoroll.AutoRollIssue
	g            *gerrit.Gerrit
	recent       *recent_rolls.RecentRolls
	retrieveRoll func(context.Context, int64) (*gerrit.ChangeInfo, *autoroll.AutoRollIssue, error)
	result       string
	rm           repo_manager.RepoManager
}

// newGerritRoll obtains a gerritRoll instance from the given Gerrit issue
// number.
func newGerritRoll(ctx context.Context, g *gerrit.Gerrit, rm repo_manager.RepoManager, recent *recent_rolls.RecentRolls, issueNum int64) (RollImpl, error) {
	ci, issue, err := retrieveGerritIssue(ctx, g, rm, false, issueNum)
	if err != nil {
		return nil, err
	}
	return &gerritRoll{
		ci:     ci,
		issue:  issue,
		g:      g,
		recent: recent,
		retrieveRoll: func(ctx context.Context, issueNum int64) (*gerrit.ChangeInfo, *autoroll.AutoRollIssue, error) {
			return retrieveGerritIssue(ctx, g, rm, false, issueNum)
		},
		rm: rm,
	}, nil
}

// See documentation for RollImpl interface.
func (r *gerritRoll) InsertIntoDB() error {
	return r.recent.Add(r.issue)
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) AddComment(msg string) error {
	return r.g.AddComment(r.ci, msg)
}

// Helper function for modifying a roll CL which might fail due to the CL being
// closed by a human or some other process, in which case we don't want to error
// out.
func (r *gerritRoll) withModify(ctx context.Context, action string, fn func() error) error {
	if err := fn(); err != nil {
		// It's possible that somebody abandoned the CL (or the CL
		// landed) while we were working. If that's the case, log an
		// error and move on.
		if err2 := r.Update(ctx); err2 != nil {
			return fmt.Errorf("Failed to %s with error:\n%s\nAnd failed to update it with error:\n%s", action, err, err2)
		}
		if r.ci.IsClosed() {
			sklog.Errorf("Attempted to %s but it is already closed! Error: %s", action, err)
			return nil
		}
		return err
	}
	return r.Update(ctx)
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) Close(ctx context.Context, result, msg string) error {
	sklog.Infof("Closing issue %d (result %q) with message: %s", r.ci.Issue, result, msg)
	r.result = result
	return r.withModify(ctx, "close the CL", func() error {
		return r.g.Abandon(r.ci, msg)
	})
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) IsFinished() bool {
	return r.ci.IsClosed() || !r.issue.CommitQueue
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) IsSuccess() bool {
	return r.ci.Status == gerrit.CHANGE_STATUS_MERGED
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) IsDryRunFinished() bool {
	// The CQ removes the CQ+1 label when the dry run finishes, regardless
	// of success or failure. Since we uploaded with the dry run label set,
	// we know the roll is in progress if the label is still set, and done
	// otherwise.
	return !r.issue.CommitQueueDryRun
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) IsDryRunSuccess() bool {
	return r.issue.AllTrybotsSucceeded()
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) RollingTo() string {
	return r.issue.RollingTo
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) SwitchToDryRun(ctx context.Context) error {
	return r.withModify(ctx, "switch the CL to dry run", func() error {
		return r.g.SendToDryRun(r.ci, "Mode was changed to dry run")
	})
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) SwitchToNormal(ctx context.Context) error {
	return r.withModify(ctx, "switch the CL out of dry run", func() error {
		return r.g.SendToCQ(r.ci, "Mode was changed to normal")
	})
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) RetryCQ(ctx context.Context) error {
	return r.withModify(ctx, "retry the CQ", func() error {
		return r.g.SendToCQ(r.ci, "CQ failed but there are no new commits. Retrying...")
	})
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) RetryDryRun(ctx context.Context) error {
	return r.withModify(ctx, "retry the CQ (dry run)", func() error {
		return r.g.SendToDryRun(r.ci, "Dry run failed but there are no new commits. Retrying...")
	})
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) Update(ctx context.Context) error {
	ci, issue, err := r.retrieveRoll(ctx, r.issue.Issue)
	if err != nil {
		return err
	}
	r.ci = ci
	if r.result != "" {
		issue.Result = r.result
	}
	r.issue = issue
	return r.recent.Update(r.issue)
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) IssueID() string {
	return fmt.Sprintf("%d", r.issue.Issue)
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritRoll) IssueURL() string {
	return fmt.Sprintf("%s%d", r.rm.GetIssueUrlBase(), r.issue.Issue)
}

// Special type for Android rolls.
type gerritAndroidRoll struct {
	*gerritRoll
}

// newGerritAndroidRoll obtains a gerritAndroidRoll instance from the given Gerrit issue number.
func newGerritAndroidRoll(ctx context.Context, g *gerrit.Gerrit, rm repo_manager.RepoManager, recent *recent_rolls.RecentRolls, issueNum int64) (RollImpl, error) {
	ci, issue, err := retrieveGerritIssue(ctx, g, rm, true, issueNum)
	if err != nil {
		return nil, err
	}
	return &gerritAndroidRoll{&gerritRoll{
		ci:     ci,
		issue:  issue,
		g:      g,
		recent: recent,
		retrieveRoll: func(ctx context.Context, issueNum int64) (*gerrit.ChangeInfo, *autoroll.AutoRollIssue, error) {
			return retrieveGerritIssue(ctx, g, rm, true, issueNum)
		},
		rm: rm,
	}}, nil
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritAndroidRoll) IsDryRunFinished() bool {
	if _, ok := r.ci.Labels[gerrit.PRESUBMIT_VERIFIED_LABEL]; ok {
		for _, lb := range r.ci.Labels[gerrit.PRESUBMIT_VERIFIED_LABEL].All {
			if lb.Value != gerrit.PRESUBMIT_VERIFIED_LABEL_RUNNING {
				return true
			}
		}
	}
	return false
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritAndroidRoll) IsDryRunSuccess() bool {
	presubmit, ok := r.ci.Labels[gerrit.PRESUBMIT_VERIFIED_LABEL]
	if !ok || len(presubmit.All) == 0 {
		// Not done yet.
		return false
	}
	for _, lb := range presubmit.All {
		if lb.Value == gerrit.PRESUBMIT_VERIFIED_LABEL_ACCEPTED {
			return true
		}
	}
	return false
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritAndroidRoll) SwitchToDryRun(ctx context.Context) error {
	return r.withModify(ctx, "switch the CL to dry run", func() error {
		return r.g.SetReview(r.ci, "Mode was changed to dry run", map[string]interface{}{gerrit.AUTOSUBMIT_LABEL: gerrit.AUTOSUBMIT_LABEL_NONE})
	})
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritAndroidRoll) SwitchToNormal(ctx context.Context) error {
	return r.withModify(ctx, "switch the CL out of dry run", func() error {
		return r.g.SetReview(r.ci, "Mode was changed to normal", map[string]interface{}{gerrit.AUTOSUBMIT_LABEL: gerrit.AUTOSUBMIT_LABEL_SUBMIT})
	})
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritAndroidRoll) RetryCQ(ctx context.Context) error {
	return r.withModify(ctx, "retry TH", func() error {
		return r.g.SetReview(r.ci, "TH failed but there are no new commits. Retrying...", map[string]interface{}{gerrit.PRESUBMIT_READY_LABEL: "1"})

	})
}

// See documentation for state_machine.RollCLImpl interface.
func (r *gerritAndroidRoll) RetryDryRun(ctx context.Context) error {
	return r.withModify(ctx, "retry the TH (dry run)", func() error {
		return r.g.SetReview(r.ci, "Dry run failed but there are no new commits. Retrying...", map[string]interface{}{gerrit.PRESUBMIT_READY_LABEL: "1"})
	})
}

// githubRoll is an implementation of RollImpl.
// TODO(rmistry): Add tests after a code-review abstraction later exists.
type githubRoll struct {
	pullRequest  *github_api.PullRequest
	issue        *autoroll.AutoRollIssue
	g            *github.GitHub
	t            *travisci.TravisCI
	recent       *recent_rolls.RecentRolls
	retrieveRoll func(context.Context, int64) (*github_api.PullRequest, *autoroll.AutoRollIssue, error)
	result       string
	rm           repo_manager.RepoManager
}

func retrieveGithubPullRequest(ctx context.Context, g *github.GitHub, t *travisci.TravisCI, rm repo_manager.RepoManager, issueNum int64) (*github_api.PullRequest, *autoroll.AutoRollIssue, error) {

	// Retrieve travis build if it exists.
	travisBuilds, err := t.GetPullRequestBuilds(int(issueNum), rm.User())
	if err != nil {
		return nil, nil, fmt.Errorf("Could not retrieve travis build details: %s", err)
	}

	// Retrieve the pull request from github.
	pullRequest, err := g.GetPullRequest(int(issueNum))
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to get pull request for %d: %s", issueNum, err)
	}
	a, err := autoroll.FromGitHubPullRequest(pullRequest, func(h string) (string, error) {
		return rm.FullChildHash(ctx, h)
	})
	if err != nil {
		return nil, nil, fmt.Errorf("Failed to convert issue format: %s", err)
	}

	tryResults := []*autoroll.TryResult{}
	for _, travisBuild := range travisBuilds {
		if travisBuild.Id != 0 {
			testStatus := autoroll.TRYBOT_STATUS_STARTED
			testResult := ""
			switch travisBuild.State {
			case travisci.BUILD_STATE_CREATED:
				// Build is not completely ready yet. It will not have a
				// startedAt yet.
				continue
			case travisci.BUILD_STATE_FAILED:
				testStatus = autoroll.TRYBOT_STATUS_COMPLETED
				testResult = autoroll.TRYBOT_RESULT_FAILURE
			case travisci.BUILD_STATE_PASSED:
				testStatus = autoroll.TRYBOT_STATUS_COMPLETED
				testResult = autoroll.TRYBOT_RESULT_SUCCESS
			}
			buildStartedAt, err := time.Parse(time.RFC3339, travisBuild.StartedAt)
			if err != nil {
				return nil, nil, fmt.Errorf("Failed to parse %s: %s", travisBuild.StartedAt, err)
			}
			tryResults = append(tryResults,
				&autoroll.TryResult{
					Builder:  fmt.Sprintf("TravisCI Build #%d", travisBuild.Id),
					Category: autoroll.TRYBOT_CATEGORY_CQ,
					Created:  buildStartedAt,
					Result:   testResult,
					Status:   testStatus,
					Url:      t.GetBuildURL(travisBuild.Id),
				})
		}
	}
	a.TryResults = tryResults

	return pullRequest, a, nil
}

// newGithubRoll obtains a githubRoll instance from the given Gerrit issue number.
func newGithubRoll(ctx context.Context, g *github.GitHub, rm repo_manager.RepoManager, recent *recent_rolls.RecentRolls, pullRequestNum int64) (RollImpl, error) {
	pullRequest, issue, err := retrieveGithubPullRequest(ctx, g, g.TravisCi, rm, pullRequestNum)
	if err != nil {
		return nil, err
	}
	return &githubRoll{
		pullRequest: pullRequest,
		issue:       issue,
		g:           g,
		t:           g.TravisCi,
		recent:      recent,
		retrieveRoll: func(ctx context.Context, pullRequestNum int64) (*github_api.PullRequest, *autoroll.AutoRollIssue, error) {
			return retrieveGithubPullRequest(ctx, g, g.TravisCi, rm, pullRequestNum)
		},
		rm: rm,
	}, nil
}

// See documentation for state_machine.RollImpl interface.
func (r *githubRoll) InsertIntoDB() error {
	return r.recent.Add(r.issue)
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) AddComment(msg string) error {
	return r.g.AddComment(r.pullRequest.GetNumber(), msg)
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) Close(ctx context.Context, result, msg string) error {
	sklog.Infof("Closing pull request %d (result %q) with message: %s", r.pullRequest.GetNumber(), result, msg)
	r.result = result
	return r.withModify(ctx, "close the pull request", func() error {
		if err := r.g.AddComment(r.pullRequest.GetNumber(), msg); err != nil {
			return err
		}
		_, err := r.g.ClosePullRequest(r.pullRequest.GetNumber())
		return err
	})
}

// Helper function for modifying a roll CL which might fail due to the CL being
// closed by a human or some other process, in which case we don't want to error
// out.
func (r *githubRoll) withModify(ctx context.Context, action string, fn func() error) error {
	if err := fn(); err != nil {
		// It's possible that somebody abandoned the CL (or the CL
		// landed) while we were working. If that's the case, log an
		// error and move on.
		if err2 := r.Update(ctx); err2 != nil {
			return fmt.Errorf("Failed to %s with error:\n%s\nAnd failed to update it with error:\n%s", action, err, err2)
		}
		if r.pullRequest.GetState() == github.CLOSED_STATE {
			sklog.Errorf("Attempted to %s but it is already closed! Error: %s", action, err)
			return nil
		}
		return err
	}
	return r.Update(ctx)
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) Update(ctx context.Context) error {
	pullRequest, issue, err := r.retrieveRoll(ctx, int64(r.pullRequest.GetNumber()))
	if err != nil {
		return err
	}
	r.pullRequest = pullRequest
	if r.result != "" {
		issue.Result = r.result
	}
	r.issue = issue
	return r.recent.Update(r.issue)
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) IsFinished() bool {
	//trybotsRanAndSucceeded := len(r.issue.TryResults) > 0 && r.issue.AllTrybotsSucceeded()
	return r.pullRequest.GetState() == github.CLOSED_STATE || r.pullRequest.GetMerged() || !r.issue.CommitQueue
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) IsSuccess() bool {
	return r.pullRequest.GetMerged()
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) IsDryRunFinished() bool {
	return len(r.issue.TryResults) > 0 && r.issue.AllTrybotsSucceeded()
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) IsDryRunSuccess() bool {
	return len(r.issue.TryResults) > 0 && r.issue.AllTrybotsSucceeded()
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) RollingTo() string {
	return r.issue.RollingTo
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) SwitchToDryRun(ctx context.Context) error {
	return nil
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) SwitchToNormal(ctx context.Context) error {
	return nil
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) RetryCQ(ctx context.Context) error {
	// TODO(rmistry): Is there a way to retrigger travisci? if there is then
	// do we want to?
	return nil
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) RetryDryRun(ctx context.Context) error {
	// TODO(rmistry): Is there a way to retrigger travisci? if there is then
	// do we want to?
	return nil
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) IssueID() string {
	return fmt.Sprintf("%d", r.issue.Issue)
}

// See documentation for state_machine.RollCLImpl interface.
func (r *githubRoll) IssueURL() string {
	return fmt.Sprintf("%s%d", r.rm.GetIssueUrlBase(), r.issue.Issue)
}

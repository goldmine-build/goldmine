package parent

import (
	"context"
	"fmt"

	"go.skia.org/infra/autoroll/go/codereview"
	"go.skia.org/infra/autoroll/go/repo_manager/common/gerrit_common"
	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/git"
	"go.skia.org/infra/go/skerr"
)

// GitCheckoutGerritConfig provides configuration for Parents which use a local
// git checkout and upload changes to Gerrit.
type GitCheckoutGerritConfig struct {
	GitCheckoutConfig
	Gerrit *codereview.GerritConfig `json:"gerrit"`
}

// See documentation for util.Validator interface.
func (c GitCheckoutGerritConfig) Validate() error {
	if err := c.GitCheckoutConfig.Validate(); err != nil {
		return skerr.Wrap(err)
	}
	if err := c.Gerrit.Validate(); err != nil {
		return skerr.Wrap(err)
	}
	return nil
}

// GitCheckoutUploadGerritRollFunc returns a GitCheckoutUploadRollFunc which
// uploads a CL to Gerrit.
func GitCheckoutUploadGerritRollFunc(g gerrit.GerritInterface) GitCheckoutUploadRollFunc {
	return func(ctx context.Context, co *git.Checkout, upstreamBranch, hash string, emails []string, dryRun bool, commitMsg string) (int64, error) {
		// Find the change ID in the commit message.
		out, err := co.Git(ctx, "log", "-n1", hash)
		if err != nil {
			return 0, skerr.Wrap(err)
		}
		changeId, err := gerrit.ParseChangeId(out)
		if err != nil {
			return 0, skerr.Wrapf(err, "Commit message:\n%s", out)
		}

		// Upload CL.
		if _, err := co.Git(ctx, "push", "origin", fmt.Sprintf("%s:refs/for/%s", hash, upstreamBranch)); err != nil {
			return 0, skerr.Wrap(err)
		}
		ci, err := g.GetChange(ctx, changeId)
		if err != nil {
			return 0, skerr.Wrap(err)
		}

		// TODO(borenet): We shouldn't assume that the commit has the correct
		// message; instead, we should edit the CL to use the passed-in
		// commitMsg.

		if err := gerrit_common.SetChangeLabels(ctx, g, ci, emails, dryRun); err != nil {
			return 0, skerr.Wrap(err)
		}

		return ci.Issue, nil
	}
}

// SetupGerrit performs additional setup for a GitCheckoutParent which uses
// Gerrit. This is required for all users of GitCheckoutUploadGerritRollFunc.
// TODO(borenet): This is needed for RepoManagers which use NewDEPSLocal, since
// they need to pass in a GitCheckoutUploadRollFunc but can't do other
// initialization. Find a way to make this unnecessary.
func SetupGerrit(ctx context.Context, p *GitCheckoutParent, g gerrit.GerritInterface) error {
	// Install the Gerrit Change-Id hook.
	if err := gerrit_common.DownloadCommitMsgHook(ctx, g, p.Checkout.Checkout); err != nil {
		return skerr.Wrap(err)
	}
	return nil
}

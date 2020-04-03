package gerrit_common

import (
	"context"
	"fmt"

	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/skerr"
)

// SetChangeLabels sets the necessary labels on the given change, marking it
// ready for review and starting the commit queue (or submitting the change
// outright, if there is no configured commit queue).
func SetChangeLabels(ctx context.Context, g gerrit.GerritInterface, ci *gerrit.ChangeInfo, emails []string, dryRun bool) error {
	// Mark the change as ready for review, if necessary.
	if err := UnsetWIP(ctx, g, ci, 0); err != nil {
		return skerr.Wrapf(err, "failed to unset WIP")
	}

	// Set the CQ bit as appropriate.
	labels := g.Config().SetCqLabels
	if dryRun {
		labels = g.Config().SetDryRunLabels
	}
	labels = gerrit.MergeLabels(labels, g.Config().SelfApproveLabels)
	if err := g.SetReview(ctx, ci, "", labels, emails); err != nil {
		// TODO(borenet): Should we try to abandon the CL?
		return skerr.Wrapf(err, "failed to set review")
	}

	// Manually submit if necessary.
	if !g.Config().HasCq {
		if err := g.Submit(ctx, ci); err != nil {
			// TODO(borenet): Should we try to abandon the CL?
			return skerr.Wrapf(err, "failed to submit")
		}
	}

	return nil
}

// UnsetWIP is a helper function for unsetting the WIP bit on a Gerrit CL if
// necessary. Either the change or issueNum parameter is required; if change is
// not  provided, it will be loaded from Gerrit. unsetWIP checks for a nil
// GerritInterface, so this is safe to call from RepoManagers which don't
// use Gerrit. If we fail to unset the WIP bit, unsetWIP abandons the change.
func UnsetWIP(ctx context.Context, g gerrit.GerritInterface, change *gerrit.ChangeInfo, issueNum int64) error {
	if g != nil {
		if change == nil {
			var err error
			change, err = g.GetIssueProperties(ctx, issueNum)
			if err != nil {
				return err
			}
		}
		if change.WorkInProgress {
			if err := g.SetReadyForReview(ctx, change); err != nil {
				if err2 := g.Abandon(ctx, change, "Failed to set ready for review."); err2 != nil {
					return fmt.Errorf("Failed to set ready for review with: %s\nand failed to abandon with: %s", err, err2)
				}
				return fmt.Errorf("Failed to set ready for review: %s", err)
			}
		}
	}
	return nil
}

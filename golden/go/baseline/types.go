package baseline

import (
	"go.skia.org/infra/golden/go/types/expectations"
)

// Baseline captures the data necessary to verify test results on the
// commit queue. A baseline is essentially just the positive expectations
// for a branch.
type Baseline struct {
	// MD5 is the hash of the Expectations field. Can be used to quickly test equality.
	MD5 string `json:"md5"`

	// Expectations captures the "baseline expectations", that is, the Expectations
	// with only the positive digests of the current commit.
	Expectations expectations.Baseline `json:"master"`

	// ChangeListID indicates the Gerrit or GitHub issue id of this baseline.
	// "" indicates the master branch.
	ChangeListID string `json:"cl_id,omitempty"`

	// CodeReviewSystem indicates which CRS system (if any) this baseline is tied to.
	// (e.g. "gerrit", "github") "" indicates the master branch.
	CodeReviewSystem string `json:"crs,omitempty"`
}

type BaselineFetcher interface {
	// FetchBaseline fetches a Baseline. If clID and crs are non-empty, the given ChangeList will
	// be created by loading the master baseline and the CL baseline and combining
	// them.
	// If issueOnly is true and clID/crs != "" then only the expectations attached to the CL are
	// returned (omitting the baselines of the master branch).
	// issueOnly is primarily used for debugging.
	FetchBaseline(clID, crs string, issueOnly bool) (*Baseline, error)
}

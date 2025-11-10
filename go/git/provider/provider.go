// Package provider contains types and interfaces for interacting with Git
// repos.
package provider

import (
	"context"
	"fmt"
	"time"

	"go.goldmine.build/go/human"
	"go.goldmine.build/perf/go/types"
)

// GitAuthType is the type of authentication Git should use, if any.
type GitAuthType string

const (
	// GitAuthNone implies no authentication is needed when cloning/pulling a
	// Git repo, i.e. it is public. The value is the empty string so that the
	// default is no authentication.
	GitAuthNone GitAuthType = ""

	// GitAuthGerrit is for repos that are hosted by Gerrit and require
	// authentication. This setting implies that a
	// GOOGLE_APPLICATION_CREDENTIALS environment variable will be set and the
	// associated service account has read access to the Gerrit repo.
	GitAuthGerrit GitAuthType = "gerrit"
)

// GitProvider is the method used to interrogate git repos.
type GitProvider string

const (
	// GitProviderCLI uses a local copy of git to checkout the repo.
	GitProviderCLI GitProvider = "git"

	// GitProviderGitiles uses the Gitiles API.
	GitProviderGitiles GitProvider = "gitiles"
)

// AllGitProviders is a slice of all valid GitProviders.
var AllGitProviders []GitProvider = []GitProvider{
	GitProviderCLI,
	GitProviderGitiles,
}

// Commit represents a single commit stored in the database.
//
// JSON annotations make it serialize like the legacy cid.CommitDetail.
type Commit struct {
	CommitNumber types.CommitNumber `json:"offset"`
	GitHash      string             `json:"hash"`
	Timestamp    int64              `json:"ts"` // Unix timestamp, seconds from the epoch.
	Author       string             `json:"author"`
	Subject      string             `json:"message"`
	URL          string             `json:"url"`
	Body         string             `json:"body"` // it's used to parse commit number, won't be insert into database.
}

// Display returns a display string that describes the commit.
func (c Commit) Display(now time.Time) string {
	return fmt.Sprintf("%s - %s - %s", c.GitHash[:7], human.Duration(now.Sub(time.Unix(c.Timestamp, 0))), c.Subject)
}

// HumanTime returns a display string that describes the commit time relative to
// the current time.
func (c Commit) HumanTime() string {
	return human.Duration(time.Since(time.Unix(c.Timestamp, 0)))
}

// CommitProcessor is a callback function that will be called with a Commit.
// Used in GitProvider.
type CommitProcessor func(c Commit) error

// Provider in abstraction of how we get information about a repo. This could
// be implemented by either Git or the Gitiles API.
type Provider interface {
	// CommitsFromMostRecentGitHashToHead will call the `cb` func with every
	// Commit, starting from the oldest and going to the newest. If
	// mostRecentGitHash is the empty string then the commits will start with
	// the very first commit to the repo, or from the start commit if one is
	// provided.
	CommitsFromMostRecentGitHashToHead(ctx context.Context, mostRecentGitHash string, cb CommitProcessor) error

	// GitHashesInRangeForFile returns all the git hashes when the given file
	// has changed between [begin, end], i.e. the given range is exclusive of
	// the begin commit and inclusive of the end commit. If 'begin' is the empty
	// string then the scan should go back to the initial commit of the repo.
	GitHashesInRangeForFile(ctx context.Context, begin, end, filename string) ([]string, error)

	// LogEntry returns the full log entry of a commit (minus the diff) as a string.
	LogEntry(ctx context.Context, gitHash string) (string, error)

	// Update does any necessary work, like a `git pull`, to ensure that the
	// GitProvider has the most recent commits available.
	Update(ctx context.Context) error
}

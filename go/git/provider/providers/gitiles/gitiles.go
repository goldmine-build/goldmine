// Package gitiles imlements provider.Provider using the Gitiles API.
package gitiles

import (
	"context"
	"fmt"
	"time"

	"go.goldmine.build/go/auth"
	"go.goldmine.build/go/git"
	"go.goldmine.build/go/git/provider"
	"go.goldmine.build/go/gitiles"
	"go.goldmine.build/go/httputils"
	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/go/vcsinfo"
	"golang.org/x/oauth2/google"
)

const (
	batchSize = 100
)

// Gitiles implements provider.Provider.
type Gitiles struct {
	gr gitiles.GitilesRepo

	// startCommit is the commit in the repo where we start tracking commits. If
	// not supplied then we start with the first commit in the repo as reachable
	// from HEAD.
	startCommit string
}

// New returns a new instance of Gitiles.
func New(
	ctx context.Context,
	url string,
	branch string,
	startCommit string,
) (*Gitiles, error) {
	ts, err := google.DefaultTokenSource(ctx, auth.ScopeGerrit)
	c := httputils.DefaultClientConfig().WithTokenSource(ts).Client()
	if err != nil {
		return nil, skerr.Wrap(err)
	}

	return &Gitiles{
		gr:          gitiles.NewRepoWithBranch(url, branch, c),
		startCommit: startCommit,
	}, nil
}

// CommitsFromMostRecentGitHashToHead implements provider.Provider.
func (g *Gitiles) CommitsFromMostRecentGitHashToHead(ctx context.Context, mostRecentGitHash string, cb provider.CommitProcessor) error {
	if mostRecentGitHash == "" {
		mostRecentGitHash = g.startCommit
	}

	expr := git.LogFromTo(mostRecentGitHash, "HEAD")
	if mostRecentGitHash == "" {
		expr = git.MainBranch
	}

	sklog.Infof("Populating from gitiles from %q", expr)
	err := g.gr.LogFnBatch(ctx, expr, func(ctx context.Context, lcs []*vcsinfo.LongCommit) error {
		sklog.Infof("Processing %s commits: ", len(lcs))
		for _, longCommit := range lcs {
			c := provider.Commit{
				GitHash:   longCommit.Hash,
				Timestamp: longCommit.Timestamp.Unix(),
				Author:    longCommit.Author,
				Subject:   longCommit.Subject,
				Body:      longCommit.Body,
			}
			err := cb(c)
			if err != nil {
				return skerr.Wrapf(err, "processing callback")
			}
		}
		return nil
	}, gitiles.LogBatchSize(batchSize), gitiles.LogReverse())
	if err != nil {
		return skerr.Wrapf(err, "loading commits")
	}
	return nil
}

// GitHashesInRangeForFile implements provider.Provider.
func (g *Gitiles) GitHashesInRangeForFile(ctx context.Context, begin, end, filename string) ([]string, error) {

	lc, err := g.gr.Log(ctx, git.LogFromTo(begin, end), gitiles.LogPath(filename), gitiles.LogReverse())
	if err != nil {
		return nil, skerr.Wrapf(err, "loading commits")
	}
	ret := make([]string, len(lc))
	for i, c := range lc {
		ret[i] = c.Hash
	}
	return ret, nil
}

// LogEntry implements provider.Provider.
func (g *Gitiles) LogEntry(ctx context.Context, gitHash string) (string, error) {
	lc, err := g.gr.Log(ctx, gitHash, gitiles.LogLimit(1))
	if err != nil {
		return "", skerr.Wrapf(err, "loading log entry")
	}
	if len(lc) != 1 {
		return "", skerr.Fmt("received %d log entries when expecting 1", len(lc))
	}
	commit := lc[0]
	return fmt.Sprintf(`commit %s
Author %s
Date %s

%s

%s`, commit.Hash, commit.Author, commit.Timestamp.Format(time.RFC822Z), commit.Subject, commit.Body), nil
}

// Update implements provider.Provider.
func (g *Gitiles) Update(ctx context.Context) error {
	return nil
}

// Confirm *Gitiles implements provider.Provider.
var _ provider.Provider = (*Gitiles)(nil)

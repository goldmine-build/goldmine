package gitapi

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/bartventer/httpcache"
	_ "github.com/bartventer/httpcache/store/memcache" //  Register the in-memory backend
	"github.com/google/go-github/v80/github"
	"go.goldmine.build/go/git/provider"
	"go.goldmine.build/go/skerr"
	"go.goldmine.build/golden/go/config"
	"go.goldmine.build/perf/go/types"
)

type gitApi struct {
	client *github.Client
	owner  string
	repo   string
	branch string
}

func New(
	ctx context.Context,
	patPath string, // Path to file that contains a GitHub Personal Access Token (PAT).
	cfg config.Common,
) (*gitApi, error) {
	b, err := os.ReadFile(patPath)
	if err != nil {
		return nil, skerr.Wrap(err)
	}

	client := github.NewClient(
		httpcache.NewClient("memcache://"),
	).WithAuthToken(strings.TrimSpace(string(b)))

	if len(cfg.CodeReviewSystems) == 0 {
		return nil, skerr.Fmt("At least on CodeReviewSystem must be defined.")
	}
	githubRepo := cfg.CodeReviewSystems[0].GitHubRepo
	if strings.Count(githubRepo, "/") != 1 {
		return nil, skerr.Fmt("Invalid format for github_repo, expected a string in the form <owner>/<repo>, instead got: %q", githubRepo)
	}
	parts := strings.Split(githubRepo, "/")
	owner := parts[0]
	repo := parts[1]

	strings.Contains(githubRepo, "/")

	return &gitApi{
		client: client,
		owner:  owner,
		repo:   repo,
		branch: cfg.GitRepoBranch,
	}, nil
}

func (g *gitApi) timeStampForCommit(ctx context.Context, hash string) (time.Time, error) {
	commit, _, err := g.client.Repositories.GetCommit(ctx, g.owner, g.repo, hash, nil)
	if err != nil {
		return time.Now(), skerr.Wrap(err)
	}

	return commit.Committer.UpdatedAt.Time, nil
}

func (g *gitApi) CommitsFromMostRecentGitHashToHead(ctx context.Context, mostRecentGitHash string, cb provider.CommitProcessor) error {
	since, err := g.timeStampForCommit(ctx, mostRecentGitHash)
	if err != nil {
		return err
	}

	opt := &github.CommitsListOptions{
		SHA:   g.branch,
		Since: since,
	}

	commits, _, err := g.client.Repositories.ListCommits(ctx, g.owner, g.branch, opt)
	if err != nil {
		return skerr.Wrap(err)
	}

	// Commits are returned from newest to oldest, so we need to reverse the
	// list first.
	slices.Reverse(commits)
	for _, c := range commits {
		subject := ""
		body := ""

		if c.Commit.Message != nil {
			parts := strings.SplitN(*c.Commit.Message, "\n", 2)
			subject = parts[0]
			if len(parts) > 1 {
				body = parts[1]
			}
		}

		err := cb(provider.Commit{
			CommitNumber: types.BadCommitNumber,
			GitHash:      *c.SHA,
			Timestamp:    c.Commit.Committer.Date.Unix(),
			Author:       *c.Commit.Author.Email,
			Subject:      subject,
			URL:          *c.HTMLURL,
			Body:         body,
		})
		if err != nil {
			return skerr.Wrapf(err, "processing callback")
		}
	}
	return nil
}

// GitHashesInRangeForFile implements provider.Provider.
func (g *gitApi) GitHashesInRangeForFile(ctx context.Context, begin, end, filename string) ([]string, error) {
	since, err := g.timeStampForCommit(ctx, begin)
	if err != nil {
		return nil, err
	}
	until, err := g.timeStampForCommit(ctx, end)
	if err != nil {
		return nil, err
	}

	opt := &github.CommitsListOptions{
		SHA:   g.branch,
		Since: since,
		Until: until,
	}

	commits, _, err := g.client.Repositories.ListCommits(ctx, g.owner, g.branch, opt)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	slices.Reverse(commits)
	ret := []string{}

	for _, c := range commits {
		for _, f := range c.Files {
			if *f.Filename == filename {
				ret = append(ret, *c.SHA)
				break
			}
		}
	}

	return ret, nil
}

// LogEntry implements provider.Provider.
func (g *gitApi) LogEntry(ctx context.Context, gitHash string) (string, error) {

	commit, _, err := g.client.Repositories.GetCommit(ctx, g.owner, g.repo, gitHash, nil)
	if err != nil {
		return "", skerr.Wrap(err)
	}

	return fmt.Sprintf(`commit %s
Author %s
Date %s

%s
`, *commit.SHA, *commit.Commit.Author.Email, commit.Commit.Committer.Date.Format(time.RFC822Z), *commit.Commit.Message), nil
}

// Update implements provider.Provider.
func (g *gitApi) Update(ctx context.Context) error {
	return nil
}

// Confirm *Gitiles implements provider.Provider.
var _ provider.Provider = (*gitApi)(nil)

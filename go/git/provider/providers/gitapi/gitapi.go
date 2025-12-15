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
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/perf/go/types"
)

type GitApi struct {
	client *github.Client
	owner  string
	repo   string
	branch string
}

func New(
	ctx context.Context,
	patPath string, // Path to file that contains a GitHub Personal Access Token (PAT).
	owner string,
	repo string,
	branch string,
) (*GitApi, error) {
	authToken := ""
	if patPath != "" {
		b, err := os.ReadFile(patPath)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		authToken = strings.TrimSpace(string(b))
	}

	client := github.NewClient(
		httpcache.NewClient("memcache://"),
	)
	if authToken != "" {
		client = client.WithAuthToken(authToken)
	}

	return &GitApi{
		client: client,
		owner:  owner,
		repo:   repo,
		branch: branch,
	}, nil
}

func (g *GitApi) timeStampForCommit(ctx context.Context, hash string) (time.Time, error) {
	commit, _, err := g.client.Repositories.GetCommit(ctx, g.owner, g.repo, hash, nil)
	if err != nil {
		return time.Now(), skerr.Wrap(err)
	}

	return commit.Commit.Committer.Date.Time, nil
}

func (g *GitApi) CommitsFromMostRecentGitHashToHead(ctx context.Context, mostRecentGitHash string, cb provider.CommitProcessor) error {
	// This is also a test that the mostRecentGitHash is actually on the given
	// branch.
	since, err := g.timeStampForCommit(ctx, mostRecentGitHash)
	if err != nil {
		return err
	}
	since = since.Add(-time.Hour * 24)

	opt := &github.CommitsListOptions{
		SHA:   g.branch,
		Since: since,
		ListOptions: github.ListOptions{
			Page:    0,
			PerPage: 10,
		},
	}

	foundCommits := []provider.Commit{}
	for {
		commits, resp, err := g.client.Repositories.ListCommits(ctx, g.owner, g.repo, opt)
		if err != nil {
			return skerr.Wrap(err)
		}

		for _, c := range commits {
			if *c.SHA == mostRecentGitHash {
				sklog.Infof("=== Found mostRecentGitHash")
				goto foundMostRecentGitHash
			}

			subject := ""
			body := ""

			if c.Commit.Message != nil {
				parts := strings.SplitN(*c.Commit.Message, "\n", 2)
				subject = parts[0]
				if len(parts) > 1 {
					body = parts[1]
				}
			}

			foundCommits = append(foundCommits, provider.Commit{
				CommitNumber: types.BadCommitNumber,
				GitHash:      *c.SHA,
				Timestamp:    c.Commit.Committer.Date.Unix(),
				Author:       *c.Commit.Author.Email,
				Subject:      subject,
				URL:          *c.HTMLURL,
				Body:         body,
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opt.ListOptions.Page = resp.NextPage
	}

foundMostRecentGitHash:

	// Commits are returned from newest to oldest, so we need to reverse the
	// list first.
	slices.Reverse(foundCommits)

	// Now trigger the callback on each commit found.
	for _, c := range foundCommits {
		err := cb(c)
		if err != nil {
			return skerr.Wrapf(err, "processing callback")
		}
	}
	return nil
}

// GitHashesInRangeForFile implements provider.Provider.
func (g *GitApi) GitHashesInRangeForFile(ctx context.Context, begin, end, filename string) ([]string, error) {
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

	commits, _, err := g.client.Repositories.ListCommits(ctx, g.owner, g.repo, opt)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	sklog.Infof("Found %d commits in range", len(commits))

	slices.Reverse(commits)
	ret := []string{}
	for _, c := range commits {
		fullCommit, _, err := g.client.Repositories.GetCommit(ctx, g.owner, g.repo, *c.SHA, nil)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		for _, f := range fullCommit.Files {
			if *f.Filename == filename {
				sklog.Infof("Filename match: %q", *f.Filename)
				ret = append(ret, *c.SHA)
				break
			}
		}
	}

	return ret, nil
}

// LogEntry implements provider.Provider.
func (g *GitApi) LogEntry(ctx context.Context, gitHash string) (string, error) {
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
func (g *GitApi) Update(ctx context.Context) error {
	return nil
}

// Confirm *Gitiles implements provider.Provider.
var _ provider.Provider = (*GitApi)(nil)

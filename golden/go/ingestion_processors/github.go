package ingestion_processors

/*
// githubLookupSystem implements the LookupSystem interface for GitHub PRs.
type githubLookupSystem struct {
	crsClient *github_crs.CRSImpl
}

// NewGitHubLookupSystem returns a new instance of githubLookupSystem.
func NewGitHubLookupSystem(crsClient *github_crs.CRSImpl) *githubLookupSystem {
	return &githubLookupSystem{
		crsClient: crsClient,
	}
}

// Lookup implements the LookupSystem interface.
func (g *githubLookupSystem) Lookup(ctx context.Context, trybotJobID string) (string, string, int, error) {
	// The trybotJobID is expected to be in the format <PR#>-<COMMIT HASH>.
	parts := strings.SplitN(trybotJobID, "-", 2)
	if len(parts) != 2 {
		return "", "", 0, skerr.Fmt("invalid trybotJobID format: %s", trybotJobID)
	}
	clID := parts[0]
	psID := parts[1]

	// We don't have a reliable way to get psOrder from psID alone, so we set it to 0.
	psOrder := 0
	ps, err := g.crsClient.GetPatchset(ctx, clID, psID, psOrder)
	if err != nil {
		return "", "", 0, skerr.Wrapf(err, "looking up patchset for trybotJobID %s", trybotJobID)
	}
	return "github", clID, ps.Order, nil
}

*/

/*

To fetch GitHub PR data for a tryjob, first pull the PR info:

    curl -L   -H "Accept: application/vnd.github+json"   -H "X-GitHub-Api-Version: 2022-11-28"   https://api.github.com/repos/goldmine-build/goldmine/pulls/1 > pull.json

Note that we don't need authentication for this as the goldmine repo is public.

That will return JSON like this, note we elide a bunch:

    {
        "url": "https://api.github.com/repos/goldmine-build/goldmine/pulls/1",
        "id": 3054634226,
        "node_id": "PR_kwDOPxTrJs62EgTy",
        "html_url": "https://github.com/goldmine-build/goldmine/pull/1",
        "diff_url": "https://github.com/goldmine-build/goldmine/pull/1.diff",
        "patch_url": "https://github.com/goldmine-build/goldmine/pull/1.patch",
        "issue_url": "https://api.github.com/repos/goldmine-build/goldmine/issues/1",
        "number": 1,
        "state": "open",
        "locked": false,
        "title": "starting on adding github trybot support to Gold.",
        "user": {
            "login": "jcgregorio",
            "id": 1726460,
            "node_id": "MDQ6VXNlcjE3MjY0NjA=",
            "avatar_url": "https://avatars.githubusercontent.com/u/1726460?v=4",
            "gravatar_id": "",
            "url": "https://api.github.com/users/jcgregorio",
            "html_url": "https://github.com/jcgregorio",
             ...
            "type": "User",
            "user_view_type": "public",
            "site_admin": false
        },
        "body": null,
        "created_at": "2025-11-28T17:44:54Z",
        "updated_at": "2025-11-28T19:04:39Z",
        "closed_at": null,
        "merged_at": null,
        "merge_commit_sha": "a23fca2fc99206cbcabc36e7a875c09cc101dae6",
        "assignee": null,
        "assignees": [

        ],
        "requested_reviewers": [

        ],
        "requested_teams": [

        ],
        "labels": [

        ],
        "milestone": null,
        "draft": false,
        "commits_url": "https://api.github.com/repos/goldmine-build/goldmine/pulls/1/commits",
        "review_comments_url": "https://api.github.com/repos/goldmine-build/goldmine/pulls/1/comments",
        "review_comment_url": "https://api.github.com/repos/goldmine-build/goldmine/pulls/comments{/number}",
        "comments_url": "https://api.github.com/repos/goldmine-build/goldmine/issues/1/comments",
        "statuses_url": "https://api.github.com/repos/goldmine-build/goldmine/statuses/55ab257a68a8f523fb25bcfc30d9ed53a2d5edda",
        "head": {
            "label": "goldmine-build:github-trybot",
            "ref": "github-trybot",
            "sha": "55ab257a68a8f523fb25bcfc30d9ed53a2d5edda",
            "user": {
            "login": "goldmine-build",
            "id": 232705022,


To get the patchset number, we can fetch the full PR commits data:

    curl -L   -H "Accept: application/vnd.github+json"   -H "X-GitHub-Api-Version: 2022-11-28"   https://api.github.com/repos/goldmine-build/goldmine/pulls/1/commits > commits.json

Which will return JSON like this, again eliding a bunch. Note that the commits
are an array, and we need to find the one that matches the commit we ran the
tryjob at to get the patchset number (1-based index in the array). Note also we
can pull out the commit message, author, date, etc. from here as well.

[
    {},
    {},
    {},
    {
    "sha": "55ab257a68a8f523fb25bcfc30d9ed53a2d5edda",
    "node_id": "C_kwDOPxTrJtoAKDU1YWIyNTdhNjhhOGY1MjNmYjI1YmNmYzMwZDllZDUzYTJkNWVkZGE",
    "commit": {
        "author": {
        "name": "Joe Gregorio",
        "email": "joe@bitworking.org",
        "date": "2025-11-28T19:04:32Z"
        },
        "committer": {
        "name": "Joe Gregorio",
        "email": "joe@bitworking.org",
        "date": "2025-11-28T19:04:32Z"
        },
        "message": "Third or fourth",
        "tree": {
        "sha": "1948f9f02fb3c31c6011a0c2eeb9fa01590d826b",
        "url": "https://api.github.com/repos/goldmine-build/goldmine/git/trees/1948f9f02fb3c31c6011a0c2eeb9fa01590d826b"
        },
        "url": "https://api.github.com/repos/goldmine-build/goldmine/git/commits/55ab257a68a8f523fb25bcfc30d9ed53a2d5edda",
        "comment_count": 0,
        "verification": {
        "verified": false,
        "reason": "unsigned",
        "signature": null,
        "payload": null,
        "verified_at": null
        }
    },
    "url": "https://api.github.com/repos/goldmine-build/goldmine/commits/55ab257a68a8f523fb25bcfc30d9ed53a2d5edda",
    "html_url": "https://github.com/goldmine-build/goldmine/commit/55ab257a68a8f523fb25bcfc30d9ed53a2d5edda",
    "comments_url": "https://api.github.com/repos/goldmine-build/goldmine/commits/55ab257a68a8f523fb25bcfc30d9ed53a2d5edda/comments",
    "author": {
        "login": "jcgregorio",
        "id": 1726460,
        "node_id": "MDQ6VXNlcjE3MjY0NjA=",
        "avatar_url": "https://avatars.githubusercontent.com/u/1726460?v=4",
        "gravatar_id": "",
        "url": "https://api.github.com/users/jcgregorio",
        "html_url": "https://github.com/jcgregorio",
        "followers_url": "https://api.github.com/users/jcgregorio/followers",
        "following_url": "https://api.github.com/users/jcgregorio/following{/other_user}",
        "gists_url": "https://api.github.com/users/jcgregorio/gists{/gist_id}",
        "starred_url": "https://api.github.com/users/jcgregorio/starred{/owner}{/repo}",
        "subscriptions_url": "https://api.github.com/users/jcgregorio/subscriptions",
        "organizations_url": "https://api.github.com/users/jcgregorio/orgs",
        "repos_url": "https://api.github.com/users/jcgregorio/repos",
        "events_url": "https://api.github.com/users/jcgregorio/events{/privacy}",
        "received_events_url": "https://api.github.com/users/jcgregorio/received_events",
        "type": "User",
        "user_view_type": "public",
        "site_admin": false
    },
    "committer": {
        "login": "jcgregorio",
        "id": 1726460,
        "node_id": "MDQ6VXNlcjE3MjY0NjA=",
        "avatar_url": "https://avatars.githubusercontent.com/u/1726460?v=4",
        "gravatar_id": "",
        "url": "https://api.github.com/users/jcgregorio",
        "html_url": "https://github.com/jcgregorio",
        "followers_url": "https://api.github.com/users/jcgregorio/followers",
        "following_url": "https://api.github.com/users/jcgregorio/following{/other_user}",
        "gists_url": "https://api.github.com/users/jcgregorio/gists{/gist_id}",
        "starred_url": "https://api.github.com/users/jcgregorio/starred{/owner}{/repo}",
        "subscriptions_url": "https://api.github.com/users/jcgregorio/subscriptions",
        "organizations_url": "https://api.github.com/users/jcgregorio/orgs",
        "repos_url": "https://api.github.com/users/jcgregorio/repos",
        "events_url": "https://api.github.com/users/jcgregorio/events{/privacy}",
        "received_events_url": "https://api.github.com/users/jcgregorio/received_events",
        "type": "User",
        "user_view_type": "public",
        "site_admin": false
    },
    "parents": [
        {
        "sha": "b401674f88f0171a410076219f78ceca18a705ff",
        "url": "https://api.github.com/repos/goldmine-build/goldmine/commits/b401674f88f0171a410076219f78ceca18a705ff",
        "html_url": "https://github.com/goldmine-build/goldmine/commit/b401674f88f0171a410076219f78ceca18a705ff"
        }
    ]
    }
]

So during a workflow run we record the pull request number (1 in this case), and
the merge_commit_sha, supplied in the following environment variables:

    GITHUB_REF_NAME=1/merge
    GITHUB_SHA=a23fca2fc99206cbcabc36e7a875c09cc101dae6
    GITHUB_REPOSITORY=goldmine-build/goldmine

We can extract the pull request number like this:

    PULL_NUMBER=$(echo $GITHUB_REF_NAME | cut -d'/' -f1)

In the workflow we can then record GITHUB_SHA (the merge commit sha) and the
GITHUB_WORKFLOW.

    GITHUB_SHA=a23fca2fc99206cbcabc36e7a875c09cc101dae6
    GITHUB_WORKFLOW=Run all the UI tests and upload the results to Gold.

Note that GITHUB_SHA is the merge commit sha, so we actually want to record

    PR_PATCHES_PARENTS=git log --pretty=%P -n 1 $GITHUB_SHA

Note that will return 2 SHAs separated by a space in the case of a merge commit,
e.g.:

    Run git log --pretty=%P -n 1 $GITHUB_SHA
    5ae561f50219876d62d50f41b3d2f7c9c094106d 403e9b69508e292a06bc02be27cb5e4330a71c89

We need the second one in this case, which is the actual commit that was
introduced by the PR. We can get that with:

	PATCHSET_ID=$(git log --pretty=%P -n 1 $GITHUB_SHA | sed 's#.* ##')

// This is how we distinguish tryjob uploads from non-tryjob uploads.
GITHUB_REF_NAME=main # or 1/merge for PR 1

PULL_NUMBER=$(echo $GITHUB_REF_NAME | cut -d'/' -f1)
PATCHSET_ID=$(git log --pretty=%P -n 1 $GITHUB_SHA | sed 's#.* ##')

	--crs=github
	--cis=github
	--changelist=$PULL_NUMBER
	--patchset=0                # Using 0 here to indicate we are using patchset_id
	--patchset_id=$PATCHSET_ID
	--jobid=$PULL_NUMBER-$PATCHSET_ID # Add a lookupSystem that can map this to CL/PS.
*/

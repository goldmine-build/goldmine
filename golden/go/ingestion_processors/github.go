package ingestion_processors

/*

To fetch GitHub PR data for tryjobs, first pull the PR info:

    curl -L   -H "Accept: application/vnd.github+json"   -H "X-GitHub-Api-Version: 2022-11-28"   https://api.github.com/repos/goldmine-build/goldmine/pulls/1 > pull.json

Note that we don't need authentication for this as the goldmine repo is public.

That will return JSON like this, note we elide a bunch, what really matters is
the sha of head:

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

So `head.sha` is what we need to identify the commit for the tryjob in other API
calls. In this case `55ab257a68a8f523fb25bcfc30d9ed53a2d5edda`.


We also need to record `merge_commit_sha` which is the sha of the commit that
gets supplied to the workflow when it runs the tryjob and is supplied as
`GITHUB_SHA`. In this case that is `a23fca2fc99206cbcabc36e7a875c09cc101dae6`.

Then to get the patchset number, we can fetch the full PR commits data:

    curl -L   -H "Accept: application/vnd.github+json"   -H "X-GitHub-Api-Version: 2022-11-28"   https://api.github.com/repos/goldmine-build/goldmine/pulls/1/commits > commits.json

Which will return JSON like this, again eliding a bunch. Note that the commits
are an array, and we need to find the one that matches the head.sha from above
to get the patchset number (1-based index in the array). Note also we can pull
out the commit message, author, date, etc. from here as well.

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

So during a workflow run we record the pull request number (1 in this case),
and the merge_commit_sha, supplied in the following environment variables:

	GITHUB_REF_NAME=1/merge
	GITHUB_SHA=a23fca2fc99206cbcabc36e7a875c09cc101dae6
	GITHUB_REPOSITORY=goldmine-build/goldmine


	PULL_NUMBER=$(echo $GITHUB_REF_NAME | cut -d'/' -f1)

With the pull request number of `1` we can fetch the PR data to get

	https://api.github.com/repos/goldmine-build/goldmine/pulls/1

i.e.

	https://api.github.com/repos/$GITHUB_REPOSITORY/pulls/$PULL_NUMBER

Confirm that GITHUB_SHA matches merge_commit_sha in the PR data.
Then get head.sha from the PR data in this case `55ab257a68a8f523fb25bcfc30d9ed53a2d5edda`.

Then pull the commits data for the PR:

	https://api.github.com/repos/goldmine-build/goldmine/pulls/1/commits

i.e.

	https://api.github.com/repos/$GITHUB_REPOSITORY/pulls/$PULL_NUMBER/commits


Then find the commit in the array that matches head.sha to get the patchset number
(which is the 1-based index in the array).


In the workflow we can then record GITHUB_SHA (the merge commit sha) and the
GITHUB_WORKFLOW.


	GITHUB_SHA=a23fca2fc99206cbcabc36e7a875c09cc101dae6
	GITHUB_WORKFLOW=Run all the UI tests and upload the results to Gold.

Note that GITHUB_SHA is the merge commit sha, so we actually want to record

	PR_PATCHSET_SHA=$(git rev-parse HEAD^)

In the goldctl upload we then supply the following keys in the metadata:

  // These keys are required for tryjobs and can be omitted for non-tryjobs.
  // GitHub support coming soon, Gerrit/googlesource support only at the moment.
  issue: '1',  # Pull Request number
  patchset: '4', # Commit index in the PR commits array (1-based)

  github_merge_commit_sha: 'a23fca2fc99206cbcabc36e7a875c09cc101dae6',

  buildbucket_build_id: '0',


  // These keys are optional, but can assist in debugging
  builder: 'Test-Android-Clang-iPhone7-GPU-PowerVRGT7600-arm64-Debug-All-Metal',
  swarming_bot_id: 'skia-rpi-102',
  swarming_task_id: '3fcd8d4a539ba311',




*/

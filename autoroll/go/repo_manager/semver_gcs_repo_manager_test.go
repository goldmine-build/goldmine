package repo_manager

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/url"
	"path"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.skia.org/infra/autoroll/go/codereview"
	"go.skia.org/infra/autoroll/go/revision"
	"go.skia.org/infra/go/deepequal/assertdeep"
	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/git"
	git_testutils "go.skia.org/infra/go/git/testutils"
	gitiles_testutils "go.skia.org/infra/go/gitiles/testutils"
	"go.skia.org/infra/go/mockhttpclient"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/testutils/unittest"
	"go.skia.org/infra/go/util"
)

const (
	afdoRevPrev = "chromeos-chrome-amd64-66.0.3336.0_rc-r0-merged.afdo.bz2"
	afdoRevBase = "chromeos-chrome-amd64-66.0.3336.0_rc-r1-merged.afdo.bz2"
	afdoRevNext = "chromeos-chrome-amd64-66.0.3337.0_rc-r1-merged.afdo.bz2"

	afdoTimePrev = "2009-11-10T23:00:00Z"
	afdoTimeBase = "2009-11-10T23:01:00Z"
	afdoTimeNext = "2009-11-10T23:02:00Z"

	AFDO_GS_BUCKET = "chromeos-prebuilt"
	AFDO_GS_PATH   = "afdo-job/llvm"

	// Example name: chromeos-chrome-amd64-63.0.3239.57_rc-r1.afdo.bz2
	AFDO_VERSION_REGEX = ("^chromeos-chrome-amd64-" + // Prefix
		"(\\d+)\\.(\\d+)\\.(\\d+)\\.0" + // Version
		"_rc-r(\\d+)" + // Revision
		"-merged\\.afdo\\.bz2$") // Suffix
	AFDO_SHORT_REV_REGEX = "(\\d+)\\.(\\d+)\\.(\\d+)\\.0_rc-r(\\d+)-merged"

	AFDO_VERSION_FILE_PATH = "chrome/android/profiles/newest.txt"

	TMPL_COMMIT_MSG_AFDO = `Roll AFDO from {{.RollingFrom.String}} to {{.RollingTo.String}}

This CL may cause a small binary size increase, roughly proportional
to how long it's been since our last AFDO profile roll. For larger
increases (around or exceeding 100KB), please file a bug against
gbiv@chromium.org. Additional context: https://crbug.com/805539

Please note that, despite rolling to chrome/android, this profile is
used for both Linux and Android.

If this roll has caused a breakage, revert this CL and stop the roller
using the controls here:
{{.ServerURL}}
Please CC {{stringsJoin .Reviewers ","}} on the revert to ensure that a human
is aware of the problem.

To report a problem with the AutoRoller itself, please file a bug:
https://bugs.chromium.org/p/skia/issues/entry?template=Autoroller+Bug

Documentation for the AutoRoller is here:
https://skia.googlesource.com/buildbot/+/master/autoroll/README.md

Tbr: {{stringsJoin .Reviewers ","}}
`
)

func TestCompareSemanticVersions(t *testing.T) {
	unittest.SmallTest(t)

	test := func(a, b []int, expect int) {
		require.Equal(t, expect, compareSemanticVersions(a, b))
	}
	test([]int{}, []int{}, 0)
	test([]int{}, []int{1}, 1)
	test([]int{1}, []int{}, -1)
	test([]int{1}, []int{1}, 0)
	test([]int{0}, []int{1}, 1)
	test([]int{1}, []int{0}, -1)
	test([]int{1, 1}, []int{1, 0}, -1)
	test([]int{1}, []int{1, 0}, 1)
	test([]int{1, 0}, []int{1}, -1)
}

func afdoCfg() *SemVerGCSRepoManagerConfig {
	return &SemVerGCSRepoManagerConfig{
		GCSRepoManagerConfig: GCSRepoManagerConfig{
			NoCheckoutRepoManagerConfig: NoCheckoutRepoManagerConfig{
				CommonRepoManagerConfig: CommonRepoManagerConfig{
					ChildBranch:   "master",
					ChildPath:     "unused/by/afdo/repomanager",
					CommitMsgTmpl: TMPL_COMMIT_MSG_AFDO,
					ParentBranch:  "master",
					ParentRepo:    "", // Filled in after GitInit().
				},
			},
			GCSBucket:   "chromeos-prebuilt",
			GCSPath:     "afdo-job/llvm",
			VersionFile: "chrome/android/profiles/newest.txt",
		},
		ShortRevRegex: "(\\d+)\\.(\\d+)\\.(\\d+)\\.0_rc-r(\\d+)-merged",
		VersionRegex:  "^chromeos-chrome-amd64-(\\d+)\\.(\\d+)\\.(\\d+)\\.0_rc-r(\\d+)-merged\\.afdo\\.bz2$",
	}
}

func gerritCR(t *testing.T, g gerrit.GerritInterface) codereview.CodeReview {
	rv, err := (&codereview.GerritConfig{
		URL:     "https://skia-review.googlesource.com",
		Project: "skia",
		Config:  codereview.GERRIT_CONFIG_CHROMIUM,
	}).Init(g, nil)
	require.NoError(t, err)
	return rv
}

func setupAfdo(t *testing.T) (context.Context, *gcsRepoManager, *mockhttpclient.URLMock, *gitiles_testutils.MockRepo, *git_testutils.GitBuilder, func()) {
	wd, err := ioutil.TempDir("", "")
	require.NoError(t, err)

	ctx := context.Background()

	// Create child and parent repos.
	parent := git_testutils.GitInit(t, ctx)
	parent.Add(context.Background(), AFDO_VERSION_FILE_PATH, afdoRevBase)
	parent.Commit(context.Background())

	urlmock := mockhttpclient.NewURLMock()
	mockParent := gitiles_testutils.NewMockRepo(t, parent.RepoUrl(), git.GitDir(parent.Dir()), urlmock)

	gUrl := "https://fake-skia-review.googlesource.com"
	serialized, err := json.Marshal(&gerrit.AccountDetails{
		AccountId: 101,
		Name:      mockUser,
		Email:     mockUser,
		UserName:  mockUser,
	})
	require.NoError(t, err)
	serialized = append([]byte("abcd\n"), serialized...)
	urlmock.MockOnce(gUrl+"/a/accounts/self/detail", mockhttpclient.MockGetDialogue(serialized))
	g, err := gerrit.NewGerrit(gUrl, urlmock.Client())
	require.NoError(t, err)

	cfg := afdoCfg()
	cfg.ParentRepo = parent.RepoUrl()

	// Initial update. Everything up-to-date.
	mockParent.MockGetCommit(ctx, "master")
	parentMaster, err := git.GitDir(parent.Dir()).RevParse(ctx, "HEAD")
	require.NoError(t, err)
	mockParent.MockReadFile(ctx, AFDO_VERSION_FILE_PATH, parentMaster)
	mockGSList(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, map[string]string{
		afdoRevBase: afdoTimeBase,
	})

	rm, err := NewSemVerGCSRepoManager(ctx, cfg, wd, g, "fake.server.com", urlmock.Client(), gerritCR(t, g), false)
	require.NoError(t, err)

	_, _, _, err = rm.Update(ctx)
	require.NoError(t, err)

	cleanup := func() {
		testutils.RemoveAll(t, wd)
		parent.Cleanup()
	}

	return ctx, rm.(*gcsRepoManager), urlmock, mockParent, parent, cleanup
}

type gsObject struct {
	Kind                    string `json:"kind"`
	Id                      string `json:"id"`
	SelfLink                string `json:"selfLink"`
	Name                    string `json:"name"`
	Bucket                  string `json:"bucket"`
	Generation              string `json:"generation"`
	Metageneration          string `json:"metageneration"`
	ContentType             string `json:"contentType"`
	TimeCreated             string `json:"timeCreated"`
	Updated                 string `json:"updated"`
	StorageClass            string `json:"storageClass"`
	TimeStorageClassUpdated string `json:"timeStorageClassUpdated"`
	Size                    string `json:"size"`
	Md5Hash                 string `json:"md5Hash"`
	MediaLink               string `json:"mediaLink"`
	Crc32c                  string `json:"crc32c"`
	Etag                    string `json:"etag"`
}

type gsObjectList struct {
	Kind  string     `json:"kind"`
	Items []gsObject `json:"items"`
}

func mockGSList(t *testing.T, urlmock *mockhttpclient.URLMock, bucket, gsPath string, items map[string]string) {
	fakeUrl := fmt.Sprintf("https://www.googleapis.com/storage/v1/b/%s/o?alt=json&delimiter=&pageToken=&prefix=%s&prettyPrint=false&projection=full&versions=false", bucket, url.PathEscape(gsPath))
	resp := gsObjectList{
		Kind:  "storage#objects",
		Items: []gsObject{},
	}
	for item, timestamp := range items {
		resp.Items = append(resp.Items, gsObject{
			Kind:                    "storage#object",
			Id:                      path.Join(bucket+gsPath, item),
			SelfLink:                path.Join(bucket+gsPath, item),
			Name:                    item,
			Bucket:                  bucket,
			Generation:              "1",
			Metageneration:          "1",
			ContentType:             "application/octet-stream",
			TimeCreated:             timestamp,
			Updated:                 timestamp,
			StorageClass:            "MULTI_REGIONAL",
			TimeStorageClassUpdated: timestamp,
			Size:                    "12345",
			Md5Hash:                 "dsafkldkldsaf",
			MediaLink:               fakeUrl + item,
			Crc32c:                  "eiekls",
			Etag:                    "lasdfklds",
		})
	}
	respBytes, err := json.MarshalIndent(resp, "", "  ")
	require.NoError(t, err)
	urlmock.MockOnce(fakeUrl, mockhttpclient.MockGetDialogue(respBytes))
}

func mockGSObject(t *testing.T, urlmock *mockhttpclient.URLMock, bucket, gsPath, item, timestamp string) {
	fakeUrl := fmt.Sprintf("https://www.googleapis.com/storage/v1/b/%s/o/%s?alt=json&prettyPrint=false&projection=full", bucket, url.PathEscape(path.Join(gsPath, item)))
	resp := gsObject{
		Kind:                    "storage#object",
		Id:                      path.Join(bucket+gsPath, item),
		SelfLink:                path.Join(bucket+gsPath, item),
		Name:                    item,
		Bucket:                  bucket,
		Generation:              "1",
		Metageneration:          "1",
		ContentType:             "application/octet-stream",
		TimeCreated:             timestamp,
		Updated:                 timestamp,
		StorageClass:            "MULTI_REGIONAL",
		TimeStorageClassUpdated: timestamp,
		Size:                    "12345",
		Md5Hash:                 "dsafkldkldsaf",
		MediaLink:               fakeUrl,
		Crc32c:                  "eiekls",
		Etag:                    "lasdfklds",
	}
	respBytes, err := json.MarshalIndent(resp, "", "  ")
	require.NoError(t, err)
	urlmock.MockOnce(fakeUrl, mockhttpclient.MockGetDialogue(respBytes))
}

func TestAFDORepoManager(t *testing.T) {
	unittest.LargeTest(t)

	ctx, rm, urlmock, mockParent, parent, cleanup := setupAfdo(t)
	defer cleanup()

	mockParent.MockGetCommit(ctx, "master")
	parentMaster, err := git.GitDir(parent.Dir()).RevParse(ctx, "HEAD")
	require.NoError(t, err)
	mockParent.MockReadFile(ctx, AFDO_VERSION_FILE_PATH, parentMaster)
	mockGSList(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, map[string]string{
		afdoRevBase: afdoTimeBase,
	})
	lastRollRev, tipRev, notRolledRevs, err := rm.Update(ctx)
	require.NoError(t, err)
	require.Equal(t, afdoRevBase, lastRollRev.Id)
	require.Equal(t, afdoRevBase, tipRev.Id)
	mockGSObject(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, afdoRevPrev, afdoTimePrev)
	prev, err := rm.GetRevision(ctx, afdoRevPrev)
	require.NoError(t, err)
	require.Equal(t, afdoRevPrev, prev.Id)
	mockGSObject(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, afdoRevBase, afdoTimeBase)
	base, err := rm.GetRevision(ctx, afdoRevBase)
	require.NoError(t, err)
	require.Equal(t, afdoRevBase, base.Id)
	mockGSObject(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, afdoRevNext, afdoTimeNext)
	next, err := rm.GetRevision(ctx, afdoRevNext)
	require.NoError(t, err)
	require.Equal(t, afdoRevNext, next.Id)
	require.Empty(t, rm.preUploadSteps)
	require.Equal(t, 0, len(notRolledRevs))

	// There's a new version.
	mockParent.MockGetCommit(ctx, "master")
	mockParent.MockReadFile(ctx, AFDO_VERSION_FILE_PATH, parentMaster)
	mockGSList(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, map[string]string{
		afdoRevBase: afdoTimeBase,
		afdoRevNext: afdoTimeNext,
	})
	lastRollRev, tipRev, notRolledRevs, err = rm.Update(ctx)
	require.NoError(t, err)
	require.Equal(t, afdoRevBase, lastRollRev.Id)
	require.Equal(t, afdoRevNext, tipRev.Id)
	require.Equal(t, 1, len(notRolledRevs))
	require.Equal(t, afdoRevNext, notRolledRevs[0].Id)

	// Upload a CL.

	// Mock the initial change creation.
	commitMsg := `Roll AFDO from 66.0.3336.0_rc-r1-merged to 66.0.3337.0_rc-r1-merged

This CL may cause a small binary size increase, roughly proportional
to how long it's been since our last AFDO profile roll. For larger
increases (around or exceeding 100KB), please file a bug against
gbiv@chromium.org. Additional context: https://crbug.com/805539

Please note that, despite rolling to chrome/android, this profile is
used for both Linux and Android.

If this roll has caused a breakage, revert this CL and stop the roller
using the controls here:
fake.server.com
Please CC reviewer@chromium.org on the revert to ensure that a human
is aware of the problem.

To report a problem with the AutoRoller itself, please file a bug:
https://bugs.chromium.org/p/skia/issues/entry?template=Autoroller+Bug

Documentation for the AutoRoller is here:
https://skia.googlesource.com/buildbot/+/master/autoroll/README.md

Tbr: reviewer@chromium.org
`
	subject := strings.Split(commitMsg, "\n")[0]
	reqBody := []byte(fmt.Sprintf(`{"project":"%s","subject":"%s","branch":"%s","topic":"","status":"NEW","base_commit":"%s"}`, rm.noCheckoutRepoManager.gerritConfig.Project, subject, rm.parentBranch, parentMaster))
	ci := gerrit.ChangeInfo{
		ChangeId: "123",
		Id:       "123",
		Issue:    123,
		Revisions: map[string]*gerrit.Revision{
			"ps1": {
				ID:     "ps1",
				Number: 1,
			},
		},
	}
	respBody, err := json.Marshal(ci)
	require.NoError(t, err)
	respBody = append([]byte(")]}'\n"), respBody...)
	urlmock.MockOnce("https://fake-skia-review.googlesource.com/a/changes/", mockhttpclient.MockPostDialogueWithResponseCode("application/json", reqBody, respBody, 201))

	// Mock the edit of the change to update the commit message.
	reqBody = []byte(fmt.Sprintf(`{"message":"%s"}`, strings.Replace(commitMsg, "\n", "\\n", -1)))
	urlmock.MockOnce("https://fake-skia-review.googlesource.com/a/changes/123/edit:message", mockhttpclient.MockPutDialogue("application/json", reqBody, []byte("")))

	// Mock the request to modify the version file.
	reqBody = []byte(tipRev.Id)
	url := fmt.Sprintf("https://fake-skia-review.googlesource.com/a/changes/123/edit/%s", url.QueryEscape(AFDO_VERSION_FILE_PATH))
	urlmock.MockOnce(url, mockhttpclient.MockPutDialogue("", reqBody, []byte("")))

	// Mock the request to publish the change edit.
	reqBody = []byte(`{"notify":"ALL"}`)
	urlmock.MockOnce("https://fake-skia-review.googlesource.com/a/changes/123/edit:publish", mockhttpclient.MockPostDialogue("application/json", reqBody, []byte("")))

	// Mock the request to load the updated change.
	respBody, err = json.Marshal(ci)
	require.NoError(t, err)
	respBody = append([]byte(")]}'\n"), respBody...)
	urlmock.MockOnce("https://fake-skia-review.googlesource.com/a/changes/123/detail?o=ALL_REVISIONS", mockhttpclient.MockGetDialogue(respBody))

	// Mock the request to set the CQ.
	reqBody = []byte(`{"labels":{"Code-Review":1,"Commit-Queue":2},"message":"","reviewers":[{"reviewer":"reviewer@chromium.org"}]}`)
	urlmock.MockOnce("https://fake-skia-review.googlesource.com/a/changes/123/revisions/ps1/review", mockhttpclient.MockPostDialogue("application/json", reqBody, []byte("")))

	issue, err := rm.CreateNewRoll(ctx, lastRollRev, tipRev, notRolledRevs, emails, cqExtraTrybots, false)
	require.NoError(t, err)
	require.Equal(t, ci.Issue, issue)
}

func TestChromiumAFDOConfigValidation(t *testing.T) {
	unittest.SmallTest(t)

	cfg := afdoCfg()
	// Fill in some fields which are not supplied above.
	cfg.ParentRepo = "dummy"
	cfg.CommitMsgTmpl = TMPL_COMMIT_MSG_AFDO
	cfg.GCSBucket = AFDO_GS_BUCKET
	cfg.GCSPath = AFDO_GS_PATH
	cfg.VersionFile = AFDO_VERSION_FILE_PATH
	cfg.VersionRegex = AFDO_VERSION_REGEX
	require.NoError(t, cfg.Validate())

	// The only fields come from the nested Configs, so exclude them and
	// verify that we fail validation.
	cfg = &SemVerGCSRepoManagerConfig{}
	require.Error(t, cfg.Validate())
}

func TestAFDORepoManagerCurrentRevNotFound(t *testing.T) {
	unittest.LargeTest(t)

	ctx, rm, urlmock, mockParent, parent, cleanup := setupAfdo(t)
	defer cleanup()

	// Sanity check.
	mockGSObject(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, afdoRevPrev, afdoTimePrev)
	prev, err := rm.GetRevision(ctx, afdoRevPrev)
	require.NoError(t, err)
	require.Equal(t, afdoRevPrev, prev.Id)
	mockGSObject(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, afdoRevBase, afdoTimeBase)
	base, err := rm.GetRevision(ctx, afdoRevBase)
	require.NoError(t, err)
	require.Equal(t, afdoRevBase, base.Id)
	mockGSObject(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, afdoRevNext, afdoTimeNext)
	next, err := rm.GetRevision(ctx, afdoRevNext)
	require.NoError(t, err)
	require.Equal(t, afdoRevNext, next.Id)

	// Roll to a revision which is not in the GCS bucket.
	parent.Add(context.Background(), AFDO_VERSION_FILE_PATH, "BOGUS_REV")
	parent.Commit(context.Background())
	mockParent.MockGetCommit(ctx, "master")
	parentMaster, err := git.GitDir(parent.Dir()).RevParse(ctx, "HEAD")
	require.NoError(t, err)
	mockParent.MockReadFile(ctx, AFDO_VERSION_FILE_PATH, parentMaster)
	mockGSList(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, map[string]string{
		afdoRevBase: afdoTimeBase,
		afdoRevPrev: afdoTimePrev,
		afdoRevNext: afdoTimeNext,
	})
	mockGSObject(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, "BOGUS_REV", afdoTimePrev)
	lastRollRev, tipRev, notRolledRevs, err := rm.Update(ctx)
	require.NoError(t, err)
	expect := &revision.Revision{
		Id:      "BOGUS_REV",
		Display: "BOGUS_REV",
		URL:     "https://www.googleapis.com/storage/v1/b/chromeos-prebuilt/o/afdo-job%2Fllvm%2FBOGUS_REV?alt=json&prettyPrint=false&projection=full",
	}
	expect.Timestamp = lastRollRev.Timestamp
	assertdeep.Equal(t, expect, lastRollRev)
	require.False(t, util.TimeIsZero(lastRollRev.Timestamp))
	require.Equal(t, afdoRevNext, tipRev.Id)
	require.Equal(t, 1, len(notRolledRevs))
	require.Equal(t, afdoRevNext, notRolledRevs[0].Id)
	require.True(t, urlmock.Empty())

	// Now try again, but don't mock the bogus rev in GCS. We should still
	// come up with the same lastRollRev.Id, but the Revision will otherwise
	// be empty.
	mockParent.MockGetCommit(ctx, "master")
	mockParent.MockReadFile(ctx, AFDO_VERSION_FILE_PATH, parentMaster)
	mockGSList(t, urlmock, AFDO_GS_BUCKET, AFDO_GS_PATH, map[string]string{
		afdoRevBase: afdoTimeBase,
		afdoRevPrev: afdoTimePrev,
		afdoRevNext: afdoTimeNext,
	})
	lastRollRev, tipRev, notRolledRevs, err = rm.Update(ctx)
	require.NoError(t, err)
	assertdeep.Equal(t, &revision.Revision{
		Id:      "BOGUS_REV",
		Display: "BOGUS_REV",
	}, lastRollRev)
	require.Equal(t, afdoRevNext, tipRev.Id)
	require.Equal(t, 1, len(notRolledRevs))
	require.Equal(t, afdoRevNext, notRolledRevs[0].Id)
	require.True(t, urlmock.Empty())
}

// Converts data from an incoming GitHub PR into a format needed for the CI.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	shared "go.goldmine.build/ci/go"
	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/sklog"
)

type PRConvert struct {
	s3client *minio.Client
	path     string
	bucket   string
}

// New creates a new PRConvert instance.
//
// credentialsDir is the directory where the storage HMAC key and secret files
// are located for authenticating
//
//	to the S3 (or compatible) storage.
//
// endpoint is the S3 compatible URL of the API endpoint without the http(s)://
// prefix.
//
// useSSL says whether or not to use HTTP or HTTPS on the endpoint.
//
// bucket is the name of the S3 bucket to store the data.
//
// All of this work is so that we can take a GitHub PR and conjure up a patchset
// number. That is, a monotonically increasing number that keeps track of the
// versions of the PR. Since the PR itself doesn't have the info we need to keep
// track of all the PR versions we've seen and count them ourselves, storing the
// list of versions in a durable storage.
func New(credentialsDir string, endpoint string, useSSL bool, path string, bucket string) (*PRConvert, error) {
	// Load HMAC key and secret.
	b, err := os.ReadFile(filepath.Join(credentialsDir, "key"))
	if err != nil {
		return nil, skerr.Wrapf(err, "reading key")
	}
	key := strings.TrimSpace(string(b))

	b, err = os.ReadFile(filepath.Join(credentialsDir, "secret"))
	if err != nil {
		return nil, skerr.Wrapf(err, "reading secret")
	}
	secret := strings.TrimSpace(string(b))

	// Initialize minio client object.
	s3client, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(key, secret, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, skerr.Wrapf(err, "constructing minio.Client")
	}

	return &PRConvert{
		s3client: s3client,
		path:     path,
		bucket:   bucket,
	}, nil
}

// Patchset is the information we extracted from a GitHub Pull Request that we
// actually need to run the CI.
type Patchset struct {
	PRNumber int
	Login    string
	SHA      string
}

// allPatchsets is the file format (in JSON) of the file we write to the storage
// system to keep track of all the Pull Request updated we've seen.
type allPatchsets []Patchset

// WorkflowArgsFromPullRequest adds the latest Patchset to the given PR and then
// returns a TryotWorkflowArgs that contains all the information the CI will
// need to run the tests.
func (prc *PRConvert) WorkflowArgsFromPullRequest(ctx context.Context, p Patchset) (*shared.TrybotWorkflowArgs, error) {
	filename := fmt.Sprintf("%s/%d.txt", prc.path, p.PRNumber)

	// TODO if the GetObject fails we should probably retry.
	o, err := prc.s3client.GetObject(ctx, prc.bucket, filename, minio.GetObjectOptions{})
	allPatchsets := allPatchsets{}
	if err != nil {
		sklog.Errorf("Failed to GetObject: %s", err)
	} else {
		if err := json.NewDecoder(o).Decode(&allPatchsets); err != nil {
			sklog.Warningf("Failed to read %s: %s", filename, err)
		}
	}
	allPatchsets = append(allPatchsets, p)
	patchsetNumber := len(allPatchsets)

	var buf bytes.Buffer
	err = json.NewEncoder(&buf).Encode(allPatchsets)
	if err != nil {
		sklog.Errorf("Failed to encode appPatchsets: %s", err)
		return nil, err
	}

	_, err = prc.s3client.PutObject(ctx, prc.bucket, filename, &buf, int64(buf.Len()), minio.PutObjectOptions{})
	if err != nil {
		sklog.Errorf("Failed to store file back to storage: %s", err)
	}

	return &shared.TrybotWorkflowArgs{
		PRNumber:       p.PRNumber,
		PatchsetNumber: patchsetNumber,
		SHA:            p.SHA,
	}, nil
}

package storage

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	gstorage "cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"go.skia.org/infra/go/gcs"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/golden/go/baseline"
)

// GSClientOptions is used to define input parameters to the GStorageClient.
type GSClientOptions struct {
	HashesGSPath   string // bucket and path for storing the list of known digests.
	BaselineGSPath string // bucket and path for storing the base line information. This is considered a directory.
}

// GStorageClient provides read/write to Google storage for one-off
// use cases, i.e. the list of known hash files or the base line.
type GStorageClient struct {
	storageClient *gstorage.Client
	options       GSClientOptions
}

// NewGStorageClient creates a new instance of GStorage client. The various
// output paths are set in GSClientOptions.
func NewGStorageClient(client *http.Client, options *GSClientOptions) (*GStorageClient, error) {
	storageClient, err := gstorage.NewClient(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}

	return &GStorageClient{
		storageClient: storageClient,
		options:       *options,
	}, nil
}

// WriteKnownDigests writes the given list of digests to GS as newline
// separated strings.
func (g *GStorageClient) WriteKnownDigests(digests []string) error {
	writeFn := func(w *gstorage.Writer) error {
		for _, digest := range digests {
			if _, err := w.Write([]byte(digest + "\n")); err != nil {
				return fmt.Errorf("Error writing digests: %s", err)
			}
		}
		return nil
	}

	return g.writeToPath(g.options.HashesGSPath, "text/plain", writeFn)
}

// WriteBaseLine writes the given baseline to GCS. It returns the path of the
// written file in GCS (prefixed with 'gs://').
func (g *GStorageClient) WriteBaseLine(baseLine *baseline.CommitableBaseLine) (string, error) {
	writeFn := func(w *gstorage.Writer) error {
		if err := json.NewEncoder(w).Encode(baseLine); err != nil {
			return fmt.Errorf("Error encoding baseline to JSON: %s", err)
		}
		return nil
	}

	// Change the output file based on whether it's the master branch or a Gerrit issue.
	outPath := "master.json"
	if baseLine.Issue > 0 {
		outPath = fmt.Sprintf("issue_%d.json", baseLine.Issue)
	}
	outPath = g.options.BaselineGSPath + "/" + outPath
	return "gs://" + outPath, g.writeToPath(outPath, "application/json", writeFn)
}

// loadKnownDigests loads the digests that have previously been written
// to GS via WriteKnownDigests. Used for testing.
func (g *GStorageClient) loadKnownDigests() ([]string, error) {
	bucketName, storagePath := gcs.SplitGSPath(g.options.HashesGSPath)

	ctx := context.Background()
	target := g.storageClient.Bucket(bucketName).Object(storagePath)

	// If the item doesn't exist this will return gstorage.ErrObjectNotExist
	_, err := target.Attrs(ctx)
	if err != nil {
		return nil, err
	}

	reader, err := target.NewReader(ctx)
	if err != nil {
		return nil, err
	}
	defer util.Close(reader)

	scanner := bufio.NewScanner(reader)
	ret := []string{}
	for scanner.Scan() {
		ret = append(ret, scanner.Text())
	}
	return ret, nil
}

// removeGSPath removes the given file. Primarily used for testing.
func (g *GStorageClient) removeGSPath(targetPath string) error {
	bucketName, storagePath := gcs.SplitGSPath(targetPath)
	target := g.storageClient.Bucket(bucketName).Object(storagePath)
	return target.Delete(context.Background())
}

// writeToPath is a generic function that allows to write data to the given
// target path in GCS. The actual writing is done in the passed write function.
func (g *GStorageClient) writeToPath(targetPath, contentType string, wrtFn func(w *gstorage.Writer) error) error {
	bucketName, storagePath := gcs.SplitGSPath(targetPath)

	// Only write the known digests if a target path was given.
	if (bucketName == "") || (storagePath == "") {
		return nil
	}

	ctx := context.Background()
	target := g.storageClient.Bucket(bucketName).Object(storagePath)
	writer := target.NewWriter(ctx)
	writer.ObjectAttrs.ContentType = contentType
	writer.ObjectAttrs.ACL = []gstorage.ACLRule{{Entity: gstorage.AllUsers, Role: gstorage.RoleReader}}
	defer util.Close(writer)

	// Write the actual data.
	if err := wrtFn(writer); err != nil {
		return err
	}

	sklog.Infof("File written to GS path %s", targetPath)
	return nil
}

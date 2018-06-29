package ingestion

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"cloud.google.com/go/storage"
	"google.golang.org/api/option"

	"go.skia.org/infra/go/depot_tools"
	"go.skia.org/infra/go/eventbus"
	"go.skia.org/infra/go/fileutil"
	"go.skia.org/infra/go/gcs"
	"go.skia.org/infra/go/git/gitinfo"
	"go.skia.org/infra/go/httputils"
	"go.skia.org/infra/go/sharedconfig"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/go/vcsinfo"
)

// Limit the number of times the ingester tries to get a file before giving up.
const MAX_URI_GET_TRIES = 4

// Constructor is the signature that has to be implemented to register a
// Processor implementation to be instantiated by name from a config struct.
//   vcs is an instance that might be shared across multiple ingesters.
//   config is ususally parsed from a JSON5 file.
//   client can be assumed to be ready to serve the needs of the resulting Processor.
//   eventBus is the eventbus to be used by the ingester (optional).
type Constructor func(vcs vcsinfo.VCS, config *sharedconfig.IngesterConfig, client *http.Client, eventBus eventbus.EventBus) (Processor, error)

// stores the constructors that register for instantiation from a config struct.
var constructors = map[string]Constructor{}

// used to synchronize constructor registration and instantiation.
var registrationMutex sync.Mutex

// Register registers the given constructor to create an instance of a Processor.
func Register(id string, constructor Constructor) {
	registrationMutex.Lock()
	defer registrationMutex.Unlock()
	constructors[id] = constructor
}

// IngestersFromConfig creates a list of ingesters from a config struct.
// Usually the struct is created from parsing a config file.
// client is assumed to be suitable for the given application. If e.g. the
// processors of the current application require an authenticated http client,
// then it is expected that client meets these requirements.
func IngestersFromConfig(ctx context.Context, config *sharedconfig.Config, client *http.Client, eventBus eventbus.EventBus) ([]*Ingester, error) {
	registrationMutex.Lock()
	defer registrationMutex.Unlock()
	ret := []*Ingester{}

	// Set up the gitinfo object.
	var vcs vcsinfo.VCS
	var err error
	if vcs, err = gitinfo.CloneOrUpdate(ctx, config.GitRepoURL, config.GitRepoDir, true); err != nil {
		return nil, err
	}

	// Instantiate the secondary repo if one was specified.
	var secondaryVCS vcsinfo.VCS
	var extractor depot_tools.DEPSExtractor
	if config.SecondaryRepoURL != "" {
		if secondaryVCS, err = gitinfo.CloneOrUpdate(ctx, config.SecondaryRepoURL, config.SecondaryRepoDir, true); err != nil {
			return nil, err
		}
		extractor = depot_tools.NewRegExDEPSExtractor(config.SecondaryRegEx)
		vcs.(*gitinfo.GitInfo).SetSecondaryRepo(secondaryVCS, extractor)
	}

	// Set up the Google storage client.
	if client == nil {
		client = httputils.NewBackOffClient()
	}

	// for each defined ingester create an instance.
	for id, ingesterConf := range config.Ingesters {
		processorConstructor, ok := constructors[id]
		if !ok {
			return nil, fmt.Errorf("Unknown ingester: '%s'", id)
		}

		// Instantiate the sources
		sources := make([]Source, 0, len(ingesterConf.Sources))
		for _, dataSource := range ingesterConf.Sources {
			oneSource, err := getSource(id, dataSource, client)
			if err != nil {
				return nil, fmt.Errorf("Error instantiating sources for ingester '%s': %s", id, err)
			}
			sources = append(sources, oneSource)
		}

		// instantiate the processor
		processor, err := processorConstructor(vcs, ingesterConf, client, eventBus)
		if err != nil {
			return nil, err
		}

		// create the ingester and add it to the result.
		ingester, err := NewIngester(id, ingesterConf, vcs, sources, processor)
		if err != nil {
			return nil, err
		}
		ret = append(ret, ingester)
	}

	return ret, nil
}

// getSource returns an instance of source that is either getting data from
// Google storage or the local fileystem.
func getSource(id string, dataSource *sharedconfig.DataSource, client *http.Client) (Source, error) {
	if dataSource.Dir == "" {
		return nil, fmt.Errorf("Datasource for %s is missing a directory.", id)
	}

	if dataSource.Bucket != "" {
		return NewGoogleStorageSource(id, dataSource.Bucket, dataSource.Dir, client)
	}
	return NewFileSystemSource(id, dataSource.Dir)
}

// validIngestionFile returns true if the given file name matches basic rules.
func validIngestionFile(fName string) bool {
	return strings.HasSuffix(strings.TrimSpace(fName), ".json")
}

// GoogleStorageSource implementes the Source interface for Google Storage.
type GoogleStorageSource struct {
	bucket        string
	rootDir       string
	id            string
	storageClient *storage.Client
	client        *http.Client
}

// NewGoogleStorageSource returns a new instance of GoogleStorageSource based
// on the bucket and directory provided. The id is used to identify the Source
// and is generally the same id as the ingester.
func NewGoogleStorageSource(baseName, bucket, rootDir string, client *http.Client) (Source, error) {
	storageClient, err := storage.NewClient(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("Failed to create a Google Storage API client: %s", err)
	}

	return &GoogleStorageSource{
		bucket:        bucket,
		rootDir:       rootDir,
		id:            fmt.Sprintf("%s:gs://%s/%s", baseName, bucket, rootDir),
		client:        client,
		storageClient: storageClient,
	}, nil
}

// See Source interface.
func (g *GoogleStorageSource) Poll(startTime, endTime int64) ([]ResultFileLocation, error) {
	dirs := gcs.GetLatestGCSDirs(startTime, endTime, g.rootDir)
	retval := []ResultFileLocation{}
	for _, dir := range dirs {
		err := gcs.AllFilesInDir(g.storageClient, g.bucket, dir, func(item *storage.ObjectAttrs) {
			// TODO(stephana): remove this when we move away from the chromium-skia-gm bucket.
			if strings.Contains(filepath.Base(item.Name), "uploading") {
				sklog.Warningf("Received temporary file from GS: %s", item.Name)
			} else if validIngestionFile(item.Name) && (item.Updated.Unix() > startTime) {
				retval = append(retval, newGCSResultFileLocation(item, g.rootDir, g.storageClient))
			}
		})

		if err != nil {
			return nil, fmt.Errorf("Error occurred while retrieving files from %s/%s: %s", g.bucket, dir, err)
		}
	}
	return retval, nil
}

// See Source interface.
func (g *GoogleStorageSource) ID() string {
	return g.id
}

// gsResultFileLocation implements the ResultFileLocation for Google storage.
type gsResultFileLocation struct {
	bucket        string
	name          string
	relativeName  string
	lastUpdated   int64
	md5           string
	storageClient *storage.Client
	content       []byte
}

func newGCSResultFileLocation(result *storage.ObjectAttrs, rootDir string, storageClient *storage.Client) ResultFileLocation {
	return &gsResultFileLocation{
		bucket:        result.Bucket,
		name:          result.Name,
		relativeName:  strings.TrimPrefix(result.Name, rootDir+"/"),
		lastUpdated:   result.Updated.Unix(),
		md5:           hex.EncodeToString(result.MD5),
		storageClient: storageClient,
	}
}

// See ResultFileLocation interface.
func (g *gsResultFileLocation) Open() (io.ReadCloser, error) {
	// If we have read this before, then just return a reader.
	if g.content != nil {
		return ioutil.NopCloser(bytes.NewBuffer(g.content)), nil
	}

	if g.content == nil {
		var reader io.Reader
		var err error
		for i := 0; i < MAX_URI_GET_TRIES; i++ {
			reader, err = g.storageClient.Bucket(g.bucket).Object(g.name).NewReader(context.Background())
			if err != nil {
				sklog.Errorf("New reader failed for %s/%s: %s", g.bucket, g.name, err)
				continue
			}

			// Read the entire file into memory and return a buffer.
			if g.content, err = ioutil.ReadAll(reader); err != nil {
				sklog.Errorf("Error reading content of %s/%s: %s", g.bucket, g.name, err)
				g.content = nil
				continue
			}

			sklog.Infof("GCSFILE READ %s/%s", g.bucket, g.name)
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Failed fetching %s/%s after %d attempts", g.bucket, g.name, MAX_URI_GET_TRIES)
		}
	}

	return ioutil.NopCloser(bytes.NewBuffer(g.content)), nil
}

// See ResultFileLocation interface.
func (g *gsResultFileLocation) Name() string {
	return fmt.Sprintf("gs://%s/%s", g.bucket, g.name)
}

// See ResultFileLocation interface.
func (g *gsResultFileLocation) MD5() string {
	return g.md5
}

// See ResultFileLocation interface.
func (g *gsResultFileLocation) TimeStamp() int64 {
	return g.lastUpdated
}

// See ResultFileLocation interface.
func (g *gsResultFileLocation) Content() []byte {
	return g.content
}

// FileSystemSource implements the Source interface to read from the local
// file system.
type FileSystemSource struct {
	rootDir string
	id      string
}

func NewFileSystemSource(baseName, rootDir string) (Source, error) {
	return &FileSystemSource{
		rootDir: rootDir,
		id:      fmt.Sprintf("%s:fs:%s", baseName, rootDir),
	}, nil
}

// See Source interface.
func (f *FileSystemSource) Poll(startTime, endTime int64) ([]ResultFileLocation, error) {
	retval := []ResultFileLocation{}

	// although GetLatestGCSDirs is in the "gcs" package, there's nothing specific about
	// its operation that makes it not re-usable here.
	dirs := gcs.GetLatestGCSDirs(startTime, endTime, f.rootDir)
	for _, dir := range dirs {
		// Inject dir into a closure.
		func(dir string) {
			walkFn := func(path string, info os.FileInfo, err error) error {
				if err != nil {
					// We swallow the error to continue processing, but make sure it's
					// shows up in the logs.
					sklog.Errorf("Error walking %s: %s", path, err)
					return nil
				}
				if info.IsDir() {
					return nil
				}

				updateTimestamp := info.ModTime().Unix()
				if validIngestionFile(path) && (updateTimestamp > startTime) {
					rf, err := FileSystemResult(path, f.rootDir)
					if err != nil {
						sklog.Errorf("Unable to create file system result: %s", err)
						return nil
					}
					retval = append(retval, rf)
				}
				return nil
			}

			// Only walk the tree if the top directory exists.
			if fileutil.FileExists(dir) {
				if err := filepath.Walk(dir, walkFn); err != nil {
					sklog.Infof("Unable to read the local dir %s: %s", dir, err)
					return
				}
			}
		}(dir)
	}

	return retval, nil
}

// See Source interface.
func (f *FileSystemSource) ID() string {
	return f.id
}

// fsResultFileLocation implements the ResultFileLocation interface for
// the local filesystem.
type fsResultFileLocation struct {
	path        string
	buf         []byte
	md5         string
	lastUpdated int64
}

// FileSystemResult returns a ResultFileLocation for files. path is the path
// where the target file resides and rootDir is the root of all paths.
func FileSystemResult(path, rootDir string) (ResultFileLocation, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	// Read file into buffer and calculate the md5 in the process.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer util.Close(file)

	var buf bytes.Buffer
	md5, err := util.MD5FromReader(file, &buf)
	if err != nil {
		return nil, fmt.Errorf("Unable to get MD5 hash of %s: %s", path, err)
	}

	absRootDir, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	return &fsResultFileLocation{
		path:        strings.TrimPrefix(absPath, absRootDir+"/"),
		buf:         buf.Bytes(),
		md5:         hex.EncodeToString(md5),
		lastUpdated: fileInfo.ModTime().Unix(),
	}, nil
}

// see ResultFileLocation interface.
func (f *fsResultFileLocation) Open() (io.ReadCloser, error) {
	return ioutil.NopCloser(bytes.NewBuffer(f.buf)), nil
}

// see ResultFileLocation interface.
func (f *fsResultFileLocation) Name() string {
	return f.path
}

// see ResultFileLocation interface.
func (f *fsResultFileLocation) MD5() string {
	return f.md5
}

// see ResultFileLocation interface.
func (f *fsResultFileLocation) TimeStamp() int64 {
	return f.lastUpdated
}

// see ResultFileLocation interface.
func (f *fsResultFileLocation) Content() []byte {
	return f.buf
}

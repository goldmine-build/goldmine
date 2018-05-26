// Stores and retrieves fiddles and associated assets in Google Storage.
package store

import (
	"context"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/golang/groupcache/lru"
	"go.skia.org/infra/fiddle/go/types"
	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	FIDDLE_STORAGE_BUCKET = "skia-fiddle"

	LRU_CACHE_SIZE = 10000

	// *_METADATA are the keys used to store the metadata values in Google
	// Storage.
	USER_METADATA                   = "user"
	WIDTH_METADATA                  = "width"
	HEIGHT_METADATA                 = "height"
	SOURCE_METADATA                 = "source"
	SOURCE_MIPMAP_METADATA          = "source_mipmap"
	TEXTONLY_METADATA               = "textOnly"
	SRGB_METADATA                   = "srgb"
	F16_METADATA                    = "f16"
	ANIMATED_METADATA               = "animated"
	DURATION_METADATA               = "duration"
	OFFSCREEN_METADATA              = "offscreen"
	OFFSCREEN_WIDTH_METADATA        = "offscreen_width"
	OFFSCREEN_HEIGHT_METADATA       = "offscreen_height"
	OFFSCREEN_SAMPLE_COUNT_METADATA = "offscreen_sample_count"
	OFFSCREEN_TEXTURABLE_METADATA   = "offscreen_texturable"
	OFFSCREEN_MIPMAP_METADATA       = "offscreen_mipmap"
)

// Media is the type of outputs we can get from running a fiddle.
type Media string

// Media constants.
const (
	CPU      Media = "CPU"
	GPU      Media = "GPU"
	PDF      Media = "PDF"
	SKP      Media = "SKP"
	TXT      Media = "TXT"
	ANIM_CPU Media = "ANIM_CPU"
	ANIM_GPU Media = "ANIM_GPU"
	GLINFO   Media = "GLINFO"
	UNKNOWN  Media = ""
)

// props records the name and content-type for each type of Media and is used in mediaProps.
type props struct {
	filename    string
	contentType string
}

var (
	mediaProps = map[Media]props{
		CPU:      {filename: "cpu.png", contentType: "image/png"},
		GPU:      {filename: "gpu.png", contentType: "image/png"},
		PDF:      {filename: "pdf.pdf", contentType: "application/pdf"},
		SKP:      {filename: "skp.skp", contentType: "application/octet-stream"},
		TXT:      {filename: "txt.txt", contentType: "text/plain"},
		ANIM_CPU: {filename: "cpu.webm", contentType: "video/webm"},
		ANIM_GPU: {filename: "gpu.webm", contentType: "video/webm"},
		GLINFO:   {filename: "glinfo.text", contentType: "text/plain"},
	}

	// sourceFileName parses a souce image filename as stored in Google Storage.
	sourceFileName = regexp.MustCompile("^([0-9]+).png$")
)

// cacheEntry is used to store PNGs in the Store lru cache.
type cacheEntry struct {
	body  []byte
	runId string
}

// Store is used to read and write user code and media to and from Google
// Storage.
type Store struct {
	bucket *storage.BucketHandle

	// cache is an in-memory cache of PNGs, where the keys are <fiddlehash>-<media>.
	cache *lru.Cache
}

func cacheKey(fiddleHash string, media Media) string {
	return fiddleHash + "-" + string(media)
}

func shouldBeCached(media Media) bool {
	return media == CPU || media == GPU
}

// New create a new Store.
func New() (*Store, error) {
	// TODO(jcgregorio) Decide is this needs to be a backoff client. May not be necessary if we add caching at this layer.
	client, err := auth.NewDefaultJWTServiceAccountClient(auth.SCOPE_READ_WRITE)
	if err != nil {
		return nil, fmt.Errorf("Problem setting up client OAuth: %s", err)
	}
	storageClient, err := storage.NewClient(context.Background(), option.WithHTTPClient(client))
	if err != nil {
		return nil, fmt.Errorf("Problem creating storage client: %s", err)
	}
	return &Store{
		bucket: storageClient.Bucket(FIDDLE_STORAGE_BUCKET),
		cache:  lru.New(LRU_CACHE_SIZE),
	}, nil
}

// writeMediaFile writes a file to Google Storage. It also adds it to the cache.
//
//    media - The type of the file to write.
//    fiddleHash - The hash of the fiddle.
//    runId - A unique identifier for the specific run (git checkout of Skia).
//    b64 - The contents of the media file base64 encoded.
func (s *Store) writeMediaFile(media Media, fiddleHash, runId, b64 string) error {
	if b64 == "" && media != TXT {
		return fmt.Errorf("An empty file is not a valid %s file.", string(media))
	}
	p := mediaProps[media]
	if p.filename == "" {
		return fmt.Errorf("Unknown media type.")
	}
	body, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return fmt.Errorf("Media wasn't properly encoded base64: %s", err)
	}

	// Only PNGs get stored in the cache.
	if shouldBeCached(media) {
		key := cacheKey(fiddleHash, media)
		sklog.Infof("Cache write: %s", key)
		if c, ok := s.cache.Get(key); !ok {
			s.cache.Add(key, &cacheEntry{
				runId: runId,
				body:  body,
			})
		} else {
			if entry, ok := c.(*cacheEntry); ok {
				if runId > entry.runId {
					entry.body = body
					entry.runId = runId
				} else {
					sklog.Infof("Ran an older version of Skia, not caching: %v <= %v", runId, entry.runId)
				}
			} else {
				sklog.Errorf("Found a non-cacheEntry in the lru Cache: %v", reflect.TypeOf(c))
			}
		}
	}

	// Don't stall the http response while we write the image to Google Storage.
	// Instead, do the work in a Go routine. We know that by the time we reach
	// here we've successfully written the code to Google Storage, so even if
	// this fails the user can always 'rerun' the fiddle to generate an image
	// that failed to write.
	go func() {
		path := strings.Join([]string{"fiddle", fiddleHash, runId, p.filename}, "/")
		w := s.bucket.Object(path).NewWriter(context.Background())
		defer util.Close(w)
		w.ObjectAttrs.ContentEncoding = p.contentType
		if n, err := w.Write(body); err != nil {
			sklog.Errorf("There was a problem storing the media for %s. Uploaded %d bytes: %s", string(media), n, err)
		}
	}()
	return nil
}

// Put writes the code and media to Google Storage.
//
//    code - The user's code.
//    options - The options the user chose to run the code under.
//    gitHash - The git checkout this was built under.
//    ts - The timestamp of the gitHash.
//    results - The results from running fiddle_run.
//
// Code is written to:
//
//   gs://skia-fiddle/fiddle/<fiddleHash>/draw.cpp
//
// And media files are written to:
//
//   gs://skia-fiddle/fiddle/<fiddleHash>/<runId>/cpu.png
//   gs://skia-fiddle/fiddle/<fiddleHash>/<runId>/gpu.png
//   gs://skia-fiddle/fiddle/<fiddleHash>/<runId>/skp.skp
//   gs://skia-fiddle/fiddle/<fiddleHash>/<runId>/pdf.pdf
//
// Where runId is <git commit timestamp in RFC3339>:<git commit hash>.
//
// If results is nil then only the code is written.
//
// Returns the fiddleHash.
func (s *Store) Put(code string, options types.Options, gitHash string, ts time.Time, results *types.Result) (string, error) {
	fiddleHash, err := options.ComputeHash(code)
	if err != nil {
		return "", fmt.Errorf("Could not compute hash for the code: %s", err)
	}
	// Write code.
	path := strings.Join([]string{"fiddle", fiddleHash, "draw.cpp"}, "/")
	w := s.bucket.Object(path).NewWriter(context.Background())
	defer util.Close(w)
	w.ObjectAttrs.ContentEncoding = "text/plain"
	w.ObjectAttrs.Metadata = map[string]string{
		WIDTH_METADATA:                  fmt.Sprintf("%d", options.Width),
		HEIGHT_METADATA:                 fmt.Sprintf("%d", options.Height),
		SOURCE_METADATA:                 fmt.Sprintf("%d", options.Source),
		SOURCE_MIPMAP_METADATA:          fmt.Sprintf("%v", options.SourceMipMap),
		TEXTONLY_METADATA:               fmt.Sprintf("%v", options.TextOnly),
		SRGB_METADATA:                   fmt.Sprintf("%v", options.SRGB),
		F16_METADATA:                    fmt.Sprintf("%v", options.F16),
		ANIMATED_METADATA:               fmt.Sprintf("%v", options.Animated),
		DURATION_METADATA:               fmt.Sprintf("%f", options.Duration),
		OFFSCREEN_METADATA:              fmt.Sprintf("%v", options.OffScreen),
		OFFSCREEN_WIDTH_METADATA:        fmt.Sprintf("%d", options.OffScreenWidth),
		OFFSCREEN_HEIGHT_METADATA:       fmt.Sprintf("%d", options.OffScreenHeight),
		OFFSCREEN_SAMPLE_COUNT_METADATA: fmt.Sprintf("%d", options.OffScreenSampleCount),
		OFFSCREEN_TEXTURABLE_METADATA:   fmt.Sprintf("%v", options.OffScreenTexturable),
		OFFSCREEN_MIPMAP_METADATA:       fmt.Sprintf("%v", options.OffScreenMipMap),
	}
	if n, err := w.Write([]byte(code)); err != nil {
		return "", fmt.Errorf("There was a problem storing the code. Uploaded %d bytes: %s", n, err)
	}
	// Write media, if any.
	if results == nil {
		return fiddleHash, nil
	}
	if err := s.PutMedia(options, fiddleHash, gitHash, ts, results); err != nil {
		return fiddleHash, err
	}
	return fiddleHash, nil
}

// PutMedia writes the media for the given fiddleHash to Google Storage.
//
//    fiddleHash - The fiddle hash.
//    gitHash - The git checkout this was built under.
//    ts - The timestamp of the gitHash.
//    results - The results from running fiddle_run.
//
// Media files are written to:
//
//   gs://skia-fiddle/fiddle/<fiddleHash>/<runId>/cpu.png
//   gs://skia-fiddle/fiddle/<fiddleHash>/<runId>/gpu.png
//   gs://skia-fiddle/fiddle/<fiddleHash>/<runId>/skp.skp
//   gs://skia-fiddle/fiddle/<fiddleHash>/<runId>/pdf.pdf
//
// Where runId is <git commit timestamp in RFC3339>:<git commit hash>.
//
// If results is nil then only the code is written.
//
// Returns the fiddleHash.
func (s *Store) PutMedia(options types.Options, fiddleHash string, gitHash string, ts time.Time, results *types.Result) error {
	// Write each of the media files.
	runId := fmt.Sprintf("%s:%s", ts.UTC().Format(time.RFC3339), gitHash)
	if options.TextOnly {
		err := s.writeMediaFile(TXT, fiddleHash, runId, results.Execute.Output.Text)
		if err != nil {
			return err
		}
	} else {
		if options.Animated {
			err := s.writeMediaFile(ANIM_CPU, fiddleHash, runId, results.Execute.Output.AnimatedRaster)
			if err != nil {
				return err
			}
			err = s.writeMediaFile(ANIM_GPU, fiddleHash, runId, results.Execute.Output.AnimatedGpu)
			if err != nil {
				return err
			}
		} else {
			err := s.writeMediaFile(CPU, fiddleHash, runId, results.Execute.Output.Raster)
			if err != nil {
				return err
			}
			err = s.writeMediaFile(GPU, fiddleHash, runId, results.Execute.Output.Gpu)
			if err != nil {
				return err
			}
			err = s.writeMediaFile(PDF, fiddleHash, runId, results.Execute.Output.Pdf)
			if err != nil {
				return err
			}
			err = s.writeMediaFile(SKP, fiddleHash, runId, results.Execute.Output.Skp)
			if err != nil {
				return err
			}
		}
	}
	if results.Execute.Output.GLInfo != "" {
		err := s.writeMediaFile(GLINFO, fiddleHash, runId, results.Execute.Output.GLInfo)
		if err != nil {
			sklog.Warningf("Failed to save GLInfo: %s", err)
		}
	}
	return nil
}

// GetCode returns the code and options for the given fiddle hash.
//
//    fiddleHash - The fiddle hash.
//
// Returns the code and the options the code was run under.
func (s *Store) GetCode(fiddleHash string) (string, *types.Options, error) {
	o := s.bucket.Object(fmt.Sprintf("fiddle/%s/draw.cpp", fiddleHash))
	r, err := o.NewReader(context.Background())
	if err != nil {
		return "", nil, fmt.Errorf("Failed to open source file for %s: %s", fiddleHash, err)
	}
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return "", nil, fmt.Errorf("Failed to read source file for %s: %s", fiddleHash, err)
	}
	attr, err := o.Attrs(context.Background())
	if err != nil {
		return "", nil, fmt.Errorf("Failed to read attributes for %s: %s", fiddleHash, err)
	}
	width, err := strconv.Atoi(attr.Metadata[WIDTH_METADATA])
	if err != nil {
		return "", nil, fmt.Errorf("Failed to parse options width: %s", err)
	}
	height, err := strconv.Atoi(attr.Metadata[HEIGHT_METADATA])
	if err != nil {
		return "", nil, fmt.Errorf("Failed to parse options height: %s", err)
	}
	source, err := strconv.Atoi(attr.Metadata[SOURCE_METADATA])
	if err != nil {
		return "", nil, fmt.Errorf("Failed to parse options source: %s", err)
	}
	animated := attr.Metadata[ANIMATED_METADATA] == "true"
	duration, err := strconv.ParseFloat(attr.Metadata[DURATION_METADATA], 64)
	if err != nil && animated {
		duration = 1.0
	}

	offscreen_width, err := strconv.Atoi(attr.Metadata[OFFSCREEN_WIDTH_METADATA])
	if err != nil {
		offscreen_width = 0
	}
	offscreen_height, err := strconv.Atoi(attr.Metadata[OFFSCREEN_HEIGHT_METADATA])
	if err != nil {
		offscreen_height = 0
	}
	offscreen_sample_count, err := strconv.Atoi(attr.Metadata[OFFSCREEN_SAMPLE_COUNT_METADATA])
	if err != nil {
		offscreen_sample_count = 0
	}
	options := &types.Options{
		Width:                width,
		Height:               height,
		Source:               source,
		SourceMipMap:         attr.Metadata[SOURCE_MIPMAP_METADATA] == "true",
		TextOnly:             attr.Metadata[TEXTONLY_METADATA] == "true",
		SRGB:                 attr.Metadata[SRGB_METADATA] == "true",
		F16:                  attr.Metadata[F16_METADATA] == "true",
		Animated:             animated,
		Duration:             duration,
		OffScreen:            attr.Metadata[OFFSCREEN_METADATA] == "true",
		OffScreenWidth:       offscreen_width,
		OffScreenHeight:      offscreen_height,
		OffScreenSampleCount: offscreen_sample_count,
		OffScreenTexturable:  attr.Metadata[OFFSCREEN_TEXTURABLE_METADATA] == "true",
		OffScreenMipMap:      attr.Metadata[OFFSCREEN_MIPMAP_METADATA] == "true",
	}
	return string(b), options, nil
}

// GetMedia returns the file, content-type, filename, and error for a given fiddle hash and type of media.
//
//    fiddleHash - The hash of the fiddle.
//    media - The type of the file to read.
//
// Returns the media file contents as a byte slice, the content-type, and the filename of the media.
func (s *Store) GetMedia(fiddleHash string, media Media) ([]byte, string, string, error) {
	key := cacheKey(fiddleHash, media)
	if c, ok := s.cache.Get(key); ok {
		if entry, ok := c.(*cacheEntry); ok {
			sklog.Infof("Cache hit: %s", key)
			return entry.body, mediaProps[media].contentType, mediaProps[media].filename, nil
		}
	}
	// List the dirs under gs://skia-fiddle/fiddle/<fiddleHash>/ and find the most recent one.
	// Use Delimiter and Prefix to get a directory listing of sub-directories. See
	// https://cloud.google.com/storage/docs/json_api/v1/objects/list
	q := &storage.Query{
		Delimiter: "/",
		Prefix:    fmt.Sprintf("fiddle/%s/", fiddleHash),
	}
	runIds := []string{}
	ctx := context.Background()
	it := s.bucket.Objects(ctx, q)
	for obj, err := it.Next(); err != iterator.Done; obj, err = it.Next() {
		if err != nil {
			return nil, "", "", fmt.Errorf("Failed to retrieve list of results for (%s, %s): %s", fiddleHash, string(media), err)
		}
		if obj.Prefix != "" {
			runIds = append(runIds, obj.Prefix)
		}
	}
	if len(runIds) == 0 {
		return nil, "", "", fmt.Errorf("This fiddle has no valid output written (%s, %s)", fiddleHash, string(media))
	}
	sort.Strings(runIds)
	r, err := s.bucket.Object(runIds[len(runIds)-1] + mediaProps[media].filename).NewReader(ctx)
	if err != nil {
		return nil, "", "", fmt.Errorf("Unable to get reader for the media file (%s, %s): %s", fiddleHash, string(media), err)
	}
	defer util.Close(r)
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return nil, "", "", fmt.Errorf("Unable to read the media file (%s, %s): %s", fiddleHash, string(media), err)
	}
	if shouldBeCached(media) {
		s.cache.Add(cacheKey(fiddleHash, media), &cacheEntry{
			body: b,
		})
	}
	return b, mediaProps[media].contentType, mediaProps[media].filename, nil
}

// AddSource adds a new source image. The image must be a PNG.
//
//    image - The bytes on the PNG file.
//
// Returns the id of the source image.
func AddSource(image []byte) (int, error) {
	// Use the file 'lastid.txt' in the bucket that contains the last id used.
	// Read, record gen, increments, write with condition of unchanged generation.

	// TODO(jcgregorio) Implement.
	return 0, fmt.Errorf("Not implemented yet.")
}

// downloadSingleSourceImage downloads a single source image from the Google Storage bucket.
//
//    ctx - The context of the request.
//    bucket - The Google Storage bucket.
//    srcName - The full Google Storage path of the source image.
//    dstName - The full local file system name where the source image will be written to.
func downloadSingleSourceImage(ctx context.Context, bucket *storage.BucketHandle, srcName, dstName string) error {
	r, err := bucket.Object(srcName).NewReader(ctx)
	if err != nil {
		return fmt.Errorf("Failed to open reader for image %s: %s", srcName, err)
	}
	defer util.Close(r)
	w, err := os.Create(dstName)
	if err != nil {
		return fmt.Errorf("Failed to open writer for image %s: %s", dstName, err)
	}
	defer util.Close(w)
	_, err = io.Copy(w, r)
	if err != nil {
		return fmt.Errorf("Failed to copy bytes for image %s: %s", dstName, err)
	}
	return nil
}

// DownloadAllSourceImages downloads all the images under gs://skia-fiddles/source/
// and copies them as PNG images under FIDDLE_ROOT/images/.
//
//    fiddleRoot - The root directory where fiddle is working. See DESIGN.md.
func (s *Store) DownloadAllSourceImages(fiddleRoot string) error {
	ctx := context.Background()
	q := &storage.Query{
		Prefix: fmt.Sprintf("source/"),
	}
	if err := os.MkdirAll(filepath.Join(fiddleRoot, "images"), 0755); err != nil {
		return fmt.Errorf("Failed to create images directory: %s", err)
	}
	it := s.bucket.Objects(ctx, q)
	for obj, err := it.Next(); err != iterator.Done; obj, err = it.Next() {
		if err != nil {
			return fmt.Errorf("Failed to retrieve image list: %s", err)
		}
		filename := strings.Split(obj.Name, "/")[1]
		dstFullPath := filepath.Join(fiddleRoot, "images", filename)
		if err := downloadSingleSourceImage(ctx, s.bucket, obj.Name, dstFullPath); err != nil {
			sklog.Errorf("Failed to download image %q: %s", obj.Name, err)
		}
	}
	return nil
}

// GetSourceImage downloads a single source image from the Google Storage bucket.
func (s *Store) GetSourceImage(i int) (image.Image, error) {
	ctx := context.Background()
	r, err := s.bucket.Object(fmt.Sprintf("source/%d.png", i)).NewReader(ctx)
	if err != nil {
		return nil, fmt.Errorf("Failed to open reader for image: %s", err)
	}
	defer util.Close(r)
	return png.Decode(r)
}

// ListSourceImages returns the ids of all the images under gs://skia-fiddles/source/.
func (s *Store) ListSourceImages() ([]int, error) {
	ret := []int{}
	ctx := context.Background()
	q := &storage.Query{
		Prefix: fmt.Sprintf("source/"),
	}
	it := s.bucket.Objects(ctx, q)
	for obj, err := it.Next(); err != iterator.Done; obj, err = it.Next() {
		if err != nil {
			return nil, fmt.Errorf("Failed to retrieve image list: %s", err)
		}
		filename := strings.Split(obj.Name, "/")[1]
		matches := sourceFileName.FindAllStringSubmatch(filename, -1)
		if len(matches) != 1 || len(matches[0]) != 2 {
			sklog.Infof("Filename %s is not a source image.", filename)
			continue
		}
		i, err := strconv.Atoi(matches[0][1])
		if err != nil {
			sklog.Errorf("Failed to parse souce image filename: %s", err)
			continue
		}
		ret = append(ret, i)
	}
	return ret, nil
}

// Named is the information about a named fiddle.
type Named struct {
	Name string
	User string
}

// ListAllNames returns the list of all named fiddles.
func (s *Store) ListAllNames() ([]Named, error) {
	ret := []Named{}
	ctx := context.Background()
	q := &storage.Query{
		Prefix: fmt.Sprintf("named/"),
	}
	it := s.bucket.Objects(ctx, q)
	for obj, err := it.Next(); err != iterator.Done; obj, err = it.Next() {
		if err != nil {
			return nil, fmt.Errorf("Failed to retrieve name list: %s", err)
		}
		filename := strings.Split(obj.Name, "/")[1]
		ret = append(ret, Named{
			Name: filename,
			User: obj.Metadata[USER_METADATA],
		})
	}
	return ret, nil
}

// GetHashFromName loads the fiddle hash for the given name.
func (s *Store) GetHashFromName(name string) (string, error) {
	ctx := context.Background()
	r, err := s.bucket.Object(fmt.Sprintf("named/%s", name)).NewReader(ctx)
	if err != nil {
		return "", fmt.Errorf("Failed to open reader for name %q: %s", name, err)
	}
	defer util.Close(r)
	b, err := ioutil.ReadAll(r)
	if err != nil {
		return "", fmt.Errorf("Failed to read named file %q: %s", name, err)
	}
	return string(b), nil
}

// WriteName writes the name file for a named fiddle.
//
//   name - The name of the fidde.
//   hash - The fiddle hash.
//   user - The email of the user that created the name.
func (s *Store) WriteName(name, hash, user string) error {
	ctx := context.Background()
	w := s.bucket.Object(fmt.Sprintf("named/%s", name)).NewWriter(ctx)
	defer util.Close(w)
	w.ObjectAttrs.Metadata = map[string]string{
		USER_METADATA: user,
	}
	if _, err := w.Write([]byte(hash)); err != nil {
		return fmt.Errorf("Failed to write named file %q: %s", name, err)
	}
	return nil
}

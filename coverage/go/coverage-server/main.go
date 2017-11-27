package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"
	"time"

	"google.golang.org/api/option"

	"cloud.google.com/go/storage"
	"github.com/gorilla/mux"
	"go.skia.org/infra/coverage/go/coverageingest"
	"go.skia.org/infra/coverage/go/db"
	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/gcs"
	"go.skia.org/infra/go/git/gitinfo"
	"go.skia.org/infra/go/httputils"
	"go.skia.org/infra/go/login"
	"go.skia.org/infra/go/skiaversion"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/vcsinfo"
)

const (
	// OAUTH2_CALLBACK_PATH is callback endpoint used for the Oauth2 flow.
	OAUTH2_CALLBACK_PATH = "/oauth2callback/"
)

var (
	extractDir = flag.String("extract_dir", "./extract", "The directory that the coverage data should be extracted to.")
	gitDir     = flag.String("git_dir", "./git", "The directory that the git repo should live in.")
	cachePath  = flag.String("cache_path", "./boltdb", "The path to where the cached coverage data should be stored.")

	ingestPeriod = flag.Duration("ingest_period", 10*time.Minute, "How often to check for new data.")

	local        = flag.Bool("local", false, "Running locally if true. As opposed to in production.")
	port         = flag.String("port", ":8080", "HTTP service port")
	resourcesDir = flag.String("resources_dir", "", "The directory to find templates, JS, and CSS files. If blank the current directory will be used.")
	promPort     = flag.String("prom_port", ":20000", "Metrics service address (e.g., ':10110')")
	nCommits     = flag.Int("n_commits", 50, "The last N commits to ingest coverage data for.")
	bucket       = flag.String("bucket", "skia-coverage", "The GCS bucket that will house the coverage data.")

	// OAUTH params
	authWhiteList = flag.String("auth_whitelist", login.DEFAULT_DOMAIN_WHITELIST, "White space separated list of domains and email addresses that are allowed to login.")
	redirectURL   = flag.String("redirect_url", "https://fuzzer.skia.org/oauth2callback/", "OAuth2 redirect url. Only used when local=false.")
)

var (
	coverageIngester coverageingest.Ingester = nil

	storageClient *storage.Client = nil
)

func main() {
	flag.Parse()
	defer common.LogPanic()

	if *local {
		common.InitWithMust(
			"coverage",
			common.PrometheusOpt(promPort),
		)
	} else {
		common.InitWithMust(
			"coverage",
			common.PrometheusOpt(promPort),
			common.CloudLoggingOpt(),
		)
	}

	ctx := context.Background()
	if err := setupFileIngestion(ctx); err != nil {
		sklog.Fatalf("Could not set up ingestion: %s", err)
	}

	useRedirectURL := fmt.Sprintf("http://localhost%s/oauth2callback/", *port)
	if !*local {
		useRedirectURL = *redirectURL
	}
	if err := login.Init(useRedirectURL, *authWhiteList); err != nil {
		sklog.Fatalf("Problem setting up server OAuth: %s", err)
	}

	r := mux.NewRouter()
	r.PathPrefix("/res/").HandlerFunc(httputils.MakeResourceHandler(*resourcesDir))

	r.HandleFunc(OAUTH2_CALLBACK_PATH, login.OAuth2CallbackHandler)
	r.HandleFunc("/", getIndexHandler())
	r.HandleFunc("/coverage", getPageHandler())
	r.HandleFunc("/ingested", ingestedHandler)
	r.HandleFunc("/loginstatus/", login.StatusHandler)
	r.HandleFunc("/logout/", login.LogoutHandler)
	r.HandleFunc("/json/version", skiaversion.JsonHandler)

	r.PathPrefix("/cov_html/").Handler(http.StripPrefix("/cov_html/", http.FileServer(http.Dir(*extractDir))))

	rootHandler := httputils.LoggingGzipRequestResponse(r)

	http.Handle("/", rootHandler)
	sklog.Infof("Ready to serve on http://127.0.0.1%s", *port)
	sklog.Fatal(http.ListenAndServe(*port, nil))

}

// getIndexHandler returns a handler that displays the index page, which has no
// real templating. The client side JS will query for more information.
func getIndexHandler() func(http.ResponseWriter, *http.Request) {
	tempFiles := []string{
		filepath.Join(*resourcesDir, "templates/index.html"),
		filepath.Join(*resourcesDir, "templates/header.html"),
	}

	indexTemplate := template.Must(template.ParseFiles(tempFiles...))

	return func(w http.ResponseWriter, r *http.Request) {
		if *local {
			indexTemplate = template.Must(template.ParseFiles(tempFiles...))
		}
		w.Header().Set("Content-Type", "text/html")

		if err := indexTemplate.Execute(w, nil); err != nil {
			sklog.Errorf("Failed to expand template: %s", err)
		}
	}
}

// getPageHandler returns a handler that displays the coverage page, which has no
// real templating. The client side JS will make a request to cov_html for an iframe.
func getPageHandler() func(http.ResponseWriter, *http.Request) {
	tempFiles := []string{
		filepath.Join(*resourcesDir, "templates/coverage-page.html"),
		filepath.Join(*resourcesDir, "templates/header.html"),
	}

	indexTemplate := template.Must(template.ParseFiles(tempFiles...))

	return func(w http.ResponseWriter, r *http.Request) {
		if *local {
			indexTemplate = template.Must(template.ParseFiles(tempFiles...))
		}
		w.Header().Set("Content-Type", "text/html")

		if err := indexTemplate.Execute(w, nil); err != nil {
			sklog.Errorf("Failed to expand template: %s", err)
		}
	}
}

// ingestedHandler returns a list of commits and completed coverage tasks as JSON.
func ingestedHandler(w http.ResponseWriter, r *http.Request) {
	if coverageIngester == nil {
		http.Error(w, "Server not ready yet", http.StatusServiceUnavailable)
		return
	}
	type list struct {
		List []coverageingest.IngestedResults `json:"list"`
	}
	summary := list{List: coverageIngester.GetResults()}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(summary); err != nil {
		sklog.Errorf("Failed to write or encode output: %s", err)
		return
	}
}

// setupFileIngestion begins a background goroutine to occasionally check GCS for
// completed coverage tasks and ingest their data.
func setupFileIngestion(ctx context.Context) error {
	sklog.Info("Checking out skia")
	repo, err := gitinfo.CloneOrUpdate(ctx, common.REPO_SKIA, filepath.Join(*gitDir, "skia"), false)
	if err != nil {
		return fmt.Errorf("Could not clone skia repo: %s", err)
	}

	client, err := auth.NewDefaultJWTServiceAccountClient(auth.SCOPE_READ_ONLY)
	if err != nil {
		return fmt.Errorf("Problem setting up client OAuth: %s", err)
	}

	storageClient, err = storage.NewClient(ctx, option.WithHTTPClient(client))
	if err != nil {
		return fmt.Errorf("Problem authenticating: %s", err)
	}

	gcsClient := gcs.NewGCSClient(storageClient, *bucket)
	boltDB, err := db.NewBoltDB(*cachePath)
	if err != nil {
		return fmt.Errorf("could not set up bolt db cache: %s", err)
	}
	coverageIngester = coverageingest.New(*extractDir, gcsClient, boltDB)

	cycle := func(v vcsinfo.VCS, coverageIngester coverageingest.Ingester) {
		sklog.Info("Begin coverage ingest cycle")
		if err := v.Update(ctx, true, false); err != nil {
			sklog.Warningf("Could not update git repo, but continuing anyway: %s", err)
		}
		commits := []*vcsinfo.LongCommit{}
		for _, c := range v.LastNIndex(*nCommits) {
			lc, err := v.Details(ctx, c.Hash, false)
			if err != nil {
				sklog.Errorf("Could not get commit info for git revision %s: %s", c.Hash, err)
				continue
			}
			// Reverse the order so the most recent commit is first
			commits = append([]*vcsinfo.LongCommit{lc}, commits...)
		}
		coverageIngester.IngestCommits(ctx, commits)
		sklog.Info("End coverage ingest cycle")
	}

	go func(v vcsinfo.VCS, coverageIngester coverageingest.Ingester) {
		cycle(repo, coverageIngester)
		for range time.Tick(*ingestPeriod) {
			cycle(repo, coverageIngester)
		}
	}(repo, coverageIngester)
	return nil
}

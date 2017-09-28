package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/mux"

	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/httputils"
	"go.skia.org/infra/go/login"
	"go.skia.org/infra/go/promalertsclient"
	"go.skia.org/infra/go/skiaversion"
	"go.skia.org/infra/go/sklog"
	skswarming "go.skia.org/infra/go/swarming"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/power/go/decider"
	"go.skia.org/infra/power/go/gatherer"
	"go.skia.org/infra/power/go/recorder"
)

const (
	// OAUTH2_CALLBACK_PATH is callback endpoint used for the Oauth2 flow.
	OAUTH2_CALLBACK_PATH = "/oauth2callback/"
)

var downBots gatherer.Gatherer = nil
var fixRecorder recorder.Recorder = nil

var (
	// web server params
	port           = flag.String("port", ":8080", "HTTP service port")
	local          = flag.Bool("local", false, "Running locally if true. As opposed to in production.")
	resourcesDir   = flag.String("resources_dir", "", "The directory to find templates, JS, and CSS files. If blank the current directory will be used.")
	promPort       = flag.String("prom_port", ":20000", "Metrics service address (e.g., ':10110')")
	alertsEndpoint = flag.String("alerts_endpoint", "skia-prom:8001", "The Prometheus GCE name and port")

	// OAUTH params
	authWhiteList = flag.String("auth_whitelist", login.DEFAULT_DOMAIN_WHITELIST, "White space separated list of domains and email addresses that are allowed to login.")
	redirectURL   = flag.String("redirect_url", "https://power.skia.org/oauth2callback/", "OAuth2 redirect url. Only used when local=false.")

	powercycleConfigs = common.NewMultiStringFlag("powercycle_config", nil, "JSON5 file with powercycle bot/device configuration. Same as used for powercycle.")
	updatePeriod      = flag.Duration("update_period", time.Minute, "How often to update the list of down bots.")
	authorizedEmails  = common.NewMultiStringFlag("authorized_email", nil, "Email addresses of users who are authorized to post to this web service.")
)

func main() {
	flag.Parse()
	defer common.LogPanic()

	if *local {
		common.InitWithMust(
			"power-controller",
			common.PrometheusOpt(promPort),
		)
	} else {
		common.InitWithMust(
			"power-controller",
			common.PrometheusOpt(promPort),
			common.CloudLoggingOpt(),
		)
	}

	if err := setupGatherer(); err != nil {
		sklog.Fatalf("Could not set up down bot gatherer: %s", err)
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
	r.HandleFunc("/loginstatus/", login.StatusHandler)
	r.HandleFunc("/logout/", login.LogoutHandler)
	r.HandleFunc("/json/version", skiaversion.JsonHandler)
	r.HandleFunc("/down_bots", downBotsHandler)
	r.HandleFunc("/powercycled_bots", powercycledBotsHandler)

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
			sklog.Errorf("Failed to expand template: %v", err)
		}
	}
}

type downBotsResponse struct {
	List []gatherer.DownBot `json:"list"`
}

func downBotsHandler(w http.ResponseWriter, r *http.Request) {
	if downBots == nil {
		http.Error(w, "The power-controller isn't finished booting up.  Try again later", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	response := downBotsResponse{List: downBots.DownBots()}
	if err := json.NewEncoder(w).Encode(response); err != nil {
		sklog.Errorf("Failed to write or encode output: %s", err)
		return
	}
}

// powercycledBotsHandler is the way that the powercycle daemons can talk
// to the server and report that they have powercycled.
func powercycledBotsHandler(w http.ResponseWriter, r *http.Request) {
	a := r.Header.Get("Authorization")
	if a == "" {
		http.Error(w, "Missing Authorization Header", 403)
		return
	}
	// Check Authentication, i.e. the claimed POST actually comes from
	// the entity it says it is from and has not expired..
	ti, err := auth.ValidateBearerToken(strings.TrimPrefix(a, "Bearer "))
	if err != nil {
		sklog.Errorf("Could not auth: %s", err)
		http.Error(w, "Invalid Authentication", 403)
		return
	}
	// We know the token is valid, make sure the bearer of the token is
	// on the whitelist of entities who we allow to post.
	if !util.In(ti.Email, *authorizedEmails) {
		http.Error(w, "Not on authorized whitelist", 403)
		return
	}

	var input struct {
		PowercycledBots []string `json:"powercycled_bots"`
	}

	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		httputils.ReportError(w, r, err, fmt.Sprintf("Failed to decode request body: %s", err))
		return
	}
	sklog.Infof("%s reported they powercycled %q", ti.Email, input.PowercycledBots)
	fixRecorder.PowercycledBots(ti.Email, input.PowercycledBots)
	w.WriteHeader(http.StatusAccepted)
}

func setupGatherer() error {
	oauthCacheFile := path.Join(*resourcesDir, "google_storage_token.data")
	authedClient, err := auth.NewClient(*local, oauthCacheFile, skswarming.AUTH_SCOPE)
	if err != nil {
		return fmt.Errorf("Could not set up autheticated HTTP client: %s", err)
	}

	es, err := skswarming.NewApiClient(authedClient, "chromium-swarm.appspot.com")
	if err != nil {
		return err
	}
	is, err := skswarming.NewApiClient(authedClient, "chrome-swarming.appspot.com")
	if err != nil {
		return fmt.Errorf("Could not get ApiClient for chrome-swarming: %s", err)
	}
	ac := promalertsclient.New(&http.Client{}, *alertsEndpoint)
	d, hostMap, err := decider.New(*powercycleConfigs)
	if err != nil {
		return fmt.Errorf("Could not initialize down bot decider: %s", err)
	}

	fixRecorder = recorder.NewCloudLoggingRecorder()
	downBots = gatherer.NewPollingGatherer(es, is, ac, d, fixRecorder, hostMap, *updatePeriod)

	return nil
}

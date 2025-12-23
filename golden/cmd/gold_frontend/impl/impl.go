package impl

// The goldfrontend executable is the process that exposes a RESTful API used by the JS frontend.

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v4/pgxpool"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gstorage "google.golang.org/api/storage/v1"

	"go.goldmine.build/go/alogin"
	"go.goldmine.build/go/alogin/proxylogin"
	"go.goldmine.build/go/auth"
	"go.goldmine.build/go/gerrit"
	"go.goldmine.build/go/httputils"
	"go.goldmine.build/go/metrics2"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/go/clstore"
	"go.goldmine.build/golden/go/code_review"
	"go.goldmine.build/golden/go/code_review/gerrit_crs"
	"go.goldmine.build/golden/go/code_review/github_crs"
	"go.goldmine.build/golden/go/config"
	"go.goldmine.build/golden/go/db"
	"go.goldmine.build/golden/go/ignore"
	"go.goldmine.build/golden/go/ignore/sqlignorestore"
	"go.goldmine.build/golden/go/publicparams"
	"go.goldmine.build/golden/go/search"
	"go.goldmine.build/golden/go/storage"
	"go.goldmine.build/golden/go/web"
	"go.goldmine.build/golden/go/web/frontend"
)

func FrontendMain(ctx context.Context, cfg config.Common, flags config.ServerFlags) {
	client := mustMakeAuthenticatedHTTPClient(cfg.Local)

	sqlDB := db.MustInitSQLDatabase(ctx, cfg, flags.LogSQLQueries)

	gsClient := mustMakeGCSClient(ctx, cfg, client)

	publiclyViewableParams := mustMakePubliclyViewableParams(cfg)

	ignoreStore := mustMakeIgnoreStore(ctx, sqlDB)

	reviewSystems := mustInitializeReviewSystems(cfg, client)

	s2a := mustLoadSearchAPI(ctx, cfg, sqlDB, publiclyViewableParams, reviewSystems)

	plogin, err := proxylogin.New(
		cfg.FrontendServerConfig.ProxyLoginHeaderName,
		cfg.FrontendServerConfig.ProxyLoginEmailRegex,
		cfg.FrontendServerConfig.BypassRoles)
	if err != nil {
		sklog.Fatalf("proxylogin configuration: %s", err)
	}

	handlers := mustMakeWebHandlers(ctx, cfg, sqlDB, gsClient, ignoreStore, reviewSystems, s2a, plogin)

	rootRouter := mustMakeRootRouter(cfg, handlers, plogin)

	// Start the server
	sklog.Infof("Serving on http://127.0.0.1" + flags.Port)
	sklog.Fatal(http.ListenAndServe(flags.Port, rootRouter))
}

func mustLoadSearchAPI(ctx context.Context, cfg config.Common, sqlDB *pgxpool.Pool, publiclyViewableParams publicparams.Matcher, systems []clstore.ReviewSystem) *search.Impl {
	templates := map[string]string{}
	for _, crs := range systems {
		templates[crs.ID] = crs.URLTemplate
	}

	s2a := search.New(sqlDB, cfg.WindowSize)
	s2a.SetReviewSystemTemplates(templates)
	sklog.Infof("SQL Search loaded with CRS templates %s", templates)
	err := s2a.StartCacheProcess(ctx, 5*time.Minute, cfg.WindowSize)
	if err != nil {
		sklog.Fatalf("Cannot load caches for search2 backend: %s", err)
	}
	if err := s2a.StartMaterializedViews(ctx, cfg.FrontendServerConfig.MaterializedViewCorpora, 5*time.Minute); err != nil {
		sklog.Fatalf("Cannot create materialized views %s: %s", cfg.FrontendServerConfig.MaterializedViewCorpora, err)
	}
	if cfg.FrontendServerConfig.IsPublicView {
		if err := s2a.StartApplyingPublicParams(ctx, publiclyViewableParams, 5*time.Minute); err != nil {
			sklog.Fatalf("Could not apply public params: %s", err)
		}
		sklog.Infof("Public params applied to search2")
	}

	return s2a
}

// mustMakeAuthenticatedHTTPClient returns an http.Client with the credentials required by the
// services that Gold communicates with.
func mustMakeAuthenticatedHTTPClient(local bool) *http.Client {
	// Get the token source for the service account with access to the services
	// we need to operate.
	tokenSource, err := google.DefaultTokenSource(context.TODO(), auth.ScopeUserinfoEmail, gstorage.CloudPlatformScope, auth.ScopeGerrit)
	if err != nil {
		sklog.Fatalf("Failed to authenticate service account: %s", err)
	}
	return httputils.DefaultClientConfig().WithTokenSource(tokenSource).Client()
}

// mustMakeGCSClient returns a storage.GCSClient that uses the given http.Client. If the Gold
// instance is not authoritative (e.g. when running locally) the client won't actually write any
// files.
func mustMakeGCSClient(ctx context.Context, cfg config.Common, client *http.Client) storage.GCSClient {
	gsClientOpt := storage.GCSClientOptions{
		Bucket:             cfg.GCSBucket,
		KnownHashesGCSPath: cfg.KnownHashesGCSPath,
		Dryrun:             !cfg.IsAuthoritative(),
	}

	gsClient, err := storage.NewGCSClient(ctx, client, gsClientOpt)
	if err != nil {
		sklog.Fatalf("Unable to create GCSClient: %s", err)
	}

	return gsClient
}

// mustMakePubliclyViewableParams validates and computes a publicparams.Matcher from the publicly
// allowed params specified in the JSON configuration files.
func mustMakePubliclyViewableParams(cfg config.Common) publicparams.Matcher {
	var publiclyViewableParams publicparams.Matcher
	var err error

	// Load the publiclyViewable params if configured and disable querying for issues.
	if len(cfg.FrontendServerConfig.PubliclyAllowableParams) > 0 {
		if publiclyViewableParams, err = publicparams.MatcherFromRules(cfg.FrontendServerConfig.PubliclyAllowableParams); err != nil {
			sklog.Fatalf("Could not load list of public params: %s", err)
		}
	}

	// Check if this is public instance. If so, make sure we have a non-nil Matcher.
	if cfg.FrontendServerConfig.IsPublicView && publiclyViewableParams == nil {
		sklog.Fatal("A non-empty map of publiclyViewableParams must be provided if is public view.")
	}

	return publiclyViewableParams
}

// mustMakeIgnoreStore returns a new ignore.Store and starts a monitoring routine that counts the
// the number of expired ignore rules and exposes this as a metric.
func mustMakeIgnoreStore(ctx context.Context, db *pgxpool.Pool) ignore.Store {
	ignoreStore := sqlignorestore.New(db)

	if err := ignore.StartMetrics(ctx, ignoreStore, 5*time.Minute); err != nil {
		sklog.Fatalf("Failed to start monitoring for expired ignore rules: %s", err)
	}
	return ignoreStore
}

// mustInitializeReviewSystems validates and instantiates one clstore.ReviewSystem for each CRS
// specified via the JSON configuration files.
func mustInitializeReviewSystems(cfg config.Common, hc *http.Client) []clstore.ReviewSystem {
	rs := make([]clstore.ReviewSystem, 0, len(cfg.CodeReviewSystems))
	for _, cfg := range cfg.CodeReviewSystems {
		var crs code_review.Client
		if cfg.Flavor == "gerrit" {
			if cfg.GerritURL == "" {
				sklog.Fatal("You must specify gerrit_url")
				return nil
			}
			gerritClient, err := gerrit.NewGerrit(cfg.GerritURL, hc)
			if err != nil {
				sklog.Fatalf("Could not create gerrit client for %s", cfg.GerritURL)
				return nil
			}
			crs = gerrit_crs.New(gerritClient)
		} else if cfg.Flavor == "github" {
			if cfg.GitHubRepo == "" || cfg.GitHubCredPath == "" {
				sklog.Fatal("You must specify github_repo and github_cred_path")
				return nil
			}
			gBody, err := os.ReadFile(cfg.GitHubCredPath)
			if err != nil {
				sklog.Fatalf("Couldn't find githubToken in %s: %s", cfg.GitHubCredPath, err)
				return nil
			}
			gToken := strings.TrimSpace(string(gBody))
			githubTS := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: gToken})
			c := httputils.DefaultClientConfig().With2xxOnly().WithTokenSource(githubTS).Client()
			crs = github_crs.New(c, cfg.GitHubRepo)
		} else {
			sklog.Fatalf("CRS flavor %s not supported.", cfg.Flavor)
			return nil
		}
		rs = append(rs, clstore.ReviewSystem{
			ID:          cfg.ID,
			Client:      crs,
			URLTemplate: cfg.URLTemplate,
		})
	}
	return rs
}

// mustMakeWebHandlers returns a new web.Handlers.
func mustMakeWebHandlers(ctx context.Context, cfg config.Common, db *pgxpool.Pool, gsClient storage.GCSClient, ignoreStore ignore.Store, reviewSystems []clstore.ReviewSystem, s2a search.API, alogin alogin.Login) *web.Handlers {
	handlers, err := web.NewHandlers(web.HandlersConfig{
		DB:                        db,
		GCSClient:                 gsClient,
		IgnoreStore:               ignoreStore,
		ReviewSystems:             reviewSystems,
		Search2API:                s2a,
		WindowSize:                cfg.WindowSize,
		GroupingParamKeysByCorpus: cfg.GroupingParamKeysByCorpus,
	}, web.FullFrontEnd, alogin)
	if err != nil {
		sklog.Fatalf("Failed to initialize web handlers: %s", err)
	}
	handlers.StartCacheWarming(ctx)
	return handlers
}

// mustMakeRootRouter returns a chi.Router that can be used to serve Gold's web UI and JSON API.
func mustMakeRootRouter(cfg config.Common, handlers *web.Handlers, plogin alogin.Login) chi.Router {
	rootRouter := chi.NewRouter()
	rootRouter.HandleFunc("/healthz", httputils.ReadyHandleFunc)

	// loggedRouter contains all the endpoints that are logged. See the call below to
	// LoggingGzipRequestResponse.
	loggedRouter := chi.NewRouter()

	loggedRouter.HandleFunc("/_/login/status", alogin.LoginStatusHandler(plogin))

	// JSON endpoints.
	addAuthenticatedJSONRoutes(loggedRouter, cfg, handlers, plogin)
	addUnauthenticatedJSONRoutes(rootRouter, cfg, handlers)

	// Routes to serve the UI, static assets, etc.
	addUIRoutes(loggedRouter, cfg, handlers, plogin)

	// set up the app router that might be authenticated and logs almost everything.
	appRouter := chi.NewRouter()
	// Images should not be served gzipped as PNGs typically have zlib compression anyway.
	appRouter.Get("/img/*", handlers.ImageHandler)
	appRouter.Handle("/*", httputils.LoggingGzipRequestResponse(loggedRouter))

	appHandler := http.Handler(appRouter)

	// The appHandler contains all application specific routes that are have logging and
	// authentication configured. Now we wrap it into the router that is exposed to the host
	// (aka the K8s container) which requires that some routes are never logged or authenticated.
	rootRouter.Handle("/*", appHandler)

	return rootRouter
}

// addUIRoutes adds the necessary routes to serve Gold's web pages and static assets such as JS and
// CSS bundles, static images (digest and diff images are handled elsewhere), etc.
func addUIRoutes(router chi.Router, cfg config.Common, handlers *web.Handlers, plogin alogin.Login) {
	// Serve static assets (JS and CSS bundles, images, etc.).
	//
	// Note that this includes the raw HTML templates (e.g. /dist/byblame.html) with unpopulated
	// placeholders such as {{.Title}}. These aren't used directly by client code. We should probably
	// unexpose them and only serve the JS/CSS bundles from this route (and any other static assets
	// such as the favicon).
	router.Handle("/dist/*", http.StripPrefix("/dist/", http.HandlerFunc(makeResourceHandler(cfg.FrontendServerConfig.ResourcesPath))))

	var templates *template.Template

	loadTemplates := func() {
		templates = template.Must(template.New("").ParseGlob(filepath.Join(cfg.FrontendServerConfig.ResourcesPath, "*.html")))
	}

	loadTemplates()

	cfg.FrontendServerConfig.FrontendConfig.IsPublic = cfg.FrontendServerConfig.IsPublicView

	frontendConfigBytes, err := json.Marshal(cfg.FrontendServerConfig.FrontendConfig)
	if err != nil {
		sklog.Error("Failed to marshal frontend config to JSON: %s", err)
	}

	templateHandler := func(name string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if cfg.FrontendServerConfig.ForceLogin && len(plogin.Roles(r)) == 0 {
				http.Redirect(w, r, plogin.LoginURL(r), http.StatusSeeOther)
				return
			}
			w.Header().Set("Content-Type", "text/html")

			// Reload the template if we are running locally.
			if cfg.Local {
				loadTemplates()
			}

			templateData := struct {
				Title        string
				GoldSettings template.JS
			}{
				Title:        cfg.FrontendServerConfig.FrontendConfig.Title,
				GoldSettings: template.JS(frontendConfigBytes),
			}
			if err := templates.ExecuteTemplate(w, name, templateData); err != nil {
				sklog.Errorf("Failed to expand template %s : %s", name, err)
				return
			}
		}
	}

	// These routes serve the web UI.
	router.HandleFunc("/", templateHandler("byblame.html"))
	router.HandleFunc("/changelists", templateHandler("changelists.html"))
	router.HandleFunc("/cluster", templateHandler("cluster.html"))
	router.HandleFunc("/triagelog", templateHandler("triagelog.html"))
	router.HandleFunc("/ignores", templateHandler("ignorelist.html"))
	router.HandleFunc("/diff", templateHandler("diff.html"))
	router.HandleFunc("/detail", templateHandler("details.html"))
	router.HandleFunc("/details", templateHandler("details.html"))
	router.HandleFunc("/list", templateHandler("by_test_list.html"))
	router.HandleFunc("/help", templateHandler("help.html"))
	router.HandleFunc("/search", templateHandler("search.html"))
	router.HandleFunc("/cl/{system}/{id}", handlers.ChangelistSearchRedirect)
}

// addAuthenticatedJSONRoutes populates the given router with the subset of Gold's JSON RPC routes
// that require authentication.
func addAuthenticatedJSONRoutes(router chi.Router, cfg config.Common, handlers *web.Handlers, plogin alogin.Login) {
	// Set up a subrouter for the '/json' routes which make up the Gold API.
	// This makes routing faster, but also returns a failure when an /json route is
	// requested that doesn't exist. If we did this differently a call to a non-existing endpoint
	// would be handled by the route that handles the returning the index template and make
	// debugging confusing.
	pathPrefix := "/json"
	jsonRouter := router.Route(pathPrefix, func(r chi.Router) {})

	add := func(jsonRoute string, handlerToProtect http.HandlerFunc, method string) {
		wrappedHandler := func(w http.ResponseWriter, r *http.Request) {
			// Any role is >= Viewer
			if cfg.FrontendServerConfig.ForceLogin && len(plogin.Roles(r)) == 0 {
				http.Error(w, "You must be logged in as a viewer to complete this action.", http.StatusUnauthorized)
				return
			}
			handlerToProtect(w, r)
		}
		addJSONRoute(method, jsonRoute, wrappedHandler, jsonRouter, pathPrefix)
	}

	add("/json/v2/byblame", handlers.ByBlameHandler, "GET")
	add("/json/v2/changelists", handlers.ChangelistsHandler, "GET")
	add("/json/v2/clusterdiff", handlers.ClusterDiffHandler, "GET")
	add("/json/v2/commits", handlers.CommitsHandler, "GET")
	add("/json/v1/positivedigestsbygrouping/{groupingID}", handlers.PositiveDigestsByGroupingIDHandler, "GET")
	add("/json/v2/details", handlers.DetailsHandler, "POST")
	add("/json/v2/diff", handlers.DiffHandler, "POST")
	add("/json/v2/digests", handlers.DigestListHandler, "GET")
	add("/json/v2/latestpositivedigest/{traceID}", handlers.LatestPositiveDigestHandler, "GET")
	add("/json/v2/list", handlers.ListTestsHandler, "GET")
	add("/json/v2/paramset", handlers.ParamsHandler, "GET")
	add("/json/v2/search", handlers.SearchHandler, "GET")
	add("/json/v2/triage", handlers.TriageHandlerV2, "POST") // TODO(lovisolo): Delete when unused.
	add("/json/v3/triage", handlers.TriageHandlerV3, "POST")
	add("/json/v2/triagelog", handlers.TriageLogHandler, "GET")
	add("/json/v2/triagelog/undo", handlers.TriageUndoHandler, "POST")
	add("/json/whoami", handlers.Whoami, "GET")
	add("/json/v1/whoami", handlers.Whoami, "GET")
	// TODO(lovisolo): Delete once all links to details page include grouping information.
	add("/json/v1/groupingfortest", handlers.GroupingForTestHandler, "POST")

	// Only expose these endpoints if this instance is not a public view. The reason we want to hide
	// ignore rules is so that we don't leak params that might be in them.
	if !cfg.FrontendServerConfig.IsPublicView {
		add("/json/v2/ignores", handlers.ListIgnoreRules2, "GET")
		add("/json/ignores/add/", handlers.AddIgnoreRule, "POST")
		add("/json/v1/ignores/add/", handlers.AddIgnoreRule, "POST")
		add("/json/ignores/del/{id}", handlers.DeleteIgnoreRule, "POST")
		add("/json/v1/ignores/del/{id}", handlers.DeleteIgnoreRule, "POST")
		add("/json/ignores/save/{id}", handlers.UpdateIgnoreRule, "POST")
		add("/json/v1/ignores/save/{id}", handlers.UpdateIgnoreRule, "POST")
	}

	// Make sure we return a 404 for anything that starts with /json and could not be found.
	jsonRouter.HandleFunc("/{ignore:.*}", http.NotFound)
	router.HandleFunc(pathPrefix, http.NotFound)
}

// addUnauthenticatedJSONRoutes populates the given router with the subset of Gold's JSON RPC routes
// that do not require authentication.
func addUnauthenticatedJSONRoutes(router chi.Router, _ config.Common, handlers *web.Handlers) {
	add := func(jsonRoute string, handlerFunc http.HandlerFunc) {
		addJSONRoute("GET", jsonRoute, httputils.CorsHandler(handlerFunc), router, "")
	}

	add("/json/v2/trstatus", handlers.StatusHandler)
	add("/json/v2/changelist/{system}/{id}", handlers.PatchsetsAndTryjobsForCL2)
	add("/json/v1/changelist_summary/{system}/{id}", handlers.ChangelistSummaryHandler)

	// Routes shared with the baseline server. These usually don't see traffic because the envoy
	// routing directs these requests to the baseline servers, if there are some.
	add(frontend.KnownHashesRoute, handlers.KnownHashesHandler)
	add(frontend.KnownHashesRouteV1, handlers.KnownHashesHandler)
	// Retrieving a baseline for the primary branch and a Gerrit issue are handled the same way.
	// These routes can be served with baseline_server for higher availability.
	add(frontend.ExpectationsRouteV2, handlers.BaselineHandlerV2)
	add(frontend.GroupingsRouteV1, handlers.GroupingsHandler)
}

var (
	unversionedJSONRouteRegexp = regexp.MustCompile(`/json/(?P<path>.+)`)
	versionedJSONRouteRegexp   = regexp.MustCompile(`/json/v(?P<version>\d+)/(?P<path>.+)`)
)

// addJSONRoute adds a handler function to a router for the given JSON RPC route, which must be of
// the form "/json/<path>" or "/json/v<n>/<path>", and increases a counter to track RPC and version
// usage every time the RPC is invoked.
//
// If the given routerPathPrefix is non-empty, it will be removed from the JSON RPC route before the
// handler function is added to the router (useful with subrouters for path prefixes, e.g. "/json").
//
// It panics if jsonRoute does not start with '/json', or if the routerPathPrefix is not a prefix of
// the jsonRoute, or if the jsonRoute uses version 0 (e.g. /json/v0/foo), which is reserved for
// unversioned RPCs.
//
// This function has been designed to take the full JSON RPC route as an argument, including the
// RPC version number and the subrouter path prefix, if any (e.g. "/json/v2/my/rpc" vs. "/my/rpc").
// This results in clearer code at the callsite because the reader can immediately see what the
// final RPC route will look like from outside the HTTP server.
func addJSONRoute(method, jsonRoute string, handlerFunc http.HandlerFunc, router chi.Router, routerPathPrefix string) {
	// Make sure the jsonRoute agrees with the router path prefix (which can be the empty string).
	if !strings.HasPrefix(jsonRoute, routerPathPrefix) {
		panic(fmt.Sprintf(`Prefix "%s" not found in JSON RPC route: %s`, routerPathPrefix, jsonRoute))
	}

	// Parse the JSON RPC route, which can be of the form "/json/v<n>/<path>" or "/json/<path>", and
	// extract <path> and <n>, defaulting to 0 for the unversioned case.
	var path string
	version := 0 // Default value is used for unversioned JSON RPCs.
	if matches := versionedJSONRouteRegexp.FindStringSubmatch(jsonRoute); matches != nil {
		var err error
		version, err = strconv.Atoi(matches[1])
		if err != nil {
			// Should never happen.
			panic("Failed to convert RPC version to integer (indicates a bug in the regexp): " + jsonRoute)
		}
		if version == 0 {
			// Disallow /json/v0/* because we indicate unversioned RPCs with version 0.
			panic("JSON RPC version cannot be 0: " + jsonRoute)
		}
		path = matches[2]
	} else if matches := unversionedJSONRouteRegexp.FindStringSubmatch(jsonRoute); matches != nil {
		path = matches[1]
	} else {
		// The path is neither a versioned nor an unversioned JSON RPC route. This is a coding error.
		panic("Unrecognized JSON RPC route format: " + jsonRoute)
	}

	counter := metrics2.GetCounter(web.RPCCallCounterMetric, map[string]string{
		"route":   "/" + path,
		"version": fmt.Sprintf("v%d", version),
	})

	pattern := strings.TrimPrefix(jsonRoute, routerPathPrefix)
	fn := func(w http.ResponseWriter, r *http.Request) {
		counter.Inc(1)
		handlerFunc(w, r)
	}

	switch method {
	case "GET":
		router.Get(pattern, fn)
	case "POST":
		router.Post(pattern, fn)
	default:
		panic(fmt.Sprintf("unknown method: %s", method))
	}
}

// makeResourceHandler creates a static file handler that sets a caching policy.
func makeResourceHandler(resourceDir string) func(http.ResponseWriter, *http.Request) {
	fileServer := http.FileServer(http.Dir(resourceDir))
	return func(w http.ResponseWriter, r *http.Request) {
		// No limit for anon users - this should be fast enough to handle a large load.
		w.Header().Add("Cache-Control", "max-age=300")
		fileServer.ServeHTTP(w, r)
	}
}

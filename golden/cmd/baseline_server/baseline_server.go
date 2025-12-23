// This program serves content that is mostly static and needs to be highly
// available. The content comes from highly available backend services like
// GCS. It needs to be deployed in a redundant way to ensure high uptime.
// It is read-only; it does not create new baselines or update expectations.
package main

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"golang.org/x/oauth2/google"
	gstorage "google.golang.org/api/storage/v1"

	"go.goldmine.build/go/alogin/proxylogin"
	"go.goldmine.build/go/common"
	"go.goldmine.build/go/httputils"
	"go.goldmine.build/go/metrics2"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/golden/go/clstore"
	"go.goldmine.build/golden/go/config"
	"go.goldmine.build/golden/go/db"
	"go.goldmine.build/golden/go/storage"
	"go.goldmine.build/golden/go/web"
	"go.goldmine.build/golden/go/web/frontend"
)

var flags config.ServerFlags

func main() {
	common.InitWithMust(
		"gold-ingestion",
		common.PrometheusOpt(&flags.PromPort),
		common.FlagSetOpt((&flags).Flagset()),
	)

	if flags.Hang {
		sklog.Info("Hanging")
		select {}
	}

	var cfg config.Common
	cfg, err := config.LoadConfigFromJSON5(flags.ConfigPath)
	if err != nil {
		sklog.Fatalf("Reading config: %s", err)
	}
	sklog.Infof("Loaded config %#v", cfg)

	if flags.Hang {
		sklog.Info("Hanging")
		select {}
	}

	ctx := context.Background()
	db := db.MustInitSQLDatabase(ctx, cfg, flags.LogSQLQueries)

	_, appName := filepath.Split(os.Args[0])
	common.InitWithMust(
		appName,
		common.PrometheusOpt(&cfg.PromPort),
	)

	gsClientOpt := storage.GCSClientOptions{
		Bucket:             cfg.GCSBucket,
		KnownHashesGCSPath: cfg.KnownHashesGCSPath,
	}

	tokenSource, err := google.DefaultTokenSource(ctx, gstorage.CloudPlatformScope)
	if err != nil {
		sklog.Fatalf("Could not create token source: %s", err)
	}

	client := httputils.DefaultClientConfig().WithTokenSource(tokenSource).Client()

	gsClient, err := storage.NewGCSClient(ctx, client, gsClientOpt)
	if err != nil {
		sklog.Fatalf("Unable to create GCSClient: %s", err)
	}

	// Baselines just need a list of valid CRS; we can leave all other fields blank.
	var reviewSystems []clstore.ReviewSystem
	for _, cfg := range cfg.CodeReviewSystems {
		reviewSystems = append(reviewSystems, clstore.ReviewSystem{ID: cfg.ID})
	}

	// We only need to fill in the HandlersConfig struct with the following subset, since the baseline
	// server only supplies a subset of the functionality.
	handlers, err := web.NewHandlers(web.HandlersConfig{
		DB:                        db,
		GCSClient:                 gsClient,
		ReviewSystems:             reviewSystems,
		GroupingParamKeysByCorpus: cfg.GroupingParamKeysByCorpus,
	}, web.BaselineSubset, proxylogin.NewWithDefaults())
	if err != nil {
		sklog.Fatalf("Failed to initialize web handlers: %s", err)
	}

	handlers.StartKnownHashesCacheProcess(ctx)

	// Set up a router for all the application endpoints which are part of the Gold API.
	appRouter := chi.NewRouter()

	// Version 0 of the routes are actually the unversioned legacy versions of the route.
	v0 := func(method, rpcRoute string, handlerFunc http.HandlerFunc) {
		counter := metrics2.GetCounter(web.RPCCallCounterMetric, map[string]string{
			// For consistency, we remove the /json from all routes when adding them in the metrics.
			"route":   strings.TrimPrefix(rpcRoute, "/json"),
			"version": "v0",
		})
		appRouter.MethodFunc(method, rpcRoute, func(w http.ResponseWriter, r *http.Request) {
			counter.Inc(1)
			handlerFunc(w, r)
		})
	}

	v1 := func(method, rpcRoute string, handlerFunc http.HandlerFunc) {
		counter := metrics2.GetCounter(web.RPCCallCounterMetric, map[string]string{
			// For consistency, we remove the /json/vN from all routes when adding them in the metrics.
			"route":   strings.TrimPrefix(rpcRoute, "/json/v1"),
			"version": "v1",
		})
		appRouter.MethodFunc(method, rpcRoute, func(w http.ResponseWriter, r *http.Request) {
			counter.Inc(1)
			handlerFunc(w, r)
		})
	}

	v2 := func(method, rpcRoute string, handlerFunc http.HandlerFunc) {
		counter := metrics2.GetCounter(web.RPCCallCounterMetric, map[string]string{
			// For consistency, we remove the /json/vN from all routes when adding them in the metrics.
			"route":   strings.TrimPrefix(rpcRoute, "/json/v2"),
			"version": "v2",
		})
		appRouter.MethodFunc(method, rpcRoute, func(w http.ResponseWriter, r *http.Request) {
			counter.Inc(1)
			handlerFunc(w, r)
		})
	}

	// Serve the known hashes from GCS.
	v0("GET", frontend.KnownHashesRoute, handlers.KnownHashesHandler)
	v1("GET", frontend.KnownHashesRouteV1, handlers.KnownHashesHandler)
	// Serve the expectations for the primary branch and for CLs in progress.
	v2("GET", frontend.ExpectationsRouteV2, handlers.BaselineHandlerV2)
	v1("GET", frontend.GroupingsRouteV1, handlers.GroupingsHandler)

	// Only log and compress the app routes, but not the health check.
	router := chi.NewRouter()
	router.HandleFunc("/healthz", httputils.ReadyHandleFunc)
	router.Handle("/*", httputils.LoggingGzipRequestResponse(appRouter))

	// Start the server
	sklog.Infof("Serving on http://127.0.0.1" + cfg.ReadyPort)
	sklog.Fatal(http.ListenAndServe(cfg.ReadyPort, router))
}

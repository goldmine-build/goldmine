package main

import (
	"context"
	"flag"
	"io"
	"net/http"
	"os/user"
	"path/filepath"
	"time"

	"cloud.google.com/go/datastore"
	"github.com/flynn/json5"
	"github.com/gorilla/mux"
	"go.skia.org/infra/autoroll/go/roller"
	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/ds"
	"go.skia.org/infra/go/gitauth"
	"go.skia.org/infra/go/httputils"
	"go.skia.org/infra/go/skiaversion"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/go/webhook"
	"google.golang.org/api/option"
)

// flags
var (
	configFile  = flag.String("config_file", "", "Configuration file to use.")
	local       = flag.Bool("local", false, "Running locally if true. As opposed to in production.")
	port        = flag.String("port", ":8000", "HTTP service port (e.g., ':8000')")
	promPort    = flag.String("prom_port", ":20000", "Metrics service address (e.g., ':10110')")
	webhookSalt = flag.String("webhook_request_salt", "", "Path to a file containing webhook request salt.")
)

func main() {
	common.InitWithMust(
		"google3-autoroll",
		common.PrometheusOpt(promPort),
		common.MetricsLoggingOpt(),
	)
	defer common.Defer()

	skiaversion.MustLogVersion()

	if *webhookSalt == "" {
		sklog.Fatal("--webhook_request_salt is required.")
	}

	var cfg roller.AutoRollerConfig
	if err := util.WithReadFile(*configFile, func(f io.Reader) error {
		return json5.NewDecoder(f).Decode(&cfg)
	}); err != nil {
		sklog.Fatal(err)
	}

	ts, err := auth.NewDefaultTokenSource(*local, auth.SCOPE_USERINFO_EMAIL, auth.SCOPE_GERRIT, datastore.ScopeDatastore)
	if err != nil {
		sklog.Fatal(err)
	}
	if err := ds.InitWithOpt(common.PROJECT_ID, ds.AUTOROLL_INTERNAL_NS, option.WithTokenSource(ts)); err != nil {
		sklog.Fatal(err)
	}
	client := httputils.DefaultClientConfig().WithTokenSource(ts).With2xxOnly().Client()

	// The rollers use the gitcookie created by gitauth package.
	user, err := user.Current()
	if err != nil {
		sklog.Fatal(err)
	}
	gitcookiesPath := filepath.Join(user.HomeDir, ".gitcookies")
	if _, err := gitauth.New(ts, gitcookiesPath, true, cfg.ServiceAccount); err != nil {
		sklog.Fatalf("Failed to create git cookie updater: %s", err)
	}

	r := mux.NewRouter()
	if err := webhook.InitRequestSaltFromFile(*webhookSalt); err != nil {
		sklog.Fatal(err)
	}
	ctx := context.Background()
	arb, err := NewAutoRoller(ctx, gitcookiesPath, &cfg, client)
	if err != nil {
		sklog.Fatal(err)
	}
	arb.AddHandlers(r)
	arb.Start(ctx, time.Minute, time.Minute)
	h := httputils.LoggingGzipRequestResponse(r)
	if !*local {
		h = httputils.HealthzAndHTTPS(h)
	}
	http.Handle("/", h)
	sklog.Fatal(http.ListenAndServe(*port, nil))
}

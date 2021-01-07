package main

import (
	"context"
	"flag"
	"fmt"
	"html/template"
	"net/http"
	"path/filepath"

	"cloud.google.com/go/datastore"
	"github.com/gorilla/mux"
	"google.golang.org/api/option"

	"go.skia.org/infra/autoroll/go/status"
	"go.skia.org/infra/go/allowed"
	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/baseapp"
	"go.skia.org/infra/go/httputils"
	"go.skia.org/infra/go/login"
	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
)

// Flags
var (
	modifyGroup        = flag.String("modify_group", "project-skia-committers", "The chrome infra auth group to use for who is allowed to change tree status.")
	chromeInfraAuthJWT = flag.String("chrome_infra_auth_jwt", "/var/secrets/skia-public-auth/key.json", "The JWT key for the service account that has access to chrome infra auth.")
	namespace          = flag.String("namespace", "tree-status-staging", "The Cloud Datastore namespace.")
	project            = flag.String("project", "skia-public", "The Google Cloud project name.")
)

var (
	// dsClient is the Cloud Datastore client to access tree statuses.
	dsClient *datastore.Client
)

// Server is the state of the server.
type Server struct {
	templates  *template.Template
	modify     allowed.Allow // Who is allowed to modify tree status.
	autorollDB status.DB
}

// See baseapp.Constructor.
func New() (baseapp.App, error) {
	ctx := context.Background()
	ts, err := auth.NewDefaultTokenSource(*baseapp.Local, "https://www.googleapis.com/auth/datastore")
	if err != nil {
		return nil, skerr.Wrapf(err, "Problem setting up default token source")
	}

	dsClient, err = datastore.NewClient(context.Background(), *project, option.WithTokenSource(ts))
	if err != nil {
		return nil, skerr.Wrapf(err, "Failed to initialize Cloud Datastore for tree status")
	}

	// Start watching for statuses with autorollers specified.
	autorollDB, err := AutorollersInit(ctx, ts)
	if err != nil {
		return nil, skerr.Wrapf(err, "Could not init autorollers")
	}

	// Load the last status and whether autorollers need to be watched.
	s, err := GetLatestStatus()
	if err != nil {
		return nil, skerr.Wrapf(err, "Could not find latest status")
	}
	if s.Rollers != "" {
		sklog.Infof("Last status has rollers that need to be watched: %s", s.Rollers)
		StartWatchingAutorollers(s.Rollers)
	}

	var modify allowed.Allow
	if !*baseapp.Local {
		ts, err := auth.NewJWTServiceAccountTokenSource("", *chromeInfraAuthJWT, auth.SCOPE_USERINFO_EMAIL)
		if err != nil {
			return nil, err
		}
		client := httputils.DefaultClientConfig().WithTokenSource(ts).With2xxOnly().Client()
		modify, err = allowed.NewAllowedFromChromeInfraAuth(client, *modifyGroup)
		if err != nil {
			return nil, err
		}
	} else {
		modify = allowed.NewAllowedFromList([]string{"barney@example.org"})
	}

	login.SimpleInitWithAllow(*baseapp.Port, *baseapp.Local, nil /* Admins not needed */, modify, nil /* Everyone is allowed to access */)

	srv := &Server{
		modify:     modify,
		autorollDB: autorollDB,
	}
	srv.loadTemplates()
	liveness := metrics2.NewLiveness("alive", map[string]string{})
	fmt.Println(liveness)

	return srv, nil
}

func (srv *Server) loadTemplates() {
	blah := *baseapp.ResourcesDir
	srv.templates = template.Must(template.New("").Delims("{%", "%}").ParseFiles(
		filepath.Join(blah, "index.html"),
	))
}

// user returns the currently logged in user, or a placeholder if running locally.
func (srv *Server) user(r *http.Request) string {
	user := "barney@example.org"
	if !*baseapp.Local {
		user = login.LoggedInAs(r)
	}
	return user
}

// See baseapp.App.
func (srv *Server) AddHandlers(r *mux.Router) {
	// For login/logout.
	r.HandleFunc(login.DEFAULT_OAUTH2_CALLBACK, login.OAuth2CallbackHandler)
	r.HandleFunc("/logout/", login.LogoutHandler)
	r.HandleFunc("/loginstatus/", login.StatusHandler)

	// All endpoints that require authentication should be added to this router. The
	// rest of endpoints are left unauthenticated because they are accessed from various
	// places like: Skia infra apps, Gerrit plugin, Chrome extensions, presubmits, etc.
	appRouter := mux.NewRouter()

	// For tree status.
	appRouter.HandleFunc("/", srv.treeStateHandler).Methods("GET")
	appRouter.HandleFunc("/_/add_tree_status", srv.addStatusHandler).Methods("POST")
	appRouter.HandleFunc("/_/get_autorollers", srv.autorollersHandler).Methods("POST")
	appRouter.HandleFunc("/_/recent_statuses", srv.recentStatusesHandler).Methods("POST")
	r.HandleFunc("/current", httputils.CorsHandler(srv.bannerStatusHandler)).Methods("GET")

	// Use the appRouter as a handler and wrap it into middleware that enforces authentication.
	appHandler := http.Handler(appRouter)
	if !*baseapp.Local {
		appHandler = login.ForceAuth(appRouter, login.DEFAULT_REDIRECT_URL)
	}

	r.PathPrefix("/").Handler(appHandler)
}

// See baseapp.App.
func (srv *Server) AddMiddleware() []mux.MiddlewareFunc {
	return []mux.MiddlewareFunc{}
}

func main() {
	baseapp.Serve(New, []string{"tree-status.skia.org"})
}

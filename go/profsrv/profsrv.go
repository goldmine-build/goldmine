package profsrv

import (
	"net/http"
	"net/http/pprof"

	"github.com/go-chi/chi/v5"
	"go.goldmine.build/go/httputils"
	"go.goldmine.build/go/sklog"
)

// Start starts an internal HTTP server for debugging purposes if requested.
func Start(port string) {
	// Start the internal server on the internal port if requested.
	if port != "" {
		// Add the profiling endpoints to the internal router.
		internalRouter := chi.NewRouter()

		// Set up the health check endpoint.
		internalRouter.HandleFunc("/healthz", httputils.ReadyHandleFunc)

		// Register pprof handlers
		internalRouter.HandleFunc("/debug/pprof/", pprof.Index)
		internalRouter.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
		internalRouter.HandleFunc("/debug/pprof/profile", pprof.Profile)
		internalRouter.HandleFunc("/debug/pprof/{profile}", pprof.Index)

		go func() {
			sklog.Infof("Internal server on %q", port)
			sklog.Fatal(http.ListenAndServe(port, internalRouter))
		}()
	}
}

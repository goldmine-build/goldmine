// Serves the jsdoc's for both the elements-sk and common libraries.
package main

import (
	"flag"
	"io/fs"
	"net/http"
	"path/filepath"

	"go.goldmine.build/go/common"
	"go.goldmine.build/go/httputils"
	"go.goldmine.build/go/sklog"
)

// flags
var (
	local        = flag.Bool("local", false, "Running locally if true. As opposed to in production.")
	port         = flag.String("port", ":8000", "HTTP service address (e.g., ':8000')")
	promPort     = flag.String("prom_port", ":20000", "Metrics service address (e.g., ':10110')")
	resourcesDir = flag.String("resources_dir", "/usr/local/share/jsdoc/dist", "Root directory of resources to serve.")
)

func main() {
	common.InitWithMust(
		"jsdocserver",
		common.PrometheusOpt(promPort),
	)

	filepath.WalkDir(*resourcesDir, func(path string, d fs.DirEntry, err error) error {
		sklog.Infof("path: %s", path)
		return nil
	})

	h := httputils.LoggingGzipRequestResponse(
		http.FileServer(
			http.Dir(*resourcesDir)))
	if !*local {
		h = httputils.HealthzAndHTTPS(h)
	}

	http.Handle("/", h)
	sklog.Info("Ready to serve.")
	sklog.Fatal(http.ListenAndServe(*port, nil))
}

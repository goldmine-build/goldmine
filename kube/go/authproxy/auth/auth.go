// Package auth provides an interface for handling authenticated users.
package auth

import (
	"net/http"

	"go.skia.org/infra/go/allowed"
)

// Auth is an abstraction of the functionality we use out fo the go/login
// package.
type Auth interface {
	SimpleInitWithAllow(port string, local bool, admin, edit, view allowed.Allow)
	LoggedInAs(r *http.Request) string
	IsViewer(r *http.Request) bool
	LoginURL(w http.ResponseWriter, r *http.Request) string
}

/*
	Reports version information.

	Requires running "make skiaversion" to set the constants.
*/

package skiaversion

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.skia.org/infra/go/sklog"
)

var version *Version

// Version holds information about the version of code this program is running.
type Version struct {
	Commit string    `json:"commit"`
	Date   time.Time `json:"date"`
}

// GetVersion returns a Version object for this program.
func GetVersion() (*Version, error) {
	if version != nil {
		return version, nil
	}
	return nil, fmt.Errorf("No version was set at compile time! Did you forget to run \"make skiaversion\"?")
}

// MustLogVersion logs the version info and panics if it is not found.
func MustLogVersion() {
	v, err := GetVersion()
	if err != nil {
		sklog.Fatal(err)
	}
	sklog.Infof("Version %s, built at %s", v.Commit, v.Date)
}

// JsonHandler is a pre-built handler for HTTP requests which returns version
// information in JSON format.
func JsonHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	v, err := GetVersion()
	if err != nil {
		sklog.Error(err)
		v = &Version{
			Commit: "(unknown)",
			Date:   time.Time{},
		}
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		sklog.Errorf("Failed to write or encode output: %s", err)
		return
	}
}

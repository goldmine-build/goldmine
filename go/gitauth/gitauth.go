// A Go implementation of https://gerrit.googlesource.com/gcompute-tools/+/master/git-cookie-authdaemon
package gitauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"

	"go.skia.org/infra/go/exec"
	"go.skia.org/infra/go/git"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"golang.org/x/oauth2"
)

var (
	ErrCookiesMissing = errors.New("Unable to find a valid .gitcookies file.")

	// SCOPES are the list of valid scopes for git auth.
	SCOPES = []string{
		"https://www.googleapis.com/auth/gerritcodereview",
		"https://www.googleapis.com/auth/source.full_control",
		"https://www.googleapis.com/auth/source.read_write",
		"https://www.googleapis.com/auth/source.read_only",
	}
)

const (
	REFRESH        = time.Minute
	RETRY_INTERVAL = 5 * time.Second
)

// GitAuth continuously updates the git cookie.
//
type GitAuth struct {
	tokenSource oauth2.TokenSource
	filename    string
}

func (g *GitAuth) updateCookie() (time.Duration, error) {
	token, err := g.tokenSource.Token()
	if err != nil {
		return RETRY_INTERVAL, fmt.Errorf("Failed to retrieve token: %s", err)
	}
	refresh_in := token.Expiry.Sub(time.Now())
	refresh_in -= REFRESH
	if refresh_in < 0 {
		refresh_in = REFRESH
	}
	contents := []string{}
	// As documented on a random website: https://xiix.wordpress.com/2006/03/23/mozillafirefox-cookie-format/
	contents = append(contents, fmt.Sprintf("source.developers.google.com\tFALSE\t/\tTRUE\t%d\to\t%s\n", token.Expiry.Unix(), token.AccessToken))
	contents = append(contents, fmt.Sprintf(".googlesource.com\tTRUE\t/\tTRUE\t%d\to\t%s\n", token.Expiry.Unix(), token.AccessToken))
	err = util.WithWriteFile(g.filename, func(w io.Writer) error {
		_, err := w.Write([]byte(strings.Join(contents, "")))
		return err
	})
	if err != nil {
		return RETRY_INTERVAL, fmt.Errorf("Failed to write new cookie file: %s", err)
	}
	sklog.Infof("Refreshing cookie in %v", refresh_in)

	return refresh_in, nil
}

// New returns a new *GitAuth.
//
// tokenSource - An oauth2.TokenSource authorized to access the repository, with an appropriate scope set.
// filename - The name of the git cookie file, e.g. "~/.git-credential-cache/cookie".
// config - If true then set the http.cookiefile config globally for git and set the user name and email globally if 'email' is not the empty string.
// email - The email address of the authorized account. Used to set the git config user.name and user.email. Can be "", in which case user.name
//    and user.email are not set.
//
// If config if false then Git must be told about the location of the Cookie file, for example:
//
//    git config --global http.cookiefile ~/.git-credential-cache/cookie
//
func New(tokenSource oauth2.TokenSource, filename string, config bool, email string) (*GitAuth, error) {
	if config {
		gitExec, err := git.Executable(context.TODO())
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		output := bytes.Buffer{}
		err = exec.Run(context.Background(), &exec.Command{
			Name: gitExec,
			Args: []string{
				"config",
				"--global",
				"http.cookiefile",
				filename},
			CombinedOutput: &output,
		})
		if err != nil {
			return nil, fmt.Errorf("Failed to set cookie in git config %q: %s", output.String(), err)
		}
		if email != "" {
			ctx := context.Background()
			out, err := exec.RunSimple(ctx, fmt.Sprintf("git config --global user.email %s", email))
			if err != nil {
				return nil, fmt.Errorf("Failed to config: %s: %s", err, out)
			}
			name := strings.Split(email, "@")[0]
			out, err = exec.RunSimple(ctx, fmt.Sprintf("git config --global user.name %s", name))
			if err != nil {
				return nil, fmt.Errorf("Failed to config: %s: %s", err, out)
			}
		}
	}
	g := &GitAuth{
		tokenSource: tokenSource,
		filename:    filename,
	}
	refresh_in, err := g.updateCookie()
	if err != nil {
		return nil, fmt.Errorf("Failed to get initial git cookie: %s", err)
	}
	// Set the GIT_COOKIES_PATH environment variable for Depot Tools.
	if err := os.Setenv("GIT_COOKIES_PATH", filename); err != nil {
		return nil, err
	}

	go func() {
		for {
			time.Sleep(refresh_in)
			refresh_in, err = g.updateCookie()
			if err != nil {
				sklog.Errorf("Failed to get initial git cookie: %s", err)
			}
		}
	}()
	return g, nil
}

// getCredentials returns the parsed contents of .gitCookies.
//
// This logic has been borrowed from
// https://cs.chromium.org/chromium/tools/depot_tools/gerrit_util.py?l=143
func getCredentials(gitCookiesPath string) (map[string]string, error) {
	// Set empty cookies if no path was given and issue a warning.
	if gitCookiesPath == "" {
		return nil, ErrCookiesMissing
	}

	gitCookies := map[string]string{}

	dat, err := ioutil.ReadFile(gitCookiesPath)
	if err != nil {
		return nil, err
	}
	contents := string(dat)
	for _, line := range strings.Split(contents, "\n") {
		if strings.HasPrefix(line, "#") || line == "" {
			continue
		}
		tokens := strings.Split(line, "\t")
		domain, xpath, key, value := tokens[0], tokens[2], tokens[5], tokens[6]
		if xpath == "/" && key == "o" {
			gitCookies[domain] = value
		}
	}
	return gitCookies, nil
}

// AddAuthenticationCookie adds a git authentication cookie to the given request.
//
// TODO(borenet): It would be nice if this could be part of an http.Client, or
// use an http.CookieJar in combination with the above code so that we don't
// have to call AddAuthenticationCookie manually all the time.
func AddAuthenticationCookie(gitCookiesPath string, req *http.Request) error {
	auth := ""
	cookies, err := getCredentials(gitCookiesPath)
	if err != nil {
		return err
	}
	for d, a := range cookies {
		if util.CookieDomainMatch(req.URL.Host, d) {
			auth = a
			cookie := http.Cookie{Name: "o", Value: a}
			req.AddCookie(&cookie)
			break
		}
	}
	if auth == "" {
		return ErrCookiesMissing
	}
	return nil
}

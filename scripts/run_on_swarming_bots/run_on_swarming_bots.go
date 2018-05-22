package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/pborman/uuid"
	"go.skia.org/infra/go/auth"
	"go.skia.org/infra/go/common"
	"go.skia.org/infra/go/isolate"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/swarming"
)

/*
	Run a specified command on all specified GCE instances.
*/

const (
	ISOLATE_DEFAULT_NAMESPACE = "default-gzip"
	TMP_ISOLATE_FILE_NAME     = "script.isolate"
	TMP_ISOLATE_FILE_CONTENTS = `{
  'variables': {
    'command': [
      'python', '-u', '%s',
    ],
    'files': [
      '%s',
    ],
  },
}`
)

var (
	dimensions  = common.NewMultiStringFlag("dimension", nil, "Colon-separated key/value pair, eg: \"os:Linux\" Dimensions of the bots on which to run. Can specify multiple times.")
	pool        = flag.String("pool", swarming.DIMENSION_POOL_VALUE_SKIA, "Which Swarming pool to use.")
	script      = flag.String("script", "", "Path to a Python script to run.")
	taskName    = flag.String("task_name", "", "Name of the task to run.")
	workdir     = flag.String("workdir", os.TempDir(), "Working directory. Optional, but recommended not to use CWD.")
	includeBots = common.NewMultiStringFlag("include_bot", nil, "If specified, treated as a white list of bots which will be affected, calculated AFTER the dimensions is computed. Can be simple strings or regexes")
	internal    = flag.Bool("internal", false, "Run against internal swarming and isolate instances.")
)

func main() {
	// Setup, parse args.
	defer common.LogPanic()
	common.Init()

	ctx := context.Background()
	if *script == "" {
		sklog.Fatal("--script is required.")
	}
	scriptName := path.Base(*script)
	if *taskName == "" {
		*taskName = fmt.Sprintf("run_%s", scriptName)
	}

	dims, err := swarming.ParseDimensionFlags(dimensions)
	if err != nil {
		sklog.Fatalf("Problem parsing dimensions: %s", err)
	}
	dims["pool"] = *pool

	*workdir, err = filepath.Abs(*workdir)
	if err != nil {
		sklog.Fatal(err)
	}

	includeRegs, err := parseRegex(*includeBots)
	if err != nil {
		sklog.Fatal(err)
	}

	isolateServer := isolate.ISOLATE_SERVER_URL
	swarmingServer := swarming.SWARMING_SERVER
	if *internal {
		isolateServer = isolate.ISOLATE_SERVER_URL_PRIVATE
		swarmingServer = swarming.SWARMING_SERVER_PRIVATE
	}

	// Authenticated HTTP client.
	oauthCacheFile := path.Join(*workdir, "google_storage_token.data")
	httpClient, err := auth.NewClient(true, oauthCacheFile, swarming.AUTH_SCOPE)
	if err != nil {
		sklog.Fatal(err)
	}

	// Swarming API client.
	swarmApi, err := swarming.NewApiClient(httpClient, swarmingServer)
	if err != nil {
		sklog.Fatal(err)
	}

	// Obtain the list of bots.
	bots, err := swarmApi.ListBots(dims)
	if err != nil {
		sklog.Fatal(err)
	}

	swarmClient, err := swarming.NewSwarmingClient(ctx, *workdir, swarmingServer, isolateServer)
	if err != nil {
		sklog.Fatal(err)
	}

	// Copy the script to the workdir.
	dstScript := path.Join(*workdir, scriptName)
	contents, err := ioutil.ReadFile(*script)
	if err != nil {
		sklog.Fatal(err)
	}
	if err := ioutil.WriteFile(dstScript, contents, 0644); err != nil {
		sklog.Fatal(err)
	}

	// Create an isolate file.
	isolateFile := path.Join(*workdir, TMP_ISOLATE_FILE_NAME)
	if err := ioutil.WriteFile(isolateFile, []byte(fmt.Sprintf(TMP_ISOLATE_FILE_CONTENTS, scriptName, scriptName)), 0644); err != nil {
		sklog.Fatal(err)
	}

	// Upload to isolate server.
	isolated, err := swarmClient.CreateIsolatedGenJSON(isolateFile, *workdir, "linux", *taskName, map[string]string{}, []string{})
	if err != nil {
		sklog.Fatal(err)
	}
	m, err := swarmClient.BatchArchiveTargets(ctx, []string{isolated}, 5*time.Minute)
	if err != nil {
		sklog.Fatal(err)
	}
	group := fmt.Sprintf("%s_%s", *taskName, uuid.New())
	tags := map[string]string{
		"group": group,
	}

	var wg sync.WaitGroup

	// Trigger the task on each bot.
	for _, bot := range bots {
		if !matchesAny(bot.BotId, includeRegs) {
			sklog.Debugf("Skipping %s because it isn't in the whitelist", bot.BotId)
			continue
		}
		botDims := map[string][]string{}
		for _, d := range bot.Dimensions {
			botDims[d.Key] = d.Value
		}
		wg.Add(1)
		go func(id string, botDims map[string][]string) {
			defer wg.Done()
			dims := map[string]string{
				"pool": *pool,
				"id":   id,
			}
			sklog.Infof("Triggering on %s", id)
			if _, err := swarmClient.TriggerSwarmingTasks(ctx, m, dims, tags, []string{}, swarming.HIGHEST_PRIORITY, 120*time.Minute, 120*time.Minute, 120*time.Minute, false, false, ""); err != nil {
				sklog.Fatal(err)
			}
		}(bot.BotId, botDims)
	}

	wg.Wait()
	tasksLink := fmt.Sprintf("https://%s/tasklist?f=group:%s", swarmingServer, group)
	sklog.Infof("Triggered Swarming tasks. Visit this link to track progress:\n%s", tasksLink)
}

func parseRegex(flags common.MultiString) (retval []*regexp.Regexp, e error) {
	if len(flags) == 0 {
		return retval, nil
	}

	for _, s := range flags {
		r, err := regexp.Compile(s)
		if err != nil {
			return nil, err
		}
		retval = append(retval, r)
	}
	return retval, nil
}

func matchesAny(s string, xr []*regexp.Regexp) bool {
	if len(xr) == 0 {
		return true
	}
	for _, r := range xr {
		if r.MatchString(s) {
			return true
		}
	}
	return false
}

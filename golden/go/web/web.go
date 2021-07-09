package web

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cockroachdb/cockroach-go/v2/crdb/crdbpgx"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	lru "github.com/hashicorp/golang-lru"
	"github.com/jackc/pgtype"
	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	"go.opencensus.io/trace"
	"golang.org/x/sync/errgroup"
	"golang.org/x/time/rate"

	"go.skia.org/infra/go/httputils"
	"go.skia.org/infra/go/human"
	"go.skia.org/infra/go/login"
	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/now"
	"go.skia.org/infra/go/paramtools"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/golden/go/clstore"
	"go.skia.org/infra/golden/go/diff"
	"go.skia.org/infra/golden/go/expectations"
	"go.skia.org/infra/golden/go/ignore"
	"go.skia.org/infra/golden/go/indexer"
	"go.skia.org/infra/golden/go/search"
	search_query "go.skia.org/infra/golden/go/search/query"
	"go.skia.org/infra/golden/go/search2"
	"go.skia.org/infra/golden/go/sql"
	"go.skia.org/infra/golden/go/sql/schema"
	"go.skia.org/infra/golden/go/status"
	"go.skia.org/infra/golden/go/storage"
	"go.skia.org/infra/golden/go/tilesource"
	"go.skia.org/infra/golden/go/tiling"
	"go.skia.org/infra/golden/go/tjstore"
	"go.skia.org/infra/golden/go/types"
	"go.skia.org/infra/golden/go/validation"
	"go.skia.org/infra/golden/go/web/frontend"
)

const (
	// pageSize is the default page size used for pagination.
	pageSize = 20

	// maxPageSize is the maximum page size used for pagination.
	maxPageSize = 100

	// These params limit how anonymous (not logged-in) users can hit various endpoints.
	// We have two buckets of requests - cheap and expensive. Expensive stuff hits a database
	// or similar, where as cheap stuff is cached. These limits are shared by *all* endpoints
	// in a given bucket. See skbug.com/9476 for more.
	maxAnonQPSExpensive   = rate.Limit(0.01)
	maxAnonBurstExpensive = 50
	maxAnonQPSCheap       = rate.Limit(5.0)
	maxAnonBurstCheap     = 50
	// Special settings for RPCs serving the gerrit plugin. See skbug.com/10768 for more.
	maxAnonQPSGerritPlugin   = rate.Limit(200.0)
	maxAnonBurstGerritPlugin = 1000

	changelistSummaryCacheSize = 10000

	// RPCCallCounterMetric is the metric that should be used when counting how many times a given
	// RPC route is called from clients.
	RPCCallCounterMetric = "gold_rpc_call_counter"
)

type validateFields int

const (
	// FullFrontEnd means all fields should be set
	FullFrontEnd validateFields = iota
	// BaselineSubset means just the fields needed for BaselineV2Response Server should be set.
	BaselineSubset
)

// HandlersConfig holds the environment needed by the various http handler functions.
type HandlersConfig struct {
	DB                *pgxpool.Pool
	ExpectationsStore expectations.Store
	GCSClient         storage.GCSClient
	IgnoreStore       ignore.Store
	Indexer           indexer.IndexSource
	ReviewSystems     []clstore.ReviewSystem
	SearchAPI         search.SearchAPI
	Search2API        search2.API
	StatusWatcher     *status.StatusWatcher
	TileSource        tilesource.TileSource
	TryJobStore       tjstore.Store
}

// Handlers represents all the handlers (e.g. JSON endpoints) of Gold.
// It should be created by clients using NewHandlers.
type Handlers struct {
	HandlersConfig

	anonymousExpensiveQuota *rate.Limiter
	anonymousCheapQuota     *rate.Limiter
	anonymousGerritQuota    *rate.Limiter

	clSummaryCache *lru.Cache

	statusCache      frontend.GUIStatus
	statusCacheMutex sync.RWMutex

	// These can be set for unit tests to simplify the testing.
	testingAuthAs string
}

// NewHandlers returns a new instance of Handlers.
func NewHandlers(conf HandlersConfig, val validateFields) (*Handlers, error) {
	// These fields are required by all types.
	if conf.DB == nil {
		return nil, skerr.Fmt("Baseliner cannot be nil")
	}
	if len(conf.ReviewSystems) == 0 {
		return nil, skerr.Fmt("ReviewSystems cannot be empty")
	}
	if conf.GCSClient == nil {
		return nil, skerr.Fmt("GCSClient cannot be nil")
	}

	if val == FullFrontEnd {
		if conf.ExpectationsStore == nil {
			return nil, skerr.Fmt("ExpectationsStore cannot be nil")
		}
		if conf.IgnoreStore == nil {
			return nil, skerr.Fmt("IgnoreStore cannot be nil")
		}
		if conf.Indexer == nil {
			return nil, skerr.Fmt("Indexer cannot be nil")
		}
		if conf.SearchAPI == nil {
			return nil, skerr.Fmt("SearchAPI cannot be nil")
		}
		if conf.Search2API == nil {
			return nil, skerr.Fmt("Search2API cannot be nil")
		}
		if conf.StatusWatcher == nil {
			return nil, skerr.Fmt("StatusWatcher cannot be nil")
		}
		if conf.TileSource == nil {
			return nil, skerr.Fmt("TileSource cannot be nil")
		}
		if conf.TryJobStore == nil {
			return nil, skerr.Fmt("TryJobStore cannot be nil")
		}
	}

	clcache, err := lru.New(changelistSummaryCacheSize)
	if err != nil {
		return nil, skerr.Wrap(err)
	}

	return &Handlers{
		HandlersConfig:          conf,
		anonymousExpensiveQuota: rate.NewLimiter(maxAnonQPSExpensive, maxAnonBurstExpensive),
		anonymousCheapQuota:     rate.NewLimiter(maxAnonQPSCheap, maxAnonBurstCheap),
		anonymousGerritQuota:    rate.NewLimiter(maxAnonQPSGerritPlugin, maxAnonBurstGerritPlugin),
		clSummaryCache:          clcache,
		testingAuthAs:           "", // Just to be explicit that we do *not* bypass Auth.
	}, nil
}

// limitForAnonUsers blocks using the configured rate.Limiter for expensive queries.
func (wh *Handlers) limitForAnonUsers(r *http.Request) error {
	if login.LoggedInAs(r) != "" {
		return nil
	}
	return wh.anonymousExpensiveQuota.Wait(r.Context())
}

// cheapLimitForAnonUsers blocks using the configured rate.Limiter for cheap queries.
func (wh *Handlers) cheapLimitForAnonUsers(r *http.Request) error {
	if login.LoggedInAs(r) != "" {
		return nil
	}
	return wh.anonymousCheapQuota.Wait(r.Context())
}

// cheapLimitForGerritPlugin blocks using the configured rate.Limiter for queries for the
// Gerrit Plugin.
func (wh *Handlers) cheapLimitForGerritPlugin(r *http.Request) error {
	if login.LoggedInAs(r) != "" {
		return nil
	}
	return wh.anonymousGerritQuota.Wait(r.Context())
}

// TODO(stephana): once the byBlameHandler is removed, refactor this to
// remove the redundant types ByBlameEntry and ByBlame.

// ByBlameHandler returns a json object with the digests to be triaged grouped by blamelist.
func (wh *Handlers) ByBlameHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	// Extract the corpus from the query parameters.
	corpus := ""
	if v := r.FormValue("query"); v != "" {
		if qp, err := url.ParseQuery(v); err != nil {
			httputils.ReportError(w, err, "invalid input", http.StatusBadRequest)
			return
		} else if corpus = qp.Get(types.CorpusField); corpus == "" {
			// If no corpus specified report an error.
			http.Error(w, "did not receive value for corpus", http.StatusBadRequest)
			return
		}
	}

	blameEntries, err := wh.computeByBlame(r.Context(), corpus)
	if err != nil {
		httputils.ReportError(w, err, "could not compute blames", http.StatusInternalServerError)
		return
	}

	// Wrap the result in an object because we don't want to return a JSON array.
	sendJSONResponse(w, frontend.ByBlameResponse{Data: blameEntries})
}

// computeByBlame creates several ByBlameEntry structs based on the state
// of HEAD and returns them in a slice, for use by the frontend.
func (wh *Handlers) computeByBlame(ctx context.Context, corpus string) ([]frontend.ByBlameEntry, error) {
	idx := wh.Indexer.GetIndex()
	// At this point query contains at least a corpus.
	untriagedSummaries, err := idx.SummarizeByGrouping(ctx, corpus, nil, types.ExcludeIgnoredTraces, true)
	if err != nil {
		return nil, skerr.Wrapf(err, "could not get summaries for corpus %q", corpus)
	}
	commits := idx.Tile().DataCommits()

	// This is a very simple grouping of digests, for every digest we look up the
	// blame list for that digest and then use the concatenated git hashes as a
	// group id. All of the digests are then grouped by their group id.

	// Collects a ByBlame for each untriaged digest, keyed by group id.
	grouped := map[string][]frontend.ByBlame{}

	// The Commit info for each group id.
	commitinfo := map[string][]tiling.Commit{}
	// map [groupid] [test] TestRollup
	rollups := map[string]map[types.TestName]frontend.TestRollup{}

	for _, s := range untriagedSummaries {
		test := s.Name
		for _, d := range s.UntHashes {
			dist := idx.GetBlame(test, d, commits)
			if dist.IsEmpty() {
				// Should only happen if the index isn't quite ready being prepared.
				// Since we wait until the index is created before exposing the web
				// server, this should never happen.
				sklog.Warningf("empty blame for %s %s", test, d)
				continue
			}
			groupid := strings.Join(lookUpCommits(dist.Freq, commits), ":")
			// Only fill in commitinfo for each groupid only once.
			if _, ok := commitinfo[groupid]; !ok {
				var blameCommits []tiling.Commit
				for _, index := range dist.Freq {
					blameCommits = append(blameCommits, commits[index])
				}
				sort.Slice(blameCommits, func(i, j int) bool {
					return blameCommits[i].CommitTime.After(blameCommits[j].CommitTime)
				})
				commitinfo[groupid] = blameCommits
			}
			// Construct a ByBlame and add it to grouped.
			value := frontend.ByBlame{
				Test:          test,
				Digest:        d,
				Blame:         dist,
				CommitIndices: dist.Freq,
			}
			if _, ok := grouped[groupid]; !ok {
				grouped[groupid] = []frontend.ByBlame{value}
			} else {
				grouped[groupid] = append(grouped[groupid], value)
			}
			if _, ok := rollups[groupid]; !ok {
				rollups[groupid] = map[types.TestName]frontend.TestRollup{}
			}
			// Calculate the rollups.
			r, ok := rollups[groupid][test]
			if !ok {
				r = frontend.TestRollup{
					Test:         test,
					Num:          0,
					SampleDigest: d,
				}
			}
			r.Num += 1
			rollups[groupid][test] = r
		}
	}

	// Assemble the response.
	blameEntries := make([]frontend.ByBlameEntry, 0, len(grouped))
	for groupid, byBlames := range grouped {
		rollup := rollups[groupid]
		nTests := len(rollup)
		var affectedTests []frontend.TestRollup

		// Only include the affected tests if there are no more than 10 of them.
		if nTests <= 10 {
			affectedTests = make([]frontend.TestRollup, 0, nTests)
			for _, testInfo := range rollup {
				affectedTests = append(affectedTests, testInfo)
			}
			sort.Slice(affectedTests, func(i, j int) bool {
				// Put the highest amount of digests first
				return affectedTests[i].Num > affectedTests[j].Num ||
					// Break ties based on test name (for determinism).
					(affectedTests[i].Num == affectedTests[j].Num && affectedTests[i].Test < affectedTests[j].Test)
			})
		}

		blameEntries = append(blameEntries, frontend.ByBlameEntry{
			GroupID:       groupid,
			NDigests:      len(byBlames),
			NTests:        nTests,
			AffectedTests: affectedTests,
			Commits:       frontend.FromTilingCommits(commitinfo[groupid]),
		})
	}
	sort.Slice(blameEntries, func(i, j int) bool {
		return blameEntries[i].NDigests > blameEntries[j].NDigests ||
			// For test determinism, use GroupID as a tie-breaker
			(blameEntries[i].NDigests == blameEntries[j].NDigests && blameEntries[i].GroupID < blameEntries[j].GroupID)
	})

	return blameEntries, nil
}

// lookUpCommits returns the commit hashes for the commit indices in 'freq'.
func lookUpCommits(freq []int, commits []tiling.Commit) []string {
	var ret []string
	for _, index := range freq {
		ret = append(ret, commits[index].Hash)
	}
	return ret
}

// ByBlameHandler2 takes the response from the SQL backend's GetBlamesForUntriagedDigests and
// converts it into the same format that the legacy version (v1) produced.
func (wh *Handlers) ByBlameHandler2(w http.ResponseWriter, r *http.Request) {
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}
	ctx, span := trace.StartSpan(r.Context(), "web_ByBlameHandler2", trace.WithSampler(trace.AlwaysSample()))
	defer span.End()

	// Extract the corpus from the query parameters.
	corpus := ""
	if v := r.FormValue("query"); v != "" {
		if qp, err := url.ParseQuery(v); err != nil {
			httputils.ReportError(w, err, "invalid input", http.StatusBadRequest)
			return
		} else if corpus = qp.Get(types.CorpusField); corpus == "" {
			// If no corpus specified report an error.
			http.Error(w, "did not receive value for corpus", http.StatusBadRequest)
			return
		}
	} else {
		// If no corpus specified report an error.
		http.Error(w, "did not receive value for search query", http.StatusBadRequest)
		return
	}
	summary, err := wh.Search2API.GetBlamesForUntriagedDigests(ctx, corpus)
	if err != nil {
		httputils.ReportError(w, err, "Could not compute blames", http.StatusInternalServerError)
		return
	}
	result := frontend.ByBlameResponse{}
	for _, sr := range summary.Ranges {
		entry := frontend.ByBlameEntry{
			GroupID:  sr.CommitRange,
			NDigests: sr.TotalUntriagedDigests,
			NTests:   len(sr.AffectedGroupings),
			Commits:  sr.Commits,
		}
		var groupings []frontend.TestRollup
		for _, gr := range sr.AffectedGroupings {
			groupings = append(groupings, frontend.TestRollup{
				Test:         types.TestName(gr.Grouping[types.PrimaryKeyField]),
				Num:          gr.UntriagedDigests,
				SampleDigest: gr.SampleDigest,
			})
		}
		entry.AffectedTests = groupings
		result.Data = append(result.Data, entry)
	}
	sendJSONResponse(w, result)
}

// ChangelistsHandler returns the list of code_review.Changelists that have
// uploaded results to Gold (via TryJobs).
func (wh *Handlers) ChangelistsHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	values := r.URL.Query()
	offset, size, err := httputils.PaginationParams(values, 0, pageSize, maxPageSize)
	if err != nil {
		httputils.ReportError(w, err, "Invalid pagination params.", http.StatusInternalServerError)
		return
	}

	_, activeOnly := values["active"]
	cls, pagination, err := wh.getIngestedChangelists(r.Context(), offset, size, activeOnly)

	if err != nil {
		httputils.ReportError(w, err, "Retrieving changelists results failed.", http.StatusInternalServerError)
		return
	}

	response := frontend.ChangelistsResponse{
		Changelists:        cls,
		ResponsePagination: *pagination,
	}

	sendJSONResponse(w, response)
}

// getIngestedChangelists performs the core of the logic for ChangelistsHandler,
// by fetching N Changelists given an offset.
func (wh *Handlers) getIngestedChangelists(ctx context.Context, offset, size int, activeOnly bool) ([]frontend.Changelist, *httputils.ResponsePagination, error) {
	so := clstore.SearchOptions{
		StartIdx: offset,
		Limit:    size,
	}
	if activeOnly {
		so.OpenCLsOnly = true
	}

	grandTotal := 0
	var retCls []frontend.Changelist
	for _, system := range wh.ReviewSystems {
		cls, total, err := system.Store.GetChangelists(ctx, so)
		if err != nil {
			return nil, nil, skerr.Wrapf(err, "fetching Changelists from [%d:%d)", offset, offset+size)
		}

		for _, cl := range cls {
			retCls = append(retCls, frontend.ConvertChangelist(cl, system.ID, system.URLTemplate))
		}
		if grandTotal == clstore.CountMany || total == clstore.CountMany {
			grandTotal = clstore.CountMany
		} else {
			grandTotal += total
		}
	}

	pagination := &httputils.ResponsePagination{
		Offset: offset,
		Size:   size,
		Total:  grandTotal,
	}
	return retCls, pagination, nil
}

// PatchsetsAndTryjobsForCL returns a summary of the data we have collected
// for a given Changelist, specifically any TryJobs that have uploaded data
// to Gold belonging to various patchsets in it.
func (wh *Handlers) PatchsetsAndTryjobsForCL(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}
	clID, ok := mux.Vars(r)["id"]
	if !ok {
		http.Error(w, "Must specify 'id' of Changelist.", http.StatusBadRequest)
		return
	}
	crs, ok := mux.Vars(r)["system"]
	if !ok {
		http.Error(w, "Must specify 'system' of Changelist.", http.StatusBadRequest)
		return
	}
	system, ok := wh.getCodeReviewSystem(crs)
	if !ok {
		http.Error(w, "Invalid Code Review System", http.StatusBadRequest)
		return
	}

	rv, err := wh.getCLSummary(r.Context(), system, clID)
	if err != nil {
		httputils.ReportError(w, err, "could not retrieve data for the specified CL.", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, rv)
}

// A list of CI systems we support. So far, the mapping of task ID to link is project agnostic. If
// that stops being the case, then we'll need to supply this mapping on a per-instance basis.
var cisTemplates = map[string]string{
	"cirrus":              "https://cirrus-ci.com/task/%s",
	"buildbucket":         "https://cr-buildbucket.appspot.com/build/%s",
	"buildbucketInternal": "https://cr-buildbucket.appspot.com/build/%s",
}

// getCLSummary does a bulk of the work for PatchsetsAndTryjobsForCL, specifically
// fetching the Changelist and Patchsets from clstore and any associated TryJobs from
// the tjstore.
func (wh *Handlers) getCLSummary(ctx context.Context, system clstore.ReviewSystem, clID string) (frontend.ChangelistSummary, error) {
	cl, err := system.Store.GetChangelist(ctx, clID)
	if err != nil {
		return frontend.ChangelistSummary{}, skerr.Wrapf(err, "getting CL %s", clID)
	}

	// We know xps is sorted by order, if it is non-nil
	xps, err := system.Store.GetPatchsets(ctx, clID)
	if err != nil {
		return frontend.ChangelistSummary{}, skerr.Wrapf(err, "getting Patchsets for CL %s", clID)
	}

	var patchsets []frontend.Patchset
	maxOrder := 0

	// TODO(kjlubick): maybe fetch these in parallel (with errgroup)
	for _, ps := range xps {
		if ps.Order > maxOrder {
			maxOrder = ps.Order
		}
		psID := tjstore.CombinedPSID{
			CL:  clID,
			CRS: system.ID,
			PS:  ps.SystemID,
		}
		xtj, err := wh.TryJobStore.GetTryJobs(ctx, psID)
		if err != nil {
			return frontend.ChangelistSummary{}, skerr.Wrapf(err, "getting TryJobs for CL %s - PS %s", clID, ps.SystemID)
		}
		var tryjobs []frontend.TryJob
		for _, tj := range xtj {
			templ := cisTemplates[tj.System]
			tryjobs = append(tryjobs, frontend.ConvertTryJob(tj, templ))
		}

		patchsets = append(patchsets, frontend.Patchset{
			SystemID: ps.SystemID,
			Order:    ps.Order,
			TryJobs:  tryjobs,
		})
	}

	return frontend.ChangelistSummary{
		CL:                frontend.ConvertChangelist(cl, system.ID, system.URLTemplate),
		Patchsets:         patchsets,
		NumTotalPatchsets: maxOrder,
	}, nil
}

// PatchsetsAndTryjobsForCL2 returns a summary of the data we have collected
// for a given Changelist, specifically any TryJobs that have uploaded data
// to Gold belonging to various patchsets in it.
func (wh *Handlers) PatchsetsAndTryjobsForCL2(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "web_PatchsetsAndTryjobsForCL2")
	defer span.End()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}
	clID, ok := mux.Vars(r)["id"]
	if !ok {
		http.Error(w, "Must specify 'id' of Changelist.", http.StatusBadRequest)
		return
	}
	crs, ok := mux.Vars(r)["system"]
	if !ok {
		http.Error(w, "Must specify 'system' of Changelist.", http.StatusBadRequest)
		return
	}
	rv, err := wh.getPatchsetsAndTryjobs(ctx, crs, clID)
	if err != nil {
		httputils.ReportError(w, err, "could not retrieve data for the specified CL.", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, rv)
}

// getPatchsetsAndTryjobs returns a summary of the patchsets and tryjobs that belong to a given
// CL.
func (wh *Handlers) getPatchsetsAndTryjobs(ctx context.Context, crs, clID string) (frontend.ChangelistSummary, error) {
	ctx, span := trace.StartSpan(ctx, "getPatchsetsAndTryjobs")
	defer span.End()

	system, ok := wh.getCodeReviewSystem(crs)
	if !ok {
		return frontend.ChangelistSummary{}, skerr.Fmt("Invalid Code Review System %q", crs)
	}

	qCLID := sql.Qualify(crs, clID)
	row := wh.DB.QueryRow(ctx, `SELECT status, owner_email, subject, last_ingested_data FROM Changelists
WHERE changelist_id = $1`, qCLID)
	var cl frontend.Changelist
	if err := row.Scan(&cl.Status, &cl.Owner, &cl.Subject, &cl.Updated); err != nil {
		return frontend.ChangelistSummary{}, skerr.Wrapf(err, "checking if CL %q exists", qCLID)
	}
	cl.Updated = cl.Updated.UTC()
	cl.SystemID = clID
	cl.System = crs
	cl.URL = strings.Replace(system.URLTemplate, "%s", cl.SystemID, 1)
	rv := frontend.ChangelistSummary{CL: cl}

	const statement = `SELECT Patchsets.patchset_id, Patchsets.ps_order,
tryjob_id, display_name, Tryjobs.last_ingested_data, Tryjobs.system FROM
Tryjobs JOIN Patchsets ON Tryjobs.patchset_id = Patchsets.patchset_id
WHERE Tryjobs.changelist_id = $1
ORDER BY Patchsets.patchset_id
`
	rows, err := wh.DB.Query(ctx, statement, qCLID)
	if err != nil {
		return frontend.ChangelistSummary{}, skerr.Wrap(err)
	}
	defer rows.Close()
	var patchsets []*frontend.Patchset
	var currentPS *frontend.Patchset
	for rows.Next() {
		var psID string
		var order int
		var tj frontend.TryJob
		if err := rows.Scan(&psID, &order, &tj.SystemID, &tj.DisplayName, &tj.Updated, &tj.System); err != nil {
			return frontend.ChangelistSummary{}, skerr.Wrap(err)
		}
		tj.Updated = tj.Updated.UTC()
		urlTempl, ok := cisTemplates[tj.System]
		if !ok {
			return frontend.ChangelistSummary{}, skerr.Fmt("Unrecognized CIS system: %q", tj.System)
		}
		tj.URL = strings.Replace(urlTempl, "%s", tj.SystemID, 1)
		if currentPS == nil || currentPS.SystemID != psID {
			currentPS = &frontend.Patchset{
				SystemID: psID,
				Order:    order,
			}
			patchsets = append(patchsets, currentPS)
		}
		currentPS.TryJobs = append(currentPS.TryJobs, tj)
	}

	for _, ps := range patchsets {
		rv.Patchsets = append(rv.Patchsets, *ps)
	}
	rv.NumTotalPatchsets = len(rv.Patchsets)

	sort.Slice(rv.Patchsets, func(i, j int) bool {
		return rv.Patchsets[i].Order > rv.Patchsets[j].Order
	})
	return rv, nil
}

// ChangelistUntriagedHandler writes out a list of untriaged digests uploaded by this CL that
// are not on master already and are not ignored.
func (wh *Handlers) ChangelistUntriagedHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.cheapLimitForGerritPlugin(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	requestVars := mux.Vars(r)
	clID, ok := requestVars["id"]
	if !ok {
		http.Error(w, "Must specify 'id' of Changelist.", http.StatusBadRequest)
		return
	}
	psID, ok := requestVars["patchset"]
	if !ok {
		http.Error(w, "Must specify 'patchset' of Changelist.", http.StatusBadRequest)
		return
	}
	crs, ok := requestVars["system"]
	if !ok {
		http.Error(w, "Must specify 'system' of Changelist.", http.StatusBadRequest)
		return
	}

	id := tjstore.CombinedPSID{
		CL:  clID,
		CRS: crs,
		PS:  psID,
	}
	dl, err := wh.SearchAPI.UntriagedUnignoredTryJobExclusiveDigests(r.Context(), id)
	if err != nil {
		sklog.Warningf("Could not get untriaged digests for %v - possibly this CL/PS has none or is too old to be indexed: %s", id, err)
		// Errors can trip up the Gerrit Plugin (at least until skbug/10706 is resolved).
		sendJSONResponse(w, frontend.UntriagedDigestList{TS: time.Now()})
		return
	}
	sendJSONResponse(w, dl)
}

// SearchHandler is the endpoint for all searches, including accessing
// results that belong to a tryjob.  It times out after 3 minutes, to prevent outstanding requests
// from growing unbounded.
func (wh *Handlers) SearchHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	q, ok := parseSearchQuery(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	ctx, span := trace.StartSpan(ctx, "SearchHandler")
	defer span.End()

	searchResponse, err := wh.SearchAPI.Search(ctx, q)
	if err != nil {
		httputils.ReportError(w, err, "Search for digests failed.", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, searchResponse)
}

// SearchHandler2 searches the data in the new SQL backend. It times out after 3 minutes, to prevent
// outstanding requests from growing unbounded.
func (wh *Handlers) SearchHandler2(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	q, ok := parseSearchQuery(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	ctx, span := trace.StartSpan(ctx, "web_SearchHandler2", trace.WithSampler(trace.AlwaysSample()))
	defer span.End()

	searchResponse, err := wh.Search2API.Search(ctx, q)
	if err != nil {
		httputils.ReportError(w, err, "Search for digests failed in the SQL backend.", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, searchResponse)
}

// parseSearchQuery extracts the search query from request.
func parseSearchQuery(w http.ResponseWriter, r *http.Request) (*search_query.Search, bool) {
	q := search_query.Search{Limit: 50}
	if err := search_query.ParseSearch(r, &q); err != nil {
		httputils.ReportError(w, err, "Search for digests failed.", http.StatusInternalServerError)
		return nil, false
	}
	return &q, true
}

// DetailsHandler returns the details about a single digest.
func (wh *Handlers) DetailsHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	// Extract: test, digest, issue
	if err := r.ParseForm(); err != nil {
		httputils.ReportError(w, err, "Failed to parse form values", http.StatusInternalServerError)
		return
	}
	test := r.Form.Get("test")
	digest := r.Form.Get("digest")
	if test == "" || !validation.IsValidDigest(digest) {
		http.Error(w, "Some query parameters are wrong or missing", http.StatusBadRequest)
		return
	}
	clID := r.Form.Get("changelist_id")
	crs := r.Form.Get("crs")
	if clID != "" {
		if _, ok := wh.getCodeReviewSystem(crs); !ok {
			http.Error(w, "Invalid Code Review System; did you include crs?", http.StatusBadRequest)
			return
		}
	} else {
		crs = ""
	}

	ret, err := wh.SearchAPI.GetDigestDetails(r.Context(), types.TestName(test), types.Digest(digest), clID, crs)
	if err != nil {
		httputils.ReportError(w, err, "Unable to get digest details.", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, ret)
}

// DetailsHandler2 returns the details about a single digest.
func (wh *Handlers) DetailsHandler2(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "web_DetailsHandler2")
	defer span.End()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	// Extract: test, digest, issue
	if err := r.ParseForm(); err != nil {
		httputils.ReportError(w, err, "Failed to parse form values", http.StatusInternalServerError)
		return
	}
	// TODO(kjlubick) require corpus
	test := r.Form.Get("test")
	digest := r.Form.Get("digest")
	if test == "" || !validation.IsValidDigest(digest) {
		http.Error(w, "Some query parameters are wrong or missing", http.StatusBadRequest)
		return
	}
	clID := r.Form.Get("changelist_id")
	crs := r.Form.Get("crs")
	if clID != "" {
		if _, ok := wh.getCodeReviewSystem(crs); !ok {
			http.Error(w, "Invalid Code Review System; did you include crs?", http.StatusBadRequest)
			return
		}
	} else {
		crs = ""
	}

	grouping, err := wh.getGroupingForTest(ctx, test)
	if err != nil {
		httputils.ReportError(w, err, "could not get grouping", http.StatusInternalServerError)
		return
	}
	ret, err := wh.Search2API.GetDigestDetails(ctx, grouping, types.Digest(digest), clID, crs)
	if err != nil {
		httputils.ReportError(w, err, "Unable to get digest details.", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, ret)
}

// getGroupingForTest acts as a bridge for RPCs that only take in a test name, when they should
// be taking in a grouping. It looks up the grouping by test name and returns it.
// TODO(kjlubick) Migrate all RPCs and remove this function.
func (wh *Handlers) getGroupingForTest(ctx context.Context, testName string) (paramtools.Params, error) {
	ctx, span := trace.StartSpan(ctx, "getGroupingForTest")
	defer span.End()

	const statement = `SELECT keys FROM Groupings WHERE keys->'name' = $1 LIMIT 1`
	// Need to wrap testName with quotes to make it "valid JSON", so we can use the inverted index
	// on keys.
	row := wh.DB.QueryRow(ctx, statement, `"`+testName+`"`)
	var ps paramtools.Params
	if err := row.Scan(&ps); err != nil {
		return nil, skerr.Wrapf(err, "looking up grouping for test name %q", testName)
	}
	return ps, nil
}

// DiffHandler returns difference between two digests.
func (wh *Handlers) DiffHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	// Extract: test, left, right where left and right are digests.
	if err := r.ParseForm(); err != nil {
		httputils.ReportError(w, err, "Failed to parse form values", http.StatusInternalServerError)
		return
	}
	// TODO(kjlubick) require corpus
	test := r.Form.Get("test")
	left := r.Form.Get("left")
	right := r.Form.Get("right")
	if test == "" || !validation.IsValidDigest(left) || !validation.IsValidDigest(right) {
		sklog.Debugf("Bad query params: %q %q %q", test, left, right)
		http.Error(w, "invalid query params", http.StatusBadRequest)
		return
	}
	clID := r.Form.Get("changelist_id")
	crs := r.Form.Get("crs")
	if clID != "" {
		if _, ok := wh.getCodeReviewSystem(crs); !ok {
			http.Error(w, "Invalid Code Review System; did you include crs?", http.StatusBadRequest)
			return
		}
	} else {
		crs = ""
	}

	ret, err := wh.SearchAPI.DiffDigests(r.Context(), types.TestName(test), types.Digest(left), types.Digest(right), clID, crs)
	if err != nil {
		httputils.ReportError(w, err, "Unable to compare digests", http.StatusInternalServerError)
		return
	}

	sendJSONResponse(w, ret)
}

// DiffHandler2 compares two digests and returns that information along with triage data.
func (wh *Handlers) DiffHandler2(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "web_DiffHandler2")
	defer span.End()

	// Extract: test, left, right where left and right are digests.
	if err := r.ParseForm(); err != nil {
		httputils.ReportError(w, err, "Failed to parse form values", http.StatusInternalServerError)
		return
	}
	// TODO(kjlubick) require corpus
	test := r.Form.Get("test")
	left := r.Form.Get("left")
	right := r.Form.Get("right")
	if test == "" || !validation.IsValidDigest(left) || !validation.IsValidDigest(right) {
		sklog.Debugf("Bad query params: %q %q %q", test, left, right)
		http.Error(w, "invalid query params", http.StatusBadRequest)
		return
	}
	clID := r.Form.Get("changelist_id")
	crs := r.Form.Get("crs")
	if clID != "" {
		if _, ok := wh.getCodeReviewSystem(crs); !ok {
			http.Error(w, "Invalid Code Review System; did you include crs?", http.StatusBadRequest)
			return
		}
	} else {
		crs = ""
	}

	grouping, err := wh.getGroupingForTest(ctx, test)
	if err != nil {
		httputils.ReportError(w, err, "could not get grouping", http.StatusInternalServerError)
		return
	}
	ret, err := wh.Search2API.GetDigestsDiff(ctx, grouping, types.Digest(left), types.Digest(right), clID, crs)
	if err != nil {
		httputils.ReportError(w, err, "Unable to get diff for digests.", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, ret)
}

// ListIgnoreRules returns the current ignore rules in JSON format.
func (wh *Handlers) ListIgnoreRules(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()

	_, includeCounts := r.URL.Query()["counts"]
	// Counting can be expensive, since it goes through every trace.
	if includeCounts {
		if err := wh.limitForAnonUsers(r); err != nil {
			httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
			return
		}
	} else if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	ignores, err := wh.getIgnores(r.Context(), includeCounts)
	if err != nil {
		httputils.ReportError(w, err, "Failed to retrieve ignore rules, there may be none.", http.StatusInternalServerError)
		return
	}

	response := frontend.IgnoresResponse{
		Rules: ignores,
	}

	sendJSONResponse(w, response)
}

// getIgnores fetches the ignores from the store and optionally counts how many
// times they are applied.
func (wh *Handlers) getIgnores(ctx context.Context, withCounts bool) ([]frontend.IgnoreRule, error) {
	rules, err := wh.IgnoreStore.List(ctx)
	if err != nil {
		return nil, skerr.Wrapf(err, "fetching ignores from store")
	}

	// We want to make a slice of pointers because addIgnoreCounts will add the counts in-place.
	ret := make([]frontend.IgnoreRule, 0, len(rules))
	for _, r := range rules {
		fr, err := frontend.ConvertIgnoreRule(r)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		ret = append(ret, fr)
	}

	if withCounts {
		// addIgnoreCounts updates the values of ret directly
		if err := wh.addIgnoreCounts(ctx, ret); err != nil {
			return nil, skerr.Wrapf(err, "adding ignore counts to %d rules", len(ret))
		}
	}

	return ret, nil
}

// addIgnoreCounts goes through the whole tile and counts how many traces each of the rules
// applies to. This uses the most recent index, so there may be some discrepancies in the counts
// if a new rule has been added since the last index was computed.
func (wh *Handlers) addIgnoreCounts(ctx context.Context, rules []frontend.IgnoreRule) error {
	defer metrics2.FuncTimer().Stop()
	sklog.Debugf("adding counts to %d rules", len(rules))

	exp, err := wh.ExpectationsStore.Get(ctx)
	if err != nil {
		return skerr.Wrap(err)
	}
	// Go through every trace and look for only those that are ignored. Then, count how many
	// rules apply to a given ignored trace.
	idx := wh.Indexer.GetIndex()
	nonIgnoredTraces := idx.DigestCountsByTrace(types.ExcludeIgnoredTraces)
	traces := idx.SlicedTraces(types.IncludeIgnoredTraces, nil)
	const numShards = 32
	chunkSize := len(traces) / numShards
	// Very small shards are likely not worth the overhead.
	if chunkSize < 50 {
		chunkSize = 50
	}
	// This mutex protects the passed in rules array and allows the final step of each
	// of the goroutines below to be done safely in parallel to add each shard's results
	// to the total.
	var mutex sync.RWMutex
	err = util.ChunkIterParallel(ctx, len(traces), chunkSize, func(ctx context.Context, start, stop int) error {
		type counts struct {
			Count                   int
			UntriagedCount          int
			ExclusiveCount          int
			ExclusiveUntriagedCount int
		}

		ruleCounts, err := func() ([]counts, error) {
			mutex.RLock()
			defer mutex.RUnlock()

			ruleCounts := make([]counts, len(rules))
			for _, tp := range traces[start:stop] {
				if err := ctx.Err(); err != nil {
					return nil, skerr.Wrap(err)
				}
				id, tr := tp.ID, tp.Trace
				if _, ok := nonIgnoredTraces[id]; ok {
					// This wasn't ignored, so we can skip having to count it
					continue
				}
				idxMatched := -1
				untIdxMatched := -1
				numMatched := 0
				untMatched := 0
				for i, r := range rules {
					if tr.Matches(r.ParsedQuery) {
						numMatched++
						ruleCounts[i].Count++
						idxMatched = i

						// Check to see if the digest is untriaged at head
						if d := tr.AtHead(); d != tiling.MissingDigest && exp.Classification(tr.TestName(), d) == expectations.Untriaged {
							ruleCounts[i].UntriagedCount++
							untMatched++
							untIdxMatched = i
						}
					}
				}
				// Check for any exclusive matches
				if numMatched == 1 {
					ruleCounts[idxMatched].ExclusiveCount++
				}
				if untMatched == 1 {
					ruleCounts[untIdxMatched].ExclusiveUntriagedCount++
				}
			}
			return ruleCounts, nil
		}()
		if err != nil {
			return skerr.Wrap(err)
		}

		mutex.Lock()
		defer mutex.Unlock()
		for i := range rules {
			(&rules[i]).Count += ruleCounts[i].Count
			(&rules[i]).UntriagedCount += ruleCounts[i].UntriagedCount
			(&rules[i]).ExclusiveCount += ruleCounts[i].ExclusiveCount
			(&rules[i]).ExclusiveUntriagedCount += ruleCounts[i].ExclusiveUntriagedCount
		}
		return nil
	})
	return skerr.Wrap(err)
}

// UpdateIgnoreRule updates an existing ignores rule.
func (wh *Handlers) UpdateIgnoreRule(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	user := wh.loggedInAs(r)
	if user == "" {
		http.Error(w, "You must be logged in to update an ignore rule.", http.StatusUnauthorized)
		return
	}
	id := mux.Vars(r)["id"]
	if id == "" {
		http.Error(w, "ID must be non-empty.", http.StatusBadRequest)
		return
	}
	expiresInterval, irb, err := getValidatedIgnoreRule(r)
	if err != nil {
		httputils.ReportError(w, err, "invalid ignore rule input", http.StatusBadRequest)
		return
	}
	ts := now.Now(r.Context())
	ignoreRule := ignore.NewRule(user, ts.Add(expiresInterval), irb.Filter, irb.Note)
	ignoreRule.ID = id
	if err := wh.IgnoreStore.Update(r.Context(), ignoreRule); err != nil {
		httputils.ReportError(w, err, "Unable to update ignore rule", http.StatusInternalServerError)
		return
	}

	sklog.Infof("Successfully updated ignore with id %s", id)
	sendJSONResponse(w, map[string]string{"updated": "true"})
}

// getValidatedIgnoreRule parses the JSON from the given request into an IgnoreRuleBody. As a
// convenience, the duration as a time.Duration is returned.
func getValidatedIgnoreRule(r *http.Request) (time.Duration, frontend.IgnoreRuleBody, error) {
	irb := frontend.IgnoreRuleBody{}
	if err := parseJSON(r, &irb); err != nil {
		return 0, irb, skerr.Wrapf(err, "reading request JSON")
	}
	if irb.Filter == "" {
		return 0, irb, skerr.Fmt("must supply a filter")
	}
	// If a user accidentally includes a huge amount of text, we'd like to catch that here.
	if len(irb.Filter) >= 10*1024 {
		return 0, irb, skerr.Fmt("Filter must be < 10 KB")
	}
	if len(irb.Note) >= 1024 {
		return 0, irb, skerr.Fmt("Note must be < 1 KB")
	}
	d, err := human.ParseDuration(irb.Duration)
	if err != nil {
		return 0, irb, skerr.Wrapf(err, "invalid duration")
	}
	return d, irb, nil
}

// DeleteIgnoreRule deletes an existing ignores rule.
func (wh *Handlers) DeleteIgnoreRule(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	user := wh.loggedInAs(r)
	if user == "" {
		http.Error(w, "You must be logged in to delete an ignore rule", http.StatusUnauthorized)
		return
	}
	id := mux.Vars(r)["id"]
	if id == "" {
		http.Error(w, "ID must be non-empty.", http.StatusBadRequest)
		return
	}

	if err := wh.IgnoreStore.Delete(r.Context(), id); err != nil {
		httputils.ReportError(w, err, "Unable to delete ignore rule", http.StatusInternalServerError)
		return
	}
	sklog.Infof("Successfully deleted ignore with id %s", id)
	sendJSONResponse(w, map[string]string{"deleted": "true"})
}

// AddIgnoreRule is for adding a new ignore rule.
func (wh *Handlers) AddIgnoreRule(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	user := wh.loggedInAs(r)
	if user == "" {
		http.Error(w, "You must be logged in to add an ignore rule", http.StatusUnauthorized)
		return
	}

	expiresInterval, irb, err := getValidatedIgnoreRule(r)
	if err != nil {
		httputils.ReportError(w, err, "invalid ignore rule input", http.StatusBadRequest)
		return
	}
	ts := now.Now(r.Context())
	ignoreRule := ignore.NewRule(user, ts.Add(expiresInterval), irb.Filter, irb.Note)
	if err := wh.IgnoreStore.Create(r.Context(), ignoreRule); err != nil {
		httputils.ReportError(w, err, "Failed to create ignore rule", http.StatusInternalServerError)
		return
	}

	sklog.Infof("Successfully added ignore from %s", user)
	sendJSONResponse(w, map[string]string{"added": "true"})
}

// TriageHandler handles a request to change the triage status of one or more
// digests of one test.
//
// It accepts a POST'd JSON serialization of TriageRequest and updates
// the expectations.
func (wh *Handlers) TriageHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	user := login.LoggedInAs(r)
	if user == "" {
		http.Error(w, "You must be logged in to triage.", http.StatusUnauthorized)
		return
	}

	req := frontend.TriageRequest{}
	if err := parseJSON(r, &req); err != nil {
		httputils.ReportError(w, err, "Failed to parse JSON request.", http.StatusBadRequest)
		return
	}
	sklog.Infof("Triage request: %#v", req)

	if err := wh.triage(r.Context(), user, req); err != nil {
		httputils.ReportError(w, err, "Could not triage", http.StatusInternalServerError)
		return
	}
	// Nothing to return, so just set 200
	w.WriteHeader(http.StatusOK)
}

// triage processes the given TriageRequest.
func (wh *Handlers) triage(ctx context.Context, user string, req frontend.TriageRequest) error {
	// TODO(kjlubick) remove the legacy check for "0" when the frontend no longer sends it.
	if req.ChangelistID != "" && req.ChangelistID != "0" {
		if req.CodeReviewSystem == "" {
			// TODO(kjlubick) remove this default after the search page is converted to lit-html.
			req.CodeReviewSystem = wh.ReviewSystems[0].ID
		}
		if _, ok := wh.getCodeReviewSystem(req.CodeReviewSystem); !ok {
			return skerr.Fmt("Unknown Code Review System; did you remember to include crs?")
		}
	} else {
		req.CodeReviewSystem = ""
	}

	// Build the expectations change request from the list of digests passed in.
	tc := make([]expectations.Delta, 0, len(req.TestDigestStatus))
	for test, digests := range req.TestDigestStatus {
		for d, label := range digests {
			if label == "" {
				// Empty string means the frontend didn't have a closest digest to use when making a
				// "bulk triage to the closest digest" request. It's easier to catch this on the server
				// side than make the JS check for empty string and mutate the POST body.
				continue
			}
			if !expectations.ValidLabel(label) {
				return skerr.Fmt("invalid label %q in triage request", label)
			}
			tc = append(tc, expectations.Delta{
				Grouping: test,
				Digest:   d,
				Label:    label,
			})
		}
	}

	// Use the expectations store for the master branch, unless an issue was given
	// in the request, then get the expectations store for the issue.
	expStore := wh.ExpectationsStore
	// TODO(kjlubick) remove the legacy check here after the frontend bakes in.
	if req.ChangelistID != "" && req.ChangelistID != "0" {
		expStore = wh.ExpectationsStore.ForChangelist(req.ChangelistID, req.CodeReviewSystem)
	}

	// If set, use the image matching algorithm's name as the author of this change.
	if req.ImageMatchingAlgorithm != "" {
		user = req.ImageMatchingAlgorithm
	}

	// Add the change.
	if err := expStore.AddChange(ctx, tc, user); err != nil {
		return skerr.Wrapf(err, "Failed to store the updated expectations.")
	}
	return nil
}

// TriageHandler2 handles a request to change the triage status of one or more
// digests of one test.
//
// It accepts a POST'd JSON serialization of TriageRequest and updates
// the expectations.
// TODO(kjlubick) In V3, this should take groupings, not test names. Additionally, to avoid race
//   conditions where users triage the same thing at the same time, the request should include
//   before and after. Finally, to avoid confusion on CLs, we should fail to apply changes
//   on closed CLs (skbug.com/12122)
func (wh *Handlers) TriageHandler2(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "web_TriageHandler2")
	defer span.End()
	user := login.LoggedInAs(r)
	if user == "" {
		http.Error(w, "You must be logged in to triage.", http.StatusUnauthorized)
		return
	}

	req := frontend.TriageRequest{}
	if err := parseJSON(r, &req); err != nil {
		httputils.ReportError(w, err, "Failed to parse JSON request.", http.StatusBadRequest)
		return
	}
	sklog.Infof("Triage v2 request: %#v", req)

	if err := wh.triage2(ctx, user, req); err != nil {
		httputils.ReportError(w, err, "Could not triage", http.StatusInternalServerError)
		return
	}
	// Nothing to return, so just set 200
	w.WriteHeader(http.StatusOK)
}

func (wh *Handlers) triage2(ctx context.Context, userID string, req frontend.TriageRequest) error {
	ctx, span := trace.StartSpan(ctx, "triage2", trace.WithSampler(trace.AlwaysSample()))
	defer span.End()
	branch := ""
	if req.ChangelistID != "" && req.CodeReviewSystem != "" {
		branch = sql.Qualify(req.CodeReviewSystem, req.ChangelistID)
	}
	// If set, use the image matching algorithm's name as the author of this change.
	if req.ImageMatchingAlgorithm != "" {
		userID = req.ImageMatchingAlgorithm
	}

	deltas, err := wh.convertToDeltas(ctx, req)
	if err != nil {
		return skerr.Wrapf(err, "getting groupings")
	}
	if len(deltas) == 0 {
		return nil
	}
	span.AddAttributes(trace.Int64Attribute("num_changes", int64(len(deltas))))

	err = crdbpgx.ExecuteTx(ctx, wh.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		newRecordID, err := writeRecord(ctx, tx, userID, len(deltas), branch)
		if err != nil {
			return err
		}
		err = fillPreviousLabel(ctx, tx, deltas, newRecordID)
		if err != nil {
			return err
		}
		err = writeDeltas(ctx, tx, deltas)
		if err != nil {
			return err
		}
		if branch == "" {
			return applyDeltasToPrimary(ctx, tx, deltas)
		}
		return applyDeltasToBranch(ctx, tx, deltas, branch)
	})
	if err != nil {
		return skerr.Wrapf(err, "writing %d expectations from %s to branch %q", len(deltas), userID, branch)
	}
	return nil
}

// convertToDeltas converts in triage request (a map) into a slice of deltas. These deltas are
// partially filled out, with only the
func (wh *Handlers) convertToDeltas(ctx context.Context, req frontend.TriageRequest) ([]schema.ExpectationDeltaRow, error) {
	rv := make([]schema.ExpectationDeltaRow, 0, len(req.TestDigestStatus))
	for test, digests := range req.TestDigestStatus {
		for d, label := range digests {
			if label == "" {
				// Empty string means the frontend didn't have a closest digest to use when making a
				// "bulk triage to the closest digest" request. It's easier to catch this on the
				// server side than make the JS check for empty string and mutate the POST body.
				continue
			}
			if !expectations.ValidLabel(label) {
				return nil, skerr.Fmt("invalid label %q in triage request", label)
			}
			labelAfter := schema.FromExpectationLabel(label)
			grouping, err := wh.getGroupingForTest(ctx, string(test))
			if err != nil {
				return nil, skerr.Wrap(err)
			}
			_, groupingID := sql.SerializeMap(grouping)
			digestBytes, err := sql.DigestToBytes(d)
			if err != nil {
				return nil, skerr.Wrap(err)
			}
			rv = append(rv, schema.ExpectationDeltaRow{
				GroupingID: groupingID,
				Digest:     digestBytes,
				LabelAfter: labelAfter,
			})
		}
	}
	return rv, nil
}

// fillPreviousLabel looks up all the expectations for the partially filled-out deltas passed in
// and updates those in-place. It only pulls labels from the primary branch, as this is not meant
// for long term use (see notes for getting to V3 triage).
func fillPreviousLabel(ctx context.Context, tx pgx.Tx, deltas []schema.ExpectationDeltaRow, newRecordID uuid.UUID) error {
	ctx, span := trace.StartSpan(ctx, "fillPreviousLabel")
	defer span.End()
	type expectationKey struct {
		groupingID schema.MD5Hash
		digest     schema.MD5Hash
	}
	toUpdate := map[expectationKey]*schema.ExpectationDeltaRow{}
	for i := range deltas {
		deltas[i].ExpectationRecordID = newRecordID
		deltas[i].LabelBefore = schema.LabelUntriaged
		toUpdate[expectationKey{
			groupingID: sql.AsMD5Hash(deltas[i].GroupingID),
			digest:     sql.AsMD5Hash(deltas[i].Digest),
		}] = &deltas[i]
	}

	statement := `SELECT grouping_id, digest, label FROM Expectations WHERE `
	// We should be safe from injection attacks because we are hex encoding known valid byte arrays.
	// I couldn't find a better way to match multiple composite keys using our usual techniques
	// involving placeholders.
	for i, d := range deltas {
		if i != 0 {
			statement += " OR "
		}
		statement += fmt.Sprintf(`(grouping_id = x'%x' AND digest = x'%x')`, d.GroupingID, d.Digest)
	}
	rows, err := tx.Query(ctx, statement)
	if err != nil {
		return err // don't wrap, could be retried
	}
	defer rows.Close()
	for rows.Next() {
		var gID schema.GroupingID
		var d schema.DigestBytes
		var label schema.ExpectationLabel
		if err := rows.Scan(&gID, &d, &label); err != nil {
			return skerr.Wrap(err) // probably not retryable
		}
		ek := expectationKey{
			groupingID: sql.AsMD5Hash(gID),
			digest:     sql.AsMD5Hash(d),
		}
		row := toUpdate[ek]
		if row == nil {
			sklog.Warningf("Unmatched row with grouping %x and digest %x", gID, d)
			continue // should never happen
		}
		row.LabelBefore = label
	}
	return nil
}

// StatusHandler returns the current status of with respect to HEAD.
func (wh *Handlers) StatusHandler(w http.ResponseWriter, _ *http.Request) {
	defer metrics2.FuncTimer().Stop()

	// This should be an incredibly cheap call and therefore does not count against any quota.
	sendJSONResponse(w, wh.StatusWatcher.GetStatus())
}

// StatusHandler2 returns information about the most recently ingested data and the triage status
// of the various corpora.
func (wh *Handlers) StatusHandler2(w http.ResponseWriter, r *http.Request) {
	_, span := trace.StartSpan(r.Context(), "web_StatusHandler2")
	defer span.End()
	wh.statusCacheMutex.RLock()
	defer wh.statusCacheMutex.RUnlock()
	// This should be an incredibly cheap call and therefore does not count against any quota.
	sendJSONResponse(w, wh.statusCache)
}

// ClusterDiffHandler calculates the NxN diffs of all the digests that match
// the incoming query and returns the data in a format appropriate for
// handling in d3.
func (wh *Handlers) ClusterDiffHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	// Extract the test name as we only allow clustering within a test.
	q := search_query.Search{Limit: 50}
	if err := search_query.ParseSearch(r, &q); err != nil {
		httputils.ReportError(w, err, "Unable to parse query parameter.", http.StatusBadRequest)
		return
	}
	testNames := q.TraceValues[types.PrimaryKeyField]
	if len(testNames) == 0 {
		http.Error(w, "No test name provided.", http.StatusBadRequest)
		return
	}
	testName := testNames[0]
	ctx := r.Context()
	ctx, span := trace.StartSpan(ctx, "ClusterDiff_sql")
	defer span.End()

	idx := wh.Indexer.GetIndex()
	searchResponse, err := wh.SearchAPI.Search(ctx, &q)
	if err != nil {
		httputils.ReportError(w, err, "Search for digests failed.", http.StatusInternalServerError)
		return
	}

	// TODO(kjlubick): Check if we need to sort these
	// Sort the digests so they are displayed with untriaged last, which means
	// they will be displayed 'on top', because in SVG document order is z-order.

	digests := types.DigestSlice{}
	for _, digest := range searchResponse.Results {
		digests = append(digests, digest.Digest)
	}

	digestIndex := map[types.Digest]int{}
	for i, d := range digests {
		digestIndex[d] = i
	}

	d3 := frontend.ClusterDiffResult{
		Test:             types.TestName(testName),
		Nodes:            []frontend.Node{},
		Links:            []frontend.Link{},
		ParamsetByDigest: map[types.Digest]paramtools.ParamSet{},
		ParamsetsUnion:   paramtools.ParamSet{},
	}
	for i, d := range searchResponse.Results {
		d3.Nodes = append(d3.Nodes, frontend.Node{
			Digest: d.Digest,
			Status: d.Status,
		})
		remaining := digests[i:]
		links, err := wh.getLinksBetween(r.Context(), d.Digest, remaining)
		if err != nil {
			httputils.ReportError(w, err, "could not compute diff metrics", http.StatusInternalServerError)
			return
		}
		for otherDigest, distance := range links {
			d3.Links = append(d3.Links, frontend.Link{
				LeftIndex:  digestIndex[d.Digest],
				RightIndex: digestIndex[otherDigest],
				Distance:   distance,
			})
		}
		d3.ParamsetByDigest[d.Digest] = idx.GetParamsetSummary(d.Test, d.Digest, types.ExcludeIgnoredTraces)
		for _, p := range d3.ParamsetByDigest[d.Digest] {
			sort.Strings(p)
		}
		d3.ParamsetsUnion.AddParamSet(d3.ParamsetByDigest[d.Digest])
	}

	for _, p := range d3.ParamsetsUnion {
		sort.Strings(p)
	}

	sendJSONResponse(w, d3)
}

// getLinksBetween queries the SQL DB for the PercentPixelsDiff between the left digest and
// the right digests. It returns them in a map.
func (wh *Handlers) getLinksBetween(ctx context.Context, left types.Digest, right types.DigestSlice) (map[types.Digest]float32, error) {
	ctx, span := trace.StartSpan(ctx, "getLinksBetween")
	span.AddAttributes(trace.Int64Attribute("num_right", int64(len(right))))
	defer span.End()
	const statement = `
SELECT encode(right_digest, 'hex'), percent_pixels_diff FROM DiffMetrics
AS OF SYSTEM TIME '-0.1s'
WHERE left_digest = $1 AND right_digest IN `
	arguments := make([]interface{}, 0, len(right)+1)
	lb, err := sql.DigestToBytes(left)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	arguments = append(arguments, lb)
	for _, r := range right {
		rb, err := sql.DigestToBytes(r)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		arguments = append(arguments, rb)
	}
	vp := sql.ValuesPlaceholders(len(arguments), 1)
	rows, err := wh.DB.Query(ctx, statement+vp, arguments...)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	defer rows.Close()
	rv := map[types.Digest]float32{}
	for rows.Next() {
		var rightD types.Digest
		var linkDistance float32
		if err := rows.Scan(&rightD, &linkDistance); err != nil {
			return nil, skerr.Wrap(err)
		}
		rv[rightD] = linkDistance
	}
	return rv, nil
}

// ClusterDiffRequest contains the options that the frontend provides to the clusterdiff RPC.
type ClusterDiffRequest struct {
	Corpus                  string
	Filters                 paramtools.ParamSet
	IncludePositiveDigests  bool
	IncludeNegativeDigests  bool
	IncludeUntriagedDigests bool
	// TODO(kjlubick) the frontend does not yet support these yet.
	ChangelistID       string
	CodeReviewSystemID string
	PatchsetID         string
}

func parseClusterDiffQuery(r *http.Request) (ClusterDiffRequest, error) {
	if err := r.ParseForm(); err != nil {
		return ClusterDiffRequest{}, skerr.Wrap(err)
	}
	var rv ClusterDiffRequest
	// TODO(kjlubick) rename this field on the UI side
	if corpus := r.FormValue("source_type"); corpus == "" {
		return ClusterDiffRequest{}, skerr.Fmt("Must include corpus")
	} else {
		rv.Corpus = corpus
	}
	if q := r.FormValue("query"); q == "" {
		return ClusterDiffRequest{}, skerr.Fmt("Must include query")
	} else {
		filters, err := url.ParseQuery(q)
		if err != nil {
			return ClusterDiffRequest{}, skerr.Wrapf(err, "invalid query %q", q)
		}
		rv.Filters = paramtools.ParamSet(filters)
	}
	rv.IncludePositiveDigests = r.FormValue("pos") == "true"
	rv.IncludeNegativeDigests = r.FormValue("neg") == "true"
	rv.IncludeUntriagedDigests = r.FormValue("unt") == "true"

	rv.CodeReviewSystemID = r.FormValue("crs")
	rv.ChangelistID = r.FormValue("cl_id")
	rv.PatchsetID = r.FormValue("ps_id")
	return rv, nil
}

// ClusterDiffHandler2 computes the diffs between all digests that match the filters and
// returns them in a way that is convenient for rendering via d3.js
func (wh *Handlers) ClusterDiffHandler2(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "web_ClusterDiffHandler2")
	defer span.End()
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	q, err := parseClusterDiffQuery(r)
	if err != nil {
		httputils.ReportError(w, err, "Invalid requrest", http.StatusBadRequest)
		return
	}

	testNames, ok := q.Filters[types.PrimaryKeyField]
	if !ok || len(testNames) == 0 {
		http.Error(w, "Must include test name", http.StatusBadRequest)
		return
	}
	leftGrouping := paramtools.Params{
		types.CorpusField:     q.Corpus,
		types.PrimaryKeyField: testNames[0],
	}
	delete(q.Filters, types.PrimaryKeyField)
	clusterOpts := search2.ClusterOptions{
		Grouping:                leftGrouping,
		Filters:                 q.Filters,
		IncludePositiveDigests:  q.IncludePositiveDigests,
		IncludeNegativeDigests:  q.IncludeNegativeDigests,
		IncludeUntriagedDigests: q.IncludeUntriagedDigests,

		CodeReviewSystem: q.CodeReviewSystemID,
		ChangelistID:     q.ChangelistID,
		PatchsetID:       q.PatchsetID,
	}
	clusterResp, err := wh.Search2API.GetCluster(ctx, clusterOpts)
	if err != nil {
		httputils.ReportError(w, err, "Unable to compute cluster.", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, clusterResp)
}

// ListTestsHandler returns a summary of the digests seen for a given test.
func (wh *Handlers) ListTestsHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}
	// Inputs: (head, ignored, corpus, keys)
	q, err := frontend.ParseListTestsQuery(r)
	if err != nil {
		httputils.ReportError(w, err, "Failed to parse form data.", http.StatusBadRequest)
		return
	}

	idx := wh.Indexer.GetIndex()
	summaries, err := idx.SummarizeByGrouping(r.Context(), q.Corpus, q.TraceValues, q.IgnoreState, true)
	if err != nil {
		httputils.ReportError(w, err, "Could not compute query.", http.StatusInternalServerError)
		return
	}
	// We explicitly want a zero-length slice instead of a nil slice because the latter serializes
	// to JSON as null instead of []
	tests := make([]frontend.TestSummary, 0, len(summaries))
	for _, s := range summaries {
		if s != nil {
			tests = append(tests, frontend.TestSummary{
				Name:             s.Name,
				PositiveDigests:  s.Pos,
				NegativeDigests:  s.Neg,
				UntriagedDigests: s.Untriaged,
				TotalDigests:     s.Pos + s.Neg + s.Untriaged,
			})
		}
	}
	// For determinism, sort by test name. The client will have the power to sort these differently.
	sort.Slice(tests, func(i, j int) bool {
		return tests[i].Name < tests[j].Name
	})

	// Frontend will have option to hide tests with no digests.
	response := frontend.ListTestsResponse{Tests: tests}
	sendJSONResponse(w, response)
}

func (wh *Handlers) ListTestsHandler2(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "web_ListTestsHandler2")
	defer span.End()
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}
	// Inputs: (head, ignored, corpus, keys)
	q, err := frontend.ParseListTestsQuery(r)
	if err != nil {
		httputils.ReportError(w, err, "Failed to parse form data.", http.StatusBadRequest)
		return
	}

	counts, err := wh.Search2API.CountDigestsByTest(ctx, q)
	if err != nil {
		httputils.ReportError(w, err, "Could not compute query.", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, counts)
}

// TriageLogHandler returns the entries in the triagelog paginated
// in reverse chronological order.
func (wh *Handlers) TriageLogHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	// Get the pagination params.
	q := r.URL.Query()
	offset, size, err := httputils.PaginationParams(q, 0, pageSize, maxPageSize)
	if err != nil {
		httputils.ReportError(w, err, "Invalid Pagination params", http.StatusBadRequest)
		return
	}

	clID := q.Get("changelist_id")
	crs := q.Get("crs")
	if clID != "" {
		if _, ok := wh.getCodeReviewSystem(crs); !ok {
			http.Error(w, "Invalid Code Review System; did you include crs?", http.StatusBadRequest)
			return
		}
	} else {
		crs = ""
	}

	details := q.Get("details") == "true"
	logEntries, total, err := wh.getTriageLog(r.Context(), crs, clID, offset, size, details)

	if err != nil {
		httputils.ReportError(w, err, "Unable to retrieve triage logs", http.StatusInternalServerError)
		return
	}

	response := frontend.TriageLogResponse{
		Entries: logEntries,
		ResponsePagination: httputils.ResponsePagination{
			Offset: offset,
			Size:   size,
			Total:  total,
		},
	}

	sendJSONResponse(w, response)
}

// getTriageLog does the actual work of the TriageLogHandler, but is easier to test.
func (wh *Handlers) getTriageLog(ctx context.Context, crs, changelistID string, offset, size int, withDetails bool) ([]frontend.TriageLogEntry, int, error) {
	expStore := wh.ExpectationsStore
	// TODO(kjlubick) remove this legacy handler
	if changelistID != "" && changelistID != "0" {
		expStore = wh.ExpectationsStore.ForChangelist(changelistID, crs)
	}
	entries, total, err := expStore.QueryLog(ctx, offset, size, withDetails)
	if err != nil {
		return nil, -1, skerr.Wrap(err)
	}
	logEntries := make([]frontend.TriageLogEntry, 0, len(entries))
	for _, e := range entries {
		logEntries = append(logEntries, frontend.ConvertLogEntry(e))
	}
	return logEntries, total, nil
}

// TriageLogHandler2 returns what has been triaged recently.
func (wh *Handlers) TriageLogHandler2(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "web_TriageLogHandler2")
	defer span.End()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	// Get the pagination params.
	q := r.URL.Query()
	offset, size, err := httputils.PaginationParams(q, 0, pageSize, maxPageSize)
	if err != nil {
		httputils.ReportError(w, err, "Invalid Pagination params", http.StatusBadRequest)
		return
	}

	clID := q.Get("changelist_id")
	crs := q.Get("crs")
	if clID != "" {
		if _, ok := wh.getCodeReviewSystem(crs); !ok {
			http.Error(w, "Invalid Code Review System; did you include crs?", http.StatusBadRequest)
			return
		}
	} else {
		crs = ""
	}

	logEntries, total, err := wh.getTriageLog2(ctx, crs, clID, offset, size)
	if err != nil {
		httputils.ReportError(w, err, "Unable to retrieve triage logs", http.StatusInternalServerError)
		return
	}

	response := frontend.TriageLogResponse2{
		Entries: logEntries,
		ResponsePagination: httputils.ResponsePagination{
			Offset: offset,
			Size:   size,
			Total:  total,
		},
	}

	sendJSONResponse(w, response)
}

// getTriageLog2 returns the specified entries and the total count of expectation records.
func (wh *Handlers) getTriageLog2(ctx context.Context, crs, clid string, offset, size int) ([]frontend.TriageLogEntry2, int, error) {
	ctx, span := trace.StartSpan(ctx, "getTriageLog2")
	defer span.End()

	total, err := wh.getTotalTriageRecords(ctx, crs, clid)
	if err != nil {
		return nil, 0, skerr.Wrap(err)
	}
	if total == 0 {
		return []frontend.TriageLogEntry2{}, 0, nil // We don't want null in our JSON response.
	}

	// Default to the primary branch, which is associated with branch_name (i.e. CL) as NULL.
	branchStatement := "WHERE branch_name IS NULL"
	if crs != "" {
		branchStatement = "WHERE branch_name = $3"
	}

	statement := `WITH
RecentRecords AS (
	SELECT expectation_record_id, user_name, triage_time
	FROM ExpectationRecords ` + branchStatement + `
	ORDER BY triage_time DESC, expectation_record_id
	OFFSET $1 LIMIT $2
)
SELECT RecentRecords.*, Groupings.keys, digest, label_before, label_after
FROM RecentRecords
	JOIN ExpectationDeltas ON RecentRecords.expectation_record_id = ExpectationDeltas.expectation_record_id
JOIN Groupings ON ExpectationDeltas.grouping_id = Groupings.grouping_id
ORDER BY triage_time DESC, expectation_record_id, digest
`
	args := []interface{}{offset, size}
	if crs != "" {
		args = append(args, sql.Qualify(crs, clid))
	}
	rows, err := wh.DB.Query(ctx, statement, args...)
	if err != nil {
		return nil, 0, skerr.Wrap(err)
	}
	defer rows.Close()
	var currentEntry *frontend.TriageLogEntry2
	var rv []frontend.TriageLogEntry2
	for rows.Next() {
		var record schema.ExpectationRecordRow
		var delta schema.ExpectationDeltaRow
		var grouping paramtools.Params
		if err := rows.Scan(&record.ExpectationRecordID, &record.UserName, &record.TriageTime,
			&grouping, &delta.Digest, &delta.LabelBefore, &delta.LabelAfter); err != nil {
			return nil, 0, skerr.Wrap(err)
		}
		if currentEntry == nil || currentEntry.ID != record.ExpectationRecordID.String() {
			rv = append(rv, frontend.TriageLogEntry2{
				ID:   record.ExpectationRecordID.String(),
				User: record.UserName,
				// Multiply by 1000 to convert seconds to milliseconds
				TS: record.TriageTime.UTC().Unix() * 1000,
			})
			currentEntry = &rv[len(rv)-1]
		}
		currentEntry.Details = append(currentEntry.Details, frontend.TriageDelta2{
			Grouping:    grouping,
			Digest:      types.Digest(hex.EncodeToString(delta.Digest)),
			LabelBefore: delta.LabelBefore.ToExpectation(),
			LabelAfter:  delta.LabelAfter.ToExpectation(),
		})
	}
	return rv, total, nil
}

// getTotalTriageRecords returns the total number of triage records for the CL (or the primary
// branch)
func (wh *Handlers) getTotalTriageRecords(ctx context.Context, crs, clid string) (int, error) {
	ctx, span := trace.StartSpan(ctx, "getTotalTriageRecords")
	defer span.End()

	branchStatement := "WHERE branch_name IS NULL"
	if crs != "" {
		branchStatement = "WHERE branch_name = $1"
	}

	statement := `SELECT COUNT(*) FROM ExpectationRecords ` + branchStatement
	var args []interface{}
	if crs != "" {
		args = append(args, sql.Qualify(crs, clid))
	}
	row := wh.DB.QueryRow(ctx, statement, args...)
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, skerr.Wrap(err)
	}
	return count, nil
}

// TriageUndoHandler performs an "undo" for a given change id.
// The change id's are returned in the result of jsonTriageLogHandler.
// It accepts one query parameter 'id' which is the id if the change
// that should be reversed.
// If successful it returns the same result as a call to jsonTriageLogHandler
// to reflect the changed triagelog.
// TODO(kjlubick): This does not properly handle undoing of ChangelistExpectations.
func (wh *Handlers) TriageUndoHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	// Get the user and make sure they are logged in.
	user := login.LoggedInAs(r)
	if user == "" {
		http.Error(w, "You must be logged in to change expectations", http.StatusUnauthorized)
		return
	}

	// Extract the id to undo.
	changeID := r.URL.Query().Get("id")

	// Do the undo procedure.
	if err := wh.ExpectationsStore.UndoChange(r.Context(), changeID, user); err != nil {
		httputils.ReportError(w, err, "Unable to undo.", http.StatusInternalServerError)
		return
	}

	// Send the same response as a query for the first page.
	wh.TriageLogHandler(w, r)
}

// TriageUndoHandler2 performs an "undo" for a given id. This id corresponds to the record id of the
// set of changes in the DB.
// If successful it returns the same result as a call to TriageLogHandler2 to reflect the changes.
func (wh *Handlers) TriageUndoHandler2(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "web_TriageUndoHandler2")
	defer span.End()
	// Get the user and make sure they are logged in.
	user := login.LoggedInAs(r)
	if user == "" {
		http.Error(w, "You must be logged in to change expectations", http.StatusUnauthorized)
		return
	}

	// Extract the id to undo.
	changeID := r.URL.Query().Get("id")

	// Do the undo procedure.
	if err := wh.undoExpectationChanges(ctx, changeID, user); err != nil {
		httputils.ReportError(w, err, "Unable to undo.", http.StatusInternalServerError)
		return
	}

	// Send the same response as a query for the first page.
	wh.TriageLogHandler2(w, r)
}

// undoExpectationChanges will look up all ExpectationDeltas associated with the record that has
// the given ID. It will set the current expectations for those digests/groupings to be the
// label_before value. This will all be done in a transaction.
func (wh *Handlers) undoExpectationChanges(ctx context.Context, recordID, userID string) error {
	ctx, span := trace.StartSpan(ctx, "undoExpectationChanges")
	defer span.End()

	err := crdbpgx.ExecuteTx(ctx, wh.DB, pgx.TxOptions{}, func(tx pgx.Tx) error {
		deltas, err := getDeltasForRecord(ctx, tx, recordID)
		if err != nil {
			return err // Don't wrap - crdbpgx might retry
		}
		if len(deltas) == 0 {
			return skerr.Fmt("no expectation deltas found for record %s", recordID)
		}
		branchNameRow := tx.QueryRow(ctx, `SELECT branch_name FROM ExpectationRecords WHERE expectation_record_id = $1`, recordID)
		var branchOfOriginal pgtype.Text
		if err := branchNameRow.Scan(&branchOfOriginal); err != nil {
			return err
		}

		newRecordID, err := writeRecord(ctx, tx, userID, len(deltas), branchOfOriginal.String)
		if err != nil {
			return err
		}

		invertedDeltas := invertDeltas(deltas, newRecordID)
		if err := writeDeltas(ctx, tx, invertedDeltas); err != nil {
			return err
		}

		if branchOfOriginal.Status != pgtype.Present {
			err = applyDeltasToPrimary(ctx, tx, invertedDeltas)
		} else {
			err = applyDeltasToBranch(ctx, tx, invertedDeltas, branchOfOriginal.String)
		}
		return err
	})
	if err != nil {
		return skerr.Wrap(err)
	}
	return nil
}

// writeRecord writes a new ExpectationRecord to the DB.
func writeRecord(ctx context.Context, tx pgx.Tx, userID string, numChanges int, branch string) (uuid.UUID, error) {
	ctx, span := trace.StartSpan(ctx, "writeRecord")
	defer span.End()

	var br *string
	if branch != "" {
		br = &branch
	}
	const statement = `INSERT INTO ExpectationRecords
(user_name, triage_time, num_changes, branch_name) VALUES ($1, $2, $3, $4) RETURNING expectation_record_id`
	row := tx.QueryRow(ctx, statement, userID, now.Now(ctx), numChanges, br)
	var recordUUID uuid.UUID
	err := row.Scan(&recordUUID)
	if err != nil {
		return uuid.UUID{}, err
	}
	return recordUUID, nil
}

// invertDeltas returns a slice of deltas corresponding to the same grouping+digest pairs as the
// original slice, but with inverted before/after labels and a new record ID.
func invertDeltas(deltas []schema.ExpectationDeltaRow, newRecordID uuid.UUID) []schema.ExpectationDeltaRow {
	var rv []schema.ExpectationDeltaRow
	for _, d := range deltas {
		rv = append(rv, schema.ExpectationDeltaRow{
			ExpectationRecordID: newRecordID,
			GroupingID:          d.GroupingID,
			Digest:              d.Digest,
			LabelBefore:         d.LabelAfter, // Intentionally flipped around
			LabelAfter:          d.LabelBefore,
		})
	}
	return rv
}

// getDeltasForRecord returns all ExpectationDeltaRows for the given record ID.
func getDeltasForRecord(ctx context.Context, tx pgx.Tx, recordID string) ([]schema.ExpectationDeltaRow, error) {
	ctx, span := trace.StartSpan(ctx, "getDeltasForRecord")
	defer span.End()
	const statement = `SELECT grouping_id, digest, label_before, label_after
FROM ExpectationDeltas WHERE expectation_record_id = $1`
	rows, err := tx.Query(ctx, statement, recordID)
	if err != nil {
		return nil, err // Don't wrap - crdbpgx might retry
	}
	defer rows.Close()
	var deltas []schema.ExpectationDeltaRow
	for rows.Next() {
		var row schema.ExpectationDeltaRow
		if err := rows.Scan(&row.GroupingID, &row.Digest, &row.LabelBefore, &row.LabelAfter); err != nil {
			return nil, skerr.Wrap(err) // probably not retriable
		}
		deltas = append(deltas, row)
	}
	return deltas, nil
}

// writeDeltas writes the given rows to the SQL DB.
func writeDeltas(ctx context.Context, tx pgx.Tx, deltas []schema.ExpectationDeltaRow) error {
	ctx, span := trace.StartSpan(ctx, "writeDeltas")
	defer span.End()

	const statement = `INSERT INTO ExpectationDeltas
(expectation_record_id, grouping_id, digest, label_before, label_after) VALUES `
	const valuesPerRow = 5
	vp := sql.ValuesPlaceholders(valuesPerRow, len(deltas))
	arguments := make([]interface{}, 0, len(deltas)*valuesPerRow)
	for _, d := range deltas {
		arguments = append(arguments, d.ExpectationRecordID, d.GroupingID, d.Digest, d.LabelBefore, d.LabelAfter)
	}
	_, err := tx.Exec(ctx, statement+vp, arguments...)
	return err // don't wrap, could be retryable
}

// applyDeltasToPrimary applies the given deltas to the primary branch expectations.
func applyDeltasToPrimary(ctx context.Context, tx pgx.Tx, deltas []schema.ExpectationDeltaRow) error {
	ctx, span := trace.StartSpan(ctx, "applyDeltasToPrimary")
	defer span.End()

	const statement = `UPSERT INTO Expectations
(grouping_id, digest, label, expectation_record_id) VALUES `
	const valuesPerRow = 4
	vp := sql.ValuesPlaceholders(valuesPerRow, len(deltas))
	arguments := make([]interface{}, 0, len(deltas)*valuesPerRow)
	for _, d := range deltas {
		arguments = append(arguments, d.GroupingID, d.Digest, d.LabelAfter, d.ExpectationRecordID)
	}
	_, err := tx.Exec(ctx, statement+vp, arguments...)
	return err // don't wrap, could be retryable
}

// applyDeltasToBranch applies the given deltas to the given branch (i.e. CL).
func applyDeltasToBranch(ctx context.Context, tx pgx.Tx, deltas []schema.ExpectationDeltaRow, branch string) error {
	ctx, span := trace.StartSpan(ctx, "applyInvertedDeltasToBranch")
	defer span.End()

	const statement = `UPSERT INTO SecondaryBranchExpectations
(branch_name, grouping_id, digest, label, expectation_record_id) VALUES `
	const valuesPerRow = 5
	vp := sql.ValuesPlaceholders(valuesPerRow, len(deltas))
	arguments := make([]interface{}, 0, len(deltas)*valuesPerRow)
	for _, d := range deltas {
		arguments = append(arguments, branch, d.GroupingID, d.Digest, d.LabelAfter, d.ExpectationRecordID)
	}
	_, err := tx.Exec(ctx, statement+vp, arguments...)
	return err // don't wrap, could be retryable
}

// ParamsHandler returns the union of all parameters.
func (wh *Handlers) ParamsHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		httputils.ReportError(w, err, "Invalid form headers", http.StatusBadRequest)
		return
	}
	clID := r.Form.Get("changelist_id")
	crs := r.Form.Get("crs")
	if clID != "" {
		if crs == "" {
			// TODO(kjlubick) remove this default after the search page is converted to lit-html.
			crs = wh.ReviewSystems[0].ID
		}
		if _, ok := wh.getCodeReviewSystem(crs); !ok {
			http.Error(w, "Invalid Code Review System; did you include crs?", http.StatusBadRequest)
			return
		}
	} else {
		crs = ""
	}

	if clID != "" {
		clIdx := wh.Indexer.GetIndexForCL(crs, clID)
		if clIdx != nil {
			sendJSONResponse(w, clIdx.ParamSet)
			return
		}
		// Fallback to master branch
	}

	tile := wh.Indexer.GetIndex().Tile().GetTile(types.IncludeIgnoredTraces)
	sendJSONResponse(w, tile.ParamSet)
}

// ParamsHandler2 returns all Params that could be searched over. It uses the SQL Backend
func (wh *Handlers) ParamsHandler2(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		httputils.ReportError(w, err, "Invalid form headers", http.StatusBadRequest)
		return
	}
	clID := r.Form.Get("changelist_id")
	crs := r.Form.Get("crs")

	if clID == "" {
		ps, err := wh.Search2API.GetPrimaryBranchParamset(r.Context())
		if err != nil {
			httputils.ReportError(w, err, "Could not get paramset for primary branch", http.StatusInternalServerError)
			return
		}
		sendJSONResponse(w, ps)
		return
	}

	if _, ok := wh.getCodeReviewSystem(crs); !ok {
		http.Error(w, "Invalid Code Review System; did you include crs?", http.StatusBadRequest)
		return
	}
	ps, err := wh.Search2API.GetChangelistParamset(r.Context(), crs, clID)
	if err != nil {
		httputils.ReportError(w, err, "Could not get paramset for given CL", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, ps)
}

// CommitsHandler returns the commits from the most recent tile.
func (wh *Handlers) CommitsHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	cpxTile := wh.TileSource.GetTile()
	if cpxTile == nil {
		httputils.ReportError(w, nil, "Not loaded yet - try back later", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(frontend.FromTilingCommits(cpxTile.DataCommits())); err != nil {
		sklog.Errorf("Failed to write or encode result: %s", err)
	}
}

// CommitsHandler2 returns the last n commits with data that make up the sliding window.
func (wh *Handlers) CommitsHandler2(w http.ResponseWriter, r *http.Request) {
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}
	ctx, span := trace.StartSpan(r.Context(), "web_CommitsHandler2")
	defer span.End()

	commits, err := wh.Search2API.GetCommitsInWindow(ctx)
	if err != nil {
		httputils.ReportError(w, err, "Could not get commits", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, commits)
}

// TextKnownHashesProxy returns known hashes that have been written to GCS in the background
// Each line contains a single digest for an image. Bots will then only upload images which
// have a hash not found on this list, avoiding significant amounts of unnecessary uploads.
func (wh *Handlers) TextKnownHashesProxy(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	// No limit for anon users - this is an endpoint backed up by baseline servers, and
	// should be able to handle a large load.

	w.Header().Set("Content-Type", "text/plain")
	if err := wh.GCSClient.LoadKnownDigests(r.Context(), w); err != nil {
		sklog.Errorf("Failed to copy the known hashes from GCS: %s", err)
		return
	}
}

// BaselineHandlerV2 returns a JSON representation of that baseline including
// baselines for a options issue. It can respond to requests like these:
//
//    /json/expectations
//    /json/expectations?issue=123456
//
// The "issue" parameter indicates the changelist ID for which we would like to
// retrieve the baseline. In that case the returned options will be a blend of
// the master baseline and the baseline defined for the changelist (usually
// based on tryjob results).
func (wh *Handlers) BaselineHandlerV2(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "frontend_BaselineHandlerV2")
	defer span.End()
	// No limit for anon users - this is an endpoint backed up by baseline servers, and
	// should be able to handle a large load.

	q := r.URL.Query()
	clID := q.Get("issue")
	crs := q.Get("crs")

	if clID != "" {
		if _, ok := wh.getCodeReviewSystem(crs); !ok {
			http.Error(w, "Invalid CRS provided.", http.StatusBadRequest)
			return
		}
	} else {
		crs = ""
	}

	bl, err := wh.fetchBaseline(ctx, crs, clID)
	if err != nil {
		httputils.ReportError(w, err, "Fetching baseline failed.", http.StatusInternalServerError)
		return
	}

	sendJSONResponse(w, bl)
}

// fetchBaseline returns an object that contains all the positive and negatively triaged digests
// for either the primary branch or the primary branch and the CL. As per usual, the triage status
// on a CL overrides the triage status on the primary branch.
func (wh *Handlers) fetchBaseline(ctx context.Context, crs, clID string) (frontend.BaselineV2Response, error) {
	ctx, span := trace.StartSpan(ctx, "fetchBaseline")
	defer span.End()

	statement := `WITH
PrimaryBranchExps AS (
	SELECT grouping_id, digest, label FROM Expectations
	WHERE label = 'n' OR label = 'p'
)`
	var args []interface{}
	if crs == "" {
		span.AddAttributes(trace.StringAttribute("type", "primary"))
		statement += `
SELECT Groupings.keys ->> 'name', encode(digest, 'hex'), label FROM PrimaryBranchExps
JOIN Groupings ON PrimaryBranchExps.grouping_id = Groupings.grouping_id`
	} else {
		span.AddAttributes(trace.StringAttribute("type", "changelist"))
		qCLID := sql.Qualify(crs, clID)
		statement += `,
CLExps AS (
	SELECT grouping_id, digest, label FROM SecondaryBranchExpectations
	WHERE branch_name = $1
),
JoinedExps AS (
	SELECT COALESCE(CLExps.grouping_id, PrimaryBranchExps.grouping_id) as grouping_id,
		COALESCE(CLExps.digest, PrimaryBranchExps.digest) as digest,
		COALESCE(CLExps.label, PrimaryBranchExps.label, 'u') as label
    FROM CLExps FULL OUTER JOIN PrimaryBranchExps ON
		CLExps.grouping_id = PrimaryBranchExps.grouping_id
		AND CLExps.digest = PrimaryBranchExps.digest
)
SELECT Groupings.keys ->> 'name', encode(digest, 'hex'), label FROM JoinedExps
JOIN Groupings ON JoinedExps.grouping_id = Groupings.grouping_id
WHERE label = 'n' OR label = 'p'`
		args = append(args, qCLID)
	}
	rows, err := wh.DB.Query(ctx, statement, args...)
	if err != nil {
		return frontend.BaselineV2Response{}, skerr.Wrap(err)
	}
	defer rows.Close()
	baseline := expectations.Baseline{}
	for rows.Next() {
		var testName types.TestName
		var digest types.Digest
		var label schema.ExpectationLabel
		if err := rows.Scan(&testName, &digest, &label); err != nil {
			return frontend.BaselineV2Response{}, skerr.Wrap(err)
		}
		byDigest, ok := baseline[testName]
		if !ok {
			byDigest = map[types.Digest]expectations.Label{}
			baseline[testName] = byDigest
		}
		byDigest[digest] = label.ToExpectation()
	}

	return frontend.BaselineV2Response{
		CodeReviewSystem: crs,
		ChangelistID:     clID,
		Expectations:     baseline,
	}, nil
}

// MakeResourceHandler creates a static file handler that sets a caching policy.
func MakeResourceHandler(resourceDir string) func(http.ResponseWriter, *http.Request) {
	fileServer := http.FileServer(http.Dir(resourceDir))
	return func(w http.ResponseWriter, r *http.Request) {
		defer metrics2.FuncTimer().Stop()
		// No limit for anon users - this should be fast enough to handle a large load.
		w.Header().Add("Cache-Control", "max-age=300")
		fileServer.ServeHTTP(w, r)
	}
}

// DigestListHandler returns a list of digests for a given test. This is used by goldctl's
// local diff tech.
func (wh *Handlers) DigestListHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	if err := r.ParseForm(); err != nil {
		httputils.ReportError(w, err, "Failed to parse form values", http.StatusInternalServerError)
		return
	}

	test := r.Form.Get("test")
	corpus := r.Form.Get("corpus")
	if test == "" || corpus == "" {
		http.Error(w, "You must include 'test' and 'corpus'", http.StatusBadRequest)
		return
	}

	out := wh.getDigestsResponse(test, corpus)
	sendJSONResponse(w, out)
}

// getDigestsResponse returns the digests belonging to the given test (and eventually corpus).
func (wh *Handlers) getDigestsResponse(test, corpus string) frontend.DigestListResponse {
	// TODO(kjlubick): Grouping by only test is something we should avoid. We should
	// at least group by test and corpus, but maybe something more robust depending
	// on the instance (e.g. Skia might want to group by colorspace)
	idx := wh.Indexer.GetIndex()
	dc := idx.DigestCountsByTest(types.IncludeIgnoredTraces)

	var xd []types.Digest
	for d := range dc[types.TestName(test)] {
		xd = append(xd, d)
	}

	// Sort alphabetically for determinism
	sort.Slice(xd, func(i, j int) bool {
		return xd[i] < xd[j]
	})

	return frontend.DigestListResponse{
		Digests: xd,
	}
}

// DigestListHandler2 returns a list of digests for a given test. This is used by goldctl's
// local diff tech.
func (wh *Handlers) DigestListHandler2(w http.ResponseWriter, r *http.Request) {
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}
	ctx, span := trace.StartSpan(r.Context(), "web_DigestListHandler2")
	defer span.End()

	if err := r.ParseForm(); err != nil {
		httputils.ReportError(w, err, "Failed to parse form values", http.StatusInternalServerError)
		return
	}

	encodedGrouping := r.Form.Get("grouping")
	if encodedGrouping == "" {
		http.Error(w, "You must include 'grouping'", http.StatusBadRequest)
		return
	}
	groupingSet, err := url.ParseQuery(encodedGrouping)
	if err != nil {
		httputils.ReportError(w, skerr.Wrapf(err, "bad grouping %s", encodedGrouping), "Invalid grouping", http.StatusBadRequest)
		return
	}
	grouping := make(paramtools.Params, len(groupingSet))
	for key, values := range groupingSet {
		if len(values) == 0 {
			continue
		}
		grouping[key] = values[0]
	}

	// If needed, we could add a TTL cache here.
	out, err := wh.Search2API.GetDigestsForGrouping(ctx, grouping)
	if err != nil {
		httputils.ReportError(w, err, "Could not retrieve digests", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, out)
}

// Whoami returns the email address of the user or service account used to authenticate the
// request. For debugging purposes only.
func (wh *Handlers) Whoami(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	user := wh.loggedInAs(r)
	sendJSONResponse(w, map[string]string{"whoami": user})
}

func (wh *Handlers) LatestPositiveDigestHandler(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	traceId, ok := mux.Vars(r)["traceId"]
	if !ok {
		http.Error(w, "Must specify traceId.", http.StatusBadRequest)
		return
	}

	digest, err := wh.Indexer.GetIndex().MostRecentPositiveDigest(r.Context(), tiling.TraceID(traceId))
	if err != nil {
		httputils.ReportError(w, err, "Could not retrieve most recent positive digest.", http.StatusInternalServerError)
		return
	}

	sendJSONResponse(w, frontend.MostRecentPositiveDigestResponse{Digest: digest})
}

// LatestPositiveDigestHandler2 returns the most recent positive digest for the given trace.
// Starting at the tip of tree, it will skip over any missing data, untriaged digests or digests
// triaged negative until it finds a positive digest.
func (wh *Handlers) LatestPositiveDigestHandler2(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "web_LatestPositiveDigestHandler2")
	defer span.End()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}

	tID, ok := mux.Vars(r)["traceID"]
	if !ok {
		http.Error(w, "Must specify traceID.", http.StatusBadRequest)
		return
	}

	traceKeys, err := tiling.ParseTraceID(tID)
	if err != nil || len(traceKeys) == 0 {
		httputils.ReportError(w, err, "Invalid traceID.", http.StatusBadRequest)
		return
	}

	_, traceID := sql.SerializeMap(traceKeys)
	digest, err := wh.getLatestPositiveDigest(ctx, traceID)
	if err != nil {
		httputils.ReportError(w, err, "Could not complete query.", http.StatusInternalServerError)
		return
	}
	sendJSONResponse(w, frontend.MostRecentPositiveDigestResponse{Digest: digest})
}

func (wh *Handlers) getLatestPositiveDigest(ctx context.Context, traceID schema.TraceID) (types.Digest, error) {
	ctx, span := trace.StartSpan(ctx, "getLatestPositiveDigest")
	defer span.End()

	const statement = `WITH
RecentDigests AS (
	SELECT digest, commit_id, grouping_id FROM TraceValues WHERE trace_id = $1
	ORDER BY commit_id DESC LIMIT 1000 -- arbitrary limit
)
SELECT encode(RecentDigests.digest, 'hex') FROM RecentDigests
JOIN Expectations ON Expectations.grouping_id = RecentDigests.grouping_id AND
	Expectations.digest = RecentDigests.digest
WHERE label = 'p'
ORDER BY commit_id DESC LIMIT 1
`
	row := wh.DB.QueryRow(ctx, statement, traceID)
	var digest types.Digest
	if err := row.Scan(&digest); err != nil {
		if err == pgx.ErrNoRows {
			return "", nil
		}
		return "", skerr.Wrap(err)
	}
	return digest, nil
}

// GetPerTraceDigestsByTestName returns the digests in the current trace for the given test name
// and corpus, grouped by trace ID.
func (wh *Handlers) GetPerTraceDigestsByTestName(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.limitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
	}

	corpus, ok := mux.Vars(r)["corpus"]
	if !ok {
		http.Error(w, "Must specify corpus.", http.StatusBadRequest)
		return
	}

	testName, ok := mux.Vars(r)["testName"]
	if !ok {
		http.Error(w, "Must specify testName.", http.StatusBadRequest)
		return
	}

	digestsByTraceId := frontend.GetPerTraceDigestsByTestNameResponse{}

	// Iterate over all traces in the current tile for the given test name.
	tracesById := wh.Indexer.GetIndex().SlicedTraces(types.IncludeIgnoredTraces, map[string][]string{
		types.CorpusField:     {corpus},
		types.PrimaryKeyField: {testName},
	})
	for _, tracePair := range tracesById {
		// Populate map with the trace's digests.
		digestsByTraceId[tracePair.ID] = tracePair.Trace.Digests
	}

	sendJSONResponse(w, digestsByTraceId)
}

const maxFlakyTraces = 10000 // We don't want to return a slice longer than this because it could
// end up with a result that is too big. 10k * ~200 bytes per trace means this return size will be
// <= 2MB.

// GetFlakyTracesData returns all traces with a number of unique digests (in the current sliding
// window of commits) greater than or equal to a certain threshold.
func (wh *Handlers) GetFlakyTracesData(w http.ResponseWriter, r *http.Request) {
	defer metrics2.FuncTimer().Stop()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
	}

	minUniqueDigests := 10
	minUniqueDigestsStr, ok := mux.Vars(r)["minUniqueDigests"]
	if ok {
		var err error
		minUniqueDigests, err = strconv.Atoi(minUniqueDigestsStr)
		if err != nil {
			httputils.ReportError(w, err, "invalid value for minUniqueDigests", http.StatusBadRequest)
			return
		}
	}

	idx := wh.Indexer.GetIndex()
	counts := idx.DigestCountsByTrace(types.IncludeIgnoredTraces)

	flakyData := frontend.FlakyTracesDataResponse{
		TileSize:    len(idx.Tile().DataCommits()),
		TotalTraces: len(counts),
	}

	for traceID, dc := range counts {
		if len(dc) >= minUniqueDigests {
			flakyData.Traces = append(flakyData.Traces, frontend.FlakyTrace{
				ID:            traceID,
				UniqueDigests: len(dc),
			})
		}
	}
	flakyData.TotalFlakyTraces = len(flakyData.Traces)

	// Sort the flakiest traces first.
	sort.Slice(flakyData.Traces, func(i, j int) bool {
		if flakyData.Traces[i].UniqueDigests == flakyData.Traces[j].UniqueDigests {
			return flakyData.Traces[i].ID < flakyData.Traces[j].ID
		}
		return flakyData.Traces[i].UniqueDigests > flakyData.Traces[j].UniqueDigests
	})

	// Limit the number of traces to maxFlakyTraces, if needed.
	if len(flakyData.Traces) > maxFlakyTraces {
		flakyData.Traces = flakyData.Traces[:maxFlakyTraces]
	}

	sendJSONResponse(w, flakyData)
}

// ChangelistSearchRedirect redirects the user to a search page showing the search results
// for a given CL. It will do a (hopefully) quick scan of the untriaged digests - if it finds some,
// it will include the corpus containing some of those untriaged digests in the search query so the
// user will see results (instead of getting directed to a corpus with no results).
func (wh *Handlers) ChangelistSearchRedirect(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "web_ChangelistSearchRedirect")
	defer span.End()
	if err := wh.cheapLimitForAnonUsers(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
	}

	requestVars := mux.Vars(r)
	crs, ok := requestVars["system"]
	if !ok {
		http.Error(w, "Must specify 'system' of Changelist.", http.StatusBadRequest)
		return
	}
	clID, ok := requestVars["id"]
	if !ok {
		http.Error(w, "Must specify 'id' of Changelist.", http.StatusBadRequest)
		return
	}
	_, ok = wh.getCodeReviewSystem(crs)
	if !ok {
		http.Error(w, "Invalid Code Review System", http.StatusBadRequest)
		return
	}

	qualifiedPSID, psOrder, err := wh.getLatestPatchset(ctx, crs, clID)
	if err != nil {
		httputils.ReportError(w, err, "Could not find latest patchset", http.StatusNotFound)
		return
	}
	// TODO(kjlubick) when we change the patchsets arg to not be a list of orders, we should
	//   update it here too (probably specify the ps id).
	baseURL := fmt.Sprintf("/search?issue=%s&crs=%s&patchsets=%d", clID, crs, psOrder)

	corporaWithUntriagedUnignoredDigests, err := wh.getActionableDigests(ctx, crs, clID, qualifiedPSID)
	if err != nil {
		sklog.Errorf("Error getting digests for CL %s from CRS %s with PS %s: %s", clID, crs, qualifiedPSID, err)
		http.Redirect(w, r, baseURL, http.StatusTemporaryRedirect)
		return
	}
	if len(corporaWithUntriagedUnignoredDigests) == 0 {
		http.Redirect(w, r, baseURL, http.StatusTemporaryRedirect)
		return
	}
	http.Redirect(w, r, baseURL+"&corpus="+corporaWithUntriagedUnignoredDigests[0].Corpus, http.StatusTemporaryRedirect)
}

// getLatestPatchset returns the latest patchset for a given CL. It goes off of created_ts, due
// to the fact that (for GitHub) rebases can happen and potentially cause ps_order to be off.
func (wh *Handlers) getLatestPatchset(ctx context.Context, crs, clID string) (string, int, error) {
	ctx, span := trace.StartSpan(ctx, "getLatestPatchset")
	defer span.End()
	const statement = `SELECT patchset_id, ps_order FROM Patchsets
WHERE changelist_id = $1
ORDER BY created_ts DESC, ps_order DESC
LIMIT 1`
	row := wh.DB.QueryRow(ctx, statement, sql.Qualify(crs, clID))
	var qualifiedID string
	var order int
	if err := row.Scan(&qualifiedID, &order); err != nil {
		return "", 0, skerr.Wrap(err)
	}
	return qualifiedID, order, nil
}

type corpusAndCount struct {
	Corpus string
	Count  int
}

// getActionableDigests returns a list of corpus and the number of untriaged, not-ignored digests
// that have been seen in the data for the given PS. We choose *not* to strip out digests that
// are already on the primary branch because that additional join makes this query too slow.
// As is, it can take 3-4 seconds on a large instance like Skia. The return value will be sorted
// by count, with the corpus name being the tie-breaker.
func (wh *Handlers) getActionableDigests(ctx context.Context, crs, clID, qPSID string) ([]corpusAndCount, error) {
	ctx, span := trace.StartSpan(ctx, "getActionableDigests")
	defer span.End()

	const statement = `WITH
DataFromCL AS (
    SELECT secondary_branch_trace_id, SecondaryBranchValues.grouping_id, digest
    FROM SecondaryBranchValues WHERE branch_name = $1 AND version_name = $2
),
ExpectationsForCL AS (
    SELECT grouping_id, digest, label
    FROM SecondaryBranchExpectations
    WHERE branch_name = $1
),
JoinedExpectations AS (
    SELECT COALESCE(ExpectationsForCL.grouping_id, Expectations.grouping_id) AS grouping_id,
        COALESCE(ExpectationsForCL.digest, Expectations.digest) AS digest,
        COALESCE(ExpectationsForCL.label, Expectations.label, 'u') AS label
    FROM ExpectationsForCL FULL OUTER JOIN Expectations ON
    ExpectationsForCL.grouping_id = Expectations.grouping_id
        AND ExpectationsForCL.digest = Expectations.digest
),
UntriagedData AS (
    SELECT secondary_branch_trace_id, DataFromCL.grouping_id, DataFromCL.digest FROM DataFromCL
    LEFT JOIN JoinedExpectations ON DataFromCL.grouping_id = JoinedExpectations.grouping_id
        AND DataFromCL.digest = JoinedExpectations.digest
    WHERE label = 'u' OR label IS NULL
),
UnignoredUntriagedData AS (
    SELECT DISTINCT UntriagedData.grouping_id, digest FROM UntriagedData
    JOIN Traces ON UntriagedData.secondary_branch_trace_id = Traces.trace_id
    AND matches_any_ignore_rule = FALSE
)
SELECT keys->>'source_type', COUNT(*) FROM Groupings JOIN UnignoredUntriagedData
    ON Groupings.grouping_id = UnignoredUntriagedData.grouping_id
GROUP BY 1
ORDER BY 2 DESC, 1 ASC`

	rows, err := wh.DB.Query(ctx, statement, sql.Qualify(crs, clID), qPSID)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	defer rows.Close()
	var rv []corpusAndCount
	for rows.Next() {
		var c corpusAndCount
		if err := rows.Scan(&c.Corpus, &c.Count); err != nil {
			return nil, skerr.Wrap(err)
		}
		rv = append(rv, c)
	}
	return rv, nil
}

func (wh *Handlers) loggedInAs(r *http.Request) string {
	if wh.testingAuthAs != "" {
		return wh.testingAuthAs
	}
	return login.LoggedInAs(r)
}

func (wh *Handlers) getCodeReviewSystem(crs string) (clstore.ReviewSystem, bool) {
	var system clstore.ReviewSystem
	found := false
	for _, rs := range wh.ReviewSystems {
		if rs.ID == crs {
			system = rs
			found = true
		}
	}
	return system, found
}

const (
	validDigestLength = 2 * md5.Size
	dotPNG            = ".png"
)

// ImageHandler returns either a single image or a diff between two images identified by their
// respective digests.
func (wh *Handlers) ImageHandler(w http.ResponseWriter, r *http.Request) {
	// No rate limit, as this should be quite fast.
	_, imgFile := path.Split(r.URL.Path)
	// Get the file that was requested and verify that it's a valid PNG file.
	if !strings.HasSuffix(imgFile, dotPNG) {
		noCacheNotFound(w, r)
		return
	}

	// Trim the image extension to get the image or diff ID.
	imgID := imgFile[:len(imgFile)-len(dotPNG)]
	// Cache images for 12 hours.
	w.Header().Set("Cache-Control", "public, max-age=43200")
	if len(imgID) == validDigestLength {
		// Example request:
		// https://skia-infra-gold.skia.org/img/images/8588cad6f3821b948468df35b67778ef.png
		wh.serveImageWithDigest(w, r, types.Digest(imgID))
	} else if len(imgID) == validDigestLength*2+1 {
		// Example request:
		// https://skia-infra-gold.skia.org/img/diffs/81c4d3a64cf32143ff6c1fbf4cbbec2d-d20731492287002a3f046eae4bd4ce7d.png
		left := types.Digest(imgID[:validDigestLength])
		// + 1 for the dash
		right := types.Digest(imgID[validDigestLength+1:])
		wh.serveImageDiff(w, r, left, right)
	} else {
		noCacheNotFound(w, r)
		return
	}
}

// serveImageWithDigest downloads the image from GCS and returns it. If there is an error, a 404
// or 500 error is returned, as appropriate.
func (wh *Handlers) serveImageWithDigest(w http.ResponseWriter, r *http.Request, digest types.Digest) {
	ctx, span := trace.StartSpan(r.Context(), "frontend_serveImageWithDigest")
	defer span.End()
	// Go's image package has no color profile support and we convert to 8-bit NRGBA to diff,
	// but our source images may have embedded color profiles and be up to 16-bit. So we must
	// at least take care to serve the original .pngs unaltered.
	b, err := wh.GCSClient.GetImage(ctx, digest)
	if err != nil {
		sklog.Warningf("Could not get image with digest %s: %s", digest, err)
		noCacheNotFound(w, r)
		return
	}
	if _, err := w.Write(b); err != nil {
		httputils.ReportError(w, err, "Could not load image. Try again later.", http.StatusInternalServerError)
		return
	}
}

// serveImageDiff downloads the left and right images, computes the diff between them, encodes
// the diff as a PNG image and writes it to the provided ResponseWriter. If there is an error, it
// returns a 404 or 500 error as appropriate.
func (wh *Handlers) serveImageDiff(w http.ResponseWriter, r *http.Request, left types.Digest, right types.Digest) {
	ctx, span := trace.StartSpan(r.Context(), "frontend_serveImageDiff")
	defer span.End()
	// TODO(lovisolo): Diff in NRGBA64?
	// TODO(lovisolo): Make sure each pair of images is in the same color space before diffing?
	//                 (They probably are today but it'd be a good correctness check to make sure.)
	eg, eCtx := errgroup.WithContext(ctx)
	var leftImg *image.NRGBA
	var rightImg *image.NRGBA
	eg.Go(func() error {
		b, err := wh.GCSClient.GetImage(eCtx, left)
		if err != nil {
			return skerr.Wrap(err)
		}
		leftImg, err = decode(b)
		return skerr.Wrap(err)
	})
	eg.Go(func() error {
		b, err := wh.GCSClient.GetImage(eCtx, right)
		if err != nil {
			return skerr.Wrap(err)
		}
		rightImg, err = decode(b)
		return skerr.Wrap(err)
	})
	if err := eg.Wait(); err != nil {
		sklog.Warningf("Could not get diff for images %q and %q: %s", left, right, err)
		noCacheNotFound(w, r)
		return
	}
	// Compute the diff image.
	_, diffImg := diff.PixelDiff(leftImg, rightImg)

	// Write output image to the http.ResponseWriter. Content-Type is set automatically
	// based on the first 512 bytes of written data. See docs for ResponseWriter.Write()
	// for details.
	//
	// The encoding step below does not take color profiles into account. This is fine since
	// both the left and right images used to compute the diff are in the same color space,
	// and also because the resulting diff image is just a visual approximation of the
	// differences between the left and right images.
	if err := encodeImg(w, diffImg); err != nil {
		httputils.ReportError(w, err, "could not serve diff image", http.StatusInternalServerError)
		return
	}
}

// decode decodes the provided bytes as a PNG and returns them as an *image.NRGBA.
func decode(b []byte) (*image.NRGBA, error) {
	im, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	return diff.GetNRGBA(im), nil
}

// noCacheNotFound disables caching and returns a 404.
func noCacheNotFound(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	http.NotFound(w, r)
}

// ChangelistSummaryHandler returns a summary of the new and untriaged digests produced by this
// CL across all Patchsets.
func (wh *Handlers) ChangelistSummaryHandler(w http.ResponseWriter, r *http.Request) {
	ctx, span := trace.StartSpan(r.Context(), "web_ChangelistSummaryHandler")
	defer span.End()
	if err := wh.cheapLimitForGerritPlugin(r); err != nil {
		httputils.ReportError(w, err, "Try again later", http.StatusInternalServerError)
		return
	}
	clID, ok := mux.Vars(r)["id"]
	if !ok {
		http.Error(w, "Must specify 'id' of Changelist.", http.StatusBadRequest)
		return
	}
	crs, ok := mux.Vars(r)["system"]
	if !ok {
		http.Error(w, "Must specify 'system' of Changelist.", http.StatusBadRequest)
		return
	}
	system, ok := wh.getCodeReviewSystem(crs)
	if !ok {
		http.Error(w, "Invalid Code Review System", http.StatusBadRequest)
		return
	}

	qCLID := sql.Qualify(system.ID, clID)
	sum, err := wh.getCLSummary2(ctx, qCLID)
	if err != nil {
		httputils.ReportError(w, err, "Could not get summary", http.StatusInternalServerError)
		return
	}
	rv := convertChangelistSummaryResponseV1(sum)
	sendJSONResponse(w, rv)
}

// getCLSummary2 fetches, caches, and returns the summary for a given CL. If the result has already
// been cached, it will return that cached value with a flag if the value is still up to date or
// not. If the cached data is stale, it will spawn a goroutine to update the cached value.
func (wh *Handlers) getCLSummary2(ctx context.Context, qCLID string) (search2.NewAndUntriagedSummary, error) {
	ts, err := wh.Search2API.ChangelistLastUpdated(ctx, qCLID)
	if err != nil {
		return search2.NewAndUntriagedSummary{}, skerr.Wrap(err)
	}
	if ts.IsZero() { // A Zero time means we have no data for this CL.
		return search2.NewAndUntriagedSummary{}, nil
	}

	cached, ok := wh.clSummaryCache.Get(qCLID)
	if ok {
		sum, ok := cached.(search2.NewAndUntriagedSummary)
		if ok {
			if ts.Before(sum.LastUpdated) || sum.LastUpdated.Equal(ts) {
				sum.Outdated = false
				return sum, nil
			}
			// Result is stale. Start a goroutine to fetch it again.
			done := make(chan struct{})
			go func() {
				// We intentionally use context.Background() and not the request's context because
				// if we return a result, we want the fetching in the background to continue so
				// if/when the client tries again, we can serve that updated result.
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				newValue, err := wh.Search2API.NewAndUntriagedSummaryForCL(ctx, qCLID)
				if err != nil {
					sklog.Warningf("Could not fetch out of date summary for cl %s in background: %s", qCLID, err)
					return
				}
				wh.clSummaryCache.Add(qCLID, newValue)
				done <- struct{}{}
			}()
			// Wait up to 500ms to return the latest value quickly if available
			timer := time.NewTimer(500 * time.Millisecond)
			defer timer.Stop()
			select {
			case <-done:
			case <-timer.C:
			}
			cached, ok = wh.clSummaryCache.Get(qCLID)
			if ok {
				if possiblyUpdated, ok := cached.(search2.NewAndUntriagedSummary); ok {
					if ts.Before(possiblyUpdated.LastUpdated) || possiblyUpdated.LastUpdated.Equal(ts) {
						// We were able to fetch new data quickly, so return it now.
						possiblyUpdated.Outdated = false
						return possiblyUpdated, nil
					}
				}
			}
			// The cached data is still stale or invalid, so return what we have marked as outdated.
			sum.Outdated = true
			return sum, nil
		}
	}
	// Invalid or missing cache entry. We must fetch because we have nothing to give the user.
	sum, err := wh.Search2API.NewAndUntriagedSummaryForCL(ctx, qCLID)
	if err != nil {
		return search2.NewAndUntriagedSummary{}, skerr.Wrap(err)
	}
	wh.clSummaryCache.Add(qCLID, sum)
	return sum, nil
}

// convertChangelistSummaryResponseV1 converts the search2 version of a Changelist summary into
// the version expected by the frontend.
func convertChangelistSummaryResponseV1(summary search2.NewAndUntriagedSummary) frontend.ChangelistSummaryResponseV1 {
	xps := make([]frontend.PatchsetNewAndUntriagedSummaryV1, 0, len(summary.PatchsetSummaries))
	for _, ps := range summary.PatchsetSummaries {
		xps = append(xps, frontend.PatchsetNewAndUntriagedSummaryV1{
			NewImages:            ps.NewImages,
			NewUntriagedImages:   ps.NewUntriagedImages,
			TotalUntriagedImages: ps.TotalUntriagedImages,
			PatchsetID:           ps.PatchsetID,
			PatchsetOrder:        ps.PatchsetOrder,
		})
	}
	// It is convenient for the UI to have these sorted with the latest patchset first.
	sort.Slice(xps, func(i, j int) bool {
		return xps[i].PatchsetOrder > xps[j].PatchsetOrder
	})
	return frontend.ChangelistSummaryResponseV1{
		ChangelistID:      summary.ChangelistID,
		PatchsetSummaries: xps,
		Outdated:          summary.Outdated,
	}
}

// StartCacheWarming starts warming the caches for data we want to serve quickly. It starts
// goroutines that will run in the background (until the provided context is cancelled).
func (wh *Handlers) StartCacheWarming(ctx context.Context, windowSize int) {
	wh.startCLCacheProcess(ctx)
	wh.startStatusCacheProcess(ctx, windowSize)
}

// startCLCacheProcess starts a go routine to warm the CL Summary cache. This way, most
// summaries are responsive, even on big instances.
func (wh *Handlers) startCLCacheProcess(ctx context.Context) {
	// We warm every CL that was open and produced data or saw triage activity in the last 5 days.
	// After the first cycle, we will incrementally update the cache.
	lastCheck := now.Now(ctx).Add(-5 * 24 * time.Hour)
	go util.RepeatCtx(ctx, time.Minute, func(ctx context.Context) {
		ctx, span := trace.StartSpan(ctx, "web_warmCLCacheCycle", trace.WithSampler(trace.AlwaysSample()))
		defer span.End()
		newTS := now.Now(ctx)
		rows, err := wh.DB.Query(ctx, `WITH
ChangelistsWithNewData AS (
	SELECT changelist_id FROM Changelists
	WHERE status = 'open' and last_ingested_data > $1
),
ChangelistsWithTriageActivity AS (
	SELECT DISTINCT branch_name AS changelist_id FROM ExpectationRecords
	WHERE branch_name IS NOT NULL AND triage_time > $1
)
SELECT changelist_id FROM ChangelistsWithNewData
UNION
SELECT changelist_id FROM ChangelistsWithTriageActivity
`, lastCheck)
		if err != nil {
			if err == pgx.ErrNoRows {
				sklog.Infof("No CLS updated since %s", lastCheck)
				lastCheck = newTS
				return
			}
			sklog.Errorf("Could not fetch updated CLs to warm cache: %s", err)
			return
		}
		defer rows.Close()
		var qualifiedIDS []string
		for rows.Next() {
			var qID string
			if err := rows.Scan(&qID); err != nil {
				sklog.Errorf("Could not scan: %s", err)
			}
			qualifiedIDS = append(qualifiedIDS, qID)
		}
		sklog.Infof("Warming cache for %d CLs", len(qualifiedIDS))
		span.AddAttributes(trace.Int64Attribute("num_cls", int64(len(qualifiedIDS))))
		// warm cache 3 at a time. This number of goroutines was chosen arbitrarily.
		_ = util.ChunkIterParallel(ctx, len(qualifiedIDS), len(qualifiedIDS)/3+1, func(ctx context.Context, startIdx int, endIdx int) error {
			if err := ctx.Err(); err != nil {
				return nil
			}
			for _, qCLID := range qualifiedIDS[startIdx:endIdx] {
				_, err := wh.getCLSummary2(ctx, qCLID)
				if err != nil {
					sklog.Warningf("Ignoring error while warming CL Cache for %s: %s", qCLID, err)
				}
			}
			return nil
		})
		lastCheck = newTS
		sklog.Infof("Done warming cache")
	})
}

// startStatusCacheProcess
func (wh *Handlers) startStatusCacheProcess(ctx context.Context, windowSize int) {
	go util.RepeatCtx(ctx, time.Minute, func(ctx context.Context) {
		ctx, span := trace.StartSpan(ctx, "web_warmStatusCacheCycle", trace.WithSampler(trace.AlwaysSample()))
		defer span.End()

		var gs frontend.GUIStatus
		row := wh.DB.QueryRow(ctx, `SELECT git_hash, GitCommits.commit_id, commit_time, author_email, subject
FROM GitCommits JOIN CommitsWithData ON GitCommits.commit_id = CommitsWithData.commit_id
AS OF SYSTEM TIME '-0.1s'
ORDER BY CommitsWithData.commit_id DESC LIMIT 1`)
		var ts time.Time
		if err := row.Scan(&gs.LastCommit.Hash, &gs.LastCommit.ID, &ts,
			&gs.LastCommit.Author, &gs.LastCommit.Subject); err != nil {
			sklog.Errorf("Could not get most recent commit with data: %s", err)
			return
		}
		gs.LastCommit.CommitTime = ts.UTC().Unix()

		const statement = `WITH
CommitsInWindow AS (
	SELECT commit_id FROM CommitsWithData
	ORDER BY commit_id DESC LIMIT $1
),
OldestCommitInWindow AS (
	SELECT commit_id FROM CommitsInWindow
	ORDER BY commit_id ASC LIMIT 1
),
DistinctNotIgnoredDigests AS (
	SELECT DISTINCT corpus, digest, grouping_id FROM ValuesAtHead
	JOIN OldestCommitInWindow ON ValuesAtHead.most_recent_commit_id >= OldestCommitInWindow.commit_id
	WHERE matches_any_ignore_rule = FALSE
),
CorporaWithAtLeastOneTriaged AS (
    SELECT corpus, COUNT(DistinctNotIgnoredDigests.digest) AS num_untriaged FROM DistinctNotIgnoredDigests
    JOIN Expectations ON DistinctNotIgnoredDigests.grouping_id = Expectations.grouping_id AND
        DistinctNotIgnoredDigests.digest = Expectations.digest AND label = 'u'
    GROUP BY corpus
),
AllCorpora AS (
    -- Corpora with no untriaged digests will not show up in CorporaWithAtLeastOneTriaged.
    -- We still want to include them in our status, so we do a separate query and union it in.
    SELECT DISTINCT corpus, 0 AS num_untriaged FROM ValuesAtHead
    JOIN OldestCommitInWindow ON ValuesAtHead.most_recent_commit_id >= OldestCommitInWindow.commit_id
)
SELECT corpus, max(num_untriaged) FROM (
    SELECT corpus, num_untriaged FROM AllCorpora
    UNION
    SELECT corpus, num_untriaged FROM CorporaWithAtLeastOneTriaged
) GROUP BY corpus`

		rows, err := wh.DB.Query(ctx, statement, windowSize)
		if err != nil {
			sklog.Errorf("Could not update status cache: %s", err)
			return
		}
		defer rows.Close()
		for rows.Next() {
			var cs frontend.GUICorpusStatus
			if err := rows.Scan(&cs.Name, &cs.UntriagedCount); err != nil {
				sklog.Errorf("Could not scan status cache: %s", err)
				return
			}
			gs.CorpStatus = append(gs.CorpStatus, &cs)
		}

		sort.Slice(gs.CorpStatus, func(i, j int) bool {
			return gs.CorpStatus[i].Name < gs.CorpStatus[j].Name
		})

		wh.statusCacheMutex.Lock()
		defer wh.statusCacheMutex.Unlock()
		wh.statusCache = gs
	})
}

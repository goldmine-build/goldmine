// Package search contains the core functionality for searching for digests across a sliding window
// of the last N commits. N is set per instance, but is typically between 100 and 500 commits.
package search

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	ttlcache "github.com/patrickmn/go-cache"
	"go.opencensus.io/trace"
	"golang.org/x/sync/errgroup"

	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/paramtools"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/golden/go/clstore"
	"go.skia.org/infra/golden/go/code_review"
	"go.skia.org/infra/golden/go/comment"
	"go.skia.org/infra/golden/go/diff"
	"go.skia.org/infra/golden/go/expectations"
	"go.skia.org/infra/golden/go/indexer"
	"go.skia.org/infra/golden/go/publicparams"
	"go.skia.org/infra/golden/go/search/common"
	"go.skia.org/infra/golden/go/search/frontend"
	"go.skia.org/infra/golden/go/search/query"
	"go.skia.org/infra/golden/go/search/ref_differ"
	"go.skia.org/infra/golden/go/sql"
	"go.skia.org/infra/golden/go/tiling"
	"go.skia.org/infra/golden/go/tjstore"
	"go.skia.org/infra/golden/go/types"
	web_frontend "go.skia.org/infra/golden/go/web/frontend"
)

const (
	// TODO(kjlubick): no tests for this option yet.
	GROUP_TEST_MAX_COUNT = "count"

	// These params configure how long we should hold values in storeCache.
	// They are arbitrarily defined, loosely based on the idea that data flowing
	// into the store for a given CL does not change at all after ingestion is complete
	// and even during ingestion, things are likely to not change much over a time period
	// less than one minute.
	searchCacheFreshness = 1 * time.Minute
	searchCacheCleanup   = 5 * time.Minute
)

// SearchImpl holds onto various objects needed to search the latest
// tile for digests. It implements the SearchAPI interface.
type SearchImpl struct {
	diffStore         diff.DiffStore
	expectationsStore expectations.Store
	indexSource       indexer.IndexSource
	reviewSystems     []clstore.ReviewSystem
	tryJobStore       tjstore.Store
	commentStore      comment.Store

	// storeCache allows for better performance by caching values from changelistStore and
	// tryJobStore for a little while, before evicting them.
	// See skbug.com/9476
	storeCache *ttlcache.Cache

	// triageHistoryCache maps expectation.ID to frontend.TriageHistory. Entries get removed if
	// we see an event indicating expectations for that ID changed.
	triageHistoryCache *sync.Map

	// If a given trace has more unique digests than this threshold, it can be considered "flaky".
	// Flaky traces can sometimes be ignored, e.g. in UntriagedUnignoredTryJobExclusiveDigests.
	flakyTraceThreshold int

	// optional. If specified, will only show the traces that match this Matcher. Specifically, this
	// limits the Tryjob Results
	publiclyViewableParams publicparams.Matcher

	clIndexCacheHitCounter  metrics2.Counter
	clIndexCacheMissCounter metrics2.Counter
	sqlDB                   *pgxpool.Pool
}

// New returns a new SearchImpl instance.
func New(ds diff.DiffStore, es expectations.Store, cer expectations.ChangeEventRegisterer, is indexer.IndexSource, reviewSystems []clstore.ReviewSystem, tjs tjstore.Store, cs comment.Store, publiclyViewableParams publicparams.Matcher, flakyThreshold int, sqlDB *pgxpool.Pool) *SearchImpl {
	var triageHistoryCache sync.Map
	if cer != nil {
		// If the expectations change for a given ID, we should purge it from our cache so as not
		// to serve stale data.
		cer.ListenForChange(func(id expectations.ID) {
			triageHistoryCache.Delete(id)
		})
	}

	return &SearchImpl{
		diffStore:              ds,
		expectationsStore:      es,
		indexSource:            is,
		reviewSystems:          reviewSystems,
		tryJobStore:            tjs,
		commentStore:           cs,
		publiclyViewableParams: publiclyViewableParams,
		flakyTraceThreshold:    flakyThreshold,
		sqlDB:                  sqlDB,

		storeCache:         ttlcache.New(searchCacheFreshness, searchCacheCleanup),
		triageHistoryCache: &triageHistoryCache,

		clIndexCacheHitCounter:  metrics2.GetCounter("gold_search_cl_index_cache_hit"),
		clIndexCacheMissCounter: metrics2.GetCounter("gold_search_cl_index_cache_miss"),
	}
}

// Search implements the SearchAPI interface.
func (s *SearchImpl) Search(ctx context.Context, q *query.Search) (*frontend.SearchResponse, error) {
	ctx, span := trace.StartSpan(ctx, "search.Search")
	defer span.End()
	if q == nil {
		return nil, skerr.Fmt("nil query")
	}

	// Keep track if we are including reference diffs. This is going to be true
	// for the majority of queries. TODO(kjlubick) Who uses this? Do we have tests? Can it go?
	getRefDiffs := !q.NoDiff
	// TODO(kjlubick) remove the legacy check against "0" once the frontend is updated
	//   not to pass it.
	isChangelistSearch := q.ChangelistID != "" && q.ChangelistID != "0"
	// Get the expectations and the current index, which we assume constant
	// for the duration of this query.
	if isChangelistSearch && q.CodeReviewSystemID == "" {
		// TODO(kjlubick) remove this default after the search page is converted to lit-html.
		q.CodeReviewSystemID = s.reviewSystems[0].ID
	}
	exp, err := s.getExpectations(ctx, q.ChangelistID, q.CodeReviewSystemID)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	idx := s.indexSource.GetIndex()

	commits := web_frontend.FromTilingCommits(idx.Tile().DataCommits())
	var results []*frontend.SearchResult
	// Find the digests (left hand side) we are interested in.
	if isChangelistSearch {
		reviewSystem, err := s.reviewSystem(q.CodeReviewSystemID)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		cl, err := reviewSystem.Store.GetChangelist(ctx, q.ChangelistID)
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		// Add this CL information as a faux Commit, so we can properly show the blamelists for
		// the trace data, which will include this CL's output appended to the end (as if it was the
		// most recent commit to land on master).
		commits = append(commits, web_frontend.Commit{
			CommitTime:    cl.Updated.Unix(),
			Hash:          cl.SystemID,
			Author:        cl.Owner,
			Subject:       cl.Subject,
			ChangelistURL: strings.Replace(reviewSystem.URLTemplate, "%s", cl.SystemID, 1),
		})
		results, err = s.queryChangelist(ctx, q, idx, exp)
		if err != nil {
			return nil, skerr.Wrapf(err, "getting digests from clstore/tjstore")
		}
	} else {
		// Iterate through the tile and find the digests that match the queries.
		results, err = s.filterTile(ctx, q, idx, exp)
		if err != nil {
			return nil, skerr.Wrapf(err, "getting digests from master tile")
		}
	}
	// At this first point, results will only be partially filled out. The next steps will fill
	// in the remaining pieces.

	// Add the expectation values to the results.
	addExpectations(results, exp)

	// Get reference diffs unless it was specifically disabled.
	if getRefDiffs {
		// Diff stage: Compare all digests found in the previous stages and find
		// reference points (positive, negative etc.) for each digest.
		if err := s.getReferenceDiffs(ctx, results, q.Metric, q.Match, q.RightTraceValues, q.IgnoreState(), exp, idx); err != nil {
			return nil, skerr.Wrapf(err, "fetching reference diffs for %#v", q)
		}

		// Post-diff stage: Apply all filters that are relevant once we have
		// diff values for the digests.
		results = s.afterDiffResultFilter(ctx, results, q)
	}

	bulkTriageData := collectDigestsForBulkTriage(results)

	// Sort the digests and fill the ones that are going to be displayed with
	// additional data.
	displayRet, offset := s.sortAndLimitDigests(ctx, q, results, int(q.Offset), int(q.Limit))
	s.addTriageHistory(ctx, s.makeTriageHistoryGetter(q.CodeReviewSystemID, q.ChangelistID), displayRet)
	traceComments := s.getTraceComments(ctx)
	prepareTraceGroups(displayRet, exp, traceComments, isChangelistSearch)

	// Return all digests with the selected offset within the result set.
	searchRet := &frontend.SearchResponse{
		Results:        displayRet,
		Offset:         offset,
		Size:           len(results),
		Commits:        commits,
		BulkTriageData: bulkTriageData,
		TraceComments:  traceComments,
	}
	return searchRet, nil
}

func collectDigestsForBulkTriage(results []*frontend.SearchResult) web_frontend.TriageRequestData {
	testNameToPrimaryDigest := web_frontend.TriageRequestData{}
	for _, r := range results {
		test := r.Test
		digestToLabel, ok := testNameToPrimaryDigest[test]
		if !ok {
			digestToLabel = map[types.Digest]expectations.Label{}
			testNameToPrimaryDigest[test] = digestToLabel
		}
		primary := r.Digest
		switch r.ClosestRef {
		case common.PositiveRef:
			digestToLabel[primary] = expectations.Positive
		case common.NegativeRef:
			digestToLabel[primary] = expectations.Negative
		case common.NoRef:
			digestToLabel[primary] = ""
		}
	}
	return testNameToPrimaryDigest
}

// GetDigestDetails implements the SearchAPI interface.
func (s *SearchImpl) GetDigestDetails(ctx context.Context, test types.TestName, digest types.Digest, clID, crs string) (*frontend.DigestDetails, error) {
	ctx, span := trace.StartSpan(ctx, "search.GetDigestDetails")
	defer span.End()
	idx := s.indexSource.GetIndex()

	// Make sure we have valid data, i.e. we know about that test/digest
	dct := idx.DigestCountsByTest(types.IncludeIgnoredTraces)
	digests, ok := dct[test]
	if !ok {
		if clID != "" {
			clIdx := s.indexSource.GetIndexForCL(crs, clID)
			if clIdx == nil || !util.In(string(test), clIdx.ParamSet[types.PrimaryKeyField]) {
				return nil, skerr.Fmt("unknown test %s for cl %s", test, clID)
			}
			return s.getCLOnlyDigestDetails(ctx, test, digest, clID, crs)
		}
		return nil, skerr.Fmt("unknown test %s", test)
	}

	tile := idx.Tile().GetTile(types.IncludeIgnoredTraces)

	exp, err := s.getExpectations(ctx, clID, crs)
	if err != nil {
		return nil, skerr.Wrap(err)
	}

	result := frontend.SearchResult{
		Test:     test,
		Digest:   digest,
		ParamSet: paramtools.ParamSet{},
	}

	if _, ok := digests[digest]; ok {
		// We know a digest is somewhere in at least one trace. Iterate through all of them
		// to find which ones.
		byTrace := idx.DigestCountsByTrace(types.IncludeIgnoredTraces)
		for traceID, trace := range tile.Traces {
			if trace.TestName() != test {
				continue
			}
			if _, ok := byTrace[traceID][digest]; ok {
				ko := trace.KeysAndOptions()
				result.ParamSet.AddParams(ko)
				result.TraceGroup.Traces = append(result.TraceGroup.Traces, frontend.Trace{
					ID:       traceID,
					RawTrace: trace,
					Params:   ko,
				})
			}
		}
	}
	// Normalizing the ParamSet makes the return values deterministic.
	result.ParamSet.Normalize()

	// We wrap the result in a slice so we can re-use the search functions.
	results := []*frontend.SearchResult{&result}
	addExpectations(results, exp)
	err = s.getReferenceDiffs(ctx, results, query.CombinedMetric, []string{types.PrimaryKeyField}, nil, types.IncludeIgnoredTraces, exp, idx)
	if err != nil {
		return nil, skerr.Wrapf(err, "Fetching reference diffs for test %s, digest %s", test, digest)
	}

	var traceComments []frontend.TraceComment
	if len(result.TraceGroup.Traces) > 0 {
		// Get the params and traces.
		traceComments = s.getTraceComments(ctx)
		prepareTraceGroups(results, exp, traceComments, false)
	}
	s.addTriageHistory(ctx, s.makeTriageHistoryGetter(crs, clID), results)

	return &frontend.DigestDetails{
		Result:        result,
		Commits:       web_frontend.FromTilingCommits(tile.Commits),
		TraceComments: traceComments,
	}, nil
}

// getExpectations returns a slice of expectations that should be
// used in the given query. It will add the issue expectations if this is
// querying Changelist results. If query is nil the expectations of the master
// tile are returned.
func (s *SearchImpl) getExpectations(ctx context.Context, clID, crs string) (expectations.Classifier, error) {
	ctx, span := trace.StartSpan(ctx, "search.getExpectations")
	defer span.End()
	exp, err := s.expectationsStore.Get(ctx)
	if err != nil {
		return nil, skerr.Wrapf(err, "loading expectations for master")
	}
	// TODO(kjlubick) remove the legacy value "0" once frontend changes have baked in.
	if clID != "" && clID != "0" {
		issueExpStore := s.expectationsStore.ForChangelist(clID, crs)
		tjExp, err := issueExpStore.Get(ctx)
		if err != nil {
			return nil, skerr.Wrapf(err, "loading expectations for cl %s (%s)", clID, crs)
		}
		return expectations.Join(tjExp, exp), nil
	}

	return exp, nil
}

// getCLOnlyDigestDetails returns details for a digest when it is newly added to a CL (and does
// not exist on the master branch). This is handled as its own special case because the existing
// master branch index, which normally aids in filling out these details (e.g. has a map from
// digest to traces) does not help us here and we must re-scan the list of tryjob results
// ourselves.
func (s *SearchImpl) getCLOnlyDigestDetails(ctx context.Context, test types.TestName, digest types.Digest, clID, crs string) (*frontend.DigestDetails, error) {
	exp, err := s.getExpectations(ctx, clID, crs)
	if err != nil {
		return nil, skerr.Wrap(err)
	}

	// We know xps is sorted by order, if it is non-nil.
	xps, err := s.getPatchsets(ctx, crs, clID)
	if err != nil {
		return nil, skerr.Wrapf(err, "getting Patchsets for CL %s", clID)
	}
	if len(xps) == 0 {
		return nil, skerr.Fmt("No data for CL %s", clID)
	}

	latestPatchset := xps[len(xps)-1]
	id := tjstore.CombinedPSID{
		CL:  latestPatchset.ChangelistID,
		CRS: crs,
		PS:  latestPatchset.SystemID,
	}
	xtr, err := s.getTryJobResults(ctx, id)
	if err != nil {
		return nil, skerr.Wrapf(err, "getting tryjob results for %v", id)
	}
	paramSet := paramtools.ParamSet{}
	for _, tr := range xtr { // this could be done in parallel, if needed for performance reasons.
		if tr.Digest != digest {
			continue
		}
		if tr.ResultParams[types.PrimaryKeyField] != string(test) {
			continue
		}
		p := paramtools.Params{}
		p.Add(tr.GroupParams, tr.Options, tr.ResultParams)
		// If we've been given a set of PubliclyViewableParams, only show those.
		if s.publiclyViewableParams != nil {
			if !s.publiclyViewableParams.Matches(p) {
				continue
			}
		}
		paramSet.AddParams(p)
	}

	return &frontend.DigestDetails{
		TraceComments: nil, // TODO(skbug.com/6630)
		Result: frontend.SearchResult{
			Test:          test,
			Digest:        digest,
			Status:        exp.Classification(test, digest),
			TriageHistory: nil, // TODO(skbug.com/10097)
			ParamSet:      paramSet,
			// The trace-related fields can be omitted because there are no traces on master branch of
			// which to show the history
		},
	}, nil
}

// queryChangelist returns the digests associated with the Changelist referenced by q.CRSAndCLID
// in intermediate representation. It returns the filtered digests as specified by q. The param
// exp should contain the expectations for the given Changelist.
func (s *SearchImpl) queryChangelist(ctx context.Context, q *query.Search, idx indexer.IndexSearcher, exp expectations.Classifier) ([]*frontend.SearchResult, error) {
	ctx, span := trace.StartSpan(ctx, "search.queryChangelist")
	defer span.End()
	// Build the intermediate map to group results belonging to the same test and digest.
	resultsByGroupingAndDigest := map[groupingAndDigest]*frontend.SearchResult{}
	talliesByTest := idx.DigestCountsByTest(q.IgnoreState())

	addByGroupAndDigest := func(test types.TestName, digest types.Digest, params paramtools.Params, tp tiling.TracePair) {
		if !q.IncludeDigestsProducedOnMaster {
			if _, ok := talliesByTest[test][digest]; ok {
				return // skip this entry because it was already seen on master branch.
			}
		}

		key := groupingAndDigest{grouping: test, digest: digest}
		existing := resultsByGroupingAndDigest[key]
		if existing == nil {
			existing = &frontend.SearchResult{
				Test:     test,
				Digest:   digest,
				ParamSet: paramtools.ParamSet{},
			}
			resultsByGroupingAndDigest[key] = existing
		}
		existing.ParamSet.AddParams(params)
		// The trace might not exist on the master branch, but if it does, we can show it.
		if tp.Trace != nil {
			existing.TraceGroup.Traces = append(existing.TraceGroup.Traces, frontend.Trace{
				ID:       tp.ID,
				RawTrace: tp.Trace,
				Params:   tp.Trace.KeysAndOptions(),
			})
		}
	}

	err := s.extractChangelistDigests(ctx, q, idx, exp, addByGroupAndDigest)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	ret := make([]*frontend.SearchResult, 0, len(resultsByGroupingAndDigest))
	for _, srd := range resultsByGroupingAndDigest {
		// Normalizing the ParamSet makes the return values deterministic.
		srd.ParamSet.Normalize()
		ret = append(ret, srd)
	}
	return ret, nil
}

// filterAddFn is a filter and add function that is passed to the getIssueDigest interface. It will
// be called for each testName/digest combination and should accumulate the digests of interest.
type filterAddFn func(test types.TestName, digest types.Digest, params paramtools.Params, tp tiling.TracePair)

// extractFilterShards dictates how to break up the filtering of extractChangelistDigests after
// they have been fetched from the TryJobStore. It was determined experimentally on
// BenchmarkExtractChangelistDigests. It sped up things by about a factor of 6 and was a good
// balance of dividing up and mutex contention.
const extractFilterShards = 16

// extractChangelistDigests loads the Changelist referenced by q.CRSAndCLID and the TryJobResults
// associated with it. Then, it filters those results with the given query. For each
// testName/digest pair that matches the query, it calls addFn (which the supplier will likely use
// to build up a list of those results.
func (s *SearchImpl) extractChangelistDigests(ctx context.Context, q *query.Search, idx indexer.IndexSearcher, exp expectations.Classifier, addFn filterAddFn) error {
	defer metrics2.FuncTimer().Stop()

	clID := q.ChangelistID
	// We know xps is sorted by order, if it is non-nil.
	xps, err := s.getPatchsets(ctx, q.CodeReviewSystemID, clID)
	if err != nil {
		return skerr.Wrapf(err, "getting Patchsets for CL %s", clID)
	}

	if len(xps) == 0 {
		return skerr.Fmt("No data for CL %s", clID)
	}

	// Default to the latest Patchset
	ps := xps[len(xps)-1]
	if len(q.Patchsets) > 0 {
		// legacy code used to request multiple patchsets at once - we don't do that
		// so we just look at the first one mentioned by the query.
		psOrder := int(q.Patchsets[0])
		found := false
		for _, p := range xps {
			if p.Order == psOrder {
				ps = p
				found = true
				break
			}
		}
		if !found {
			return skerr.Fmt("Could not find PS with order %d in CL %s", psOrder, clID)
		}
	}

	id := tjstore.CombinedPSID{
		CL:  ps.ChangelistID,
		CRS: q.CodeReviewSystemID,
		PS:  ps.SystemID,
	}

	var xtr []tjstore.TryJobResult
	wasCached := false
	if q.IncludeUntriagedDigests && !q.IncludePositiveDigests && !q.IncludeNegativeDigests {
		// If the search is just for untriaged digests, we can use the CL index for this.
		clIdx := s.indexSource.GetIndexForCL(id.CRS, id.CL)
		if clIdx != nil && clIdx.LatestPatchset.Equal(id) {
			s.clIndexCacheHitCounter.Inc(1)
			xtr = clIdx.UntriagedResults
			wasCached = true
		}
	}
	if !wasCached {
		s.clIndexCacheMissCounter.Inc(1)
		xtr, err = s.getTryJobResults(ctx, id)
		if err != nil {
			return skerr.Wrapf(err, "getting tryjob results for %v", id)
		}
	} else {
		sklog.Debugf("Cache hit for untriaged tryjob results")
	}

	addMutex := sync.Mutex{}
	chunkSize := len(xtr) / extractFilterShards
	// Very small shards are likely not worth the overhead.
	if chunkSize < 50 {
		chunkSize = 50
	}
	queryParams := q.TraceValues
	ignoreMatcher := idx.GetIgnoreMatcher()
	tracesByID := idx.Tile().GetTile(q.IgnoreState()).Traces

	return util.ChunkIterParallel(ctx, len(xtr), chunkSize, func(ctx context.Context, start, stop int) error {
		sliced := xtr[start:stop]
		for _, tr := range sliced {
			if err := ctx.Err(); err != nil {
				return skerr.Wrap(err)
			}
			tn := types.TestName(tr.ResultParams[types.PrimaryKeyField])
			// Filter by classification.
			c := exp.Classification(tn, tr.Digest)
			if q.ExcludesClassification(c) {
				continue
			}
			p := make(paramtools.Params, len(tr.ResultParams)+len(tr.GroupParams)+len(tr.Options))
			p.Add(tr.GroupParams)
			p.Add(tr.ResultParams)
			// Compute the traceID here because by definition, the traceID does not include optional keys.
			traceID := tiling.TraceIDFromParams(p)
			p.Add(tr.Options)
			// Filter the ignored results
			if !q.IncludeIgnoredTraces {
				// Because ignores can happen on a mix of params from Result, Group, and Options,
				// we have to invoke the matcher the whole set of params.
				if ignoreMatcher.MatchAnyParams(p) {
					continue
				}
			}
			// If we've been given a set of PubliclyViewableParams, only show those.
			if s.publiclyViewableParams != nil {
				if !s.publiclyViewableParams.Matches(p) {
					continue
				}
			}
			// Filter by query.
			if queryParams.MatchesParams(p) {
				tp := tiling.TracePair{
					ID:    traceID,
					Trace: tracesByID[traceID],
				}
				func() {
					addMutex.Lock()
					addFn(tn, tr.Digest, p, tp)
					addMutex.Unlock()
				}()
			}
		}
		return nil
	})
}

// getPatchsets returns the Patchsets for a given CL either from the store or from the cache.
func (s *SearchImpl) getPatchsets(ctx context.Context, crs, id string) ([]code_review.Patchset, error) {
	key := crs + "_patchsets_" + id
	if xtr, ok := s.storeCache.Get(key); ok {
		return xtr.([]code_review.Patchset), nil
	}
	rs, err := s.reviewSystem(crs)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	xps, err := rs.Store.GetPatchsets(ctx, id)
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	s.storeCache.SetDefault(key, xps)
	return xps, nil
}

// getTryJobResults returns the TryJobResults for a given CL either from the store or
// from the cache.
func (s *SearchImpl) getTryJobResults(ctx context.Context, id tjstore.CombinedPSID) ([]tjstore.TryJobResult, error) {
	key := "tjresults_" + id.Key()
	if xtr, ok := s.storeCache.Get(key); ok {
		return xtr.([]tjstore.TryJobResult), nil
	}
	xtr, err := s.tryJobStore.GetResults(ctx, id, time.Time{})
	if err != nil {
		return nil, skerr.Wrap(err)
	}
	s.storeCache.SetDefault(key, xtr)
	return xtr, nil
}

// DiffDigests implements the SearchAPI interface.
func (s *SearchImpl) DiffDigests(ctx context.Context, test types.TestName, left, right types.Digest, clID string, crs string) (*frontend.DigestComparison, error) {
	ctx, span := trace.StartSpan(ctx, "search.DiffDigests")
	defer span.End()

	// Get the diff between the two digests
	diffMetric, err := s.getDiffMetric(ctx, left, right)
	if err != nil {
		return nil, skerr.Wrap(err)
	}

	exp, err := s.getExpectations(ctx, clID, crs)
	if err != nil {
		return nil, skerr.Wrap(err)
	}

	idx := s.indexSource.GetIndex()

	psLeft := idx.GetParamsetSummary(test, left, types.IncludeIgnoredTraces)
	// Normalizing the ParamSet makes the return values deterministic.
	psLeft.Normalize()
	psRight := idx.GetParamsetSummary(test, right, types.IncludeIgnoredTraces)
	psRight.Normalize()

	history := s.makeTriageHistoryGetter(crs, clID)
	return &frontend.DigestComparison{
		Left: frontend.SearchResult{
			Test:          test,
			Digest:        left,
			Status:        exp.Classification(test, left),
			TriageHistory: s.getTriageHistory(ctx, history, test, left),
			ParamSet:      psLeft,
		},
		Right: &frontend.SRDiffDigest{
			Digest:           right,
			Status:           exp.Classification(test, right),
			ParamSet:         psRight,
			NumDiffPixels:    diffMetric.NumDiffPixels,
			CombinedMetric:   diffMetric.CombinedMetric,
			PixelDiffPercent: diffMetric.PixelDiffPercent,
			MaxRGBADiffs:     diffMetric.MaxRGBADiffs,
			DimDiffer:        diffMetric.DimDiffer,
		},
	}, nil
}

// getDiffMetric returns the associated diff metric with the given two digests or an error if
// it cannot be found. It will use either the Firestore backend or the SQL backend, depending on
// if UseSQLDiffMetricsKey has a value in the provided context.
func (s *SearchImpl) getDiffMetric(ctx context.Context, left, right types.Digest) (diff.DiffMetrics, error) {
	if useSQLDiffMetrics(ctx) {
		ctx, span := trace.StartSpan(ctx, "search.getSQLDiffMetric")
		defer span.End()
		const statement = `
SELECT num_pixels_diff, percent_pixels_diff, max_rgba_diffs, combined_metric, dimensions_differ
FROM DiffMetrics WHERE left_digest = $1 AND right_digest = $2`
		lBytes, err := sql.DigestToBytes(left)
		if err != nil {
			return diff.DiffMetrics{}, skerr.Wrapf(err, "invalid digest %s", left)
		}
		rBytes, err := sql.DigestToBytes(right)
		if err != nil {
			return diff.DiffMetrics{}, skerr.Wrapf(err, "invalid digest %s", right)
		}
		var m diff.DiffMetrics
		row := s.sqlDB.QueryRow(ctx, statement, lBytes, rBytes)
		err = row.Scan(&m.NumDiffPixels, &m.PixelDiffPercent, &m.MaxRGBADiffs,
			&m.CombinedMetric, &m.DimDiffer)
		if err == pgx.ErrNoRows {
			return diff.DiffMetrics{}, skerr.Fmt("diff not found for %s-%s", left, right)
		} else if err != nil {
			return diff.DiffMetrics{}, skerr.Wrap(err)
		}
		return m, nil
	}
	ctx, span := trace.StartSpan(ctx, "search.getFirestoreDiffMetric")
	defer span.End()
	res, err := s.diffStore.Get(ctx, left, types.DigestSlice{right})
	if err != nil {
		return diff.DiffMetrics{}, skerr.Wrap(err)
	}
	if len(res) != 1 {
		return diff.DiffMetrics{}, skerr.Fmt("diff not found for %s-%s", left, right)
	}
	return *res[right], nil
}

// useSQLDiffMetrics returns true if there is a non-nil value associated with UseSQLDiffMetricsKey.
func useSQLDiffMetrics(ctx context.Context) bool {
	return ctx.Value(UseSQLDiffMetricsKey) != nil
}

// filterTile iterates over the tile and accumulates the traces
// that match the given query creating the initial search result.
func (s *SearchImpl) filterTile(ctx context.Context, q *query.Search, idx indexer.IndexSearcher, exp expectations.Classifier) ([]*frontend.SearchResult, error) {
	ctx, span := trace.StartSpan(ctx, "search.filterTile")
	defer span.End()
	var acceptFn iterTileAcceptFn
	if q.GroupTestFilter == GROUP_TEST_MAX_COUNT {
		maxDigestsByTest := idx.MaxDigestsByTest(q.IgnoreState())
		acceptFn = func(params paramtools.Params, digests types.DigestSlice) bool {
			testName := types.TestName(params[types.PrimaryKeyField])
			for _, d := range digests {
				if maxDigestsByTest[testName][d] {
					return true
				}
			}
			return false
		}
	}

	// We'll want to find all traces that generate a given digest for a given grouping.
	resultsByGroupingAndDigest := map[groupingAndDigest]*frontend.SearchResult{}
	mutex := sync.Mutex{}
	// For each trace that does, we'll add the params the trace has to the paramset of associated
	// with the digest and include the trace in slice of traces.
	addFn := func(test types.TestName, digest types.Digest, traceID tiling.TraceID, trace *tiling.Trace) {
		mutex.Lock()
		defer mutex.Unlock()
		key := groupingAndDigest{grouping: test, digest: digest}
		existing := resultsByGroupingAndDigest[key]
		if existing == nil {
			existing = &frontend.SearchResult{
				Test:     test,
				Digest:   digest,
				ParamSet: paramtools.ParamSet{},
			}
			resultsByGroupingAndDigest[key] = existing
		}
		ko := trace.KeysAndOptions()
		existing.ParamSet.AddParams(ko)
		// It is tempting to think we could just convert the RawTrace into the frontend.Trace right
		// here, but in fact we need all the traces for a given digest (i.e. in a given TraceGroup)
		// to be able to do that. Specifically, we want to be able to share the digest indices.
		existing.TraceGroup.Traces = append(existing.TraceGroup.Traces, frontend.Trace{
			ID:       traceID,
			RawTrace: trace,
			Params:   ko,
		})
	}

	if err := iterTile(ctx, q, addFn, acceptFn, exp, idx); err != nil {
		return nil, skerr.Wrap(err)
	}

	results := make([]*frontend.SearchResult, 0, len(resultsByGroupingAndDigest))
	for _, srd := range resultsByGroupingAndDigest {
		// Normalizing the ParamSet makes the return values deterministic.
		srd.ParamSet.Normalize()
		results = append(results, srd)
	}

	return results, nil
}

type groupingAndDigest struct {
	grouping types.TestName
	digest   types.Digest
}

// addExpectations adds the expectations to the current set of results using the provided
// Classifier. TODO(kjlubick) this can be moved into filterTile/etc
func addExpectations(results []*frontend.SearchResult, exp expectations.Classifier) {
	for _, r := range results {
		r.Status = exp.Classification(r.Test, r.Digest)
	}
}

// getReferenceDiffs compares all digests collected in the intermediate representation
// and compares them to the other known results for the test at hand.
func (s *SearchImpl) getReferenceDiffs(ctx context.Context, resultDigests []*frontend.SearchResult, metric string, match []string, rhsQuery paramtools.ParamSet, is types.IgnoreState, exp expectations.Classifier, idx indexer.IndexSearcher) error {
	ctx, span := trace.StartSpan(ctx, "search.getReferenceDiffs")
	defer span.End()
	var refDiffer ref_differ.RefDiffer
	if useSQLDiffMetrics(ctx) {
		refDiffer = ref_differ.NewSQLImpl(s.sqlDB, exp, idx)
	} else {
		refDiffer = ref_differ.NewFirestoreImpl(exp, s.diffStore, idx)
	}

	errGroup, gCtx := errgroup.WithContext(ctx)
	sklog.Infof("Going to spawn %d goroutines to get reference diffs", len(resultDigests))
	for _, retDigest := range resultDigests {
		func(d *frontend.SearchResult) {
			errGroup.Go(func() error {
				defer metrics2.NewTimer("gold_find_closest_digests").Stop()
				err := refDiffer.FillRefDiffs(gCtx, d, metric, match, rhsQuery, is)
				if err != nil {
					sklog.Warningf("Error while computing ref diffs: %s", err)
					return nil
				}

				// TODO(kjlubick): if we decide we want the TriageHistory on the right hand side
				//   digests, we could add it here.
				return nil
			})
		}(retDigest)
	}
	return skerr.Wrap(errGroup.Wait())
}

// afterDiffResultFilter filters the results based on the diff results in 'digestInfo'.
func (s *SearchImpl) afterDiffResultFilter(ctx context.Context, digestInfo []*frontend.SearchResult, q *query.Search) []*frontend.SearchResult {
	ctx, span := trace.StartSpan(ctx, "search.afterDiffResultFilter")
	defer span.End()
	newDigestInfo := make([]*frontend.SearchResult, 0, len(digestInfo))
	filterRGBADiff := (q.RGBAMinFilter > 0) || (q.RGBAMaxFilter < 255)
	filterDiffMax := q.DiffMaxFilter >= 0
	for _, digest := range digestInfo {
		ref, ok := digest.RefDiffs[digest.ClosestRef]

		// Filter all digests where MaxRGBA is within the given band.
		if filterRGBADiff {
			// If there is no diff metric we exclude the digest.
			if !ok {
				continue
			}

			rgbaMaxDiff := int32(util.MaxInt(ref.MaxRGBADiffs[:]...))
			if (rgbaMaxDiff < q.RGBAMinFilter) || (rgbaMaxDiff > q.RGBAMaxFilter) {
				continue
			}
		}

		// Filter all digests where the diff is below the given threshold.
		if filterDiffMax && (!ok || (ref.QueryMetric > q.DiffMaxFilter)) {
			continue
		}

		// If selected only consider digests that have a reference to compare to.
		if q.MustIncludeReferenceFilter && !ok {
			continue
		}

		newDigestInfo = append(newDigestInfo, digest)
	}
	return newDigestInfo
}

// sortAndLimitDigests sorts the digests based on the settings in the Query
// instance. It then paginates the digests according to the query and returns
// the slice that should be shown on the page with its offset in the entire
// result set.
func (s *SearchImpl) sortAndLimitDigests(ctx context.Context, q *query.Search, digestInfo []*frontend.SearchResult, offset, limit int) ([]*frontend.SearchResult, int) {
	ctx, span := trace.StartSpan(ctx, "search.sortAndLimitDigests")
	defer span.End()
	fullLength := len(digestInfo)
	if offset >= fullLength {
		return []*frontend.SearchResult{}, 0
	}

	sortSlice := sort.Interface(newSRDigestSlice(digestInfo))
	if q.Sort == query.SortDescending {
		sortSlice = sort.Reverse(sortSlice)
	}
	sort.Sort(sortSlice)

	// Fill in the extra information for the traces we are interested in.
	if limit <= 0 {
		limit = fullLength
	}
	end := util.MinInt(fullLength, offset+limit)
	return digestInfo[offset:end], offset
}

// getTraceComments returns the complete list of TraceComments, ready for display on the frontend.
func (s *SearchImpl) getTraceComments(ctx context.Context) []frontend.TraceComment {
	ctx, span := trace.StartSpan(ctx, "search.getTraceComments")
	defer span.End()
	var traceComments []frontend.TraceComment
	// TODO(kjlubick) remove this check once the commentStore is implemented and included from main.
	if s.commentStore != nil {
		xtc, err := s.commentStore.ListComments(ctx)
		if err != nil {
			sklog.Warningf("Omitting comments due to error: %s", err)
			traceComments = nil
		} else {
			for _, tc := range xtc {
				traceComments = append(traceComments, frontend.ToTraceComment(tc))
			}
			sort.Slice(traceComments, func(i, j int) bool {
				return traceComments[i].UpdatedTS.Before(traceComments[j].UpdatedTS)
			})
		}
	}
	return traceComments
}

// prepareTraceGroups processes the TraceGroup for each SearchResult, preparing it to be displayed
// on the frontend. If appendPrimaryDigest is true, the primary digest for each SearchResult will
// be appended to the end of the trace data, useful for visualizing changelist results.
func prepareTraceGroups(searchResults []*frontend.SearchResult, exp expectations.Classifier, comments []frontend.TraceComment, appendPrimaryDigest bool) {
	for _, di := range searchResults {
		// Add the drawable traces to the result.
		fillInFrontEndTraceData(&di.TraceGroup, di.Test, di.Digest, exp, comments, appendPrimaryDigest)
	}
}

const (
	// maxDistinctDigestsToPresent is the maximum number of digests we want to show
	// in a dotted line of traces. We assume that showing more digests yields
	// no additional information, because the trace is likely to be flaky.
	maxDistinctDigestsToPresent = 9

	// 0 is always the primary digest, no matter where (or if) it appears in the trace.
	primaryDigestIndex = 0

	// The frontend knows to handle -1 specially and show no dot.
	missingDigestIndex = -1
)

// fillInFrontEndTraceData fills in the data needed to draw the traces for the given test/digest
// and to connect the traces to the appropriate comments.
func fillInFrontEndTraceData(traceGroup *frontend.TraceGroup, test types.TestName, primary types.Digest, exp expectations.Classifier, comments []frontend.TraceComment, appendPrimaryDigest bool) {
	if len(traceGroup.Traces) == 0 {
		return
	}

	// Put the traces in a deterministic order
	sort.Slice(traceGroup.Traces, func(i, j int) bool {
		return traceGroup.Traces[i].ID < traceGroup.Traces[j].ID
	})

	// Compute the digestIndices for the traceGroup. These indices map to an assigned color on the
	// frontend.
	digestIndices, totalDigests := computeDigestIndices(traceGroup, primary)

	// Fill out the statuses, now that we know the order our labeled digests will be in.
	traceGroup.Digests = make([]frontend.DigestStatus, len(digestIndices))
	for digest, idx := range digestIndices {
		traceGroup.Digests[idx] = frontend.DigestStatus{
			Digest: digest,
			Status: exp.Classification(test, digest),
		}
	}

	// For each trace, fill out DigestIndices based on digestIndices. Then we fill out other
	// information specific to this trace.
	for idx, oneTrace := range traceGroup.Traces {
		traceLen := len(oneTrace.RawTrace.Digests)
		if appendPrimaryDigest {
			traceLen++
		}
		// Create a new trace entry.
		oneTrace.DigestIndices = make([]int, traceLen)

		if appendPrimaryDigest {
			// As requested, append the primaryDigest to the end
			oneTrace.DigestIndices[traceLen-1] = primaryDigestIndex
		}

		for i, digest := range oneTrace.RawTrace.Digests {
			if digest == tiling.MissingDigest {
				oneTrace.DigestIndices[i] = missingDigestIndex
				continue
			}
			// See if we have a digest index assigned for this digest.
			digestIndex, ok := digestIndices[digest]
			if ok {
				oneTrace.DigestIndices[i] = digestIndex
			} else {
				// Fold everything else into the last digest index (grey on the frontend).
				oneTrace.DigestIndices[i] = maxDistinctDigestsToPresent - 1
			}
		}

		for i, c := range comments {
			if c.QueryToMatch.MatchesParams(oneTrace.Params) {
				oneTrace.CommentIndices = append(oneTrace.CommentIndices, i)
			}
		}
		// No longer need the RawTrace data, now that it has been turned into the frontend version.
		oneTrace.RawTrace = nil
		traceGroup.Traces[idx] = oneTrace
		traceGroup.TileSize = traceLen // TileSize will go away soon.
	}
	traceGroup.TotalDigests = totalDigests
}

type digestCountAndLastSeen struct {
	digest types.Digest
	// count is how many times a digest has been seen in a TraceGroup.
	count int
	// lastSeenIndex refers to the commit index that this digest was most recently seen. That is,
	// a higher number means it was seen more recently. This digest might have seen much much earlier
	// than this index, but only the latest occurrence affects this value.
	lastSeenIndex int
}

const mostRecentNDigests = 3

// computeDigestIndices assigns distinct digests an index ( up to maxDistinctDigestsToPresent).
// This index
// maps to a color of dot on the frontend when representing traces. The indices are assigned to
// some of the most recent digests and some of the most common digests. All digests not in this
// map will be grouped under the highest index (represented by a grey color on the frontend).
// This hybrid approach was adapted in an effort to minimize the "interesting" digests that are
// globbed together under the grey color, which is harder to inspect from the frontend.
// See skbug.com/10387 for more context.
func computeDigestIndices(traceGroup *frontend.TraceGroup, primary types.Digest) (map[types.Digest]int, int) {
	// digestStats is a slice that has one entry per unique digest. This could be a map, but
	// we are going to sort it later, so it's cleaner to just use a slice initially especially
	// when the vast vast majority (99.9% of Skia's data) of our traces have fewer than 30 unique
	// digests. The worst case would be a few hundred unique digests, for which Ω(n) lookup isn't
	// terrible.
	digestStats := make([]digestCountAndLastSeen, 0, 5)
	// Populate digestStats, iterating over the digests from all traces from oldest to newest.
	// By construction, all traces in the TraceGroup will have the same length.
	traceLength := len(traceGroup.Traces[0].RawTrace.Digests)
	for idx := 0; idx < traceLength; idx++ {
		for _, trace := range traceGroup.Traces {
			digest := trace.RawTrace.Digests[idx]
			// Don't bother counting up data for missing digests.
			if digest == tiling.MissingDigest {
				continue
			}
			// Go look up the entry for this digest. The sentinel value -1 will tell us if we haven't
			// seen one and need to add one.
			dsIdxToUpdate := -1
			for i, ds := range digestStats {
				if ds.digest == digest {
					dsIdxToUpdate = i
					break
				}
			}
			if dsIdxToUpdate == -1 {
				dsIdxToUpdate = len(digestStats)
				digestStats = append(digestStats, digestCountAndLastSeen{
					digest: digest,
				})
			}
			digestStats[dsIdxToUpdate].count++
			digestStats[dsIdxToUpdate].lastSeenIndex = idx
		}
	}

	// Sort in order of highest last seen index, with tiebreaks being higher count and then
	// lexicographically by digest.
	sort.Slice(digestStats, func(i, j int) bool {
		statsA, statsB := digestStats[i], digestStats[j]
		if statsA.lastSeenIndex != statsB.lastSeenIndex {
			return statsA.lastSeenIndex > statsB.lastSeenIndex
		}
		if statsA.count != statsB.count {
			return statsA.count > statsB.count
		}
		return statsA.digest < statsB.digest
	})

	// Assign the primary digest the primaryDigestIndex.
	digestIndices := make(map[types.Digest]int, maxDistinctDigestsToPresent)
	digestIndices[primary] = primaryDigestIndex
	// Go through the slice until we have either added the n most recent digests or have run out
	// of unique digests. We are careful not to add a digest we've already added (e.g. the primary
	// digest). We start with the most recent digests to preserve a little bit of backwards
	// compatibility with the assigned colors (e.g. developers are used to green and orange being the
	// more recent digests).
	digestIndex := 1
	for i := 0; i < len(digestStats) && len(digestIndices) < 1+mostRecentNDigests; i++ {
		ds := digestStats[i]
		if _, ok := digestIndices[ds.digest]; ok {
			continue
		}
		digestIndices[ds.digest] = digestIndex
		digestIndex++
	}

	// Re-sort the slice in order of highest count, with tiebreaks being a higher last seen index
	// and then lexicographically by digest.
	sort.Slice(digestStats, func(i, j int) bool {
		statsA, statsB := digestStats[i], digestStats[j]
		if statsA.count != statsB.count {
			return statsA.count > statsB.count
		}
		if statsA.lastSeenIndex != statsB.lastSeenIndex {
			return statsA.lastSeenIndex > statsB.lastSeenIndex
		}
		return statsA.digest < statsB.digest
	})

	// Assign the rest of the indices in order of most common digests.
	for i := 0; i < len(digestStats) && len(digestIndices) < maxDistinctDigestsToPresent; i++ {
		ds := digestStats[i]
		if _, ok := digestIndices[ds.digest]; ok {
			continue
		}
		digestIndices[ds.digest] = digestIndex
		digestIndex++
	}
	return digestIndices, len(digestStats)
}

// UntriagedUnignoredTryJobExclusiveDigests implements the SearchAPI interface. It uses the cached
// TryJobResults, so as to improve performance.
func (s *SearchImpl) UntriagedUnignoredTryJobExclusiveDigests(ctx context.Context, psID tjstore.CombinedPSID) (*frontend.UntriagedDigestList, error) {
	ctx, span := trace.StartSpan(ctx, "search.UntriagedUnignoredTryJobExclusiveDigests")
	defer span.End()

	var resultsForThisPS []tjstore.TryJobResult
	listTS := time.Now()
	clIdx := s.indexSource.GetIndexForCL(psID.CRS, psID.CL)
	if clIdx != nil && clIdx.LatestPatchset.Equal(psID) {
		s.clIndexCacheHitCounter.Inc(1)

		resultsForThisPS = clIdx.UntriagedResults
		listTS = clIdx.ComputedTS
	} else {
		s.clIndexCacheMissCounter.Inc(1)
		// CL Data was not indexed; either the CL was closed or too old. In this case, it is too
		// expensive to recompute this data, so we just return an error indicating we don't know.
		return nil, skerr.Fmt("CL/PS %v was not indexed", psID)
	}

	exp, err := s.getExpectations(ctx, psID.CL, psID.CRS)
	if err != nil {
		return nil, skerr.Wrap(err)
	}

	idx := s.indexSource.GetIndex()
	ignoreMatcher := idx.GetIgnoreMatcher()
	knownDigestsForTest := idx.DigestCountsByTest(types.IncludeIgnoredTraces)
	digestsByTrace := idx.DigestCountsByTrace(types.IncludeIgnoredTraces)

	var returnDigests []types.Digest
	var returnCorpora []string

	for _, tr := range resultsForThisPS {
		if err := ctx.Err(); err != nil {
			return nil, skerr.Wrap(err)
		}
		tn := types.TestName(tr.ResultParams[types.PrimaryKeyField])
		if exp.Classification(tn, tr.Digest) != expectations.Untriaged {
			// It's been triaged already.
			continue
		}
		if _, ok := knownDigestsForTest[tn][tr.Digest]; ok {
			// It's already been seen on master
			continue
		}
		p := make(paramtools.Params, len(tr.ResultParams)+len(tr.GroupParams)+len(tr.Options))
		p.Add(tr.GroupParams)
		p.Add(tr.ResultParams)
		// Compute the traceID here because by definition, the traceID does not include optional keys.
		traceID := tiling.TraceIDFromParams(p)
		p.Add(tr.Options)
		if ignoreMatcher.MatchAnyParams(p) {
			// This trace matches an ignore
			continue
		}

		uniqueDigestsForThisTrace := len(digestsByTrace[traceID])
		if uniqueDigestsForThisTrace > s.flakyTraceThreshold {
			// We don't want to include flaky traces.
			continue
		}

		if corpus := p[types.CorpusField]; !util.In(corpus, returnCorpora) {
			returnCorpora = append(returnCorpora, corpus)
		}
		returnDigests = append(returnDigests, tr.Digest)
	}
	// Sort digests alphabetically for determinism.
	sort.Slice(returnDigests, func(i, j int) bool {
		return returnDigests[i] < returnDigests[j]
	})
	return &frontend.UntriagedDigestList{
		Digests: returnDigests,
		Corpora: returnCorpora,
		TS:      listTS,
	}, nil
}

// getTriageHistory returns all TriageHistory for a given name and digest.
func (s *SearchImpl) getTriageHistory(ctx context.Context, history triageHistoryGetter, name types.TestName, digest types.Digest) []frontend.TriageHistory {
	id := expectations.ID{
		Grouping: name,
		Digest:   digest,
	}
	if cv, ok := s.triageHistoryCache.Load(id); ok {
		if rv, ok := cv.([]frontend.TriageHistory); ok {
			return rv
		}
		// purge the corrupt entry from the cache
		s.triageHistoryCache.Delete(id)
	}
	xth, err := history.GetTriageHistory(ctx, name, digest)
	if err != nil {
		metrics2.GetCounter("gold_search_triage_history_failures").Inc(1)
		sklog.Errorf("Could not get triage history, falling back to no history: %s", err)
		return nil
	}
	var rv []frontend.TriageHistory
	for _, th := range xth {
		rv = append(rv, frontend.TriageHistory{
			User: th.User,
			TS:   th.TS,
		})
	}
	s.triageHistoryCache.Store(id, rv)
	return rv
}

// addTriageHistory fills in the TriageHistory field of the passed in SRDigests. It does so in
// parallel to reduce latency of the response.
func (s *SearchImpl) addTriageHistory(ctx context.Context, history triageHistoryGetter, digestResults []*frontend.SearchResult) {
	ctx, span := trace.StartSpan(ctx, "search.addTriageHistory")
	defer span.End()
	wg := sync.WaitGroup{}
	wg.Add(len(digestResults))
	for i, dr := range digestResults {
		go func(i int, dr *frontend.SearchResult) {
			defer wg.Done()
			if dr == nil {
				// This should never happen
				return
			}
			digestResults[i].TriageHistory = s.getTriageHistory(ctx, history, dr.Test, dr.Digest)
		}(i, dr)
	}
	wg.Wait()
}

func (s *SearchImpl) reviewSystem(crs string) (clstore.ReviewSystem, error) {
	for _, rs := range s.reviewSystems {
		if rs.ID == crs {
			return rs, nil
		}
	}
	sklog.Errorf("Got passed in an unknown crs - %q", crs)
	return clstore.ReviewSystem{}, skerr.Fmt("Invalid crs")
}

// Make sure SearchImpl fulfills the SearchAPI interface.
var _ SearchAPI = (*SearchImpl)(nil)

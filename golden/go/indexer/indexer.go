// Package indexer continuously creates an index of the test results
// as the tiles, expectations, and ignores change.
package indexer

import (
	"context"
	"fmt"
	"sync"
	"time"

	ttlcache "github.com/patrickmn/go-cache"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/golden/go/clstore"
	"go.skia.org/infra/golden/go/tjstore"

	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/paramtools"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/golden/go/blame"
	"go.skia.org/infra/golden/go/diff"
	"go.skia.org/infra/golden/go/digest_counter"
	"go.skia.org/infra/golden/go/digesttools"
	"go.skia.org/infra/golden/go/expectations"
	"go.skia.org/infra/golden/go/paramsets"
	"go.skia.org/infra/golden/go/pdag"
	"go.skia.org/infra/golden/go/shared"
	"go.skia.org/infra/golden/go/storage"
	"go.skia.org/infra/golden/go/summary"
	"go.skia.org/infra/golden/go/tilesource"
	"go.skia.org/infra/golden/go/tiling"
	"go.skia.org/infra/golden/go/types"
	"go.skia.org/infra/golden/go/warmer"
)

const (
	// Metric to track the number of digests that do not have be uploaded by bots.
	knownHashesMetric = "known_digests"
	// Metric to track the number of changelists we currently have indexed.
	indexedCLsMetric = "gold_indexed_changelists"
)

// SearchIndex contains everything that is necessary to search
// our current knowledge about test results. It should be
// considered as immutable. Whenever the underlying data changes,
// a new index is calculated via a pdag.
type SearchIndex struct {
	searchIndexConfig
	// The indices of these arrays are the int values of types.IgnoreState
	dCounters         [2]digest_counter.DigestCounter
	summaries         [2]countsAndBlames
	paramsetSummaries [2]paramsets.ParamSummary
	preSliced         map[preSliceGroup][]*tiling.TracePair

	cpxTile tiling.ComplexTile
	blamer  blame.Blamer

	// This is set by the indexing pipeline when we just want to update
	// individual tests that have changed.
	testNames types.TestNameSet
}

type preSliceGroup struct {
	IgnoreState types.IgnoreState
	Corpus      string
	Test        types.TestName
}

// countsAndBlame makes the type declaration of SearchIndex a little nicer to read.
type countsAndBlames []*summary.TriageStatus

type searchIndexConfig struct {
	diffStore         diff.DiffStore
	expectationsStore expectations.Store
	gcsClient         storage.GCSClient
	warmer            warmer.DiffWarmer
}

// newSearchIndex creates a new instance of SearchIndex. It is not intended to
// be used outside of this package. SearchIndex instances are created by the
// Indexer and retrieved via GetIndex().
func newSearchIndex(sic searchIndexConfig, cpxTile tiling.ComplexTile) *SearchIndex {
	return &SearchIndex{
		searchIndexConfig: sic,
		// The indices of these slices are the int values of types.IgnoreState
		dCounters:         [2]digest_counter.DigestCounter{},
		summaries:         [2]countsAndBlames{},
		paramsetSummaries: [2]paramsets.ParamSummary{},
		preSliced:         map[preSliceGroup][]*tiling.TracePair{},
		cpxTile:           cpxTile,
	}
}

// SearchIndexForTesting returns filled in search index to be used when testing. Note that the
// indices of the arrays are the int values of types.IgnoreState
func SearchIndexForTesting(cpxTile tiling.ComplexTile, dc [2]digest_counter.DigestCounter, pm [2]paramsets.ParamSummary, exp expectations.Store, b blame.Blamer) (*SearchIndex, error) {
	s := &SearchIndex{
		searchIndexConfig: searchIndexConfig{
			expectationsStore: exp,
		},
		dCounters:         dc,
		summaries:         [2]countsAndBlames{},
		paramsetSummaries: pm,
		preSliced:         map[preSliceGroup][]*tiling.TracePair{},
		blamer:            b,
		cpxTile:           cpxTile,
	}
	return s, preSliceData(context.Background(), s)
}

// Tile implements the IndexSearcher interface.
func (idx *SearchIndex) Tile() tiling.ComplexTile {
	return idx.cpxTile
}

// GetIgnoreMatcher implements the IndexSearcher interface.
func (idx *SearchIndex) GetIgnoreMatcher() paramtools.ParamMatcher {
	return idx.cpxTile.IgnoreRules()
}

// DigestCountsByTest implements the IndexSearcher interface.
func (idx *SearchIndex) DigestCountsByTest(is types.IgnoreState) map[types.TestName]digest_counter.DigestCount {
	return idx.dCounters[is].ByTest()
}

// MaxDigestsByTest implements the IndexSearcher interface.
func (idx *SearchIndex) MaxDigestsByTest(is types.IgnoreState) map[types.TestName]types.DigestSet {
	return idx.dCounters[is].MaxDigestsByTest()
}

// DigestCountsByTrace implements the IndexSearcher interface.
func (idx *SearchIndex) DigestCountsByTrace(is types.IgnoreState) map[tiling.TraceID]digest_counter.DigestCount {
	return idx.dCounters[is].ByTrace()
}

// DigestCountsByQuery implements the IndexSearcher interface.
func (idx *SearchIndex) DigestCountsByQuery(query paramtools.ParamSet, is types.IgnoreState) digest_counter.DigestCount {
	return idx.dCounters[is].ByQuery(idx.cpxTile.GetTile(is), query)
}

// GetSummaries implements the IndexSearcher interface.
func (idx *SearchIndex) GetSummaries(is types.IgnoreState) []*summary.TriageStatus {
	return idx.summaries[is]
}

// SummarizeByGrouping implements the IndexSearcher interface.
func (idx *SearchIndex) SummarizeByGrouping(ctx context.Context, corpus string, query paramtools.ParamSet, is types.IgnoreState, head bool) ([]*summary.TriageStatus, error) {
	exp, err := idx.expectationsStore.Get(ctx)
	if err != nil {
		return nil, skerr.Wrap(err)
	}

	// The summaries are broken down by grouping (currently corpus and test name). Conveniently,
	// we already have the traces broken down by those areas, and summaries are independent, so we
	// can calculate them in parallel.
	type groupedTracePairs []*tiling.TracePair
	var groups []groupedTracePairs
	for g, traces := range idx.preSliced {
		if g.IgnoreState == is && g.Corpus == corpus && g.Test != "" {
			groups = append(groups, traces)
		}
	}
	rv := make([]*summary.TriageStatus, len(groups))
	wg := sync.WaitGroup{}
	for i, g := range groups {
		wg.Add(1)
		go func(slice int, gtp groupedTracePairs) {
			defer wg.Done()
			d := summary.Data{
				Traces: gtp,
				// These are all thread-safe, so they can be shared.
				Expectations: exp,
				ByTrace:      idx.dCounters[is].ByTrace(),
				Blamer:       idx.blamer,
			}
			ts := d.Calculate(nil, query, head)
			if len(ts) > 1 {
				// this should never happen, as we'd only get multiple if there were multiple
				// tests in the pre-sliced data (e.g. our pre-slicing code is bugged).
				sklog.Warningf("Summary Calculation should always be length 1, but wasn't %#v", ts)
				return
			} else if len(ts) == 0 {
				// This will happen if query removes all of the traces belonging to this test.
				// It results in a nil in the return value; if that is a problem we can either
				// fill in a zeroish value or a TriageStatus with Test/Corpus filled and 0 in the
				// counts.
				return
			}
			rv[slice] = ts[0]
		}(i, g)
	}
	wg.Wait()
	return rv, nil
}

// GetParamsetSummary implements the IndexSearcher interface.
func (idx *SearchIndex) GetParamsetSummary(test types.TestName, digest types.Digest, is types.IgnoreState) paramtools.ParamSet {
	return idx.paramsetSummaries[is].Get(test, digest)
}

// GetParamsetSummaryByTest implements the IndexSearcher interface.
func (idx *SearchIndex) GetParamsetSummaryByTest(is types.IgnoreState) map[types.TestName]map[types.Digest]paramtools.ParamSet {
	return idx.paramsetSummaries[is].GetByTest()
}

// GetBlame implements the IndexSearcher interface.
func (idx *SearchIndex) GetBlame(test types.TestName, digest types.Digest, commits []*tiling.Commit) blame.BlameDistribution {
	if idx.blamer == nil {
		// should never happen - indexer should have this initialized
		// before the web server starts serving requests.
		return blame.BlameDistribution{}
	}
	return idx.blamer.GetBlame(test, digest, commits)
}

// SlicedTraces returns a slice of TracePairs that match the query and the ignore state.
// This is meant to be a superset of traces, as only the corpus and testname from the query are
// used for this pre-filter step.
func (idx *SearchIndex) SlicedTraces(is types.IgnoreState, query map[string][]string) []*tiling.TracePair {
	if len(query[types.CorpusField]) == 0 {
		return idx.preSliced[preSliceGroup{
			IgnoreState: is,
		}]
	}
	var rv []*tiling.TracePair
	for _, corpus := range query[types.CorpusField] {
		if len(query[types.PrimaryKeyField]) == 0 {
			rv = append(rv, idx.preSliced[preSliceGroup{
				IgnoreState: is,
				Corpus:      corpus,
			}]...)
		} else {
			for _, tn := range query[types.PrimaryKeyField] {
				rv = append(rv, idx.preSliced[preSliceGroup{
					IgnoreState: is,
					Corpus:      corpus,
					Test:        types.TestName(tn),
				}]...)
			}
		}
	}
	return rv
}

// MostRecentPositiveDigest implements the IndexSearcher interface.
func (idx *SearchIndex) MostRecentPositiveDigest(ctx context.Context, traceID tiling.TraceID) (types.Digest, error) {
	defer metrics2.FuncTimer().Stop()

	// Retrieve Trace for the given traceID.
	trace, ok := idx.cpxTile.GetTile(types.IncludeIgnoredTraces).Traces[traceID]
	if !ok {
		return tiling.MissingDigest, nil
	}

	// Retrieve expectations.
	exps, err := idx.expectationsStore.Get(ctx)
	if err != nil {
		return "", skerr.Wrapf(err, "retrieving expectations (traceID=%q)", traceID)
	}

	// Find and return the most recent positive digest in the Trace.
	for i := len(trace.Digests) - 1; i >= 0; i-- {
		digest := trace.Digests[i]
		if digest != tiling.MissingDigest && exps.Classification(trace.TestName(), digest) == expectations.Positive {
			return digest, nil
		}
	}
	return tiling.MissingDigest, nil
}

type IndexerConfig struct {
	DiffStore         diff.DiffStore
	ChangeListener    expectations.ChangeEventRegisterer
	ExpectationsStore expectations.Store
	GCSClient         storage.GCSClient
	TileSource        tilesource.TileSource
	Warmer            warmer.DiffWarmer
	TryJobStore       tjstore.Store
	CLStore           clstore.Store
}

// Indexer is the type that continuously processes data as the underlying
// data change. It uses a DAG that encodes the dependencies of the
// different components of an index and creates a processing pipeline on top
// of it.
type Indexer struct {
	IndexerConfig

	pipeline         *pdag.Node
	indexTestsNode   *pdag.Node
	lastMasterIndex  *SearchIndex
	masterIndexMutex sync.RWMutex

	changeListIndices *ttlcache.Cache
}

// New returns a new IndexSource instance. It synchronously indexes the initially
// available tile. If the indexing fails an error is returned.
// The provided interval defines how often the index should be refreshed.
func New(ctx context.Context, ic IndexerConfig, interval time.Duration) (*Indexer, error) {
	ret := &Indexer{
		IndexerConfig:     ic,
		changeListIndices: ttlcache.New(changelistCacheExpirationDuration, changelistCacheExpirationDuration),
	}

	// Set up the processing pipeline.
	root := pdag.NewNodeWithParents(pdag.NoOp)

	// We can start indexing the ChangeLists right away since it only depends on the expectations
	// (and nothing from the master branch index).
	pdag.NewNodeWithParents(ret.calcChangeListIndices, root)

	// At the top level, Add the DigestCounters...
	countsNodeInclude := root.Child(calcDigestCountsInclude)
	// These are run in parallel because they can take tens of seconds
	// in large repos like Skia.
	countsNodeExclude := root.Child(calcDigestCountsExclude)

	preSliceNode := root.Child(preSliceData)

	// Node that triggers blame and writing baselines.
	// This is used to trigger when expectations change.
	// We don't need to re-calculate DigestCounts if the
	// expectations change because the DigestCounts don't care about
	// the expectations, only on the tile.
	indexTestsNode := root.Child(pdag.NoOp)

	// ... and invoke the Blamer to calculate the blames.
	blamerNode := indexTestsNode.Child(calcBlame)

	// Parameters depend on DigestCounter.
	paramsNodeInclude := pdag.NewNodeWithParents(calcParamsetsInclude, countsNodeInclude)
	// These are run in parallel because they can take tens of seconds
	// in large repos like Skia.
	paramsNodeExclude := pdag.NewNodeWithParents(calcParamsetsExclude, countsNodeExclude)

	// Write known hashes after ignores are computed. DigestCount is a
	// convenient way to get all the hashes, so that's what this node uses.
	writeHashes := countsNodeInclude.Child(writeKnownHashesList)

	// Summaries depend on DigestCounter and Blamer.
	summariesNode := pdag.NewNodeWithParents(calcSummaries, countsNodeInclude, countsNodeExclude, blamerNode, preSliceNode)

	// The Warmer depends on summaries.
	pdag.NewNodeWithParents(runWarmer, summariesNode)

	// Set the result on the Indexer instance, once summaries, parameters and writing
	// the hash files is done.
	pdag.NewNodeWithParents(ret.setIndex, summariesNode, paramsNodeInclude, paramsNodeExclude, writeHashes)

	ret.pipeline = root
	ret.indexTestsNode = indexTestsNode

	// Process the first tile and start the indexing process.
	return ret, ret.start(ctx, interval)
}

// GetIndex implements the IndexSource interface.
func (ix *Indexer) GetIndex() IndexSearcher {
	return ix.getIndex()
}

// getIndex is like GetIndex but returns the bare struct, for
// internal package use.
func (ix *Indexer) getIndex() *SearchIndex {
	ix.masterIndexMutex.RLock()
	defer ix.masterIndexMutex.RUnlock()
	return ix.lastMasterIndex
}

// start builds the initial index and starts the background
// process to continuously build indices.
func (ix *Indexer) start(ctx context.Context, interval time.Duration) error {
	if interval == 0 {
		sklog.Warning("Not starting indexer because duration was 0")
		return nil
	}
	defer shared.NewMetricsTimer("initial_synchronous_index").Stop()
	// Build the first index synchronously.
	tileStream := tilesource.GetTileStreamNow(ix.TileSource, interval, "gold-indexer")
	if err := ix.executePipeline(ctx, <-tileStream); err != nil {
		return err
	}

	// When the master expectations change, update the blamer and its dependents. This channel
	// will usually be empty, except when triaging happens. We set the size to be big enough to
	// handle a large bulk triage, if needed.
	expCh := make(chan expectations.ID, 100000)
	ix.ChangeListener.ListenForChange(func(e expectations.ID) {
		// Schedule the list of test names to be recalculated.
		expCh <- e
	})

	// Keep building indices for different types of events. This is the central
	// event loop of the indexer.
	go func() {
		var cpxTile tiling.ComplexTile
		for {
			if err := ctx.Err(); err != nil {
				sklog.Warningf("Stopping indexer - context error: %s", err)
				return
			}
			var testChanges []expectations.ID

			// See if there is a tile or changed tests.
			cpxTile = nil
			select {
			// Catch a new tile.
			case cpxTile = <-tileStream:
				sklog.Infof("Indexer saw a new tile")

				// Catch any test changes.
			case tn := <-expCh:
				testChanges = append(testChanges, tn)
				sklog.Infof("Indexer saw some tests change")
			}

			// Drain all input channels, effectively bunching signals together that arrive in short
			// succession.
		DrainLoop:
			for {
				select {
				case tn := <-expCh:
					testChanges = append(testChanges, tn)
				default:
					break DrainLoop
				}
			}

			// If there is a tile, re-index everything and forget the
			// individual tests that changed.
			if cpxTile != nil {
				if err := ix.executePipeline(ctx, cpxTile); err != nil {
					sklog.Errorf("Unable to index tile: %s", err)
				}
			} else if len(testChanges) > 0 {
				// Only index the tests that have changed.
				ix.indexTests(ctx, testChanges)
			}
		}
	}()

	return nil
}

// executePipeline runs the given tile through the the indexing pipeline.
// pipeline.Trigger blocks until everything is done, so this function will as well.
func (ix *Indexer) executePipeline(ctx context.Context, cpxTile tiling.ComplexTile) error {
	defer shared.NewMetricsTimer("indexer_execute_pipeline").Stop()
	// Create a new index from the given tile.
	sic := searchIndexConfig{
		diffStore:         ix.DiffStore,
		expectationsStore: ix.ExpectationsStore,
		gcsClient:         ix.GCSClient,
		warmer:            ix.Warmer,
	}
	return ix.pipeline.Trigger(ctx, newSearchIndex(sic, cpxTile))
}

// indexTest creates an updated index by indexing the given list of expectation changes.
func (ix *Indexer) indexTests(ctx context.Context, testChanges []expectations.ID) {
	// Get all the test names that had expectations changed.
	testNames := types.TestNameSet{}
	for _, d := range testChanges {
		testNames[d.Grouping] = true
	}
	if len(testNames) == 0 {
		return
	}

	sklog.Infof("Going to re-index %d tests", len(testNames))

	defer shared.NewMetricsTimer("index_tests").Stop()
	newIdx := ix.cloneLastIndex()
	// Set the testNames such that we only recompute those tests.
	newIdx.testNames = testNames
	if err := ix.indexTestsNode.Trigger(ctx, newIdx); err != nil {
		sklog.Errorf("Error indexing tests: %v \n\n Got error: %s", testNames.Keys(), err)
	}
}

// cloneLastIndex returns a copy of the most recent index.
func (ix *Indexer) cloneLastIndex() *SearchIndex {
	lastIdx := ix.getIndex()
	sic := searchIndexConfig{
		diffStore:         ix.DiffStore,
		expectationsStore: ix.ExpectationsStore,
		gcsClient:         ix.GCSClient,
		warmer:            ix.Warmer,
	}
	return &SearchIndex{
		searchIndexConfig: sic,
		cpxTile:           lastIdx.cpxTile,
		dCounters:         lastIdx.dCounters,         // stay the same even if expectations change.
		paramsetSummaries: lastIdx.paramsetSummaries, // stay the same even if expectations change.
		preSliced:         lastIdx.preSliced,         // stay the same even if expectations change.

		summaries: [2]countsAndBlames{
			// the objects inside the summaries are immutable, but may be replaced if expectations
			// are recalculated for a subset of tests.
			lastIdx.summaries[types.ExcludeIgnoredTraces],
			lastIdx.summaries[types.IncludeIgnoredTraces],
		},

		blamer: nil, // This will need to be recomputed if expectations change.

		// Force testNames to be empty, just to be sure we re-compute everything by default
		testNames: nil,
	}
}

// setIndex sets the lastMasterIndex value at the very end of the pipeline.
func (ix *Indexer) setIndex(_ context.Context, state interface{}) error {
	newIndex := state.(*SearchIndex)
	ix.masterIndexMutex.Lock()
	defer ix.masterIndexMutex.Unlock()
	ix.lastMasterIndex = newIndex
	return nil
}

// calcDigestCountsInclude is the pipeline function to calculate DigestCounts from
// the full tile (not applying ignore rules)
func calcDigestCountsInclude(_ context.Context, state interface{}) error {
	idx := state.(*SearchIndex)
	is := types.IncludeIgnoredTraces
	idx.dCounters[is] = digest_counter.New(idx.cpxTile.GetTile(is))
	return nil
}

// calcDigestCountsExclude is the pipeline function to calculate DigestCounts from
// the partial tile (applying ignore rules).
func calcDigestCountsExclude(_ context.Context, state interface{}) error {
	idx := state.(*SearchIndex)
	is := types.ExcludeIgnoredTraces
	idx.dCounters[is] = digest_counter.New(idx.cpxTile.GetTile(is))
	return nil
}

// preSliceData is the pipeline function to pre-slice our traces. Currently, we pre-slice by
// corpus name and then by test name because this breaks our traces up into groups of <1000.
func preSliceData(_ context.Context, state interface{}) error {
	idx := state.(*SearchIndex)
	for _, is := range types.IgnoreStates {
		t := idx.cpxTile.GetTile(is)
		for id, trace := range t.Traces {
			if trace == nil {
				sklog.Warningf("Unexpected nil trace id %s", id)
				continue
			}
			tp := tiling.TracePair{
				ID:    id,
				Trace: trace,
			}
			// Pre-slice the data by IgnoreState, then by IgnoreState and Corpus, finally by all
			// three of IgnoreState/Corpus/Test. We shouldn't allow queries by Corpus w/o specifying
			// IgnoreState, nor should we allow queries by TestName w/o specifying a Corpus or
			// IgnoreState.
			ignoreOnly := preSliceGroup{
				IgnoreState: is,
			}
			idx.preSliced[ignoreOnly] = append(idx.preSliced[ignoreOnly], &tp)

			ignoreAndCorpus := preSliceGroup{
				IgnoreState: is,
				Corpus:      trace.Corpus(),
			}
			idx.preSliced[ignoreAndCorpus] = append(idx.preSliced[ignoreAndCorpus], &tp)

			ignoreCorpusTest := preSliceGroup{
				IgnoreState: is,
				Corpus:      trace.Corpus(),
				Test:        trace.TestName(),
			}
			idx.preSliced[ignoreCorpusTest] = append(idx.preSliced[ignoreCorpusTest], &tp)
		}
	}
	return nil
}

// calcSummaries is the pipeline function to calculate the summaries.
func calcSummaries(ctx context.Context, state interface{}) error {
	idx := state.(*SearchIndex)
	exp, err := idx.expectationsStore.Get(ctx)
	if err != nil {
		return skerr.Wrap(err)
	}
	for _, is := range types.IgnoreStates {
		d := summary.Data{
			Traces:       idx.SlicedTraces(is, nil),
			Expectations: exp,
			ByTrace:      idx.dCounters[is].ByTrace(),
			Blamer:       idx.blamer,
		}
		sum := d.Calculate(idx.testNames, nil, true)
		// If we have recalculated only a subset of tests, we want to keep the results from
		// the previous scans and overwrite what we have just recomputed.
		if len(idx.testNames) > 0 && len(idx.summaries[is]) > 0 {
			idx.summaries[is] = summary.MergeSorted(idx.summaries[is], sum)
		} else {
			idx.summaries[is] = sum
		}
	}
	return nil
}

// calcParamsetsInclude is the pipeline function to calculate the parameters from
// the full tile (not applying ignore rules)
func calcParamsetsInclude(_ context.Context, state interface{}) error {
	idx := state.(*SearchIndex)
	is := types.IncludeIgnoredTraces
	idx.paramsetSummaries[is] = paramsets.NewParamSummary(idx.cpxTile.GetTile(is), idx.dCounters[is])
	return nil
}

// calcParamsetsExclude is the pipeline function to calculate the parameters from
// the partial tile (applying ignore rules)
func calcParamsetsExclude(_ context.Context, state interface{}) error {
	idx := state.(*SearchIndex)
	is := types.ExcludeIgnoredTraces
	idx.paramsetSummaries[is] = paramsets.NewParamSummary(idx.cpxTile.GetTile(is), idx.dCounters[is])
	return nil
}

// calcBlame is the pipeline function to calculate the blame.
func calcBlame(ctx context.Context, state interface{}) error {
	idx := state.(*SearchIndex)
	exp, err := idx.expectationsStore.Get(ctx)
	if err != nil {
		return skerr.Wrapf(err, "fetching expectations needed to calculate blame")
	}
	b, err := blame.New(idx.cpxTile.GetTile(types.ExcludeIgnoredTraces), exp)
	if err != nil {
		idx.blamer = nil
		return skerr.Wrapf(err, "calculating blame")
	}
	idx.blamer = b
	return nil
}

func writeKnownHashesList(ctx context.Context, state interface{}) error {
	idx := state.(*SearchIndex)

	// Only write the hash file if a storage client is available.
	if idx.gcsClient == nil {
		return nil
	}

	// Trigger writing the hashes list.
	go func() {
		// Make sure this doesn't hang indefinitely. 2 minutes was chosen as a time that's plenty
		// long to make sure it completes (usually takes only a few seconds).
		ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()

		byTest := idx.DigestCountsByTest(types.IncludeIgnoredTraces)
		unavailableDigests, err := idx.diffStore.UnavailableDigests(ctx)
		if err != nil {
			sklog.Warningf("could not fetch unavailable digests, going to assume all are valid: %s", err)
			unavailableDigests = nil
		}
		// Collect all hashes in the tile that haven't been marked as unavailable yet.
		hashes := types.DigestSet{}
		for _, test := range byTest {
			for k := range test {
				if _, ok := unavailableDigests[k]; !ok {
					hashes[k] = true
				}
			}
		}

		for h := range hashes {
			if _, ok := unavailableDigests[h]; ok {
				delete(hashes, h)
			}
		}

		// Keep track of the number of known hashes since this directly affects how
		// many images the bots have to upload.
		metrics2.GetInt64Metric(knownHashesMetric).Update(int64(len(hashes)))
		if err := idx.gcsClient.WriteKnownDigests(ctx, hashes.Keys()); err != nil {
			sklog.Errorf("Error writing known digests list: %s", err)
		}
		sklog.Infof("Finished writing %d known hashes", len(hashes))
	}()
	return nil
}

// runWarmer is the pipeline function to run the warmer. It runs
// asynchronously since its results are not relevant for the searchIndex.
func runWarmer(ctx context.Context, state interface{}) error {
	idx := state.(*SearchIndex)

	is := types.IncludeIgnoredTraces
	exp, err := idx.expectationsStore.Get(ctx)
	if err != nil {
		return skerr.Wrapf(err, "preparing to run warmer - expectations failure")
	}
	d := digesttools.NewClosestDiffFinder(exp, idx.dCounters[is], idx.diffStore)

	// We don't want to pass the whole digestCounters because the byTrace map actually takes
	// quite a lot of memory, with potentially millions of entries.
	wd := warmer.Data{
		TestSummaries: idx.summaries[is],
		DigestsByTest: idx.dCounters[is].ByTest(),
		SubsetOfTests: idx.testNames,
	}
	// Pass these in so as to allow the rest of the items in the index to be GC'd if needed.
	go func(warmer warmer.DiffWarmer, wd warmer.Data, d digesttools.ClosestDiffFinder) {
		// If there are somehow lots and lots of diffs or the warmer gets stuck, we should bail out
		// at some point to prevent amount of work being done on the diffstore (e.g. a remote
		// diffserver) from growing in an unbounded fashion.
		// 15 minutes was chosen based on the 90th percentile time looking at the metrics.
		ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
		defer cancel()

		if err := warmer.PrecomputeDiffs(ctx, wd, d); err != nil {
			sklog.Warningf("Could not precompute diffs for %d summaries and %d test names: %s", len(wd.TestSummaries), len(wd.SubsetOfTests), err)
		}
	}(idx.warmer, wd, d)
	return nil
}

const (
	// maxAgeOfOpenCLsToIndex is the maximum time between now and a CL's last updated time that we
	// will still index.
	maxAgeOfOpenCLsToIndex = 3 * 24 * time.Hour
	// We only keep around open CLs in the index. When a CL is closed, we don't update the indices
	// any more. These entries will expire and be removed from the cache after
	// changelistCacheExpirationDuration time has passed.
	changelistCacheExpirationDuration = 24 * time.Hour
	// maxCLsToIndex is the maximum number of CLs we query each loop to index them. Hopefully this
	// limit isn't reached regularly.
	maxCLsToIndex = 1000
)

func (ix *Indexer) calcChangeListIndices(ctx context.Context, state interface{}) error {
	// Update the metric when we return (either from error or because we completed indexing).
	defer metrics2.GetInt64Metric(indexedCLsMetric).Update(int64(ix.changeListIndices.ItemCount()))
	defer shared.NewMetricsTimer("indexer_calculate_changelist_indices").Stop()
	// Make sure this doesn't take arbitrarily long.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	now := time.Now()
	masterExp, err := ix.ExpectationsStore.Get(ctx)
	if err != nil {
		return skerr.Wrap(err)
	}

	// An arbitrary cut off to the amount of recent, open CLs we try to index.
	recent := time.Now().Add(-maxAgeOfOpenCLsToIndex)
	xcl, _, err := ix.CLStore.GetChangeLists(ctx, clstore.SearchOptions{
		StartIdx:    0,
		Limit:       maxCLsToIndex,
		OpenCLsOnly: true,
		After:       recent,
	})

	sklog.Infof("Indexing %d CLs", len(xcl))

	crs := ix.CLStore.System()
	const numChunks = 8 // arbitrarily picked, could likely be tuned based on contention of
	// changelistCache
	chunkSize := (len(xcl) / numChunks) + 1 // add one to avoid integer truncation.
	err = util.ChunkIterParallel(ctx, len(xcl), chunkSize, func(ctx context.Context, startIdx int, endIdx int) error {
		for _, cl := range xcl[startIdx:endIdx] {
			if err := ctx.Err(); err != nil {
				sklog.Errorf("ChangeList indexing timed out (%v)", err)
				return nil
			}

			issueExpStore := ix.ExpectationsStore.ForChangeList(cl.SystemID, crs)
			clExps, err := issueExpStore.Get(ctx)
			if err != nil {
				return skerr.Wrapf(err, "loading expectations for cl %s (%s)", cl.SystemID, crs)
			}
			exps := expectations.Join(clExps, masterExp)

			clKey := fmt.Sprintf("%s_%s", crs, cl.SystemID)
			clIdx, ok := ix.getCLIndex(clKey)
			var alreadyFilteredPS tjstore.CombinedPSID
			if !ok || clIdx.ComputedTS.Before(cl.Updated) {
				// Compute it from scratch and store
				xps, err := ix.CLStore.GetPatchSets(ctx, cl.SystemID)
				if err != nil {
					return skerr.Wrap(err)
				}
				if len(xps) == 0 {
					continue
				}
				latestPS := xps[len(xps)-1]
				psID := tjstore.CombinedPSID{
					CL:  cl.SystemID,
					CRS: crs,
					PS:  latestPS.SystemID,
				}
				xtjr, err := ix.TryJobStore.GetResults(ctx, psID)
				if err != nil {
					return skerr.Wrap(err)
				}
				filtered := filterUntriagedResults(xtjr, exps)
				if !ok {
					clIdx = &ChangeListIndex{
						UntriagedResultsProduced: map[tjstore.CombinedPSID][]tjstore.TryJobResult{},
					}
				}
				clIdx.UntriagedResultsProduced[psID] = filtered
				alreadyFilteredPS = psID
			}
			if ok {
				// Make a copy of the existing index so as we update maps, etc, it doesn't cause a race
				// with anybody retrieving them.
				clIdx = clIdx.Copy()
			}

			// Re-apply expectations on existing TryJob results. Then, update the timestamp and update the
			// cache.
			for psID, xtjr := range clIdx.UntriagedResultsProduced {
				// One of these entries might be newly created (and therefore was already filtered).
				if psID.Equal(alreadyFilteredPS) {
					continue
				}
				clIdx.UntriagedResultsProduced[psID] = filterUntriagedResults(xtjr, exps)
			}
			clIdx.ComputedTS = now
			ix.changeListIndices.Set(clKey, clIdx, ttlcache.DefaultExpiration)
		}
		return nil
	})
	// Wrap err if non-nil.
	return skerr.Wrap(err)
}

// filterUntriagedResults goes through all the TryJobResults and returns a slice with just the
// untriaged results.
func filterUntriagedResults(xtjr []tjstore.TryJobResult, exps expectations.Classifier) []tjstore.TryJobResult {
	var rv []tjstore.TryJobResult
	for _, tjr := range xtjr {
		tn := types.TestName(tjr.ResultParams[types.PrimaryKeyField])
		if exps.Classification(tn, tjr.Digest) == expectations.Untriaged {
			rv = append(rv, tjr)
		}
	}
	return rv
}

// getCLIndex is a helper that returns the appropriately typed element from changeListIndices
func (ix *Indexer) getCLIndex(key string) (*ChangeListIndex, bool) {
	clIdx, ok := ix.changeListIndices.Get(key)
	if !ok {
		return nil, false
	}
	return clIdx.(*ChangeListIndex), true
}

// GetIndexForCL implements the IndexSource interface.
func (ix *Indexer) GetIndexForCL(crs, clID string) *ChangeListIndex {
	key := fmt.Sprintf("%s_%s", crs, clID)
	clIdx, ok := ix.getCLIndex(key)
	if !ok {
		return nil
	}
	// Return a copy to prevent clients from messing with the cached version.
	return clIdx.Copy()
}

// Make sure SearchIndex fulfills the IndexSearcher interface
var _ IndexSearcher = (*SearchIndex)(nil)

// Make sure Indexer fulfills the IndexSource interface
var _ IndexSource = (*Indexer)(nil)

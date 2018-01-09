// Package indexer continously creates an index of the test results
// as the tiles, expectations and ignores change.
package indexer

import (
	"net/url"
	"sync"
	"time"

	"go.skia.org/infra/golden/go/baseline"

	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/util"

	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/paramtools"
	"go.skia.org/infra/go/tiling"
	"go.skia.org/infra/go/timer"
	"go.skia.org/infra/golden/go/blame"
	"go.skia.org/infra/golden/go/diff"
	"go.skia.org/infra/golden/go/expstorage"
	"go.skia.org/infra/golden/go/paramsets"
	"go.skia.org/infra/golden/go/pdag"
	"go.skia.org/infra/golden/go/storage"
	"go.skia.org/infra/golden/go/summary"
	"go.skia.org/infra/golden/go/tally"
	"go.skia.org/infra/golden/go/types"
	"go.skia.org/infra/golden/go/warmer"
)

const (
	// Event emitted when the indexer updates the search index.
	// Callback argument: *SearchIndex
	EV_INDEX_UPDATED = "indexer:index-updated"

	// Metric to track the number of digests that do not have be uploaded by bots.
	METRIC_KNOWN_HASHES = "known-digests"
)

// SearchIndex contains everything that is necessary to search
// our current knowledge about test results. It should be
// considered as immutable. Whenever the underlying data change
// a new index is calculated via a pdag.
type SearchIndex struct {
	tilePair             *types.TilePair
	tallies              *tally.Tallies
	talliesWithIgnores   *tally.Tallies
	summaries            *summary.Summaries
	summariesWithIgnores *summary.Summaries
	paramsetSummary      *paramsets.ParamSummary
	blamer               *blame.Blamer
	warmer               *warmer.Warmer

	// This is set by the indexing pipeline when we just want to update
	// individual tests that have changed.
	testNames []string
	storages  *storage.Storage
}

// newSearchIndex creates a new instance of SearchIndex. It is not intended to
// be used outside of this package. SearchIndex instances are created by the
// Indexer and retrieved via GetIndex().
func newSearchIndex(storages *storage.Storage, tilePair *types.TilePair) *SearchIndex {
	return &SearchIndex{
		tilePair:             tilePair,
		tallies:              tally.New(),
		talliesWithIgnores:   tally.New(),
		summaries:            summary.New(storages),
		summariesWithIgnores: summary.New(storages),
		paramsetSummary:      paramsets.New(),
		blamer:               blame.New(storages),
		warmer:               warmer.New(storages),
		storages:             storages,
	}
}

// GetTile returns the current tile either with or without the ignored traces.
func (idx *SearchIndex) GetTile(includeIgnores bool) *tiling.Tile {
	if includeIgnores {
		return idx.tilePair.TileWithIgnores
	}
	return idx.tilePair.Tile
}

// GetIgnoreMatcher returns a matcher for the ignore rules that were used to
// build the tile with ignores.
func (idx *SearchIndex) GetIgnoreMatcher() paramtools.ParamMatcher {
	return idx.tilePair.IgnoreRules
}

// Proxy to tally.Tallies.ByTest
func (idx *SearchIndex) TalliesByTest(includeIgnores bool) map[string]tally.Tally {
	if includeIgnores {
		return idx.talliesWithIgnores.ByTest()
	}
	return idx.tallies.ByTest()
}

// Proxy to tally.Tallies.MaxDigestsByTest
func (idx *SearchIndex) MaxDigestsByTest(includeIgnores bool) map[string]util.StringSet {
	if includeIgnores {
		return idx.talliesWithIgnores.MaxDigestsByTest()
	}
	return idx.tallies.MaxDigestsByTest()
}

// Proxy to tally.Tallies.ByTrace
func (idx *SearchIndex) TalliesByTrace(includeIgnores bool) map[string]tally.Tally {
	if includeIgnores {
		return idx.talliesWithIgnores.ByTrace()
	}
	return idx.tallies.ByTrace()
}

// ByQuery returns a Tally of all the digests that match the given query.
func (idx *SearchIndex) TalliesByQuery(query url.Values, includeIgnores bool) tally.Tally {
	return idx.tallies.ByQuery(idx.GetTile(includeIgnores), query)
}

// Proxy to summary.Summary.Get.
func (idx *SearchIndex) GetSummaries(includeIgnores bool) map[string]*summary.Summary {
	if includeIgnores {
		return idx.summariesWithIgnores.Get()
	}
	return idx.summaries.Get()
}

// Proxy to summary.CalcSummaries.
func (idx *SearchIndex) CalcSummaries(testNames []string, query url.Values, includeIgnores, head bool) (map[string]*summary.Summary, error) {
	if includeIgnores {
		return idx.summaries.CalcSummaries(idx.tilePair.TileWithIgnores, testNames, query, head)
	}
	return idx.summaries.CalcSummaries(idx.tilePair.Tile, testNames, query, head)
}

// Proxy to paramsets.Get
func (idx *SearchIndex) GetParamsetSummary(test, digest string, includeIgnores bool) paramtools.ParamSet {
	return idx.paramsetSummary.Get(test, digest, includeIgnores)
}

// Proxy to paramsets.GetByTest
func (idx *SearchIndex) GetParamsetSummaryByTest(includeIgnores bool) map[string]map[string]paramtools.ParamSet {
	return idx.paramsetSummary.GetByTest(includeIgnores)
}

// Proxy to blame.Blamer.GetBlame.
func (idx *SearchIndex) GetBlame(test, digest string, commits []*tiling.Commit) *blame.BlameDistribution {
	return idx.blamer.GetBlame(test, digest, commits)
}

// Indexer is the type that drive continously indexing as the underlying
// data change. It uses a DAG that encodes the dependencies of the
// different components of an index and creates a processing pipeline on top
// of it.
type Indexer struct {
	storages       *storage.Storage
	pipeline       *pdag.Node
	indexTestsNode *pdag.Node
	lastIndex      *SearchIndex
	testNames      []string
	mutex          sync.RWMutex
}

// New returns a new Indexer instance. It synchronously indexes the initiallly
// available tile. If the indexing fails an error is returned.
// The provided interval defines how often the index should be refreshed.
func New(storages *storage.Storage, interval time.Duration) (*Indexer, error) {
	ret := &Indexer{
		storages: storages,
	}

	// Set up the processing pipeline.
	root := pdag.NewNode(pdag.NoOp)

	// Node that triggers blame and writing baseslines.
	// This is used to trigger when expectations change.
	indexTestsNode := root.Child(pdag.NoOp)

	blamerNode := indexTestsNode.Child(calcBlame)

	// write baselines whenever a new tile is processed or when the expectations
	// change.
	pdag.NewNode(writeBaseline, indexTestsNode)

	// Add the blamer and tallies
	tallyNode := root.Child(calcTallies)
	tallyIgnoresNode := root.Child(calcTalliesWithIgnores)

	// parameters depend on tallies.
	paramsNode := pdag.NewNode(calcParamsets, tallyNode, tallyIgnoresNode)
	pdag.NewNode(writeKnownHashesList, tallyIgnoresNode)

	// summaries depend on tallies and blamer.
	summaryNode := pdag.NewNode(calcSummaries, tallyNode, blamerNode)
	summaryIgnoresNode := pdag.NewNode(calcSummariesWithIgnores, tallyIgnoresNode, blamerNode)

	// The warmer depends on summaries.
	pdag.NewNode(runWarmer, summaryNode, summaryIgnoresNode)

	// Set the result on the Indexer instance, once summaries, parameters and writing
	// the hash files is done.
	pdag.NewNode(ret.setIndex, summaryNode, summaryIgnoresNode, paramsNode)

	ret.pipeline = root
	ret.indexTestsNode = indexTestsNode

	// Process the first tile and start the indexing process.
	return ret, ret.start(interval)
}

// GetIndex returns the current index, which is updated continously in the
// background. The returned instances of SearchIndex can be considered immutable
// and is not going to change. It should be used to handle an entire request
// to provide consistent information.
func (ixr *Indexer) GetIndex() *SearchIndex {
	ixr.mutex.RLock()
	defer ixr.mutex.RUnlock()
	return ixr.lastIndex
}

// start builds the initial index and starts the background
// process to continously build indices.
func (ixr *Indexer) start(interval time.Duration) error {
	// Build the first index synchronously.
	tileStream := ixr.storages.GetTileStreamNow(interval)
	if err := ixr.indexTilePair(<-tileStream); err != nil {
		return err
	}

	// When the expecations change, update the blamer and its dependents.
	expCh := make(chan map[string]types.TestClassification)
	ixr.storages.EventBus.SubscribeAsync(expstorage.EV_EXPSTORAGE_CHANGED, func(e interface{}) {
		// Schedule the list of test names to be recalculated.
		expCh <- e.(map[string]types.TestClassification)
	})

	// Keep building indices as tiles become available and expecations change.
	go func() {
		var tilePair *types.TilePair
		for {
			testChanges := []map[string]types.TestClassification{}

			// See if there is a tile or changed tests.
			tilePair = nil
			select {
			// Catch a new tile.
			case tilePair = <-tileStream:

			// Catch any test changes.
			case tn := <-expCh:
				testChanges = append(testChanges, tn)
			}

			// Drain all the tests that might have changed in the meantime.
			done := false
			for !done {
				select {
				case tn := <-expCh:
					testChanges = append(testChanges, tn)
				default:
					done = true
				}
			}

			// If there is a tile, re-index everything and forget the
			// individual tests that changed.
			if tilePair != nil {
				if err := ixr.indexTilePair(tilePair); err != nil {
					sklog.Errorf("Unable to index tile: %s", err)
				}
			} else if len(testChanges) > 0 {
				// Only index the tests that have changed.
				ixr.indexTests(testChanges)
			}
		}
	}()

	return nil
}

// indexTilePair runs the given TilePair through the the indexing pipeline.
func (ixr *Indexer) indexTilePair(tilePair *types.TilePair) error {
	defer timer.New("indexTilePair").Stop()
	// Create a new index from the given tile.
	return ixr.pipeline.Trigger(newSearchIndex(ixr.storages, tilePair))
}

// indexTest creates an updated index by indexing the given list of expectation changes.
func (ixr *Indexer) indexTests(testChanges []map[string]types.TestClassification) {
	// Get all the testnames
	testNames := util.StringSet{}
	for _, testChange := range testChanges {
		for testName := range testChange {
			testNames[testName] = true
		}
	}

	defer timer.New("indexTests").Stop()
	lastIdx := ixr.GetIndex()
	newIdx := &SearchIndex{
		tilePair:             lastIdx.tilePair,
		tallies:              lastIdx.tallies,            // stay the same even if tests change.
		talliesWithIgnores:   lastIdx.talliesWithIgnores, // stay the same even if tests change.
		summaries:            lastIdx.summaries.Clone(),
		summariesWithIgnores: lastIdx.summariesWithIgnores.Clone(),
		paramsetSummary:      lastIdx.paramsetSummary,
		blamer:               blame.New(ixr.storages),
		warmer:               warmer.New(ixr.storages),
		testNames:            testNames.Keys(),
		storages:             lastIdx.storages,
	}

	if err := ixr.indexTestsNode.Trigger(newIdx); err != nil {
		sklog.Errorf("Error indexing tests: %v \n\n Got error: %s", testNames, err)
	}
}

// setIndex sets the lastIndex value at the very end of the pipeline.
func (ixr *Indexer) setIndex(state interface{}) error {
	newIndex := state.(*SearchIndex)
	ixr.mutex.Lock()
	defer ixr.mutex.Unlock()
	ixr.lastIndex = newIndex
	if ixr.storages.EventBus != nil {
		ixr.storages.EventBus.Publish(EV_INDEX_UPDATED, state, false)
	}
	return nil
}

// calcTallies is the pipeline function to calculate the tallies.
func calcTallies(state interface{}) error {
	idx := state.(*SearchIndex)
	idx.tallies.Calculate(idx.tilePair.Tile)
	return nil
}

// calcTalliesWithIgnores is the pipeline function to calculate the tallies for
// the tile that includes ignores.
func calcTalliesWithIgnores(state interface{}) error {
	idx := state.(*SearchIndex)
	idx.talliesWithIgnores.Calculate(idx.tilePair.TileWithIgnores)
	return nil
}

// calcSummaries is the pipeline function to calculate the summaries.
func calcSummaries(state interface{}) error {
	idx := state.(*SearchIndex)
	err := idx.summaries.Calculate(idx.tilePair.Tile, idx.testNames, idx.tallies, idx.blamer)
	return err
}

// calcSummariesWithIgnores is the pipeline function to calculate the summaries.
func calcSummariesWithIgnores(state interface{}) error {
	idx := state.(*SearchIndex)
	err := idx.summariesWithIgnores.Calculate(idx.tilePair.TileWithIgnores, idx.testNames, idx.talliesWithIgnores, idx.blamer)
	return err
}

// calcParamsets is the pipeline function to calculate the parameters.
func calcParamsets(state interface{}) error {
	idx := state.(*SearchIndex)
	idx.paramsetSummary.Calculate(idx.tilePair, idx.tallies, idx.talliesWithIgnores)
	return nil
}

// calcBlame is the pipeline function to calculate the blame.
func calcBlame(state interface{}) error {
	idx := state.(*SearchIndex)
	err := idx.blamer.Calculate(idx.tilePair.Tile)
	return err
}

func writeKnownHashesList(state interface{}) error {
	idx := state.(*SearchIndex)

	// Only write the hash file if a storage client is available.
	if idx.storages.GStorageClient == nil {
		return nil
	}

	// Trigger writing the hashes list.
	go func() {
		byTest := idx.TalliesByTest(true)
		unavailableDigests := idx.storages.DiffStore.UnavailableDigests()
		// Collect all hashes in the tile that haven't been marked as unavailable yet.
		hashes := util.StringSet{}
		for _, test := range byTest {
			for k := range test {
				if _, ok := unavailableDigests[k]; !ok {
					hashes[k] = true
				}
			}
		}

		// Make sure they all fetched already. This will block until all digests
		// are on disk or have failed to load repeatedly.
		idx.storages.DiffStore.WarmDigests(diff.PRIORITY_NOW, hashes.Keys(), true)
		unavailableDigests = idx.storages.DiffStore.UnavailableDigests()
		for h := range hashes {
			if _, ok := unavailableDigests[h]; ok {
				delete(hashes, h)
			}
		}

		// Keep track of the number of known hashes since this directly affects how
		// many images the bots have to upload.
		metrics2.GetInt64Metric(METRIC_KNOWN_HASHES).Update(int64(len(hashes)))
		if err := idx.storages.GStorageClient.WriteKnownDigests(hashes.Keys()); err != nil {
			sklog.Errorf("Error writing known digests list: %s", err)
		}
	}()
	return nil
}

// writeBaseline asynchronously writes the baseline to GCS.
func writeBaseline(state interface{}) error {
	idx := state.(*SearchIndex)

	// Only write the hash file if a storage client is available.
	if idx.storages.GStorageClient == nil {
		return nil
	}

	// Write the baseline asynchronously.
	go func() {
		exps, err := idx.storages.ExpectationsStore.Get()
		if err != nil {
			sklog.Errorf("Error retrieving expectations: %s", err)
			return
		}

		// Write the baseline to disk.
		baseLine := baseline.GetBaseline(exps, idx.GetTile(false))
		if err := idx.storages.GStorageClient.WriteBaseLine(baseLine); err != nil {
			sklog.Errorf("Error writing baseline to GCS: %s", err)
		}
	}()

	return nil
}

// runWamer is the pipeline function to run the wamer. It runs it
// asynchronously since its results are not relevant for the searchIndex.
func runWarmer(state interface{}) error {
	idx := state.(*SearchIndex)

	// TODO (stephana): Instead of warming everything we should warm non-ignored
	// traces with higher priority.
	go idx.warmer.Run(idx.tilePair.TileWithIgnores, idx.summariesWithIgnores, idx.talliesWithIgnores)
	return nil
}

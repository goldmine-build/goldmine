// paramsets keeps a running summary of paramsets per test, digest pair.
package paramsets

import (
	"go.skia.org/infra/go/paramtools"
	"go.skia.org/infra/go/tiling"
	"go.skia.org/infra/golden/go/digest_counter"
	"go.skia.org/infra/golden/go/shared"
	"go.skia.org/infra/golden/go/types"
)

// ParamSummary keep precalculated paramsets for each test, digest pair.
// It is considered immutable and thread-safe.
type ParamSummary interface {
	// Get returns the paramset for the given digest.
	Get(test types.TestName, digest types.Digest) paramtools.ParamSet

	// GetByTest returns the parameter sets organized by tests and digests.
	GetByTest() map[types.TestName]map[types.Digest]paramtools.ParamSet
}

// ParamSummaryImpl implements the ParamSummary interface.
type ParamSummaryImpl struct {
	byTest map[types.TestName]map[types.Digest]paramtools.ParamSet
}

// byTestForTile calculates all the paramsets from the given tile and tallies.
func byTestForTile(tile *tiling.Tile, digestCountsByTrace map[tiling.TraceID]digest_counter.DigestCount) map[types.TestName]map[types.Digest]paramtools.ParamSet {
	ret := map[types.TestName]map[types.Digest]paramtools.ParamSet{}

	for id, dc := range digestCountsByTrace {
		if tr, ok := tile.Traces[id]; ok {
			test := types.TestName(tr.Params()[types.PRIMARY_KEY_FIELD])
			for digest := range dc {
				if foundTest, ok := ret[test]; !ok {
					ret[test] = map[types.Digest]paramtools.ParamSet{
						digest: paramtools.NewParamSet(tr.Params()),
					}
				} else if foundDigest, ok := foundTest[digest]; !ok {
					foundTest[digest] = paramtools.NewParamSet(tr.Params())
				} else {
					foundDigest.AddParams(tr.Params())
				}
			}
		}
	}

	// Normalize the data so clients don't have to.
	for _, byDigest := range ret {
		for _, ps := range byDigest {
			ps.Normalize()
		}
	}
	return ret
}

// NewParamSummary creates a new ParamSummaryImpl.
func NewParamSummary(tile *tiling.Tile, dCounter digest_counter.DigestCounter) *ParamSummaryImpl {
	defer shared.NewMetricsTimer("param_summary_calculate").Stop()
	p := &ParamSummaryImpl{
		byTest: byTestForTile(tile, dCounter.ByTrace()),
	}
	return p
}

// Get implements the ParamSummaryImpl interface.
func (s *ParamSummaryImpl) Get(test types.TestName, digest types.Digest) paramtools.ParamSet {
	if foundTest, ok := s.byTest[test]; ok {
		return foundTest[digest]
	}
	return nil
}

// GetByTest implements the ParamSummaryImpl interface.
func (s *ParamSummaryImpl) GetByTest() map[types.TestName]map[types.Digest]paramtools.ParamSet {
	return s.byTest
}

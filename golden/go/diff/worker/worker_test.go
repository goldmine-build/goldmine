package worker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/paramtools"
	"go.skia.org/infra/go/repo_root"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/testutils/unittest"
	goldctl_mocks "go.skia.org/infra/gold-client/go/mocks"
	"go.skia.org/infra/golden/go/diff/mocks"
	"go.skia.org/infra/golden/go/sql"
	"go.skia.org/infra/golden/go/sql/databuilder"
	dks "go.skia.org/infra/golden/go/sql/datakitchensink"
	"go.skia.org/infra/golden/go/sql/schema"
	"go.skia.org/infra/golden/go/sql/sqltest"
	"go.skia.org/infra/golden/go/types"
)

func TestCalculateDiffs_NoExistingData_Success(t *testing.T) {
	unittest.LargeTest(t)

	fakeNow := time.Date(2021, time.February, 1, 1, 1, 1, 0, time.UTC)
	ctx := context.WithValue(context.Background(), NowSourceKey, mockTime(fakeNow))
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	waitForSystemTime()
	w := newWorkerUsingImagesFromKitchenSink(t, db)
	metricBefore := metrics2.GetCounter("diffcalculator_metricscalculated").Get()

	grouping := paramtools.Params{
		types.CorpusField:     "not used",
		types.PrimaryKeyField: "not used",
	}
	imagesToCalculateDiffsFor := []types.Digest{dks.DigestA01Pos, dks.DigestA02Pos, dks.DigestA04Unt, dks.DigestA05Unt}
	require.NoError(t, w.CalculateDiffs(ctx, grouping, imagesToCalculateDiffsFor))

	actualMetrics := getAllDiffMetricRows(t, db)
	assert.Equal(t, []schema.DiffMetricRow{
		expectedFromKS(t, dks.DigestA01Pos, dks.DigestA02Pos, fakeNow),
		expectedFromKS(t, dks.DigestA01Pos, dks.DigestA04Unt, fakeNow),
		expectedFromKS(t, dks.DigestA01Pos, dks.DigestA05Unt, fakeNow),

		expectedFromKS(t, dks.DigestA02Pos, dks.DigestA01Pos, fakeNow),
		expectedFromKS(t, dks.DigestA02Pos, dks.DigestA04Unt, fakeNow),
		expectedFromKS(t, dks.DigestA02Pos, dks.DigestA05Unt, fakeNow),

		expectedFromKS(t, dks.DigestA04Unt, dks.DigestA01Pos, fakeNow),
		expectedFromKS(t, dks.DigestA04Unt, dks.DigestA02Pos, fakeNow),
		expectedFromKS(t, dks.DigestA04Unt, dks.DigestA05Unt, fakeNow),

		expectedFromKS(t, dks.DigestA05Unt, dks.DigestA01Pos, fakeNow),
		expectedFromKS(t, dks.DigestA05Unt, dks.DigestA02Pos, fakeNow),
		expectedFromKS(t, dks.DigestA05Unt, dks.DigestA04Unt, fakeNow),
	}, actualMetrics)
	assert.Empty(t, getAllProblemImageRows(t, db))

	// 6 distinct pairs of images were compared (left-right order does not matter).
	assert.Equal(t, metricBefore+6, metrics2.GetCounter("diffcalculator_metricscalculated").Get())
}

func TestCalculateDiffs_MultipleBatches_Success(t *testing.T) {
	unittest.LargeTest(t)

	fakeNow := time.Date(2021, time.February, 1, 1, 1, 1, 0, time.UTC)
	ctx := context.WithValue(context.Background(), NowSourceKey, mockTime(fakeNow))
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	waitForSystemTime()
	// all images loaded will be the same, so diff values are all zeros.
	w := newWorkerUsingBlankImages(t, db)

	grouping := paramtools.Params{
		types.CorpusField:     "not used",
		types.PrimaryKeyField: "not used",
	}
	var imagesToCalculateDiffsFor []types.Digest
	// 16 digests should result in 32 * 32 - 32 diffs (a 32 by 32 square with the diagonal removed)
	// This is more than the batchSize in writeMetrics
	for i := 0; i < 32; i++ {
		d := fmt.Sprintf("%032d", i)
		imagesToCalculateDiffsFor = append(imagesToCalculateDiffsFor, types.Digest(d))
	}
	require.NoError(t, w.CalculateDiffs(ctx, grouping, imagesToCalculateDiffsFor))

	row := db.QueryRow(ctx, `SELECT count(*) FROM DiffMetrics`)
	count := 0
	require.NoError(t, row.Scan(&count))
	assert.Equal(t, 32*31, count)
	assert.Empty(t, getAllProblemImageRows(t, db))
}

func TestCalculateDiffs_ExistingMetrics_NoExistingTiledTraces_Success(t *testing.T) {
	unittest.LargeTest(t)

	fakeNow := time.Date(2021, time.February, 1, 1, 1, 1, 0, time.UTC)
	ctx := context.WithValue(context.Background(), NowSourceKey, mockTime(fakeNow))
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	existingData := schema.Tables{DiffMetrics: []schema.DiffMetricRow{
		sentinelMetricRow(dks.DigestA01Pos, dks.DigestA02Pos), // should not be recomputed
		sentinelMetricRow(dks.DigestA02Pos, dks.DigestA01Pos), // should not be recomputed
		sentinelMetricRow(dks.DigestBlank, dks.DigestA04Unt),  // should not impact computation
		sentinelMetricRow(dks.DigestA04Unt, dks.DigestBlank),  // should not impact computation
	}}
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))
	waitForSystemTime()
	w := newWorkerUsingImagesFromKitchenSink(t, db)
	metricBefore := metrics2.GetCounter("diffcalculator_metricscalculated").Get()

	grouping := paramtools.Params{
		types.CorpusField:     "not used",
		types.PrimaryKeyField: "not used",
	}
	imagesToCalculateDiffsFor := []types.Digest{dks.DigestA01Pos, dks.DigestA02Pos, dks.DigestA04Unt, dks.DigestA05Unt}
	require.NoError(t, w.CalculateDiffs(ctx, grouping, imagesToCalculateDiffsFor))

	actualMetrics := getAllDiffMetricRows(t, db)
	assert.Equal(t, []schema.DiffMetricRow{
		sentinelMetricRow(dks.DigestBlank, dks.DigestA04Unt),

		sentinelMetricRow(dks.DigestA01Pos, dks.DigestA02Pos),
		expectedFromKS(t, dks.DigestA01Pos, dks.DigestA04Unt, fakeNow),
		expectedFromKS(t, dks.DigestA01Pos, dks.DigestA05Unt, fakeNow),

		sentinelMetricRow(dks.DigestA02Pos, dks.DigestA01Pos),
		expectedFromKS(t, dks.DigestA02Pos, dks.DigestA04Unt, fakeNow),
		expectedFromKS(t, dks.DigestA02Pos, dks.DigestA05Unt, fakeNow),

		sentinelMetricRow(dks.DigestA04Unt, dks.DigestBlank),
		expectedFromKS(t, dks.DigestA04Unt, dks.DigestA01Pos, fakeNow),
		expectedFromKS(t, dks.DigestA04Unt, dks.DigestA02Pos, fakeNow),
		expectedFromKS(t, dks.DigestA04Unt, dks.DigestA05Unt, fakeNow),

		expectedFromKS(t, dks.DigestA05Unt, dks.DigestA01Pos, fakeNow),
		expectedFromKS(t, dks.DigestA05Unt, dks.DigestA02Pos, fakeNow),
		expectedFromKS(t, dks.DigestA05Unt, dks.DigestA04Unt, fakeNow),
	}, actualMetrics)
	assert.Empty(t, getAllProblemImageRows(t, db))

	// 5 distinct pairs of images were compared (left-right order does not matter).
	assert.Equal(t, metricBefore+5, metrics2.GetCounter("diffcalculator_metricscalculated").Get())
}

func TestCalculateDiffs_NoNewMetrics_Success(t *testing.T) {
	unittest.LargeTest(t)

	fakeNow := time.Date(2021, time.February, 1, 1, 1, 1, 0, time.UTC)
	ctx := context.WithValue(context.Background(), NowSourceKey, mockTime(fakeNow))
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	existingData := schema.Tables{DiffMetrics: []schema.DiffMetricRow{
		sentinelMetricRow(dks.DigestA01Pos, dks.DigestA02Pos), // should not be recomputed
		sentinelMetricRow(dks.DigestA02Pos, dks.DigestA01Pos), // should not be recomputed
	}}
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))
	waitForSystemTime()
	w := newWorkerUsingImagesFromKitchenSink(t, db)
	metricBefore := metrics2.GetCounter("diffcalculator_metricscalculated").Get()

	grouping := paramtools.Params{
		types.CorpusField:     "not used",
		types.PrimaryKeyField: "not used",
	}
	imagesToCalculateDiffsFor := []types.Digest{dks.DigestA01Pos, dks.DigestA02Pos}
	require.NoError(t, w.CalculateDiffs(ctx, grouping, imagesToCalculateDiffsFor))

	actualMetrics := getAllDiffMetricRows(t, db)
	assert.Equal(t, []schema.DiffMetricRow{
		sentinelMetricRow(dks.DigestA01Pos, dks.DigestA02Pos),
		sentinelMetricRow(dks.DigestA02Pos, dks.DigestA01Pos),
	}, actualMetrics)
	assert.Empty(t, getAllProblemImageRows(t, db))
	// no pairs of images were compared.
	assert.Equal(t, metricBefore, metrics2.GetCounter("diffcalculator_metricscalculated").Get())
}

func TestCalculateDiffs_ReadFromPrimaryBranch_Success(t *testing.T) {
	unittest.LargeTest(t)

	fakeNow := time.Date(2021, time.February, 1, 1, 1, 1, 0, time.UTC)
	ctx := context.WithValue(context.Background(), NowSourceKey, mockTime(fakeNow))
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	existingData := dks.Build()
	// Remove existing diffs, so the ones for triangle test can be recomputed.
	existingData.DiffMetrics = nil
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingData))
	waitForSystemTime()
	w := newWorkerUsingImagesFromKitchenSink(t, db)

	grouping := paramtools.Params{
		types.CorpusField:     dks.CornersCorpus,
		types.PrimaryKeyField: dks.TriangleTest,
	}
	require.NoError(t, w.CalculateDiffs(ctx, grouping, nil))

	actualMetrics := getAllDiffMetricRows(t, db)
	assert.Equal(t, []schema.DiffMetricRow{
		expectedFromKS(t, dks.DigestBlank, dks.DigestB01Pos, fakeNow),
		expectedFromKS(t, dks.DigestBlank, dks.DigestB02Pos, fakeNow),
		expectedFromKS(t, dks.DigestBlank, dks.DigestB03Neg, fakeNow),
		expectedFromKS(t, dks.DigestBlank, dks.DigestB04Neg, fakeNow),

		expectedFromKS(t, dks.DigestB01Pos, dks.DigestBlank, fakeNow),
		expectedFromKS(t, dks.DigestB01Pos, dks.DigestB02Pos, fakeNow),
		expectedFromKS(t, dks.DigestB01Pos, dks.DigestB03Neg, fakeNow),
		expectedFromKS(t, dks.DigestB01Pos, dks.DigestB04Neg, fakeNow),

		expectedFromKS(t, dks.DigestB02Pos, dks.DigestBlank, fakeNow),
		expectedFromKS(t, dks.DigestB02Pos, dks.DigestB01Pos, fakeNow),
		expectedFromKS(t, dks.DigestB02Pos, dks.DigestB03Neg, fakeNow),
		expectedFromKS(t, dks.DigestB02Pos, dks.DigestB04Neg, fakeNow),

		expectedFromKS(t, dks.DigestB03Neg, dks.DigestBlank, fakeNow),
		expectedFromKS(t, dks.DigestB03Neg, dks.DigestB01Pos, fakeNow),
		expectedFromKS(t, dks.DigestB03Neg, dks.DigestB02Pos, fakeNow),
		expectedFromKS(t, dks.DigestB03Neg, dks.DigestB04Neg, fakeNow),

		expectedFromKS(t, dks.DigestB04Neg, dks.DigestBlank, fakeNow),
		expectedFromKS(t, dks.DigestB04Neg, dks.DigestB01Pos, fakeNow),
		expectedFromKS(t, dks.DigestB04Neg, dks.DigestB02Pos, fakeNow),
		expectedFromKS(t, dks.DigestB04Neg, dks.DigestB03Neg, fakeNow),
	}, actualMetrics)
	assert.Empty(t, getAllProblemImageRows(t, db))
}

func TestCalculateDiffs_ReadFromPrimaryBranch_SparseData_Success(t *testing.T) {
	unittest.LargeTest(t)

	fakeNow := time.Date(2021, time.February, 1, 1, 1, 1, 0, time.UTC)
	ctx := context.WithValue(context.Background(), NowSourceKey, mockTime(fakeNow))
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	// Only C03, C04, C05 will be in the last two tiles for this data.
	sparseData := makeSparseData()
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, sparseData))
	waitForSystemTime()
	w := newWorkerUsingImagesFromKitchenSink(t, db)

	grouping := paramtools.Params{
		types.CorpusField:     dks.RoundCorpus,
		types.PrimaryKeyField: dks.CircleTest,
	}
	require.NoError(t, w.CalculateDiffs(ctx, grouping, []types.Digest{dks.DigestC06Pos_CL}))

	actualMetrics := getAllDiffMetricRows(t, db)
	assert.Equal(t, []schema.DiffMetricRow{
		expectedFromKS(t, dks.DigestC03Unt, dks.DigestC04Unt, fakeNow),
		expectedFromKS(t, dks.DigestC03Unt, dks.DigestC05Unt, fakeNow),
		expectedFromKS(t, dks.DigestC03Unt, dks.DigestC06Pos_CL, fakeNow),

		expectedFromKS(t, dks.DigestC04Unt, dks.DigestC03Unt, fakeNow),
		expectedFromKS(t, dks.DigestC04Unt, dks.DigestC05Unt, fakeNow),
		expectedFromKS(t, dks.DigestC04Unt, dks.DigestC06Pos_CL, fakeNow),

		expectedFromKS(t, dks.DigestC05Unt, dks.DigestC03Unt, fakeNow),
		expectedFromKS(t, dks.DigestC05Unt, dks.DigestC04Unt, fakeNow),
		expectedFromKS(t, dks.DigestC05Unt, dks.DigestC06Pos_CL, fakeNow),

		expectedFromKS(t, dks.DigestC06Pos_CL, dks.DigestC03Unt, fakeNow),
		expectedFromKS(t, dks.DigestC06Pos_CL, dks.DigestC04Unt, fakeNow),
		expectedFromKS(t, dks.DigestC06Pos_CL, dks.DigestC05Unt, fakeNow),
	}, actualMetrics)
	assert.Empty(t, getAllProblemImageRows(t, db))
}

func TestCalculateDiffs_ImageNotFound_PartialData(t *testing.T) {
	unittest.LargeTest(t)

	fakeNow := time.Date(2021, time.February, 1, 1, 1, 1, 0, time.UTC)
	ctx := context.WithValue(context.Background(), NowSourceKey, mockTime(fakeNow))
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	waitForSystemTime()
	metricBefore := metrics2.GetCounter("diffcalculator_metricscalculated").Get()

	// Set up an image source that can download A01 and A02, but returns error on A04
	b01, err := ioutil.ReadFile(filepath.Join(kitchenSinkRoot(t), string(dks.DigestA01Pos)+".png"))
	require.NoError(t, err)
	b02, err := ioutil.ReadFile(filepath.Join(kitchenSinkRoot(t), string(dks.DigestA02Pos)+".png"))
	require.NoError(t, err)
	mis := &mocks.ImageSource{}
	mis.On("GetImage", testutils.AnyContext, dks.DigestA01Pos).Return(b01, nil)
	mis.On("GetImage", testutils.AnyContext, dks.DigestA02Pos).Return(b02, nil)
	mis.On("GetImage", testutils.AnyContext, dks.DigestA04Unt).Return(nil, errors.New("not found"))

	w := New(db, mis, 2)

	grouping := paramtools.Params{
		types.CorpusField:     "not used",
		types.PrimaryKeyField: "not used",
	}
	imagesToCalculateDiffsFor := []types.Digest{dks.DigestA01Pos, dks.DigestA02Pos, dks.DigestA04Unt}
	require.NoError(t, w.CalculateDiffs(ctx, grouping, imagesToCalculateDiffsFor))

	actualMetrics := getAllDiffMetricRows(t, db)
	// We should see partial success
	assert.Equal(t, []schema.DiffMetricRow{
		expectedFromKS(t, dks.DigestA01Pos, dks.DigestA02Pos, fakeNow),
		expectedFromKS(t, dks.DigestA02Pos, dks.DigestA01Pos, fakeNow),
	}, actualMetrics)

	actualProblemImageRows := getAllProblemImageRows(t, db)
	require.Len(t, actualProblemImageRows, 1)
	problem := actualProblemImageRows[0]
	assert.Equal(t, string(dks.DigestA04Unt), problem.Digest)
	// could be more than 1 because of multiple goroutines running
	assert.True(t, problem.NumErrors >= 1)
	assert.Contains(t, problem.LatestError, "not found")
	assert.Equal(t, fakeNow, problem.ErrorTS)

	// One pair of images was compared successfully.
	assert.Equal(t, metricBefore+1, metrics2.GetCounter("diffcalculator_metricscalculated").Get())
}

func TestCalculateDiffs_CorruptedImage_PartialData(t *testing.T) {
	unittest.LargeTest(t)

	fakeNow := time.Date(2021, time.February, 2, 2, 2, 2, 0, time.UTC)
	ctx := context.WithValue(context.Background(), NowSourceKey, mockTime(fakeNow))
	db := sqltest.NewCockroachDBForTestsWithProductionSchema(ctx, t)
	// Write a sentinel error for A04. We expect this to be updated.
	existingProblem := schema.Tables{ProblemImages: []schema.ProblemImageRow{{
		Digest:      string(dks.DigestA04Unt),
		NumErrors:   100,
		LatestError: "not found",
		ErrorTS:     time.Date(2020, time.January, 1, 1, 1, 1, 0, time.UTC),
	}}}
	require.NoError(t, sqltest.BulkInsertDataTables(ctx, db, existingProblem))
	waitForSystemTime()

	// Set up an image source that can download A01 and A02, but invalid PNG data for A04
	b01, err := ioutil.ReadFile(filepath.Join(kitchenSinkRoot(t), string(dks.DigestA01Pos)+".png"))
	require.NoError(t, err)
	b02, err := ioutil.ReadFile(filepath.Join(kitchenSinkRoot(t), string(dks.DigestA02Pos)+".png"))
	require.NoError(t, err)
	mis := &mocks.ImageSource{}
	mis.On("GetImage", testutils.AnyContext, dks.DigestA01Pos).Return(b01, nil)
	mis.On("GetImage", testutils.AnyContext, dks.DigestA02Pos).Return(b02, nil)
	mis.On("GetImage", testutils.AnyContext, dks.DigestA04Unt).Return([]byte(`not a png`), nil)

	w := New(db, mis, 2)

	grouping := paramtools.Params{
		types.CorpusField:     "not used",
		types.PrimaryKeyField: "not used",
	}
	imagesToCalculateDiffsFor := []types.Digest{dks.DigestA01Pos, dks.DigestA02Pos, dks.DigestA04Unt}
	require.NoError(t, w.CalculateDiffs(ctx, grouping, imagesToCalculateDiffsFor))

	actualMetrics := getAllDiffMetricRows(t, db)
	// We should see partial success
	assert.Equal(t, []schema.DiffMetricRow{
		expectedFromKS(t, dks.DigestA01Pos, dks.DigestA02Pos, fakeNow),
		expectedFromKS(t, dks.DigestA02Pos, dks.DigestA01Pos, fakeNow),
	}, actualMetrics)

	actualProblemImageRows := getAllProblemImageRows(t, db)
	require.Len(t, actualProblemImageRows, 1)
	problem := actualProblemImageRows[0]
	assert.Equal(t, string(dks.DigestA04Unt), problem.Digest)
	// The sentinel value is 100. This should be greater than that because of the new error.
	assert.True(t, problem.NumErrors >= 101)
	assert.Contains(t, problem.LatestError, "png: invalid format: not a PNG file")
	assert.Equal(t, fakeNow, problem.ErrorTS)
}

func getAllDiffMetricRows(t *testing.T, db *pgxpool.Pool) []schema.DiffMetricRow {
	rows, err := db.Query(context.Background(), `SELECT * FROM DiffMetrics ORDER BY left_digest, right_digest`)
	require.NoError(t, err)
	defer rows.Close()
	var actualMetrics []schema.DiffMetricRow
	for rows.Next() {
		var m schema.DiffMetricRow
		require.NoError(t, rows.Scan(&m.LeftDigest, &m.RightDigest, &m.NumPixelsDiff, &m.PercentPixelsDiff,
			&m.MaxRGBADiffs, &m.MaxChannelDiff, &m.CombinedMetric, &m.DimensionsDiffer, &m.Timestamp))
		m.Timestamp = m.Timestamp.UTC()
		actualMetrics = append(actualMetrics, m)
		// spot check that we handle arrays correctly.
		assert.NotEqual(t, [4]int{0, 0, 0, 0}, m.MaxRGBADiffs)
	}
	return actualMetrics
}

func getAllProblemImageRows(t *testing.T, db *pgxpool.Pool) []schema.ProblemImageRow {
	rows, err := db.Query(context.Background(), `SELECT * FROM ProblemImages ORDER BY digest`)
	require.NoError(t, err)
	defer rows.Close()
	var actualRows []schema.ProblemImageRow
	for rows.Next() {
		var r schema.ProblemImageRow
		require.NoError(t, rows.Scan(&r.Digest, &r.NumErrors, &r.LatestError, &r.ErrorTS))
		r.ErrorTS = r.ErrorTS.UTC()
		actualRows = append(actualRows, r)
	}
	return actualRows
}

func makeSparseData() schema.Tables {
	b := databuilder.TablesBuilder{}
	// Make a few commits with data across several tiles (tile width == 100)
	b.CommitsWithData().
		Insert(337, "whomever", "commit 337", "2020-12-01T00:00:01Z").
		Insert(437, "whomever", "commit 437", "2020-12-01T00:00:02Z").
		Insert(537, "whomever", "commit 537", "2020-12-01T00:00:03Z").
		Insert(637, "whomever", "commit 637", "2020-12-01T00:00:04Z").
		Insert(687, "whomever", "commit 687", "2020-12-01T00:00:05Z")
	// Then add a bunch of empty commits after. These should not impact the latest commit/tile
	// with data.
	nd := b.CommitsWithNoData()
	for i := 688; i < 710; i++ {
		nd.Insert(i, "no data author", "no data "+strconv.Itoa(i), "2020-12-01T00:00:06Z")
	}

	b.SetDigests(map[rune]types.Digest{
		// All of these will be untriaged because diffs don't care about triage status
		'a': dks.DigestC01Pos,
		'b': dks.DigestC02Pos,
		'c': dks.DigestC03Unt,
		'd': dks.DigestC04Unt,
		'e': dks.DigestC05Unt,
	})
	b.SetGroupingKeys(types.CorpusField, types.PrimaryKeyField)

	b.AddTracesWithCommonKeys(paramtools.Params{
		types.CorpusField: dks.RoundCorpus,
	}).History("abcde").Keys([]paramtools.Params{{types.PrimaryKeyField: dks.CircleTest}}).
		OptionsAll(paramtools.Params{"ext": "png"}).
		IngestedFrom([]string{"file1", "file2", "file3", "file4", "file5"}, // not used in this test
			[]string{"2020-12-01T00:00:05Z", "2020-12-01T00:00:05Z", "2020-12-01T00:00:05Z", "2020-12-01T00:00:05Z", "2020-12-01T00:00:05Z"})

	rv := b.Build()
	rv.DiffMetrics = nil
	return rv
}

// sentinelMetricRow returns a DiffMetricRow with arbitrary data that should not change as a result
// of the test using it (DiffMetrics are immutable).
func sentinelMetricRow(left types.Digest, right types.Digest) schema.DiffMetricRow {
	return schema.DiffMetricRow{
		LeftDigest:        d(left),
		RightDigest:       d(right),
		NumPixelsDiff:     -1,
		PercentPixelsDiff: -1,
		MaxRGBADiffs:      [4]int{-1, -1, -1, -1},
		MaxChannelDiff:    -1,
		CombinedMetric:    -1,
		Timestamp:         time.Date(2020, time.March, 14, 15, 9, 26, 0, time.UTC),
	}
}

func d(d types.Digest) schema.DigestBytes {
	b, err := sql.DigestToBytes(d)
	if err != nil {
		panic(err)
	}
	return b
}

func kitchenSinkRoot(t *testing.T) string {
	root, err := repo_root.Get()
	if err != nil {
		require.NoError(t, err)
	}
	return filepath.Join(root, "golden", "go", "sql", "datakitchensink", "img")
}

func newWorkerUsingImagesFromKitchenSink(t *testing.T, db *pgxpool.Pool) *WorkerImpl {
	return New(db, &fsImageSource{root: kitchenSinkRoot(t)}, 2)
}

func newWorkerUsingBlankImages(t *testing.T, db *pgxpool.Pool) *WorkerImpl {
	infraRoot, err := repo_root.Get()
	require.NoError(t, err)
	blankImagePath := filepath.Join(infraRoot, "golden", "go", "sql", "datakitchensink", "img", string(dks.DigestBlank+".png"))
	return New(db, &fixedImageSource{img: blankImagePath}, 2)
}

var kitchenSinkData = dks.Build()

// expectedFromKS returns the computed diff metric from the kitchen sink data. It replaces the
// default timestamp with the provided timestamp.
func expectedFromKS(t *testing.T, left types.Digest, right types.Digest, ts time.Time) schema.DiffMetricRow {
	leftB := d(left)
	rightB := d(right)
	for _, row := range kitchenSinkData.DiffMetrics {
		if bytes.Equal(leftB, row.LeftDigest) && bytes.Equal(rightB, row.RightDigest) {
			row.Timestamp = ts
			return row
		}
	}
	require.Fail(t, "Could not find diff metrics for %s-%s", left, right)
	return schema.DiffMetricRow{}
}

// waitForSystemTime waits for a time greater than the duration mentioned in "AS OF SYSTEM TIME"
// clauses in queries. This way, the queries will be accurate.
func waitForSystemTime() {
	time.Sleep(150 * time.Millisecond)
}

func mockTime(ts time.Time) NowSource {
	mt := goldctl_mocks.NowSource{}
	mt.On("Now").Return(ts)
	return &mt
}

// fsImageSource returns an image from the local file system, looking in a given root directory.
type fsImageSource struct {
	root string
}

func (f fsImageSource) GetImage(_ context.Context, digest types.Digest) ([]byte, error) {
	p := filepath.Join(f.root, string(digest)+".png")
	return ioutil.ReadFile(p)
}

// fixedImageSource returns the same bytes for every image
type fixedImageSource struct {
	img string
}

func (f fixedImageSource) GetImage(_ context.Context, _ types.Digest) ([]byte, error) {
	return ioutil.ReadFile(f.img)
}

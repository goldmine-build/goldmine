package expstorage

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"

	assert "github.com/stretchr/testify/require"
	"go.skia.org/infra/go/database/testutil"

	"go.skia.org/infra/go/ds"
	ds_testutil "go.skia.org/infra/go/ds/testutil"
	"go.skia.org/infra/go/eventbus"
	"go.skia.org/infra/go/testutils"
	"go.skia.org/infra/go/util"
	"go.skia.org/infra/golden/go/db"
	"go.skia.org/infra/golden/go/types"
)

func TestMySQLExpectationsStore(t *testing.T) {
	testutils.LargeTest(t)
	// Set up the test database.
	testDb := testutil.SetupMySQLTestDatabase(t, db.MigrationSteps())
	defer testDb.Close(t)

	conf := testutil.LocalTestDatabaseConfig(db.MigrationSteps())
	vdb, err := conf.NewVersionedDB()
	assert.NoError(t, err)

	// Test the MySQL backed store
	sqlStore := NewSQLExpectationStore(vdb)
	testExpectationStore(t, sqlStore, nil, 0, EV_EXPSTORAGE_CHANGED)

	// Test the caching version of the MySQL store.
	eventBus := eventbus.New()
	cachingStore := NewCachingExpectationStore(sqlStore, eventBus)
	testExpectationStore(t, cachingStore, eventBus, 0, EV_EXPSTORAGE_CHANGED)
}

func TestMasterCloudExpectationsStore(t *testing.T) {
	testutils.LargeTest(t)

	cleanup := initDS(t)
	defer cleanup()

	// Test the DS backed store for master.
	masterEventBus := eventbus.New()
	cloudStore, _, err := NewCloudExpectationsStore(ds.DS, masterEventBus)
	assert.NoError(t, err)
	testExpectationStore(t, cloudStore, masterEventBus, 0, EV_EXPSTORAGE_CHANGED)
}

func TestCachingCloudExpectationsStore(t *testing.T) {
	testutils.LargeTest(t)

	cleanup := initDS(t)
	defer cleanup()

	// Test the caching version of the DS store.
	cachingEventBus := eventbus.New()
	cloudStore, _, err := NewCloudExpectationsStore(ds.DS, nil)
	assert.NoError(t, err)
	cachingStore := NewCachingExpectationStore(cloudStore, cachingEventBus)
	testExpectationStore(t, cachingStore, cachingEventBus, 0, EV_EXPSTORAGE_CHANGED)
}

func TestIssueCloudExpectationsStore(t *testing.T) {
	testutils.LargeTest(t)

	cleanup := initDS(t)
	defer cleanup()

	// Test the expectation store for an individual issue.
	masterEventBus := eventbus.New()
	_, issueStoreFactory, err := NewCloudExpectationsStore(ds.DS, masterEventBus)
	assert.NoError(t, err)
	issueID := int64(1234567)
	issueStore := issueStoreFactory(issueID)
	testExpectationStore(t, issueStore, masterEventBus, issueID, EV_TRYJOB_EXP_CHANGED)
}

// initDS initializes the datastore for testing.
func initDS(t *testing.T) func() {
	return ds_testutil.InitDatastore(t,
		ds.MASTER_EXP_CHANGE,
		ds.MASTER_TEST_DIGEST_EXP,
		ds.TRYJOB_EXP_CHANGE,
		ds.TRYJOB_TEST_DIGEST_EXP,
		ds.HELPER_RECENT_KEYS)
}

const hexLetters = "0123456789abcdef"
const md5Length = 32

func randomDigest() string {
	ret := make([]byte, md5Length, md5Length)
	for i := 0; i < md5Length; i++ {
		ret[i] = hexLetters[rand.Intn(len(hexLetters))]
	}
	return string(ret)
}

func TestBigSQLChange(t *testing.T) {
	testutils.LargeTest(t)

	// Set up the test database.
	testDb := testutil.SetupMySQLTestDatabase(t, db.MigrationSteps())
	defer testDb.Close(t)

	conf := testutil.LocalTestDatabaseConfig(db.MigrationSteps())
	vdb, err := conf.NewVersionedDB()
	assert.NoError(t, err)

	// Test the MySQL backed store
	sqlStore := NewSQLExpectationStore(vdb)

	nDigests := 25313
	labels := []types.Label{types.POSITIVE, types.NEGATIVE, types.UNTRIAGED}
	digests := make(types.TestClassification, nDigests)
	for i := 0; i < nDigests; i++ {
		digests[randomDigest()] = labels[rand.Intn(len(labels))]
	}

	bigChange := map[string]types.TestClassification{
		"mytest": digests,
	}

	assert.NoError(t, sqlStore.AddChange(bigChange, "user-99"))
	exp, err := sqlStore.Get()
	assert.NoError(t, err)
	assert.Equal(t, bigChange, exp.Tests)
}

// Test against the expectation store interface.
func testExpectationStore(t *testing.T, store ExpectationsStore, eventBus eventbus.EventBus, issueID int64, eventType string) {
	// Get the initial log size. This is necessary because we
	// call this function multiple times with the same underlying
	// SQLExpectationStore.
	initialLogRecs, initialLogTotal, err := store.QueryLog(0, 100, true)
	assert.NoError(t, err)
	initialLogRecsLen := len(initialLogRecs)

	// If we have an event bus then keep gathering events.
	callbackCh := make(chan []string, 3)
	if eventBus != nil {
		eventBus.SubscribeAsync(eventType, func(e interface{}) {
			evData := e.(*EventExpectationChange)
			if (issueID > 0) && (evData.IssueID != issueID) {
				return
			}

			testNames := make([]string, 0, len(evData.TestChanges))
			for testName := range evData.TestChanges {
				testNames = append(testNames, testName)
			}
			sort.Strings(testNames)
			callbackCh <- testNames
		})
	}

	TEST_1, TEST_2 := "test1", "test2"

	// digests
	DIGEST_11, DIGEST_12 := "d11", "d12"
	DIGEST_21, DIGEST_22 := "d21", "d22"

	expChange_1 := map[string]types.TestClassification{
		TEST_1: {
			DIGEST_11: types.POSITIVE,
			DIGEST_12: types.NEGATIVE,
		},
		TEST_2: {
			DIGEST_21: types.POSITIVE,
			DIGEST_22: types.NEGATIVE,
		},
	}
	logEntry_1 := []*TriageDetail{
		{TEST_1, DIGEST_11, "positive"},
		{TEST_1, DIGEST_12, "negative"},
		{TEST_2, DIGEST_21, "positive"},
		{TEST_2, DIGEST_22, "negative"},
	}

	assert.NoError(t, store.AddChange(expChange_1, "user-0"))
	if eventBus != nil {
		eventBus.(*eventbus.MemEventBus).Wait(eventType)
		assert.Equal(t, 1, len(callbackCh))
		assert.Equal(t, []string{TEST_1, TEST_2}, <-callbackCh)
	}

	foundExps, err := store.Get()
	assert.NoError(t, err)

	assert.Equal(t, expChange_1, foundExps.Tests)
	assert.False(t, &expChange_1 == &foundExps.Tests)
	checkLogEntry(t, store, expChange_1)

	// Update digests.
	expChange_2 := map[string]types.TestClassification{
		TEST_1: {
			DIGEST_11: types.NEGATIVE,
		},
		TEST_2: {
			DIGEST_22: types.UNTRIAGED,
		},
	}
	logEntry_2 := []*TriageDetail{
		{TEST_1, DIGEST_11, "negative"},
		{TEST_2, DIGEST_22, "untriaged"},
	}

	assert.NoError(t, store.AddChange(expChange_2, "user-1"))
	if eventBus != nil {
		eventBus.(*eventbus.MemEventBus).Wait(eventType)
		assert.Equal(t, 1, len(callbackCh))
		assert.Equal(t, []string{TEST_1, TEST_2}, <-callbackCh)
	}

	foundExps, err = store.Get()
	assert.NoError(t, err)
	assert.Equal(t, types.NEGATIVE, foundExps.Tests[TEST_1][DIGEST_11])
	assert.Equal(t, types.UNTRIAGED, foundExps.Tests[TEST_2][DIGEST_22])
	checkLogEntry(t, store, expChange_2)

	// Send empty changes to test the event bus.
	emptyChanges := map[string]types.TestClassification{}
	assert.NoError(t, store.AddChange(emptyChanges, "user-2"))
	if eventBus != nil {
		eventBus.(*eventbus.MemEventBus).Wait(eventType)
		assert.Equal(t, 1, len(callbackCh))
		assert.Equal(t, []string{}, <-callbackCh)
	}
	checkLogEntry(t, store, emptyChanges)

	foundExps, err = store.Get()
	assert.NoError(t, err)

	// Remove digests.
	removeDigests_1 := map[string]types.TestClassification{
		TEST_1: {DIGEST_11: types.UNTRIAGED},
		TEST_2: {DIGEST_22: types.UNTRIAGED},
	}

	assert.NoError(t, store.removeChange(removeDigests_1))
	if eventBus != nil {
		eventBus.(*eventbus.MemEventBus).Wait(eventType)
		assert.Equal(t, 1, len(callbackCh))
		assert.Equal(t, []string{TEST_1, TEST_2}, <-callbackCh)
	}

	foundExps, err = store.Get()
	assert.NoError(t, err)

	assert.Equal(t, types.TestClassification(map[string]types.Label{DIGEST_12: types.NEGATIVE}), foundExps.Tests[TEST_1])
	assert.Equal(t, types.TestClassification(map[string]types.Label{DIGEST_21: types.POSITIVE}), foundExps.Tests[TEST_2])

	removeDigests_2 := map[string]types.TestClassification{TEST_1: {DIGEST_12: types.UNTRIAGED}}
	assert.NoError(t, store.removeChange(removeDigests_2))
	if eventBus != nil {
		eventBus.(*eventbus.MemEventBus).Wait(eventType)
		assert.Equal(t, 1, len(callbackCh))
		assert.Equal(t, []string{TEST_1}, <-callbackCh)
	}

	foundExps, err = store.Get()
	assert.NoError(t, err)
	assert.Equal(t, 1, len(foundExps.Tests))

	assert.NoError(t, store.removeChange(map[string]types.TestClassification{}))
	if eventBus != nil {
		eventBus.(*eventbus.MemEventBus).Wait(eventType)
		assert.Equal(t, 1, len(callbackCh))
		assert.Equal(t, []string{}, <-callbackCh)
	}

	// Make sure we added the correct number of triage log entries.
	addedRecs := 3
	logEntries, total, err := store.QueryLog(0, 5, true)
	assert.NoError(t, err)
	assert.Equal(t, addedRecs+initialLogTotal, total)
	assert.Equal(t, util.MinInt(addedRecs+initialLogRecsLen, 5), len(logEntries))
	lastRec := logEntries[0]
	secondToLastRec := logEntries[1]

	assert.Equal(t, 0, len(logEntries[0].Details))
	assert.Equal(t, logEntry_2, logEntries[1].Details)
	assert.Equal(t, logEntry_1, logEntries[2].Details)

	logEntries, total, err = store.QueryLog(100, 5, true)
	assert.NoError(t, err)
	assert.Equal(t, addedRecs+initialLogTotal, total)
	assert.Equal(t, 0, len(logEntries))

	// Undo the latest version and make sure the corresponding record is correct.
	changes, err := store.UndoChange(int64(lastRec.ID), "user-1")
	assert.NoError(t, err)
	checkLogEntry(t, store, changes)

	changes, err = store.UndoChange(int64(secondToLastRec.ID), "user-1")
	assert.NoError(t, err)
	checkLogEntry(t, store, changes)

	addedRecs += 2
	logEntries, total, err = store.QueryLog(0, 2, true)
	assert.NoError(t, err)
	assert.Equal(t, addedRecs+initialLogTotal, total)
	assert.Equal(t, 0, len(logEntries[1].Details))
	assert.Equal(t, 2, len(logEntries[0].Details))

	foundExps, err = store.Get()
	assert.NoError(t, err)

	for testName, digests := range expChange_2 {
		for d := range digests {
			_, ok := foundExps.Tests[testName][d]
			assert.True(t, ok)
			assert.Equal(t, expChange_1[testName][d].String(), foundExps.Tests[testName][d].String())
		}
	}

	// Make sure undoing the previous undo causes an error.
	logEntries, _, err = store.QueryLog(0, 1, false)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(logEntries))
	_, err = store.UndoChange(int64(logEntries[0].ID), "user-1")
	assert.NotNil(t, err)

	// Make sure getExpectationsAt works correctly.
	sqlStore, ok := store.(*SQLExpectationsStore)
	if ok {
		logEntries, _, err = store.QueryLog(0, 100, true)
		assert.NoError(t, err)

		// Check the first addition.
		firstAdd := logEntries[len(logEntries)-1]
		secondAdd := logEntries[len(logEntries)-2]
		secondUndo := logEntries[len(logEntries)-5]

		checkExpectationsAt(t, sqlStore, firstAdd, "first")
		checkExpectationsAt(t, sqlStore, secondAdd, "second")
		checkExpectationsAt(t, sqlStore, secondUndo, "third")
	}
}

func checkExpectationsAt(t *testing.T, sqlStore *SQLExpectationsStore, changeInfo *TriageLogEntry, name string) {
	changeInfo.TS++
	changes, err := sqlStore.getExpectationsAt(changeInfo)
	assert.NoError(t, err)

	for _, d := range changeInfo.Details {
		assert.Equal(t, d.Label, changes[d.TestName][d.Digest].String(), fmt.Sprintf("Comparing: %s:  %s - %s", name, d.TestName, d.Digest))
	}
}

func checkLogEntry(t *testing.T, store ExpectationsStore, changes map[string]types.TestClassification) {
	logEntries, _, err := store.QueryLog(0, 1, true)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(logEntries))

	counter := 0
	for _, digests := range changes {
		counter += len(digests)
	}
	assert.Equal(t, counter, len(logEntries[0].Details))
	for _, d := range logEntries[0].Details {
		_, ok := changes[d.TestName][d.Digest]
		assert.True(t, ok)
		assert.Equal(t, changes[d.TestName][d.Digest].String(), d.Label)
	}
}

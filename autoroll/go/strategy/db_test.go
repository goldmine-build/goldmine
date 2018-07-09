package strategy

import (
	"io/ioutil"
	"os"
	"path"
	"testing"
	"time"

	assert "github.com/stretchr/testify/require"
	"go.skia.org/infra/go/deepequal"
	"go.skia.org/infra/go/testutils"
)

// testDB is a struct used for testing database operations.
type testDB struct {
	db  *db
	dir string
}

// newTestDB returns a testDB instance. The caller should call cleanup() on it
// when finished.
func newTestDB(t *testing.T) *testDB {
	tmpDir, err := ioutil.TempDir("", "test_autoroll_db_")
	assert.NoError(t, err)
	dbFile := path.Join(tmpDir, "test.db")
	d, err := openDB(dbFile)
	assert.NoError(t, err)
	return &testDB{
		db:  d,
		dir: tmpDir,
	}
}

// cleanup closes the database and removes the underlying temporary directory.
func (d *testDB) cleanup(t *testing.T) {
	assert.NoError(t, d.db.Close())
	assert.NoError(t, os.RemoveAll(d.dir))
}

// TestGetStrategyHistory verifies that we correctly track strategy history.
func TestGetStrategyHistory(t *testing.T) {
	testutils.MediumTest(t)
	d := newTestDB(t)
	defer d.cleanup(t)

	// Single strategy.
	m1 := &StrategyChange{
		Message:  "Starting!",
		Strategy: ROLL_STRATEGY_BATCH,
		Time:     time.Now().UTC(),
		User:     "me@google.com",
	}
	assert.NoError(t, d.db.SetStrategy(m1))
	history, err := d.db.GetStrategyHistory(10)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(history))
	deepequal.AssertDeepEqual(t, m1, history[0])

	// Add more strategies, ensuring that we retrieve them consistently.
	m2 := &StrategyChange{
		Message:  "Stoppit",
		Strategy: ROLL_STRATEGY_SINGLE,
		Time:     time.Now().UTC().Add(time.Minute),
		User:     "me@google.com",
	}
	m3 := &StrategyChange{
		Message:  "Dry run",
		Strategy: ROLL_STRATEGY_BATCH,
		Time:     time.Now().UTC().Add(2 * time.Minute),
		User:     "me@google.com",
	}
	m4 := &StrategyChange{
		Message:  "Dry run",
		Strategy: ROLL_STRATEGY_BATCH,
		Time:     time.Now().UTC().Add(3 * time.Minute),
		User:     "me@google.com",
	}

	assert.NoError(t, d.db.SetStrategy(m2))
	history, err = d.db.GetStrategyHistory(10)
	assert.NoError(t, err)
	deepequal.AssertDeepEqual(t, []*StrategyChange{m2, m1}, history)

	assert.NoError(t, d.db.SetStrategy(m3))
	history, err = d.db.GetStrategyHistory(10)
	assert.NoError(t, err)
	deepequal.AssertDeepEqual(t, []*StrategyChange{m3, m2, m1}, history)

	assert.NoError(t, d.db.SetStrategy(m4))
	history, err = d.db.GetStrategyHistory(10)
	assert.NoError(t, err)
	deepequal.AssertDeepEqual(t, []*StrategyChange{m4, m3, m2, m1}, history)

	// Only three changes?
	history, err = d.db.GetStrategyHistory(3)
	assert.NoError(t, err)
	deepequal.AssertDeepEqual(t, []*StrategyChange{m4, m3, m2}, history)
}

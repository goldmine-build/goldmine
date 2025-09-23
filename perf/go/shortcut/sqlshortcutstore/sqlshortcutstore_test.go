package sqlshortcutstore

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.goldmine.build/perf/go/shortcut/shortcuttest"
	"go.goldmine.build/perf/go/sql/sqltest"
)

func TestShortcutStore_CockroachDB(t *testing.T) {

	for name, subTest := range shortcuttest.SubTests {
		t.Run(name, func(t *testing.T) {
			db := sqltest.NewCockroachDBForTests(t, "shortcutstore")
			store, err := New(db)
			require.NoError(t, err)
			subTest(t, store)
		})
	}
}

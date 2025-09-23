package mem_gitstore

import (
	"testing"

	"go.goldmine.build/go/gitstore/shared_tests"
)

func TestMemGitStore(t *testing.T) {
	shared_tests.TestGitStore(t, New())
}

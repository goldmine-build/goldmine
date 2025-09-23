package ingester

import (
	"go.goldmine.build/perf/go/file"
	"go.goldmine.build/perf/go/trybot"
)

// Ingester converts file.Files into trybot.TryFiles as they arrive.
type Ingester interface {
	// Start a background Go routine that processes the incoming channel.
	Start(<-chan file.File) (<-chan trybot.TryFile, error)
}

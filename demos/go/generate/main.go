//go:generate bazelisk run --config=mayberemote //:go -- run . -o ../../modules/rpc_types.ts

package main

import (
	"flag"
	"io"

	"go.goldmine.build/demos/go/frontend"
	"go.goldmine.build/go/go2ts"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/go/util"
)

func main() {
	var outputPath = flag.String("o", "", "Path to the output TypeScript file.")
	flag.Parse()

	generator := go2ts.New()
	addTypes(generator)

	err := util.WithWriteFile(*outputPath, func(w io.Writer) error {
		return generator.Render(w)
	})
	if err != nil {
		sklog.Fatal(err)
	}
}

func addTypes(generator *go2ts.Go2TS) {
	generator.Add(frontend.Metadata{})
}

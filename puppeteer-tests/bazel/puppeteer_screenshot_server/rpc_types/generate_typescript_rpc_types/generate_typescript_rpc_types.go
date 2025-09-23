//go:generate bazelisk run --config=mayberemote //:go -- run . -o ../../../../modules/rpc_types.ts

package main

import (
	"flag"
	"io"

	"go.goldmine.build/go/go2ts"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/go/util"
	"go.goldmine.build/puppeteer-tests/bazel/puppeteer_screenshot_server/rpc_types"
)

func main() {
	var outputPath = flag.String("o", "", "Path to the output TypeScript file.")
	flag.Parse()

	generator := go2ts.New()
	generator.AddIgnoreNil(rpc_types.GetScreenshotsRPCResponse{})

	err := util.WithWriteFile(*outputPath, func(w io.Writer) error {
		return generator.Render(w)
	})
	if err != nil {
		sklog.Fatal(err)
	}
}

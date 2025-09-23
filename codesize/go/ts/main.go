//go:generate bazelisk run --config=mayberemote //:go -- run . -o ../../modules/rpc_types.ts

package main

import (
	"flag"
	"io"

	"go.goldmine.build/codesize/go/codesizeserver/rpc"
	"go.goldmine.build/go/go2ts"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/go/util"
)

func main() {
	var outputPath = flag.String("o", "", "Path to the output TypeScript file.")
	flag.Parse()

	generator := go2ts.New()
	generator.Add(rpc.BinaryRPCRequest{})
	generator.Add(rpc.BinaryRPCResponse{})
	generator.Add(rpc.BinarySizeDiffRPCRequest{})
	generator.Add(rpc.BinarySizeDiffRPCResponse{})
	generator.Add(rpc.MostRecentBinariesRPCResponse{})

	err := util.WithWriteFile(*outputPath, func(w io.Writer) error {
		return generator.Render(w)
	})
	if err != nil {
		sklog.Fatal(err)
	}
}

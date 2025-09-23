// Program to generate TypeScript definition files for Golang structs that are
// serialized to JSON for the web UI.
//
//go:generate bazelisk run --config=mayberemote //:go -- run . -o ../../modules/json/index.ts
package main

import (
	"flag"
	"io"

	"go.goldmine.build/go/alogin"
	"go.goldmine.build/go/go2ts"
	"go.goldmine.build/go/roles"
	"go.goldmine.build/go/sklog"
	"go.goldmine.build/go/util"
)

func main() {
	var outputPath = flag.String("o", "", "Path to the output TypeScript file.")
	flag.Parse()

	generator := go2ts.New()
	generator.Add(alogin.Status{})
	generator.AddMultipleUnion(
		roles.AllRoles,
	)

	err := util.WithWriteFile(*outputPath, func(w io.Writer) error {
		return generator.Render(w)
	})
	if err != nil {
		sklog.Fatal(err)
	}
}

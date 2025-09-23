// Program to generate JSON Schema definitions for the Probe struct.
//
//go:generate bazelisk run --config=mayberemote //:go -- run .
package main

import (
	"go.goldmine.build/go/jsonschema"
	"go.goldmine.build/proberk/go/types"
)

func main() {
	jsonschema.GenerateSchema("../probesSchema.json", &types.Probes{})
}

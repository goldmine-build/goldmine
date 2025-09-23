// Program to generate JSON Schema definitions for the InstanceConfig struct.
//
//go:generate bazelisk run --config=mayberemote //:go -- run .
package main

import (
	"go.goldmine.build/go/jsonschema"
	"go.goldmine.build/perf/go/ingest/format"
)

func main() {
	jsonschema.GenerateSchema("../formatSchema.json", &format.Format{})
}

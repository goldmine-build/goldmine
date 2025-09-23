// Program to generate JSON Schema definitions for the InstanceConfig struct.
//
//go:generate bazelisk run --config=mayberemote //:go -- run .
package main

import (
	"go.goldmine.build/go/jsonschema"
	"go.goldmine.build/perf/go/config"
)

func main() {
	jsonschema.GenerateSchema("../validate/instanceConfigSchema.json", &config.InstanceConfig{})
}

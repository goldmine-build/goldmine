// Program to generate JSON Schema definitions for the Tool struct.
//
//go:generate bazelisk run --config=mayberemote //:go -- run .
package main

import (
	"go.goldmine.build/go/jsonschema"
	"go.goldmine.build/tool/go/tool"
)

func main() {
	jsonschema.GenerateSchema("../schema.json", &tool.Tool{})
}

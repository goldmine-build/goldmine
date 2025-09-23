// Application exportschema exports the expected schema as a serialized schema.Description.
package main

import (
	"os"

	"go.goldmine.build/go/sklog"
	"go.goldmine.build/go/sql/schema/exportschema"
	"go.goldmine.build/perf/go/sql"
)

func main() {
	err := exportschema.Main(os.Args, sql.Tables{}, sql.Schema)
	if err != nil {
		sklog.Fatal(err)
	}
}

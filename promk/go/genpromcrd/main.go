// Package main implements the genpromcrd command line application.
package main

import (
	"os"

	"go.goldmine.build/go/sklog"
	"go.goldmine.build/promk/go/genpromcrd/genpromcrd"
)

func main() {
	app := genpromcrd.NewApp()

	if err := app.Main(os.Args); err != nil {
		if err != genpromcrd.ErrFlagsParse {
			sklog.Fatal(err)
		}
	}
}

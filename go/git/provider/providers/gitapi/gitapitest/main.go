package main

import (
	"context"
	"flag"

	"go.goldmine.build/go/git/provider"
	"go.goldmine.build/go/git/provider/providers/gitapi"
	"go.goldmine.build/go/sklog"
)

var (
	patPath = flag.String("pat_path", "", "Path to GitHub PAT.")
)

func main() {
	flag.Parse()
	ctx := context.Background()
	ghProvider, err := gitapi.New(
		ctx,
		*patPath,
		"goldmine-build",
		"goldmine",
		"main",
	)
	if err != nil {
		sklog.Fatal(err)
	}
	err = ghProvider.CommitsFromMostRecentGitHashToHead(ctx, "414bffea697f0d5f60edb8116cd6f20089b52379", func(c provider.Commit) error {
		sklog.Info(c.Subject)
		return nil
	})

	if err != nil {
		sklog.Fatal(err)
	}
}

// Package providers builds different kinds of provider.Provider.
package providers

import (
	"context"

	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/util"
	"go.goldmine.build/perf/go/config"
	"go.goldmine.build/perf/go/git/provider"
	"go.goldmine.build/perf/go/git/provider/providers/git_checkout"
	"go.goldmine.build/perf/go/git/provider/providers/gitiles"
)

// New builds a Provider based on the instance config.
func New(
	ctx context.Context,
	provider config.GitProvider,
	url string,
	startCommit string,
	authType config.GitAuthType, // Only used for git_checkout provider.
	dir string, // Only used for git_checkout provider.
) (provider.Provider, error) {
	if util.In(string(provider), []string{"", string(config.GitProviderCLI)}) {
		return git_checkout.New(ctx, authType, url, startCommit, dir)
	} else if provider == config.GitProviderGitiles {
		return gitiles.New(ctx, url, startCommit)
	}
	return nil, skerr.Fmt("invalid type of Provider selected: %q expected one of %q", provider, config.AllGitProviders)
}

// Package providers builds different kinds of provider.Provider.
package providers

import (
	"context"

	"go.goldmine.build/go/auth"
	"go.goldmine.build/go/httputils"
	"go.goldmine.build/go/skerr"
	"go.goldmine.build/go/util"
	"go.goldmine.build/perf/go/config"
	"go.goldmine.build/perf/go/git/provider"
	"go.goldmine.build/perf/go/git/providers/git_checkout"
	"go.goldmine.build/perf/go/git/providers/gitiles"
	"golang.org/x/oauth2/google"
)

// New builds a Provider based on the instance config.
func New(ctx context.Context, instanceConfig *config.InstanceConfig) (provider.Provider, error) {
	prov := instanceConfig.GitRepoConfig.Provider

	if util.In(string(prov), []string{"", string(config.GitProviderCLI)}) {
		return git_checkout.New(ctx, instanceConfig)
	} else if prov == config.GitProviderGitiles {
		ts, err := google.DefaultTokenSource(ctx, auth.ScopeGerrit)
		client := httputils.DefaultClientConfig().WithTokenSource(ts).Client()
		if err != nil {
			return nil, skerr.Wrap(err)
		}
		return gitiles.New(client, instanceConfig), nil
	}
	return nil, skerr.Fmt("invalid type of Provider selected: %q expected one of %q", instanceConfig.GitRepoConfig.Provider, config.AllGitProviders)
}

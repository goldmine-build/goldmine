package ingestion_processors

import (
	"context"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"go.skia.org/infra/go/buildbucket"
	"go.skia.org/infra/go/firestore"
	"go.skia.org/infra/go/gerrit"
	"go.skia.org/infra/go/httputils"
	"go.skia.org/infra/go/metrics2"
	"go.skia.org/infra/go/skerr"
	"go.skia.org/infra/go/sklog"
	"go.skia.org/infra/go/vcsinfo"
	"go.skia.org/infra/golden/go/clstore"
	"go.skia.org/infra/golden/go/clstore/fs_clstore"
	"go.skia.org/infra/golden/go/code_review"
	"go.skia.org/infra/golden/go/code_review/gerrit_crs"
	"go.skia.org/infra/golden/go/code_review/github_crs"
	"go.skia.org/infra/golden/go/continuous_integration"
	"go.skia.org/infra/golden/go/continuous_integration/buildbucket_cis"
	"go.skia.org/infra/golden/go/continuous_integration/dummy_cis"
	"go.skia.org/infra/golden/go/expectations/fs_expectationstore"
	"go.skia.org/infra/golden/go/ingestion"
	"go.skia.org/infra/golden/go/jsonio"
	"go.skia.org/infra/golden/go/shared"
	"go.skia.org/infra/golden/go/tjstore"
	"go.skia.org/infra/golden/go/tjstore/fs_tjstore"
)

const (
	firestoreTryJobIngester = "gold-tryjob-fs"
	firestoreProjectIDParam = "FirestoreProjectID"
	firestoreNamespaceParam = "FirestoreNamespace"

	codeReviewSystemParam      = "CodeReviewSystem"
	gerritURLParam             = "GerritURL"
	githubRepoParam            = "GitHubRepo"
	githubCredentialsPathParam = "GitHubCredentialsPath"

	continuousIntegrationSystemsParam = "ContinuousIntegrationSystems"

	gerritCRS      = "gerrit"
	githubCRS      = "github"
	buildbucketCIS = "buildbucket"
	cirrusCIS      = "cirrus"
)

// Register the ingestion Processor with the ingestion framework.
func init() {
	ingestion.Register(firestoreTryJobIngester, newModularTryjobProcessor)
}

// goldTryjobProcessor implements the ingestion.Processor interface to ingest tryjob results.
type goldTryjobProcessor struct {
	reviewClient code_review.Client
	cisClients   map[string]continuous_integration.Client

	changeListStore clstore.Store
	tryJobStore     tjstore.Store

	crsName string
}

// newModularTryjobProcessor returns an ingestion.Processor which is modular and can support
// different CodeReviewSystems (e.g. "Gerrit", "GitHub") and different ContinuousIntegrationSystems
// (e.g. "BuildBucket", "CirrusCI"). This particular implementation stores the data in Firestore.
func newModularTryjobProcessor(ctx context.Context, _ vcsinfo.VCS, config *ingestion.IngesterConfig, client *http.Client) (ingestion.Processor, error) {
	crsName := config.ExtraParams[codeReviewSystemParam]
	if strings.TrimSpace(crsName) == "" {
		return nil, skerr.Fmt("missing code review system (e.g. 'gerrit')")
	}

	crs, err := codeReviewSystemFactory(crsName, config, client)
	if err != nil {
		return nil, skerr.Wrapf(err, "could not create client for CRS %q", crsName)
	}

	cisNames := strings.Split(config.ExtraParams[continuousIntegrationSystemsParam], ",")
	if len(cisNames) == 0 {
		return nil, skerr.Fmt("missing CI system (e.g. 'buildbucket')")
	}
	cisClients := make(map[string]continuous_integration.Client, len(cisNames))
	for _, cisName := range cisNames {
		cis, err := continuousIntegrationSystemFactory(cisName, config, client)
		if err != nil {
			return nil, skerr.Wrapf(err, "could not create client for CIS %q", cisName)
		}
		cisClients[cisName] = cis
	}

	fsProjectID := config.ExtraParams[firestoreProjectIDParam]
	if strings.TrimSpace(fsProjectID) == "" {
		return nil, skerr.Fmt("missing firestore project id")
	}

	fsNamespace := config.ExtraParams[firestoreNamespaceParam]
	if strings.TrimSpace(fsNamespace) == "" {
		return nil, skerr.Fmt("missing firestore namespace")
	}

	fsClient, err := firestore.NewClient(ctx, fsProjectID, "gold", fsNamespace, nil)
	if err != nil {
		return nil, skerr.Wrapf(err, "could not init firestore in project %s, namespace %s", fsProjectID, fsNamespace)
	}

	expStore := fs_expectationstore.New(fsClient, nil, fs_expectationstore.ReadOnly)
	if err := expStore.Initialize(ctx); err != nil {
		return nil, skerr.Wrapf(err, "initializing expectation store")
	}

	return &goldTryjobProcessor{
		reviewClient:    crs,
		cisClients:      cisClients,
		changeListStore: fs_clstore.New(fsClient, crsName),
		tryJobStore:     fs_tjstore.New(fsClient),
		crsName:         crsName,
	}, nil
}

func codeReviewSystemFactory(crsName string, config *ingestion.IngesterConfig, client *http.Client) (code_review.Client, error) {
	if crsName == gerritCRS {
		gerritURL := config.ExtraParams[gerritURLParam]
		if strings.TrimSpace(gerritURL) == "" {
			return nil, skerr.Fmt("missing URL for the Gerrit code review system")
		}
		gerritClient, err := gerrit.NewGerrit(gerritURL, client)
		if err != nil {
			return nil, skerr.Wrapf(err, "creating gerrit client for %s", gerritURL)
		}
		return gerrit_crs.New(gerritClient), nil
	}
	if crsName == githubCRS {
		githubRepo := config.ExtraParams[githubRepoParam]
		if strings.TrimSpace(githubRepo) == "" {
			return nil, skerr.Fmt("missing repo for the GitHub code review system")
		}
		githubCredPath := config.ExtraParams[githubCredentialsPathParam]
		if strings.TrimSpace(githubCredPath) == "" {
			return nil, skerr.Fmt("missing credentials path for the GitHub code review system")
		}
		gBody, err := ioutil.ReadFile(githubCredPath)
		if err != nil {
			return nil, skerr.Wrapf(err, "reading githubToken in %s", githubCredPath)
		}
		gToken := strings.TrimSpace(string(gBody))
		githubTS := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: gToken})
		c := httputils.DefaultClientConfig().With2xxOnly().WithTokenSource(githubTS).Client()
		return github_crs.New(c, githubRepo), nil
	}
	return nil, skerr.Fmt("CodeReviewSystem %q not recognized", crsName)
}

func continuousIntegrationSystemFactory(cisName string, _ *ingestion.IngesterConfig, client *http.Client) (continuous_integration.Client, error) {
	if cisName == buildbucketCIS {
		bbClient := buildbucket.NewClient(client)
		return buildbucket_cis.New(bbClient), nil
	}
	if cisName == cirrusCIS {
		return dummy_cis.New(cisName), nil
	}
	return nil, skerr.Fmt("ContinuousIntegrationSystem %q not recognized", cisName)
}

// Process implements the Processor interface.
func (g *goldTryjobProcessor) Process(ctx context.Context, rf ingestion.ResultFileLocation) error {
	defer metrics2.FuncTimer().Stop()
	gr, err := processGoldResults(ctx, rf)
	if err != nil {
		sklog.Errorf("Error processing result: %s", err)
		return ingestion.IgnoreResultsFileErr
	}

	clID := ""
	psOrder := 0
	psID := ""
	crs := gr.CodeReviewSystem
	if crs == "" {
		// Default to Gerrit
		crs = gerritCRS
	}
	if crs == g.crsName {
		clID = gr.ChangeListID
		psOrder = gr.PatchSetOrder
		psID = gr.PatchSetID
	} else {
		sklog.Warningf("Result %s said it was for crs %q, but this ingester is configured for %s", rf.Name(), crs, g.crsName)
		// We only support one CRS and one CIS at the moment, but if needed, we can have
		// multiple configured and pivot to the one we need.
		return ingestion.IgnoreResultsFileErr
	}

	tjID := ""
	cisName := gr.ContinuousIntegrationSystem
	if cisName == "" {
		// Default to BuildBucket
		cisName = buildbucketCIS
	}
	var cisClient continuous_integration.Client
	if ci, ok := g.cisClients[cisName]; ok {
		tjID = gr.TryJobID
		cisClient = ci
	} else {
		sklog.Warningf("Result %s said it was for cis %q, but this ingester wasn't configured for it", rf.Name(), cisName)
		// We only support one CRS and one CIS at the moment, but if needed, we can have
		// multiple configured and pivot to the one we need.
		return ingestion.IgnoreResultsFileErr
	}

	// Fetch CL from clstore if we have seen it before, from CRS if we have not.
	cl, err := g.changeListStore.GetChangeList(ctx, clID)
	if err == clstore.ErrNotFound {
		cl, err = g.reviewClient.GetChangeList(ctx, clID)
		if err == code_review.ErrNotFound {
			sklog.Warningf("Unknown %s CL with id %q", crs, clID)
			// Try again later - maybe the input was created before the CL?
			return ingestion.IgnoreResultsFileErr
		} else if err != nil {
			return skerr.Wrapf(err, "fetching CL from %s with id %q", crs, clID)
		}
		// This is a new CL, but we'll be storing it to the clstore down below when
		// we confirm that the TryJob is valid.
	} else if err != nil {
		return skerr.Wrapf(err, "fetching CL from clstore with id %q", clID)
	}

	ps, err := g.getPatchSet(ctx, psOrder, psID, clID, crs)
	if err != nil {
		// Do not wrap this error - this returns IgnoreResultsFileErr sometimes.
		return err
	}

	combinedID := tjstore.CombinedPSID{CL: clID, PS: ps.SystemID, CRS: crs}

	// We now need to 1) verify the TryJob is valid (either we've seen it before and know it's valid
	// or we check now with the CIS) and 2) update the ChangeList's timestamp and store it to
	// clstore. This "refreshes" the ChangeList, making it appear higher up on search results, etc.
	_, err = g.tryJobStore.GetTryJob(ctx, tjID, cisName)
	if err == tjstore.ErrNotFound {
		tj, err := cisClient.GetTryJob(ctx, tjID)
		if err == tjstore.ErrNotFound {
			sklog.Warningf("Unknown %s Tryjob with id %q", cisName, tjID)
			// Try again later - maybe there's some lag with the Integration System?
			return ingestion.IgnoreResultsFileErr
		} else if err != nil {
			return skerr.Wrapf(err, "fetching tryjob from %s with id %q", cisName, tjID)
		}
		err = g.tryJobStore.PutTryJob(ctx, combinedID, tj)
		if err != nil {
			return skerr.Wrapf(err, "storing tryjob %q to tryjobstore", tjID)
		}
		// If we are seeing that a CL was marked as Abandoned, it probably means the CL was
		// re-opened. If this is incorrect (e.g. TryJob was triggered, CL was abandoned, commenter
		// noticed CL was abandoned, and then the TryJob results started being processed), this
		// is fine to mark it as Open, because commenter will correctly mark it as abandoned again.
		// This approach makes fewer queries to the CodeReviewSystem than, for example, querying
		// the CRS *here* if the CL is really open. Keeping CRS queries to a minimum is important,
		// because our quota of them is not high enough to potentially check a CL is abandoned for
		// every TryJobResult that is being streamed in.
		if cl.Status == code_review.Abandoned {
			cl.Status = code_review.Open
		}
	} else if err != nil {
		return skerr.Wrapf(err, "fetching TryJob with id %s", tjID)
	}

	defer shared.NewMetricsTimer("put_tryjobstore_entries").Stop()

	// Store the results from the file.
	tjr := toTryJobResults(gr)
	if err := g.changeListStore.PutPatchSet(ctx, ps); err != nil {
		return skerr.Wrapf(err, "could not store PS %s of CL %q to clstore", psID, clID)
	}
	err = g.tryJobStore.PutResults(ctx, combinedID, tjID, cisName, tjr)
	if err != nil {
		return skerr.Wrapf(err, "putting %d results for CL %s, PS %d (%s), TJ %s, file %s", len(tjr), clID, psOrder, psID, tjID, rf.Name())
	}

	// Be sure to update this time now, so that other processes can use the cl.Update timestamp
	// to determine if any changes have happened to the CL or any children PSes in a given time
	// period.
	cl.Updated = time.Now()
	if err = g.changeListStore.PutChangeList(ctx, cl); err != nil {
		return skerr.Wrapf(err, "updating CL with id %q to clstore", clID)
	}
	return nil
}

// getPatchSet looks up a PatchSet either by id or order from our changeListStore. If it's not
// there, it looks it up from the CRS and then stores it to the changeListStore before returning it.
func (g *goldTryjobProcessor) getPatchSet(ctx context.Context, psOrder int, psID, clID, crs string) (code_review.PatchSet, error) {
	// Try looking up patchset by ID first, then fall back to order.
	if psID != "" {
		// Fetch PS from clstore if we have seen it before, from CRS if we have not.
		ps, err := g.changeListStore.GetPatchSet(ctx, clID, psID)
		if err == clstore.ErrNotFound {
			xps, err := g.reviewClient.GetPatchSets(ctx, clID)
			if err != nil {
				return code_review.PatchSet{}, skerr.Wrapf(err, "could not get patchsets for %s cl %s", crs, clID)
			}
			// It should be ok to overwrite any PatchSets we've seen before - they should be
			// immutable.
			for _, p := range xps {
				if p.SystemID == psID {
					return p, nil
				}
			}
			sklog.Warningf("Unknown %s PS %s for CL %q", crs, psID, clID)
			// Try again later - maybe the input was created before the CL uploaded its PS?
			return code_review.PatchSet{}, ingestion.IgnoreResultsFileErr

		} else if err != nil {
			return code_review.PatchSet{}, skerr.Wrapf(err, "fetching PS from clstore with id %s for CL %q", psID, clID)
		}
		// already found the PS in the store
		return ps, nil
	}
	// Fetch PS from clstore if we have seen it before, from CRS if we have not.
	ps, err := g.changeListStore.GetPatchSetByOrder(ctx, clID, psOrder)
	if err == clstore.ErrNotFound {
		xps, err := g.reviewClient.GetPatchSets(ctx, clID)
		if err != nil {
			return code_review.PatchSet{}, skerr.Wrapf(err, "could not get patchsets for %s cl %s", crs, clID)
		}
		// It should be ok to put any PatchSets we've seen before - they should be immutable.
		for _, p := range xps {
			if p.Order == psOrder {
				return p, nil
			}
		}
		sklog.Warningf("Unknown %s PS with order %d for CL %q", crs, psOrder, clID)
		// Try again later - maybe the input was created before the CL uploaded its PS?
		return code_review.PatchSet{}, ingestion.IgnoreResultsFileErr
	} else if err != nil {
		return code_review.PatchSet{}, skerr.Wrapf(err, "fetching PS from clstore with order %d for CL %q", psOrder, clID)
	}
	// already found the PS in the store
	return ps, nil
}

// toTryJobResults converts the JSON file to a slice of TryJobResult.
func toTryJobResults(j *jsonio.GoldResults) []tjstore.TryJobResult {
	var tjr []tjstore.TryJobResult
	for _, r := range j.Results {
		tjr = append(tjr, tjstore.TryJobResult{
			GroupParams:  j.Key,
			ResultParams: r.Key,
			Options:      r.Options,
			Digest:       r.Digest,
		})
	}
	return tjr
}

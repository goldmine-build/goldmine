import './index';
import { setUpElementUnderTest, eventSequencePromise, eventPromise, setQueryString, expectQueryStringToEqual, noEventPromise } from '../../../infra-sk/modules/test_util';
import { searchResponse, statusResponse, paramSetResponse, changeListSummaryResponse } from './demo_data';
import fetchMock from 'fetch-mock';
import { deepCopy } from 'common-sk/modules/object';
import { fromObject } from 'common-sk/modules/query';
import { SearchPageSk, SearchRequest } from './search-page-sk';
import { SearchPageSkPO } from './search-page-sk_po';
import { Label, SearchResponse, TriageRequest } from '../rpc_types';
import { testOnlySetSettings } from '../settings';
import { SearchCriteria } from '../search-controls-sk/search-controls-sk';
import { SearchControlsSkPO } from '../search-controls-sk/search-controls-sk_po';
import { ChangelistControlsSkPO } from '../changelist-controls-sk/changelist-controls-sk_po';
import { BulkTriageSkPO } from '../bulk-triage-sk/bulk-triage-sk_po';

const expect = chai.expect;

describe('search-page-sk', () => {
  const newInstance = setUpElementUnderTest<SearchPageSk>('search-page-sk');

  let searchPageSk: SearchPageSk;
  let searchPageSkPO: SearchPageSkPO;
  let searchControlsSkPO: SearchControlsSkPO;
  let changelistControlsSkPO: ChangelistControlsSkPO;
  let bulkTriageSkPO: BulkTriageSkPO;

  // SearchCriteria shown by the search-controls-sk component when the search page loads without any
  // URL parameters.
  const defaultSearchCriteria: SearchCriteria = {
    corpus: 'infra',
    leftHandTraceFilter: {},
    rightHandTraceFilter: {},
    includePositiveDigests: false,
    includeNegativeDigests: false,
    includeUntriagedDigests: true,
    includeDigestsNotAtHead: false,
    includeIgnoredDigests: false,
    minRGBADelta: 0,
    maxRGBADelta: 255,
    mustHaveReferenceImage: false,
    sortOrder: 'descending'
  };

  // Default request to the /json/v1/search RPC when the page is loaded with an empty query string.
  const defaultSearchRequest: SearchRequest = {
    fref: false,
    frgbamax: 255,
    frgbamin: 0,
    head: true,
    include: false,
    neg: false,
    pos: false,
    query: 'source_type=infra',
    rquery: 'source_type=infra',
    sort: 'desc',
    unt: true,
  }

  // Query string that will produce the searchRequestWithCL defined below upon page load.
  const queryStringWithCL = '?crs=gerrit&issue=123456';

  // Request to the /json/v1/search RPC when URL parameters crs=gerrit and issue=123456 are present.
  const searchRequestWithCL: SearchRequest = deepCopy(defaultSearchRequest);
  searchRequestWithCL.crs = 'gerrit';
  searchRequestWithCL.issue = '123456';

  // Search response when the query matches 0 digests.
  const emptySearchResponse: SearchResponse = deepCopy(searchResponse);
  emptySearchResponse.size = 0;
  emptySearchResponse.digests = [];

  // Options for the instantiate() function below.
  interface InstantiationOptions {
    initialQueryString: string;
    expectedInitialSearchRequest: SearchRequest;
    initialSearchResponse: SearchResponse;
    mockAndWaitForChangelistSummaryRPC: boolean;
  };

  // Instantiation options for tests where the URL params crs=gerrit and issue=123456 are present.
  const instantiationOptionsWithCL: Partial<InstantiationOptions> = {
    initialQueryString: queryStringWithCL,
    expectedInitialSearchRequest: searchRequestWithCL,
    mockAndWaitForChangelistSummaryRPC: true,
  };

  // Instantiates the search page, sets up the necessary mock RPCs and waits for it to load.
  const instantiate = async (opts: Partial<InstantiationOptions> = {}) => {
    const defaults: InstantiationOptions = {
      initialQueryString: '',
      expectedInitialSearchRequest: defaultSearchRequest,
      initialSearchResponse: searchResponse,
      mockAndWaitForChangelistSummaryRPC: false,
    };

    // Override defaults with the given options, if any.
    opts = {...defaults, ...opts};

    fetchMock.getOnce('/json/v1/trstatus', () => statusResponse);
    fetchMock.getOnce('/json/v1/paramset', () => paramSetResponse);
    fetchMock.get(
      '/json/v1/search?' + fromObject(opts.expectedInitialSearchRequest as any),
      () => opts.initialSearchResponse);

    // We always wait for at least the three above RPCs.
    const eventsToWaitFor = ['end-task', 'end-task', 'end-task'];

    // This mocked RPC corresponds to the queryStringWithCL and searchRequestWithCL constants
    // defined above.
    if (opts.mockAndWaitForChangelistSummaryRPC) {
      fetchMock.getOnce('/json/v1/changelist/gerrit/123456', () => changeListSummaryResponse);
      eventsToWaitFor.push('end-task');
    }

    // The search page will derive its initial search RPC from the query parameters in the URL.
    setQueryString(opts.initialQueryString!);

    // Instantiate search page and wait for all of the above mocked RPCs to complete.
    const events = eventSequencePromise(eventsToWaitFor);
    searchPageSk = newInstance();
    await events;

    searchPageSkPO = new SearchPageSkPO(searchPageSk);
    searchControlsSkPO = await searchPageSkPO.getSearchControlsSkPO();
    changelistControlsSkPO = await searchPageSkPO.getChangelistControlsSkPO();
    bulkTriageSkPO = await searchPageSkPO.getBulkTriageSkPO();
  }

  before(() => {
    testOnlySetSettings({
      title: 'Skia Infra',
      defaultCorpus: 'infra',
      baseRepoURL: 'https://skia.googlesource.com/buildbot.git',
    });
  });

  afterEach(() => {
    expect(fetchMock.done()).to.be.true; // All mock RPCs called at least once.
    fetchMock.reset();
  });

  // This function adds tests to ensure that a search field in the UI is correctly bound to its
  // corresponding query parameter in the URL and to its corresponding field in the SearchRequest
  // object.
  const searchFieldIsBoundToURLAndRPC = <T>(
    instantiationOpts: Partial<InstantiationOptions>,
    queryStringWithSearchField: string,
    uiValueGetterFn: () => Promise<T>,
    uiValueSetterFn: () => Promise<void>,
    expectedUiValue: T,
    expectedSearchRequest: SearchRequest,
  ) => {
    it('is read from the URL and included in the initial search RPC', async () => {
      // We initialize the search page using a query string that contains the search field under
      // test, so that said field is included in the initial search RPC.
      //
      // If the search RPC is not called with the expected SearchRequest, the top-level
      // afterEach() hook will fail.
      await instantiate({
        ...instantiationOpts,
        initialQueryString: queryStringWithSearchField,
        expectedInitialSearchRequest: expectedSearchRequest
      });

      // The search field in the UI should reflect the value from the URL.
      expect(await uiValueGetterFn()).to.deep.equal(expectedUiValue);
    });

    it('is reflected in the URL and included in the search RPC when set via the UI', async () => {
      // We initialize the search page using the default query string.
      await instantiate(instantiationOpts);

      // We will trigger a search RPC when we set the value of the field under test via the UI.
      // If the RPC is not called with the expected SearchRequest, the top-level afterEach() hook
      // will fail.
      fetchMock.get(
        '/json/v1/search?' + fromObject(expectedSearchRequest as any), () => searchResponse);

      // Set the search field under test via the UI and wait for the above RPC to complete.
      const event = eventPromise('end-task');
      await uiValueSetterFn();
      await event;

      // The search field under test should now be reflected in the URL.
      expectQueryStringToEqual(queryStringWithSearchField);
    });
  }

  describe('search-controls-sk', () => {
    const itIsBoundToURLAndRPC = (
      queryString: string,
      searchCriteria: Partial<SearchCriteria>,
      serachRequest: Partial<SearchRequest>
    ) => {
      const expectedSearchCriteria: SearchCriteria = {...defaultSearchCriteria, ...searchCriteria};
      const expectedSearchRequest: SearchRequest = {...defaultSearchRequest, ...serachRequest};

      searchFieldIsBoundToURLAndRPC<SearchCriteria>(
        /* initializationOpts= */ {},
        queryString,
        () => searchControlsSkPO.getSearchCriteria(),
        () => searchControlsSkPO.setSearchCriteria(expectedSearchCriteria!),
        expectedSearchCriteria!,
        expectedSearchRequest!);
    }

    describe('field "corpus"', () => {
      itIsBoundToURLAndRPC(
        '?corpus=my-corpus',
        {corpus: 'my-corpus'},
        {query: 'source_type=my-corpus', rquery: 'source_type=my-corpus'});
    });

    describe('field "left-hand trace filter"', () => {
      itIsBoundToURLAndRPC(
        '?left_filter=name%3Dam_email-chooser-sk',
        {leftHandTraceFilter: {'name': ['am_email-chooser-sk']}},
        {query: 'name=am_email-chooser-sk&source_type=infra'});
    });

    describe('field "right-hand trace filter"', () => {
      itIsBoundToURLAndRPC(
        '?right_filter=name%3Dam_email-chooser-sk',
        {rightHandTraceFilter: {'name': ['am_email-chooser-sk']}},
        {rquery: 'name=am_email-chooser-sk&source_type=infra'});
    });

    describe('field "include positive digests"', () => {
      itIsBoundToURLAndRPC(
        '?positive=true',
        {includePositiveDigests: true},
        {pos: true});
    });

    describe('field "include negative digests"', () => {
      itIsBoundToURLAndRPC(
        '?negative=true',
        {includeNegativeDigests: true},
        {neg: true});
    });

    describe('field "include untriaged digests"', () => {
      // This field is true by default, so we set it to false.
      itIsBoundToURLAndRPC(
        '?untriaged=false',
        {includeUntriagedDigests: false},
        {unt: false});
    });

    describe('field "include digests not at head"', () => {
      itIsBoundToURLAndRPC(
        '?not_at_head=true',
        {includeDigestsNotAtHead: true},
        {head: false}); // SearchRequest field "head" means "at head only".
    });

    describe('field "include ignored digests"', () => {
      itIsBoundToURLAndRPC(
        '?include_ignored=true',
        {includeIgnoredDigests: true},
        {include: true});
    });

    describe('field "min RGBA delta"', () => {
      itIsBoundToURLAndRPC(
        '?min_rgba=10',
        {minRGBADelta: 10},
        {frgbamin: 10});
    });

    describe('field "max RGBA delta"', () => {
      itIsBoundToURLAndRPC(
        '?max_rgba=200',
        {maxRGBADelta: 200},
        {frgbamax: 200});
    });

    describe('field "max RGBA delta"', () => {
      itIsBoundToURLAndRPC(
        '?max_rgba=200',
        {maxRGBADelta: 200},
        {frgbamax: 200});
    });

    describe('field "must have reference image"', () => {
      itIsBoundToURLAndRPC(
        '?reference_image_required=true',
        {mustHaveReferenceImage: true},
        {fref: true});
    });

    describe('field "sort order"', () => {
      itIsBoundToURLAndRPC(
        '?sort=ascending',
        {sortOrder: 'ascending'},
        {sort: 'asc'});
    });
  });

  describe('changelist-controls-sk', () => {
    it('is hidden if no CL is provided in the query string', async () => {
      // When instantiated without URL parameters "crs" and "issue", the search page does not make
      // an RPC to /json/v1/changelist, therefore there is no changelist summary for the
      // changelist-controls-sk component to display.
      await instantiate();
      expect(await changelistControlsSkPO.isVisible()).to.be.false;
    });

    it(
        'is visible if a CL is provided in the query string and /json/v1/changelist returns a ' +
        'non-empty response',
        async () => {
      // We instantiate the serach page with URL parameters "crs" and "issue", which causes it to
      // make an RPC to /json/v1/changelist. The returned changelist summary is passed to the
      // changelist-controls-sk component, which then makes itself visible.
      await instantiate(instantiationOptionsWithCL);
      expect(await changelistControlsSkPO.isVisible()).to.be.true;
    });

    describe('field "patchset"', () => {
      searchFieldIsBoundToURLAndRPC<string>(
        instantiationOptionsWithCL,
        queryStringWithCL + '&patchsets=1',
        () => changelistControlsSkPO.getPatchSet(),
        () => changelistControlsSkPO.setPatchSet('PS 1'),
        /* expectedUiValue= */ 'PS 1',
        {...searchRequestWithCL, patchsets: 1});
    });

    describe('radio "exclude results from primary branch"', () => {
      // When this radio is clicked, the "master" parameter is removed from the URL if present, so
      // we need to test this backwards by starting with "master=true" in the URL (which means the
      // initial search RPC will include "master=true" in the SearchRequest as well) and then
      // asserting that "master" is removed from both the URL and the SearchRequest when the radio
      // is clicked.
      searchFieldIsBoundToURLAndRPC<boolean>(
        {
          ...instantiationOptionsWithCL,
          expectedInitialSearchRequest:  {...searchRequestWithCL, master: true, patchsets: 2},
          initialQueryString: queryStringWithCL + '&master=true&patchsets=2'
        },
        queryStringWithCL + '&patchsets=2',
        () => changelistControlsSkPO.isExcludeResultsFromPrimaryRadioChecked(),
        () => changelistControlsSkPO.clickExcludeResultsFromPrimaryRadio(),
        /* expectedUiValue= */ true,
        {...searchRequestWithCL, patchsets: 2});
    });

    describe('radio "show all results"', () => {
      searchFieldIsBoundToURLAndRPC<boolean>(
        instantiationOptionsWithCL,
        queryStringWithCL + '&master=true&patchsets=2',
        () => changelistControlsSkPO.isShowAllResultsRadioChecked(),
        () => changelistControlsSkPO.clickShowAllResultsRadio(),
        /* expectedUiValue= */ true,
        {...searchRequestWithCL, master: true, patchsets: 2});
    });
  });

  describe('search results', () => {
    it('shows empty search results', async () => {
      await instantiate({initialSearchResponse: emptySearchResponse});

      expect(await searchPageSkPO.getSummary())
        .to.equal('No results matched your search criteria.');
      expect(await searchPageSkPO.getDigests()).to.be.empty;
    });

    it('shows search results', async () => {
      await instantiate();

      expect(await searchPageSkPO.getSummary()).to.equal('Showing results 1 to 3 (out of 85).');
      expect(await searchPageSkPO.getDigests()).to.deep.equal([
        'Left: fbd3de3fff6b852ae0bb6751b9763d27',
        'Left: 2fa58aa430e9c815755624ca6cca4a72',
        'Left: ed4a8cf9ea9fbb57bf1f302537e07572'
      ]);
    });

    it('shows search results with changelist information', async () => {
      await instantiate(instantiationOptionsWithCL);

      expect(await searchPageSkPO.getDigests()).to.deep.equal([
        'Left: fbd3de3fff6b852ae0bb6751b9763d27',
        'Left: 2fa58aa430e9c815755624ca6cca4a72',
        'Left: ed4a8cf9ea9fbb57bf1f302537e07572'
      ]);

      const diffDetailsHrefs = await searchPageSkPO.getDiffDetailsHrefs();
      expect(diffDetailsHrefs[0]).to.contain('changelist_id=123456&crs=gerrit');
      expect(diffDetailsHrefs[1]).to.contain('changelist_id=123456&crs=gerrit');
      expect(diffDetailsHrefs[2]).to.contain('changelist_id=123456&crs=gerrit');
    });
  });

  // TODO(lovisolo): Add some sort of indication in the UI when searching by blame.
  describe('"blame" URL parameter', () => {
    it('is reflected in the initial search RPC', async () => {
      // No explicit assertions are necessary because if the search RPC is not called with the
      // expected SearchRequest then the fetchMock.done() call in the top-level afterEach() hook
      // will fail.
      await instantiate({
        initialQueryString:
          '?blame=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
        expectedInitialSearchRequest: {
          ...defaultSearchRequest,
          blame: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb'
        },
      });
    });
  });

  describe('help dialog', () => {
    it('is closed by default', async () => {
      await instantiate();
      expect(await searchPageSkPO.isHelpDialogOpen()).to.be.false;
    });

    it('opens when clicking the "Help" button', async () => {
      await instantiate();
      await searchPageSkPO.clickHelpBtn();
      expect(await searchPageSkPO.isHelpDialogOpen()).to.be.true;
    });

    it('closes when the "Close" button is clicked', async () => {
      await instantiate();
      await searchPageSkPO.clickHelpBtn();
      await searchPageSkPO.clickHelpDialogCancelBtn();
      expect(await searchPageSkPO.isHelpDialogOpen()).to.be.false;
    });
  });

  describe('bulk triage dialog', () => {
    describe('opening and closing', () => {
      it('is closed by default', async () => {
        await instantiate();
        expect(await searchPageSkPO.isBulkTriageDialogOpen()).to.be.false;
      });

      it('opens when clicking the "Bulk Triage" button', async () => {
        await instantiate();
        await searchPageSkPO.clickBulkTriageBtn();
        expect(await searchPageSkPO.isBulkTriageDialogOpen()).to.be.true;
      });

      it('closes when the "Cancel" button is clicked', async () => {
        await instantiate();
        await searchPageSkPO.clickBulkTriageBtn();
        await bulkTriageSkPO.clickCancelBtn();
        expect(await searchPageSkPO.isBulkTriageDialogOpen()).to.be.false;
      });

      it('closes when the "Triage ..." button is clicked', async () => {
        fetchMock.post('/json/v1/triage', 200); // We ignore the TriageRequest in this test.

        await instantiate();
        await searchPageSkPO.clickBulkTriageBtn();
        await bulkTriageSkPO.clickTriageBtn();
        expect(await searchPageSkPO.isBulkTriageDialogOpen()).to.be.false;
      });
    });

    describe('affected CL', () => {
      it('does not show an affected CL if none is provided', async () => {
        await instantiate();
        await searchPageSkPO.clickBulkTriageBtn();
        expect(await bulkTriageSkPO.isAffectedChangelistIdVisible()).to.be.false;
      });

      it('shows the affected CL if one is provided', async () => {
        await instantiate(instantiationOptionsWithCL);
        await searchPageSkPO.clickBulkTriageBtn();
        expect(await bulkTriageSkPO.isAffectedChangelistIdVisible()).to.be.true;
        expect(await bulkTriageSkPO.getAffectedChangelistId()).to.equal(
          'This affects ChangeList 123456.');
      });
    });

    describe('RPCs', () => {
      describe('search results from current page only', () => {
        const expectedTriageRequest: TriageRequest = {
          testDigestStatus: {
            'gold_search-controls-sk_right-hand-trace-filter-editor': {
              'fbd3de3fff6b852ae0bb6751b9763d27': 'positive',
            },
            'perf_alert-config-sk': {
              '2fa58aa430e9c815755624ca6cca4a72': 'positive',
              'ed4a8cf9ea9fbb57bf1f302537e07572': 'positive',
            },
          },
          changelist_id: '',
          crs: '',
        };

        it('can bulk-triage without a CL', async () => {
          fetchMock.post('/json/v1/triage', 200, {body: expectedTriageRequest});

          await instantiate();
          await searchPageSkPO.clickBulkTriageBtn();
          await bulkTriageSkPO.clickPositiveBtn();
          await bulkTriageSkPO.clickTriageBtn();
        });

        it('can bulk-triage with a CL', async () => {
          fetchMock.post('/json/v1/triage', 200, {
            body: {
              ...expectedTriageRequest,
              changelist_id: '123456',
              crs: 'gerrit',
            }
          });

          await instantiate(instantiationOptionsWithCL);
          await searchPageSkPO.clickBulkTriageBtn();
          await bulkTriageSkPO.clickPositiveBtn();
          await bulkTriageSkPO.clickTriageBtn();
        });
      });

      describe('all search results', () => {
        const expectedTriageRequest: TriageRequest = {
          testDigestStatus: {
            'gold_details-page-sk': {
              '29f31f703510c2091840b5cf2b032f56': 'positive',
              '7c0a393e57f14b5372ec1590b79bed0f': 'positive',
              '971fe90fa07ebc2c7d0c1a109a0f697c': 'positive',
              'e49c92a2cff48531810cc5e863fad0ee': 'positive'
          },
          'gold_search-controls-sk_right-hand-trace-filter-editor': {
              '5d8c80eda80e015d633a4125ab0232dc': 'positive',
              'd20f37006e436fe17f50ecf49ff2bdb5': 'positive',
              'fbd3de3fff6b852ae0bb6751b9763d27': 'positive'
          },
          'perf_alert-config-sk': {
              '2fa58aa430e9c815755624ca6cca4a72': 'positive',
              'ed4a8cf9ea9fbb57bf1f302537e07572': 'positive'
          },
          },
          changelist_id: '',
          crs: '',
        }

        it('can bulk-triage without a CL', async () => {
          fetchMock.post('/json/v1/triage', 200, {body: expectedTriageRequest});

          await instantiate();
          await searchPageSkPO.clickBulkTriageBtn();
          await bulkTriageSkPO.clickTriageAllCheckbox();
          await bulkTriageSkPO.clickPositiveBtn();
          await bulkTriageSkPO.clickTriageBtn();
        });

        it('can bulk-triage with a CL', async () => {
          fetchMock.post('/json/v1/triage', 200, {
            body: {
              ...expectedTriageRequest,
              changelist_id: '123456',
              crs: 'gerrit',
            }
          });

          await instantiate(instantiationOptionsWithCL);
          await searchPageSkPO.clickBulkTriageBtn();
          await bulkTriageSkPO.clickTriageAllCheckbox();
          await bulkTriageSkPO.clickPositiveBtn();
          await bulkTriageSkPO.clickTriageBtn();
        });
      });
    });
  });

  describe('keyboard shortcuts', () => {
    // TODO(lovisolo): Clean this up after digest-details-sk is ported to TypeScript and we have
    //                 a DigestDetailsSkPO.
    const firstDigest = 'Left: fbd3de3fff6b852ae0bb6751b9763d27';
    const secondDigest = 'Left: 2fa58aa430e9c815755624ca6cca4a72';
    const thirdDigest = 'Left: ed4a8cf9ea9fbb57bf1f302537e07572';

    const expectLabelsForFirstSecondAndThirdDigestsToBe =
        async (firstLabel: Label, secondLabel: Label, thirdLabel: Label) => {
      expect(await searchPageSkPO.getLabelForDigest(firstDigest)).to.equal(firstLabel);
      expect(await searchPageSkPO.getLabelForDigest(secondDigest)).to.equal(secondLabel);
      expect(await searchPageSkPO.getLabelForDigest(thirdDigest)).to.equal(thirdLabel);
    }

    describe('navigation', () => {
      it('initially has an empty selection', async () => {
        await instantiate();
        expect(await searchPageSkPO.getSelectedDigest()).to.be.null;
      });

      it('can navigate between digests with keys "J" and "K"', async () => {
        await instantiate();

        expect(await searchPageSkPO.getSelectedDigest()).to.be.null;

        // Forward.
        await searchPageSkPO.typeKey('j');
        expect(await searchPageSkPO.getSelectedDigest()).to.equal(firstDigest);

        // Forward.
        await searchPageSkPO.typeKey('j');
        expect(await searchPageSkPO.getSelectedDigest()).to.equal(secondDigest);

        // Forward.
        await searchPageSkPO.typeKey('j');
        expect(await searchPageSkPO.getSelectedDigest()).to.equal(thirdDigest);

        // Forward. Nothing happens because we're at the last search result.
        await searchPageSkPO.typeKey('j');
        expect(await searchPageSkPO.getSelectedDigest()).to.equal(thirdDigest);

        // Back.
        await searchPageSkPO.typeKey('k');
        expect(await searchPageSkPO.getSelectedDigest()).to.equal(secondDigest);

        // Back.
        await searchPageSkPO.typeKey('k');
        expect(await searchPageSkPO.getSelectedDigest()).to.equal(firstDigest);

        // Back. Nothing happens because we're at the first search result.
        await searchPageSkPO.typeKey('k');
        expect(await searchPageSkPO.getSelectedDigest()).to.equal(firstDigest);
      });

      it('resets the selection when the search results change', async () => {
        await instantiate();

        // Select the first search result.
        await searchPageSkPO.typeKey('j');

        // Refresh the results by changing a search parameter.
        fetchMock.get('glob:/json/v1/search?*', searchResponse);
        const event = eventPromise('end-task');
        await searchControlsSkPO.clickIncludePositiveDigestsCheckbox();
        await event;

        // Search results should be non-empty, but selection should be empty.
        expect(await searchPageSkPO.getDigests()).to.not.be.empty;
        expect(await searchPageSkPO.getSelectedDigest()).to.be.null;
      });
    });

    describe('triaging', () => {
      it('cannot triage with "A", "S" and "D" keys when the selection is empty', async () => {
        await instantiate();

        // Check initial labels.
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'negative', 'untriaged');

        // Triaging as positive should have no effect.
        await searchPageSkPO.typeKey('a');
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'negative', 'untriaged');

        // Triaging as negative should have no effect.
        await searchPageSkPO.typeKey('s');
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'negative', 'untriaged');

        // Triaging as untriaged should have no effect.
        await searchPageSkPO.typeKey('d');
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'negative', 'untriaged');
      });

      it('can triage the selected digest with keys "A", "S" and "D"', async () => {
        fetchMock.post('/json/v1/triage', 200); // We ignore the TriageRequest in this test.

        await instantiate();

        // Check initial labels.
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'negative', 'untriaged');

        // Select the second search result.
        await searchPageSkPO.typeKey('j');
        await searchPageSkPO.typeKey('j');

        // Triage as positive.
        let event = eventPromise('end-task');
        await searchPageSkPO.typeKey('a');
        await event;
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'positive', 'untriaged');

        // Triage as negative.
        event = eventPromise('end-task');
        await searchPageSkPO.typeKey('s');
        await event;
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'negative', 'untriaged');

        // Triage as untriaged.
        event = eventPromise('end-task');
        await searchPageSkPO.typeKey('d');
        await event;
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'untriaged', 'untriaged');
      });
    });

    describe('zoom', () => {
      it('cannot zoom with the "W" key when the selection is empty', async () => {
        await instantiate();

        // Check that there is no open zoom dialog.
        expect(await searchPageSkPO.getDigestWithOpenZoomDialog()).to.be.null;

        // The keyboard shortcut should have no effect as no digest is selected.
        await searchPageSkPO.typeKey('w');
        expect(await searchPageSkPO.getDigestWithOpenZoomDialog()).to.be.null;
      });

      it('can zoom into the selected digest with the "W" key', async () => {
        await instantiate();

        // Select the second search result.
        await searchPageSkPO.typeKey('j');
        await searchPageSkPO.typeKey('j');

        // The zoom dialog for the second search result should open.
        await searchPageSkPO.typeKey('w');
        expect(await searchPageSkPO.getDigestWithOpenZoomDialog()).to.equal(secondDigest);
      });
    });

    it('shows the help dialog when pressing the "?" key', async () => {
      await instantiate();
      await searchPageSkPO.typeKey('?');
      expect(await searchPageSkPO.isHelpDialogOpen()).to.be.true;
    });

    describe('shortcuts are disabled when a dialog is open', () => {
      beforeEach(async () => {
        await instantiate();

        // Select the second search result. The expectKeyboardShortcutsToBeDisabled() helper below
        // relies on this.
        await searchPageSkPO.typeKey('j');
        await searchPageSkPO.typeKey('j');
      });

      const expectKeyboardShortcutsToBeDisabled = async () => {
        // Navigation shortcuts should have no effect.
        expect(await searchPageSkPO.getSelectedDigest()).to.equal(secondDigest);
        await searchPageSkPO.typeKey('j');
        expect(await searchPageSkPO.getSelectedDigest()).to.equal(secondDigest);
        await searchPageSkPO.typeKey('k');
        expect(await searchPageSkPO.getSelectedDigest()).to.equal(secondDigest);

        // Check initial triage labels.
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'negative', 'untriaged');

        // Shortcut for triaging as positive should have no effect.
        let noEvent = noEventPromise('begin-task');
        await searchPageSkPO.typeKey('a');
        await noEvent;
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'negative', 'untriaged');

        // Shortcut for triaging as negative should have no effect.
        noEvent = noEventPromise('begin-task');
        await searchPageSkPO.typeKey('s');
        await noEvent;
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'negative', 'untriaged');

        // Shortcut for triaging as untriagaed should have no effect.
        noEvent = noEventPromise('begin-task');
        await searchPageSkPO.typeKey('d');
        await noEvent;
        await expectLabelsForFirstSecondAndThirdDigestsToBe('positive', 'negative', 'untriaged');

        // Shortcut for the help dialog should have no effect, but we can only test this if the
        // help dialog is not already open, otherwise the shortcut has no effect.
        if (!(await searchPageSkPO.isHelpDialogOpen())) {
          await searchPageSkPO.typeKey('?');
          expect(await searchPageSkPO.isHelpDialogOpen()).to.be.false;
        }
      };

      it('disables keyboard shortcuts when the help dialog is open', async () => {
        await searchPageSkPO.clickHelpBtn(); // Open help dialog.

        expect(await searchPageSkPO.isHelpDialogOpen()).to.be.true;
        await expectKeyboardShortcutsToBeDisabled();
      });

      it('disables keyboard shortcuts when the bulk triage dialog is open', async () => {
        await searchPageSkPO.clickBulkTriageBtn(); // Open bulk triage dialog.

        expect(await searchPageSkPO.isBulkTriageDialogOpen()).to.be.true;
        await expectKeyboardShortcutsToBeDisabled();
      });

      it('disables keyboard shortcuts when the left-hand trace filter dialog is open', async () => {
        const leftHandTraceFilterSkPO = await searchControlsSkPO.getTraceFilterSkPO();
        await leftHandTraceFilterSkPO.clickEditBtn(); // Open left-hand trace filter dialog.

        expect(await leftHandTraceFilterSkPO.isQueryDialogSkOpen()).to.be.true;
        await expectKeyboardShortcutsToBeDisabled();
      });

      it('disables keyboard shortcuts when the more filters dialog is open', async () => {
        const filterDialogSkPO = await searchControlsSkPO.getFilterDialogSkPO();
        await searchControlsSkPO.clickMoreFiltersBtn(); // Open more filters dialog.

        expect(await filterDialogSkPO.isDialogOpen()).to.be.true;
        await expectKeyboardShortcutsToBeDisabled();
      });

      it(
          'disables keyboard shortcuts when the right-hand trace filter dialog is open',
          async () => {

        const filterDialogSkPO = await searchControlsSkPO.getFilterDialogSkPO();
        await searchControlsSkPO.clickMoreFiltersBtn(); // Open more filters dialog.

        const rightHandTraceFilterSkPO = await filterDialogSkPO.getTraceFilterSkPO();
        await rightHandTraceFilterSkPO.clickEditBtn(); // Open right-hand trace filter dialog.

        expect(await filterDialogSkPO.isDialogOpen()).to.be.true;
        expect(await rightHandTraceFilterSkPO.isQueryDialogSkOpen()).to.be.true;
        await expectKeyboardShortcutsToBeDisabled();
      });

      it('disables keyboard shortcuts when the zoom dialog is open', async () => {
        await searchPageSkPO.typeKey('w'); // Open zoom dialog.

        expect(await searchPageSkPO.getDigestWithOpenZoomDialog()).to.not.be.null;
        await expectKeyboardShortcutsToBeDisabled();
      });
    });
  });

  describe('back to legacy search page link', () => {
    it('links with default query parameters', async () => {
      await instantiate();
      const expectedHref = [
        '/oldsearch?',
        'fref=false&',
        'frgbamax=255&',
        'frgbamin=0&',
        'head=true&',
        'include=false&',
        'neg=false&',
        'pos=false&',
        `query=${encodeURIComponent('source_type=infra')}&`,
        `rquery=${encodeURIComponent('source_type=infra')}&`,
        'sort=desc&',
        'unt=true'
      ].join('');
      expect(await searchPageSkPO.getLegacySearchPageHref()).to.equal(expectedHref);
    });

    it('links with all query parameters set', async () => {
      // Pretend the "blame", "crs" and "issue" parameters are set. We will test that these are also
      // present in the link to the legacy search page.
      await instantiate({
        initialQueryString:
          '?blame=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb&' +
          'crs=gerrit&issue=123456',
        expectedInitialSearchRequest: {
          ...searchRequestWithCL,
          blame: 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
        },
        mockAndWaitForChangelistSummaryRPC: true,
      });

      // This test ignores the search response, but we still need to mock the search endpoint
      // because each simulated change to the search controls will trigger a new search request.
      fetchMock.get('glob:/json/v1/search?*', searchResponse);

      // Set search controls.
      const searchCriteria: SearchCriteria = {
        corpus: 'infra',

        leftHandTraceFilter: {
          'name': ['gold_dots-sk', 'gold_dots-sk_highlighted'],
          'ext': ['png'],
        },

        rightHandTraceFilter: {
          'name': ['gold_dots-sk'],
        },

        includePositiveDigests: true,
        includeNegativeDigests: true,
        includeUntriagedDigests: true,
        includeDigestsNotAtHead: true,
        includeIgnoredDigests: true,

        minRGBADelta: 100,
        maxRGBADelta: 200,
        mustHaveReferenceImage: true,
        sortOrder: 'ascending',
      };
      await searchControlsSkPO.setSearchCriteria(searchCriteria);

      // Set changelist controls.
      await changelistControlsSkPO.setPatchSet('PS 2');
      await changelistControlsSkPO.clickShowAllResultsRadio();

      const expectedBlame =
        encodeURIComponent('aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb');
      const expectedLeftHandQuery =
        encodeURIComponent(
          'ext=png&name=gold_dots-sk&name=gold_dots-sk_highlighted&source_type=infra');
      const expectedRightHandQuery =  encodeURIComponent('name=gold_dots-sk&source_type=infra');
      const expectedHref = [
        '/oldsearch?',
        `blame=${expectedBlame}&`,
        'crs=gerrit&',
        'fref=true&',
        'frgbamax=200&',
        'frgbamin=100&',
        'head=false&',
        'include=true&',
        'issue=123456&',
        'master=true&',
        'neg=true&',
        'patchsets=2&',
        'pos=true&',
        `query=${expectedLeftHandQuery}&`,
        `rquery=${expectedRightHandQuery}&`,
        'sort=asc&',
        'unt=true',
      ].join('');
      expect(await searchPageSkPO.getLegacySearchPageHref()).to.equal(expectedHref);
    });
  });
});

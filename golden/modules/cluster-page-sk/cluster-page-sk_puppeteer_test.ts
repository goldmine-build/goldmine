import { expect } from 'chai';
import { ElementHandle } from 'puppeteer';
import {
  addEventListenersToPuppeteerPage,
  EventName,
  loadCachedTestBed,
  ModeOption,
  Modes,
  takeScreenshotWithMode,
  TestBed,
} from '../../../puppeteer-tests/util';
import { ClusterPageSkPO } from './cluster-page-sk_po';
import { negativeDigest, positiveDigest, untriagedDigest } from './test_data';

describe('cluster-page-sk', () => {
  let testBed: TestBed;

  before(async () => {
    testBed = await loadCachedTestBed();
  });

  let clusterPageSk: ElementHandle;
  let clusterPageSkPO: ClusterPageSkPO;

  let promiseFactory: <T>(eventName: EventName) => Promise<T>;

  beforeEach(async () => {
    await testBed.page.setViewport({ width: 1200, height: 1200 });

    promiseFactory = await addEventListenersToPuppeteerPage(testBed.page, [
      'layout-complete',
      'selection-changed',
    ]);

    const loaded = promiseFactory('layout-complete'); // Emitted when layout stabilizes.
    await testBed.page.goto(testBed.baseUrl);
    await loaded;

    clusterPageSk = (await testBed.page.$('cluster-page-sk'))!;
    clusterPageSkPO = new ClusterPageSkPO(clusterPageSk);
  });

  it('should render the demo page', async () => {
    // Smoke test.
    expect(await testBed.page.$$('cluster-page-sk')).to.have.length(1);
  });

  Modes.forEach(async (mode: ModeOption) => {
    it('should take a screenshot', async () => {
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'cluster-page-sk',
        mode
      );
    });

    it('shows details about a single digest when clicked', async () => {
      await clusterPageSkPO.clusterDigestsSkPO.clickNode(positiveDigest);
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'cluster-page-sk_one-digest-selected',
        mode
      );
    });

    it('shows diff between two digests that are selected', async () => {
      await clusterPageSkPO.clusterDigestsSkPO.clickNode(positiveDigest);
      await clusterPageSkPO.clusterDigestsSkPO.shiftClickNode(negativeDigest);
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'cluster-page-sk_two-digests-selected',
        mode
      );
    });

    it('shows a summary when more than two digests are selected', async () => {
      await clusterPageSkPO.clusterDigestsSkPO.clickNode(positiveDigest);
      await clusterPageSkPO.clusterDigestsSkPO.shiftClickNode(negativeDigest);
      await clusterPageSkPO.clusterDigestsSkPO.shiftClickNode(untriagedDigest);
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'cluster-page-sk_three-digests-selected',
        mode
      );
    });

    it('shows all values when a paramset key is clicked', async () => {
      const done = promiseFactory('layout-complete');
      await clusterPageSkPO.paramSetSkPO.clickKey('gpu');
      await done;
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'cluster-page-sk_key-clicked',
        mode
      );
    });

    it('shows nodes with matching values when a value is clicked', async () => {
      const done = promiseFactory('layout-complete');
      await clusterPageSkPO.paramSetSkPO.clickValue({
        paramSetIndex: 0,
        key: 'gpu',
        value: 'AMD',
      });
      await done;
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'cluster-page-sk_value-clicked',
        mode
      );
    });

    it('can zoom in using the keyboard', async () => {
      const done = promiseFactory('layout-complete');
      await clusterPageSk.type('aa');
      await done;
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'cluster-page-sk_zoom-in',
        mode
      );
    });

    it('can zoom out using the keyboard', async () => {
      const done = promiseFactory('layout-complete');
      await clusterPageSk.type('zz');
      await done;
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'cluster-page-sk_zoom-out',
        mode
      );
    });

    it('can increase node spacing using the keyboard', async () => {
      const done = promiseFactory('layout-complete');
      await clusterPageSk.type('ss');
      await done;
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'cluster-page-sk_more-node-space',
        mode
      );
    });

    it('can decrease node spacing using the keyboard', async () => {
      const done = promiseFactory('layout-complete');
      await clusterPageSk.type('xx');
      await done;
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'cluster-page-sk_less-node-space',
        mode
      );
    });
  });
});

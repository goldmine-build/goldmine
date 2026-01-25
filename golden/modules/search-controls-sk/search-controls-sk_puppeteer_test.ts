import { expect } from 'chai';
import {
  loadCachedTestBed,
  ModeOption,
  Modes,
  takeScreenshotWithMode,
  TestBed,
} from '../../../puppeteer-tests/util';

describe('search-controls-sk', () => {
  let testBed: TestBed;

  before(async () => {
    testBed = await loadCachedTestBed();
  });

  beforeEach(async () => {
    await testBed.page.goto(testBed.baseUrl);
    await testBed.page.setViewport({ width: 1200, height: 800 });
  });

  Modes.forEach(async (mode: ModeOption) => {
    it('should render the demo page', async () => {
      // Smoke test.
      expect(await testBed.page.$$('search-controls-sk')).to.have.length(1);
    });

    it('shows an empty search criteria', async () => {
      await testBed.page.click('button#clear');
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'search-controls-sk_empty',
        mode
      );
    });

    it('shows a non-empty search criteria', async () => {
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'search-controls-sk',
        mode
      );
    });

    it('shows the left-hand trace filter editor', async () => {
      await testBed.page.click('.traces button.edit-query');
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'search-controls-sk_left-hand-trace-filter-editor',
        mode
      );
    });

    it('shows more filters', async () => {
      await testBed.page.click('button.more-filters');
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'search-controls-sk_more-filters',
        mode
      );
    });

    it('shows the left-hand trace filter editor', async () => {
      await testBed.page.click('button.more-filters');
      await testBed.page.click('filter-dialog-sk button.edit-query');
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'search-controls-sk_right-hand-trace-filter-editor',
        mode
      );
    });
  });
});

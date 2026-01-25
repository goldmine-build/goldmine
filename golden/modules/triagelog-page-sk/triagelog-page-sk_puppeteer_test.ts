import {
  addEventListenersToPuppeteerPage,
  loadCachedTestBed,
  ModeOption,
  Modes,
  takeScreenshotWithMode,
  TestBed,
} from '../../../puppeteer-tests/util';

describe('triagelog-page-sk', () => {
  let testBed: TestBed;
  before(async () => {
    testBed = await loadCachedTestBed();
  });

  beforeEach(async () => {
    const eventPromise = await addEventListenersToPuppeteerPage(testBed.page, [
      'end-task',
    ]);
    const loaded = eventPromise('end-task'); // Emitted when page is loaded.
    await testBed.page.goto(testBed.baseUrl);
    await loaded;
  });

  Modes.forEach(async (mode: ModeOption) => {
    it('should take a screenshot', async () => {
      await testBed.page.setViewport({ width: 1200, height: 1800 });
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'triagelog-page-sk',
        mode
      );
    });
  });
});

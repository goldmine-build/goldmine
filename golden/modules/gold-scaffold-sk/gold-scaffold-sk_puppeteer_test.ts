import { expect } from 'chai';
import {
  loadCachedTestBed,
  ModeOption,
  Modes,
  takeScreenshotWithMode,
  TestBed,
} from '../../../puppeteer-tests/util';

describe('gold-scaffold-sk', () => {
  let testBed: TestBed;

  before(async () => {
    testBed = await loadCachedTestBed();
  });

  beforeEach(async () => {
    await testBed.page.goto(testBed.baseUrl);
  });

  it('should render the demo page', async () => {
    // Smoke test.
    expect(await testBed.page.$$('gold-scaffold-sk')).to.have.length(1);
  });

  Modes.forEach(async (mode: ModeOption) => {
    it('should take a screenshot', async () => {
      await testBed.page.setViewport({ width: 1200, height: 600 });
      await takeScreenshotWithMode(
        testBed.page,
        'gold',
        'gold-scaffold-sk',
        mode
      );
    });
  });
});

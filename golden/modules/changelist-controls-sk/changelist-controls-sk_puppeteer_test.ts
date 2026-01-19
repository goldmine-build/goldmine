import { expect } from 'chai';
import {
  loadCachedTestBed,
  ModeOption,
  Modes,
  takeScreenshotWithMode,
  TestBed,
} from '../../../puppeteer-tests/util';

describe('changelist-controls-sk', () => {
  let testBed: TestBed;

  before(async () => {
    testBed = await loadCachedTestBed();
  });

  beforeEach(async () => {
    await testBed.page.goto(testBed.baseUrl);
  });

  it('should render the demo page', async () => {
    // Smoke test.
    expect(await testBed.page.$$('changelist-controls-sk')).to.have.length(1);
  });

  Modes.forEach(async (mode: ModeOption) => {
    it('should take a screenshot', async () => {
      const controls = await testBed.page.$('.search_response');
      await takeScreenshotWithMode(
        controls!,
        'gold',
        'changelist-controls-sk',
        mode
      );
    });
  });
});

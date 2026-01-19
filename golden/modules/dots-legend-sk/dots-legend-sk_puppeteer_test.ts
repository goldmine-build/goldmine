import { expect } from 'chai';
import {
  ModeOption,
  Modes,
  loadCachedTestBed,
  TestBed,
  takeScreenshotWithMode,
} from '../../../puppeteer-tests/util';

describe('dots-legend-sk', () => {
  let testBed: TestBed;

  before(async () => {
    testBed = await loadCachedTestBed();
  });

  beforeEach(async () => {
    await testBed.page.goto(testBed.baseUrl);
  });

  it('should render the demo page', async () => {
    // Smoke test.
    expect(await testBed.page.$$('dots-legend-sk')).to.have.length(2);
  });

  describe('screenshots', () => {
    Modes.forEach(async (mode: ModeOption) => {
      it('some digests', async () => {
        const dotsLegendSk = await testBed.page.$('#some-digests');
        await takeScreenshotWithMode(
          dotsLegendSk!,
          'gold',
          'dots-legend-sk',
          mode
        );
      });

      it('too many digests', async () => {
        const dotsLegendSk = await testBed.page.$('#too-many-digests');
        await takeScreenshotWithMode(
          dotsLegendSk!,
          'gold',
          'dots-legend-sk_too-many-digests',
          mode
        );
      });
    });
  });
});

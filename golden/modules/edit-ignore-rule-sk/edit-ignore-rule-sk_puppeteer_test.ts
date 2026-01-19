import { expect } from 'chai';
import {
  loadCachedTestBed,
  ModeOption,
  Modes,
  takeScreenshotWithMode,
  TestBed,
} from '../../../puppeteer-tests/util';

describe('edit-ignore-rule-sk', () => {
  let testBed: TestBed;

  before(async () => {
    testBed = await loadCachedTestBed();
  });

  beforeEach(async () => {
    await testBed.page.goto(testBed.baseUrl);
  });

  it('should render the demo page', async () => {
    // Smoke test.
    expect(await testBed.page.$$('edit-ignore-rule-sk')).to.have.length(4);
  });

  describe('screenshots', () => {
    Modes.forEach(async (mode: ModeOption) => {
      it('is a view with nothing selected', async () => {
        const editor = await testBed.page.$('#empty');
        await takeScreenshotWithMode(
          editor!,
          'gold',
          'edit-ignore-rule-sk',
          mode
        );
      });

      it('has all inputs filled out', async () => {
        const editor = await testBed.page.$('#filled');
        await takeScreenshotWithMode(
          editor!,
          'gold',
          'edit-ignore-rule-sk_with-data',
          mode
        );
      });

      it('shows an error when missing data', async () => {
        const editor = await testBed.page.$('#missing');
        await takeScreenshotWithMode(
          editor!,
          'gold',
          'edit-ignore-rule-sk_missing-data',
          mode
        );
      });

      it('shows an error when one or more of custom key/value is not filled out', async () => {
        const editor = await testBed.page.$('#partial_custom_values');
        await takeScreenshotWithMode(
          editor!,
          'gold',
          'edit-ignore-rule-sk_missing-custom-value',
          mode
        );
      });
    });
  });
});

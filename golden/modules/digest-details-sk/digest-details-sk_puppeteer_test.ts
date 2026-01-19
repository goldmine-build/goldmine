import { expect } from 'chai';
import {
  loadCachedTestBed,
  ModeOption,
  Modes,
  takeScreenshotWithMode,
  TestBed,
} from '../../../puppeteer-tests/util';

describe('digest-details-sk', () => {
  let testBed: TestBed;
  before(async () => {
    testBed = await loadCachedTestBed();
  });

  beforeEach(async () => {
    await testBed.page.goto(testBed.baseUrl, { waitUntil: 'networkidle0' });
  });

  it('should render the demo page', async () => {
    // Smoke test.
    expect(await testBed.page.$$('digest-details-sk')).to.have.length(11);
  });

  describe('screenshots', () => {
    Modes.forEach(async (mode: ModeOption) => {
      it('has the left and right image', async () => {
        const digestDetailsSk = await testBed.page.$('#normal');
        await takeScreenshotWithMode(
          digestDetailsSk!,
          'gold',
          'digest-details-sk',
          mode
        );
      });

      it('has the left and right image with triaging disallowed', async () => {
        const digestDetailsSk = await testBed.page.$(
          '#normal_disallow_triaging'
        );
        await takeScreenshotWithMode(
          digestDetailsSk!,
          'gold',
          'digest-details-sk_disallow-triaging',
          mode
        );
      });

      it('was given data with only a negative image to compare against', async () => {
        const digestDetailsSk = await testBed.page.$('#negative_only');
        await takeScreenshotWithMode(
          digestDetailsSk!,
          'gold',
          'digest-details-sk_negative-only',
          mode
        );
      });

      it('was given no other images to compare against', async () => {
        const digestDetailsSk = await testBed.page.$('#no_refs');
        await takeScreenshotWithMode(
          digestDetailsSk!,
          'gold',
          'digest-details-sk_no-refs',
          mode
        );
      });

      it('was given no other images to compare against with triaging disallowed', async () => {
        const digestDetailsSk = await testBed.page.$(
          '#no_refs_disallow_triaging'
        );
        await takeScreenshotWithMode(
          digestDetailsSk!,
          'gold',
          'digest-details-sk_no-refs-disallow-triaging',
          mode
        );
      });

      it('is computing the closest positive and negative', async () => {
        const digestDetailsSk = await testBed.page.$('#no_refs_yet');
        await takeScreenshotWithMode(
          digestDetailsSk!,
          'gold',
          'digest-details-sk_computing-refs',
          mode
        );
      });

      it('was given a changelist id', async () => {
        const digestDetailsSk = await testBed.page.$('#changelist_id');
        await takeScreenshotWithMode(
          digestDetailsSk!,
          'gold',
          'digest-details-sk_changelist-id',
          mode
        );
      });

      it('had the right side overridden', async () => {
        const digestDetailsSk = await testBed.page.$('#right_overridden');
        await takeScreenshotWithMode(
          digestDetailsSk!,
          'gold',
          'digest-details-sk_right-overridden',
          mode
        );
      });

      it('had no trace data sent by the backend', async () => {
        const digestDetailsSk = await testBed.page.$('#no_traces');
        await takeScreenshotWithMode(
          digestDetailsSk!,
          'gold',
          'digest-details-sk_no-traces',
          mode
        );
      });

      it('had no params sent by the backend', async () => {
        const digestDetailsSk = await testBed.page.$('#no_params');
        await takeScreenshotWithMode(
          digestDetailsSk!,
          'gold',
          'digest-details-sk_no-params',
          mode
        );
      });

      it('shows full size images', async () => {
        const digestDetailsSk = await testBed.page.$('#full_size_images');
        await takeScreenshotWithMode(
          digestDetailsSk!,
          'gold',
          'digest-details-sk_full-size-images',
          mode
        );
      });
    });
  });
});

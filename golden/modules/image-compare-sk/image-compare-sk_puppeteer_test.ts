import { expect } from 'chai';
import {
  loadCachedTestBed,
  ModeOption,
  Modes,
  takeScreenshotWithMode,
  TestBed,
} from '../../../puppeteer-tests/util';
import { ImageCompareSkPO } from './image-compare-sk_po';

describe('image-compare-sk', () => {
  let testBed: TestBed;

  before(async () => {
    testBed = await loadCachedTestBed();
  });

  beforeEach(async () => {
    await testBed.page.goto(testBed.baseUrl, { waitUntil: 'networkidle0' });
  });

  it('should render the demo page', async () => {
    // Smoke test.
    expect(await testBed.page.$$('image-compare-sk')).to.have.length(3);
  });

  describe('screenshots', () => {
    Modes.forEach(async (mode: ModeOption) => {
      it('has the left and right image', async () => {
        const imageCompareSk = await testBed.page.$('#normal');
        await takeScreenshotWithMode(
          imageCompareSk!,
          'gold',
          'image-compare-sk',
          mode
        );
      });

      it('shows the multi-zoom-sk dialog when zoom button clicked', async () => {
        await testBed.page.setViewport({ width: 1000, height: 800 });
        await testBed.page.click('#normal button.zoom_btn');
        await takeScreenshotWithMode(
          testBed.page,
          'gold',
          'image-compare-sk_zoom-dialog',
          mode
        );
      });

      it('has just the left image', async () => {
        const imageCompareSk = await testBed.page.$('#no_right');
        await takeScreenshotWithMode(
          imageCompareSk!,
          'gold',
          'image-compare-sk_no-right',
          mode
        );
      });

      it('shows full size images', async () => {
        const imageCompareSk = await testBed.page.$('#full_size_images');
        await takeScreenshotWithMode(
          imageCompareSk!,
          'gold',
          'image-compare-sk_full-size-images',
          mode
        );
      });

      it('zooms in and out of a specific image', async () => {
        const imageCompareSk = await testBed.page.$('#normal');
        const imageCompareSkPO = new ImageCompareSkPO(imageCompareSk!);
        await imageCompareSkPO.clickImage(0);
        await takeScreenshotWithMode(
          imageCompareSk!,
          'gold',
          'image-compare-sk_image-zoomed-in',
          mode
        );
        await imageCompareSkPO.clickImage(0);
        await takeScreenshotWithMode(
          imageCompareSk!,
          'gold',
          'image-compare-sk_image-zoomed-out',
          mode
        );
      });
    });
  });
});

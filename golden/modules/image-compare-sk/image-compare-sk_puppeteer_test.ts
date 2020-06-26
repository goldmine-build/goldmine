import { expect } from 'chai';
import { takeScreenshot, TestBed } from '../../../puppeteer-tests/util';
import { loadGoldWebpack } from '../common_puppeteer_test/common_puppeteer_test';

describe('image-compare-sk', () => {
  let testBed: TestBed;
  before(async () => {
    testBed = await loadGoldWebpack();
  });

  beforeEach(async () => {
    await testBed.page.goto(`${testBed.baseUrl}/dist/image-compare-sk.html`, { waitUntil: 'networkidle0' });
  });

  it('should render the demo page', async () => {
    // Smoke test.
    expect(await testBed.page.$$('image-compare-sk')).to.have.length(2);
  });

  describe('screenshots', () => {
    it('has the left and right image', async () => {
      const imageCompareSk = await testBed.page.$('#normal');
      await takeScreenshot(imageCompareSk!, 'gold', 'image-compare-sk');
    });

    it('shows the multi-zoom-sk dialog when zoom button clicked', async () => {
      await testBed.page.setViewport({ width: 1000, height: 800 });
      await testBed.page.click('#normal button.zoom_btn');
      await takeScreenshot(testBed.page, 'gold', 'image-compare-sk_zoom-dialog');
    });

    it('has just the left image', async () => {
      const imageCompareSk = await testBed.page.$('#no_right');
      await takeScreenshot(
        imageCompareSk!, 'gold', 'image-compare-sk_no-right',
      );
    });
  });
});

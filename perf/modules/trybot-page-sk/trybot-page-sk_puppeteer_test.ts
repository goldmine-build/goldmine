import * as path from 'path';
import { expect } from 'chai';
import {
  inBazel,
  loadCachedTestBed, takeScreenshot, TestBed,
} from '../../../puppeteer-tests/util';

describe('trybot-page-sk', () => {
  let testBed: TestBed;
  before(async () => {
    testBed = await loadCachedTestBed(
        path.join(__dirname, '..', '..', 'webpack.config.ts')
    );
  });

  beforeEach(async () => {
    await testBed.page.goto(
      inBazel() ? testBed.baseUrl : `${testBed.baseUrl}/dist/trybot-page-sk.html`);
    await testBed.page.setViewport({ width: 800, height: 1024 });
  });

  it('should render the demo page (smoke test)', async () => {
    expect(await testBed.page.$$('trybot-page-sk')).to.have.length(1);
  });

  describe('screenshots', () => {
    it('shows the default view', async () => {
      await testBed.page.waitForSelector('#load-complete pre');
      await takeScreenshot(testBed.page, 'perf', 'trybot-page-sk');
    });
  });
});

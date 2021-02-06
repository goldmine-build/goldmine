import * as path from 'path';
import { expect } from 'chai';
import {
  inBazel,
  loadCachedTestBed,
  takeScreenshot,
  TestBed,
} from '../../../puppeteer-tests/util';

describe('uniform-dimensions-sk', () => {
  let testBed: TestBed;
  before(async () => {
    testBed = await loadCachedTestBed(
      path.join(__dirname, '..', '..', 'webpack.config.ts'),
    );
  });

  beforeEach(async () => {
    await testBed.page.goto(
      inBazel()
        ? testBed.baseUrl
        : `${testBed.baseUrl}/uniform-dimensions-sk.html`,
    );
    await testBed.page.setViewport({ width: 400, height: 550 });
  });

  it('should render the demo page (smoke test)', async () => {
    expect(await testBed.page.$$('uniform-dimensions-sk')).to.have.length(1);
  });

  describe('screenshots', () => {
    it('shows the default view', async () => {
      await takeScreenshot(testBed.page, 'infra-sk', 'uniform-dimensions-sk');
    });
  });
});

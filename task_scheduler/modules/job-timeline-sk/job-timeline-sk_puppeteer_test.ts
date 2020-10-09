import * as path from 'path';
import { expect } from 'chai';
import {
  setUpPuppeteerAndDemoPageServer,
  takeScreenshot,
} from '../../../puppeteer-tests/util';

describe('job-timeline-sk', () => {
  const testBed = setUpPuppeteerAndDemoPageServer(
    path.join(__dirname, '..', '..', 'webpack.config.ts')
  );

  beforeEach(async () => {
    await testBed.page.goto(`${testBed.baseUrl}/dist/job-timeline-sk.html`);
    await testBed.page.setViewport({ width: 700, height: 359 });
  });

  it('should render the demo page (smoke test)', async () => {
    expect(await testBed.page.$$('job-timeline-sk')).to.have.length(1);
  });

  describe('screenshots', () => {
    it('shows the default view', async () => {
      await takeScreenshot(testBed.page, 'task-scheduler', 'job-timeline-sk');
    });
  });
});

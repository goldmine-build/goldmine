const expect = require('chai').expect;
const path = require('path');
const addEventListenersToPuppeteerPage = require('../../../puppeteer-tests/util').addEventListenersToPuppeteerPage;
const setUpPuppeteerAndDemoPageServer = require('../../../puppeteer-tests/util').setUpPuppeteerAndDemoPageServer;
const takeScreenshot = require('../../../puppeteer-tests/util').takeScreenshot;

describe('diff-page-sk', () => {
  // Contains page and baseUrl.
  const testBed = setUpPuppeteerAndDemoPageServer(path.join(__dirname, '..', '..', 'webpack.config.js'));

  const baseParams = '?left=6246b773851984c726cb2e1cb13510c2&right=99c58c7002073346ff55f446d47d6311&test=My%20test%20has%20spaces';

  it('should render the demo page', async () => {
    await navigateTo(testBed.page, testBed.baseUrl, baseParams);
    // Smoke test.
    expect(await testBed.page.$$('diff-page-sk')).to.have.length(1);
  });

  describe('screenshots', () => {
    it('should show the default page', async () => {
      await navigateTo(testBed.page, testBed.baseUrl, baseParams);
      await testBed.page.setViewport({
        width: 1300,
        height: 700,
      });
      await takeScreenshot(testBed.page, 'gold', 'diff-page-sk');
    });
  });

  describe('url params', () => {
    it('correctly extracts the grouping, left and right digest', async () => {
      await navigateTo(testBed.page, testBed.baseUrl, baseParams);
      const diffPageSk = await testBed.page.$('diff-page-sk');

      expect(await getPropertyAsJSON(diffPageSk, '_grouping')).to.equal('My test has spaces');
      expect(await getPropertyAsJSON(diffPageSk, '_leftDigest')).to.equal('6246b773851984c726cb2e1cb13510c2');
      expect(await getPropertyAsJSON(diffPageSk, '_rightDigest')).to.equal('99c58c7002073346ff55f446d47d6311');
      expect(await getPropertyAsJSON(diffPageSk, '_changeListID')).to.equal('');
    });

    it('correctly extracts the changelistID (issue) if provided', async () => {
      await navigateTo(testBed.page, testBed.baseUrl, `${baseParams}&issue=65432`);
      const diffPageSk = await testBed.page.$('diff-page-sk');

      expect(await getPropertyAsJSON(diffPageSk, '_grouping')).to.equal('My test has spaces');
      expect(await getPropertyAsJSON(diffPageSk, '_leftDigest')).to.equal('6246b773851984c726cb2e1cb13510c2');
      expect(await getPropertyAsJSON(diffPageSk, '_rightDigest')).to.equal('99c58c7002073346ff55f446d47d6311');
      expect(await getPropertyAsJSON(diffPageSk, '_changeListID')).to.equal('65432');

      const digestDetails = await testBed.page.$('diff-page-sk digest-details-sk');
      expect(await getPropertyAsJSON(digestDetails, '_issue')).to.equal('65432');
    });
  });
});

async function navigateTo(page, base, queryParams = '') {
  const eventPromise = await addEventListenersToPuppeteerPage(page, ['busy-end']);
  const loaded = eventPromise('busy-end'); // Emitted from gold-scaffold when page is loaded.
  await page.goto(`${base}/dist/diff-page-sk.html${queryParams}`);
  await loaded;
}

async function getPropertyAsJSON(ele, propName) {
  const prop = await ele.getProperty(propName);
  return prop.jsonValue();
}

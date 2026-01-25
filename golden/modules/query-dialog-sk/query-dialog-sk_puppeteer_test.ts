import { expect } from 'chai';
import {
  loadCachedTestBed,
  ModeOption,
  Modes,
  takeScreenshotWithMode,
  TestBed,
} from '../../../puppeteer-tests/util';
import { QueryDialogSkPO } from './query-dialog-sk_po';

describe('query-dialog-sk', () => {
  let queryDialogSkPO: QueryDialogSkPO;

  let testBed: TestBed;

  before(async () => {
    testBed = await loadCachedTestBed();
  });

  beforeEach(async () => {
    await testBed.page.goto(testBed.baseUrl);
    queryDialogSkPO = new QueryDialogSkPO(
      (await testBed.page.$('query-dialog-sk'))!
    );
  });

  it('should render the demo page', async () => {
    // Smoke test.
    expect(await testBed.page.$$('query-dialog-sk')).to.have.length(1);
  });

    Modes.forEach(async (mode: ModeOption) => {

  it('is initially empty', async () => {
    await testBed.page.click('#show-dialog');
    await takeScreenshotWithMode(testBed.page, 'gold', 'query-dialog-sk_no-selection', mode);
  });

  it('can select a key', async () => {
    await testBed.page.click('#show-dialog');
    await queryDialogSkPO.clickKey('car make');
    await takeScreenshotWithMode(testBed.page, 'gold', 'query-dialog-sk_key-selected', mode);
  });

  it('can select a key and a value', async () => {
    await testBed.page.click('#show-dialog');
    await queryDialogSkPO.clickKey('car make');
    await queryDialogSkPO.clickValue('chevrolet');
    await takeScreenshotWithMode(
      testBed.page,
      'gold',
      'query-dialog-sk_key-and-value-selected'
    , mode);
  });

  it('can select multiple values', async () => {
    await testBed.page.click('#show-dialog');
    await queryDialogSkPO.setSelection({
      'car make': ['chevrolet', 'dodge', 'ford'],
      color: ['blue'],
      used: ['yes', 'no'],
      year: ['2020', '2019', '2018', '2017'],
    });
    await takeScreenshotWithMode(
      testBed.page,
      'gold',
      'query-dialog-sk_multiple-values-selected'
    , mode);
  });

  it('can be opened with an initial non-empty selection', async () => {
    await testBed.page.click('#show-dialog-with-selection');
    await takeScreenshotWithMode(
      testBed.page,
      'gold',
      'query-dialog-sk_nonempty-initial-selection'
    , mode);
  });
});
});

import { expect } from 'chai';
import {
  loadCachedTestBed,
  ModeOption,
  Modes,
  takeScreenshotWithMode,
  TestBed,
} from '../../../puppeteer-tests/util';
import { TraceFilterSkPO } from './trace-filter-sk_po';

describe('trace-filter-sk', () => {
  let traceFilterSkPO: TraceFilterSkPO;

  let testBed: TestBed;

  before(async () => {
    testBed = await loadCachedTestBed();
  });

  beforeEach(async () => {
    await testBed.page.goto(testBed.baseUrl);
    traceFilterSkPO = new TraceFilterSkPO(
      (await testBed.page.$('trace-filter-sk'))!
    );
  });

  it('should render the demo page', async () => {
    // Basic smoke test that things loaded.
    expect(await testBed.page.$$('trace-filter-sk')).to.have.length(1);
  });

  Modes.forEach(async (mode: ModeOption) => {
    describe('empty selection', () => {
      beforeEach(async () => {
        await traceFilterSkPO.clickEditBtn();
        await traceFilterSkPO.setQueryDialogSkSelection({});
        await traceFilterSkPO.clickQueryDialogSkShowMatchesBtn();
      });

      it('shows the user input', async () => {
        await takeScreenshotWithMode(
          testBed.page,
          'gold',
          'trace-filter-sk',
          mode
        );
      });

      it('opens the query dialog', async () => {
        await traceFilterSkPO.clickEditBtn();
        const queryDialogSkPO = await traceFilterSkPO.queryDialogSkPO;
        await queryDialogSkPO.clickKey('car make');
        await takeScreenshotWithMode(
          testBed.page,
          'gold',
          'trace-filter-sk_query-dialog-open',
          mode
        );
      });
    });

    describe('non-empty selection', () => {
      it('shows the user input', async () => {
        await takeScreenshotWithMode(
          testBed.page,
          'gold',
          'trace-filter-sk_nonempty',
          mode
        );
      });

      it('opens the query dialog', async () => {
        await traceFilterSkPO.clickEditBtn();
        const queryDialogSkPO = await traceFilterSkPO.queryDialogSkPO;
        await queryDialogSkPO.clickKey('car make');
        await takeScreenshotWithMode(
          testBed.page,
          'gold',
          'trace-filter-sk_nonempty_query-dialog-open',
          mode
        );
      });
    });
  });
});

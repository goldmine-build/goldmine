import { expect } from 'chai';
import { ElementHandle } from 'puppeteer';
import {
  loadCachedTestBed,
  ModeOption,
  Modes,
  takeScreenshotWithMode,
  TestBed,
} from '../../../puppeteer-tests/util';
import { BulkTriageSkPO } from './bulk-triage-sk_po';

describe('bulk-triage-sk', () => {
  let bulkTriageSk: ElementHandle;
  let bulkTriageSkPO: BulkTriageSkPO;

  let testBed: TestBed;

  before(async () => {
    testBed = await loadCachedTestBed();
  });

  beforeEach(async () => {
    await testBed.page.goto(testBed.baseUrl);

    bulkTriageSk = (await testBed.page.$('#default'))!;
    bulkTriageSkPO = new BulkTriageSkPO(bulkTriageSk);
  });

  it('should render the demo page', async () => {
    expect(await testBed.page.$$('bulk-triage-sk')).to.have.length(2); // Smoke test.
  });

  describe('screenshots', async () => {
    Modes.forEach(async (mode: ModeOption) => {
      it('should be closest by default', async () => {
        await takeScreenshotWithMode(
          bulkTriageSk,
          'gold',
          'bulk-triage-sk_closest',
          mode
        );
      });

      it('should be negative', async () => {
        await bulkTriageSkPO.clickUntriagedBtn();
        await testBed.page.click('body'); // Remove focus from button.
        await takeScreenshotWithMode(
          bulkTriageSk,
          'gold',
          'bulk-triage-sk_untriaged',
          mode
        );
      });

      it('should be negative', async () => {
        await bulkTriageSkPO.clickNegativeBtn();
        await testBed.page.click('body'); // Remove focus from button.
        await takeScreenshotWithMode(
          bulkTriageSk,
          'gold',
          'bulk-triage-sk_negative',
          mode
        );
      });

      it('should be positive', async () => {
        await bulkTriageSkPO.clickPositiveBtn();
        await testBed.page.click('body'); // Remove focus from button.
        await takeScreenshotWithMode(
          bulkTriageSk,
          'gold',
          'bulk-triage-sk_positive',
          mode
        );
      });

      it('should be positive, with button focused', async () => {
        await bulkTriageSkPO.clickPositiveBtn();
        await takeScreenshotWithMode(
          bulkTriageSk,
          'gold',
          'bulk-triage-sk_positive-button-focused',
          mode
        );
      });

      it('changes views when checkbox clicked', async () => {
        await bulkTriageSkPO.clickTriageAllCheckbox();
        await takeScreenshotWithMode(
          bulkTriageSk,
          'gold',
          'bulk-triage-sk_triage-all',
          mode
        );
      });

      it('shows some extra information for changelists', async () => {
        const bulkTriageSkWithCL = await testBed.page.$('#changelist');
        await takeScreenshotWithMode(
          bulkTriageSkWithCL!,
          'gold',
          'bulk-triage-sk_changelist',
          mode
        );
      });
    });
  });
});

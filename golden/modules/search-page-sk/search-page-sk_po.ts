import { PageObject } from '../../../infra-sk/modules/page_object/page_object';
import { SearchControlsSkPO } from '../search-controls-sk/search-controls-sk_po';
import { ChangelistControlsSkPO } from '../changelist-controls-sk/changelist-controls-sk_po';
import { BulkTriageSkPO } from '../bulk-triage-sk/bulk-triage-sk_po';

/** A page object for the SearchPageSkPO component. */
export class SearchPageSkPO extends PageObject {
  getBulkTriageSkPO() {
    return this.selectOnePOEThenApplyFn('bulk-triage-sk', async (el) => new BulkTriageSkPO(el));
  }

  getSearchControlsSkPO() {
    return this.selectOnePOEThenApplyFn(
      'search-controls-sk', async (el) => new SearchControlsSkPO(el));
  }

  getChangelistControlsSkPO() {
    return this.selectOnePOEThenApplyFn(
      'changelist-controls-sk', async (el) => new ChangelistControlsSkPO(el));
  }

  async clickBulkTriageBtn() {
    await this.selectOnePOEThenApplyFn('button.bulk-triage', (el) => el.click());
  }

  isBulkTriageDialogOpen() {
    return this.selectOnePOEThenApplyFn('dialog.bulk-triage', (el) => el.hasAttribute('open'))
  }

  async clickHelpBtn() {
    await this.selectOnePOEThenApplyFn('button.help', (el) => el.click());
  }

  async clickHelpDialogCancelBtn() {
    await this.selectOnePOEThenApplyFn('dialog.help button.cancel', (el) => el.click());
  }

  isHelpDialogOpen() {
    return this.selectOnePOEThenApplyFn('dialog.help', (el) => el.hasAttribute('open'))
  }

  getSummary() {
    return this.selectOnePOEThenApplyFn('p.summary', (el) => el.innerText);
  }

  // TODO(lovisolo): Replace with DigestDetailsSkPO when DigestDetailsSk is ported to TypeScript
  //                 and tested with a page object.
  getDigests() {
    return this.selectAllPOEThenMap('.digest_label:nth-child(1)', (el) => el.innerText);
  }

  // TODO(lovisolo): Replace with DigestDetailsSkPO when DigestDetailsSk is ported to TypeScript
  //                 and tested with a page object.
  getDiffDetailsHrefs() {
    return this.selectAllPOEThenMap('.diffpage_link', (el) => el.href);
  }
};

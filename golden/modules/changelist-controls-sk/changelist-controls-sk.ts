import { define } from 'elements-sk/define';
import { html } from 'lit-html';
import { ElementSk } from '../../../infra-sk/modules/ElementSk';
import { truncateWithEllipses } from '../common';

import 'elements-sk/radio-sk';
import 'elements-sk/styles/select';
import 'elements-sk/icon/find-in-page-icon-sk';
import { ChangelistSummaryResponse, TryJob } from '../rpc_types';

export interface ChangelistControlsSkChangeEventDetail {
  readonly include_master: boolean;
  readonly ps_order: number;
};

export class ChangelistControlsSk extends ElementSk {

  private static template = (ele: ChangelistControlsSk) => {
    if (!ele._summary) {
      return '';
    }
    const cl = ele._summary.cl;
    const ps = ele._getSelectedPatchset();
    return html`
      <div class=info>
        <span class=title>${cl.system} changelist:</span>
        <a href=${cl.url} target=_blank rel=noopener>
          ${truncateWithEllipses(cl.subject, 48)}
        </a>

        <span>${truncateWithEllipses(cl.owner, 32)}</span>

        <a href="/triagelog?changelist_id=${cl.id}&crs=${cl.system}">
          <find-in-page-icon-sk></find-in-page-icon-sk>Triagelog
        </a>
      </div>

      <div class=inputs>
        <select @input=${ele._onSelectPS}>
          ${ele._summary.patch_sets.map(
            (ps) => html`<option ?selected=${ele.ps_order === ps.order}>PS ${ps.order}</option>`)}
        </select>
        <span class=spacer></span>
        <div class=radiogroup>
          <radio-sk label="exclude results from primary branch"
                    class="exclude-master"
                    name=include_master
                    ?checked=${!ele.include_master}
                    @change=${() => ele._onIncludeDigestsFromPrimaryChange(false)}>
          </radio-sk>
          <radio-sk label="show all results"
                    class="include-master"
                    name=include_master
                    ?checked=${ele.include_master}
                    @change=${() => ele._onIncludeDigestsFromPrimaryChange(true)}>
          </radio-sk>
        </div>
      </div>

      <div class=tryjob-container>
        ${ps?.try_jobs.map((tj) => ChangelistControlsSk.tryJobTemplate(tj))}
      </div>
    `;
  };

  private static tryJobTemplate = (tj: TryJob) => html`
    <div class=tryjob title=${tj.name}>
      <a href=${tj.url} target=_blank rel=noopener>
        ${truncateWithEllipses(tj.name, 60)}
      </a>
    </div>
  `;

  private _psOrder = 0; // Default to use the last patchset.
  private _includeDigestsFromPrimary = false;
  private _summary: ChangelistSummaryResponse | null = null;

  constructor() {
    super(ChangelistControlsSk.template);
  }

  connectedCallback() {
    super.connectedCallback();
    this._render();
  }

  /** Changelist summary for this element to display. */
  get summary() { return this._summary; }

  set summary(summary: ChangelistSummaryResponse | null) {
    this._summary = summary;
    this._render();
  }

  /**
   * Deprecated. Equivalent to the summary property setter.
   *
   * TODO(lovisolo): Remove after the legacy search page is deleted.
   */
  setSummary(summary: ChangelistSummaryResponse) {
    this._summary = summary;
    this._render();
  }

  /**
   * The order of the patchset currently being shown. If set to 0, the latest patchset will be used.
   */
  get ps_order() { return this._psOrder; }

  set ps_order(val) {
    this._psOrder = +val;
    this._render();
  }

  /**
   * Whether to show results that are also on the primary branch, as opposed to those that are
   * exclusive.
   */
  get include_master() { return this._includeDigestsFromPrimary; }

  set include_master(val) {
    this._includeDigestsFromPrimary = (val as unknown as string) !== 'false' && !!val;
    this._render();
  }

  _onIncludeDigestsFromPrimaryChange(newVal: boolean) {
    this.include_master = newVal; // calls _render()
    this._sendUpdateEvent();
  }

  _onSelectPS(e: InputEvent) {
    const selectedIndex = (e.target! as HTMLSelectElement).selectedIndex;
    const xps = this._summary!.patch_sets;
    const ps = xps[selectedIndex];
    this.ps_order = ps.order; // calls _render()
    this._sendUpdateEvent();
  }

  /**
   * Returns the Patchset object which matches _psOrder. if _psOrder is 0 (match latest), _psOrder
   * will be updated to whatever the latest order is.
   */
  _getSelectedPatchset() {
    if (!this._summary?.patch_sets?.length) {
      return null;
    }
    const xps = this._summary.patch_sets;
    if (!this._psOrder) {
      const o = xps[xps.length - 1];
      this._psOrder = o.order;
      return o;
    }
    for (let i = 0; i < xps.length; i++) {
      if (xps[i].order === this._psOrder) {
        return xps[i];
      }
    }
    return null;
  }

  _sendUpdateEvent() {
    this.dispatchEvent(new CustomEvent<ChangelistControlsSkChangeEventDetail>('cl-control-change', {
      detail: {
        include_master: this.include_master,
        ps_order: this.ps_order,
      },
      bubbles: true,
    }));
  }
};

define('changelist-controls-sk', ChangelistControlsSk);

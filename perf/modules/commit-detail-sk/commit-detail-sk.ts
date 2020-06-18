/**
 * @module modules/commit-detail-sk
 * @description <h2><code>commit-detail-sk</code></h2>
 *
 * An element to display information around a single commit.
 *
 */
import { define } from 'elements-sk/define';
import { html } from 'lit-html';
import { $$ } from 'common-sk/modules/dom';
import { upgradeProperty } from 'elements-sk/upgradeProperty';
import { ElementSk } from '../../../infra-sk/modules/ElementSk';
import { CommitDetail } from '../json';

export class CommitDetailSk extends ElementSk {
  private static template = (ele: CommitDetailSk) => html`<div
      @click=${() => ele._click()}
      class="linkish"
    >
      <pre>${ele.cid.message}</pre>
    </div>
    <div class="tip hidden">
      <a href="/g/e/${ele.cid.hash}">Explore</a>
      <a href="/g/c/${ele.cid.hash}">Cluster</a>
      <a href="/g/t/${ele.cid.hash}">Triage</a>
      <a href="${ele.cid.url}">Commit</a>
    </div>`;

  private _cid: CommitDetail;

  constructor() {
    super(CommitDetailSk.template);
    this._cid = {
      author: '',
      message: '',
      url: '',
      ts: 0,
      hash: '',
      CommitID: {
        offset: 0,
      },
    };
  }

  connectedCallback() {
    super.connectedCallback();
    upgradeProperty(this, 'cid');
    this._render();
  }

  _click() {
    $$('.tip', this)!.classList.toggle('hidden');
  }

  /** @prop cid - The details about a commit. */
  get cid() {
    return this._cid;
  }

  set cid(val) {
    this._cid = val;
    this._render();
  }
}

define('commit-detail-sk', CommitDetailSk);

/**
 * @module module/digest-details-sk
 * @description <h2><code>digest-details-sk</code></h2>
 *
 * Displays the details about a digest. These details include comparing it to other digests in the
 * same grouping (e.g. test), if those are available. It provides the affordances to triage the
 * given digest and makes the POST request to triage this given digest.
 *
 * <h2>Events</h2>
 *   This element produces the following events:
 * @evt begin-task/end-task - when a POST request is in flight to handle triaging.
 *
 *   Children elements emit the following events of note:
 * @evt show-commits - Event generated when a trace dot is clicked. e.detail contains
 *   the blamelist (an array of commits that could have made up that dot).
 *
 */
import { define } from 'elements-sk/define';
import { html } from 'lit-html';
import { errorMessage } from 'elements-sk/errorMessage';
import { ElementSk } from '../../../infra-sk/modules/ElementSk';
import { $$ } from '../../../common-sk/modules/dom';
import { fromParamSet, fromObject } from '../../../common-sk/modules/query';
import {
  truncateWithEllipses, detailHref, diffPageHref, sendBeginTask, sendEndTask, sendFetchError,
} from '../common';

import 'elements-sk/icon/group-work-icon-sk';
import '../dots-sk';
import '../dots-legend-sk';
import '../triage-sk';
import '../triage-history-sk';
import '../image-compare-sk';
import '../../../infra-sk/modules/paramset-sk';

const template = (ele) => html`
<div class=container>
  <div class=top_bar>
    <span class=grouping_name>Test: ${ele._grouping}</span>
    <span class=expand></span>
    <a href=${ele._clusterHref()} target=_blank rel=noopener class=cluster_link>
      <group-work-icon-sk title="Cluster view of this digest and all others for this test.">
      </group-work-icon-sk>
    </a>
  </div>

  <div class=comparison>
    <div class=digest_labels>
      <span class=digest_label>Left: ${ele._digest}</span>
      <span class=expand></span>
      <span class=digest_label ?hidden=${!ele.right}>Right: ${ele.right && ele.right.digest}</span>
    </div>
    <div class=comparison_data>
      <div>${detailsAndTriage(ele)}</div>
      <div>${imageComparison(ele)}</div>
      <div>
        <button @click=${ele._toggleRightRef} ?disabled=${!ele._canToggle()} class=toggle_ref
           ?hidden=${ele._overrideRight || !ele.right} title=${toggleButtonMouseover(ele._canToggle())}>
           Toggle Reference
        </button>
        <div ?hidden=${!ele.right || ele.right.status !== 'negative'} class=negative_warning>
          Closest image is negative!
        </div>
        <!-- TODO(kjlubick) Comments would go here -->
      </div>
    </div>
  </div>
${traceInfo(ele)}
${paramset(ele)}
</div>`;

const detailsAndTriage = (ele) => {
  if (!ele.right) {
    return html`
<div class=metrics_and_triage>
  <triage-sk @change=${ele._triageChangeHandler} .value=${ele._status}></triage-sk>
  <triage-history-sk .history=${ele._triageHistory}></triage-history-sk>
</div>`;
  }

  // TODO(kjlubick) would it be clearer to just tell the user the images differ in size and omit
  //  the (probably useless metrics)? Could we also include the actual dimensions of the two?

  return html`
<div class=metrics_and_triage>
  <div>
    <a href=${diffPageHref(ele._grouping, ele._digest, ele.right.digest, ele.issue)}
       target=_blank rel=noopener class=diffpage_link>
      Diff Details
    </a>
  </div>
  <div class=size_warning ?hidden=${!ele.right.dimDiffer}>Images differ in size!</div>
  <div class=metric>
    <span>Diff metric:</span>
    <span>${ele.right.diffs.combined.toFixed(3)}</span>
  </div>
  <div class=metric>
    <span>Diff %:</span>
    <span>${ele.right.pixelDiffPercent.toFixed(2)}</span>
  </div>
  <div class=metric>
    <span>Pixels:</span>
    <span>${ele.right.numDiffPixels}</span>
  </div>
  <div class=metric>
    <span>Max RGBA:</span>
    <span>[${ele.right.maxRGBADiffs.join(',')}]</span>
  </div>
  <triage-sk @change=${ele._triageChangeHandler} .value=${ele._status}></triage-sk>
  <triage-history-sk .history=${ele._triageHistory}></triage-history-sk>
</div>`;
};

const imageComparison = (ele) => {
  const left = {
    digest: ele._digest,
    title: truncateWithEllipses(ele._digest),
    detail: detailHref(ele._grouping, ele._digest, ele.issue),
  };
  if (!ele.right) {
    return html`<image-compare-sk .left=${left}></image-compare-sk>`;
  }
  const right = {
    digest: ele.right.digest,
    title: ele.right.status === 'positive' ? 'Closest Positive' : 'Closest Negative',
    detail: detailHref(ele._grouping, ele.right.digest, ele.issue),
  };
  return html`<image-compare-sk .left=${left} .right=${right}></image-compare-sk>`;
};

const traceInfo = (ele) => {
  if (!ele._traces || !ele._traces.traces || !ele._traces.traces.length) {
    return '';
  }
  return html`
<div class=trace_info>
  <dots-sk .value=${ele._traces} .commits=${ele._commits} @hover=${ele._hoverOverTrace}
      @mouseleave=${ele._clearTraceHighlights}></dots-sk>
  <dots-legend-sk .digests=${ele._traces.digests} .issue=${ele.issue} .test=${ele._grouping}
.totalDigests=${ele._traces.total_digests || 0}></dots-legend-sk>
</div>`;
};

const paramset = (ele) => {
  if (!ele._digest) {
    return ''; // details might not be loaded yet.
  }
  const input = {
    titles: [truncateWithEllipses(ele._digest)],
    paramsets: [ele._params],
  };

  if (ele.right) {
    input.titles.push(truncateWithEllipses(ele.right.digest));
    input.paramsets.push(ele.right.paramset);
  }
  return html`<paramset-sk .paramsets=${input} .highlight=${ele._highlightedParams}></paramset-sk>`;
};

function toggleButtonMouseover(canToggle) {
  if (canToggle) {
    return 'By default, Gold shows the closest image, whether it has been marked positive or '
    + 'negative. This button allows you to explicitly select the closest positive or negative.';
  }
  return 'There are no other reference image types to compare against.';
}

const validRefs = ['pos', 'neg'];

define('digest-details-sk', class extends ElementSk {
  constructor() {
    super(template);

    this._grouping = '';
    this._digest = '';
    this._status = 'untriaged';
    this._triageHistory = [];
    this._params = {};
    this._traces = null;
    this._refDiffs = {};
    this._issue = '';
    this._triageHistory = [];

    this._commits = [];

    // This tracks which ref we are showing on the right. It will default to the closest one, but
    // can be changed with the toggle.
    this._rightRef = '';
    this._overrideRight = null;

    this._highlightedParams = {};
  }

  connectedCallback() {
    super.connectedCallback();
    this._render();
  }

  /**
   * @prop commits {array} an array of the commits in the tile. Used to compute the blamelist for
   *   representing traces.
   */
  get commits() { return this._commits; }

  set commits(arr) {
    this._commits = arr;
    this._render();
  }

  /**
   * @prop details {object} an object with many parts. It has a setter for compatibility with
   *   Polymer implementation. It is write-only.
   */
  set details(obj) {
    this._grouping = obj.test || '';
    this._digest = obj.digest || '';
    this._traces = obj.traces || {};
    this._params = obj.paramset || {};
    this._refDiffs = obj.refDiffs || {};
    this._rightRef = obj.closestRef || '';
    this._status = obj.status || '';
    this._triageHistory = obj.triage_history || [];
    this._render();
  }

  /**
   * @prop issue {string} The changelist id (or empty string if this is the master branch).
   *   TODO(kjlubick) rename this to changelistID.
   */
  get issue() { return this._issue; }

  set issue(id) {
    this._issue = id;
    this._render();
  }

  get right() {
    if (this._overrideRight) {
      return this._overrideRight;
    }
    return this._refDiffs[this._rightRef] || null;
  }

  set right(override) {
    this._overrideRight = override;
    this._render();
  }

  _canToggle() {
    let totalRefs = 0;
    for (const ref of validRefs) {
      if (this._refDiffs[ref]) {
        totalRefs++;
      }
    }
    return totalRefs > 1;
  }

  _clearTraceHighlights() {
    this._highlightedParams = {};
    this._render();
  }

  _clusterHref() {
    if (!this._grouping) {
      return '';
    }

    const refQuery = fromParamSet({
      name: [this._grouping],
      // TODO(kjlubick) use corpus instead of source_type on the frontend.
      source_type: this._params.source_type,
    });

    const q = {
      query: refQuery,
      head: true,
      pos: true,
      neg: true,
      unt: true,
      limit: 200,
    };
    return `/cluster?${fromObject(q)}`;
  }

  _hoverOverTrace(e) {
    const id = e.detail;
    this._highlightedParams = {};
    const traces = this._traces.traces;

    // Find the matching trace in details.traces.
    for (let i = 0, len = traces.length; i < len; i++) {
      if (traces[i].label === id) {
        this._highlightedParams = traces[i].params;
        break;
      }
    }
    this._render();
  }

  _render() {
    super._render();
    // TODO(kjlubick,lovisolo) would it make sense to have dots-sk scroll itself when its data
    //   is updated?
    const traces = $$('dots-sk', this);
    if (traces) {
      // We have to wait until after the dots-sk is rendered to set this, otherwise the scrollWidth
      // won't be correct.
      traces.scroll(traces.scrollWidth, 0);
    }
  }

  _toggleRightRef() {
    if (!this._canToggle()) {
      return;
    }
    let idx = validRefs.indexOf(this._rightRef);
    let newRight = '';
    while (!this._refDiffs[newRight]) {
      idx = (idx + 1) % validRefs.length;
      newRight = validRefs[idx];
    }
    this._rightRef = newRight;
    this._render();
  }

  _triageChangeHandler(e) {
    e.stopPropagation();
    this.triggerTriage(e.detail);
  }

  /**
   * Triages the given digest with the new status.
   * @param newStatus {string} one of 'positive', 'negative', or 'untriaged'.
   */
  triggerTriage(newStatus) {
    const digestStatus = {};
    digestStatus[this._digest] = newStatus;
    const postBody = {
      testDigestStatus: {},
    };
    postBody.testDigestStatus[this._grouping] = digestStatus;
    if (this.issue) {
      postBody.issue = this.issue;
    }

    sendBeginTask(this);

    fetch('/json/triage', {
      method: 'POST',
      body: JSON.stringify(postBody),
      headers: {
        'Content-Type': 'application/json',
      },
    }).then((resp) => {
      if (resp.ok) {
        // Triaging was successful.
        this._status = newStatus;
        this._triageHistory.unshift({
          user: 'me',
          ts: Date.now(),
        });
        this._render();
        sendEndTask(this);
      } else {
        // Triaging did not work (possibly because the user was not logged in). We want to set
        // the status of the triage-sk back to what it was to give a visual indication it did not
        // go through. Additionally, toast error message should catch the user's attention.
        console.error(resp);
        errorMessage(`Unexpected error triaging: ${resp.status} ${resp.statusText} `
          + '(Are you logged in with the right account?)', 8000);
        $$('triage-sk', this).value = this._status;
        this._render();
        sendEndTask(this);
      }
    }).catch((e) => sendFetchError(this, e, 'triaging'));
  }
});

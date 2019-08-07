/**
 * @module module/cluster-page-sk
 * @description <h2><code>cluster-page-sk</code></h2>
 *
 *   The top level element for clustering traces.
 *
 */
import { ElementSk } from '../../../infra-sk/modules/ElementSk'
import { errorMessage } from 'elements-sk/errorMessage'
import { html, render } from 'lit-html'
import { jsonOrThrow } from 'common-sk/modules/jsonOrThrow'
import { stateReflector } from 'common-sk/modules/stateReflector'

import 'elements-sk/spinner-sk'
import 'elements-sk/checkbox-sk'
import 'elements-sk/styles/buttons'

import '../../../infra-sk/modules/sort-sk'

import '../algo-select-sk'
import '../cluster-summary2-sk'
import '../commit-detail-picker-sk'
import '../day-range-sk'
import '../query-count-sk'
import '../query-sk'
import '../query-summary-sk'

const _summaryRows = (ele) => {
  const ret = ele._summaries.map((summary) => {
    return html`<cluster-summary2-sk .full_summary=${summary} notriage></cluster-summary2-sk>`;
  });
  if (!ret.length) {
    ret.push(html`
      <p class=info>
        No clusters found.
      </p>
    `);
  }
  return ret;
}

const template = (ele) => html`
  <h2>Commit</h2>
  <h3>Appears in Date Range</h3>
  <div class=day-range-with-spinner>
    <day-range-sk
      id=range
      @day-range-change=${ele._rangeChange}
      begin=${ele._state.begin}
      end=${ele._state.end}
      ></day-range-sk>
    <spinner-sk ?active=${ele._updating_commits}></spinner-sk>
  </div>
  <h3>Commit</h3>
  <div>
    <commit-detail-picker-sk
      @commit-selected=${ele._commitSelected}
      .selected=${ele._selected_commit_index}
      .details=${ele._cids}
      id=commit
      ></commit-picker-sk>
  </div>

  <h2>Algorithm</h2>
  <algo-select-sk
    algo=${ele._state.algo}
    @algo-change=${(e) => ele._state.algo = e.detail.algo}
    ></algo-select-sk>

  <h2>Query</h2>
  <div class=query-action>
    <query-sk
      @query-change=${ele._queryChanged}
      .key_order=${sk.perf.key_order}
      .paramset=${ele._paramset}
      current_query=${ele._state.query}
      ></query-sk>
    <div id=selections>
      <h3>Selections</h3>
      <query-summary-sk id=summary selection=${ele._state.query}></query-summary-sk>
      <div>
        Matches:
          <query-count-sk
            url='/_/count/'
            current_query=${ele._state.query}
            @paramset-changed=${ele._paramsetChanged}
            ></query-count-sk>
      </div>
      <button @click=${ele._start} class=action id=start ?disabled=${!!ele._requestId} >
        Run
      </button>
      <div>
        <spinner-sk ?active=${!!ele._requestId}></spinner-sk>
        <span>${ele._status}</span>
      </div>
    </div>
  </div>

  <details>
    <summary id=advanced>
      <h2>Advanced</h2>
    </summary>
    <div id=inputs>
      <label>
        K (A value of 0 means the server chooses).
        <input
          type=number
          min=0
          max=100
          .value=${ele._state.k}
          @input=${(e) => ele._state.k = e.target.value}>
      </label>
      <label>
        Number of commits to include on either side.
        <input
          type=number
          min=1
          max=25
          .value=${ele._state.radius}
          @input=${(e) => ele._state.radius = e.target.value}>
      </label>
      <label>
        Clusters are interesting if regression score &gt;= this.
        <input
          type=number
          min=0
          max=500
          .value=${ele._state.interesting}
          @input=${(e) => ele._state.interesting = e.target.value}>
      </label>
      <checkbox-sk
        ?checked=${ele._state.sparse}
        @input=${(e) => ele._state.sparse = e.target.checked}
        label='Data is sparse, so only include commits that have data.'
        >
      </checkbox-sk>
    </div>
  </details>

  <h2>Results</h2>
  <sort-sk target=clusters>
    <button data-key=clustersize>Cluster Size </button>
    <button data-key=stepregression data-default=up>Regression </button>
    <button data-key=stepsize>Step Size </button>
    <button data-key=steplse>Least Squares</button>
  </sort-sk>
  <div id=clusters @open-keys=${ele._openKeys}>
    ${_summaryRows(ele)}
  </div>
  `;

window.customElements.define('cluster-page-sk', class extends ElementSk {
  constructor() {
    super(template);

    // The computed clusters.
    this._summaries = [];

    // The commits to choose from.
    this._cids = [];

    // Which commit is selected.
    this._selected_commit_index = -1;

    // The paramset to build queries from.
    this._paramset = {};

    // The id of the current cluster request. Will be the empty string if
    // there is no pending request.
    this._requestId = '';

    // The status of a running request.
    this._status = '';

    // True if we are fetching a new list of _cids from the server.
    this._updating_commits = false;

    // The state that gets reflected to the URL.
    this._state = {
      begin: Math.floor(Date.now()/1000 - 24*60*60),
      end: Math.floor(Date.now()/1000),
      source: '',
      offset: -1,
      radius: '' + sk.perf.radius,
      query: '',
      k: '0',
      algo: 'kmeans',
      interesting: '' + sk.perf.interesting,
      sparse: false,
    };

    // Only update _cids if the date range is different from the last fetch.
    this._lastRange = {
      begin: null,
      end: null,
    };
  }

  connectedCallback() {
    super.connectedCallback();
    this._render();
    this._clusters = this.querySelector('#clusters');

    const tz = Intl.DateTimeFormat().resolvedOptions().timeZone;
    fetch(`/_/initpage/?tz=${tz}`).then(jsonOrThrow).then((json) => {
      this._paramset = json.dataframe.paramset;
      this._render();
    }).catch(errorMessage);

    this._stateHasChanged = stateReflector(() => this._state, (state) => {
      this._state = state;
      this._render();
      this._updateCommitSelections();
    });
    // There are a lot of pieces of _state, so just keep the URL up to date by polling.
    this._keepURLUpdated();
  }

  _keepURLUpdated() {
    this._stateHasChanged();
    window.setTimeout(() => this._keepURLUpdated(), 100);
  }

  _queryChanged(e) {
    this._state.query = e.detail.q;
    this._render();
  }

  _paramsetChanged(e) {
    this._paramset = e.detail;
    this._render();
  }

  _openKeys(e) {
    const query = {
      keys:       e.detail.shortcut,
      begin:      e.detail.begin,
      end:        e.detail.end,
      xbaroffset: e.detail.xbar.offset,
      num_commits: 50,
      request_type: 1,
    };
    window.open(`/e/?${sk.query.fromObject(query)}`, '_blank');
  }

  _rangeChange(e) {
    this._state.begin = e.detail.begin;
    this._state.end = e.detail.end;
    this._updateCommitSelections();
  }

  _commitSelected(e) {
    this._state.source = e.detail.commit.source;
    this._state.offset = e.detail.commit.offset;
  }

  _updateCommitSelections() {
    if (this._lastRange.begin === this._state.begin && this._lastRange.end === this._state.end) {
      return;
    }
    this._lastRange = {
      begin: this._state.begin,
      end: this._state.end,
    };
    const body = {
      begin: this._state.begin,
      end: this._state.end,
      source: this._state.source,
      offset: this._state.offset,
    };
    this._updating_commits = true;
    fetch('/_/cidRange/', {
      method: 'POST',
      body: JSON.stringify(body),
      headers:{
        'Content-Type': 'application/json'
      }
    }).then(jsonOrThrow).then((cids) => {
      this._updating_commits = false;
      cids.reverse();
      this._cids = cids;

      this._selected_commit_index = -1;
      // Look for commit id in this._cids.
      for (let i = 0; i < cids.length; i++) {
        if (cids[i].source == this._state.source && cids[i].offset == this._state.offset) {
          this._selected_commit_index = i;
          break
        }
      }

      if (!this._state.begin) {
        this._state.begin   = cids[cids.length-1].ts;
        this._state.end     = cids[0].ts;
      }
      this._render();
    }).catch((msg) => {
      if (msg) {
        errorMessage(msg, 10000);
      }
      this._updating_commits = false;
      this._render();
    });
  }

  _catch(msg) {
    this._requestId = '';
    this._status = '';
    if (msg) {
      sk.errorMessage(msg, 10000);
    }
    this._render();
  }

  _checkClusterRequestStatus(cb) {
    fetch(`/_/cluster/status/${this._requestId}`).then(jsonOrThrow).then((json) => {
      if (json.state === 'Running') {
        this._status = json.message;
        this._render();
        window.setTimeout(() => this._checkClusterRequestStatus(cb), 300);
      } else {
        if (json.value) {
          cb(json.value);
        }
        this._catch(json.message);
      }
    }).catch(msg => this._catch(msg));
  }

  _start() {
    if (this._requestId) {
      errorMessage('There is a pending query already running.');
      return;
    }
    const body = {
      source: this._state.source,
      offset: this._state.offset,
      radius: +this._state.radius,
      query: this._state.query,
      k: +this._state.k,
      tz: Intl.DateTimeFormat().resolvedOptions().timeZone,
      algo: this._state.algo,
      interesting: +this._state.interesting,
      sparse: this._state.sparse,
    };
    this._summaries = [];
    fetch('/_/cluster/start', {
      method: 'POST',
      body: JSON.stringify(body),
      headers:{
        'Content-Type': 'application/json'
      }
    }).then(jsonOrThrow).then((json) => {
      this._requestId = json.id;
      this._checkClusterRequestStatus((summaries) => {
        this._summaries = [];
        summaries.summary.Clusters.forEach((cl) => {
          cl.ID = -1;
          this._summaries.push({
            summary: cl,
            frame: summaries.frame,
          });
        });
        this._render();
      });
    }).catch(msg => this._catch(msg));
  }

});

import 'common-sk/modules/error-toast-sk'

import { diffDate } from 'common-sk/modules/human'
import { errorMessage } from 'common-sk/modules/errorMessage'
import { jsonOrThrow } from 'common-sk/modules/jsonOrThrow'

import { html, render } from 'lit-html/lib/lit-extended'

// How often to update the data.
const UPDATE_INTERVAL_MS = 60000;

// Main template for this element
const template = (ele) => html`
<header>Power Controller</header>

<main>
  <h1>Broken Bots (with powercycle support)</h1>

  ${downBotsTable(ele._bots, ele._hosts)}
</main>
<footer>
  <error-toast-sk></error-toast-sk>
</footer>`;

const downBotsTable = (bots, hosts) => html`
<table>
  <thead>
    <tr>
      <th>Name</th>
      <th>Key Dimensions</th>
      <th>Status</th>
      <th>Since</th>
    </tr>
  </thead>
  <tbody>
    ${listBots(bots)}
  </tbody>
</table>

<h2>Powercycle Commands</h2>
${listHosts(hosts, bots)}`;

const listBots = (bots) => bots.map(bot => {
  return html`
<tr>
  <td>${bot.bot_id}</td>
  <td>${_keyDimension(bot)}</td>
  <td>${bot.status}</td>
  <td>${diffDate(bot.since)} ago</td>
</tr>`
});

const listHosts = (hosts, bots) => hosts.map(host => {
  return html`
<h3>On ${ host }</h3>
<div class=code>${_command(host, bots)}</div>`
});

// Helpers for templating
function _keyDimension(bot) {
  // TODO(kjlubick): Make this show only the important dimension.
  // e.g. for Android devices, just show "Nexus Player" or whatever
  if (!bot || !bot.dimensions) {
    return '';
  }
  let os = '';
  bot.dimensions.forEach(function(d){
    if (d.key === 'os') {
      os = d.value[d.value.length - 1];
    }
  });
  return os;
}

function _command(host, bots) {
  let hasBots = false;
  let cmd = 'powercycle --logtostderr ';
  bots.forEach(function(b){
    if (b.host_id === host && b.selected){
      hasBots = true;
      cmd += b.bot_id;
      if (b.status.startsWith('Device')) {
        cmd += '-device';
      }
      cmd += ' ';
    }
  });
  if (!hasBots) {
    return 'No bots down :)'
  }
  return cmd;
}

// The <power-index-sk> custom element declaration.
//
//  This is the main page for power.skia.org.
//
//  Attributes:
//    None
//
//  Properties:
//    None
//
//  Events:
//    None
//
//  Methods:
//    None
//
window.customElements.define('power-index-sk', class extends HTMLElement {

  constructor() {
    super();
    this._hosts = [];
    this._bots = [];
  }

  connectedCallback() {
    this._render();
    // make a fetch ASAP, but not immediately (demo mock up may not be set up yet)
    window.setTimeout(() => this.update());
  }

  update() {
    fetch('/down_bots')
      .then(jsonOrThrow)
      .then((json) => {
        json.list = json.list || [];
        let byHost = {};
        json.list.forEach(function(b){
          b.selected = !b.silenced;
          var host_arr = byHost[b.host_id] || [];
          host_arr.push(b.bot_id);
          byHost[b.host_id] = host_arr;
        });
        json.list.sort(function(a,b){
          return a.bot_id.localeCompare(b.bot_id);
        });
        this._bots = json.list;
        this._hosts = Object.keys(byHost);
        this._render();
        window.setTimeout(() => this.update(), UPDATE_INTERVAL_MS);
      })
      .catch((e) => {
        errorMessage(e);
        window.setTimeout(() => this.update(), UPDATE_INTERVAL_MS);
      });
  }

  _render() {
    render(template(this), this);
  }

});

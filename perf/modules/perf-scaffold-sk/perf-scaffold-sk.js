/**
 * @module module/perf-scaffold-sk
 * @description <h2><code>perf-scaffold-sk</code></h2>
 *
 * Contains the title bar and error-toast for all the Perf pages. The rest of
 * every Perf page should be a child of this element.
 *
 */
import { define } from 'elements-sk/define';
import { html } from 'lit-html';
import { ElementSk } from '../../../infra-sk/modules/ElementSk';
import 'elements-sk/error-toast-sk';
import 'elements-sk/icon/add-alert-icon-sk';
import 'elements-sk/icon/build-icon-sk';
import 'elements-sk/icon/event-icon-sk';
import 'elements-sk/icon/folder-icon-sk';
import 'elements-sk/icon/help-icon-sk';
import 'elements-sk/icon/home-icon-sk';
import 'elements-sk/icon/menu-icon-sk';
import 'elements-sk/icon/sort-icon-sk';
import 'elements-sk/icon/trending-up-icon-sk';
import '../../../infra-sk/modules/login-sk';

const template = (ele) => html`
  <nav id=topbar>
    <button id=toggleSidebarButton @click=${() => ele._toggleSidebar()}>
      <menu-icon-sk></menu-icon-sk>
    </button>
    <h1 class=name>Perf</h1>
    <login-sk></login-sk>
  </nav>
  <nav id=sidebar>
    <ul>
      <li><a href="/e/" tab-index=0 ><home-icon-sk></home-icon-sk><span>Home</span></a></li>
      <li><a href="/t/" tab-index=0 ><trending-up-icon-sk></trending-up-icon-sk><span>Triage</span></a></li>
      <li><a href="/a/" tab-index=0 ><add-alert-icon-sk></add-alert-icon-sk><span>Alerts</span></a></li>
      <li><a href="/d/" tab-index=0 ><build-icon-sk></build-icon-sk><span>Dry Run</span></a></li>
      <li><a href="/c/" tab-index=0 ><sort-icon-sk></sort-icon-sk><span>Clustering<span></a></li>
      <li><a href="http://go/perf-user-doc" tab-index=0 ><help-icon-sk></help-icon-sk><span>Help</span></a></li>
    </ul>
  </nav>
  <main>
  </main>
  <error-toast-sk></error-toast-sk>
`;

/**
 * Moves the elements from one NodeList to another NodeList.
 *
 * @param {NodeList} from - The list we are moving from.
 * @param {NodeList} to - The list we are moving to.
 */
function move(from, to) {
  Array.prototype.slice.call(from).forEach((ele) => to.appendChild(ele));
}

define('perf-scaffold-sk', class extends ElementSk {
  constructor() {
    super(template);
    this._main = null;
  }

  connectedCallback() {
    super.connectedCallback();
    // Don't call more than once.
    if (this._main) {
      return;
    }
    // We aren't using shadow dom so we need to manually move the children of
    // perf-scaffold-sk to be children of 'main'. We have to do this for the
    // existing elements and for all future mutations.

    // Create a temporary holding spot for elements we're moving.
    const div = document.createElement('div');
    move(this.children, div);

    // Now that we've moved all the old children out of the way we can render
    // the template.
    this._render();

    // Move the old children back under main.
    this._main = this.querySelector('main');
    move(div.children, this._main);

    // Move all future children under main also.
    const observer = new MutationObserver((mutList) => {
      mutList.forEach((mut) => {
        move(mut.addedNodes, this._main);
      });
    });
    observer.observe(this, { childList: true });
  }

  _toggleSidebar() {
    this.classList.toggle('sidebar_hidden');
  }
});

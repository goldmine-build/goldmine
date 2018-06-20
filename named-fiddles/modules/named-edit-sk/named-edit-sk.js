/**
 * @module named-edit-sk
 * @description <h2><code>named-edit-sk</code></h2>
 *
 *   Pop-up dialog for editing a single named edit.
 *
 * @evt named-edit-complete - Sent when the user presses OK.
 *   The event detail will be the desired state of the named
 *   fiddle.
 *
 *   <pre>
 *     {
 *        "name": "foo",
 *        "hash": "123",
 *        "new_name": "bar"
 *     }
 *   </pre>
 *
 */
import { html, render } from 'lit-html/lib/lit-extended'
import { $$ } from 'common-sk/modules/dom'
import 'elements-sk/dialog-sk'
import 'elements-sk/buttons'

const template = (ele) => html`
<dialog-sk>
  <h2>Edit Named Fiddle</h2>
  <label>Name <input type=text id=name value=${ele._state.name} size=50></label>
  <label>Hash <input type=text id=hash value=${ele._state.hash} size=40></label>
  <div class=dialog-buttons>
    <button on-click=${() => ele.hide()}>Cancel</button>
    <button id=ok on-click=${() => ele._ok()}>OK</button>
  </div>
</dialog-sk>
`;

window.customElements.define('named-edit-sk', class extends HTMLElement {
  constructor() {
    super();
    this._state = {
      name: '',
      hash: '',
    };
  }

  /** @prop state {object} A serialized Named struct.  */
  get state() { return this._state }
  set state(val) {
    this._state = Object.assign({}, val);
    this._render();
  }

  _ok() {
    if (this._state.name !== $$('#name', this).value) {
      this._state.new_name = $$('#name', this).value;
    }
    this._state.hash = $$('#hash', this).value;
    this.hide();
    this.dispatchEvent(new CustomEvent('named-edit-complete', { detail: this._state, bubbles: true }));
  }

  /** Show the dialog. */
  show() {
    this.firstElementChild.shown = true;
  }

  /** Hide the dialog. */
  hide() {
    this.firstElementChild.shown = false;
  }

  connectedCallback() {
    this._render();
  }

  _render() {
    render(template(this), this);
  }

});

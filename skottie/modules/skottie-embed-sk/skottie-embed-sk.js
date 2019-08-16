/**
 * @module skottie/skottie-embed-sk
 * @description <h2><code>skottie-embed-sk</code></h2>
 *
 * Displays just the WASM based animation suitable for iframing.
 *
 * @evt
 *
 * @attr
 *
 * @example
 *
 *  <iframe width=128 height=128
 *    src="https://skottie.skia.org/e/1112d01d28a776d777cebcd0632da15b"
 *    scrolling=no>
 *  </iframe>
 */
import '../skottie-player-sk'
import { define } from 'elements-sk/define'
import { html, render } from 'lit-html'
import { jsonOrThrow } from 'common-sk/modules/jsonOrThrow'

const template = (ele) => html`<skottie-player-sk></skottie-player-sk>`;

define('skottie-embed-sk', class extends HTMLElement {
  constructor() {
    super();
  }

  connectedCallback() {
    this._render();
    this._reflectFromURL();
  }

  _render() {
    render(template(this), this, {eventContext: this});
  }

  _reflectFromURL() {
    // Check URL.
    let match = window.location.pathname.match(/\/e\/([a-zA-Z0-9]+)/);
    if (!match) {
      // Make this the hash of the lottie file you want to play on startup.
      this._hash = '1112d01d28a776d777cebcd0632da15b'; // gear.json
    } else {
      this._hash = match[1];
    }
    // Run this on the next micro-task to allow mocks to be set up if needed.
    setTimeout(() => {
      fetch(`/_/j/${this._hash}`, {
        credentials: 'include',
      }).then(jsonOrThrow).then(json => {
        let player = this.querySelector('skottie-player-sk');
        player.initialize({
                            width:  json.width,
                            height: json.height,
                            lottie: json.lottie
                          });
      }).catch((msg) => {
        console.log(msg);
      });
    });
  }
});

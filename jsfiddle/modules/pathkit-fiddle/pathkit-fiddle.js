import 'elements-sk/error-toast-sk'
import { html } from 'lit-html'

import { SKIA_VERSION } from '../../build/version.js'
import { WasmFiddle, codeEditor } from '../wasm-fiddle'

const PathKitInit = require('../../build/pathkit/pathkit.js');

// Main template for this element
const template = (ele) => html`
<header>
  <div class=title>PathKit Fiddle</div>
  <div class=npm>
    <a href="https://www.npmjs.com/package/pathkit-wasm">Available on npm</a>
  </div>
  <div class=flex></div>

  <div class=version>
    <a href="https://skia.googlesource.com/skia/+/${SKIA_VERSION}">${SKIA_VERSION.substring(0, 10)}</a>
  </div>
</header>

<main>
  ${codeEditor(ele)}
  <div class=output>
    <div class=buttons>
      <button class=action @click=${ele.run}>Run</button>
      <button class=action @click=${ele.save}>Save</button>
    </div>
    <div id=canvasContainer><canvas width=500 height=500></canvas></div>
  </div>
</main>
<footer>
  <error-toast-sk></error-toast-sk>
</footer>`;

const wasmPromise = PathKitInit({
  locateFile: (file) => '/res/'+file,
}).ready();

/**
 * @module jsfiddle/modules/pathkit-fiddle
 * @description <h2><code>pathkit-fiddle</code></h2>
 *
 * <p>
 *   The top level element for displaying pathkit fiddles.
 *   The main elements are a code editor box (textarea), a canvas
 *   on which to render the result and a few buttons.
 * </p>
 *
 */
window.customElements.define('pathkit-fiddle', class extends WasmFiddle {

  constructor() {
    super(wasmPromise, template, 'PathKit', 'pathkit');
  }

});

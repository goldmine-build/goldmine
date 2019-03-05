import './index.js'
import './skottie-sk-demo.css'

import { gear, webfont, moving_image } from './test_gear.js'
const fetchMock = require('fetch-mock');

let state = {
  filename: 'moving_image.json',
  lottie: webfont,
}
fetchMock.get('glob:/_/j/*', {
  status: 200,
  body: JSON.stringify(state),
  headers: {'Content-Type':'application/json'},
});

fetchMock.post('glob:/_/upload', {
  status: 200,
  body: JSON.stringify({
    hash: 'MOCK_UPLOADED',
    lottie: gear,
  }),
  headers: {'Content-Type':'application/json'},
});

document.getElementsByTagName('skottie-sk')[0]._assetsPath =
    'https://storage.googleapis.com/skia-cdn/test_external_assets';


// Pass-through CanvasKit.
fetchMock.get('glob:*.wasm', fetchMock.realFetch.bind(window));
fetchMock.get('glob:https://storage.googleapis.com/skia-cdn/*', fetchMock.realFetch.bind(window));

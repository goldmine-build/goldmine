import './index';
import '../gold-scaffold-sk';

import { typicalDetails, fakeNow, twoHundredCommits } from '../digest-details-sk/test_data';
import { delay, isPuppeteerTest } from '../demo_util';
import { setImageEndpointsForDemos } from '../common';
import { $$ } from '../../../common-sk/modules/dom';

const fetchMock = require('fetch-mock');

setImageEndpointsForDemos();

// Load the demo page with some params to load if there aren't any already. 4 is an arbitrary
// small number to check against to determine "no query params"
if (window.location.search.length < 4) {
  const query = '?digest=6246b773851984c726cb2e1cb13510c2&test=My%20test%20has%20spaces&issue=12353';
  history.pushState(null, '', window.location.origin + window.location.pathname + query);
}

Date.now = () => fakeNow;

const rpcDelay = isPuppeteerTest() ? 5 : 300;

fetchMock.get('glob:/json/details*', delay(() => {
  if ($$('#simulate-rpc-error').checked) {
    return 500;
  }
  if ($$('#simulate-not-found-in-index').checked) {
    return JSON.stringify({
      digest: {
        digest: '6246b773851984c726cb2e1cb13510c2',
        test: 'This test exists, but the digest does not',
        status: 'untriaged',
      },
      commits: twoHundredCommits,
      trace_comments: null,
    });
  }
  return JSON.stringify({
    digest: typicalDetails,
    commits: twoHundredCommits,
    trace_comments: null,
  });
}, rpcDelay));
fetchMock.catch(404);

// make the page reload when checkboxes change.
document.addEventListener('change', () => {
  $$('details-page-sk')._fetch();
});

$$('#remove_btn').addEventListener('click', () => {
  const ele = $$('details-page-sk');
  ele._changeListID = '';
  ele._render();
});

import './index';
import '../../../infra-sk/modules/theme-chooser-sk';
import { $$ } from 'common-sk/modules/dom';
import { fetchMock } from 'fetch-mock';
import { pageSets } from '../pageset-selector-sk/test_data';
import { buildsJson } from '../chromium-build-selector-sk/test_data';
import 'elements-sk/error-toast-sk';

fetchMock.config.overwriteRoutes = false;
fetchMock.post('begin:/_/page_sets/', pageSets);
fetchMock.post('begin:/_/get_chromium_build_tasks', buildsJson);
// For determining running tasks for the user we just say 2.
fetchMock.postOnce('begin:/_/get', {
  data: [], ids: [], pagination: { offset: 0, size: 1, total: 2 }, permissions: [],
});
fetchMock.post('begin:/_/get', {
  data: [], ids: [], pagination: { offset: 0, size: 1, total: 0 }, permissions: [],
});

const chromiumPerf = document.createElement('capture-skps-sk');
$$('#container').appendChild(chromiumPerf);

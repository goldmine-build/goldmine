/**
 * Utility javascript functions used across the different CT FE pages.
 */
import { pad } from 'common-sk/modules/human';
import { fromObject } from 'common-sk/modules/query';
import { jsonOrThrow } from 'common-sk/modules/jsonOrThrow';
import { errorMessage } from 'elements-sk/errorMessage';

/**
 * Converts the timestamp used in CTFE DB into a user friendly string.
 */
export function getFormattedTimestamp(timestamp) {
  if (!timestamp) {
    return '<pending>';
  }
  return getTimestamp(timestamp).toLocaleString();
}

/**
 * Converts the timestamp used in CTFE DB into a Javascript timestamp.
 */
export function getTimestamp(timestamp) {
  if (!timestamp) {
    return timestamp;
  }
  const date = new Date();
  // Timestamp is of the form YYYYMMDDhhmmss.
  // Consume the pieces off the right to build the date.
  const consumeDigits = (n) => {
    const first_n_digits = timestamp % (10 ** n);
    timestamp = (timestamp - first_n_digits) / (10 ** n);
    return first_n_digits;
  };
  date.setUTCSeconds(consumeDigits(2));
  date.setUTCMinutes(consumeDigits(2));
  date.setUTCHours(consumeDigits(2));
  date.setUTCDate(consumeDigits(2));
  date.setUTCMonth(consumeDigits(2) - 1); // Month is 0 based in JS.
  date.setUTCFullYear(consumeDigits(4));
  return date;
}

/**
 * Convert from Javascript Date to timestamp recognized by CTFE DB.
 */
export function getCtDbTimestamp(d) {
  const timestamp = String(d.getUTCFullYear()) + pad(d.getUTCMonth() + 1, 2)
                  + pad(d.getUTCDate(), 2) + pad(d.getUTCHours(), 2)
                  + pad(d.getUTCMinutes(), 2) + pad(d.getUTCSeconds(), 2);
  return timestamp;
}

/**
 * Fetches benchmarks with doc links, and platforms with descriptions.
 *
 * @param {func<Object>} func - Function called with fetched benchmarks and
 * platforms object.
 */
export function fetchBenchmarksAndPlatforms(func) {
  fetch('/_/benchmarks_platforms/', {
    method: 'POST',
  })
    .then(jsonOrThrow)
    .then(func)
    .catch(errorMessage);
}

/**
 *
 * @param {Array<string>} descriptions - Array of CL descriptions, combined
 * into a description string if at least one is noneempty.
 *
 * @returns string - Combined description.
 */
export function combineClDescriptions(descriptions) {
  const combinedDesc = descriptions.filter(Boolean).reduce(
    (str, desc) => str += (str === '' ? desc : ` and ${desc}`), '',
  );
  return combinedDesc ? `Testing ${combinedDesc}` : '';
}

export function missingLiveSitesWithCustomWebpages(customWebpages, benchmarkArgs) {
  if (customWebpages && !benchmarkArgs.includes('--use-live-sites')) {
    errorMessage('Please specify --use-live-sites in benchmark arguments '
                    + 'when using custom web pages.');
    return true;
  }
  return false;
}

let activeTasks = 0;
/**
 * Asynchronously queries the logged in user's active task count.
 * This is best effort, so doesn't bother with returning a promise.
 *
 * @returns function() boolean : Whether or not the task count
 * previously fetched is more than 3.
 */
export function moreThanThreeActiveTasksChecker() {
  const queryParams = {
    size: 1,
    not_completed: true,
    filter_by_logged_in_user: true,
  };
  const queryStr = `?${fromObject(queryParams)}`;

  taskDescriptors.forEach((obj) => {
    fetch(obj.get_url + queryStr, {
      method: 'POST',
    })
      .then(jsonOrThrow)
      .then((json) => {
        activeTasks += json.pagination.total;
      })
      .catch(errorMessage);
  });
  return () => {
    if (activeTasks > 3) {
      errorMessage(`You have ${activeTasks} currently running tasks. Please wait`
        + ' for them to complete before scheduling more CT tasks.');
      return true;
    }
    return false;
  };
}

export function shortHash(commitHash) {
  return commitHash.substr(0, 7);
}

/**
 * Returns a short description for the given build.
 */
export function chromiumBuildDescription(build) {
  return `${shortHash(build.ChromiumRev)}-${shortHash(build.SkiaRev)} (Chromium rev created on ${getFormattedTimestamp(build.ChromiumRevTs)})`;
}

/**
 * List of task types and the associated urls to fetch and delete them.
 */
export const taskDescriptors = [
  {
    type: 'ChromiumPerf',
    get_url: '/_/get_chromium_perf_tasks',
    delete_url: '/_/delete_chromium_perf_task',
  },
  {
    type: 'ChromiumAnalysis',
    get_url: '/_/get_chromium_analysis_tasks',
    delete_url: '/_/delete_chromium_analysis_task',
  },
  {
    type: 'MetricsAnalysis',
    get_url: '/_/get_metrics_analysis_tasks',
    delete_url: '/_/delete_metrics_analysis_task',
  },
  {
    type: 'CaptureSkps',
    get_url: '/_/get_capture_skp_tasks',
    delete_url: '/_/delete_capture_skps_task',
  },
  {
    type: 'ChromiumBuild',
    get_url: '/_/get_chromium_build_tasks',
    delete_url: '/_/delete_chromium_build_task',
  },
  {
    type: 'RecreatePageSets',
    get_url: '/_/get_recreate_page_sets_tasks',
    delete_url: '/_/delete_recreate_page_sets_task',
  },
  {
    type: 'RecreateWebpageArchives',
    get_url: '/_/get_recreate_webpage_archives_tasks',
    delete_url: '/_/delete_recreate_webpage_archives_task',
  },
];

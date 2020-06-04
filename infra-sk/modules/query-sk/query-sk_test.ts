import './index';
import { $, $$ } from 'common-sk/modules/dom';
import { ParamSet, toParamSet } from 'common-sk/modules/query';
import { QuerySk, QuerySkQueryChangeEventDetail } from './query-sk';
import { setUpElementUnderTest, eventPromise } from '../test_util';
import { assert } from 'chai';

const paramset: ParamSet = {
  arch: [
    'WASM',
    'arm',
    'arm64',
    'asmjs',
    'wasm',
    'x86',
    'x86_64',
  ],
  bench_type: [
    'deserial',
    'micro',
    'playback',
    'recording',
    'skandroidcodec',
    'skcodec',
    'tracing',
  ],
  compiler: [
    'Clang',
    'EMCC',
    'GCC',
  ],
  config: [
    '8888',
    'f16',
    'gl',
    'gles',
  ],
};

describe('query-sk', () => {
  const newInstance = setUpElementUnderTest<QuerySk>('query-sk');

  let querySk: QuerySk;
  let fast: HTMLInputElement;
  beforeEach(() => {
    querySk = newInstance();
    fast = $$<HTMLInputElement>('#fast', querySk)!;
  })

  it('obeys key_order', () => {
    querySk.paramset = paramset;
    assert.deepEqual(['arch', 'bench_type', 'compiler', 'config'], keys(querySk));

    // Setting key_order will change the key order.
    querySk.key_order = ['config'];
    assert.deepEqual(['config', 'arch', 'bench_type', 'compiler'], keys(querySk));

    // Setting key_order to empty will go back to alphabetical order.
    querySk.key_order = [];
    assert.deepEqual(['arch', 'bench_type', 'compiler', 'config'],  keys(querySk));
  });

  it('obeys filter', () =>  {
    querySk.paramset = paramset;
    assert.deepEqual(['arch', 'bench_type', 'compiler', 'config'],  keys(querySk));

    // Setting the filter will change the keys displayed.
    fast.value = 'cro'; // Only 'micro' in 'bench_type' should match.
    fast.dispatchEvent(new Event('input')); // Emulate user input.

    // Only key should be bench_type.
    assert.deepEqual(['bench_type'], keys(querySk));

    // Clearing the filter will restore all options.
    fast.value = '';
    fast.dispatchEvent(new Event('input')); // Emulate user input.

    assert.deepEqual(['arch', 'bench_type', 'compiler', 'config'],  keys(querySk));
  });

  it('only edits displayed values when filter is used.', () =>  {
    querySk.paramset = paramset;

    // Make a selection.
    querySk.current_query = 'arch=x86';

    // Setting the filter will change the keys displayed.
    fast.value = '64'; // Only 'arm64' and 'x86_64' in 'arch' should match.
    fast.dispatchEvent(new Event('input')); // Emulate user input.

    // Only key should be arch.
    assert.deepEqual(['arch'],  keys(querySk));

    // Click on 'arch'.
    ($$('select-sk', querySk)!.firstElementChild! as HTMLElement).click();

    // Click on the value 'arm64' to add it to the query.
    ($$('multi-select-sk', querySk)!.firstElementChild! as HTMLElement).click();

    // Confirm it gets added.
    assert.deepEqual(toParamSet('arch=x86&arch=arm64'), toParamSet(querySk.current_query));

    // Click on the value 'arm64' a second time to remove it from the query.
    ($$('multi-select-sk', querySk)!.firstElementChild as HTMLElement).click();

    // Confirm it gets removed.
    assert.deepEqual(toParamSet('arch=x86'), toParamSet(querySk.current_query));
  });
});

const keys = (q: QuerySk) => $('select-sk div', q).map(e => e.textContent);

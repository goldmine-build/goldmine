import './index';

const paramset = {
  arch: ['Arm7', 'Arm64', 'x86_64', 'x86'],
  bench_type: ['micro', 'playback', 'recording'],
  compiler: ['GCC', 'MSVC', 'Clang'],
  cpu_or_gpu: ['GPU', 'CPU'],
};

const paramset2 = {
  arch: ['Arm7'],
  bench_type: ['playback', 'recording'],
  compiler: [],
  cpu_or_gpu: ['GPU'],
};

const set1 = document.querySelector('#set1');
const set2 = document.querySelector('#set2');
const set3 = document.querySelector('#set3');

const key = document.querySelector('#key');
const value = document.querySelector('#value');

set1.paramsets = { paramsets: [paramset] };
set2.paramsets = { paramsets: [paramset, paramset2], titles: ['Set 1', 'Set 2'] };
set3.paramsets = { paramsets: [paramset], titles: ['Clickable Values Only'] };

set2.addEventListener('paramset-key-click', (e) => {
  key.textContent = JSON.stringify(e.detail, null, '  ');
});

set2.addEventListener('paramset-key-value-click', (e) => {
  value.textContent = JSON.stringify(e.detail, null, '  ');
});

set3.addEventListener('paramset-key-value-click', (e) => {
  value.textContent = JSON.stringify(e.detail, null, '  ');
});

document.querySelector('#highlight').addEventListener('click', () => {
  set1.highlight = { arch: 'Arm7', cpu_or_gpu: 'GPU' };
  set2.highlight = { arch: 'Arm7', cpu_or_gpu: 'GPU' };
  set3.highlight = { arch: 'Arm7', cpu_or_gpu: 'GPU' };
});

document.querySelector('#clear').addEventListener('click', () => {
  set1.highlight = {};
  set2.highlight = {};
  set3.highlight = {};
});

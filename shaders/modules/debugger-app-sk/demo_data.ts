import { DebugTrace } from '../debug-trace/generate/debug-trace-quicktype';

export const exampleTrace: DebugTrace = {
  source: [
    'first line',
    'second line',
    'third line',
  ],
  slots: [
    {
      slot: 0,
      name: 'SkVM_DebugTrace',
      columns: 1,
      rows: 2,
      index: 3,
      kind: 4,
      line: 5,
    },
    {
      slot: 1,
      name: 'Unit_Test',
      columns: 6,
      rows: 7,
      index: 8,
      kind: 9,
      line: 10,
      retval: 11,
    },
  ],
  functions: [{ slot: 0, name: 'void testFunc();' }],
  trace: [[2], [0, 5], [1, 10, 15], [3, 20]],
};

export const exampleTraceString: string = JSON.stringify(exampleTrace);

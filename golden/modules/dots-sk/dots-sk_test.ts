import './index';
import { expect } from 'chai';
import { DotsSk } from './dots-sk';
import { commits, traces } from './demo_data';
import {
  dotToCanvasX,
  dotToCanvasY,
  DOT_FILL_COLORS,
  DOT_RADIUS,
  DOT_STROKE_COLORS,
  MAX_UNIQUE_DIGESTS,
  TRACE_LINE_COLOR,
} from './constants';
import {
  eventPromise,
  setUpElementUnderTest,
} from '../../../infra-sk/modules/test_util';
import { Commit } from '../rpc_types';

describe('dots-sk constants', () => {
  it('DOT_FILL_COLORS has the expected number of entries', () => {
    expect(DOT_FILL_COLORS).to.have.length(MAX_UNIQUE_DIGESTS);
  });


  it('DOT_STROKE_COLORS has the expected number of entries', () => {
    expect(DOT_STROKE_COLORS).to.have.length(MAX_UNIQUE_DIGESTS);
  });
});

describe('dots-sk', () => {
  const newInstance = setUpElementUnderTest<DotsSk>('dots-sk');

  let dotsSk: DotsSk;
  let dotsSkCanvas: HTMLCanvasElement;
  let dotsSkCanvasCtx: CanvasRenderingContext2D;

  beforeEach(() => {
    dotsSk = newInstance((el) => {
      // All test cases use the same set of traces and commits.
      el.value = traces;
      el.commits = commits;
    });
    dotsSkCanvas = dotsSk.querySelector('canvas')!;
    dotsSkCanvasCtx = dotsSkCanvas.getContext('2d')!;
  });


  it('emits "hover" event when a trace is hovered', async () => {
    // Hover over first trace. (X coordinate does not matter.)
    let traceLabel = await hoverOverDotAndCatchHoverEvent(dotsSkCanvas, 0, 0);
    expect(traceLabel).to.equal(',alpha=first-trace,beta=hello,gamma=world,');

    // Hover over second trace.
    traceLabel = await hoverOverDotAndCatchHoverEvent(dotsSkCanvas, 15, 1);
    expect(traceLabel).to.equal(',alpha=second-trace,beta=foo,gamma=bar,');

    // Hover over third trace.
    traceLabel = await hoverOverDotAndCatchHoverEvent(dotsSkCanvas, 10, 2);
    expect(traceLabel).to.equal(',alpha=third-trace,beta=baz,gamma=qux,');
  });

  it('emits "showblamelist" event when a dot is clicked', async () => {
    // First trace, most recent commit.
    let dotCommits = await clickDotAndCatchShowBlamelistEvent(
      dotsSkCanvas,
      19,
      0
    );
    expect(dotCommits).to.deep.equal([commits[19], commits[18]]);

    // First trace, middle-of-the-tile commit.
    dotCommits = await clickDotAndCatchShowBlamelistEvent(dotsSkCanvas, 10, 0);
    expect(dotCommits).to.deep.equal([commits[10], commits[9]]);

    // First trace, oldest commit.
    dotCommits = await clickDotAndCatchShowBlamelistEvent(dotsSkCanvas, 0, 0);
    expect(dotCommits).to.deep.equal([commits[0]]);

    // Second trace, most recent commit with data
    dotCommits = await clickDotAndCatchShowBlamelistEvent(dotsSkCanvas, 17, 1);
    expect(dotCommits).to.deep.equal([commits[17], commits[16]]);

    // Second trace, middle-of-the-tile dot preceded by two missing dots.
    dotCommits = await clickDotAndCatchShowBlamelistEvent(dotsSkCanvas, 14, 1);
    expect(dotCommits).to.deep.equal([
      commits[14],
      commits[13],
      commits[12],
      commits[11],
    ]);

    // Second trace, oldest commit with data preceded by three missing dots.
    dotCommits = await clickDotAndCatchShowBlamelistEvent(dotsSkCanvas, 3, 1);
    expect(dotCommits).to.deep.equal([
      commits[3],
      commits[2],
      commits[1],
      commits[0],
    ]);

    // Third trace, most recent commit.
    dotCommits = await clickDotAndCatchShowBlamelistEvent(dotsSkCanvas, 19, 2);
    expect(dotCommits).to.deep.equal([commits[19], commits[18]]);

    // Third trace, middle-of-the-tile commit.
    dotCommits = await clickDotAndCatchShowBlamelistEvent(dotsSkCanvas, 10, 2);
    expect(dotCommits).to.deep.equal([commits[10], commits[9]]);

    // Third trace, oldest commit.
    dotCommits = await clickDotAndCatchShowBlamelistEvent(dotsSkCanvas, 6, 2);
    expect(dotCommits).to.deep.equal([
      commits[6],
      commits[5],
      commits[4],
      commits[3],
      commits[2],
      commits[1],
      commits[0],
    ]);
  });
});


// Simulate hovering over a dot.
async function hoverOverDot(
  dotsSkCanvas: HTMLCanvasElement,
  x: number,
  y: number
) {
  dotsSkCanvas.dispatchEvent(
    new MouseEvent('mousemove', {
      clientX: dotsSkCanvas.getBoundingClientRect().left + dotToCanvasX(x),
      clientY: dotsSkCanvas.getBoundingClientRect().top + dotToCanvasY(y),
    })
  );

  // Give mousemove event a chance to be processed. Necessary due to how
  // mousemove events are processed in batches by dots-sk every 40 ms.
  await new Promise((resolve) => setTimeout(resolve, 50));
}

// Simulate hovering over a dot, and return the trace label in the "hover" event details.
async function hoverOverDotAndCatchHoverEvent(
  dotsSkCanvas: HTMLCanvasElement,
  x: number,
  y: number
): Promise<string> {
  // const eventPromise = dotsSkEventPromise(dotsSk, 'hover');
  const event = eventPromise<CustomEvent<string>>('hover');
  await hoverOverDot(dotsSkCanvas, x, y);
  return (await event).detail;
}

// Simulate clicking on a dot.
function clickDot(dotsSkCanvas: HTMLCanvasElement, x: number, y: number) {
  dotsSkCanvas.dispatchEvent(
    new MouseEvent('click', {
      clientX: dotsSkCanvas.getBoundingClientRect().left + dotToCanvasX(x),
      clientY: dotsSkCanvas.getBoundingClientRect().top + dotToCanvasY(y),
    })
  );
}

// Simulate clicking on a dot, and return the list of commits in the "showblamelist" event details.
async function clickDotAndCatchShowBlamelistEvent(
  dotsSkCanvas: HTMLCanvasElement,
  x: number,
  y: number
): Promise<Commit[]> {
  const event = eventPromise<CustomEvent<Commit[]>>('showblamelist');
  clickDot(dotsSkCanvas, x, y);
  return (await event).detail;
}

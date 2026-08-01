import { describe, expect, it } from "vitest";
import { Virtualizer } from "@tanstack/virtual-core";
import { calibrateRenderedRows } from "./feed-virtual";

// Regression harness for issue #16: after a tall Markdown row, appended
// usage / next-Turn rows must keep their virtual start/end aligned with the
// real DOM heights. The old activation path called `measure()`, which clears
// the whole itemSizeCache without reading the DOM, so already-mounted tall
// rows fell back to the 96px estimate and the following rows overlapped.

const ESTIMATE = 96;

function makeRow(index: number, height: number): HTMLDivElement {
  const node = document.createElement("div");
  node.setAttribute("data-index", String(index));
  // jsdom performs no layout; feed the DOM height through offsetHeight, which
  // is what the virtualizer's default measureElement reads.
  Object.defineProperty(node, "offsetHeight", { value: height, configurable: true });
  return node;
}

function makeFeed(heights: number[]) {
  const scrollEl = document.createElement("div");
  const rows = heights.map((height, index) => makeRow(index, height));
  for (const row of rows) scrollEl.appendChild(row);

  const options = {
    count: heights.length,
    getScrollElement: () => scrollEl,
    getItemKey: (index: number) => `row-${index}`,
    estimateSize: () => ESTIMATE,
    overscan: 6,
    observeElementRect: (_: unknown, cb: (rect: { width: number; height: number }) => void) => {
      cb({ width: 800, height: 600 });
      return () => {};
    },
    observeElementOffset: (_: unknown, cb: (offset: number, isScrolling: boolean) => void) => {
      cb(0, false);
      return () => {};
    },
    scrollToFn: () => {},
  };
  const virtualizer = new Virtualizer<HTMLDivElement, HTMLDivElement>(options);
  virtualizer._didMount();
  virtualizer._willUpdate();
  return { virtualizer, scrollEl, rows, options };
}

function expectRowsMatchDom(virtualizer: Virtualizer<HTMLDivElement, HTMLDivElement>, heights: number[]) {
  const items = virtualizer.getVirtualItems();
  expect(items.length).toBe(heights.length);
  let expectedStart = 0;
  for (const item of items) {
    expect(item.start).toBe(expectedStart);
    expect(item.size).toBe(heights[item.index]);
    // The next row must begin at or after this row's DOM bottom — no overlap.
    expect(item.end).toBe(item.start + heights[item.index]);
    expectedStart = item.end;
  }
}

describe("calibrateRenderedRows", () => {
  it("applies the actual DOM height of every rendered row", () => {
    const heights = [900, 40, 64];
    const { virtualizer, scrollEl } = makeFeed(heights);

    const measured = calibrateRenderedRows(virtualizer, scrollEl);

    expect(measured).toBe(3);
    expectRowsMatchDom(virtualizer, heights);
  });

  it("keeps a tall Markdown row measured while usage and new Turns are appended", () => {
    const heights = [900, 40];
    const { virtualizer, scrollEl, options } = makeFeed(heights);
    calibrateRenderedRows(virtualizer, scrollEl);

    // Stream in a usage row and a new Turn without re-measuring row 0: its
    // cached 900px height must survive, so the new rows start after its DOM
    // bottom instead of at the 96px estimate.
    for (const appended of [48, 120]) {
      heights.push(appended);
      scrollEl.appendChild(makeRow(heights.length - 1, appended));
      virtualizer.setOptions({ ...options, count: heights.length });
      virtualizer._willUpdate();
      calibrateRenderedRows(virtualizer, scrollEl);
      expectRowsMatchDom(virtualizer, heights);
    }

    const [tall, usage] = virtualizer.getVirtualItems();
    expect(tall.size).toBe(900);
    expect(usage.start).toBe(900);
  });

  it("re-calibrates from the DOM after a full measure() cache clear", () => {
    // Documents why the activation path must not call measure(): it resets
    // every mounted row to the estimate without producing a ResizeObserver
    // entry. calibrateRenderedRows repairs exactly that state from the DOM.
    const heights = [900, 40, 64];
    const { virtualizer, scrollEl } = makeFeed(heights);
    calibrateRenderedRows(virtualizer, scrollEl);

    virtualizer.measure();
    expect(virtualizer.getVirtualItems()[1].start).toBe(ESTIMATE);

    calibrateRenderedRows(virtualizer, scrollEl);
    expectRowsMatchDom(virtualizer, heights);
  });

  it("is a no-op without a scroll element", () => {
    const { virtualizer } = makeFeed([900]);
    expect(calibrateRenderedRows(virtualizer, null)).toBe(0);
  });
});

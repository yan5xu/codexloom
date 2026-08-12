import { Virtualizer, type VirtualizerOptions } from "@tanstack/react-virtual";
import { describe, expect, it } from "vitest";

function virtualizerOptions(
  scrollElement: HTMLDivElement,
  enabled: boolean,
  initialOffset: () => number,
): VirtualizerOptions<HTMLDivElement, HTMLDivElement> {
  return {
    count: 467,
    enabled,
    estimateSize: () => 96,
    getScrollElement: () => scrollElement,
    initialOffset,
    observeElementOffset: () => () => {},
    observeElementRect: (_instance, callback) => {
      callback({ width: 880, height: 614 });
      return () => {};
    },
    overscan: 6,
    scrollToFn: (offset) => {
      scrollElement.scrollTop = offset;
    },
  };
}

describe("Agent feed virtualizer activation", () => {
  it("re-enables from the Agent's saved scroll offset instead of row zero", () => {
    const scrollElement = document.createElement("div");
    let savedScrollTop = 0;
    const initialOffset = () => savedScrollTop;
    const virtualizer = new Virtualizer<HTMLDivElement, HTMLDivElement>(
      virtualizerOptions(scrollElement, true, initialOffset),
    );
    const cleanup = virtualizer._didMount();

    virtualizer._willUpdate();
    expect(virtualizer.getVirtualItems().map((item) => item.index)).toContain(0);

    savedScrollTop = 44_698;
    virtualizer.setOptions(virtualizerOptions(scrollElement, false, initialOffset));
    virtualizer._willUpdate();
    expect(virtualizer.getVirtualItems()).toEqual([]);

    virtualizer.setOptions(virtualizerOptions(scrollElement, true, initialOffset));
    virtualizer._willUpdate();
    const restoredIndexes = virtualizer.getVirtualItems().map((item) => item.index);

    expect(restoredIndexes).not.toContain(0);
    expect(restoredIndexes).toContain(466);
    expect(scrollElement.scrollTop).toBe(savedScrollTop);
    cleanup();
  });
});

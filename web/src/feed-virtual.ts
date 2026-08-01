// Height calibration for the virtualized Agent feed.
//
// TanStack Virtual's `measure()` empties the whole itemSizeCache instead of
// re-reading the DOM. Rows that are already mounted never re-run their ref
// callback, and ResizeObserver only reports elements whose size actually
// changed, so a cleared tall Markdown row silently falls back to the
// `estimateSize` value and the rows after it (token usage, the next Turn)
// are positioned on top of its real DOM box. The feed must therefore never
// clear measured heights wholesale; when it needs to re-calibrate — e.g.
// after an inactive pane becomes visible again — it re-feeds the actual DOM
// heights of the currently rendered rows through the virtualizer's single
// measurement entry point instead.

export interface RowHeightMeasurer {
  measureElement: (node: HTMLDivElement | null) => void;
}

/**
 * Re-measures every currently rendered feed row from its live DOM node.
 *
 * `measureElement` both (re)attaches the virtualizer's ResizeObserver and
 * applies the node's current border-box height, so streaming Markdown keeps
 * updating afterwards. Rows whose height is unchanged are no-ops inside the
 * virtualizer, which makes this safe to call on every activation.
 *
 * Returns the number of rows that were measured.
 */
export function calibrateRenderedRows(
  measurer: RowHeightMeasurer,
  scrollEl: HTMLElement | null,
): number {
  if (!scrollEl) return 0;
  const nodes = scrollEl.querySelectorAll<HTMLDivElement>("[data-index]");
  for (const node of nodes) measurer.measureElement(node);
  return nodes.length;
}

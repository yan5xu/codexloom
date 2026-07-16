import type { UsageDay } from "../types";

export function UsageBarTooltip({ day, align = "center" }: { day: UsageDay; align?: "start" | "center" | "end" }) {
  const position = align === "start" ? "left-0" : align === "end" ? "right-0" : "left-1/2 -translate-x-1/2";
  return (
    <div
      role="tooltip"
      data-usage-tooltip
      className={`pointer-events-none absolute top-1 z-20 hidden w-40 border border-border bg-popover px-2.5 py-2 text-popover-foreground shadow-float group-hover:block group-focus:block ${position}`}
    >
      <div className="border-b border-border pb-1 font-mono text-[9px] text-muted-foreground">{day.date}</div>
      <div className="mt-1 flex items-baseline justify-between gap-3">
        <span className="text-[9px] font-semibold uppercase text-muted-foreground">Total</span>
        <span className="font-mono text-[12px] font-semibold">{exactNumber(day.usage.totalTokens)}</span>
      </div>
      <dl className="mt-1 grid grid-cols-[1fr_auto] gap-x-3 gap-y-0.5 font-mono text-[9px]">
        <dt className="text-muted-foreground">Input</dt><dd>{exactNumber(day.usage.inputTokens)}</dd>
        <dt className="text-muted-foreground">Cached</dt><dd>{exactNumber(day.usage.cachedInputTokens)}</dd>
        <dt className="text-muted-foreground">Output</dt><dd>{exactNumber(day.usage.outputTokens)}</dd>
        <dt className="text-muted-foreground">Calls</dt><dd>{exactNumber(day.usage.calls)}</dd>
      </dl>
    </div>
  );
}

export function usageDayLabel(day: UsageDay) {
  return `${day.date}: ${exactNumber(day.usage.totalTokens)} total tokens, ${exactNumber(day.usage.inputTokens)} input, ${exactNumber(day.usage.cachedInputTokens)} cached, ${exactNumber(day.usage.outputTokens)} output, ${exactNumber(day.usage.calls)} calls`;
}

function exactNumber(value: number) {
  return Math.max(0, Number.isFinite(value) ? Math.round(value) : 0).toLocaleString();
}

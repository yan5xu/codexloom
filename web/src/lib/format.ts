/**
 * Shared formatting utilities — WHO: Common display formatters used by Agent and Space pages.
 *
 * WHERE: Imported by MessageBubbles, AITurnCard, AITurnRow, MessageGroupItem
 * WITH: No dependencies — pure functions
 * WHY: Eliminates 8x duplication across Agent and Space components
 */

/** Format ISO timestamp to short time (e.g., "10:30 AM") */
export function shortTime(ts: string): string {
  const d = new Date(ts);
  if (!ts || isNaN(d.getTime())) return ""; // empty/invalid → render nothing
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

/** Format token count to human-readable (e.g., 1500 → "1.5k", 2000000 → "2.0M") */
export function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}k`;
  return String(n);
}

/** Get initials from a name (e.g., "Zhang San" → "ZS") */
export function initials(name: string): string {
  return name.split(/\s+/).map((w) => w[0]).join("").toUpperCase().slice(0, 2);
}

/** Extract brief and command from tool call arguments JSON */
export function extractToolBrief(toolArgs?: string): { brief: string | null; command: string | null } {
  if (!toolArgs) return { brief: null, command: null };
  try {
    const parsed = JSON.parse(toolArgs) as { brief?: string; command?: string };
    return { brief: parsed.brief || null, command: parsed.command || null };
  } catch {
    return { brief: null, command: null };
  }
}

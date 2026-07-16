import type { TokenUsageOverview } from "./types";

export const USAGE_RANGE_MODES = ["day", "7d", "30d", "90d", "custom"] as const;

export type UsageRangeMode = (typeof USAGE_RANGE_MODES)[number];

export interface UsageDateRange {
  mode: UsageRangeMode;
  from: string;
  to: string;
  timezone: string;
}

const PRESET_DAYS: Record<Exclude<UsageRangeMode, "custom">, number> = {
  day: 1,
  "7d": 7,
  "30d": 30,
  "90d": 90,
};

export function browserTimezone() {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

export function todayDate() {
  return formatLocalDate(new Date());
}

export function usageRangeEndingOn(mode: UsageRangeMode, to = todayDate(), timezone = browserTimezone(), customFrom?: string): UsageDateRange {
  const safeTo = isDate(to) ? to : todayDate();
  if (mode === "custom") {
    const from = isDate(customFrom || "") && (customFrom as string) <= safeTo ? customFrom as string : safeTo;
    return { mode, from, to: safeTo, timezone };
  }
  return { mode, from: addCalendarDays(safeTo, -(PRESET_DAYS[mode] - 1)), to: safeTo, timezone };
}

export function readUsageLocation(hash: string): UsageDateRange & { agentId: string | null } {
  return readCalendarLocation(hash);
}

export function readCapacityLocation(hash: string): UsageDateRange & { agentId: string | null } {
  return readCalendarLocation(hash);
}

function readCalendarLocation(hash: string): UsageDateRange & { agentId: string | null } {
  const params = hashParams(hash);
  const timezone = params.get("tz") || browserTimezone();
  const requestedMode = params.get("mode") as UsageRangeMode | null;
  const mode = requestedMode && USAGE_RANGE_MODES.includes(requestedMode) ? requestedMode : legacyUsageMode(params.get("days"));
  const to = clampToToday(params.get("to") || todayDate());
  const from = params.get("from");
  const range = mode === "custom"
    ? usageRangeEndingOn(mode, to, timezone, from || to)
    : usageRangeEndingOn(mode, to, timezone);
  return { ...range, agentId: params.get("agent") || null };
}

export function buildUsageHash(range: UsageDateRange, agentId: string | null = null) {
  const query = new URLSearchParams({ mode: range.mode, from: range.from, to: range.to, tz: range.timezone });
  if (agentId) query.set("agent", agentId);
  return `#usage?${query.toString()}`;
}

export function buildCapacityHash(range: UsageDateRange, agentId: string | null = null) {
  const query = new URLSearchParams({ mode: range.mode, from: range.from, to: range.to, tz: range.timezone });
  if (agentId) query.set("agent", agentId);
  return `#capacity?${query.toString()}`;
}

export function usageAPIQuery(range: UsageDateRange) {
  return new URLSearchParams({ from: range.from, to: range.to, tz: range.timezone }).toString();
}

export function shiftUsageRange(range: UsageDateRange, direction: -1 | 1) {
  const days = dateSpanDays(range.from, range.to);
  const delta = range.mode === "custom" ? days * direction : PRESET_DAYS[range.mode] * direction;
  const nextTo = direction === 1 && addCalendarDays(range.to, delta) > todayDate() ? todayDate() : addCalendarDays(range.to, delta);
  if (range.mode === "custom") {
    return { ...range, from: addCalendarDays(nextTo, -(days - 1)), to: nextTo };
  }
  return usageRangeEndingOn(range.mode, nextTo, range.timezone);
}

export function reanchorUsageRange(range: UsageDateRange, to: string) {
  if (range.mode !== "custom") return usageRangeEndingOn(range.mode, to, range.timezone);
  const days = dateSpanDays(range.from, range.to);
  return { ...range, from: addCalendarDays(to, -(days - 1)), to };
}

export function canShiftUsageRangeForward(range: UsageDateRange) {
  return range.to < todayDate();
}

export function dateSpanDays(from: string, to: string) {
  let count = 1;
  for (let cursor = from; cursor < to; cursor = addCalendarDays(cursor, 1)) count += 1;
  return count;
}

export function resolveUsageScope(usage: TokenUsageOverview, agentId: string | null) {
  const agent = usage.agents.find((item) => item.agentId === agentId) || null;
  return {
    agent,
    daily: agent?.daily || usage.daily,
    period: agent?.period || usage.period,
    previous: agent?.previous || usage.previous,
    today: agent?.today || usage.today,
  };
}

function legacyUsageMode(value: string | null): UsageRangeMode {
  if (value === "1") return "day";
  if (value === "30") return "30d";
  if (value === "90") return "90d";
  return "7d";
}

function hashParams(hash: string) {
  return new URLSearchParams(hash.split("?")[1] || "");
}

function clampToToday(value: string) {
  const today = todayDate();
  if (!isDate(value)) return today;
  return value > today ? today : value;
}

function isDate(value: string) {
  return /^\d{4}-\d{2}-\d{2}$/.test(value) && !Number.isNaN(parseLocalDate(value).getTime());
}

function addCalendarDays(value: string, days: number) {
  const date = parseLocalDate(value);
  date.setDate(date.getDate() + days);
  return formatLocalDate(date);
}

function parseLocalDate(value: string) {
  const [year, month, day] = value.split("-").map(Number);
  return new Date(year, month - 1, day, 12, 0, 0, 0);
}

function formatLocalDate(date: Date) {
  const year = date.getFullYear();
  const month = String(date.getMonth() + 1).padStart(2, "0");
  const day = String(date.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

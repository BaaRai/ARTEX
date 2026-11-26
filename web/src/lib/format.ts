// Shared display formatters (sizes, token counts, durations, timestamps), so every
// page renders the same value the same way.

import { intlLocale } from "@/i18n";

const BYTE_UNITS = ["B", "KB", "MB", "GB"] as const;

/** Human file size: 512 B, 1.5 KB, 3.2 MB, 2.0 GB. Missing or non-positive sizes render as `empty` (default "0 B"). */
export function formatBytes(bytes: number | null | undefined, { empty = "0 B" }: { empty?: string } = {}): string {
  if (bytes == null || !Number.isFinite(bytes) || bytes <= 0) return empty;
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < BYTE_UNITS.length - 1) {
    value /= 1024;
    unit++;
  }
  return unit === 0 ? `${value} B` : `${value.toFixed(1)} ${BYTE_UNITS[unit]}`;
}

/** Compact token count: 950, 1.2k, 25k, 123.5k, 12.5M (one decimal, a trailing ".0" dropped). */
export function formatTokens(n: number): string {
  const oneDecimal = (v: number) => v.toFixed(1).replace(/\.0$/, "");
  // From 999,950 the "k" value would round up to "1000k", so switch to "M" there.
  if (n >= 999_950) return `${oneDecimal(n / 1_000_000)}M`;
  if (n >= 1000) return `${oneDecimal(n / 1000)}k`;
  return String(n);
}

/** Elapsed time from milliseconds: 42s, 3m05s, 2h07m. */
export function formatDurationMs(ms: number): string {
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m${String(s % 60).padStart(2, "0")}s`;
  const h = Math.floor(m / 60);
  return `${h}h${String(m % 60).padStart(2, "0")}m`;
}

/**
 * Short local timestamp: "11-26 14:03:22" (month-day hour:minute:second in the UI locale).
 * `seconds: false` drops the seconds, `year: true` prefixes a 2-digit year, and a
 * missing value renders as `empty`.
 */
export function formatShortDateTime(
  value: string | number | null | undefined,
  { seconds = true, year = false, empty = "" }: { seconds?: boolean; year?: boolean; empty?: string } = {},
): string {
  if (value == null || value === "") return empty;
  return new Date(value).toLocaleString(intlLocale, {
    ...(year ? { year: "2-digit" as const } : {}),
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    ...(seconds ? { second: "2-digit" as const } : {}),
  });
}

import assert from "node:assert/strict";
import { register } from "node:module";
import test from "node:test";

// format.ts reads the UI locale from "@/i18n", which needs the browser and the bundler.
// Replace it with a fixed locale so these tests run under plain `node --test`.
register(
  `data:text/javascript,${encodeURIComponent(`
    export async function resolve(specifier, context, next) {
      if (specifier === "@/i18n") {
        return { url: "data:text/javascript,export const intlLocale = 'en-US';", shortCircuit: true };
      }
      return next(specifier, context);
    }
  `)}`,
);
const { formatBytes, formatDurationMs, formatShortDateTime, formatTokens } = await import("./format.ts");

test("formatBytes picks the unit and keeps one decimal above bytes", () => {
  assert.equal(formatBytes(512), "512 B");
  assert.equal(formatBytes(1536), "1.5 KB");
  assert.equal(formatBytes(1024 ** 2), "1.0 MB");
  assert.equal(formatBytes(2 * 1024 ** 3), "2.0 GB");
});

test("formatBytes renders missing, zero and invalid sizes as the empty value", () => {
  for (const value of [0, -5, Number.NaN, null, undefined]) assert.equal(formatBytes(value), "0 B");
  assert.equal(formatBytes(null, { empty: "—" }), "—");
});

test("formatTokens keeps one decimal and drops a trailing .0", () => {
  assert.equal(formatTokens(950), "950");
  assert.equal(formatTokens(1234), "1.2k");
  assert.equal(formatTokens(25_000), "25k");
  assert.equal(formatTokens(123_456), "123.5k");
  assert.equal(formatTokens(12_500_000), "12.5M");
  assert.equal(formatTokens(50_000_000), "50M");
});

test("formatTokens switches to M where k would round up to 1000k", () => {
  assert.equal(formatTokens(999_949), "999.9k");
  assert.equal(formatTokens(999_950), "1M");
});

test("formatDurationMs renders seconds, minutes and hours", () => {
  assert.equal(formatDurationMs(42_000), "42s");
  assert.equal(formatDurationMs(185_000), "3m05s");
  assert.equal(formatDurationMs(7_620_000), "2h07m");
});

test("formatShortDateTime honours the seconds, year and empty options", () => {
  const ts = "2026-11-26T14:03:22";
  assert.match(formatShortDateTime(ts), /^11\/26, 0?2:03:22\s?PM$/);
  assert.match(formatShortDateTime(ts, { seconds: false }), /^11\/26, 0?2:03\s?PM$/);
  assert.match(formatShortDateTime(ts, { year: true, seconds: false }), /^11\/26\/26, 0?2:03\s?PM$/);
  assert.equal(formatShortDateTime("", { empty: "—" }), "—");
  assert.equal(formatShortDateTime(undefined), "");
});

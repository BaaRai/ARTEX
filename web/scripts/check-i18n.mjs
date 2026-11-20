import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

// Every literal t("...") / t('...') key must exist in the locale dictionary
// with the same {placeholders}. Dynamic keys (variables, template literals
// with ${}) are skipped: there is nothing to compare them against.
const root = join(dirname(fileURLToPath(import.meta.url)), "..");
const dict = JSON.parse(readFileSync(join(root, "src/i18n/locales/tr.json"), "utf8"));

const files = [];
function walk(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    const st = statSync(p);
    if (st.isDirectory()) walk(p);
    else if (/\.(ts|tsx)$/.test(name)) files.push(p);
  }
}
walk(join(root, "src"));

const calls = new Map(); // key -> first file that uses it
const literalCall = /(?:^|[^\w$.])t\(\s*(?:"((?:[^"\\]|\\.)*)"|'((?:[^'\\]|\\.)*)')/gm;
for (const file of files) {
  const src = readFileSync(file, "utf8");
  for (const m of src.matchAll(literalCall)) {
    const key =
      m[1] !== undefined ? JSON.parse(`"${m[1]}"`) : m[2].replace(/\\(['\\])/g, "$1");
    if (!calls.has(key)) calls.set(key, file.slice(root.length + 1));
  }
}

function placeholders(s) {
  return [...s.matchAll(/\{(\w+)\}/g)]
    .map((m) => m[1])
    .sort()
    .join(",");
}

const missing = [];
const mismatch = [];
for (const [key, file] of calls) {
  if (!Object.prototype.hasOwnProperty.call(dict, key)) {
    missing.push(`${file}: ${JSON.stringify(key)}`);
    continue;
  }
  if (placeholders(dict[key]) !== placeholders(key)) {
    mismatch.push(`${JSON.stringify(key)}: source {${placeholders(key)}} dict {${placeholders(dict[key])}}`);
  }
}

if (missing.length || mismatch.length) {
  if (missing.length) {
    console.error(`Missing tr.json entries (${missing.length}):\n${missing.join("\n")}`);
  }
  if (mismatch.length) {
    console.error(`Placeholder mismatches (${mismatch.length}):\n${mismatch.join("\n")}`);
  }
  process.exit(1);
}

console.log(
  `i18n: ${calls.size} literal keys checked against ${Object.keys(dict).length} tr.json entries`,
);

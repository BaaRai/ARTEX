// UI translation layer.
//
// The English source text is the key: t("System logs") looks the string up in the active
// locale's dictionary and falls back to the key itself, so a missing entry shows English
// instead of breaking. Placeholders use {name}: t("{count} lines", { count: 3 }).
//
// The locale is read once when this module loads and never changes while a page is open;
// setLocale() stores the choice and reloads. That keeps t() usable anywhere (components,
// module constants, toasts, non-React helpers) without hooks or re-render plumbing.
//
// To translate the UI into another language, add locales/<code>.json and list it in LOCALES.

import tr from "./locales/tr.json";

export type Locale = "tr" | "en";

// Used when nothing is saved and the browser language is not one of LOCALES,
// and for the static prerender, which has no browser.
export const DEFAULT_LOCALE: Locale = "en";

export const LOCALES: { code: Locale; label: string }[] = [
  { code: "tr", label: "Türkçe" },
  { code: "en", label: "English" },
];

const STORAGE_KEY = "artex.locale";

const DICTIONARIES: Record<Locale, Record<string, string>> = {
  tr: tr as Record<string, string>,
  en: {},
};

function detectLocale(): Locale {
  // Prerendering (static export) has no window: it renders the default locale.
  if (typeof window === "undefined") return DEFAULT_LOCALE;
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY);
    if (saved === "tr" || saved === "en") return saved;
  } catch {
    /* storage blocked: fall through to the browser language */
  }
  for (const lang of window.navigator.languages ?? [window.navigator.language]) {
    const base = lang?.toLowerCase().split("-")[0];
    const match = LOCALES.find((l) => l.code === base);
    if (match) return match.code;
  }
  return DEFAULT_LOCALE;
}

export const locale: Locale = detectLocale();

if (typeof document !== "undefined") document.documentElement.lang = locale;

/** BCP 47 tag for Intl / toLocaleString calls, e.g. date.toLocaleString(intlLocale). */
export const intlLocale = locale === "tr" ? "tr-TR" : "en-US";

const dict = DICTIONARIES[locale];

export type TVars = Record<string, string | number>;

export function t(key: string, vars?: TVars): string {
  const s = dict[key] ?? key;
  if (!vars) return s;
  return s.replace(/\{(\w+)\}/g, (m, name: string) => (name in vars ? String(vars[name]) : m));
}

export function setLocale(next: Locale) {
  if (next === locale) return;
  try {
    window.localStorage.setItem(STORAGE_KEY, next);
  } catch {
    /* storage blocked: the switch lasts only until the next load */
  }
  window.location.reload();
}

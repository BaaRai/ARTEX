// Client-side locale configuration for the static export.
//
// The production UI is a pure static export (NEXT_EXPORT=1), so there is no Node
// server and no per-request config: locale is resolved entirely on the client from
// localStorage (falling back to a cookie), defaulting to Korean.

export const locales = ["ko", "en"] as const;
export type Locale = (typeof locales)[number];

export const defaultLocale: Locale = "ko";

const STORAGE_KEY = "artex.locale";
const COOKIE_KEY = "NEXT_LOCALE";

export function isLocale(value: unknown): value is Locale {
  return typeof value === "string" && (locales as readonly string[]).includes(value);
}

/** Read the persisted locale on the client. Safe to call during SSG/SSR (returns the default). */
export function readStoredLocale(): Locale {
  if (typeof window === "undefined") return defaultLocale;
  try {
    const stored = window.localStorage.getItem(STORAGE_KEY);
    if (isLocale(stored)) return stored;
  } catch {
    // localStorage can throw in private mode or when blocked; fall through.
  }
  try {
    const match = document.cookie.match(/(?:^|;\s*)NEXT_LOCALE=([^;]+)/);
    if (match && isLocale(match[1])) return match[1];
  } catch {
    // ignore
  }
  return defaultLocale;
}

/** Persist the chosen locale to both localStorage and a cookie. */
export function persistLocale(locale: Locale): void {
  try {
    window.localStorage.setItem(STORAGE_KEY, locale);
  } catch {
    // ignore
  }
  try {
    document.cookie = `${COOKIE_KEY}=${locale};path=/;max-age=31536000;samesite=lax`;
  } catch {
    // ignore
  }
}

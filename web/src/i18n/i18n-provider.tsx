"use client";

import { createContext, useContext, useEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";

import { type IntlError, IntlErrorCode, NextIntlClientProvider } from "next-intl";

import { defaultLocale, type Locale, persistLocale, readStoredLocale } from "@/i18n/config";
import { messages } from "@/i18n/messages";

interface LocaleContextValue {
  locale: Locale;
  setLocale: (locale: Locale) => void;
}

const LocaleContext = createContext<LocaleContextValue | null>(null);

export function useLocaleSwitcher(): LocaleContextValue {
  const ctx = useContext(LocaleContext);
  if (!ctx) throw new Error("useLocaleSwitcher must be used within <I18nProvider>");
  return ctx;
}

// Resolve a dotted key path against the English catalog.
function resolveEnglish(path: string): string | undefined {
  const value = path.split(".").reduce<unknown>((acc, part) => {
    if (acc && typeof acc === "object") return (acc as Record<string, unknown>)[part];
    return undefined;
  }, messages.en as unknown);
  return typeof value === "string" ? value : undefined;
}

// A missing message is expected while the UI is translated incrementally; it must never
// throw and blank a screen in this self-hosted tool. Swallow MISSING_MESSAGE and let
// getMessageFallback supply English; surface anything else.
function handleIntlError(error: IntlError) {
  if (error.code === IntlErrorCode.MISSING_MESSAGE) return;
  console.error(error);
}

// Fall back to the English string for a key the active locale is missing, so an
// untranslated or mistyped key shows English rather than a raw key path.
function getMessageFallback({ namespace, key }: { namespace?: string; key: string; error: IntlError }) {
  const full = namespace ? `${namespace}.${key}` : key;
  return resolveEnglish(full) ?? resolveEnglish(key) ?? full;
}

export function I18nProvider({ children }: Readonly<{ children: ReactNode }>) {
  // Start from the default so the first client render matches the statically
  // prerendered HTML (no hydration mismatch); read the stored locale after mount.
  const [locale, setLocaleState] = useState<Locale>(defaultLocale);

  // biome-ignore lint/correctness/useExhaustiveDependencies: locale seeds state once after mount; depending on it would re-run and fight user locale switches.
  useEffect(() => {
    const stored = readStoredLocale();
    if (stored !== locale) setLocaleState(stored);
    // run once on mount
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    document.documentElement.lang = locale;
  }, [locale]);

  const value = useMemo<LocaleContextValue>(
    () => ({
      locale,
      setLocale: (next: Locale) => {
        persistLocale(next);
        setLocaleState(next);
      },
    }),
    [locale],
  );

  return (
    <LocaleContext.Provider value={value}>
      <NextIntlClientProvider
        locale={locale}
        messages={messages[locale]}
        timeZone="Asia/Seoul"
        onError={handleIntlError}
        getMessageFallback={getMessageFallback}
      >
        {children}
      </NextIntlClientProvider>
    </LocaleContext.Provider>
  );
}

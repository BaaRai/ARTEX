"use client";

import { Languages } from "lucide-react";
import { useTranslations } from "next-intl";

import { Button } from "@/components/ui/button";
import type { Locale } from "@/i18n/config";
import { useLocaleSwitcher } from "@/i18n/i18n-provider";

const LABELS: Record<Locale, string> = {
  ko: "한국어",
  en: "English",
};

/** Compact toggle that switches between Korean and English. */
export function LanguageToggle({ className }: { className?: string }) {
  const t = useTranslations("languageToggle");
  const { locale, setLocale } = useLocaleSwitcher();
  const next: Locale = locale === "ko" ? "en" : "ko";

  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      className={className}
      onClick={() => setLocale(next)}
      aria-label={t("switchTo", { language: LABELS[next] })}
      title={LABELS[next]}
    >
      <Languages className="size-4" />
      <span>{LABELS[locale]}</span>
    </Button>
  );
}

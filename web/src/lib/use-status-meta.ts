"use client";

import { useCallback } from "react";

import { useTranslations } from "next-intl";

import { statusMeta as rawStatusMeta, type StatusDomain } from "@/lib/status";

// Client hook over the central statusMeta(): same (domain, key) -> { label, tone }
// shape, but the label is localized via the `status` namespace (keyed
// `${domain}.${key}`). Tone and the unknown-key fallback come from the lib.
export function useStatusMeta() {
  const t = useTranslations("status");
  return useCallback(
    (domain: StatusDomain, key: string) => {
      const meta = rawStatusMeta(domain, key);
      const labelKey = `${domain}.${key}`;
      return t.has(labelKey) ? { ...meta, label: t(labelKey) } : meta;
    },
    [t],
  );
}

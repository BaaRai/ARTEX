"use client";

import { useEffect, useState } from "react";

import Link from "next/link";

import { Button } from "@/components/ui/button";
import { t } from "@/i18n";

export default function NotFound() {
  // This page is prerendered in the build-time locale; render the text only on the
  // client so a reader in another locale gets no hydration mismatch.
  const [mounted, setMounted] = useState(false);
  useEffect(() => setMounted(true), []);
  if (!mounted) return <div className="h-dvh" />;
  return (
    <div className="flex h-dvh flex-col items-center justify-center space-y-2 text-center">
      <h1 className="font-semibold text-2xl">{t("Page not found.")}</h1>
      <p className="text-muted-foreground">{t("The page you are looking for could not be found.")}</p>
      <Button asChild variant="outline">
        <Link prefetch={false} replace href="/dashboard">
          {t("Go to the dashboard")}
        </Link>
      </Button>
    </div>
  );
}

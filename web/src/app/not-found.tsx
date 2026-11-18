"use client";

import Link from "next/link";

import { Button } from "@/components/ui/button";
import { t } from "@/i18n";

export default function NotFound() {
  return (
    <div className="flex h-dvh flex-col items-center justify-center space-y-2 text-center">
      <h1 className="font-semibold text-2xl">{t("Page not found.")}</h1>
      <p className="text-muted-foreground">{t("The page you are looking for could not be found.")}</p>
      <Link prefetch={false} replace href="/function/tasks">
        <Button variant="outline">{t("Go back home")}</Button>
      </Link>
    </div>
  );
}

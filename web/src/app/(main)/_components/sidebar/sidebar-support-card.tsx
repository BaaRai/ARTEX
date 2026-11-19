import Link from "next/link";

import { siX } from "simple-icons";

import { SimpleIcon } from "@/components/simple-icon";
import { Card, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { t } from "@/i18n";

export function SidebarSupportCard() {
  // The X link sits inside the sentence through a placeholder so the word order can follow the locale.
  const [before, after = ""] = t("Open an issue or do reach out to me on {x}.").split("{x}");
  return (
    <Card size="sm" className="overflow-hidden shadow-none group-data-[collapsible=icon]:hidden">
      <CardHeader className="min-w-0 px-4">
        <CardTitle className="truncate text-sm">{t("Looking for something more?")}</CardTitle>
        <CardDescription className="line-clamp-2">
          {before.trimEnd()}&nbsp;
          <Link
            href="https://x.com/arhamkhnz"
            target="_blank"
            rel="noreferrer"
            aria-label={t("Reach out on X")}
            className="inline-flex items-center text-foreground"
          >
            <SimpleIcon icon={siX} aria-hidden className="size-3 fill-current" />
          </Link>
          {after}
        </CardDescription>
      </CardHeader>
    </Card>
  );
}

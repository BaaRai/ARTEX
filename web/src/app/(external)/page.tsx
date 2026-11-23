import { redirect } from "next/navigation";

import { t } from "@/i18n";

export default function Home() {
  redirect("/dashboard");
  return <>{t("Coming Soon")}</>;
}

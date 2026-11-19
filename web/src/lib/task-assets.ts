import type { useTranslations } from "next-intl";

import type { NewAssetType } from "@/lib/types";

// Localized display comes from the "taskAssets" namespace when a translator is threaded in; the English
// maps below stay as the fallback (and cover runtime kinds not present in the namespace).
type TaskAssetTranslator = ReturnType<typeof useTranslations<"taskAssets">>;

const ASSET_TYPE_LABELS: Record<NewAssetType, string> = {
  app: "App",
  endpoint: "Endpoint",
  ip: "IP",
  root_domain: "Root domain",
  service: "Service",
  subdomain: "Subdomain",
};

const TASK_ASSET_SOURCE_LABELS: Record<string, string> = {
  agent: "Agent-discovered",
  anchor: "Blackboard anchor",
  api: "Assets API",
  company: "Company association",
  legacy: "Legacy association",
  manual: "Manually added",
  system: "System association",
  task: "Task initialization",
};

export function taskAssetTypeLabel(type: NewAssetType, t?: TaskAssetTranslator): string {
  if (t?.has(`type.${type}`)) return t(`type.${type}`);
  return ASSET_TYPE_LABELS[type];
}

export function taskAssetSourceLabel(source: string, t?: TaskAssetTranslator): string {
  if (t?.has(`source.${source}`)) return t(`source.${source}`);
  return TASK_ASSET_SOURCE_LABELS[source] ?? source;
}

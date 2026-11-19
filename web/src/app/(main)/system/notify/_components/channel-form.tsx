"use client";

import { CheckIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import type { NotificationFilter } from "@/lib/types";

// asText / inputType are value helpers local to this file (tightly coupled to control rendering); they do not belong in channel-fields.
import { type FieldDef, type FieldKind, SEVERITY_OPTIONS } from "./channel-fields";

// asText renders any config value as a string an input can use.
// config comes from JSON; values may be string / number / boolean / array / null.
// All it cares about is "can it go into a text box" — actual serialization is buildConfig's job.
function asText(v: unknown): string {
  if (typeof v === "string") return v;
  if (v === null || v === undefined) return "";
  return String(v);
}

// inputType maps a field kind to the input's type attribute.
function inputType(kind: FieldKind): "text" | "password" | "number" {
  if (kind === "password") return "password";
  if (kind === "number") return "number";
  return "text";
}

// ConfigField renders the control matching a field definition.
//
// Masked fields are the one subtlety here: the input **does not show** the masked value itself, only a
// "Saved" line. That leaves one rule on screen — text in the box means the user typed it,
// an empty box means an empty value. Putting "__masked__:…abc123" into the input would look like placeholder
// text to delete, making accidental credential wipes more likely.
export function ConfigField({
  def,
  channel,
  value,
  isSecret,
  onChange,
}: {
  def: FieldDef;
  channel: string;
  value: unknown;
  isSecret: boolean;
  onChange: (v: unknown) => void;
}) {
  const t = useTranslations("notify");
  const id = `n-cfg-${def.key}`;
  const raw = asText(value);
  // Backend-echoed masked value: shaped like "__masked__:…abc123", with a recognizable tail of the original value.
  const masked = isSecret && raw.startsWith("__masked__");
  const maskedTail = masked ? (raw.split("…")[1] ?? "") : "";
  // Field copy is keyed by <channel>.<key> because keys collide across channels (e.g. "secret" differs for DingTalk vs Feishu);
  // the English label/help in channel-fields.ts stays as the fallback branch.
  const fieldLabel = t.has(`channelFields.${channel}.${def.key}.label`)
    ? t(`channelFields.${channel}.${def.key}.label`)
    : def.label;
  const fieldHelp =
    def.help &&
    (t.has(`channelFields.${channel}.${def.key}.help`) ? t(`channelFields.${channel}.${def.key}.help`) : def.help);

  if (def.kind === "switch") {
    return (
      <div className="flex items-center gap-2 text-sm">
        <Switch checked={value === true} onCheckedChange={onChange} aria-label={fieldLabel} />
        {fieldLabel}
        {fieldHelp && <span className="text-muted-foreground">({fieldHelp})</span>}
      </div>
    );
  }

  if (def.kind === "select") {
    return (
      <div className="grid gap-2">
        <Label>{fieldLabel}</Label>
        <Select value={raw || def.options?.[0]?.value} onValueChange={onChange}>
          <SelectTrigger>
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {(def.options ?? []).map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {t.has(`channelFields.${channel}.${def.key}.options.${o.value}`)
                  ? t(`channelFields.${channel}.${def.key}.options.${o.value}`)
                  : o.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>
    );
  }

  // Controls dispatch on field kind. An if chain rather than nested ternaries, because four controls need distinguishing
  // and a three-deep ternary already forces the reader to stop and count brackets.
  function control() {
    if (def.kind === "textarea" || def.kind === "kv") {
      return (
        <Textarea
          id={id}
          className="font-mono"
          placeholder={def.placeholder}
          value={masked ? "" : raw}
          onChange={(e) => onChange(e.target.value)}
        />
      );
    }
    if (def.kind === "list") {
      return (
        <Input
          id={id}
          value={Array.isArray(value) ? (value as string[]).join(", ") : raw}
          onChange={(e) => onChange(e.target.value)}
          placeholder={def.placeholder}
        />
      );
    }
    return (
      <Input
        id={id}
        className={def.kind === "text" ? "font-mono" : ""}
        type={inputType(def.kind)}
        placeholder={def.placeholder}
        value={masked ? "" : raw}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }

  const hint = masked ? (
    <p className="text-muted-foreground flex items-center gap-1 text-xs">
      <CheckIcon className="size-3" />
      {maskedTail ? t("channelForm.savedWithTail", { tail: maskedTail }) : t("channelForm.saved")}
    </p>
  ) : (
    fieldHelp && <p className="text-muted-foreground text-xs">{fieldHelp}</p>
  );

  return (
    <div className="grid gap-2">
      <Label htmlFor={id}>{fieldLabel}</Label>
      {control()}
      {hint}
    </div>
  );
}

// FilterSummary condenses the filter into one line so the card shows what a channel pushes without expanding it.
export function FilterSummary({ filter }: { filter: NotificationFilter }) {
  const t = useTranslations("notify");
  const parts: string[] = [];
  if (filter.min_severity) {
    const sevFallback = SEVERITY_OPTIONS.find((o) => o.value === filter.min_severity)?.label ?? filter.min_severity;
    parts.push(
      t.has(`severityOption.${filter.min_severity}`) ? t(`severityOption.${filter.min_severity}`) : sevFallback,
    );
  }
  if (filter.vulnclass_include?.length)
    parts.push(t("channelForm.filterSummary.typeKeywords", { count: filter.vulnclass_include.length }));
  if (filter.vulnclass_exclude?.length)
    parts.push(t("channelForm.filterSummary.excludeKeywords", { count: filter.vulnclass_exclude.length }));
  if (filter.task_ids?.length) parts.push(t("channelForm.filterSummary.tasks", { count: filter.task_ids.length }));
  if (filter.asset_ids?.length) parts.push(t("channelForm.filterSummary.assets", { count: filter.asset_ids.length }));
  if (filter.on_status_change) parts.push(t("channelForm.filterSummary.statusChanges"));
  if (parts.length === 0) {
    return <p className="text-muted-foreground text-sm">{t("channelForm.filterSummary.allVulns")}</p>;
  }
  return <p className="text-muted-foreground text-sm">{parts.join(" · ")}</p>;
}

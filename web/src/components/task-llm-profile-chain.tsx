"use client";

import * as React from "react";

import { ArrowDownIcon, ArrowUpIcon, XIcon } from "lucide-react";
import { useTranslations } from "next-intl";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Combobox,
  ComboboxChip,
  ComboboxChips,
  ComboboxChipsInput,
  ComboboxContent,
  ComboboxEmpty,
  ComboboxItem,
  ComboboxList,
  ComboboxValue,
} from "@/components/ui/combobox";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import type { LLMProfile } from "@/lib/types";

interface TaskLLMProfileChainProps {
  rotate?: boolean;
  onRotateChange?: (rotate: boolean) => void;
  profiles: LLMProfile[];
  value: string[];
  onValueChange: (value: string[]) => void;
  activeProfileId?: string;
  onActiveProfileChange?: (value: string) => void;
  disabled?: boolean;
  inputId?: string;
  portalContainer?: React.RefObject<HTMLElement | null>;
}

function ProfileRoleBadge({ index, currentIndex }: { index: number; currentIndex: number }) {
  const t = useTranslations("taskLlmProfileChain");
  if (index === currentIndex) return <Badge variant="default">{t("role.current")}</Badge>;
  if (index < currentIndex) {
    return (
      <Badge variant="outline" title={t("role.skippedTooltip")}>
        {t("role.skipped")}
      </Badge>
    );
  }
  return <Badge variant="secondary">{t("role.fallback", { n: index - currentIndex })}</Badge>;
}

export function TaskLLMProfileChain({
  rotate = false,
  onRotateChange,
  profiles,
  value,
  onValueChange,
  activeProfileId,
  onActiveProfileChange,
  disabled = false,
  inputId,
  portalContainer,
}: TaskLLMProfileChainProps) {
  const t = useTranslations("taskLlmProfileChain");
  const rotationID = React.useId();
  const [workerBinding, setWorkerBinding] = React.useState<number | null>(null);
  const [bindingCheckFailed, setBindingCheckFailed] = React.useState(false);
  React.useEffect(() => {
    api
      .agents()
      .then((agents) => setWorkerBinding(agents.find((a) => a.key === "worker")?.llm_profile_id ?? null))
      .catch(() => setBindingCheckFailed(true));
  }, []);
  const profilesByID = React.useMemo(() => new Map(profiles.map((profile) => [profile.id, profile])), [profiles]);
  const itemIDs = React.useMemo(() => profiles.map((profile) => profile.id), [profiles]);
  const profilesUnavailable = profiles.length === 0;
  const currentProfileID = activeProfileId && value.includes(activeProfileId) ? activeProfileId : value[0];
  const currentIndex = value.indexOf(currentProfileID);
  // api_key_hint is display-only and is empty for valid keys shorter than four
  // characters. The task API remains authoritative for profile validation.

  const profileLabel = React.useCallback(
    (id: string) => {
      const profile = profilesByID.get(id);
      return profile ? `${profile.name} ${profile.model}` : t("profileFallback", { id });
    },
    [profilesByID, t],
  );

  const move = (index: number, offset: -1 | 1) => {
    const target = index + offset;
    if (target < 0 || target >= value.length) return;
    const next = [...value];
    [next[index], next[target]] = [next[target], next[index]];
    onValueChange(next);
  };

  const remove = (id: string) => {
    const next = value.filter((profileID) => profileID !== id);
    onValueChange(next);
    if (activeProfileId === id) onActiveProfileChange?.(next[0] ?? "");
  };

  const handleValueChange = (next: string[]) => {
    onValueChange(next);
    if (activeProfileId && !next.includes(activeProfileId)) {
      onActiveProfileChange?.(next[0] ?? "");
    }
  };

  return (
    <div className="flex flex-col gap-3">
      {onRotateChange && (
        <div className="flex flex-col gap-2">
          <label htmlFor={rotationID} className="flex items-center justify-between gap-3 font-medium text-sm">
            {t("rotation.toggleLabel")}
            <Switch id={rotationID} checked={rotate} onCheckedChange={onRotateChange} disabled={disabled} />
          </label>
          <p className="text-muted-foreground text-xs">
            {rotate ? t("rotation.descriptionOn") : t("rotation.descriptionOff")}
          </p>
          {rotate && value.length < 2 && <p className="text-amber-600 text-xs">{t("rotation.warnMinProfiles")}</p>}
          {rotate && workerBinding != null && (
            <p role="alert" className="text-amber-600 text-xs">
              {t("rotation.warnBinding")}
            </p>
          )}
          {rotate && bindingCheckFailed && (
            <p className="text-amber-600 text-xs">{t("rotation.warnBindingUnchecked")}</p>
          )}
          {rotate && <p className="text-muted-foreground text-xs">{t("rotation.hint")}</p>}
        </div>
      )}
      <Combobox
        items={itemIDs}
        itemToStringValue={profileLabel}
        multiple
        value={value}
        onValueChange={handleValueChange}
        disabled={disabled}
      >
        <ComboboxChips>
          <ComboboxValue>
            {value.map((id) => (
              <ComboboxChip key={id}>{profilesByID.get(id)?.name ?? `#${id}`}</ComboboxChip>
            ))}
          </ComboboxValue>
          <ComboboxChipsInput
            id={inputId}
            placeholder={profiles.length > 0 ? t("combobox.searchPlaceholder") : t("combobox.noProfilesPlaceholder")}
            disabled={disabled ? true : profilesUnavailable}
          />
        </ComboboxChips>
        <ComboboxContent portalContainer={portalContainer}>
          <ComboboxEmpty>{t("combobox.empty")}</ComboboxEmpty>
          <ComboboxList>
            {(id) => {
              const profile = profilesByID.get(id);
              return (
                <ComboboxItem key={id} value={id} disabled={!profile}>
                  <div className="flex min-w-0 flex-1 flex-col">
                    <span className="flex min-w-0 items-center gap-2">
                      <span className="truncate">
                        {profile?.name ?? t("profileFallback", { id })}
                        {profile?.is_default ? t("activeSuffix") : ""}
                      </span>
                    </span>
                    {profile && <span className="truncate text-muted-foreground text-xs">{profile.model}</span>}
                  </div>
                </ComboboxItem>
              );
            }}
          </ComboboxList>
        </ComboboxContent>
      </Combobox>

      {value.length === 0 ? (
        <p className="text-muted-foreground text-xs">{t("emptyState")}</p>
      ) : (
        <div className="flex flex-col divide-y rounded-lg border">
          {value.map((id, index) => {
            const profile = profilesByID.get(id);
            return (
              <div
                key={id}
                className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-2 px-2.5 py-2 sm:flex"
              >
                <span className="w-5 shrink-0 text-center text-muted-foreground text-xs tabular-nums">{index + 1}</span>
                <div className="min-w-0 sm:flex-1">
                  <p className="truncate font-medium text-sm">{profile?.name ?? t("profileFallback", { id })}</p>
                  <div className="flex min-w-0 items-center gap-2">
                    <p className="truncate text-muted-foreground text-xs">
                      {profile?.model ?? t("profileUnavailable")}
                    </p>
                  </div>
                </div>
                {rotate ? (
                  <Badge variant="secondary">{t("rotation.badge", { n: index + 1 })}</Badge>
                ) : (
                  <ProfileRoleBadge index={index} currentIndex={currentIndex} />
                )}
                <div className="col-span-2 col-start-2 flex shrink-0 items-center gap-1 justify-self-end sm:col-auto sm:justify-self-auto">
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("actions.moveUp")}
                    onClick={() => move(index, -1)}
                    disabled={disabled ? true : index === 0}
                  >
                    <ArrowUpIcon data-icon="inline-start" />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("actions.moveDown")}
                    onClick={() => move(index, 1)}
                    disabled={disabled ? true : index === value.length - 1}
                  >
                    <ArrowDownIcon data-icon="inline-start" />
                  </Button>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon-sm"
                    aria-label={t("actions.remove")}
                    onClick={() => remove(id)}
                    disabled={disabled}
                  >
                    <XIcon data-icon="inline-start" />
                  </Button>
                </div>
              </div>
            );
          })}
        </div>
      )}

      {onActiveProfileChange && value.length > 0 && (
        <div className="flex flex-col items-stretch gap-2 sm:flex-row sm:items-center sm:justify-between sm:gap-3">
          <span className="font-medium text-sm">
            {rotate ? t("activeProfile.rotateLabel") : t("activeProfile.defaultLabel")}
          </span>
          <Select
            value={activeProfileId && value.includes(activeProfileId) ? activeProfileId : value[0]}
            onValueChange={onActiveProfileChange}
            disabled={disabled}
          >
            <SelectTrigger size="sm" className="w-full sm:min-w-48 sm:max-w-64">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                {value.map((id) => {
                  const profile = profilesByID.get(id);
                  return (
                    <SelectItem key={id} value={id} disabled={!profile}>
                      {profile?.name ?? t("profileFallback", { id })}
                    </SelectItem>
                  );
                })}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
      )}
    </div>
  );
}

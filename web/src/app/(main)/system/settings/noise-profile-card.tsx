"use client";

import * as React from "react";

import { GaugeIcon, RefreshCwIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { NoiseProfileName, NoiseStatus } from "@/lib/types";

// Mirrors egress.Profiles in egress/egress.go; the jitter of one send is
// min(Jitter/RPS, 250 ms).
const PROFILE_VALUES: NoiseProfileName[] = ["off", "quiet", "balanced", "aggressive"];

// NoiseProfileCard sets the target noise profile and shows the per-host
// counters of the current minute.
export function NoiseProfileCard() {
  const t = useTranslations("settings");
  const [status, setStatus] = React.useState<NoiseStatus | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);

  const refresh = React.useCallback(() => {
    setLoading(true);
    api
      .noiseStatus()
      .then(setStatus)
      .catch((e) => toast.error(t("noiseProfile.toasts.loadFailed", { message: (e as Error).message })))
      .finally(() => setLoading(false));
  }, [t]);
  React.useEffect(() => {
    refresh();
  }, [refresh]);

  const save = (value: NoiseProfileName) => {
    setSaving(true);
    api
      .setSettings({ noise_profile: value })
      .then(() => {
        toast.success(t("noiseProfile.toasts.saved", { value }));
        refresh();
      })
      .catch((e) => toast.error(t("noiseProfile.toasts.saveFailed", { message: (e as Error).message })))
      .finally(() => setSaving(false));
  };

  return (
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <GaugeIcon className="size-4" />
          {t("noiseProfile.title")}
        </CardTitle>
        <CardDescription>{t("noiseProfile.description")}</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-center justify-between gap-4">
          <Label htmlFor="noise-profile" className="text-sm font-normal text-muted-foreground">
            {t("noiseProfile.profileLabel")}
          </Label>
          <Select
            value={status?.profile ?? ""}
            disabled={!status || saving}
            onValueChange={(v) => save(v as NoiseProfileName)}
          >
            <SelectTrigger id="noise-profile" className="w-48 shrink-0">
              <SelectValue placeholder={t("noiseProfile.loadingPlaceholder")} />
            </SelectTrigger>
            <SelectContent>
              {PROFILE_VALUES.map((p) => (
                <SelectItem key={p} value={p}>
                  {t(`noiseProfile.profiles.${p}.label`)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <p className="text-muted-foreground text-xs">{t(`noiseProfile.profiles.${status?.profile ?? "off"}.limits`)}</p>
        <div className="flex items-center justify-between gap-4">
          <span className="font-medium text-sm">{t("noiseProfile.hostsCurrentMinute")}</span>
          <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
            <RefreshCwIcon data-icon="inline-start" className={loading ? "animate-spin" : undefined} />
            {t("noiseProfile.refresh")}
          </Button>
        </div>
        {status?.hosts.length ? (
          <Table aria-label={t("noiseProfile.tableAria")}>
            <TableHeader>
              <TableRow>
                <TableHead>{t("noiseProfile.columns.host")}</TableHead>
                <TableHead className="text-right">{t("noiseProfile.columns.rpm")}</TableHead>
                <TableHead className="text-right">{t("noiseProfile.columns.peakRpm")}</TableHead>
                <TableHead className="text-right">{t("noiseProfile.columns.inFlight")}</TableHead>
                <TableHead className="text-right">{t("noiseProfile.columns.duplicates")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {status.hosts.map((h) => (
                <TableRow key={h.host}>
                  <TableCell className="max-w-48 truncate font-mono text-xs" title={h.host}>
                    {h.host}
                  </TableCell>
                  <TableCell className="text-right">{h.rpm}</TableCell>
                  <TableCell className="text-right">{h.peak_rpm}</TableCell>
                  <TableCell className="text-right">
                    {h.inflight} ({h.peak_inflight})
                  </TableCell>
                  <TableCell className="text-right">{h.duplicates_avoided}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className="text-muted-foreground text-sm">
            {status?.profile === "off" ? t("noiseProfile.emptyOff") : t("noiseProfile.emptyNone")}
          </p>
        )}
      </CardContent>
    </Card>
  );
}

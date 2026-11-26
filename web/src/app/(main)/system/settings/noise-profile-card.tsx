"use client";

import * as React from "react";

import { GaugeIcon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { t } from "@/i18n";
import { api } from "@/lib/api";
import type { NoiseProfileName, NoiseStatus } from "@/lib/types";

// Mirrors egress.Profiles in egress/egress.go; the jitter of one send is
// min(Jitter/RPS, 250 ms).
const profiles: { value: NoiseProfileName; label: string; limits: string }[] = [
  { value: "off", label: t("Off"), limits: t("Off: requests and their timing are unchanged, and nothing is counted.") },
  {
    value: "quiet",
    label: t("Quiet"),
    limits: t(
      "2 req/s per host (burst 4), 1 in flight per host, 8 overall, jitter up to 250 ms, identical GETs of a task cached 60 s, up to 3 retries on 429/503.",
    ),
  },
  {
    value: "balanced",
    label: t("Balanced"),
    limits: t(
      "5 req/s per host (burst 10), 2 in flight per host, 16 overall, jitter up to 40 ms, identical GETs of a task cached 30 s, up to 3 retries on 429/503.",
    ),
  },
  {
    value: "aggressive",
    label: t("Aggressive"),
    limits: t(
      "20 req/s per host (burst 40), 4 in flight per host, 32 overall, jitter up to 5 ms, identical GETs of a task cached 10 s, up to 2 retries on 429/503.",
    ),
  },
];

type NoiseReading =
  | { state: "loading"; status: NoiseStatus | null }
  | { state: "ready"; status: NoiseStatus }
  | { state: "error"; status: NoiseStatus | null; message: string };

// NoiseProfileCard sets the target noise profile and shows the per-host
// counters of the current minute.
export function NoiseProfileCard() {
  const [reading, setReading] = React.useState<NoiseReading>({ state: "loading", status: null });
  const [saving, setSaving] = React.useState(false);
  const latestRequest = React.useRef(0);

  const refresh = React.useCallback(async () => {
    const request = ++latestRequest.current;
    setReading(({ status }) => ({ state: "loading", status }));
    try {
      const status = await api.noiseStatus();
      if (!status || !profiles.some((profile) => profile.value === status.profile) || !Array.isArray(status.hosts)) {
        throw new Error(t("Invalid noise status response."));
      }
      if (request === latestRequest.current) setReading({ state: "ready", status });
    } catch (error) {
      if (request === latestRequest.current) {
        const message = error instanceof Error ? error.message : String(error);
        setReading(({ status }) => ({ state: "error", status, message }));
      }
    }
  }, []);
  React.useEffect(() => {
    void refresh();
    return () => {
      latestRequest.current += 1;
    };
  }, [refresh]);

  const save = (value: NoiseProfileName) => {
    setSaving(true);
    api
      .setSettings({ noise_profile: value })
      .then(() => {
        toast.success(
          t("Noise profile set to {profile}", { profile: profiles.find((p) => p.value === value)?.label ?? value }),
        );
        return refresh();
      })
      .catch((e) => toast.error(t("Save failed: {error}", { error: (e as Error).message })))
      .finally(() => setSaving(false));
  };

  const { status } = reading;
  const loading = reading.state === "loading";
  const current = profiles.find((p) => p.value === status?.profile);
  return (
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <GaugeIcon className="size-4" />
          {t("Target noise")}
        </CardTitle>
        <CardDescription>
          {t(
            "Paces the requests ARTEX itself sends to targets (WebFetch, http custom tools, the enrich probe) and sets the low-and-slow guidance in agent prompts. With traffic capture on, Bash commands that use the proxy (curl, wget, python-requests…) are paced too, by rate and jitter only: the in-flight, cache and retry limits apply to ARTEX's own requests. Raw scanners such as nmap, and commands that bypass the proxy, are not paced; this is best effort, not a security boundary. This setting takes precedence over ARTEX_NOISE_PROFILE and applies to the next request.",
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        <div className="flex items-center justify-between gap-4">
          <Label htmlFor="noise-profile" className="text-sm font-normal text-muted-foreground">
            {t("Profile")}
          </Label>
          <Select
            value={status?.profile ?? ""}
            disabled={reading.state !== "ready" || saving}
            onValueChange={(v) => save(v as NoiseProfileName)}
          >
            <SelectTrigger id="noise-profile" className="w-48 shrink-0">
              <SelectValue placeholder={loading ? t("Loading…") : t("Noise status unavailable")} />
            </SelectTrigger>
            <SelectContent>
              {profiles.map((p) => (
                <SelectItem key={p.value} value={p.value}>
                  {p.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        {reading.state === "error" && (
          <p className="text-destructive text-xs" role="alert">
            {t("Failed to load the noise counters: {error}", { error: reading.message })}
          </p>
        )}
        {status && reading.state !== "ready" && (
          <p className="text-muted-foreground text-xs" role="status">
            {t("Showing the last successfully loaded noise profile and counters.")}
          </p>
        )}
        {current && <p className="text-muted-foreground text-xs">{current.limits}</p>}
        <div className="flex items-center justify-between gap-4">
          <span className="font-medium text-sm">
            {status && reading.state !== "ready" ? t("Last available host counters") : t("Hosts, current minute")}
          </span>
          <Button variant="outline" size="sm" onClick={refresh} disabled={loading || saving}>
            <RefreshCwIcon data-icon="inline-start" className={loading ? "animate-spin" : undefined} />
            {t("Refresh")}
          </Button>
        </div>
        {status?.hosts.length ? (
          <Table aria-label={t("Noise counters per host")}>
            <TableHeader>
              <TableRow>
                <TableHead>{t("Host")}</TableHead>
                <TableHead className="text-right">RPM</TableHead>
                <TableHead className="text-right">{t("Peak RPM")}</TableHead>
                <TableHead className="text-right">{t("In flight (peak)")}</TableHead>
                <TableHead className="text-right">{t("Duplicates")}</TableHead>
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
        ) : reading.state === "ready" ? (
          <p className="text-muted-foreground text-sm">
            {reading.status.profile === "off" ? t("Off counts no request.") : t("No paced request yet.")}
          </p>
        ) : null}
      </CardContent>
    </Card>
  );
}

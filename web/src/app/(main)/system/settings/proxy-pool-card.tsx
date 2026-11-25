"use client";

import * as React from "react";

import { NetworkIcon, PlayIcon, RefreshCwIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { t } from "@/i18n";
import { api } from "@/lib/api";
import type { ProxyPoolEntry } from "@/lib/types";

const rotations: { value: string; label: string }[] = [
  { value: "none", label: t("None · use the active proxy only") },
  {
    value: "on_failure",
    label: t("On failure · rotate to the next enabled proxy"),
  },
  { value: "timed", label: t("Timed · rotate on a timer") },
];

export function ProxyPoolCard() {
  const [pool, setPool] = React.useState<ProxyPoolEntry[]>([]);
  const [rotation, setRotation] = React.useState<string>("none");
  const [loading, setLoading] = React.useState(false);
  const [adding, setAdding] = React.useState(false);
  const [address, setAddress] = React.useState("");
  const [testing, setTesting] = React.useState<number | null>(null); // id under test

  const load = React.useCallback(() => {
    setLoading(true);
    api
      .proxyPool()
      .then(({ proxy_pool, rotation }) => {
        setPool(proxy_pool);
        setRotation(rotation);
      })
      .catch((e) =>
        toast.error(
          t("Failed to load the proxy pool: {error}", {
            error: (e as Error).message,
          }),
        ),
      )
      .finally(() => setLoading(false));
  }, []);

  React.useEffect(() => {
    load();
  }, [load]);

  const fmtTs = (ts: number) => {
    if (!ts) return t("Never");
    return new Date(ts * 1000).toLocaleString();
  };

  const add = () => {
    if (!address.trim()) return;
    setAdding(true);
    api
      .addProxyPool(address.trim())
      .then(() => {
        setAddress("");
        toast.success(t("Added proxy to the pool"));
        load();
      })
      .catch((e) =>
        toast.error(t("Add failed: {error}", { error: (e as Error).message })),
      )
      .finally(() => setAdding(false));
  };

  const remove = (id: number) => {
    if (!window.confirm(t("Remove this proxy from the pool?"))) return;
    api
      .deleteProxyPool(id)
      .then(() => {
        toast.success(t("Removed proxy from the pool"));
        load();
      })
      .catch((e) =>
        toast.error(t("Delete failed: {error}", { error: (e as Error).message })),
      );
  };

  const toggle = (id: number, patch: { enabled?: boolean; active?: boolean }, label: string) => {
    api
      .patchProxyPool(id, patch)
      .then(() => {
        toast.success(label);
        load();
      })
      .catch((e) =>
        toast.error(t("Save failed: {error}", { error: (e as Error).message })),
      );
  };

  const test = (id: number, address: string) => {
    setTesting(id);
    api
      .testProxyPool(address)
      .then(({ egress_ip }) => {
        toast.success(t("Egress IP: {ip}", { ip: egress_ip }));
        load();
      })
      .catch((e) =>
        toast.error(t("Test failed: {error}", { error: (e as Error).message })),
      )
      .finally(() => setTesting(null));
  };

  const saveRotation = () => {
    api
      .putProxyPoolRotation(rotation)
      .then(() => toast.success(t("Rotation strategy saved")))
      .catch((e) =>
        toast.error(t("Save failed: {error}", { error: (e as Error).message })),
      );
  };

  return (
    <Card className="mb-4 break-inside-avoid md:mb-6">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-base">
          <NetworkIcon className="size-4" />
          {t("Proxy pool")}
        </CardTitle>
        <CardDescription>
          {t(
            "A pool of egress proxies with enable/disable, active selection and egress-IP testing (restored 0.7.x feature). Only one proxy may be active at a time.",
          )}
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        {/* Rotation strategy */}
        <div className="flex items-center justify-between gap-4">
          <Label htmlFor="proxy-pool-rotation" className="text-sm font-normal text-muted-foreground">
            {t("Rotation")}
          </Label>
          <Select
            value={rotation}
            disabled={loading}
            onValueChange={(v) => setRotation(v)}
          >
            <SelectTrigger id="proxy-pool-rotation" className="w-48 shrink-0">
              <SelectValue placeholder={t("Choose a strategy")} />
            </SelectTrigger>
            <SelectContent>
              {rotations.map((r) => (
                <SelectItem key={r.value} value={r.value}>
                  {r.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={saveRotation}
            disabled={loading}
          >
            {t("Save")}
          </Button>
        </div>

        {/* Add new proxy */}
        <div className="flex items-center gap-2">
          <Input
            id="proxy-pool-add"
            placeholder={t("socks5://user:pass@host:1080 or http://host:port")}
            value={address}
            disabled={loading || adding}
            onChange={(e) => setAddress(e.target.value)}
          />
          <Button
            type="button"
            onClick={add}
            disabled={loading || adding || !address.trim()}
          >
            {adding ? t("Adding…") : t("Add")}
          </Button>
        </div>

        {/* Pool table */}
        <div className="overflow-x-auto">
          <Table aria-label={t("Proxy pool entries")}>
            <TableHeader>
              <TableRow>
                <TableHead>{t("Address")}</TableHead>
                <TableHead className="text-center">{t("Enabled")}</TableHead>
                <TableHead className="text-center">{t("Active")}</TableHead>
                <TableHead className="text-right">{t("Last tested")}</TableHead>
                <TableHead className="text-right">{t("Egress IP")}</TableHead>
                <TableHead className="text-right">{t("Failures")}</TableHead>
                <TableHead className="text-right">{t("Actions")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {pool.length ? (
                pool.map((e) => (
                  <TableRow key={e.id}>
                    <TableCell className="max-w-64 truncate font-mono text-sm" title={e.address}>
                      {e.address}
                    </TableCell>
                    <TableCell className="text-center">
                      <Switch
                        checked={e.enabled}
                        onCheckedChange={(v) =>
                          toggle(e.id, { enabled: v }, v ? t("Enabled") : t("Disabled"))
                        }
                        disabled={loading}
                      />
                    </TableCell>
                    <TableCell className="text-center">
                      {e.active ? (
                        <span className="text-xs font-medium text-emerald-600 dark:text-emerald-400">
                          {t("Active")}
                        </span>
                      ) : (
                        <Button
                          type="button"
                          variant="outline"
                          size="sm"
                          onClick={() =>
                            toggle(
                              e.id,
                              { active: true },
                              t("Set as active proxy"),
                            )
                          }
                          disabled={loading}
                        >
                          {t("Set active")}
                        </Button>
                      )}
                    </TableCell>
                    <TableCell className="text-right text-xs text-muted-foreground">
                      {fmtTs(e.last_tested ?? 0)}
                    </TableCell>
                    <TableCell className="text-right text-sm font-mono">
                      {e.egress_ip ?? <span className="text-muted-foreground">—</span>}
                    </TableCell>
                    <TableCell className="text-right text-xs">{e.fail_count ?? 0}</TableCell>
                    <TableCell className="text-right">
                      <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        onClick={() => test(e.id, e.address)}
                        disabled={loading || testing === e.id}
                      >
                        {testing === e.id ? (
                          <>
                            <RefreshCwIcon data-icon="inline-start" className="animate-spin size-3" />
                            {t("Testing…")}
                          </>
                        ) : (
                          <>
                            <PlayIcon data-icon="inline-start" className="size-3" />
                            {t("Test egress")}
                          </>
                        )}
                      </Button>
                      <Button
                        type="button"
                        variant="destructive"
                        size="sm"
                        onClick={() => remove(e.id)}
                        disabled={loading}
                      >
                        <Trash2Icon data-icon="inline-start" className="size-3" />
                      </Button>
                    </TableCell>
                  </TableRow>
                ))
              ) : (
                <TableRow>
                  <TableCell colSpan={7} className="text-muted-foreground text-sm text-center py-4">
                    {t("No proxies in the pool yet. Add one above.")}
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
        {pool.length === 0 && (
          <p className="text-muted-foreground text-xs">
            {t(
              "Add http://, https:// or socks5:// proxies (optionally user:pass@host:port). Test each one to see the egress IP it reveals, pick the active proxy, and choose how the pool rotates on failure.",
            )}
          </p>
        )}
      </CardContent>
    </Card>
  );
}

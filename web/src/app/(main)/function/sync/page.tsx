"use client";

import * as React from "react";

import { AlertCircleIcon, CheckCircle2Icon, DownloadIcon, PlugZapIcon, RefreshCwIcon, SearchIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import type { SSProject, SSTask } from "@/lib/types";

type SSStatus = {
  exists: boolean;
  configured: boolean;
  enabled: boolean;
  reachable: boolean;
  url?: string;
  tools: string[];
};

type Dimension = "project" | "task";

const ASSET_TYPES: { key: string; label: string }[] = [
  { key: "subdomain", label: "Subdomain" },
  { key: "service", label: "Service" },
  { key: "app", label: "App" },
];

export default function AssetSyncPage() {
  const t = useTranslations("sync");
  return (
    <div className="p-4 md:p-6">
      <div className="mb-4">
        <h1 className="font-semibold text-xl">{t("page.title")}</h1>
        <p className="text-muted-foreground text-sm">{t("page.subtitle")}</p>
      </div>
      <Tabs defaultValue="scopesentry">
        <TabsList>
          <TabsTrigger value="scopesentry">ScopeSentry</TabsTrigger>
        </TabsList>
        <TabsContent value="scopesentry" className="mt-4">
          <ScopeSentryPanel />
        </TabsContent>
      </Tabs>
    </div>
  );
}

function ScopeSentryPanel() {
  const t = useTranslations("sync");
  const [status, setStatus] = React.useState<SSStatus | null>(null);
  const [loadingStatus, setLoadingStatus] = React.useState(true);

  const loadStatus = React.useCallback(() => {
    setLoadingStatus(true);
    api
      .ssStatus()
      .then(setStatus)
      .catch((e) => toast.error(t("toasts.statusReadFailed", { message: e.message })))
      .finally(() => setLoadingStatus(false));
  }, [t]);

  React.useEffect(() => {
    loadStatus();
  }, [loadStatus]);

  const ready = !!status && status.exists && status.configured && status.enabled;

  return (
    <div className="space-y-4">
      <DataSourceCard status={status} loading={loadingStatus} onChanged={loadStatus} />
      {ready ? (
        <SyncWorkbench />
      ) : (
        <Card>
          <CardContent className="py-8 text-center text-muted-foreground text-sm">
            {t("panel.notReady")}
          </CardContent>
        </Card>
      )}
    </div>
  );
}

// ── Data source status card ─────────────────────────────────────────────────────────────

function DataSourceCard({
  status,
  loading,
  onChanged,
}: {
  status: SSStatus | null;
  loading: boolean;
  onChanged: () => void;
}) {
  const t = useTranslations("sync");
  const [url, setUrl] = React.useState("");
  const [apiKey, setApiKey] = React.useState("");
  const [busy, setBusy] = React.useState(false);

  React.useEffect(() => {
    if (status?.url) setUrl(status.url);
  }, [status?.url]);

  const create = async () => {
    setBusy(true);
    try {
      await api.ssDatasource({});
      toast.success(t("toasts.created"));
      onChanged();
    } catch (e) {
      toast.error(t("toasts.createFailed", { message: (e as Error).message }));
    } finally {
      setBusy(false);
    }
  };

  const save = async () => {
    if (!url.trim()) return toast.error(t("toasts.enterUrl"));
    setBusy(true);
    try {
      const r = await api.ssDatasource({ url: url.trim(), api_key: apiKey.trim() });
      toast.success(r.enabled ? t("toasts.savedEnabled") : t("toasts.savedNotEnabled"));
      setApiKey("");
      onChanged();
    } catch (e) {
      toast.error(t("toasts.saveFailed", { message: (e as Error).message }));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="flex items-center gap-2 text-base">
          <PlugZapIcon className="size-4" /> {t("dataSource.title")}
          <StatusBadge status={status} loading={loading} />
        </CardTitle>
        <Button variant="ghost" size="sm" onClick={onChanged} disabled={loading}>
          <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} /> {t("dataSource.refresh")}
        </Button>
      </CardHeader>
      <CardContent className="space-y-3">
        {!status?.exists ? (
          <div className="flex items-center justify-between gap-4">
            <p className="text-muted-foreground text-sm">
              {t("dataSource.noneYet")}
            </p>
            <Button onClick={create} disabled={busy}>
              {t("dataSource.create")}
            </Button>
          </div>
        ) : (
          <>
            {!status.configured && (
              <p className="text-amber-600 text-sm dark:text-amber-500">
                {t("dataSource.notConfigured")}
              </p>
            )}
            {status.configured && !status.enabled && (
              <p className="text-amber-600 text-sm dark:text-amber-500">{t("dataSource.notEnabled")}</p>
            )}
            <div className="grid gap-3 md:grid-cols-2">
              <div className="space-y-1.5">
                <Label>MCP URL</Label>
                <Input placeholder="http://<host>:8082/mcp" value={url} onChange={(e) => setUrl(e.target.value)} />
              </div>
              <div className="space-y-1.5">
                <Label>{t("dataSource.apiKeyLabel")}</Label>
                <Input
                  type="password"
                  placeholder="ssk_..."
                  value={apiKey}
                  onChange={(e) => setApiKey(e.target.value)}
                />
              </div>
            </div>
            <div className="flex items-center gap-2">
              <Button onClick={save} disabled={busy}>
                {t("dataSource.save")}
              </Button>
              {status.enabled && status.tools.length > 0 && (
                <span className="text-muted-foreground text-xs">{t("dataSource.toolsDiscovered", { count: status.tools.length })}</span>
              )}
            </div>
          </>
        )}
      </CardContent>
    </Card>
  );
}

function StatusBadge({ status, loading }: { status: SSStatus | null; loading: boolean }) {
  const t = useTranslations("sync");
  if (loading || !status) return <Badge variant="secondary">{t("status.checking")}</Badge>;
  if (!status.exists) return <Badge variant="destructive">{t("status.notCreated")}</Badge>;
  if (!status.configured) return <Badge variant="outline">{t("status.notConfigured")}</Badge>;
  if (!status.enabled) return <Badge variant="outline">{t("status.notEnabled")}</Badge>;
  if (status.reachable)
    return (
      <Badge className="bg-emerald-600 hover:bg-emerald-600">
        <CheckCircle2Icon className="mr-1 size-3" /> {t("status.connected")}
      </Badge>
    );
  return (
    <Badge variant="destructive">
      <AlertCircleIcon className="mr-1 size-3" /> {t("status.unreachable")}
    </Badge>
  );
}

// ── Sync workbench (project / task dimension) ────────────────────────────────────────────────

function SyncWorkbench() {
  const t = useTranslations("sync");
  const [dimension, setDimension] = React.useState<Dimension>("project");
  const [assetTypes, setAssetTypes] = React.useState<Record<string, boolean>>({
    subdomain: true,
    service: true,
    app: true,
  });
  const [createCompany, setCreateCompany] = React.useState(true);

  const [projects, setProjects] = React.useState<SSProject[]>([]);
  const [tasks, setTasks] = React.useState<SSTask[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [search, setSearch] = React.useState("");
  const [page, setPage] = React.useState(1);
  const [selected, setSelected] = React.useState<Set<string>>(new Set());

  const [syncing, setSyncing] = React.useState(false);
  const [result, setResult] = React.useState<Awaited<ReturnType<typeof api.ssSync>> | null>(null);

  const load = React.useCallback(() => {
    setLoading(true);
    setSelected(new Set());
    const fn =
      dimension === "project"
        ? api.ssProjects(page, 50, search).then((r) => setProjects(r.projects))
        : api.ssTasks(page, 50, search).then(setTasks);
    fn.catch((e) => toast.error(t("toasts.loadListFailed", { message: e.message }))).finally(() => setLoading(false));
  }, [dimension, page, search, t]);

  React.useEffect(() => {
    load();
  }, [load]);

  const rows = dimension === "project" ? projects : tasks;
  const idOf = (row: SSProject | SSTask) => (dimension === "project" ? (row as SSProject).id : (row as SSTask).name);

  const toggle = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const toggleAll = () => {
    setSelected((prev) => (prev.size === rows.length ? new Set() : new Set(rows.map(idOf))));
  };

  const chosenTypes = ASSET_TYPES.filter((t) => assetTypes[t.key]).map((t) => t.key);

  const runSync = async () => {
    if (selected.size === 0) return toast.error(dimension === "project" ? t("toasts.selectProject") : t("toasts.selectTask"));
    if (chosenTypes.length === 0) return toast.error(t("toasts.selectAssetType"));
    setSyncing(true);
    setResult(null);
    try {
      const r = await api.ssSync({
        dimension,
        targets: [...selected],
        asset_types: chosenTypes,
        create_company: dimension === "project" ? createCompany : false,
      });
      setResult(r);
      const total = Object.values(r.synced ?? {}).reduce((a, b) => a + b, 0);
      toast.success(t("toasts.syncFinished", { count: total }));
    } catch (e) {
      toast.error(t("toasts.syncFailed", { message: (e as Error).message }));
    } finally {
      setSyncing(false);
    }
  };

  const renderRows = () => {
    if (loading) {
      return (
        <TableRow>
          <TableCell colSpan={4} className="py-8 text-center text-muted-foreground text-sm">
            {t("table.loading")}
          </TableCell>
        </TableRow>
      );
    }
    if (rows.length === 0) {
      return (
        <TableRow>
          <TableCell colSpan={4} className="py-8 text-center text-muted-foreground text-sm">
            {t("table.noData")}
          </TableCell>
        </TableRow>
      );
    }
    if (dimension === "project") {
      return projects.map((p) => (
        <TableRow key={p.id} className="cursor-pointer" onClick={() => toggle(p.id)}>
          <TableCell onClick={(e) => e.stopPropagation()}>
            <Checkbox checked={selected.has(p.id)} onCheckedChange={() => toggle(p.id)} />
          </TableCell>
          <TableCell className="font-medium">{p.name}</TableCell>
          <TableCell>{p.tag ? <Badge variant="secondary">{p.tag}</Badge> : "—"}</TableCell>
          <TableCell className="text-right">{p.AssetCount ?? 0}</TableCell>
        </TableRow>
      ));
    }
    return tasks.map((t) => (
      <TableRow key={t.id} className="cursor-pointer" onClick={() => toggle(t.name)}>
        <TableCell onClick={(e) => e.stopPropagation()}>
          <Checkbox checked={selected.has(t.name)} onCheckedChange={() => toggle(t.name)} />
        </TableCell>
        <TableCell className="font-medium">{t.name}</TableCell>
        <TableCell>
          <Badge variant={t.progress === 100 ? "secondary" : "outline"}>
            {t.progress != null ? `${t.progress}%` : "—"}
          </Badge>
        </TableCell>
        <TableCell className="text-muted-foreground text-xs">{t.endTime || t.creatTime || "—"}</TableCell>
      </TableRow>
    ));
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle className="text-base">{t("workbench.title")}</CardTitle>
      </CardHeader>
      <CardContent className="space-y-4">
        {/* Dimension switch */}
        <Tabs
          value={dimension}
          onValueChange={(v) => {
            setDimension(v as Dimension);
            setPage(1);
          }}
        >
          <TabsList>
            <TabsTrigger value="project">{t("workbench.byProject")}</TabsTrigger>
            <TabsTrigger value="task">{t("workbench.byTask")}</TabsTrigger>
          </TabsList>
        </Tabs>

        {/* Asset types + options */}
        <div className="flex flex-wrap items-center gap-4">
          <span className="font-medium text-sm">{t("workbench.assetsToSync")}</span>
          {ASSET_TYPES.map((at) => (
            <label key={at.key} htmlFor={`at-${at.key}`} className="flex items-center gap-1.5 text-sm">
              <Checkbox
                id={`at-${at.key}`}
                checked={!!assetTypes[at.key]}
                onCheckedChange={(c) => setAssetTypes((prev) => ({ ...prev, [at.key]: !!c }))}
              />
              {t.has(`workbench.assetType.${at.key}`) ? t(`workbench.assetType.${at.key}`) : at.label}
            </label>
          ))}
          {dimension === "project" && (
            <label htmlFor="create-company" className="flex items-center gap-1.5 text-sm">
              <Checkbox id="create-company" checked={createCompany} onCheckedChange={(c) => setCreateCompany(!!c)} />
              {t("workbench.createCompany")}
            </label>
          )}
        </div>

        {/* Search + actions */}
        <div className="flex items-center gap-2">
          <div className="relative max-w-xs flex-1">
            <SearchIcon className="absolute top-1/2 left-2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="pl-8"
              placeholder={dimension === "project" ? t("workbench.searchProject") : t("workbench.searchTask")}
              value={search}
              onChange={(e) => setSearch(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  setPage(1);
                  load();
                }
              }}
            />
          </div>
          <Button variant="outline" size="sm" onClick={load} disabled={loading}>
            <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} />
          </Button>
          <div className="flex-1" />
          <span className="text-muted-foreground text-xs">{t("workbench.selectedCount", { count: selected.size })}</span>
          <Button onClick={runSync} disabled={syncing || selected.size === 0}>
            <DownloadIcon className={syncing ? "size-4 animate-pulse" : "size-4"} /> {t("workbench.syncSelected")}
          </Button>
        </div>

        {/* List */}
        <div className="rounded-md border">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="w-10">
                  <Checkbox checked={rows.length > 0 && selected.size === rows.length} onCheckedChange={toggleAll} />
                </TableHead>
                <TableHead>{dimension === "project" ? t("table.projectName") : t("table.taskName")}</TableHead>
                {dimension === "project" ? (
                  <>
                    <TableHead>{t("table.tag")}</TableHead>
                    <TableHead className="text-right">{t("table.assets")}</TableHead>
                  </>
                ) : (
                  <>
                    <TableHead>{t("table.status")}</TableHead>
                    <TableHead>{t("table.time")}</TableHead>
                  </>
                )}
              </TableRow>
            </TableHeader>
            <TableBody>{renderRows()}</TableBody>
          </Table>
        </div>

        {/* Pagination */}
        <div className="flex items-center justify-end gap-2">
          <Button variant="outline" size="sm" disabled={page <= 1 || loading} onClick={() => setPage((p) => p - 1)}>
            {t("pagination.previous")}
          </Button>
          <span className="text-muted-foreground text-xs">{t("pagination.page", { page })}</span>
          <Button
            variant="outline"
            size="sm"
            disabled={rows.length < 50 || loading}
            onClick={() => setPage((p) => p + 1)}
          >
            {t("pagination.next")}
          </Button>
        </div>

        {/* Result */}
        {result && <SyncResult result={result} />}
      </CardContent>
    </Card>
  );
}

function SyncResult({ result }: { result: Awaited<ReturnType<typeof api.ssSync>> }) {
  const t = useTranslations("sync");
  const synced = result.synced ?? {};
  const labels: Record<string, string> = { subdomain: "Subdomain", service: "Service", app: "App", ip: "IP" };
  return (
    <div className="space-y-2 rounded-md border bg-muted/40 p-3 text-sm">
      <div className="flex flex-wrap gap-3">
        {Object.entries(synced).map(([k, v]) => (
          <Badge key={k} variant="secondary">
            {labels[k] ?? k}: {v}
          </Badge>
        ))}
      </div>
      {result.companies && result.companies.length > 0 && (
        <p className="text-muted-foreground">{t("result.companies", { companies: result.companies.join(", ") })}</p>
      )}
      {result.warnings && result.warnings.length > 0 && (
        <ul className="list-inside list-disc text-amber-600 dark:text-amber-500">
          {result.warnings.map((wm) => (
            <li key={wm}>{wm}</li>
          ))}
        </ul>
      )}
      {result.errors && result.errors.length > 0 && (
        <ul className="list-inside list-disc text-destructive">
          {result.errors.slice(0, 20).map((em) => (
            <li key={em}>{em}</li>
          ))}
          {result.errors.length > 20 && <li>{t("result.errorsTotal", { count: result.errors.length })}</li>}
        </ul>
      )}
    </div>
  );
}

"use client";

import * as React from "react";

import {
  ArrowDownWideNarrowIcon,
  ArrowUpNarrowWideIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  DownloadIcon,
  EraserIcon,
  FilterXIcon,
  ListChecksIcon,
  Loader2Icon,
  RadioTowerIcon,
  SearchIcon,
  Trash2Icon,
  UploadIcon,
  XIcon,
} from "lucide-react";
import { toast } from "sonner";

import { HttpCodeBlock } from "@/components/http-code-block";
import { LinkTrafficDialog } from "@/components/link-traffic-dialog";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { SortableHead } from "@/components/ui/sortable-head";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { api } from "@/lib/api";
import { formatBytes, formatShortDateTime } from "@/lib/format";
import { setVisibleInterval } from "@/lib/polling";
import { useStoredSortPreference } from "@/lib/sort-preference";
import { httpStatusTone } from "@/lib/status";
import type { TrafficExchange } from "@/lib/types";
import { cn } from "@/lib/utils";
import { useActiveCompanyID } from "@/stores/company/company-store";
import { t } from "@/i18n";

import { ReplayPanel } from "./_components/replay-panel";
import { StreamMessagesPanel } from "./_components/stream-messages";
import { TamperHooksPanel } from "./_components/tamper-hooks-panel";
import { createTrafficPageState, trafficPageReducer } from "./_state/traffic-page.reducer";

function MethodBadge({ method }: { method: string }) {
  return <Badge className="shrink-0 font-mono">{method}</Badge>;
}

// Older captures may predate Host persistence because net/http keeps Host
// outside Request.Header. Fill it for display while newly recorded traffic is
// fixed at the recorder layer as well.
function requestWithHost(raw: string, exchange: TrafficExchange): string {
  if (!raw.trim() || /^host\s*:/im.test(raw)) return raw;
  let host = exchange.host;
  try {
    host = new URL(exchange.url).host || host;
  } catch {
    // Relative or legacy URLs fall back to the indexed host.
  }
  const newline = raw.includes("\r\n") ? "\r\n" : "\n";
  const firstLineEnd = raw.indexOf(newline);
  if (firstLineEnd < 0) return `${raw}${newline}Host: ${host}`;
  return `${raw.slice(0, firstLineEnd + newline.length)}Host: ${host}${newline}${raw.slice(firstLineEnd + newline.length)}`;
}

// Fixed method set (server filters exact-match); avoids deriving options from a
// single page, which would only ever list the methods on that page.
const METHODS = ["GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS"];
const PAGE_SIZES = [25, 50, 100, 200];

// Server-sortable columns. The list is sent to the backend verbatim as `sort`,
// which whitelists these same names, so keep them in sync with traffic.Page.
const SORT_FIELDS = ["ts", "status", "resp_len"] as const;
type SortField = (typeof SORT_FIELDS)[number];
const SORT_STORAGE_KEY = "traffic-sort";

// Status-class buckets for the filter dropdown; the value is sent as `status`,
// which the backend reads as either an exact code or an "Nxx" class band.
const STATUS_BUCKETS = ["2xx", "3xx", "4xx", "5xx"];

export default function TrafficPage() {
  const [state, dispatch] = React.useReducer(trafficPageReducer, undefined, createTrafficPageState);
  const { viewTab, filters, pagination, list, hostPicker, inspector, linking, deleteMode, harSummary, requests } =
    state;
  const { page, size } = pagination;
  const { traffic } = list;
  const { hosts, selected: selectedHosts } = hostPicker;
  const { selected, detail } = inspector;
  const selectedFlows = state.selection;
  const deleting = requests.delete.pending;
  const harImporting = requests.harImport.pending;
  const harExporting = requests.harExport.pending;
  const harError = requests.harImport.error;
  // The sidebar's company switcher narrows the list to that company's hosts.
  const companyId = useActiveCompanyID();

  const [sort, setSort] = useStoredSortPreference<SortField>(SORT_STORAGE_KEY, SORT_FIELDS, "ts", "desc");

  const streamKind = detail?.stream_kind === "websocket" || detail?.stream_kind === "sse" ? detail.stream_kind : null;
  const hasStream = streamKind !== null;

  // HAR 1.2 import/export state
  const harInputRef = React.useRef<HTMLInputElement | null>(null);

  // Debounce the text filters so we don't refetch on every keystroke. One shared
  // timer: the reducer applies every input at once and resets the page only if
  // an applied value actually changed.
  // biome-ignore lint/correctness/useExhaustiveDependencies: the reducer reads the latest inputs; re-arm on every keystroke.
  React.useEffect(() => {
    const t = setTimeout(() => dispatch({ type: "filtersDebounced" }), 300);
    return () => clearTimeout(t);
  }, [
    filters.input.host,
    filters.input.query,
    filters.input.body,
    filters.input.path,
    filters.input.respMin,
    filters.input.respMax,
  ]);

  const { applied } = filters;
  const hasAdvancedFilter =
    Boolean(applied.body || applied.path || applied.respMin || applied.respMax) ||
    filters.status !== "all" ||
    filters.rewritten !== "all";
  const resetAdvancedFilters = () => dispatch({ type: "advancedFiltersCleared" });

  // Filter and size changes reset the page inside the reducer; sort and company
  // live outside it, so they still reset to the first page here.
  // biome-ignore lint/correctness/useExhaustiveDependencies: these values intentionally trigger a page reset.
  React.useEffect(() => {
    dispatch({ type: "pagination/pageChanged", page: 0 });
  }, [sort, companyId]);

  // Load the current page. Auto-refresh only on page 0 (newest) so paging back
  // through history isn't yanked out from under the user.
  // biome-ignore lint/correctness/useExhaustiveDependencies: reloadTick is an explicit manual-refetch trigger.
  React.useEffect(() => {
    let alive = true;
    const load = () => {
      api
        .traffic(page, size, applied.host, filters.method, applied.query, {
          body: applied.body,
          path: applied.path,
          status: filters.status,
          rewritten: filters.rewritten,
          respMin: applied.respMin,
          respMax: applied.respMax,
          sort: sort.field,
          order: sort.direction,
          company: companyId,
        })
        .then((r) => {
          if (alive) dispatch({ type: "trafficLoaded", traffic: r });
        })
        .catch(() => {
          // Keep the last successful snapshot during transient refresh failures.
        });
      api
        .trafficHosts()
        .then((r) => {
          if (alive) dispatch({ type: "hostsLoaded", hosts: r.hosts ?? [] });
        })
        .catch(() => {
          // Keep the last successful host list during transient refresh failures.
        });
    };
    load();
    const stopPolling = setVisibleInterval(() => {
      if (page !== 0) return; // only auto-refresh the newest page
      load();
    }, 5000);
    return () => {
      alive = false;
      stopPolling();
    };
  }, [
    page,
    size,
    applied.host,
    filters.method,
    applied.query,
    applied.body,
    applied.path,
    filters.status,
    filters.rewritten,
    applied.respMin,
    applied.respMax,
    sort,
    list.reloadTick,
    companyId,
  ]);

  // Delete traffic for the current host filter (substring) or the checked
  // hosts (exact batch), then refetch.
  const allSelected = hosts.length > 0 && hosts.every((h) => selectedHosts.includes(h.host));
  const sortedHosts = React.useMemo(
    () =>
      [...hosts].sort((a, b) => {
        const countOrder = hostPicker.countSortDirection === "asc" ? a.count - b.count : b.count - a.count;
        return countOrder || a.host.localeCompare(b.host);
      }),
    [hosts, hostPicker.countSortDirection],
  );

  // "Purge" for the unfiltered purge, "Delete" for the host-scoped ones — the dialog's
  // title and its confirm button both follow from which is in play.
  const purging = deleteMode === "all";
  const deleteTitle = deleteMode
    ? {
        all: t("Purge all traffic records?"),
        selected: t("Delete all traffic for the {count} selected targets?", { count: selectedHosts.length }),
        filter: t("Delete all traffic for this target?"),
      }[deleteMode]
    : "";

  // `reclaimed` only comes back from the full purge; the host-scoped deletions
  // report the row count alone.
  const requestDelete = (mode: "filter" | "selected" | "all"): Promise<{ deleted: number; reclaimed?: number }> => {
    if (mode === "all") return api.trafficDeleteAll();
    if (mode === "selected") return api.trafficDeleteHosts(selectedHosts);
    return api.trafficDeleteHost(applied.host);
  };

  const confirmDelete = () => {
    if (!deleteMode) return;
    dispatch({ type: "request/started", key: "delete" });
    const mode = deleteMode;
    requestDelete(mode)
      .then((r) => {
        // Also clears the selection, resets to the first page and refetches.
        dispatch({ type: "deleteSucceeded", mode });
        if (mode === "all") {
          // Reclaimed space is the whole point of compacting an emptied index, so say so.
          const reclaimed = r.reclaimed ?? 0;
          toast.success(
            reclaimed > 0
              ? t("Purged {count} traffic records, freeing {size} of storage", {
                  count: r.deleted,
                  size: formatBytes(reclaimed),
                })
              : t("Purged {count} traffic records", { count: r.deleted }),
          );
        }
      })
      .catch((e) => {
        // Keep the confirmation open so the user can retry a failed deletion.
        if (mode === "all") toast.error(t("Purge failed: {error}", { error: (e as Error).message }));
      })
      .finally(() => dispatch({ type: "request/finished", key: "delete" }));
  };

  // Lazy-load the raw request/response for the selected exchange. A new
  // selection opens on Request; a WebSocket/SSE exchange then opens on Stream.
  React.useEffect(() => {
    if (!selected) {
      dispatch({ type: "detailCleared" });
      return;
    }
    let alive = true;
    dispatch({ type: "detailLoadStarted" });
    api
      .trafficExchange(selected.id)
      .then((d) => {
        if (!alive) return;
        dispatch({ type: "detailLoaded", detail: d });
      })
      .catch(() => {
        if (alive) dispatch({ type: "detailLoaded", detail: { req: t("(failed to load the messages)"), resp: "" } });
      })
      .finally(() => {
        if (alive) dispatch({ type: "detailLoadFinished" });
      });
    return () => {
      alive = false;
    };
  }, [selected]);

  // Toggle direction when re-clicking the active column, else sort the new column
  // newest/largest-first.
  const toggleSort = (field: SortField) =>
    setSort((prev) =>
      prev.field === field
        ? { field, direction: prev.direction === "asc" ? "desc" : "asc" }
        : { field, direction: "desc" },
    );

  const handleHarFileChange = async (event: React.ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    dispatch({ type: "harImportCleared" });
    if (file.size > 32 * 1024 * 1024) {
      const msg = t("HAR file exceeds the 32 MiB limit");
      dispatch({ type: "request/failed", key: "harImport", error: msg });
      toast.error(msg);
      return;
    }
    dispatch({ type: "request/started", key: "harImport" });
    try {
      const raw = await file.text();
      const res = await api.trafficImportHAR(raw);
      // Also resets to the first page and refetches.
      dispatch({ type: "harImported", summary: res });
      const summary = t("HAR import complete: {imported} imported, {skipped} skipped, {failed} failed", {
        imported: res.imported,
        skipped: res.skipped,
        failed: res.failed,
      });
      if (res.failed > 0 || res.truncated) {
        toast.warning(summary);
      } else {
        toast.success(summary);
      }
    } catch (e) {
      const msg = t("HAR import failed: {error}", { error: (e as Error).message });
      dispatch({ type: "request/failed", key: "harImport", error: msg });
      toast.error(msg);
    } finally {
      dispatch({ type: "request/finished", key: "harImport" });
    }
  };

  const handleHarExport = async () => {
    dispatch({ type: "request/started", key: "harExport" });
    try {
      const meta = await api.trafficExportHAR({
        host: applied.host,
        method: filters.method,
        q: applied.query,
        body: applied.body,
        path: applied.path,
        status: filters.status,
        rewritten: filters.rewritten,
        respMin: applied.respMin,
        respMax: applied.respMax,
      });
      if (meta.truncated) {
        toast.warning(
          t("Exported {exported} of {matched} matching exchanges (truncated: {reason})", {
            exported: meta.exported,
            matched: meta.matched,
            reason: meta.truncatedReason ?? "limit",
          }),
        );
      } else {
        toast.success(t("Exported {count} exchanges to HAR", { count: meta.exported }));
      }
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      dispatch({ type: "request/finished", key: "harExport" });
    }
  };

  const exchanges = React.useMemo(() => traffic?.exchanges ?? [], [traffic]);
  const total = traffic?.total ?? exchanges.length;
  const pageCount = Math.max(1, Math.ceil(total / size));
  const rangeStart = total === 0 ? 0 : page * size + 1;
  const rangeEnd = page * size + exchanges.length;

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">{t("Traffic")}</h1>
          <p className="text-muted-foreground text-sm">{t("Global recording proxy · all HTTP traffic")}</p>
        </div>
        <div className="flex items-center gap-4 text-sm">
          <span
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs font-medium",
              traffic?.enabled
                ? "border-emerald-500/20 bg-emerald-500/15 text-emerald-600 dark:text-emerald-400"
                : "border-transparent bg-muted text-muted-foreground",
            )}
          >
            <RadioTowerIcon className="size-3.5" />
            {traffic?.enabled ? t("Recording") : t("Disabled")}
          </span>
          {traffic?.proxy && <span className="font-mono text-xs text-muted-foreground">{traffic.proxy}</span>}
          <span className="text-xs text-muted-foreground tabular-nums">
            {t("{count} records", { count: traffic?.count ?? 0 })}
          </span>
          <input
            ref={harInputRef}
            type="file"
            accept=".har,application/json"
            className="hidden"
            onChange={(e) => void handleHarFileChange(e)}
          />
          <Button
            variant="outline"
            size="sm"
            className="h-8"
            disabled={harImporting}
            onClick={() => harInputRef.current?.click()}
          >
            {harImporting ? <Loader2Icon className="size-3.5 animate-spin" /> : <UploadIcon className="size-3.5" />}
            {harImporting ? t("Importing HAR…") : t("Import HAR")}
          </Button>
          <Button
            variant="outline"
            size="sm"
            className="h-8"
            disabled={harExporting || !traffic?.count}
            onClick={() => void handleHarExport()}
          >
            {harExporting ? <Loader2Icon className="size-3.5 animate-spin" /> : <DownloadIcon className="size-3.5" />}
            {harExporting ? t("Exporting HAR…") : t("Export HAR")}
          </Button>
        </div>
      </div>

      <Tabs
        value={viewTab}
        onValueChange={(v) => dispatch({ type: "fieldsChanged", patch: { viewTab: v as "exchanges" | "hooks" } })}
        className="flex min-h-0 flex-1 flex-col gap-4"
      >
        <TabsList className="w-fit">
          <TabsTrigger value="exchanges">{t("Exchanges")}</TabsTrigger>
          <TabsTrigger value="hooks">{t("Hooks")}</TabsTrigger>
        </TabsList>

        <TabsContent value="hooks" className="mt-0 flex min-h-0 flex-1 flex-col gap-4">
          <TamperHooksPanel recentExchanges={exchanges} />
        </TabsContent>

        <TabsContent value="exchanges" className="mt-0 flex min-h-0 flex-1 flex-col gap-4">

      {harError ? (
        <Alert variant="destructive" className="flex items-center justify-between gap-2 py-2">
          <AlertDescription className="text-xs">{harError}</AlertDescription>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={t("Dismiss")}
            onClick={() => dispatch({ type: "request/errorCleared", key: "harImport" })}
          >
            <XIcon className="size-3.5" />
          </Button>
        </Alert>
      ) : null}

      {harSummary ? (
        <Alert className="flex items-start justify-between gap-2 py-2">
          <AlertDescription className="space-y-1 text-xs">
            <p>
              {t("HAR import complete: {imported} imported, {skipped} skipped, {failed} failed", {
                imported: harSummary.imported,
                skipped: harSummary.skipped,
                failed: harSummary.failed,
              })}
            </p>
            {harSummary.truncated ? (
              <p className="text-amber-600 dark:text-amber-400">
                {t("HAR import stopped early at the 32 MiB decoded body cap.")}
              </p>
            ) : null}
            {harSummary.errors && harSummary.errors.length > 0 ? (
              <ul className="list-disc pl-4 text-muted-foreground">
                {harSummary.errors.slice(0, 5).map((item) => (
                  <li key={`${item.index}-${item.reason}`} className="font-mono">
                    #{item.index}: {item.reason}
                  </li>
                ))}
              </ul>
            ) : null}
          </AlertDescription>
          <Button
            variant="ghost"
            size="icon-xs"
            aria-label={t("Dismiss")}
            onClick={() => dispatch({ type: "harSummaryDismissed" })}
          >
            <XIcon className="size-3.5" />
          </Button>
        </Alert>
      ) : null}

      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-2">
        <Popover
          open={hostPicker.open}
          onOpenChange={(open) => dispatch({ type: "hostPickerChanged", patch: { open } })}
        >
          <PopoverTrigger asChild>
            <Button variant="outline" size="sm" className="h-8">
              <ListChecksIcon className="size-3.5" />
              {selectedHosts.length > 0
                ? t("Select targets ({count})", { count: selectedHosts.length })
                : t("Select targets…")}
            </Button>
          </PopoverTrigger>
          <PopoverContent
            className="w-[calc(100vw-2rem)] p-0 data-open:animate-none data-closed:animate-none sm:w-80"
            align="start"
            collisionPadding={16}
          >
            <div className="flex items-center justify-between border-b px-3 py-2">
              <span className="text-xs font-medium text-muted-foreground">{t("Batch delete by target")}</span>
              <div className="flex items-center gap-1">
                {hosts.length > 0 && (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        onClick={() => dispatch({ type: "hostCountSortToggled" })}
                        aria-label={
                          hostPicker.countSortDirection === "desc"
                            ? t("Packet count is currently descending; click to switch to ascending")
                            : t("Packet count is currently ascending; click to switch to descending")
                        }
                      >
                        {hostPicker.countSortDirection === "desc" ? (
                          <ArrowDownWideNarrowIcon />
                        ) : (
                          <ArrowUpNarrowWideIcon />
                        )}
                      </Button>
                    </TooltipTrigger>
                    <TooltipContent>
                      {hostPicker.countSortDirection === "desc"
                        ? t("By packet count, descending")
                        : t("By packet count, ascending")}
                    </TooltipContent>
                  </Tooltip>
                )}
                {hosts.length > 0 && (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-6 px-2 text-xs"
                    onClick={() =>
                      dispatch({
                        type: "hostPickerChanged",
                        patch: { selected: allSelected ? [] : hosts.map((h) => h.host) },
                      })
                    }
                  >
                    {allSelected ? t("Clear all") : t("Select all")}
                  </Button>
                )}
              </div>
            </div>
            <div className="max-h-64 overflow-y-auto">
              {hosts.length === 0 ? (
                <div className="px-3 py-6 text-center text-xs text-muted-foreground">{t("No traffic records")}</div>
              ) : (
                sortedHosts.map((h, index) => (
                  <label
                    key={h.host}
                    htmlFor={`traffic-host-${index}`}
                    className="flex cursor-pointer items-center gap-2 px-3 py-1.5 text-xs hover:bg-accent"
                  >
                    <Checkbox
                      id={`traffic-host-${index}`}
                      checked={selectedHosts.includes(h.host)}
                      onCheckedChange={() => dispatch({ type: "hostToggled", host: h.host })}
                    />
                    <span className="truncate font-mono">{h.host}</span>
                    <span className="ml-auto shrink-0 tabular-nums text-muted-foreground">{h.count}</span>
                  </label>
                ))
              )}
            </div>
            <div className="border-t p-2">
              <Button
                variant="destructive"
                size="sm"
                className="w-full"
                disabled={selectedHosts.length === 0}
                onClick={() => {
                  dispatch({ type: "deleteRequested", mode: "selected" });
                  dispatch({ type: "hostPickerChanged", patch: { open: false } });
                }}
              >
                {t("Delete selected ({count})", { count: selectedHosts.length })}
              </Button>
            </div>
          </PopoverContent>
        </Popover>
        <div className="relative w-48">
          <Input
            placeholder="host…"
            value={filters.input.host}
            onChange={(e) => dispatch({ type: "filterInputChanged", field: "host", value: e.target.value })}
            className="h-8"
          />
        </div>
        <Button
          variant="destructive"
          size="sm"
          className="h-8"
          disabled={!applied.host || deleting}
          title={applied.host ? undefined : t("Select a target on the left or enter a host first")}
          onClick={() => dispatch({ type: "deleteRequested", mode: "filter" })}
        >
          <Trash2Icon className="size-3.5" />
          {t("Delete this target")}
        </Button>
        {/* Outline rather than a second destructive button: this one ignores every
            filter, so it must not look one mis-click away from "Delete this target". */}
        <Button
          variant="outline"
          size="sm"
          className="h-8 text-destructive hover:bg-destructive/10 hover:text-destructive"
          disabled={!traffic?.count || deleting}
          title={traffic?.count ? t("Delete all traffic and compact storage") : t("No traffic records yet")}
          onClick={() => dispatch({ type: "deleteRequested", mode: "all" })}
        >
          <EraserIcon className="size-3.5" />
          {t("Purge all")}
        </Button>
        <div className="relative max-w-sm flex-1">
          <SearchIcon className="absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder={t("Search everything (URL / method / type / status…)")}
            value={filters.input.query}
            onChange={(e) => dispatch({ type: "filterInputChanged", field: "query", value: e.target.value })}
            className="h-8 pl-8"
          />
        </div>
        <Select value={filters.method} onValueChange={(method) => dispatch({ type: "methodChanged", method })}>
          <SelectTrigger size="sm" className="w-32">
            <SelectValue placeholder={t("Method")} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t("All methods")}</SelectItem>
            {METHODS.map((m) => (
              <SelectItem key={m} value={m}>
                {m}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={String(size)}
          onValueChange={(v) => dispatch({ type: "pagination/sizeChanged", size: Number(v) })}
        >
          <SelectTrigger size="sm" className="w-28">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {PAGE_SIZES.map((n) => (
              <SelectItem key={n} value={String(n)}>
                {t("{count} / page", { count: n })}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>

        <div className="ml-auto flex items-center gap-2 text-xs text-muted-foreground">
          <span className="tabular-nums">
            {rangeStart}–{rangeEnd} / {total}
          </span>
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            disabled={page <= 0}
            onClick={() => dispatch({ type: "previousPageRequested" })}
          >
            <ChevronLeftIcon />
          </Button>
          <span className="tabular-nums">
            {page + 1} / {pageCount}
          </span>
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            disabled={page + 1 >= pageCount}
            onClick={() => dispatch({ type: "nextPageRequested", pageCount })}
          >
            <ChevronRightIcon />
          </Button>
        </div>
      </div>

      {/* Advanced filters (issue #177): narrow 660k+ exchanges down to the one packet. */}
      <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/30 px-2 py-1.5">
        <span className="pl-1 text-xs font-medium text-muted-foreground">{t("Advanced filters")}</span>
        <div className="relative w-56">
          <Input
            placeholder={t("Response content (body keyword, ≥3 chars)")}
            value={filters.input.body}
            onChange={(e) => dispatch({ type: "filterInputChanged", field: "body", value: e.target.value })}
            className="h-8"
          />
        </div>
        <div className="relative w-52">
          <Input
            placeholder={t("Path (e.g. /api/user/…)")}
            value={filters.input.path}
            onChange={(e) => dispatch({ type: "filterInputChanged", field: "path", value: e.target.value })}
            className="h-8"
          />
        </div>
        <Select value={filters.status} onValueChange={(status) => dispatch({ type: "statusFilterChanged", status })}>
          <SelectTrigger size="sm" className="w-28">
            <SelectValue placeholder={t("Status")} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t("All statuses")}</SelectItem>
            {STATUS_BUCKETS.map((s) => (
              <SelectItem key={s} value={s}>
                {s}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Select
          value={filters.rewritten}
          onValueChange={(rewritten) => dispatch({ type: "rewrittenFilterChanged", rewritten })}
        >
          <SelectTrigger size="sm" className="w-32">
            <SelectValue placeholder={t("Rewritten")} />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="all">{t("All traffic")}</SelectItem>
            <SelectItem value="rewritten">{t("Rewritten")}</SelectItem>
            <SelectItem value="original">{t("Original")}</SelectItem>
          </SelectContent>
        </Select>
        <div className="flex items-center gap-1 text-xs text-muted-foreground">
          <span>{t("Response size")}</span>
          <Input
            type="number"
            min={0}
            placeholder={t("Min (B)")}
            value={filters.input.respMin}
            onChange={(e) => dispatch({ type: "filterInputChanged", field: "respMin", value: e.target.value })}
            className="h-8 w-24"
          />
          <span>–</span>
          <Input
            type="number"
            min={0}
            placeholder={t("Max (B)")}
            value={filters.input.respMax}
            onChange={(e) => dispatch({ type: "filterInputChanged", field: "respMax", value: e.target.value })}
            className="h-8 w-24"
          />
        </div>
        {hasAdvancedFilter ? (
          <Button variant="ghost" size="sm" className="h-8" onClick={resetAdvancedFilters}>
            <FilterXIcon className="size-3.5" />
            {t("Clear filters")}
          </Button>
        ) : null}
      </div>

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-sm text-muted-foreground">
          {t("{count} flows selected", { count: selectedFlows.size })}
        </span>
        <Button
          variant="outline"
          size="sm"
          disabled={selectedFlows.size === 0}
          onClick={() => dispatch({ type: "fieldsChanged", patch: { linking: true } })}
        >
          {t("Link to finding")}
        </Button>
        {selectedFlows.size > 0 ? (
          <Button variant="ghost" size="sm" onClick={() => dispatch({ type: "selection/cleared" })}>
            {t("Clear selection")}
          </Button>
        ) : null}
      </div>
      {/* History table */}
      <div className="flex h-[calc(100vh-15rem)] min-h-0 flex-col">
        <Card className="flex min-h-0 flex-1 flex-col overflow-hidden py-0">
          <div className="min-h-0 flex-1 overflow-auto">
            <Table>
              <TableHeader className="sticky top-0 z-10 bg-card">
                <TableRow>
                  <TableHead className="w-10">
                    <Checkbox
                      aria-label={t("Select this page's traffic")}
                      checked={exchanges.length > 0 && exchanges.every((e) => selectedFlows.has(e.id))}
                      onCheckedChange={(checked) =>
                        dispatch({
                          type: "flowsSelectionChanged",
                          ids: exchanges.map((e) => e.id),
                          checked: checked === true,
                        })
                      }
                    />
                  </TableHead>
                  <SortableHead
                    field="ts"
                    label={t("Time")}
                    activeField={sort.field}
                    direction={sort.direction}
                    onSort={toggleSort}
                    className="w-36"
                  />
                  <TableHead className="w-44">host</TableHead>
                  <TableHead className="w-20">{t("Method")}</TableHead>
                  <TableHead>URL</TableHead>
                  <SortableHead
                    field="status"
                    label={t("Status")}
                    activeField={sort.field}
                    direction={sort.direction}
                    onSort={toggleSort}
                    className="w-20"
                  />
                  <TableHead className="w-36">content-type</TableHead>
                  <SortableHead
                    field="resp_len"
                    label={t("Response size")}
                    activeField={sort.field}
                    direction={sort.direction}
                    onSort={toggleSort}
                    align="right"
                    className="w-24 text-right"
                  />
                </TableRow>
              </TableHeader>
              <TableBody>
                {exchanges.map((e) => (
                  <TableRow
                    key={e.id}
                    className={cn("cursor-pointer", selected?.id === e.id && "bg-accent hover:bg-accent")}
                    onClick={() => dispatch({ type: "inspectorChanged", patch: { selected: e } })}
                  >
                    <TableCell>
                      <Checkbox
                        aria-label={t("Select traffic {id}", { id: e.id })}
                        checked={selectedFlows.has(e.id)}
                        onClick={(event) => event.stopPropagation()}
                        onCheckedChange={(checked) =>
                          dispatch({ type: "flowsSelectionChanged", ids: [e.id], checked: checked === true })
                        }
                      />
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground tabular-nums">
                      {formatShortDateTime(e.ts)}
                    </TableCell>
                    <TableCell className="font-mono text-xs">{e.host}</TableCell>
                    <TableCell>
                      <MethodBadge method={e.method} />
                    </TableCell>
                    <TableCell className="max-w-0">
                      <div className="flex items-center gap-1.5">
                        <span className="min-w-0 truncate font-mono text-xs">{e.url}</span>
                        {e.rewritten ? (
                          <Badge
                            variant="outline"
                            className="shrink-0 border-amber-500/30 bg-amber-500/10 px-1.5 py-0 text-[10px] font-medium text-amber-600 dark:text-amber-400"
                          >
                            {t("Rewritten")}
                          </Badge>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell>
                      <span
                        className={cn("font-mono text-xs font-semibold tabular-nums", httpStatusTone(e.status))}
                      >
                        {e.status}
                      </span>
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground">{e.content_type}</TableCell>
                    <TableCell className="text-right text-xs tabular-nums">{formatBytes(e.resp_len)}</TableCell>
                  </TableRow>
                ))}
                {exchanges.length === 0 && (
                  <TableRow>
                    <TableCell colSpan={8} className="py-12 text-center text-sm text-muted-foreground">
                      {traffic === null ? t("Loading…") : t("No matching traffic.")}
                    </TableCell>
                  </TableRow>
                )}
              </TableBody>
            </Table>
          </div>
        </Card>
      </div>

      {linking ? (
        <LinkTrafficDialog
          trafficIds={[...selectedFlows]}
          onClose={() => dispatch({ type: "fieldsChanged", patch: { linking: false } })}
          onBound={() => dispatch({ type: "selection/cleared" })}
        />
      ) : null}
      <Sheet
        open={selected !== null}
        onOpenChange={(open) => !open && dispatch({ type: "inspectorChanged", patch: { selected: null } })}
      >
        <SheetContent className="w-full! max-w-none! gap-0 p-0 sm:w-[48rem]! sm:max-w-[48rem]!">
          {selected && (
            <>
              <SheetHeader className="border-b px-5 py-4">
                <div className="flex items-center gap-2 pr-8">
                  <MethodBadge method={selected.method} />
                  <Badge
                    variant="secondary"
                    className={cn("font-mono tabular-nums", httpStatusTone(selected.status))}
                  >
                    {selected.status}
                  </Badge>
                  {selected.rewritten ? (
                    <Badge
                      variant="outline"
                      className="border-amber-500/30 bg-amber-500/10 text-amber-600 dark:text-amber-400"
                    >
                      {t("Rewritten")}
                    </Badge>
                  ) : null}
                  <span className="ml-auto text-xs text-muted-foreground tabular-nums">
                    {formatShortDateTime(selected.ts)}
                  </span>
                </div>
                <SheetTitle className="break-all font-mono">{selected.host}</SheetTitle>
                <SheetDescription className="break-all font-mono">{selected.url}</SheetDescription>
              </SheetHeader>
              <Tabs
                value={inspector.tab}
                onValueChange={(tab) => dispatch({ type: "inspectorChanged", patch: { tab } })}
                className="min-h-0 flex-1 gap-0"
              >
                <TabsList className={cn("mx-5 mt-4 grid w-auto", hasStream ? "grid-cols-4" : "grid-cols-3")}>
                  <TabsTrigger value="request">{t("Request")}</TabsTrigger>
                  <TabsTrigger value="response">{t("Response")}</TabsTrigger>
                  {hasStream && (
                    <TabsTrigger value="stream">
                      {t("Stream")}
                      {(detail?.stream_count ?? 0) > 0 && (
                        <span className="text-muted-foreground text-xs tabular-nums">{detail?.stream_count}</span>
                      )}
                    </TabsTrigger>
                  )}
                  {hasStream ? (
                    <span title={t("stream replay is not supported")} className="flex">
                      <TabsTrigger value="replay" disabled>
                        {t("Replay")}
                      </TabsTrigger>
                    </span>
                  ) : (
                    <TabsTrigger value="replay">{t("Replay")}</TabsTrigger>
                  )}
                </TabsList>
                <TabsContent value="request" className="min-h-0 overflow-auto">
                  {inspector.loading ? (
                    <div className="flex items-center gap-2 p-5 text-xs text-muted-foreground">
                      <Loader2Icon className="size-3.5 animate-spin" />
                      {t("Loading messages…")}
                    </div>
                  ) : (
                    <HttpCodeBlock raw={requestWithHost(detail?.req ?? "", selected)} />
                  )}
                </TabsContent>
                <TabsContent value="response" className="min-h-0 overflow-auto">
                  {inspector.loading ? (
                    <div className="flex items-center gap-2 p-5 text-xs text-muted-foreground">
                      <Loader2Icon className="size-3.5 animate-spin" />
                      {t("Loading messages…")}
                    </div>
                  ) : (
                    <HttpCodeBlock raw={detail?.resp ?? ""} />
                  )}
                </TabsContent>
                {streamKind && (
                  <TabsContent value="stream" className="min-h-0 overflow-auto">
                    <StreamMessagesPanel exchangeId={selected.id} kind={streamKind} />
                  </TabsContent>
                )}
                {!hasStream && (
                  // Kept mounted so edits survive a look at another tab.
                  <TabsContent value="replay" forceMount className="min-h-0 overflow-auto data-[state=inactive]:hidden">
                    {inspector.loading || !detail ? (
                      <div className="flex items-center gap-2 p-5 text-muted-foreground text-xs">
                        <Loader2Icon className="size-3.5 animate-spin" />
                        {t("Loading…")}
                      </div>
                    ) : (
                      <ReplayPanel
                        exchange={selected}
                        raw={requestWithHost(detail.req, selected)}
                        methods={METHODS}
                        onRecorded={() => dispatch({ type: "reloadRequested" })}
                      />
                    )}
                  </TabsContent>
                )}
              </Tabs>
            </>
          )}
        </SheetContent>
      </Sheet>

      <AlertDialog
        open={deleteMode !== null}
        onOpenChange={(o) => {
          if (!o) dispatch({ type: "deleteDialogClosed" });
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{deleteTitle}</AlertDialogTitle>
            <AlertDialogDescription>
              {deleteMode === "all" && (
                <>
                  {t(
                    "This will permanently delete all {count} traffic records (including raw requests/responses), ignoring the current filters. This action cannot be undone. Traffic evidence already bound to findings lives in a separate evidence store and is unaffected.",
                    { count: traffic?.count ?? 0 },
                  )}
                  <br />
                  <span className="text-muted-foreground">
                    {t(
                      "Purging also compacts storage, returning the index's disk space to the system; traffic recording pauses briefly during this time.",
                    )}
                  </span>
                </>
              )}
              {deleteMode === "selected" && (
                <>
                  {t(
                    "This will permanently delete all traffic records for {count} targets ({hosts}), including raw requests/responses. This action cannot be undone.",
                    {
                      count: selectedHosts.length,
                      hosts: `${selectedHosts.slice(0, 3).join(", ")}${selectedHosts.length > 3 ? "…" : ""}`,
                    },
                  )}
                </>
              )}
              {deleteMode === "filter" && (
                <>
                  {t(
                    "This will permanently delete all traffic records for hosts containing {host} (including raw requests/responses). This action cannot be undone.",
                    { host: applied.host },
                  )}
                </>
              )}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={deleting}>{t("Cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                confirmDelete();
              }}
              disabled={deleting}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {deleting
                ? purging
                  ? t("Purging…")
                  : t("Deleting…")
                : purging
                  ? t("Confirm purge")
                  : t("Confirm delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
        </TabsContent>
      </Tabs>
    </div>
  );
}

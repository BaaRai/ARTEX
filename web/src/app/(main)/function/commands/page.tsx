"use client";

import * as React from "react";

import { BarChart3Icon, ChevronLeftIcon, ChevronRightIcon, Loader2Icon, SearchIcon, TerminalIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import { formatShortDateTime } from "@/lib/format";
import { cn } from "@/lib/utils";
import { t } from "@/i18n";

import { commandsPageReducer, initialCommandsPageState } from "./_state/commands-page.reducer";

// toolInput renders a tool's raw input for display. Bash's {"command":"..."} is
// unwrapped to the bare command; other tools show their pretty-printed JSON args.
function toolInput(raw: string): string {
  try {
    const obj = JSON.parse(raw);
    if (obj && typeof obj.command === "string") return obj.command;
    return JSON.stringify(obj, null, 2);
  } catch {
    /* not JSON */
  }
  return raw;
}

// truncate clips a string to maxLen characters.
function truncate(s: string, maxLen: number): string {
  const first = s.split("\n")[0]; // single-line preview
  if (first.length <= maxLen) return first;
  return first.slice(0, maxLen) + "…";
}

const PAGE_SIZES = [25, 50, 100];
const CMD_MAX_LEN = 80;

export default function CommandsPage() {
  const [state, dispatch] = React.useReducer(commandsPageReducer, initialCommandsPageState);
  const { filters, pagination, list, stats, selected } = state;
  const { page, size } = pagination;

  // Debounce search input.
  // biome-ignore lint/correctness/useExhaustiveDependencies: the reducer reads the latest query; re-arm on every keystroke.
  React.useEffect(() => {
    const t = setTimeout(() => dispatch({ type: "queryDebounced" }), 300);
    return () => clearTimeout(t);
  }, [filters.query]);

  // Load data. Filter changes reset the page inside the reducer, so this runs once per change.
  React.useEffect(() => {
    let alive = true;
    dispatch({ type: "list/loadStarted" });
    api
      .commands({ task: filters.task || undefined, q: filters.debouncedQuery || undefined, page, size })
      .then((r) => {
        if (!alive) return;
        dispatch({ type: "list/loaded", items: r.commands ?? [], total: r.total ?? 0 });
      })
      .catch(() => {
        if (!alive) return;
        dispatch({ type: "list/loaded", items: [], total: 0 });
      })
      .finally(() => alive && dispatch({ type: "list/loadFinished" }));
    return () => {
      alive = false;
    };
  }, [page, size, filters.debouncedQuery, filters.task]);

  // Stats follow the filters and describe the same set of records as the table (without pagination).
  React.useEffect(() => {
    if (!stats.open) return;
    let alive = true;
    dispatch({ type: "statsLoadStarted" });
    api
      .commandStats({ task: filters.task || undefined, q: filters.debouncedQuery || undefined })
      .then((r) => alive && dispatch({ type: "statsLoaded", items: r.stats ?? [] }))
      .catch(() => alive && dispatch({ type: "statsLoaded", items: [] }))
      .finally(() => alive && dispatch({ type: "statsLoadFinished" }));
    return () => {
      alive = false;
    };
  }, [stats.open, filters.debouncedQuery, filters.task]);

  const { items: commands, total, loading } = list;
  const statsTotal = stats.items.reduce((n, s) => n + s.total, 0);
  const statsErrors = stats.items.reduce((n, s) => n + s.errors, 0);
  const statsMax = stats.items.reduce((n, s) => Math.max(n, s.total), 0);
  const totalPages = Math.max(1, Math.ceil(total / size));
  const rangeStart = total === 0 ? 0 : page * size + 1;
  const rangeEnd = page * size + commands.length;

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      {/* Header */}
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-2">
          <TerminalIcon className="h-5 w-5 text-muted-foreground" />
          <h1 className="text-xl font-semibold tracking-tight">{t("Tool executions")}</h1>
          <Badge variant="secondary">{total}</Badge>
        </div>
      </div>

      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative max-w-sm flex-1">
          <SearchIcon className="absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder={t("Search tools / arguments...")}
            value={filters.query}
            onChange={(e) => dispatch({ type: "queryChanged", query: e.target.value })}
            className="h-8 pl-8"
          />
        </div>
        <Input
          placeholder={t("Task ID")}
          className="h-8 w-28"
          value={filters.task}
          onChange={(e) => dispatch({ type: "taskFilterChanged", task: e.target.value })}
        />
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

        <Button
          variant="outline"
          size="sm"
          className="h-8"
          onClick={() => dispatch({ type: "statsOpenChanged", open: true })}
        >
          <BarChart3Icon className="size-4" />
          {t("Stats")}
        </Button>

        <div className="ml-auto flex items-center gap-2 text-xs text-muted-foreground">
          <span className="tabular-nums">
            {rangeStart}–{rangeEnd} / {total}
          </span>
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            disabled={page <= 0}
            onClick={() => dispatch({ type: "pagination/pageChanged", page: page - 1 })}
          >
            <ChevronLeftIcon />
          </Button>
          <span className="tabular-nums">
            {page + 1} / {totalPages}
          </span>
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            disabled={page + 1 >= totalPages}
            onClick={() => dispatch({ type: "pagination/pageChanged", page: Math.min(totalPages - 1, page + 1) })}
          >
            <ChevronRightIcon />
          </Button>
        </div>
      </div>

      {/* History table */}
      <div className="flex h-[calc(100vh-13rem)] min-h-0 flex-col">
        <Card className="flex min-h-0 flex-1 flex-col overflow-hidden py-0">
          <div className="min-h-0 flex-1 overflow-auto">
            <Table>
              <TableHeader className="sticky top-0 z-10 bg-card">
                <TableRow>
                  <TableHead className="w-[130px]">{t("Time")}</TableHead>
                  <TableHead className="w-[60px]">{t("Task")}</TableHead>
                  <TableHead className="w-[90px]">{t("Worker")}</TableHead>
                  <TableHead className="w-[110px]">{t("Tool")}</TableHead>
                  <TableHead>{t("Input")}</TableHead>
                  <TableHead className="w-[60px]">{t("Status")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {loading && commands.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={6} className="py-12 text-center">
                      <Loader2Icon className="mx-auto h-5 w-5 animate-spin text-muted-foreground" />
                    </TableCell>
                  </TableRow>
                ) : commands.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={6} className="py-12 text-center text-sm text-muted-foreground">
                      {t("No tool execution records yet")}
                    </TableCell>
                  </TableRow>
                ) : (
                  commands.map((cmd) => (
                    <TableRow
                      key={cmd.id}
                      className={cn("cursor-pointer", selected?.id === cmd.id && "bg-accent hover:bg-accent")}
                      onClick={() => dispatch({ type: "selected", command: cmd })}
                    >
                      <TableCell className="text-xs text-muted-foreground tabular-nums">
                        {formatShortDateTime(cmd.created_at)}
                      </TableCell>
                      <TableCell className="text-xs font-mono text-muted-foreground">#{cmd.exploration_id}</TableCell>
                      <TableCell>
                        <Badge variant="outline" className="text-xs font-mono">
                          {cmd.worker || "-"}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <Badge variant="secondary" className="text-xs font-mono">
                          {cmd.tool || "-"}
                        </Badge>
                      </TableCell>
                      <TableCell className="max-w-0">
                        <code className="block truncate font-mono text-xs">
                          {truncate(toolInput(cmd.command), CMD_MAX_LEN)}
                        </code>
                      </TableCell>
                      <TableCell>
                        {cmd.is_error ? (
                          <Badge variant="destructive" className="text-xs">
                            {t("Failed")}
                          </Badge>
                        ) : (
                          <Badge variant="secondary" className="text-xs text-emerald-600">
                            {t("Success")}
                          </Badge>
                        )}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        </Card>
      </div>

      {/* Tool call stats: the same set of records as the table (same filters, no pagination) */}
      <Dialog open={stats.open} onOpenChange={(open) => dispatch({ type: "statsOpenChanged", open })}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{t("Tool call stats")}</DialogTitle>
            <DialogDescription>
              {filters.task || filters.debouncedQuery
                ? t("All records under the current filters")
                : t("All tool execution records")}
              {stats.items.length > 0 && (
                <span className="tabular-nums">
                  {" · "}
                  {t("{tools} tools · {calls} calls", { tools: stats.items.length, calls: statsTotal })}
                  {statsErrors > 0 && (
                    <>
                      {" · "}
                      <span className="text-red-600 dark:text-red-400">{t("Failed {count}", { count: statsErrors })}</span>
                    </>
                  )}
                </span>
              )}
            </DialogDescription>
          </DialogHeader>

          {stats.loading && stats.items.length === 0 ? (
            <div className="py-10 text-center">
              <Loader2Icon className="mx-auto h-5 w-5 animate-spin text-muted-foreground" />
            </div>
          ) : stats.items.length === 0 ? (
            <div className="py-10 text-center text-sm text-muted-foreground">{t("No stats data")}</div>
          ) : (
            <div className="-mr-2 max-h-[55vh] space-y-1 overflow-auto pr-2">
              {stats.items.map((s) => (
                <div key={s.tool} className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 rounded-md p-2">
                  <div className="min-w-0">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-mono text-xs font-medium">{s.tool}</span>
                      {s.errors > 0 && (
                        <span className="text-[11px] tabular-nums text-red-600 dark:text-red-400">
                          {t("Failed {count}", { count: s.errors })}
                        </span>
                      )}
                    </div>
                    <div className="mt-1.5 h-1.5 overflow-hidden rounded-full bg-muted">
                      <div
                        className="h-full rounded-full bg-primary"
                        style={{ width: `${statsMax > 0 ? (s.total / statsMax) * 100 : 0}%` }}
                      />
                    </div>
                  </div>
                  <div className="text-right">
                    <div className="text-xs font-semibold tabular-nums">{s.total}</div>
                    <div className="text-[11px] tabular-nums text-muted-foreground">
                      {statsTotal > 0 ? ((s.total / statsTotal) * 100).toFixed(1) : "0.0"}%
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </DialogContent>
      </Dialog>

      <Sheet open={selected !== null} onOpenChange={(open) => !open && dispatch({ type: "selected", command: null })}>
        <SheetContent className="w-full! max-w-none! gap-0 p-0 sm:w-[46rem]! sm:max-w-[46rem]!">
          {selected && (
            <>
              <SheetHeader className="border-b px-5 py-4">
                <SheetTitle className="pr-8">{t("Tool execution details")}</SheetTitle>
                <SheetDescription>{formatShortDateTime(selected.created_at)}</SheetDescription>
                <div className="flex flex-wrap items-center gap-2 pt-2">
                  <Badge variant="outline" className="text-xs font-mono">
                    #{selected.exploration_id}
                  </Badge>
                  <Badge variant="outline" className="text-xs font-mono">
                    {selected.worker || "-"}
                  </Badge>
                  <Badge variant="secondary" className="text-xs font-mono">
                    {selected.tool || "-"}
                  </Badge>
                  {selected.is_error ? (
                    <Badge variant="destructive" className="text-xs">
                      {t("Failed")}
                    </Badge>
                  ) : (
                    <Badge variant="secondary" className="text-xs text-emerald-600">
                      {t("Success")}
                    </Badge>
                  )}
                </div>
              </SheetHeader>
              <div className="grid min-h-0 flex-1 grid-rows-2 divide-y">
                <div className="flex min-h-0 min-w-0 flex-col">
                  <div className="border-b px-5 py-2 text-xs font-medium text-muted-foreground">{t("Input")}</div>
                  <div className="min-h-0 flex-1 overflow-auto">
                    <pre className="p-5 font-mono text-xs break-all whitespace-pre-wrap">
                      {toolInput(selected.command)}
                    </pre>
                  </div>
                </div>
                <div className="flex min-h-0 min-w-0 flex-col">
                  <div className="border-b px-5 py-2 text-xs font-medium text-muted-foreground">{t("Output")}</div>
                  <div className="min-h-0 flex-1 overflow-auto">
                    <pre
                      className={cn(
                        "p-5 font-mono text-xs break-all whitespace-pre-wrap",
                        selected.is_error && "text-red-600 dark:text-red-400",
                      )}
                    >
                      {selected.output || t("(empty)")}
                    </pre>
                  </div>
                </div>
              </div>
            </>
          )}
        </SheetContent>
      </Sheet>
    </div>
  );
}

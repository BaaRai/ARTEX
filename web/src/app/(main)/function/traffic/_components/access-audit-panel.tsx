"use client";

import * as React from "react";

import {
  ChevronLeftIcon,
  ChevronRightIcon,
  InfoIcon,
  Loader2Icon,
  SearchIcon,
  SlidersHorizontalIcon,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { t } from "@/i18n";
import type { AccessAuditEvent, AccessAuditPage } from "@/lib/access-audit";
import { api } from "@/lib/api";
import { cn } from "@/lib/utils";

const SOURCES = [
  "native_http",
  "webfetch_trace",
  "execution_proxy",
  "recording_proxy",
  "extension_ingest",
  "har_import",
];

function localTime(date: Date): string {
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60000);
  return local.toISOString().slice(0, 16);
}

function statusTone(status = 0) {
  if (status >= 500) return "text-red-500";
  if (status >= 400) return "text-amber-500";
  if (status >= 300) return "text-blue-500";
  if (status >= 200) return "text-emerald-500";
  return "text-muted-foreground";
}

export function AccessAuditPanel() {
  const formID = React.useId();
  const [url, setURL] = React.useState("");
  const [host, setHost] = React.useState("");
  const [task, setTask] = React.useState("");
  const [from, setFrom] = React.useState(() => localTime(new Date(Date.now() - 86400000)));
  const [to, setTo] = React.useState(() => localTime(new Date(Date.now() + 60000)));
  const [source, setSource] = React.useState("all");
  const [advanced, setAdvanced] = React.useState(false);
  const [coverageOpen, setCoverageOpen] = React.useState(false);
  const [selected, setSelected] = React.useState<AccessAuditEvent | null>(null);
  const [page, setPage] = React.useState<AccessAuditPage | null>(null);
  const [history, setHistory] = React.useState<string[]>([]);
  const [pageIndex, setPageIndex] = React.useState(0);
  const [busy, setBusy] = React.useState(true);
  const [error, setError] = React.useState("");
  const requestID = React.useRef(0);

  const load = React.useCallback(async (queries: string[], index: number) => {
    const id = ++requestID.current;
    setBusy(true);
    setError("");
    try {
      const result = await api.accessAudit(queries[index]);
      if (id !== requestID.current) return;
      setPage(result);
      setHistory(queries);
      setPageIndex(index);
    } catch (e) {
      if (id === requestID.current) setError(e instanceof Error ? e.message : t("Access audit unavailable"));
    } finally {
      if (id === requestID.current) setBusy(false);
    }
  }, []);

  const [initialQuery] = React.useState(() =>
    new URLSearchParams({
      from: new Date(from).toISOString(),
      to: new Date(to).toISOString(),
      limit: "100",
    }).toString(),
  );

  React.useEffect(() => {
    void load([initialQuery], 0);
    return () => {
      requestID.current++;
    };
  }, [initialQuery, load]);

  function search() {
    const start = new Date(from);
    const end = new Date(to);
    if (!Number.isFinite(start.getTime()) || !Number.isFinite(end.getTime()) || start >= end) {
      setAdvanced(true);
      setError(t("Choose a valid start and end time."));
      return;
    }
    void load(
      [
        new URLSearchParams({
          url,
          host,
          task,
          source: source === "all" ? "" : source,
          from: start.toISOString(),
          to: end.toISOString(),
          limit: "100",
        }).toString(),
      ],
      0,
    );
  }

  function nextPage() {
    if (!page?.next) return;
    const params = new URLSearchParams(history[pageIndex]);
    params.set("before", String(page.next));
    void load([...history.slice(0, pageIndex + 1), params.toString()], pageIndex + 1);
  }

  const details: [string, string | number | undefined][] = selected
    ? [
        [t("Observed at"), new Date(selected.observed_at).toLocaleString()],
        [t("Observation source"), selected.source],
        [t("Outcome"), selected.outcome],
        [t("Task ID"), selected.task_id],
        [t("Conversation ID"), selected.conversation_id || undefined],
        [t("Agent"), selected.agent],
        [t("Tool"), selected.tool],
        [t("Request ID"), selected.request_id],
        [t("Run ID"), selected.run_id],
        [t("Tool call ID"), selected.tool_use_id],
      ]
    : [];

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-3">
      <form
        className="space-y-3"
        onSubmit={(e) => {
          e.preventDefault();
          search();
        }}
      >
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative min-w-48 max-w-sm flex-1">
            <SearchIcon className="absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              aria-label={t("URL or path contains")}
              placeholder={t("URL or path contains")}
              value={url}
              onChange={(e) => setURL(e.target.value)}
              maxLength={8192}
              className="h-8 pl-8"
            />
          </div>
          <Input
            aria-label={t("Task ID")}
            placeholder={t("Task ID")}
            value={task}
            onChange={(e) => setTask(e.target.value)}
            maxLength={64}
            className="h-8 w-28"
          />
          <Select value={source} onValueChange={setSource}>
            <SelectTrigger aria-label={t("Observation source")} size="sm" className="h-8 w-40">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">{t("All sources")}</SelectItem>
              {SOURCES.map((value) => (
                <SelectItem key={value} value={value}>
                  {value}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-8"
            aria-expanded={advanced}
            aria-controls={`${formID}-filters`}
            onClick={() => setAdvanced(!advanced)}
          >
            <SlidersHorizontalIcon className="size-3.5" />
            {t("Advanced filters")}
            {host && (
              <Badge variant="secondary" className="px-1">
                1
              </Badge>
            )}
          </Button>
          <Button type="submit" variant="outline" size="sm" className="h-8" disabled={busy}>
            {busy ? <Loader2Icon className="size-3.5 animate-spin" /> : <SearchIcon className="size-3.5" />}
            {t("Search")}
          </Button>
          <div className="ml-auto flex items-center gap-2 text-muted-foreground text-xs">
            <span className="tabular-nums" aria-live="polite">
              {t("{count} observations", { count: page?.events.length ?? 0 })}
            </span>
            <Button
              type="button"
              variant="outline"
              size="icon"
              className="size-8"
              aria-label={t("Previous page")}
              disabled={busy || pageIndex === 0}
              onClick={() => void load(history, pageIndex - 1)}
            >
              <ChevronLeftIcon />
            </Button>
            <span className="tabular-nums">{t("Page {page}", { page: pageIndex + 1 })}</span>
            <Button
              type="button"
              variant="outline"
              size="icon"
              className="size-8"
              aria-label={t("Next page")}
              disabled={busy || !page?.next}
              onClick={nextPage}
            >
              <ChevronRightIcon />
            </Button>
          </div>
        </div>
        {advanced && (
          <div id={`${formID}-filters`} className="flex flex-wrap items-end gap-3 rounded-lg border bg-muted/20 p-3">
            <label htmlFor={`${formID}-host`} className="space-y-1 text-muted-foreground text-xs">
              <span>{t("Exact hostname")}</span>
              <Input
                id={`${formID}-host`}
                value={host}
                onChange={(e) => setHost(e.target.value)}
                maxLength={255}
                className="h-8 w-56 text-foreground"
              />
            </label>
            <label htmlFor={`${formID}-from`} className="space-y-1 text-muted-foreground text-xs">
              <span>{t("From (local time)")}</span>
              <Input
                id={`${formID}-from`}
                type="datetime-local"
                required
                value={from}
                onChange={(e) => setFrom(e.target.value)}
                className="h-8 w-56 text-foreground"
              />
            </label>
            <label htmlFor={`${formID}-to`} className="space-y-1 text-muted-foreground text-xs">
              <span>{t("To (local time, exclusive)")}</span>
              <Input
                id={`${formID}-to`}
                type="datetime-local"
                required
                value={to}
                onChange={(e) => setTo(e.target.value)}
                className="h-8 w-56 text-foreground"
              />
            </label>
          </div>
        )}
      </form>

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-muted-foreground text-xs">
        <span>
          {page ? `${new Date(page.from).toLocaleString()} – ${new Date(page.to).toLocaleString()}` : t("Loading…")}
        </span>
        <Button
          variant="ghost"
          size="sm"
          className="ml-auto h-6 px-1 text-muted-foreground text-xs"
          onClick={() => setCoverageOpen(true)}
        >
          <InfoIcon className="size-3.5" />
          {t("Partial coverage")}
        </Button>
        {!!page?.write_failures_since_start && (
          <span role="status" className="text-destructive">
            {t("Write failures: {count}", { count: page.write_failures_since_start })}
          </span>
        )}
      </div>
      {error && (
        <p role="alert" className="text-destructive text-sm">
          {error}
        </p>
      )}

      <Card className="min-h-80 flex-1 overflow-hidden py-0">
        <div className="min-h-0 flex-1 overflow-auto" aria-busy={busy}>
          <Table>
            <TableHeader className="sticky top-0 z-10 bg-card">
              <TableRow>
                <TableHead className="w-36">{t("Time")}</TableHead>
                <TableHead className="w-20">{t("Method")}</TableHead>
                <TableHead>{t("URL")}</TableHead>
                <TableHead className="w-20">{t("Status")}</TableHead>
                <TableHead className="w-20">{t("Task")}</TableHead>
                <TableHead className="w-36">{t("Worker")}</TableHead>
                <TableHead className="w-40">{t("Observation source")}</TableHead>
                <TableHead className="w-40">{t("Outcome")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {busy && !page && (
                <TableRow>
                  <TableCell colSpan={8} className="py-12 text-center">
                    <Loader2Icon
                      aria-label={t("Loading…")}
                      className="mx-auto size-5 animate-spin text-muted-foreground"
                    />
                  </TableCell>
                </TableRow>
              )}
              {!busy && !page?.events.length && (
                <TableRow>
                  <TableCell colSpan={8} className="py-12 text-center text-muted-foreground">
                    {error ? t("Access audit unavailable") : t("No matching observations in the retained coverage.")}
                  </TableCell>
                </TableRow>
              )}
              {page?.events.map((event) => (
                <TableRow
                  key={event.id}
                  className={cn("cursor-pointer", selected?.id === event.id && "bg-accent hover:bg-accent")}
                  onClick={() => setSelected(event)}
                >
                  <TableCell className="text-muted-foreground text-xs tabular-nums">
                    {new Date(event.observed_at).toLocaleString(undefined, {
                      month: "2-digit",
                      day: "2-digit",
                      hour: "2-digit",
                      minute: "2-digit",
                      second: "2-digit",
                    })}
                  </TableCell>
                  <TableCell>
                    <Badge className="font-mono text-xs">{event.method || "—"}</Badge>
                  </TableCell>
                  <TableCell className="min-w-48 max-w-0">
                    <button
                      type="button"
                      title={event.url}
                      className="block w-full truncate text-left font-mono text-xs underline-offset-4 hover:underline focus-visible:outline-ring"
                      onClick={(e) => {
                        e.stopPropagation();
                        setSelected(event);
                      }}
                    >
                      {event.url}
                    </button>
                  </TableCell>
                  <TableCell className={cn("font-medium text-xs tabular-nums", statusTone(event.status))}>
                    {event.status || "—"}
                  </TableCell>
                  <TableCell className="font-mono text-muted-foreground text-xs">
                    {event.task_id ? `#${event.task_id}` : "—"}
                  </TableCell>
                  <TableCell className="max-w-40 truncate text-xs" title={event.agent}>
                    {event.agent || "—"}
                  </TableCell>
                  <TableCell className="font-mono text-muted-foreground text-xs">{event.source}</TableCell>
                  <TableCell className="font-mono text-muted-foreground text-xs">{event.outcome}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      </Card>

      <Sheet open={selected !== null} onOpenChange={(open) => !open && setSelected(null)}>
        <SheetContent className="w-full! max-w-none! gap-0 p-0 sm:w-[38rem]! sm:max-w-[38rem]!">
          <SheetHeader className="border-b px-5 py-4">
            <SheetTitle>{t("Access audit details")}</SheetTitle>
            <SheetDescription>
              {selected ? `#${selected.id} · ${new Date(selected.observed_at).toLocaleString()}` : ""}
            </SheetDescription>
          </SheetHeader>
          {selected && (
            <div className="min-h-0 flex-1 space-y-5 overflow-auto p-5">
              <div className="flex items-center gap-2">
                <Badge className="font-mono">{selected.method || "—"}</Badge>
                <span className={cn("font-medium text-sm", statusTone(selected.status))}>{selected.status || "—"}</span>
              </div>
              <p className="break-all font-mono text-xs">{selected.url}</p>
              <dl className="space-y-3 text-xs">
                {details.map(([label, value]) => (
                  <div key={label} className="grid grid-cols-[9rem_minmax(0,1fr)] gap-3 border-b pb-3 last:border-0">
                    <dt className="text-muted-foreground">{label}</dt>
                    <dd className="break-all font-mono">{value || t("Unknown")}</dd>
                  </div>
                ))}
              </dl>
            </div>
          )}
        </SheetContent>
      </Sheet>

      <Sheet open={coverageOpen} onOpenChange={setCoverageOpen}>
        <SheetContent className="w-full! max-w-none! gap-0 p-0 sm:w-[32rem]! sm:max-w-[32rem]!">
          <SheetHeader className="border-b px-5 py-4">
            <SheetTitle>{t("Audit coverage")}</SheetTitle>
            <SheetDescription>{t("No match does not prove no access.")}</SheetDescription>
          </SheetHeader>
          <div className="min-h-0 flex-1 space-y-4 overflow-auto p-5 text-muted-foreground text-sm">
            <p>
              {t(
                "Audit coverage is partial across all companies. No match does not prove no access. Direct shell/MCP traffic, opaque tunnels and remote SSRF/RCE hops are not covered. Query parameters, credentials, headers and bodies are not stored; URL paths may still be sensitive.",
              )}
            </p>
            <p>
              {t(
                "Attempts and writes do not prove origin receipt. Response observations may come from a proxy. Imported exchanges are external reports. Several events can describe one request; unknown attribution stays empty.",
              )}
            </p>
            {page && (
              <p>
                {t("Retention: {days} days, up to {max} events. Write failures since startup: {failures}.", {
                  days: page.retention_days,
                  max: page.max_events,
                  failures: page.write_failures_since_start,
                })}
              </p>
            )}
          </div>
        </SheetContent>
      </Sheet>
    </div>
  );
}

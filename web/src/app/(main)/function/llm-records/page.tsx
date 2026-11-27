"use client";

import * as React from "react";
import {
  RadioIcon,
  SearchIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  Loader2Icon,
  XIcon,
  Trash2Icon,
  CopyIcon,
  CheckIcon,
} from "lucide-react";

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

import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatShortDateTime, formatTokens } from "@/lib/format";
import { cn } from "@/lib/utils";
import { t } from "@/i18n";
import { api } from "@/lib/api";

import { initialLLMRecordsPageState, llmRecordsPageReducer } from "./_state/llm-records-page.reducer";

function fmtLatency(ms: number) {
  if (ms < 1000) return `${ms}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
}

function tryFormatJSON(s: string): string {
  try {
    return JSON.stringify(JSON.parse(s), null, 2);
  } catch {
    return s;
  }
}

// Small button that copies the text in the current pane. Shows a brief checkmark after copying. Disabled when text is empty/only placeholders.
function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = React.useState(false);
  const timer = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  React.useEffect(() => () => { if (timer.current) clearTimeout(timer.current); }, []);

  const disabled = !text;
  const copy = async () => {
    if (disabled) return;
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      // navigator.clipboard is unavailable in non-secure contexts (e.g. http LAN); fall back to execCommand.
      const ta = document.createElement("textarea");
      ta.value = text;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.appendChild(ta);
      ta.select();
      try { document.execCommand("copy"); } catch { /* ignore: stay silent if unsupported */ }
      document.body.removeChild(ta);
    }
    setCopied(true);
    if (timer.current) clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopied(false), 1500);
  };

  return (
    <Button
      variant="ghost"
      size="icon"
      className="size-5 shrink-0"
      disabled={disabled}
      title={copied ? t("Copied") : t("Copy content")}
      onClick={copy}
    >
      {copied ? <CheckIcon className="size-3 text-emerald-600" /> : <CopyIcon className="size-3" />}
    </Button>
  );
}

const PAGE_SIZES = [25, 50, 100];

export default function LLMRecordsPage() {
  const [state, dispatch] = React.useReducer(llmRecordsPageReducer, initialLLMRecordsPageState);
  const { filters, tasks } = state;
  const { session, model, task: pickedTask } = filters;
  const { page, size } = state.pagination;
  const { items: records, total, loading } = state.list;
  const { reloadTick } = state;

  // Recording on/off toggle (settings.llm_record; default off). When off the
  // backend records nothing.
  // recKnown flips once the settings call resolves, so the empty state does not
  // claim "recording is off" while the answer is still in flight.
  const { enabled: recEnabled, known: recKnown, busy: recBusy } = state.recording;

  // Inline detail panel (Burp-style split, not a dialog)
  // Normalized view / raw HTTP view. The raw exchange is the only authoritative source when debugging
  // provider-side issues: the normalized view lacks tool schemas and the response has no tool_use blocks.
  const { selected, data: detail, loading: detailLoading, rawView } = state.detail;

  const hasRaw = !!(detail?.raw_request || detail?.raw_response);
  // The toggle keeps the user's choice, but switching to an old record without raw data falls back to the parsed view instead of showing blank.
  const showRaw = rawView && hasRaw;
  // The raw request body is JSON; pretty-printing only reflows it without changing meaning, for readability.
  // The raw response is SSE frames; tryFormatJSON returns failed parses as-is, so one shared function serves both.
  const reqText = showRaw
    ? detail?.raw_request && tryFormatJSON(detail.raw_request)
    : detail?.request_body && tryFormatJSON(detail.request_body);
  const respText = showRaw
    ? detail?.raw_response
    : detail?.response_body && tryFormatJSON(detail.response_body);

  // Per-task delete (task picker + confirm dialog)
  const deleteOpen = state.deleteDialogOpen;
  const deleting = state.requests.deleteTask.pending;

  // Load recording toggle state on mount.
  React.useEffect(() => {
    let alive = true;
    api
      .settings()
      .then((s) => {
        if (!alive) return;
        dispatch({ type: "recordingLoaded", enabled: !!s.llm_record });
      })
      .catch(() => {});
    return () => { alive = false; };
  }, []);

  const toggleRecording = async (on: boolean) => {
    dispatch({ type: "recordingToggleStarted", on }); // optimistic
    try {
      const s = await api.setSettings({ llm_record: on });
      dispatch({ type: "recordingSaved", enabled: !!s.llm_record });
    } catch {
      dispatch({ type: "recordingSaveFailed", on }); // revert on failure
    } finally {
      dispatch({ type: "recordingToggleFinished" });
    }
  };

  // Debounce session filter.
  // biome-ignore lint/correctness/useExhaustiveDependencies: the reducer reads the latest session; re-arm on every keystroke.
  React.useEffect(() => {
    const t = setTimeout(() => dispatch({ type: "sessionDebounced" }), 300);
    return () => clearTimeout(t);
  }, [session]);

  // Load list. Filter changes reset the page inside the reducer, so this runs once per change.
  React.useEffect(() => {
    let alive = true;
    dispatch({ type: "list/loadStarted" });
    api
      .llmRecords({
        model: model || undefined,
        session: filters.debouncedSession || undefined,
        task: pickedTask || undefined,
        page,
        size,
      })
      .then((r) => {
        if (!alive) return;
        dispatch({ type: "list/loaded", items: r.records ?? [], total: r.total ?? 0 });
      })
      .catch(() => {})
      .finally(() => alive && dispatch({ type: "list/loadFinished" }));
    api
      .llmTasks()
      .then((r) => {
        if (alive) dispatch({ type: "tasksLoaded", tasks: r.tasks ?? [] });
      })
      .catch(() => {});
    return () => { alive = false; };
  }, [page, size, filters.debouncedSession, model, pickedTask, reloadTick]);

  // Delete every LLM record for the picked task, then refetch.
  const confirmDelete = () => {
    dispatch({ type: "request/started", key: "deleteTask" });
    api
      .llmRecordsDeleteTask(pickedTask)
      .then(() => {
        dispatch({ type: "taskRecordsDeleted" });
      })
      .catch(() => {})
      .finally(() => dispatch({ type: "request/finished", key: "deleteTask" }));
  };

  // Lazy-load full request/response when a row is selected.
  React.useEffect(() => {
    if (!selected) {
      dispatch({ type: "detailCleared" });
      return;
    }
    let alive = true;
    dispatch({ type: "detailLoadStarted" });
    api
      .llmRecordDetail(selected.id)
      .then((d) => {
        if (alive) dispatch({ type: "detailLoaded", detail: d });
      })
      .catch(() => {})
      .finally(() => {
        if (alive) dispatch({ type: "detailLoadFinished" });
      });
    return () => { alive = false; };
  }, [selected]);

  const totalPages = Math.max(1, Math.ceil(total / size));
  const rangeStart = total === 0 ? 0 : page * size + 1;
  const rangeEnd = page * size + records.length;

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      {/* Header */}
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-2">
          <RadioIcon className="h-5 w-5 text-muted-foreground" />
          <h1 className="text-xl font-semibold tracking-tight">{t("LLM recording")}</h1>
          <Badge variant="secondary">{total}</Badge>
        </div>
      </div>

      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative max-w-sm flex-1">
          <SearchIcon className="absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            placeholder={t("Search Session ID...")}
            value={session}
            onChange={(e) => dispatch({ type: "sessionChanged", session: e.target.value })}
            className="h-8 pl-8"
          />
        </div>
        <Input
          placeholder={t("Model")}
          className="h-8 w-48"
          value={model}
          onChange={(e) => dispatch({ type: "modelFilterChanged", model: e.target.value })}
        />
        <Select value={pickedTask} onValueChange={(task) => dispatch({ type: "taskFilterChanged", task })}>
          <SelectTrigger size="sm" className="w-56">
            <SelectValue placeholder={t("Select a task…")} />
          </SelectTrigger>
          <SelectContent>
            {tasks.length === 0 ? (
              <SelectItem value="__none__" disabled>
                {t("No task records yet")}
              </SelectItem>
            ) : (
              tasks.map((t) => (
                <SelectItem key={t.task_id} value={t.task_id}>
                  <span className="font-mono">#{t.task_id}</span>
                  <span className="ml-2 text-muted-foreground"> ({t.count})</span>
                </SelectItem>
              ))
            )}
          </SelectContent>
        </Select>
        <Button
          variant="destructive"
          size="sm"
          className="h-8"
          disabled={!pickedTask || deleting}
          title={pickedTask ? undefined : t("Select a task above first")}
          onClick={() => dispatch({ type: "deleteDialogOpenChanged", open: true })}
        >
          <Trash2Icon className="size-3.5" />
          {t("Delete task conversations")}
        </Button>
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

        {/* Recording on/off — off means no LLM calls are recorded */}
        <div className="flex items-center gap-2 rounded-md border px-2.5 py-1">
          <Switch
            id="llm-rec-toggle"
            size="sm"
            checked={recEnabled}
            onCheckedChange={toggleRecording}
            disabled={recBusy}
          />
          <label
            htmlFor="llm-rec-toggle"
            className={cn(
              "cursor-pointer text-xs font-medium select-none",
              recEnabled ? "text-foreground" : "text-muted-foreground",
            )}
          >
            {recEnabled ? t("Recording") : t("Off")}
          </label>
        </div>

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

      {/* History table + inline detail (Burp-style split) */}
      <div className="flex h-[calc(100vh-13rem)] min-h-0 flex-col gap-3">
        <Card className="flex min-h-0 flex-1 flex-col overflow-hidden py-0">
          <div className="min-h-0 flex-1 overflow-auto">
            <Table>
              <TableHeader className="sticky top-0 z-10 bg-card">
                <TableRow>
                  <TableHead className="w-[130px]">{t("Time")}</TableHead>
                  <TableHead className="w-[60px]">{t("Task")}</TableHead>
                  <TableHead className="w-[90px]">{t("Worker")}</TableHead>
                  <TableHead className="w-[100px]">{t("Profile")}</TableHead>
                  <TableHead className="w-[140px]">{t("Model")}</TableHead>
                  <TableHead className="w-[70px]">{t("Latency")}</TableHead>
                  <TableHead className="w-[90px]">{t("Tokens")}</TableHead>
                  <TableHead className="w-[60px]">{t("Status")}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {loading && records.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={8} className="py-12 text-center">
                      <Loader2Icon className="mx-auto h-5 w-5 animate-spin text-muted-foreground" />
                    </TableCell>
                  </TableRow>
                ) : records.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={8} className="py-12 text-center text-sm text-muted-foreground">
                      {!recKnown || recEnabled
                        ? t("No LLM call records yet")
                        : t("LLM call recording is off. Turn on the Recording toggle above to start capturing calls.")}
                    </TableCell>
                  </TableRow>
                ) : (
                  records.map((rec) => (
                    <TableRow
                      key={rec.id}
                      className={cn(
                        "cursor-pointer",
                        selected?.id === rec.id && "bg-accent hover:bg-accent",
                      )}
                      onClick={() => dispatch({ type: "recordSelected", record: rec })}
                    >
                      <TableCell className="text-xs text-muted-foreground tabular-nums">
                        {formatShortDateTime(rec.ts)}
                      </TableCell>
                      <TableCell className="text-xs font-mono text-muted-foreground">
                        {rec.task_id ? `#${rec.task_id}` : "-"}
                      </TableCell>
                      <TableCell>
                        <Badge variant="outline" className="text-xs font-mono">
                          {rec.worker || "-"}
                        </Badge>
                      </TableCell>
                      <TableCell>
                        <span className="text-xs">{rec.profile_name || "-"}</span>
                      </TableCell>
                      <TableCell>
                        <span className="text-xs font-mono">{rec.model || "-"}</span>
                      </TableCell>
                      <TableCell>
                        <span className={cn("text-xs", rec.latency_ms > 30000 && "text-amber-500")}>
                          {fmtLatency(rec.latency_ms)}
                        </span>
                      </TableCell>
                      <TableCell>
                        <span className="text-xs">
                          {formatTokens(rec.input_tokens)} / {formatTokens(rec.output_tokens)}
                        </span>
                      </TableCell>
                      <TableCell>
                        {rec.status === "ok" ? (
                          <Badge variant="secondary" className="text-xs text-emerald-600">{t("OK")}</Badge>
                        ) : (
                          <Badge variant="destructive" className="text-xs">{t("Error")}</Badge>
                        )}
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        </Card>

        {/* Inline detail panel */}
        {selected && (
          <Card className="flex h-[42%] min-h-0 flex-col overflow-hidden py-0">
            {/* Detail header */}
            <div className="flex items-center gap-2 border-b px-3 py-2">
              <Badge variant="outline" className="text-xs font-mono">
                #{selected.id}
              </Badge>
              <Badge variant="outline" className="text-xs font-mono">
                {selected.profile_name || "-"}
              </Badge>
              <Badge variant="outline" className="text-xs font-mono">
                {selected.model || "-"}
              </Badge>
              {selected.task_id && (
                <Badge variant="outline" className="text-xs font-mono">
                  {t("Task #{id}", { id: selected.task_id })}
                </Badge>
              )}
              <span className="text-xs text-muted-foreground">{formatShortDateTime(selected.ts)}</span>
              <span className={cn("text-xs", selected.latency_ms > 30000 && "text-amber-500")}>
                {fmtLatency(selected.latency_ms)}
              </span>
              {selected.status === "ok" ? (
                <Badge variant="secondary" className="text-xs text-emerald-600">{t("OK")}</Badge>
              ) : (
                <Badge variant="destructive" className="text-xs">{t("Error")}</Badge>
              )}
              {/* Raw view toggle. Old records have no raw data; disable it rather than silently
                  falling back, to avoid looking like "raw matches parsed". */}
              <Button
                variant={showRaw ? "secondary" : "ghost"}
                size="sm"
                className="ml-auto h-7 shrink-0 text-xs"
                disabled={!hasRaw}
                title={
                  hasRaw
                    ? t("View the raw HTTP actually exchanged with the provider")
                    : t("This record was captured before the feature launched; no raw data")
                }
                onClick={() => dispatch({ type: "rawViewToggled" })}
              >
                {t("Raw")}
              </Button>
              <Button
                variant="ghost"
                size="icon"
                className="size-7 shrink-0"
                onClick={() => dispatch({ type: "recordSelected", record: null })}
              >
                <XIcon />
              </Button>
            </div>
            {/* Request / Response split */}
            <div className="grid min-h-0 flex-1 grid-cols-2 divide-x">
              <div className="flex min-h-0 min-w-0 flex-col">
                <div className="flex items-center gap-2 border-b py-0.5 pr-1.5 pl-3 text-[11px] font-medium text-muted-foreground">
                  <span>{showRaw ? t("Request · raw") : t("Request")}</span>
                  <CopyButton text={reqText || ""} />
                </div>
                <div className="min-h-0 flex-1 overflow-auto">
                  {detailLoading ? (
                    <div className="flex items-center gap-2 p-3 text-xs text-muted-foreground">
                      <Loader2Icon className="size-3.5 animate-spin" />
                      {t("Loading…")}
                    </div>
                  ) : (
                    <pre className="p-3 font-mono text-xs break-all whitespace-pre-wrap">
                      {reqText || t("(empty)")}
                    </pre>
                  )}
                </div>
              </div>
              <div className="flex min-h-0 min-w-0 flex-col">
                <div className="flex items-center gap-2 border-b py-0.5 pr-1.5 pl-3 text-[11px] font-medium text-muted-foreground">
                  <span>{showRaw ? t("Response · raw (SSE)") : t("Response")}</span>
                  <CopyButton text={respText || ""} />
                </div>
                <div className="min-h-0 flex-1 overflow-auto">
                  {detailLoading ? (
                    <div className="flex items-center gap-2 p-3 text-xs text-muted-foreground">
                      <Loader2Icon className="size-3.5 animate-spin" />
                      {t("Loading…")}
                    </div>
                  ) : (
                    <pre className={cn(
                      "p-3 font-mono text-xs break-all whitespace-pre-wrap",
                      selected.status !== "ok" && "text-red-600 dark:text-red-400",
                    )}>
                      {respText || t("(empty)")}
                    </pre>
                  )}
                </div>
              </div>
            </div>
          </Card>
        )}
      </div>

      <AlertDialog open={deleteOpen} onOpenChange={(open) => dispatch({ type: "deleteDialogOpenChanged", open })}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t('Delete all LLM conversations for task "{task}"?', { task: pickedTask })}</AlertDialogTitle>
            <AlertDialogDescription>
              {t(
                "This will permanently delete all LLM call records for this task (including raw requests/responses). This action cannot be undone.",
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
              {deleting ? t("Deleting…") : t("Confirm delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}

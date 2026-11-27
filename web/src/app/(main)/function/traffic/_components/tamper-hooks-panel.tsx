"use client";

import * as React from "react";

import {
  AlertTriangleIcon,
  Code2Icon,
  HistoryIcon,
  Loader2Icon,
  PencilIcon,
  PlayIcon,
  PlusIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  ShieldAlertIcon,
  ShieldCheckIcon,
  Trash2Icon,
} from "lucide-react";
import { toast } from "sonner";

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
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { t } from "@/i18n";
import { api } from "@/lib/api";
import { formatShortDateTime } from "@/lib/format";
import {
  buildMatchers,
  DEFAULT_REQUEST_HOOK_SOURCE,
  DEFAULT_RESPONSE_HOOK_SOURCE,
  decodeTraceAfter,
  exchangeDetailToDryRunIO,
  formatHeaderLines,
  formatHeaderMatcherLines,
  isTraceLineMasked,
  parseHeaderLines,
} from "@/lib/tamper-hooks";
import type { TamperDryRunRequest, TamperHook, TamperHookPhase, TrafficExchange } from "@/lib/types";

import {
  type EditorState,
  initialTamperHooksPanelState,
  tamperHooksPanelReducer,
} from "../_state/tamper-hooks-panel.reducer";

function emptyEditor(phase: TamperHookPhase = "request"): EditorState {
  return {
    mode: "create",
    enabled: false,
    name: "",
    phase,
    priority: "100",
    note: "",
    methods: "",
    host: "",
    urlRegex: "",
    headers: "",
    bodyRegex: "",
    status: "",
    respHeaders: "",
    respBodyRegex: "",
    source: phase === "response" ? DEFAULT_RESPONSE_HOOK_SOURCE : DEFAULT_REQUEST_HOOK_SOURCE,
  };
}

function editorFromHook(h: TamperHook): EditorState {
  return {
    mode: "edit",
    id: h.id,
    enabled: h.enabled,
    name: h.name,
    phase: h.phase,
    priority: String(h.priority),
    note: h.note ?? "",
    methods: (h.matchers?.methods ?? []).join(", "),
    host: h.matchers?.host ?? "",
    urlRegex: h.matchers?.url_regex ?? "",
    headers: formatHeaderMatcherLines(h.matchers?.headers),
    bodyRegex: h.matchers?.body_regex ?? "",
    status: (h.matchers?.status ?? []).join(", "),
    respHeaders: formatHeaderMatcherLines(h.matchers?.resp_headers),
    respBodyRegex: h.matchers?.resp_body_regex ?? "",
    source: h.source,
  };
}

export function TamperHooksPanel({ recentExchanges = [] }: { recentExchanges?: TrafficExchange[] }) {
  const [state, dispatch] = React.useReducer(tamperHooksPanelReducer, initialTamperHooksPanelState);
  const { list, editor, deleteTarget, versionHistory, dryRun, requests } = state;
  const { hooks, enabledByEnv, loading } = list;
  const { hook: versionsHook, items: versions, rollbackTarget } = versionHistory;
  const dryRunTarget = dryRun.target;

  const loadHooks = React.useCallback(async () => {
    dispatch({ type: "hooksLoadStarted" });
    try {
      const res = await api.trafficHooks();
      dispatch({ type: "hooksLoaded", hooks: res.hooks ?? [], enabledByEnv: Boolean(res.enabled_by_env) });
    } catch (e) {
      dispatch({ type: "hooksLoadFailed", error: (e as Error).message });
    } finally {
      dispatch({ type: "hooksLoadFinished" });
    }
  }, []);

  React.useEffect(() => {
    void loadHooks();
  }, [loadHooks]);

  const handleToggle = async (hook: TamperHook, next: boolean) => {
    dispatch({ type: "toggleStarted", id: hook.id });
    try {
      const updated = await api.toggleTrafficHook(hook.id, next);
      dispatch({ type: "hookUpdated", hook: updated });
      toast.success(
        next ? t('Hook "{name}" enabled', { name: hook.name }) : t('Hook "{name}" disabled', { name: hook.name }),
      );
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      dispatch({ type: "toggleFinished" });
    }
  };

  const handleSave = async () => {
    if (!editor) return;
    dispatch({ type: "request/errorCleared", key: "save" });
    const priority = Number.parseInt(editor.priority, 10);
    if (Number.isNaN(priority)) {
      dispatch({ type: "request/failed", key: "save", error: t("Priority must be an integer") });
      return;
    }
    const payload = {
      name: editor.name.trim(),
      enabled: editor.mode === "create" ? false : editor.enabled,
      phase: editor.phase,
      priority,
      matchers: buildMatchers(editor),
      source: editor.source,
      note: editor.note.trim(),
    };
    dispatch({ type: "request/started", key: "save" });
    try {
      if (editor.mode === "create") {
        await api.createTrafficHook(payload);
        toast.success(t("Hook created (disabled by default)"));
      } else if (editor.id !== undefined) {
        await api.updateTrafficHook(editor.id, payload);
        toast.success(t("Hook updated"));
      }
      dispatch({ type: "editorClosed" });
      await loadHooks();
    } catch (e) {
      dispatch({ type: "request/failed", key: "save", error: (e as Error).message });
    } finally {
      dispatch({ type: "request/finished", key: "save" });
    }
  };

  const handleDelete = async () => {
    if (!deleteTarget) return;
    dispatch({ type: "request/started", key: "delete" });
    try {
      await api.deleteTrafficHook(deleteTarget.id);
      toast.success(t("Hook deleted"));
      dispatch({ type: "deleteDialogClosed" });
      await loadHooks();
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      dispatch({ type: "request/finished", key: "delete" });
    }
  };

  const openVersions = async (hook: TamperHook) => {
    dispatch({ type: "versionsOpened", hook });
    try {
      const res = await api.trafficHookVersions(hook.id);
      dispatch({ type: "versionsLoaded", versions: res.versions ?? [] });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      dispatch({ type: "versionsLoadFinished" });
    }
  };

  const handleRollback = async () => {
    if (!versionsHook || !rollbackTarget) return;
    dispatch({ type: "request/started", key: "rollback" });
    try {
      const updated = await api.rollbackTrafficHook(versionsHook.id, rollbackTarget.version);
      toast.success(
        t("Rolled back to v{version} (now v{current})", { version: rollbackTarget.version, current: updated.version }),
      );
      dispatch({ type: "rolledBack", hook: updated });
      await loadHooks();
      const res = await api.trafficHookVersions(updated.id);
      dispatch({ type: "versionsLoaded", versions: res.versions ?? [] });
    } catch (e) {
      toast.error((e as Error).message);
    } finally {
      dispatch({ type: "request/finished", key: "rollback" });
    }
  };

  const openDryRunForHook = (hook: TamperHook) => {
    dispatch({
      type: "dryRunOpened",
      target: { label: `${hook.name} (v${hook.version}, ${hook.phase})`, hookId: hook.id },
    });
  };

  const openDryRunForDraft = () => {
    if (!editor) return;
    dispatch({
      type: "dryRunOpened",
      target: {
        label: t("Draft: {name} ({phase})", { name: editor.name.trim() || t("untitled"), phase: editor.phase }),
        inline: {
          name: editor.name.trim() || "dry-run",
          phase: editor.phase,
          matchers: buildMatchers(editor),
          source: editor.source,
        },
      },
    });
  };

  const loadExchangeIntoDryRun = async (id: string) => {
    const trimmed = id.trim();
    if (!trimmed) return;
    dispatch({ type: "exchangeLoadStarted" });
    try {
      const detail = await api.trafficExchange(trimmed);
      const matched = recentExchanges.find((ex) => ex.id === trimmed);
      const converted = exchangeDetailToDryRunIO(detail, matched?.url);
      const warnings: string[] = [];
      if (converted.reqBinary || converted.respBinary) {
        warnings.push(t("Binary body detected; binary bodies are skipped by hooks in 0.7."));
      }
      if (converted.reqPreviewOnly || converted.respPreviewOnly) {
        warnings.push(t("Body was truncated in the recorded preview; only the inline preview was loaded."));
      }
      dispatch({
        type: "exchangeLoaded",
        input: {
          method: converted.io.request.method,
          url: converted.io.request.url,
          reqHeaders: formatHeaderLines(converted.io.request.headers),
          reqBody: converted.io.request.body,
          status: String(converted.io.response.status),
          respHeaders: formatHeaderLines(converted.io.response.headers),
          respBody: converted.io.response.body,
        },
        warning: warnings.length ? warnings.join(" ") : null,
      });
      toast.success(t("Loaded exchange {id}", { id: trimmed }));
    } catch (e) {
      dispatch({ type: "exchangeLoadFailed", error: (e as Error).message });
    } finally {
      dispatch({ type: "exchangeLoadFinished" });
    }
  };

  const runDryRun = async () => {
    if (!dryRunTarget) return;
    dispatch({ type: "dryRunStarted" });
    const { input } = dryRun;
    const statusNum = Number.parseInt(input.status, 10);
    const body: TamperDryRunRequest = {
      ...(dryRunTarget.hookId
        ? { hook_id: dryRunTarget.hookId }
        : {
            name: dryRunTarget.inline?.name,
            phase: dryRunTarget.inline?.phase,
            matchers: dryRunTarget.inline?.matchers,
            source: dryRunTarget.inline?.source,
          }),
      request: {
        method: input.method.trim().toUpperCase() || "GET",
        url: input.url.trim() || "https://example.test/",
        headers: parseHeaderLines(input.reqHeaders),
        body: input.reqBody,
      },
      response: {
        status: Number.isNaN(statusNum) ? 200 : statusNum,
        headers: parseHeaderLines(input.respHeaders),
        body: input.respBody,
      },
    };
    try {
      const res = await api.dryRunTrafficHook(body);
      dispatch({ type: "dryRunSucceeded", result: res });
    } catch (e) {
      dispatch({ type: "dryRunFailed", error: (e as Error).message });
    } finally {
      dispatch({ type: "dryRunFinished" });
    }
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col gap-4">
      {!enabledByEnv && (
        <Alert className="border-amber-500/40 bg-amber-500/10 py-2.5">
          <AlertTriangleIcon className="size-4 text-amber-500" />
          <AlertDescription className="text-xs">
            {t(
              "Live hook execution is disabled (ARTEX_TAMPER_HOOKS_ENABLED is off). You can create, edit, version, and dry-run hooks, but none will run on live proxy traffic until enabled.",
            )}
          </AlertDescription>
        </Alert>
      )}

      {list.error && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription className="text-xs">{list.error}</AlertDescription>
        </Alert>
      )}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-xs text-muted-foreground">
          {t(
            "Operator-only Go-subset tampering hooks run after declarative rewrite rules per phase and fail open on error.",
          )}
        </p>
        <div className="flex items-center gap-2">
          <Button variant="outline" size="sm" className="h-8" onClick={() => void loadHooks()} disabled={loading}>
            <RefreshCwIcon className={`size-3.5 ${loading ? "animate-spin" : ""}`} />
            {t("Refresh")}
          </Button>
          <Button
            size="sm"
            className="h-8"
            onClick={() => dispatch({ type: "editorOpened", form: emptyEditor("request") })}
          >
            <PlusIcon className="size-3.5" />
            {t("New hook")}
          </Button>
        </div>
      </div>

      <Card className="min-h-0 flex-1 overflow-auto p-0">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("Name")}</TableHead>
              <TableHead className="w-28">{t("Phase")}</TableHead>
              <TableHead className="w-24">{t("Priority")}</TableHead>
              <TableHead className="w-24">{t("Version")}</TableHead>
              <TableHead className="w-24">{t("Enabled")}</TableHead>
              <TableHead className="w-44">{t("Last error")}</TableHead>
              <TableHead className="w-48 text-right">{t("Actions")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && hooks.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="py-10 text-center text-sm text-muted-foreground">
                  <Loader2Icon className="mx-auto mb-2 size-5 animate-spin" />
                  {t("Loading hooks…")}
                </TableCell>
              </TableRow>
            ) : hooks.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className="py-10 text-center text-sm text-muted-foreground">
                  {t("No tampering hooks configured yet.")}
                </TableCell>
              </TableRow>
            ) : (
              hooks.map((hook) => (
                <TableRow key={hook.id}>
                  <TableCell>
                    <div className="font-medium">{hook.name}</div>
                    {hook.note && <div className="line-clamp-1 text-xs text-muted-foreground">{hook.note}</div>}
                  </TableCell>
                  <TableCell>
                    <Badge variant="outline" className="font-mono text-xs">
                      {hook.phase}
                    </Badge>
                  </TableCell>
                  <TableCell className="font-mono text-xs tabular-nums">{hook.priority}</TableCell>
                  <TableCell className="font-mono text-xs tabular-nums">v{hook.version}</TableCell>
                  <TableCell>
                    <Switch
                      checked={hook.enabled}
                      disabled={list.togglingId === hook.id}
                      aria-label={t("Toggle hook {name}", { name: hook.name })}
                      onCheckedChange={(checked) => void handleToggle(hook, checked)}
                    />
                  </TableCell>
                  <TableCell>
                    {hook.load_error ? (
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <Badge variant="destructive" className="gap-1 font-mono text-xs">
                            <ShieldAlertIcon className="size-3" />
                            {t("load_error")}
                          </Badge>
                        </TooltipTrigger>
                        <TooltipContent className="max-w-xs font-mono text-xs">{hook.load_error}</TooltipContent>
                      </Tooltip>
                    ) : hook.last_error ? (
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <Badge variant="destructive" className="gap-1 font-mono text-xs">
                            <AlertTriangleIcon className="size-3" />
                            {hook.last_error.class} ({hook.last_error.count})
                          </Badge>
                        </TooltipTrigger>
                        <TooltipContent className="text-xs">
                          {t("Last seen at {time}", { time: formatShortDateTime(hook.last_error.at) })}
                        </TooltipContent>
                      </Tooltip>
                    ) : (
                      <Badge variant="secondary" className="gap-1 text-xs text-muted-foreground">
                        <ShieldCheckIcon className="size-3" />
                        {t("None")}
                      </Badge>
                    )}
                  </TableCell>
                  <TableCell className="text-right">
                    <div className="flex items-center justify-end gap-1">
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={t("Dry-run")}
                        aria-label={t("Dry-run")}
                        onClick={() => openDryRunForHook(hook)}
                      >
                        <PlayIcon className="size-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={t("Versions")}
                        aria-label={t("Versions")}
                        onClick={() => void openVersions(hook)}
                      >
                        <HistoryIcon className="size-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={t("Edit")}
                        aria-label={t("Edit")}
                        onClick={() => dispatch({ type: "editorOpened", form: editorFromHook(hook) })}
                      >
                        <PencilIcon className="size-3.5" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={t("Delete")}
                        aria-label={t("Delete")}
                        className="text-destructive hover:text-destructive"
                        onClick={() => dispatch({ type: "deleteRequested", hook })}
                      >
                        <Trash2Icon className="size-3.5" />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>

      {/* Create / Edit Dialog */}
      <Dialog
        open={editor !== null}
        onOpenChange={(open) => {
          if (!open) dispatch({ type: "editorClosed" });
        }}
      >
        <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl">
          <DialogHeader>
            <DialogTitle>{editor?.mode === "edit" ? t("Edit tampering hook") : t("New tampering hook")}</DialogTitle>
            <DialogDescription>
              {editor?.mode === "create"
                ? t("New hooks are created disabled. Save and dry-run before enabling.")
                : t("Saving creates a new version in history. Source and matchers are sealed at rest.")}
            </DialogDescription>
          </DialogHeader>

          {editor && (
            <div className="space-y-4 py-2">
              {requests.save.error && (
                <Alert variant="destructive">
                  <AlertTriangleIcon className="size-4" />
                  <AlertDescription className="font-mono text-xs whitespace-pre-wrap">
                    {requests.save.error}
                  </AlertDescription>
                </Alert>
              )}

              <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
                <div className="space-y-1.5 sm:col-span-1">
                  <Label htmlFor="hook-name">{t("Name")}</Label>
                  <Input
                    id="hook-name"
                    value={editor.name}
                    placeholder="sign-api-requests"
                    onChange={(e) => dispatch({ type: "editorFieldsChanged", patch: { name: e.target.value } })}
                  />
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="hook-phase">{t("Phase")}</Label>
                  <Select
                    value={editor.phase}
                    onValueChange={(val) => {
                      const nextPhase = val as TamperHookPhase;
                      const shouldSwapTemplate =
                        editor.mode === "create" &&
                        (editor.source.trim() === DEFAULT_REQUEST_HOOK_SOURCE.trim() ||
                          editor.source.trim() === DEFAULT_RESPONSE_HOOK_SOURCE.trim());
                      dispatch({
                        type: "editorFieldsChanged",
                        patch: {
                          phase: nextPhase,
                          source: shouldSwapTemplate
                            ? nextPhase === "response"
                              ? DEFAULT_RESPONSE_HOOK_SOURCE
                              : DEFAULT_REQUEST_HOOK_SOURCE
                            : editor.source,
                        },
                      });
                    }}
                  >
                    <SelectTrigger id="hook-phase">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="request">request</SelectItem>
                      <SelectItem value="response">response</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-1.5">
                  <Label htmlFor="hook-priority">{t("Priority")}</Label>
                  <Input
                    id="hook-priority"
                    type="number"
                    value={editor.priority}
                    onChange={(e) => dispatch({ type: "editorFieldsChanged", patch: { priority: e.target.value } })}
                  />
                </div>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="hook-note">{t("Note")}</Label>
                <Input
                  id="hook-note"
                  value={editor.note}
                  placeholder={t("Optional operator note")}
                  onChange={(e) => dispatch({ type: "editorFieldsChanged", patch: { note: e.target.value } })}
                />
              </div>

              {/* Matchers */}
              <div className="space-y-3 rounded-md border p-3">
                <div className="text-xs font-medium text-muted-foreground">
                  {t("Matchers (prefilter — empty matches all)")}
                </div>
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <div className="space-y-1">
                    <Label htmlFor="m-methods" className="text-xs">
                      {t("Methods (comma-separated)")}
                    </Label>
                    <Input
                      id="m-methods"
                      className="h-8 font-mono text-xs"
                      value={editor.methods}
                      placeholder="POST, PUT"
                      onChange={(e) => dispatch({ type: "editorFieldsChanged", patch: { methods: e.target.value } })}
                    />
                  </div>
                  <div className="space-y-1">
                    <Label htmlFor="m-host" className="text-xs">
                      {t("Host (exact or *.suffix)")}
                    </Label>
                    <Input
                      id="m-host"
                      className="h-8 font-mono text-xs"
                      value={editor.host}
                      placeholder="*.example.test"
                      onChange={(e) => dispatch({ type: "editorFieldsChanged", patch: { host: e.target.value } })}
                    />
                  </div>
                  <div className="space-y-1">
                    <Label htmlFor="m-url" className="text-xs">
                      {t("URL regex")}
                    </Label>
                    <Input
                      id="m-url"
                      className="h-8 font-mono text-xs"
                      value={editor.urlRegex}
                      placeholder="^/api/v1/"
                      onChange={(e) => dispatch({ type: "editorFieldsChanged", patch: { urlRegex: e.target.value } })}
                    />
                  </div>
                  <div className="space-y-1">
                    <Label htmlFor="m-body" className="text-xs">
                      {t("Request body regex")}
                    </Label>
                    <Input
                      id="m-body"
                      className="h-8 font-mono text-xs"
                      value={editor.bodyRegex}
                      placeholder="token"
                      onChange={(e) => dispatch({ type: "editorFieldsChanged", patch: { bodyRegex: e.target.value } })}
                    />
                  </div>
                </div>
                <div className="space-y-1">
                  <Label htmlFor="m-headers" className="text-xs">
                    {t("Request headers (one per line: Name or Name: value_regex)")}
                  </Label>
                  <Textarea
                    id="m-headers"
                    rows={2}
                    className="font-mono text-xs"
                    value={editor.headers}
                    placeholder={"Content-Type: application/json\nX-Api-Key"}
                    onChange={(e) => dispatch({ type: "editorFieldsChanged", patch: { headers: e.target.value } })}
                  />
                </div>

                {editor.phase === "response" && (
                  <div className="grid grid-cols-1 gap-3 border-t pt-3 sm:grid-cols-2">
                    <div className="space-y-1">
                      <Label htmlFor="m-status" className="text-xs">
                        {t("Status codes (comma-separated, e.g. 200, 4xx)")}
                      </Label>
                      <Input
                        id="m-status"
                        className="h-8 font-mono text-xs"
                        value={editor.status}
                        placeholder="200, 2xx"
                        onChange={(e) => dispatch({ type: "editorFieldsChanged", patch: { status: e.target.value } })}
                      />
                    </div>
                    <div className="space-y-1">
                      <Label htmlFor="m-resp-body" className="text-xs">
                        {t("Response body regex")}
                      </Label>
                      <Input
                        id="m-resp-body"
                        className="h-8 font-mono text-xs"
                        value={editor.respBodyRegex}
                        onChange={(e) =>
                          dispatch({ type: "editorFieldsChanged", patch: { respBodyRegex: e.target.value } })
                        }
                      />
                    </div>
                    <div className="space-y-1 sm:col-span-2">
                      <Label htmlFor="m-resp-headers" className="text-xs">
                        {t("Response headers (one per line: Name or Name: value_regex)")}
                      </Label>
                      <Textarea
                        id="m-resp-headers"
                        rows={2}
                        className="font-mono text-xs"
                        value={editor.respHeaders}
                        placeholder="Content-Type: application/json"
                        onChange={(e) =>
                          dispatch({ type: "editorFieldsChanged", patch: { respHeaders: e.target.value } })
                        }
                      />
                    </div>
                  </div>
                )}
              </div>

              {/* Source editor */}
              <div className="space-y-1.5">
                <div className="flex items-center justify-between">
                  <Label htmlFor="hook-source" className="flex items-center gap-1.5">
                    <Code2Icon className="size-3.5" />
                    {t("Go-subset source")}
                  </Label>
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="h-6 px-2 text-xs"
                    onClick={() =>
                      dispatch({
                        type: "editorFieldsChanged",
                        patch: {
                          source:
                            editor.phase === "response" ? DEFAULT_RESPONSE_HOOK_SOURCE : DEFAULT_REQUEST_HOOK_SOURCE,
                        },
                      })
                    }
                  >
                    {t("Reset template")}
                  </Button>
                </div>
                <Textarea
                  id="hook-source"
                  rows={12}
                  spellCheck={false}
                  className="font-mono text-xs leading-relaxed"
                  value={editor.source}
                  onChange={(e) => dispatch({ type: "editorFieldsChanged", patch: { source: e.target.value } })}
                />
                <p className="text-xs text-muted-foreground">
                  {editor.phase === "response"
                    ? t("Entrypoint: package hook · func OnResponse(req *Request, resp *Response) error")
                    : t("Entrypoint: package hook · func OnRequest(req *Request) error")}
                </p>
              </div>
            </div>
          )}

          <DialogFooter className="gap-2 sm:justify-between">
            <Button type="button" variant="outline" size="sm" onClick={openDryRunForDraft} disabled={!editor}>
              <PlayIcon className="size-3.5" />
              {t("Dry-run draft")}
            </Button>
            <div className="flex items-center gap-2">
              <Button
                type="button"
                variant="outline"
                onClick={() => dispatch({ type: "editorClosed" })}
                disabled={requests.save.pending}
              >
                {t("Cancel")}
              </Button>
              <Button type="button" onClick={() => void handleSave()} disabled={requests.save.pending}>
                {requests.save.pending && <Loader2Icon className="size-3.5 animate-spin" />}
                {editor?.mode === "edit" ? t("Save changes") : t("Create hook")}
              </Button>
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation */}
      <AlertDialog
        open={deleteTarget !== null}
        onOpenChange={(open) => !open && dispatch({ type: "deleteDialogClosed" })}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{t("Delete tampering hook?")}</AlertDialogTitle>
            <AlertDialogDescription>
              {t('This permanently removes "{name}" and its stored version history.', {
                name: deleteTarget?.name ?? "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={requests.delete.pending}>{t("Cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                void handleDelete();
              }}
              disabled={requests.delete.pending}
              className="bg-destructive text-destructive-foreground hover:bg-destructive/90"
            >
              {requests.delete.pending && <Loader2Icon className="size-3.5 animate-spin" />}
              {t("Delete")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Versions Sheet */}
      <Sheet
        open={versionsHook !== null}
        onOpenChange={(open) => !open && dispatch({ type: "versionHistoryChanged", patch: { hook: null } })}
      >
        <SheetContent className="w-full overflow-y-auto sm:max-w-2xl">
          <SheetHeader>
            <SheetTitle>{t("Versions: {name}", { name: versionsHook?.name ?? "" })}</SheetTitle>
            <SheetDescription>
              {t("Up to 20 newest revisions are retained. Rolling back creates a new monotonic version.")}
            </SheetDescription>
          </SheetHeader>

          <div className="mt-4 space-y-3">
            {versionHistory.loading ? (
              <div className="py-10 text-center text-sm text-muted-foreground">
                <Loader2Icon className="mx-auto mb-2 size-5 animate-spin" />
                {t("Loading versions…")}
              </div>
            ) : versions.length === 0 ? (
              <div className="py-8 text-center text-sm text-muted-foreground">{t("No stored versions.")}</div>
            ) : (
              versions.map((v) => {
                const isCurrent = versionsHook?.version === v.version;
                const isExpanded = versionHistory.expanded === v.version;
                return (
                  <Card key={v.id} className="space-y-2 p-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <div className="flex items-center gap-2">
                        <Badge variant={isCurrent ? "default" : "outline"} className="font-mono text-xs">
                          v{v.version}
                        </Badge>
                        {isCurrent && <Badge variant="secondary">{t("Current")}</Badge>}
                        <Badge variant="outline" className="font-mono text-xs">
                          {v.phase}
                        </Badge>
                        <span className="font-mono text-xs text-muted-foreground">
                          {t("prio {n}", { n: v.priority })} · {v.source_sha256.slice(0, 12)}
                        </span>
                      </div>
                      <span className="text-xs text-muted-foreground">{formatShortDateTime(v.created_at)}</span>
                    </div>
                    {v.note && <p className="text-xs text-muted-foreground">{v.note}</p>}
                    <div className="flex items-center justify-between gap-2 pt-1">
                      <Button
                        variant="ghost"
                        size="sm"
                        className="h-7 px-2 text-xs"
                        onClick={() =>
                          dispatch({
                            type: "versionHistoryChanged",
                            patch: { expanded: isExpanded ? null : v.version },
                          })
                        }
                      >
                        <Code2Icon className="size-3.5" />
                        {isExpanded ? t("Hide source") : t("View source")}
                      </Button>
                      {!isCurrent && (
                        <Button
                          variant="outline"
                          size="sm"
                          className="h-7 px-2.5 text-xs"
                          onClick={() => dispatch({ type: "versionHistoryChanged", patch: { rollbackTarget: v } })}
                        >
                          <RotateCcwIcon className="size-3.5" />
                          {t("Rollback to v{version}", { version: v.version })}
                        </Button>
                      )}
                    </div>
                    {isExpanded && (
                      <pre className="max-h-72 overflow-auto rounded-md border bg-muted/40 p-2.5 font-mono text-xs">
                        {v.source}
                      </pre>
                    )}
                  </Card>
                );
              })
            )}
          </div>
        </SheetContent>
      </Sheet>

      {/* Rollback confirmation */}
      <AlertDialog
        open={rollbackTarget !== null}
        onOpenChange={(open) => !open && dispatch({ type: "versionHistoryChanged", patch: { rollbackTarget: null } })}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>
              {t("Rollback hook to v{version}?", { version: rollbackTarget?.version ?? 0 })}
            </AlertDialogTitle>
            <AlertDialogDescription>
              {t('This restores the matchers and source from v{version} as a new version on "{name}".', {
                version: rollbackTarget?.version ?? 0,
                name: versionsHook?.name ?? "",
              })}
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={requests.rollback.pending}>{t("Cancel")}</AlertDialogCancel>
            <AlertDialogAction
              onClick={(e) => {
                e.preventDefault();
                void handleRollback();
              }}
              disabled={requests.rollback.pending}
            >
              {requests.rollback.pending && <Loader2Icon className="size-3.5 animate-spin" />}
              {t("Rollback")}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>

      {/* Dry-run Sheet */}
      <Sheet
        open={dryRunTarget !== null}
        onOpenChange={(open) => !open && dispatch({ type: "dryRunChanged", patch: { target: null } })}
      >
        <SheetContent className="w-full overflow-y-auto sm:max-w-3xl">
          <SheetHeader>
            <SheetTitle>{t("Dry-run: {target}", { target: dryRunTarget?.label ?? "" })}</SheetTitle>
            <SheetDescription>
              {t(
                "Runs the hook in the sandbox against a recorded exchange or synthetic HTTP message without touching live traffic.",
              )}
            </SheetDescription>
          </SheetHeader>

          <div className="mt-4 space-y-4">
            <Tabs
              value={dryRun.inputMode}
              onValueChange={(v) =>
                dispatch({ type: "dryRunChanged", patch: { inputMode: v as "synthetic" | "exchange" } })
              }
            >
              <TabsList className="grid w-full grid-cols-2">
                <TabsTrigger value="exchange">{t("From exchange ID")}</TabsTrigger>
                <TabsTrigger value="synthetic">{t("Synthetic input")}</TabsTrigger>
              </TabsList>

              <TabsContent value="exchange" className="space-y-3 pt-2">
                <div className="flex items-end gap-2">
                  <div className="flex-1 space-y-1">
                    <Label htmlFor="dryrun-ex-id" className="text-xs">
                      {t("Exchange ID")}
                    </Label>
                    <Input
                      id="dryrun-ex-id"
                      className="h-8 font-mono text-xs"
                      value={dryRun.exchangeId}
                      placeholder={t("Paste a recorded exchange ID")}
                      onChange={(e) => dispatch({ type: "dryRunChanged", patch: { exchangeId: e.target.value } })}
                    />
                  </div>
                  <Button
                    type="button"
                    variant="outline"
                    size="sm"
                    className="h-8"
                    disabled={dryRun.loadingExchange || !dryRun.exchangeId.trim()}
                    onClick={() => void loadExchangeIntoDryRun(dryRun.exchangeId)}
                  >
                    {dryRun.loadingExchange && <Loader2Icon className="size-3.5 animate-spin" />}
                    {t("Load exchange")}
                  </Button>
                </div>

                {recentExchanges.length > 0 && (
                  <div className="space-y-1">
                    <Label className="text-xs text-muted-foreground">{t("Or pick a recent captured exchange")}</Label>
                    <Select
                      value=""
                      onValueChange={(id) => {
                        dispatch({ type: "dryRunChanged", patch: { exchangeId: id } });
                        void loadExchangeIntoDryRun(id);
                      }}
                    >
                      <SelectTrigger className="h-8 font-mono text-xs">
                        <SelectValue placeholder={t("Select recent exchange…")} />
                      </SelectTrigger>
                      <SelectContent>
                        {recentExchanges.slice(0, 20).map((ex) => (
                          <SelectItem key={ex.id} value={ex.id} className="font-mono text-xs">
                            {ex.method} {ex.host}
                            {ex.url.replace(/^https?:\/\/[^/]+/, "").slice(0, 48)} ({ex.status})
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </div>
                )}

                {dryRun.exchangeWarning && (
                  <Alert className="border-amber-500/40 bg-amber-500/10 py-2">
                    <AlertTriangleIcon className="size-4 text-amber-500" />
                    <AlertDescription className="text-xs">{dryRun.exchangeWarning}</AlertDescription>
                  </Alert>
                )}
              </TabsContent>

              <TabsContent value="synthetic" className="pt-1">
                <p className="text-xs text-muted-foreground">
                  {t("Edit the synthetic request and response below, then click Run dry-run.")}
                </p>
              </TabsContent>
            </Tabs>

            {/* Editable Request / Response fields (also populated when an exchange is loaded) */}
            <div className="grid grid-cols-1 gap-3 rounded-md border p-3 sm:grid-cols-2">
              <div className="space-y-2">
                <div className="text-xs font-medium">{t("Request")}</div>
                <div className="flex gap-2">
                  <Input
                    className="h-8 w-24 font-mono text-xs"
                    value={dryRun.input.method}
                    onChange={(e) => dispatch({ type: "dryRunInputChanged", patch: { method: e.target.value } })}
                    aria-label={t("Method")}
                  />
                  <Input
                    className="h-8 flex-1 font-mono text-xs"
                    value={dryRun.input.url}
                    onChange={(e) => dispatch({ type: "dryRunInputChanged", patch: { url: e.target.value } })}
                    aria-label={t("URL")}
                  />
                </div>
                <Textarea
                  rows={3}
                  className="font-mono text-xs"
                  value={dryRun.input.reqHeaders}
                  placeholder="Header: value"
                  onChange={(e) => dispatch({ type: "dryRunInputChanged", patch: { reqHeaders: e.target.value } })}
                />
                <Textarea
                  rows={3}
                  className="font-mono text-xs"
                  value={dryRun.input.reqBody}
                  placeholder={t("Request body")}
                  onChange={(e) => dispatch({ type: "dryRunInputChanged", patch: { reqBody: e.target.value } })}
                />
              </div>

              <div className="space-y-2">
                <div className="text-xs font-medium">{t("Response")}</div>
                <Input
                  className="h-8 w-28 font-mono text-xs"
                  type="number"
                  value={dryRun.input.status}
                  onChange={(e) => dispatch({ type: "dryRunInputChanged", patch: { status: e.target.value } })}
                  aria-label={t("Status")}
                />
                <Textarea
                  rows={3}
                  className="font-mono text-xs"
                  value={dryRun.input.respHeaders}
                  placeholder="Header: value"
                  onChange={(e) => dispatch({ type: "dryRunInputChanged", patch: { respHeaders: e.target.value } })}
                />
                <Textarea
                  rows={3}
                  className="font-mono text-xs"
                  value={dryRun.input.respBody}
                  placeholder={t("Response body")}
                  onChange={(e) => dispatch({ type: "dryRunInputChanged", patch: { respBody: e.target.value } })}
                />
              </div>
            </div>

            <div className="flex justify-end">
              <Button type="button" size="sm" onClick={() => void runDryRun()} disabled={dryRun.running}>
                {dryRun.running ? <Loader2Icon className="size-3.5 animate-spin" /> : <PlayIcon className="size-3.5" />}
                {t("Run dry-run")}
              </Button>
            </div>

            {dryRun.error && (
              <Alert variant="destructive">
                <AlertTriangleIcon className="size-4" />
                <AlertDescription className="font-mono text-xs whitespace-pre-wrap">{dryRun.error}</AlertDescription>
              </Alert>
            )}

            {dryRun.result && (
              <div className="space-y-3 border-t pt-3">
                <div className="flex items-center justify-between">
                  <span className="text-sm font-medium">{t("Dry-run result")}</span>
                  <Badge variant={dryRun.result.changed ? "default" : "secondary"}>
                    {dryRun.result.changed ? t("Modified") : t("Unchanged")}
                  </Badge>
                </div>

                {/* Trace lines */}
                <div className="space-y-1.5">
                  <div className="text-xs font-medium text-muted-foreground">
                    {t("Trace lines ({count})", { count: dryRun.result.lines?.length ?? 0 })}
                  </div>
                  {!dryRun.result.lines || dryRun.result.lines.length === 0 ? (
                    <p className="text-xs text-muted-foreground">
                      {t("No trace lines recorded (matcher did not match or hook made no changes).")}
                    </p>
                  ) : (
                    <div className="overflow-x-auto rounded-md border">
                      <Table>
                        <TableHeader>
                          <TableRow>
                            <TableHead className="text-xs">{t("Field")}</TableHead>
                            <TableHead className="text-xs">{t("Outcome")}</TableHead>
                            <TableHead className="text-xs">{t("After / Masked")}</TableHead>
                            <TableHead className="text-xs">{t("Hashes")}</TableHead>
                          </TableRow>
                        </TableHeader>
                        <TableBody>
                          {dryRun.result.lines.map((line, idx) => {
                            const masked = isTraceLineMasked(line);
                            const decodedAfter = decodeTraceAfter(line.After);
                            return (
                              <TableRow key={`${line.Field}-${line.Outcome}-${idx}`}>
                                <TableCell className="font-mono text-xs">
                                  <div>{line.Field || "—"}</div>
                                  <div className="text-[11px] text-muted-foreground">
                                    {line.Phase} · {line.Action}
                                  </div>
                                </TableCell>
                                <TableCell className="text-xs">
                                  <Badge
                                    variant={
                                      line.Outcome === "error"
                                        ? "destructive"
                                        : line.Outcome === "applied"
                                          ? "default"
                                          : "secondary"
                                    }
                                    className="font-mono text-[11px]"
                                  >
                                    {line.Outcome}
                                  </Badge>
                                  {line.Reason && (
                                    <div className="mt-1 font-mono text-[11px] text-muted-foreground">
                                      {line.Reason}
                                    </div>
                                  )}
                                </TableCell>
                                <TableCell className="max-w-xs font-mono text-xs">
                                  {masked ? (
                                    <Badge
                                      variant="outline"
                                      className="border-amber-500/50 text-amber-600 dark:text-amber-400"
                                    >
                                      {t("Masked (HMAC)")}
                                    </Badge>
                                  ) : decodedAfter !== null ? (
                                    <span className="break-all">{decodedAfter.slice(0, 160)}</span>
                                  ) : (
                                    <span className="text-muted-foreground">—</span>
                                  )}
                                </TableCell>
                                <TableCell className="font-mono text-[11px] text-muted-foreground">
                                  {line.BeforeHash && <div>{line.BeforeHash.slice(0, 12)}</div>}
                                  {line.AfterHash && <div>→ {line.AfterHash.slice(0, 12)}</div>}
                                </TableCell>
                              </TableRow>
                            );
                          })}
                        </TableBody>
                      </Table>
                    </div>
                  )}
                </div>

                {/* Output preview */}
                <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <div className="space-y-1">
                    <div className="text-xs font-medium text-muted-foreground">{t("Resulting request")}</div>
                    <pre className="max-h-48 overflow-auto rounded-md border bg-muted/40 p-2 font-mono text-xs">
                      {`${dryRun.result.request.method} ${dryRun.result.request.url}\n${formatHeaderLines(dryRun.result.request.headers)}${dryRun.result.request.body ? `\n\n${dryRun.result.request.body}` : ""}`}
                    </pre>
                  </div>
                  <div className="space-y-1">
                    <div className="text-xs font-medium text-muted-foreground">{t("Resulting response")}</div>
                    <pre className="max-h-48 overflow-auto rounded-md border bg-muted/40 p-2 font-mono text-xs">
                      {`HTTP ${dryRun.result.response.status}\n${formatHeaderLines(dryRun.result.response.headers)}${dryRun.result.response.body ? `\n\n${dryRun.result.response.body}` : ""}`}
                    </pre>
                  </div>
                </div>
              </div>
            )}
          </div>
        </SheetContent>
      </Sheet>
    </div>
  );
}

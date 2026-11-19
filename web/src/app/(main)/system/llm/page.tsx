"use client";

import * as React from "react";

import {
  Loader2Icon,
  PlugZapIcon,
  PlusIcon,
  RefreshCwIcon,
  RotateCcwIcon,
  SaveIcon,
  StarIcon,
  Trash2Icon,
  ZapIcon,
} from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { api } from "@/lib/api";
import { setVisibleInterval } from "@/lib/polling";
import type { LLMPoolMember, LLMPoolStatus, LLMProfile, LLMRetryOverride } from "@/lib/types";
import { cn } from "@/lib/utils";

import { ProfileRetryFields, RetryPolicyPanel, ZERO_OVERRIDE } from "./_components/retry";

// The thinking switch (thinking.type) and the thinking effort (reasoning_effort) are two [independent] fields,
// each set on its own — some APIs have no thinking field and activate thinking via the effort parameter alone, hence the decoupling.
// An empty string in storage = the field is [not sent]; Radix Select rejects an empty value, so the UI uses "none"
// as the sentinel for not sent, converted to and from "" on load and save (NONE / fromStore / toStore).
const NONE = "none";
const fromStore = (v?: string) => (v ? v : NONE);
const toStore = (v: string) => (v === NONE ? "" : v);
const THINKING_TYPES: { value: string }[] = [{ value: NONE }, { value: "disabled" }, { value: "enabled" }];
// Which request field name carries the output cap (only meaningful for the openai format). NONE ↔ "" shares the same sentinel conversion.
const MAX_TOKENS_FIELDS: { value: string }[] = [{ value: NONE }, { value: "max_completion_tokens" }];
// LLM profile formats. The default in the create form is "anthropic"; "ollama" is the
// keyless local-server format (base URL defaults to http://127.0.0.1:11434/v1).
type ProfileFormat = "anthropic" | "openai" | "openai-responses" | "ollama";
const OLLAMA_DEFAULT_BASE_URL = "http://127.0.0.1:11434/v1";
const EFFORT_LEVELS: { value: string }[] = [
  { value: NONE },
  { value: "low" },
  { value: "medium" },
  { value: "high" },
  { value: "xhigh" },
  { value: "max" },
];

function cooldownText(secs: number) {
  if (secs <= 0) return "";
  if (secs < 60) return `${secs}s`;
  return `${Math.ceil(secs / 60)}min`;
}

// The "healthy or not" badge a profile shows on its card. A profile without a Key cannot send any request at all — say that before the breaker;
// the exception is Ollama, a keyless local server that IS callable without a key. The other states come from the pool's breaker
// records (with polling off no new records appear, so "healthy" = no known failure). Labels/hints are i18n keys resolved at the call site.
type Health = {
  labelKey: string;
  labelParams?: Record<string, string | number>;
  cls: string;
  hint?: string;
  hintKey?: string;
};
function healthOf(p: LLMProfile, m?: LLMPoolMember): Health {
  if (!p.api_key_hint && p.format !== "ollama") {
    return {
      labelKey: "page.health.noKey",
      cls: "border-muted-foreground/40 text-muted-foreground",
      hintKey: "page.health.noKeyHint",
    };
  }
  if (m?.state === "tripped") {
    return m.cooldown_secs > 0
      ? {
          labelKey: "page.health.trippedCooldown",
          labelParams: { cooldown: cooldownText(m.cooldown_secs) },
          cls: "border-destructive/50 text-destructive",
          hint: m.last_error,
        }
      : {
          labelKey: "page.health.tripped",
          cls: "border-destructive/50 text-destructive",
          hint: m.last_error,
        };
  }
  if (m?.state === "degraded") {
    return {
      labelKey: "page.health.degraded",
      labelParams: { fails: m.fails },
      cls: "border-amber-500/50 text-amber-600 dark:text-amber-400",
      hint: m.last_error,
    };
  }
  return { labelKey: "page.health.healthy", cls: "border-emerald-500/50 text-emerald-600 dark:text-emerald-400" };
}

// ─────────────────────────────────────────────────────────────────────────────
// Pool settings drawer
// ─────────────────────────────────────────────────────────────────────────────

function PoolSheet({
  open,
  onOpenChange,
  pool,
  onReload,
}: {
  open: boolean;
  onOpenChange: (o: boolean) => void;
  pool: LLMPoolStatus | null;
  onReload: () => Promise<void>;
}) {
  const t = useTranslations("llm");
  const [busy, setBusy] = React.useState(false);

  // The cooldown countdown is the remaining seconds computed by the backend — fetched on a timer only while the drawer is open and some profile is unhealthy, to keep it ticking.
  React.useEffect(() => {
    if (!open || !pool?.enabled || !pool.chain.some((m) => m.state !== "ok")) return;
    const stopPolling = setVisibleInterval(() => void onReload(), 10_000);
    return stopPolling;
  }, [open, pool, onReload]);

  async function toggle(patch: { llm_pool_enabled?: boolean; llm_pool_bind_fallback?: boolean }) {
    if (busy) return;
    setBusy(true);
    try {
      await api.setSettings(patch);
      await onReload();
      if (patch.llm_pool_enabled !== undefined) {
        toast.success(patch.llm_pool_enabled ? t("page.pool.enabledToast") : t("page.pool.disabledToast"));
      } else {
        toast.success(t("page.pool.fallbackUpdated"));
      }
    } catch (e) {
      toast.error(t("page.pool.applyFailed", { message: (e as Error).message }));
    } finally {
      setBusy(false);
    }
  }

  async function recover(id?: string) {
    try {
      await api.resetLLMPool(id);
      await onReload();
      toast.success(id ? t("page.pool.profileRecovered") : t("page.pool.allRecovered"));
    } catch (e) {
      toast.error(t("page.pool.recoveryFailed", { message: (e as Error).message }));
    }
  }

  const enabled = pool?.enabled ?? false;
  const chain = pool?.chain ?? [];
  // Members taking part in the pool (excluding those flagged "excluded from pool"), in the exact order the backend tries them.
  const inChain = chain.filter((m) => m.active || !m.excluded);
  const tripped = chain.filter((m) => m.state === "tripped");

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="right" className="flex flex-col gap-0 p-0 data-[side=right]:sm:max-w-lg">
        <SheetHeader className="px-4">
          <SheetTitle className="flex items-center gap-2">
            <ZapIcon className="size-4" /> {t("page.pool.title")}
          </SheetTitle>
          <SheetDescription>{t.rich("page.pool.description", { b: (c) => <b>{c}</b> })}</SheetDescription>
        </SheetHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-6">
          <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
            <div className="grid gap-0.5">
              <Label className="text-sm">{t("page.pool.enable")}</Label>
              <p className="text-muted-foreground text-xs">{t("page.pool.enableHint")}</p>
            </div>
            <Switch
              checked={enabled}
              disabled={busy}
              onCheckedChange={(v) => void toggle({ llm_pool_enabled: v })}
              aria-label={t("page.pool.enableAria")}
            />
          </div>

          {enabled && (
            <>
              <div className="flex items-center justify-between gap-4 rounded-lg border p-3">
                <div className="grid gap-0.5">
                  <Label className="text-sm">{t("page.pool.bindFallback")}</Label>
                  <p className="text-muted-foreground text-xs">{t("page.pool.bindFallbackHint")}</p>
                </div>
                <Switch
                  checked={pool?.bind_fallback ?? false}
                  disabled={busy}
                  onCheckedChange={(v) => void toggle({ llm_pool_bind_fallback: v })}
                  aria-label={t("page.pool.bindFallbackAria")}
                />
              </div>

              <Separator />

              <div className="grid gap-2">
                <div className="flex items-center justify-between">
                  <Label className="text-sm">{t("page.pool.order")}</Label>
                  {tripped.length > 0 && (
                    <Button size="sm" variant="ghost" onClick={() => void recover()}>
                      <RotateCcwIcon /> {t("page.pool.recoverAll")}
                    </Button>
                  )}
                </div>
                {inChain.length < 2 && (
                  <p className="text-muted-foreground text-xs">{t("page.pool.notEnough", { count: inChain.length })}</p>
                )}
                {chain.map((m) => {
                  const excluded = m.excluded && !m.active;
                  const order = excluded ? null : inChain.findIndex((x) => x.profile_id === m.profile_id) + 1;
                  return (
                    <div
                      key={m.profile_id}
                      className={cn(
                        "grid gap-1 rounded-lg border p-2.5 text-sm",
                        excluded && "opacity-55",
                        m.state === "tripped" && "border-destructive/40",
                      )}
                    >
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <span className="w-5 shrink-0 text-center font-mono text-muted-foreground text-xs">
                          {order ?? "—"}
                        </span>
                        <span className="font-medium">{m.name}</span>
                        {m.active && (
                          <Badge variant="outline" className="border-amber-400/50 text-amber-500">
                            {t("page.pool.active")}
                          </Badge>
                        )}
                        {excluded && <Badge variant="outline">{t("page.pool.excluded")}</Badge>}
                        <div className="ml-auto flex items-center gap-2">
                          {m.state === "tripped" && m.cooldown_secs > 0 && (
                            <span className="text-muted-foreground text-xs">
                              {t("page.pool.cooldown", { value: cooldownText(m.cooldown_secs) })}
                            </span>
                          )}
                          {m.state === "degraded" && (
                            <span className="text-muted-foreground text-xs">
                              {t("page.pool.consecutiveFailures", { count: m.fails })}
                            </span>
                          )}
                          {m.state !== "ok" && (
                            <Button
                              size="icon"
                              variant="ghost"
                              className="size-7"
                              aria-label={t("page.pool.recoverNowAria")}
                              title={t("page.pool.recoverNowTitle")}
                              onClick={() => void recover(m.profile_id)}
                            >
                              <RotateCcwIcon className="size-3.5" />
                            </Button>
                          )}
                        </div>
                      </div>
                      <div className="flex flex-wrap items-center gap-x-3 pl-7 text-muted-foreground text-xs">
                        <code className="truncate font-mono">{m.model}</code>
                        {!m.active && <span>{t("page.pool.priority", { priority: m.priority })}</span>}
                      </div>
                      {m.last_error && (
                        <p className="truncate pl-7 font-mono text-muted-foreground text-xs" title={m.last_error}>
                          {m.last_error}
                        </p>
                      )}
                    </div>
                  );
                })}
                {chain.length === 0 && (
                  <div className="rounded-lg border border-dashed p-4 text-center text-muted-foreground text-sm">
                    {t("page.pool.noProfiles")}
                  </div>
                )}
              </div>

              <div className="rounded-lg border border-dashed p-3 text-muted-foreground text-xs leading-relaxed">
                {t("page.pool.explanation")}
              </div>
            </>
          )}
        </div>
      </SheetContent>
    </Sheet>
  );
}

// ─────────────────────────────────────────────────────────────────────────────
// Model profile drawer (create / edit share the same form)
// ─────────────────────────────────────────────────────────────────────────────

function ProfileSheet({
  profile,
  open,
  onOpenChange,
  onSaved,
}: {
  profile: LLMProfile | null; // null = create
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onSaved: (id: string) => void;
}) {
  const t = useTranslations("llm");
  const isNew = !profile;
  const [name, setName] = React.useState("");
  const [format, setFormat] = React.useState<ProfileFormat>("anthropic");
  const [model, setModel] = React.useState("");
  const [baseUrl, setBaseUrl] = React.useState("");
  const [proxy, setProxy] = React.useState("");
  const [apiKey, setApiKey] = React.useState("");
  const [keyHint, setKeyHint] = React.useState("");
  const [rps, setRps] = React.useState("0");
  const [rpm, setRpm] = React.useState("0");
  const [cw, setCw] = React.useState("0"); // context window (K tokens); 0 = default 200K
  const [thinkingType, setThinkingType] = React.useState(NONE);
  const [effort, setEffort] = React.useState(NONE);
  const [priority, setPriority] = React.useState("0"); // pool order; higher = tried earlier
  const [poolExclude, setPoolExclude] = React.useState(false);
  const [streaming, setStreaming] = React.useState(true); // true = streaming (default); false = non-streaming
  const [maxTokens, setMaxTokens] = React.useState("0"); // per-reply output cap; 0 = not sent
  const [maxTokensField, setMaxTokensField] = React.useState(NONE); // which field name carries the cap; NONE = max_tokens
  const [sessionHeaderKey, setSessionHeaderKey] = React.useState(""); // custom session header name; empty = not sent
  const [userAgent, setUserAgent] = React.useState(""); // custom User-Agent; empty = keep the default UA
  const [retry, setRetry] = React.useState<LLMRetryOverride>(ZERO_OVERRIDE); // retry override for this profile; all 0 = follow the global
  const [testing, setTesting] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [models, setModels] = React.useState<string[]>([]);
  const [loadingModels, setLoadingModels] = React.useState(false);
  const [modelsOpen, setModelsOpen] = React.useState(false);
  const [modelQuery, setModelQuery] = React.useState(""); // filter text for the loaded model list; reset on open/close

  // Every time it opens, the form is re-filled from the passed-in profile (reset to defaults when creating). Closing and reopening
  // the drawer is a clean start, leaving no leftovers from the previous profile.
  React.useEffect(() => {
    if (!open) return;
    setName(profile?.name ?? "");
    setFormat(
      profile?.format === "openai" || profile?.format === "openai-responses" || profile?.format === "ollama"
        ? profile.format
        : "anthropic",
    );
    setModel(profile?.model ?? "");
    setBaseUrl(profile?.base_url ?? "");
    setProxy(profile?.proxy ?? "");
    setRps(String(profile?.rate_per_second ?? 0));
    setRpm(String(profile?.rate_per_minute ?? 0));
    setCw(String(profile?.context_window_k ?? 0));
    setThinkingType(fromStore(profile?.thinking_type));
    setEffort(fromStore(profile?.reasoning_effort));
    setPriority(String(profile?.priority ?? 0));
    setPoolExclude(profile?.pool_exclude ?? false);
    setStreaming(profile?.streaming ?? true);
    setMaxTokens(String(profile?.max_tokens ?? 0));
    setMaxTokensField(fromStore(profile?.max_tokens_field));
    setSessionHeaderKey(profile?.session_header_key ?? "");
    setUserAgent(profile?.user_agent ?? "");
    setRetry(profile?.retry ?? ZERO_OVERRIDE);
    setApiKey("");
    setKeyHint(profile?.api_key_hint ?? "");
    setModels([]);
    setModelsOpen(false);
    setModelQuery("");
  }, [open, profile]);

  const profileId = profile ? Number(profile.id) : undefined;

  // Case-insensitive substring filter, so a provider prefix ("deepseek/") or a
  // fragment ("flash") is enough to narrow a 400-entry list.
  const filteredModels = React.useMemo(() => {
    const q = modelQuery.trim().toLowerCase();
    if (!q) return models;
    return models.filter((m) => m.toLowerCase().includes(q));
  }, [models, modelQuery]);

  async function loadModels() {
    if (loadingModels) return;
    setLoadingModels(true);
    setModels([]);
    setModelQuery("");
    try {
      const r = await api.fetchLLMModels(format, baseUrl, apiKey, proxy, profileId);
      if (r.ok && r.models && r.models.length > 0) {
        setModels(r.models);
        setModelsOpen(true);
        toast.success(t("page.profile.modelsLoaded", { count: r.models.length }));
      } else {
        toast.error(t("page.profile.loadModelsFailed", { error: r.error ?? t("page.profile.noModelsReturned") }));
      }
    } catch (e) {
      toast.error(t("page.profile.loadModelsError", { message: (e as Error).message }));
    } finally {
      setLoadingModels(false);
    }
  }

  async function testConnection() {
    if (testing) return;
    setTesting(true);
    try {
      // Test with the thinking parameters the profile would actually run, so a model that does not support the field fails here,
      // not when a task is already running. Pass the profile id: with the Key input left empty, the stored Key is used.
      const r = await api.testLLM(
        format,
        model,
        baseUrl,
        apiKey,
        proxy,
        toStore(thinkingType),
        toStore(effort),
        profileId,
        streaming,
        sessionHeaderKey.trim(),
        userAgent.trim(),
      );
      // Show the reply too: only when the model demonstrably spoke is this the same as working inside a session.
      if (r.ok)
        toast.success(t("page.profile.connected", { latency: r.latency_ms ?? "?", model: r.model ?? model }), {
          description: r.reply ? t("page.profile.replyDescription", { reply: r.reply }) : undefined,
        });
      else toast.error(t("page.profile.connectionFailed", { error: r.error ?? t("page.profile.unknown") }));
    } catch (e) {
      toast.error(t("page.profile.testError", { message: (e as Error).message }));
    } finally {
      setTesting(false);
    }
  }

  async function save() {
    if (!name.trim() || !model.trim()) {
      toast.error(t("page.profile.fillNameModel"));
      return;
    }
    if (saving) return;
    setSaving(true);
    try {
      const { id } = await api.saveLLMProfile({
        ...(profile ? { id: Number(profile.id) } : {}),
        name: name.trim(),
        format,
        model: model.trim(),
        base_url: baseUrl.trim(),
        proxy: proxy.trim(),
        api_key: apiKey,
        rate_per_second: Number(rps) || 0,
        rate_per_minute: Number(rpm) || 0,
        context_window_k: Number(cw) || 0,
        thinking_type: toStore(thinkingType),
        reasoning_effort: toStore(effort),
        priority: Number(priority) || 0,
        pool_exclude: poolExclude,
        streaming,
        max_tokens: Math.max(0, Number(maxTokens) || 0),
        // The field-name switch only matters for openai (Chat Completions); every other format falls back to the default,
        // and the backend re-applies the same normalization; this just keeps the UI from sending contradictory values.
        max_tokens_field: format === "openai" ? toStore(maxTokensField) : "",
        session_header_key: sessionHeaderKey.trim(),
        user_agent: userAgent.trim(),
        retry,
      });
      if (isNew) toast.success(t("page.profile.created", { name: name.trim() }));
      else toast.success(profile?.is_default ? t("page.profile.savedActive") : t("page.profile.saved"));
      onSaved(String(id));
      onOpenChange(false);
    } catch (e) {
      toast.error(t("page.profile.saveFailed", { message: (e as Error).message }));
    } finally {
      setSaving(false);
    }
  }

  let keyPlaceholder = "sk-…";
  if (format === "ollama") {
    keyPlaceholder = t("page.profile.keyOllamaPlaceholder");
  } else if (keyHint) {
    keyPlaceholder = t("page.profile.keySetPlaceholder", { hint: keyHint });
  }

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="flex flex-col gap-0 p-0 data-[side=right]:min-w-[420px] data-[side=right]:sm:max-w-xl"
      >
        <SheetHeader className="px-4">
          <SheetTitle className="flex items-center gap-2">
            {isNew ? t("page.profile.titleNew") : t("page.profile.titleEdit", { name: profile?.name ?? "" })}
            {profile?.is_default && (
              <Badge variant="outline" className="border-amber-400/50 text-amber-500">
                {t("page.profile.active")}
              </Badge>
            )}
          </SheetTitle>
          <SheetDescription>{isNew ? t("page.profile.descNew") : t("page.profile.descEdit")}</SheetDescription>
        </SheetHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto px-4 pb-4">
          <div className="grid gap-4 sm:grid-cols-2">
            <div className="grid gap-2">
              <Label htmlFor="p-name">{t("page.profile.name")}</Label>
              <Input
                id="p-name"
                placeholder={t("page.profile.namePlaceholder")}
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label>{t("page.profile.format")}</Label>
              <Select value={format} onValueChange={(v) => setFormat(v as ProfileFormat)}>
                <SelectTrigger>
                  <SelectValue placeholder={t("page.profile.formatPlaceholder")} />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="anthropic">Anthropic</SelectItem>
                  <SelectItem value="openai">OpenAI (Chat Completions)</SelectItem>
                  <SelectItem value="openai-responses">OpenAI (Responses API)</SelectItem>
                  <SelectItem value="ollama">{t("page.profile.formatOllama")}</SelectItem>
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-model">{t("page.profile.model")}</Label>
            <div className="flex gap-2">
              <Input
                id="p-model"
                className="font-mono"
                placeholder={format === "ollama" ? "llama3.2" : "claude-opus-4-8"}
                value={model}
                onChange={(e) => setModel(e.target.value)}
              />
              {/* modal: this Popover's content is portaled to <body>, outside the Sheet's scroll lock,
                  so without modal the list renders but cannot scroll. modal lets it hold the topmost scroll lock itself. */}
              <Popover
                open={modelsOpen}
                onOpenChange={(o) => {
                  setModelsOpen(o);
                  if (!o) setModelQuery("");
                }}
                modal
              >
                <PopoverTrigger asChild>
                  <Button
                    type="button"
                    variant="outline"
                    size="icon"
                    className="shrink-0"
                    disabled={loadingModels}
                    onClick={loadModels}
                    title={t("page.profile.loadModelsTitle")}
                  >
                    {loadingModels ? <Loader2Icon className="animate-spin" /> : <RefreshCwIcon />}
                  </Button>
                </PopoverTrigger>
                {models.length > 0 && (
                  <PopoverContent className="w-80 gap-0 p-0" align="end">
                    <div className="border-b p-1">
                      <Input
                        autoFocus
                        className="h-8 border-0 shadow-none focus-visible:ring-0 dark:bg-transparent"
                        placeholder={t("page.profile.searchModelsPlaceholder", { count: models.length })}
                        value={modelQuery}
                        onChange={(e) => setModelQuery(e.target.value)}
                        spellCheck={false}
                        aria-label={t("page.profile.searchModelsAria")}
                      />
                    </div>
                    <div className="max-h-64 overflow-y-auto overscroll-contain p-1">
                      {filteredModels.length === 0 ? (
                        <p className="px-2 py-6 text-center text-muted-foreground text-xs">
                          {t("page.profile.noModelMatch", { query: modelQuery.trim() })}
                        </p>
                      ) : (
                        filteredModels.map((m) => (
                          <button
                            key={m}
                            type="button"
                            className="w-full shrink-0 rounded-md px-2 py-1.5 text-left font-mono text-xs hover:bg-accent hover:text-accent-foreground"
                            onClick={() => {
                              setModel(m);
                              setModelsOpen(false);
                            }}
                          >
                            {m}
                          </button>
                        ))
                      )}
                    </div>
                  </PopoverContent>
                )}
              </Popover>
            </div>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-base-url">{t("page.profile.baseUrl")}</Label>
            <Input
              id="p-base-url"
              className="font-mono"
              placeholder={format === "ollama" ? OLLAMA_DEFAULT_BASE_URL : "https://api.openai.com/v1"}
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
            />
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-proxy">{t("page.profile.proxy")}</Label>
            <Input
              id="p-proxy"
              className="font-mono"
              placeholder="socks5://user:pass@127.0.0.1:1080 · http://127.0.0.1:8080"
              value={proxy}
              onChange={(e) => setProxy(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">{t("page.profile.proxyHint")}</p>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-session-header">{t("page.profile.sessionHeader")}</Label>
            <Input
              id="p-session-header"
              className="font-mono"
              placeholder={t("page.profile.sessionHeaderPlaceholder")}
              value={sessionHeaderKey}
              onChange={(e) => setSessionHeaderKey(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">
              {t.rich("page.profile.sessionHeaderHint", { b: (c) => <b>{c}</b> })}
            </p>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-user-agent">{t("page.profile.userAgent")}</Label>
            <Input
              id="p-user-agent"
              className="font-mono"
              placeholder={t("page.profile.userAgentPlaceholder")}
              value={userAgent}
              onChange={(e) => setUserAgent(e.target.value)}
            />
            <p className="text-muted-foreground text-xs">
              {t.rich("page.profile.userAgentHint", { b: (c) => <b>{c}</b> })}
            </p>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="p-api-key">
              {format === "ollama" ? t("page.profile.apiKeyOllama") : t("page.profile.apiKey")}
            </Label>
            <Input
              id="p-api-key"
              type="password"
              placeholder={keyPlaceholder}
              value={apiKey}
              onChange={(e) => setApiKey(e.target.value)}
              // A masked field with no autocomplete hint looks like this site's
              // login password: password managers fill it and Save persists the
              // wrong secret over the stored key. new-password keeps them out.
              autoComplete="new-password"
              spellCheck={false}
              data-1p-ignore="true"
              data-lpignore="true"
            />
          </div>

          <div className="grid gap-4 sm:grid-cols-3">
            <div className="grid gap-2">
              <Label htmlFor="p-rps">{t("page.profile.rps")}</Label>
              <Input id="p-rps" type="number" min={0} value={rps} onChange={(e) => setRps(e.target.value)} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="p-rpm">{t("page.profile.rpm")}</Label>
              <Input id="p-rpm" type="number" min={0} value={rpm} onChange={(e) => setRpm(e.target.value)} />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="p-cw">{t("page.profile.contextWindow")}</Label>
              <Input
                id="p-cw"
                type="number"
                min={0}
                max={1000}
                value={cw}
                onChange={(e) => setCw(e.target.value)}
                placeholder="200"
              />
            </div>
          </div>
          <p className="-mt-2 text-muted-foreground text-xs">{t("page.profile.rateHint")}</p>

          <div className="grid gap-3 rounded-lg border p-3">
            <div className="flex items-center justify-between gap-4">
              <div className="grid gap-0.5">
                <Label htmlFor="p-priority" className="text-sm">
                  {t("page.profile.poolPriority")}
                </Label>
                <p className="text-muted-foreground text-xs">{t("page.profile.poolPriorityHint")}</p>
              </div>
              <Input
                id="p-priority"
                type="number"
                className="w-24 shrink-0"
                value={priority}
                onChange={(e) => setPriority(e.target.value)}
              />
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">{t("page.profile.excluded")}</Label>
                <p className="text-muted-foreground text-xs">{t("page.profile.excludedHint")}</p>
              </div>
              <Switch
                checked={poolExclude}
                onCheckedChange={setPoolExclude}
                aria-label={t("page.profile.excludedAria")}
              />
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">{t("page.profile.streaming")}</Label>
                <p className="text-muted-foreground text-xs">{t("page.profile.streamingHint")}</p>
              </div>
              <Switch checked={streaming} onCheckedChange={setStreaming} aria-label={t("page.profile.streamingAria")} />
            </div>
          </div>

          <div className="grid gap-3 rounded-lg border p-3">
            <div className="flex items-center justify-between gap-4">
              <div className="grid gap-0.5">
                <Label htmlFor="p-max-tokens" className="text-sm">
                  {t("page.profile.outputCap")}
                </Label>
                <p className="text-muted-foreground text-xs">{t("page.profile.outputCapHint")}</p>
              </div>
              <Input
                id="p-max-tokens"
                type="number"
                min={0}
                className="w-28 shrink-0"
                value={maxTokens}
                onChange={(e) => setMaxTokens(e.target.value)}
                placeholder="0"
              />
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">{t("page.profile.capField")}</Label>
                <p className="text-muted-foreground text-xs">{t(`page.profile.capFieldHint.${format}`)}</p>
              </div>
              <Select
                value={format === "openai" ? maxTokensField : NONE}
                onValueChange={setMaxTokensField}
                disabled={format !== "openai"}
              >
                <SelectTrigger className="w-56 shrink-0">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {MAX_TOKENS_FIELDS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.value === NONE ? t("page.profile.maxTokensDefault") : o.value}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <div className="grid gap-3 rounded-lg border p-3">
            <div className="flex items-center justify-between gap-4">
              <div className="grid gap-0.5">
                <Label className="text-sm">{t("page.profile.thinkingSwitch")}</Label>
                <p className="text-muted-foreground text-xs">{t("page.profile.thinkingSwitchHint")}</p>
              </div>
              <Select value={thinkingType} onValueChange={setThinkingType}>
                <SelectTrigger className="w-32 shrink-0">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {THINKING_TYPES.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {t(`page.profile.thinkingType.${o.value}`)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex items-center justify-between gap-4 border-t pt-3">
              <div className="grid gap-0.5">
                <Label className="text-sm">{t("page.profile.thinkingEffort")}</Label>
                <p className="text-muted-foreground text-xs">{t("page.profile.thinkingEffortHint")}</p>
              </div>
              <Select value={effort} onValueChange={setEffort}>
                <SelectTrigger className="w-32 shrink-0">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {EFFORT_LEVELS.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.value === NONE ? t("page.profile.effortNotSent") : o.value}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>

          <ProfileRetryFields value={retry} onChange={setRetry} />
        </div>

        <div className="flex gap-2 border-t px-4 py-3">
          <Button variant="outline" onClick={testConnection} disabled={testing}>
            {testing ? <Loader2Icon className="animate-spin" /> : <PlugZapIcon />}
            {testing ? t("page.profile.testing") : t("page.profile.testConnection")}
          </Button>
          <Button onClick={save} disabled={saving} className="flex-1">
            {saving && <Loader2Icon className="animate-spin" />}
            {!saving && (isNew ? <PlusIcon /> : <SaveIcon />)}
            {isNew ? t("page.profile.createBtn") : t("page.profile.saveBtn")}
          </Button>
        </div>
      </SheetContent>
    </Sheet>
  );
}

// ─────────────────────────────────────────────────────────────────────────────

export default function LLMPage() {
  const t = useTranslations("llm");
  const [profiles, setProfiles] = React.useState<LLMProfile[]>([]);
  const [pool, setPool] = React.useState<LLMPoolStatus | null>(null);
  const [poolOpen, setPoolOpen] = React.useState(false);
  // The drawer's open flag and its content are stored separately: editing stays put on close, otherwise during the closing animation the title would flash
  // from "Edit X" to "New". editing = null means create.
  const [editOpen, setEditOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<LLMProfile | null>(null);
  const openEditor = React.useCallback((p: LLMProfile | null) => {
    setEditing(p);
    setEditOpen(true);
  }, []);

  const loadPool = React.useCallback(async () => {
    try {
      setPool(await api.llmPool());
    } catch {
      /* ignore */
    }
  }, []);

  const load = React.useCallback(async () => {
    try {
      setProfiles(await api.llmProfiles());
    } catch {
      /* ignore */
    }
    await loadPool();
  }, [loadPool]);

  React.useEffect(() => {
    void load();
  }, [load]);

  // The health badge on each card reads pool state by profile id.
  const health = React.useMemo(() => {
    const m = new Map<string, LLMPoolMember>();
    for (const c of pool?.chain ?? []) m.set(c.profile_id, c);
    return m;
  }, [pool]);

  async function activate(id: string, name: string) {
    try {
      await api.activateLLMProfile(id);
      toast.success(t("page.toasts.activated", { name }));
      await load();
    } catch (e) {
      toast.error(t("page.toasts.activationFailed", { message: (e as Error).message }));
    }
  }

  async function remove(p: LLMProfile) {
    if (p.is_default) {
      toast.error(t("page.toasts.cannotDeleteActive"));
      return;
    }
    try {
      await api.deleteLLMProfile(p.id);
      toast.success(t("page.toasts.deleted", { name: p.name }));
      await load();
    } catch (e) {
      toast.error(t("page.toasts.deleteFailed", { message: (e as Error).message }));
    }
  }

  const poolOn = pool?.enabled ?? false;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h1 className="font-semibold text-xl tracking-tight">LLM</h1>
          <p className="text-muted-foreground text-sm">{t("page.subtitle")}</p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => setPoolOpen(true)}>
            <ZapIcon /> {t("page.poolSettings")}
            {poolOn && (
              <Badge variant="outline" className="ml-1 border-emerald-500/50 text-emerald-600 dark:text-emerald-400">
                {t("page.poolOn")}
              </Badge>
            )}
          </Button>
          <Button size="sm" variant="outline" onClick={() => openEditor(null)}>
            <PlusIcon /> {t("page.create")}
          </Button>
        </div>
      </div>

      <Tabs defaultValue="profiles" className="flex-1">
        <TabsList>
          <TabsTrigger value="profiles">{t("page.tabs.profiles")}</TabsTrigger>
          <TabsTrigger value="retry">{t("page.tabs.retry")}</TabsTrigger>
        </TabsList>

        <TabsContent value="profiles" className="mt-4">
          <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
            {profiles.map((p) => {
              const h = healthOf(p, health.get(p.id));
              return (
                // biome-ignore lint/a11y/useSemanticElements: the card contains its own action buttons; a native <button> would nest buttons (invalid HTML)
                <Card
                  key={p.id}
                  role="button"
                  tabIndex={0}
                  onClick={() => openEditor(p)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      openEditor(p);
                    }
                  }}
                  className={cn(
                    "cursor-pointer gap-0 py-4 outline-none transition-colors hover:border-foreground/30",
                    p.is_default && "border-amber-400/50 bg-amber-400/5",
                  )}
                >
                  <CardContent className="grid gap-2 px-4">
                    <div className="flex items-start gap-2">
                      <StarIcon
                        className={cn(
                          "mt-0.5 size-4 shrink-0",
                          p.is_default ? "fill-amber-400 text-amber-400" : "text-muted-foreground",
                        )}
                      />
                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="truncate font-medium text-sm">{p.name}</span>
                          <Badge variant="outline" className="uppercase">
                            {p.format}
                          </Badge>
                          <Badge
                            variant="outline"
                            className={cn("ml-auto", h.cls)}
                            title={h.hint ?? (h.hintKey ? t(h.hintKey) : undefined)}
                          >
                            {h.labelParams ? t(h.labelKey, h.labelParams) : t(h.labelKey)}
                          </Badge>
                        </div>
                        <code className="mt-1 block truncate font-mono text-muted-foreground text-xs">{p.model}</code>
                      </div>
                    </div>

                    <div className="flex flex-wrap gap-x-3 gap-y-0.5 pl-6 text-muted-foreground text-xs">
                      {p.api_key_hint && <span>{p.api_key_hint}</span>}
                      <span>
                        {p.rate_per_second}/s · {p.rate_per_minute}/min
                      </span>
                      {p.proxy && <span className="truncate">{t("page.card.proxy", { proxy: p.proxy })}</span>}
                      {p.reasoning_effort && (
                        <span>
                          {t("page.card.thinking", {
                            effort: p.reasoning_effort === "off" ? "off" : p.reasoning_effort,
                          })}
                        </span>
                      )}
                      {/* The two pool-related fields only matter when the pool is on; when it is off they take no space */}
                      {poolOn &&
                        !p.is_default &&
                        (p.pool_exclude ? (
                          <span>{t("page.card.excludedFromPool")}</span>
                        ) : (
                          <span>{t("page.card.priority", { priority: p.priority ?? 0 })}</span>
                        ))}
                    </div>

                    <div className="mt-1 flex gap-2">
                      <Button
                        size="sm"
                        variant="outline"
                        className="flex-1"
                        disabled={p.is_default}
                        onClick={(e) => {
                          e.stopPropagation();
                          void activate(p.id, p.name);
                        }}
                      >
                        {p.is_default ? t("page.card.active") : t("page.card.setAsActive")}
                      </Button>
                      <Button
                        size="icon"
                        variant="outline"
                        aria-label={t("page.card.deleteAria")}
                        onClick={(e) => {
                          e.stopPropagation();
                          void remove(p);
                        }}
                      >
                        <Trash2Icon className="text-destructive" />
                      </Button>
                    </div>
                  </CardContent>
                </Card>
              );
            })}
            {profiles.length === 0 && (
              <div className="col-span-full rounded-lg border border-dashed p-10 text-center text-muted-foreground text-sm">
                {t("page.empty")}
              </div>
            )}
          </div>
        </TabsContent>

        <TabsContent value="retry" className="mt-4">
          <RetryPolicyPanel />
        </TabsContent>
      </Tabs>

      <ProfileSheet profile={editing} open={editOpen} onOpenChange={setEditOpen} onSaved={() => void load()} />
      <PoolSheet open={poolOpen} onOpenChange={setPoolOpen} pool={pool} onReload={loadPool} />
    </div>
  );
}

"use client";

import * as React from "react";

import { CpuIcon, FlaskConicalIcon, KeyboardIcon, RadioTowerIcon, SearchIcon, ShieldAlertIcon } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { api } from "@/lib/api";
import { CHAT_SEND_MODE_OPTIONS, type ChatSendMode, setChatSendMode, useChatSendMode } from "@/lib/chat-send-mode";
import type { Settings } from "@/lib/types";

import { NoiseProfileCard } from "./noise-profile-card";

export default function SystemSettingsPage() {
  const t = useTranslations("settings");
  const [trafficCapture, setTrafficCapture] = React.useState(false);
  const [agentTrafficBinding, setAgentTrafficBinding] = React.useState(false);
  const [webSearch, setWebSearch] = React.useState(false);
  const [backend, setBackend] = React.useState("ddgs");
  const [braveKeySet, setBraveKeySet] = React.useState(false);
  const [braveKeyInput, setBraveKeyInput] = React.useState("");
  const [tavilyKeySet, setTavilyKeySet] = React.useState(false);
  const [tavilyKeyInput, setTavilyKeyInput] = React.useState("");
  const [savingTavilyKey, setSavingTavilyKey] = React.useState(false);
  const [keenableKeySet, setKeenableKeySet] = React.useState(false);
  const [keenableKeyInput, setKeenableKeyInput] = React.useState("");
  const [savingKeenableKey, setSavingKeenableKey] = React.useState(false);
  const [proxyInput, setProxyInput] = React.useState("");
  const [savingProxy, setSavingProxy] = React.useState(false);
  const [globalProxyInput, setGlobalProxyInput] = React.useState("");
  const [savingGlobalProxy, setSavingGlobalProxy] = React.useState(false);
  const [testing, setTesting] = React.useState(false);
  const [loaded, setLoaded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [savingKey, setSavingKey] = React.useState(false);
  const [pyInterp, setPyInterp] = React.useState("");
  const [workers, setWorkers] = React.useState("3");
  const [savingWorkers, setSavingWorkers] = React.useState(false);
  // Operation constraint injection scope (both on by default).
  const [injectPlanner, setInjectPlanner] = React.useState(true);
  const [injectWorker, setInjectWorker] = React.useState(true);
  // Experimental: noa context compaction (off by default).
  const [noaCompaction, setNoaCompaction] = React.useState(false);
  // Pure frontend preference: bypasses /api/settings, reads and writes localStorage directly.
  const sendMode = useChatSendMode();

  const apply = React.useCallback((s: Settings) => {
    setTrafficCapture(!!s.traffic_capture);
    setAgentTrafficBinding(!!s.agent_traffic_binding);
    setWebSearch(!!s.web_search_enabled);
    setBackend(s.web_search_backend || "ddgs");
    setBraveKeySet(!!s.brave_key_set);
    setTavilyKeySet(!!s.tavily_key_set);
    setKeenableKeySet(!!s.keenable_key_set);
    setProxyInput(s.web_search_proxy ?? "");
    setGlobalProxyInput(s.global_proxy ?? "");
    setPyInterp(s.python_interpreter ?? "");
    setWorkers(String(s.workers ?? 3));
    setInjectPlanner(s.constraints_inject_planner !== false);
    setInjectWorker(s.constraints_inject_worker !== false);
    setNoaCompaction(!!s.noa_compaction);
  }, []);

  const saveWorkers = () => {
    const n = Number(workers);
    if (!Number.isInteger(n) || n <= 0) {
      toast.error(t("page.toasts.concurrencyInvalid"));
      return;
    }
    setSavingWorkers(true);
    api
      .setSettings({ workers: n })
      .then((s) => {
        apply(s);
        toast.success(t("page.toasts.workersSaved"));
      })
      .catch((e) => toast.error(t("page.toasts.saveFailed", { message: (e as Error).message })))
      .finally(() => setSavingWorkers(false));
  };

  const savePython = () => {
    setSaving(true);
    api
      .setSettings({ python_interpreter: pyInterp.trim() })
      .then((s) => {
        apply(s);
        toast.success(t("page.toasts.pythonSaved"));
      })
      .catch((e) => toast.error(t("page.toasts.saveFailed", { message: (e as Error).message })))
      .finally(() => setSaving(false));
  };
  const detectPython = () => {
    setSaving(true);
    api
      .detectPython()
      .then((r) => setPyInterp(r.python_interpreter))
      .catch(() => undefined)
      .finally(() => setSaving(false));
  };

  React.useEffect(() => {
    api
      .settings()
      .then(apply)
      .catch(() => undefined)
      .finally(() => setLoaded(true));
  }, [apply]);

  const toggleTraffic = (v: boolean) => {
    setTrafficCapture(v); // optimistic
    setSaving(true);
    api
      .setSettings({ traffic_capture: v })
      .then(apply)
      .catch(() => setTrafficCapture(!v)) // revert on failure
      .finally(() => setSaving(false));
  };

  const toggleInjectPlanner = (v: boolean) => {
    setInjectPlanner(v); // optimistic
    api
      .setSettings({ constraints_inject_planner: v })
      .then(apply)
      .catch(() => setInjectPlanner(!v)); // revert on failure
  };

  const toggleAgentTrafficBinding = (v: boolean) => {
    setAgentTrafficBinding(v);
    setSaving(true);
    api
      .setSettings({ agent_traffic_binding: v })
      .then((s) => {
        apply(s);
        toast.success(v ? t("page.toasts.agentBindingEnabled") : t("page.toasts.agentBindingDisabled"));
      })
      .catch((e) => {
        setAgentTrafficBinding(!v);
        toast.error(t("page.toasts.saveFailed", { message: (e as Error).message }));
      })
      .finally(() => setSaving(false));
  };

  const toggleInjectWorker = (v: boolean) => {
    setInjectWorker(v); // optimistic
    api
      .setSettings({ constraints_inject_worker: v })
      .then(apply)
      .catch(() => setInjectWorker(!v)); // revert on failure
  };

  const toggleNoaCompaction = (v: boolean) => {
    setNoaCompaction(v); // optimistic
    api
      .setSettings({ noa_compaction: v })
      .then((s) => {
        apply(s);
        toast.success(v ? t("page.toasts.noaEnabled") : t("page.toasts.noaDisabled"));
      })
      .catch((e) => {
        setNoaCompaction(!v); // revert on failure
        toast.error(t("page.toasts.saveFailed", { message: (e as Error).message }));
      });
  };

  // Persist a web-search patch (enable and/or backend). Optimistic with refetch.
  const saveWebSearch = (patch: Partial<Settings>) => {
    setSaving(true);
    api
      .setSettings(patch)
      .then((s) => {
        apply(s);
        toast.success(t("page.toasts.webSearchSaved"));
      })
      .catch((e) => {
        toast.error(t("page.toasts.saveFailed", { message: (e as Error).message }));
        api
          .settings()
          .then(apply)
          .catch(() => undefined);
      })
      .finally(() => setSaving(false));
  };

  const saveBraveKey = () => {
    setSavingKey(true);
    api
      .setSettings({ brave_search_api_key: braveKeyInput })
      .then((s) => {
        apply(s);
        setBraveKeyInput("");
        toast.success(t("page.toasts.braveKeySaved"));
      })
      .catch((e) => toast.error(t("page.toasts.saveFailed", { message: (e as Error).message })))
      .finally(() => setSavingKey(false));
  };

  const saveTavilyKey = () => {
    setSavingTavilyKey(true);
    api
      .setSettings({ tavily_search_api_key: tavilyKeyInput })
      .then((s) => {
        apply(s);
        setTavilyKeyInput("");
        toast.success(t("page.toasts.tavilyKeySaved"));
      })
      .catch((e) => toast.error(t("page.toasts.saveFailed", { message: (e as Error).message })))
      .finally(() => setSavingTavilyKey(false));
  };

  const saveKeenableKey = () => {
    setSavingKeenableKey(true);
    api
      .setSettings({ keenable_search_api_key: keenableKeyInput })
      .then((s) => {
        apply(s);
        setKeenableKeyInput("");
        toast.success(t("page.toasts.keenableKeySaved"));
      })
      .catch((e) => toast.error(t("page.toasts.saveFailed", { message: (e as Error).message })))
      .finally(() => setSavingKeenableKey(false));
  };

  const saveProxy = () => {
    setSavingProxy(true);
    api
      .setSettings({ web_search_proxy: proxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(proxyInput.trim() ? t("page.toasts.proxySaved") : t("page.toasts.proxyCleared"));
      })
      .catch((e) => toast.error(t("page.toasts.saveFailed", { message: (e as Error).message })))
      .finally(() => setSavingProxy(false));
  };

  const saveGlobalProxy = () => {
    setSavingGlobalProxy(true);
    api
      .setSettings({ global_proxy: globalProxyInput.trim() })
      .then((s) => {
        apply(s);
        toast.success(
          globalProxyInput.trim() ? t("page.toasts.globalProxySaved") : t("page.toasts.globalProxyCleared"),
        );
      })
      .catch((e) => toast.error(t("page.toasts.saveFailed", { message: (e as Error).message })))
      .finally(() => setSavingGlobalProxy(false));
  };

  // Run a real "test" search ("test") against the CURRENT form values (backend +
  // proxy + entered key), falling back to saved values server-side. Toasts result.
  const runTest = () => {
    setTesting(true);
    api
      .testWebSearch({
        web_search_backend: backend,
        web_search_proxy: proxyInput.trim(),
        brave_search_api_key: braveKeyInput,
        tavily_search_api_key: tavilyKeyInput,
        keenable_search_api_key: keenableKeyInput,
      })
      .then((r) => {
        if (r.ok) toast.success(t("page.toasts.testSucceeded", { backend: r.backend ?? "", count: r.count ?? 0 }));
        else toast.error(t("page.toasts.testFailed", { error: r.error || t("page.toasts.unknownError") }));
      })
      .catch((e) => toast.error(t("page.toasts.testFailed", { error: (e as Error).message })))
      .finally(() => setTesting(false));
  };

  // brave-free selected but no key stored and none being entered → tool stays off.
  const braveNeedsKey = webSearch && backend === "brave-free" && !braveKeySet;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">{t("page.title")}</h1>
        <p className="text-muted-foreground text-sm">{t("page.subtitle")}</p>
      </div>

      {/* Multi-column instead of grid: the web search card is several times taller than the others and
          its height varies with the selected backend (brave/tavily key inputs are conditional). A grid
          would stretch the row to the tallest card and leave blank space beside the others, while columns
          balance-fill by content height. Card spacing uses mb rather than gap — in a multi-column layout
          column-gap only covers the gaps between columns; row spacing must come from the children. */}
      <div className="columns-1 gap-4 md:gap-6 lg:columns-2">
        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              {t("page.trafficCapture.title")}
            </CardTitle>
            <CardDescription>
              {t.rich("page.trafficCapture.description", {
                b: (c) => <b>{c}</b>,
                br: () => <br />,
              })}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="traffic-capture" className="text-sm font-normal text-muted-foreground">
              {trafficCapture ? t("page.trafficCapture.labelOn") : t("page.trafficCapture.labelOff")}
            </Label>
            <Switch
              id="traffic-capture"
              checked={trafficCapture}
              disabled={!loaded || saving}
              onCheckedChange={toggleTraffic}
            />
          </CardContent>
        </Card>

        <NoiseProfileCard />

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              {t("page.agentBinding.title")}
            </CardTitle>
            <CardDescription id="agent-traffic-binding-description">
              {t.rich("page.agentBinding.description", {
                b: (c) => <b>{c}</b>,
                br: () => <br />,
              })}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="agent-traffic-binding" className="text-sm font-normal text-muted-foreground">
              {agentTrafficBinding ? t("page.agentBinding.labelOn") : t("page.agentBinding.labelOff")}
            </Label>
            <Switch
              id="agent-traffic-binding"
              aria-describedby="agent-traffic-binding-description"
              checked={agentTrafficBinding}
              disabled={!loaded || saving}
              onCheckedChange={toggleAgentTrafficBinding}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              {t("page.globalProxy.title")}
            </CardTitle>
            <CardDescription>
              {t.rich("page.globalProxy.description", {
                b: (c) => <b>{c}</b>,
                br: () => <br />,
                code: (c) => <code>{c}</code>,
              })}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-2">
            <Label htmlFor="global-proxy" className="text-sm font-normal text-muted-foreground">
              {t("page.globalProxy.addressLabel")}
            </Label>
            <div className="flex items-center gap-2">
              <Input
                id="global-proxy"
                autoComplete="off"
                placeholder={t("page.globalProxy.placeholder")}
                value={globalProxyInput}
                disabled={!loaded || savingGlobalProxy}
                onChange={(e) => setGlobalProxyInput(e.target.value)}
              />
              <Button type="button" onClick={saveGlobalProxy} disabled={!loaded || savingGlobalProxy}>
                {t("page.actions.save")}
              </Button>
            </div>
            <p className="text-muted-foreground text-xs">
              {globalProxyInput.trim()
                ? t("page.globalProxy.statusConfigured")
                : t("page.globalProxy.statusNotConfigured")}
            </p>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <ShieldAlertIcon className="size-4" />
              {t("page.constraints.title")}
            </CardTitle>
            <CardDescription>
              {t.rich("page.constraints.description", {
                b: (c) => <b>{c}</b>,
                br: () => <br />,
              })}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="inject-planner" className="text-sm font-normal text-muted-foreground">
                {t("page.constraints.planner")}
                {injectPlanner ? t("page.onSuffix") : t("page.offSuffix")}
              </Label>
              <Switch
                id="inject-planner"
                checked={injectPlanner}
                disabled={!loaded}
                onCheckedChange={toggleInjectPlanner}
              />
            </div>
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="inject-worker" className="text-sm font-normal text-muted-foreground">
                {t("page.constraints.worker")}
                {injectWorker ? t("page.onSuffix") : t("page.offSuffix")}
              </Label>
              <Switch
                id="inject-worker"
                checked={injectWorker}
                disabled={!loaded}
                onCheckedChange={toggleInjectWorker}
              />
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <FlaskConicalIcon className="size-4" />
              {t("page.experimental.title")}
            </CardTitle>
            <CardDescription>
              {t.rich("page.experimental.description", {
                b: (c) => <b>{c}</b>,
                br: () => <br />,
              })}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="noa-compaction" className="text-sm font-normal text-muted-foreground">
              {t("page.experimental.noaLabel")}
              {noaCompaction ? t("page.onSuffix") : t("page.offSuffix")}
            </Label>
            <Switch
              id="noa-compaction"
              checked={noaCompaction}
              disabled={!loaded}
              onCheckedChange={toggleNoaCompaction}
            />
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <SearchIcon className="size-4" />
              {t("page.webSearch.title")}
            </CardTitle>
            <CardDescription>
              {t.rich("page.webSearch.description", {
                b: (c) => <b>{c}</b>,
                br: () => <br />,
              })}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="web-search" className="text-sm font-normal text-muted-foreground">
                {webSearch ? t("page.webSearch.labelOn") : t("page.webSearch.labelOff")}
              </Label>
              <Switch
                id="web-search"
                checked={webSearch}
                disabled={!loaded || saving}
                onCheckedChange={(v) => {
                  setWebSearch(v); // optimistic
                  saveWebSearch({ web_search_enabled: v });
                }}
              />
            </div>

            {webSearch && (
              <div className="flex items-center justify-between gap-4">
                <Label className="text-sm font-normal text-muted-foreground">{t("page.webSearch.sourceLabel")}</Label>
                <Select
                  value={backend}
                  disabled={!loaded || saving}
                  onValueChange={(v) => {
                    setBackend(v); // optimistic
                    saveWebSearch({ web_search_backend: v });
                  }}
                >
                  <SelectTrigger className="w-48 shrink-0">
                    <SelectValue placeholder={t("page.webSearch.sourcePlaceholder")} />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="ddgs">{t("page.webSearch.sourceDdgs")}</SelectItem>
                    <SelectItem value="brave-free">{t("page.webSearch.sourceBrave")}</SelectItem>
                    <SelectItem value="tavily">{t("page.webSearch.sourceTavily")}</SelectItem>
                    <SelectItem value="keenable">{t("page.webSearch.sourceKeenable")}</SelectItem>
                    <SelectItem value="deepseek">{t("page.webSearch.sourceDeepseek")}</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            )}

            {webSearch && backend === "deepseek" && (
              <div className="border-border/60 bg-muted/30 flex flex-col gap-2 rounded-md border p-3">
                <p className="text-sm font-medium">{t("page.webSearch.deepseek.heading")}</p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  {t.rich("page.webSearch.deepseek.note1", { b: (c) => <b>{c}</b> })}
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  {t.rich("page.webSearch.deepseek.note2", { b: (c) => <b>{c}</b> })}
                </p>
                <p className="text-muted-foreground text-xs leading-relaxed">{t("page.webSearch.deepseek.note3")}</p>
              </div>
            )}

            {webSearch && backend === "brave-free" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="brave-key" className="text-sm font-normal text-muted-foreground">
                  {t("page.webSearch.brave.keyLabel")}
                  {braveKeySet && (
                    <span className="ml-2 text-xs text-emerald-500">{t("page.webSearch.configured")}</span>
                  )}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="brave-key"
                    type="password"
                    autoComplete="off"
                    placeholder={
                      braveKeySet ? t("page.webSearch.keyPlaceholderSet") : t("page.webSearch.brave.placeholder")
                    }
                    value={braveKeyInput}
                    disabled={!loaded || savingKey}
                    onChange={(e) => setBraveKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveBraveKey}
                    disabled={!loaded || savingKey || braveKeyInput.trim() === ""}
                  >
                    {t("page.actions.save")}
                  </Button>
                </div>
                {braveNeedsKey && <p className="text-xs text-amber-500">{t("page.webSearch.brave.needsKey")}</p>}
                <p className="text-muted-foreground text-xs">{t("page.webSearch.brave.help")}</p>
              </div>
            )}

            {webSearch && backend === "tavily" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="tavily-key" className="text-sm font-normal text-muted-foreground">
                  {t("page.webSearch.tavily.keyLabel")}
                  {tavilyKeySet && (
                    <span className="ml-2 text-xs text-emerald-500">{t("page.webSearch.configured")}</span>
                  )}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="tavily-key"
                    type="password"
                    autoComplete="off"
                    placeholder={
                      tavilyKeySet ? t("page.webSearch.keyPlaceholderSet") : t("page.webSearch.tavily.placeholder")
                    }
                    value={tavilyKeyInput}
                    disabled={!loaded || savingTavilyKey}
                    onChange={(e) => setTavilyKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveTavilyKey}
                    disabled={!loaded || savingTavilyKey || tavilyKeyInput.trim() === ""}
                  >
                    {t("page.actions.save")}
                  </Button>
                </div>
                {webSearch && backend === "tavily" && !tavilyKeySet && (
                  <p className="text-xs text-amber-500">{t("page.webSearch.tavily.needsKey")}</p>
                )}
                <p className="text-muted-foreground text-xs">{t("page.webSearch.tavily.help")}</p>
              </div>
            )}

            {webSearch && backend === "keenable" && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="keenable-key" className="text-sm font-normal text-muted-foreground">
                  {t("page.webSearch.keenable.keyLabel")}
                  {keenableKeySet && (
                    <span className="ml-2 text-xs text-emerald-500">{t("page.webSearch.configured")}</span>
                  )}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="keenable-key"
                    type="password"
                    autoComplete="off"
                    placeholder={
                      keenableKeySet ? t("page.webSearch.keyPlaceholderSet") : t("page.webSearch.keenable.placeholder")
                    }
                    value={keenableKeyInput}
                    disabled={!loaded || savingKeenableKey}
                    onChange={(e) => setKeenableKeyInput(e.target.value)}
                  />
                  <Button
                    type="button"
                    onClick={saveKeenableKey}
                    disabled={!loaded || savingKeenableKey || keenableKeyInput.trim() === ""}
                  >
                    {t("page.actions.save")}
                  </Button>
                </div>
                <p className="text-muted-foreground text-xs leading-relaxed">
                  {t.rich("page.webSearch.keenable.help", { b: (c) => <b>{c}</b> })}
                </p>
              </div>
            )}

            {webSearch && (
              <div className="flex flex-col gap-2">
                <Label htmlFor="ws-proxy" className="text-sm font-normal text-muted-foreground">
                  {t("page.webSearch.egressProxy.label")}
                </Label>
                <div className="flex items-center gap-2">
                  <Input
                    id="ws-proxy"
                    autoComplete="off"
                    placeholder={t("page.webSearch.egressProxy.placeholder")}
                    value={proxyInput}
                    disabled={!loaded || savingProxy}
                    onChange={(e) => setProxyInput(e.target.value)}
                  />
                  <Button type="button" onClick={saveProxy} disabled={!loaded || savingProxy}>
                    {t("page.actions.save")}
                  </Button>
                </div>
                <p className="text-muted-foreground text-xs">{t("page.webSearch.egressProxy.help")}</p>
              </div>
            )}

            {webSearch && (
              <div className="flex items-center justify-between gap-4 border-t pt-4">
                <p className="text-muted-foreground text-xs">{t("page.webSearch.test.hint")}</p>
                <Button
                  type="button"
                  variant="outline"
                  onClick={runTest}
                  disabled={!loaded || testing}
                  className="shrink-0"
                >
                  {testing ? t("page.webSearch.test.testing") : t("page.webSearch.test.button")}
                </Button>
              </div>
            )}
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <RadioTowerIcon className="size-4" />
              {t("page.python.title")}
            </CardTitle>
            <CardDescription>{t.rich("page.python.description", { b: (c) => <b>{c}</b> })}</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                className="font-mono text-sm"
                placeholder={t("page.python.placeholder")}
                value={pyInterp}
                disabled={!loaded || saving}
                onChange={(e) => setPyInterp(e.target.value)}
              />
              <Button variant="outline" onClick={detectPython} disabled={!loaded || saving}>
                {t("page.python.redetect")}
              </Button>
              <Button onClick={savePython} disabled={!loaded || saving}>
                {t("page.actions.save")}
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <CpuIcon className="size-4" />
              {t("page.concurrency.title")}
            </CardTitle>
            <CardDescription>{t.rich("page.concurrency.description", { b: (c) => <b>{c}</b> })}</CardDescription>
          </CardHeader>
          <CardContent className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Input
                type="number"
                min={1}
                className="w-32 font-mono text-sm"
                placeholder="3"
                value={workers}
                disabled={!loaded || savingWorkers}
                onChange={(e) => setWorkers(e.target.value)}
              />
              <Button onClick={saveWorkers} disabled={!loaded || savingWorkers}>
                {t("page.actions.save")}
              </Button>
            </div>
          </CardContent>
        </Card>

        <Card className="mb-4 break-inside-avoid md:mb-6">
          <CardHeader>
            <CardTitle className="flex items-center gap-2 text-base">
              <KeyboardIcon className="size-4" />
              {t("page.chatSend.title")}
            </CardTitle>
            <CardDescription>
              {t.rich("page.chatSend.description", {
                b: (c) => <b>{c}</b>,
                br: () => <br />,
              })}
            </CardDescription>
          </CardHeader>
          <CardContent className="flex items-center justify-between gap-4">
            <Label htmlFor="chat-send-mode" className="text-sm font-normal text-muted-foreground">
              {t("page.chatSend.label")}
            </Label>
            <Select value={sendMode} onValueChange={(v) => setChatSendMode(v as ChatSendMode)}>
              <SelectTrigger id="chat-send-mode" className="w-72">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {CHAT_SEND_MODE_OPTIONS.map((option) => (
                  <SelectItem key={option.value} value={option.value}>
                    {t.has(`page.chatSend.options.${option.value}`)
                      ? t(`page.chatSend.options.${option.value}`)
                      : option.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}

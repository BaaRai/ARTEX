"use client";

import * as React from "react";

import {
  ArrowLeftIcon,
  ArrowRightIcon,
  BotIcon,
  GlobeIcon,
  HandIcon,
  Loader2Icon,
  PlayIcon,
  PlusIcon,
  RotateCwIcon,
  ShieldAlertIcon,
  UndoIcon,
  XIcon,
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { t } from "@/i18n";
import { api, sseUrl } from "@/lib/api";
import { MOCK } from "@/lib/mock/enabled";
import type { SharedBrowserAction, SharedBrowserInput, SharedBrowserStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

// The shared browser: the same Chromium the agents drive through the browser MCP.
// The page shows its viewed tab live (CDP screencast frames over SSE). Take over to
// solve a CAPTCHA, enter an OTP or sign in; the agent's browser tools wait while
// you hold control, and continue in the same tab with the same cookies after
// Hand back. See server/sharedbrowser.go.

const EMPTY: SharedBrowserStatus = {
  enabled: true,
  running: false,
  tabs: [],
  viewed: "",
  operator: false,
  agent: "",
  width: 1280,
  height: 800,
};

// CDP modifier bits: Alt=1, Ctrl=2, Meta=4, Shift=8.
function modifiers(e: { altKey: boolean; ctrlKey: boolean; metaKey: boolean; shiftKey: boolean }): number {
  return (e.altKey ? 1 : 0) | (e.ctrlKey ? 2 : 0) | (e.metaKey ? 4 : 0) | (e.shiftKey ? 8 : 0);
}

// The character a key types, for keyDown events: printable keys and Enter.
function keyText(key: string): string | undefined {
  if (key.length === 1) return key;
  if (key === "Enter") return "\r";
  return undefined;
}

const BUTTONS: Record<number, "left" | "middle" | "right"> = { 0: "left", 1: "middle", 2: "right" };

function tabLabel(tab: { title: string; url: string }): string {
  const title = tab.title.trim();
  if (title) return title.length > 60 ? `${title.slice(0, 60)}…` : title;
  return tab.url || t("New tab");
}

export default function SharedBrowserPage() {
  const [status, setStatus] = React.useState<SharedBrowserStatus>(EMPTY);
  const [frame, setFrame] = React.useState<string>("");
  const [address, setAddress] = React.useState("");
  const [editingAddress, setEditingAddress] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  const [connected, setConnected] = React.useState(false);

  const viewRef = React.useRef<HTMLDivElement>(null);
  const imgRef = React.useRef<HTMLImageElement>(null);
  const queue = React.useRef<SharedBrowserInput[]>([]);
  const flushTimer = React.useRef<ReturnType<typeof setTimeout> | null>(null);
  const pressed = React.useRef(false);
  const lastMove = React.useRef(0);
  const operatorRef = React.useRef(false);
  operatorRef.current = status.operator;

  const viewedTab = status.tabs.find((tab) => tab.id === status.viewed);

  // Keep the address bar on the viewed tab's URL unless the operator is typing.
  React.useEffect(() => {
    if (!editingAddress) setAddress(viewedTab?.url ?? "");
  }, [viewedTab?.url, editingAddress]);

  // Live view: status and frame events.
  React.useEffect(() => {
    if (MOCK) return;
    let es: EventSource | null = null;
    let closed = false;
    const open = () => {
      es = new EventSource(sseUrl("/api/browser/stream"));
      es.onopen = () => setConnected(true);
      es.onerror = () => setConnected(false); // EventSource reconnects by itself
      es.onmessage = (e) => {
        try {
          const ev = JSON.parse(e.data) as
            | { type: "status"; status: SharedBrowserStatus }
            | { type: "frame"; data: string };
          if (ev.type === "status") setStatus({ ...ev.status, tabs: ev.status.tabs ?? [] });
          else if (ev.type === "frame") setFrame(ev.data);
        } catch {
          /* ignore a malformed event */
        }
      };
    };
    // The stream answers 409 when the shared browser is turned off: read the status first.
    api
      .browserStatus()
      .then((s) => {
        if (closed) return;
        setStatus(s);
        if (s.enabled) open();
      })
      .catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
    return () => {
      closed = true;
      es?.close();
    };
  }, []);

  const run = React.useCallback(async (fn: () => Promise<SharedBrowserStatus | undefined>) => {
    setBusy(true);
    setError("");
    try {
      const s = await fn();
      if (s) setStatus({ ...s, tabs: s.tabs ?? [] });
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setBusy(false);
    }
  }, []);

  const control = React.useCallback(
    (action: SharedBrowserAction, target?: string) => run(() => api.browserControl(action, target)),
    [run],
  );

  // Input is batched: one request per ~30ms instead of one per event.
  const flush = React.useCallback(() => {
    flushTimer.current = null;
    const events = queue.current;
    queue.current = [];
    if (!events.length) return;
    api.browserInput(events).catch((e: unknown) => setError(e instanceof Error ? e.message : String(e)));
  }, []);
  const send = React.useCallback(
    (ev: SharedBrowserInput) => {
      queue.current.push(ev);
      if (!flushTimer.current) flushTimer.current = setTimeout(flush, 30);
    },
    [flush],
  );

  // Pointer position as fractions of the frame; the server maps them to page pixels.
  const point = (e: { clientX: number; clientY: number }) => {
    const r = imgRef.current?.getBoundingClientRect();
    if (!r || r.width === 0 || r.height === 0) return { x: 0, y: 0 };
    return { x: (e.clientX - r.left) / r.width, y: (e.clientY - r.top) / r.height };
  };

  const onMouseDown = (e: React.MouseEvent) => {
    e.preventDefault();
    viewRef.current?.focus();
    pressed.current = true;
    send({
      kind: "mouse",
      type: "mousePressed",
      ...point(e),
      button: BUTTONS[e.button] ?? "left",
      clickCount: e.detail || 1,
      modifiers: modifiers(e),
    });
  };
  const onMouseUp = (e: React.MouseEvent) => {
    e.preventDefault();
    pressed.current = false;
    send({
      kind: "mouse",
      type: "mouseReleased",
      ...point(e),
      button: BUTTONS[e.button] ?? "left",
      clickCount: e.detail || 1,
      modifiers: modifiers(e),
    });
  };
  const onMouseMove = (e: React.MouseEvent) => {
    // Hover only matters once you hold control (sliders, press-and-hold checks).
    if (!operatorRef.current && !pressed.current) return;
    const now = performance.now();
    if (now - lastMove.current < 40 && !pressed.current) return;
    lastMove.current = now;
    send({
      kind: "mouse",
      type: "mouseMoved",
      ...point(e),
      button: pressed.current ? "left" : "none",
      modifiers: modifiers(e),
    });
  };
  const onWheel = (e: React.WheelEvent) => {
    send({ kind: "wheel", ...point(e), deltaX: e.deltaX, deltaY: e.deltaY, modifiers: modifiers(e) });
  };
  const onKey = (type: "keyDown" | "keyUp") => (e: React.KeyboardEvent) => {
    // Keep browser shortcuts for the page except copy/paste, which the clipboard needs.
    const mod = e.ctrlKey || e.metaKey;
    if (mod && (e.key === "v" || e.key === "c")) return;
    e.preventDefault();
    send({
      kind: "key",
      type,
      key: e.key,
      code: e.code,
      keyCode: e.keyCode,
      modifiers: modifiers(e),
      text: keyText(e.key),
    });
  };
  const onPaste = (e: React.ClipboardEvent) => {
    const text = e.clipboardData.getData("text");
    if (text) {
      e.preventDefault();
      send({ kind: "text", text });
    }
  };

  const submitAddress = (e: React.FormEvent) => {
    e.preventDefault();
    setEditingAddress(false);
    if (address.trim()) void control("goto", address.trim());
  };

  let holder: "operator" | "agent" | "idle" = "idle";
  if (status.operator) holder = "operator";
  else if (status.agent) holder = "agent";

  // What the view shows before the first frame arrives.
  let placeholder: React.ReactNode;
  if (MOCK) {
    placeholder = <span>{t("The live browser view needs the ARTEX backend.")}</span>;
  } else if (status.running) {
    placeholder = <span>{t("Waiting for the first frame…")}</span>;
  } else {
    placeholder = (
      <>
        <span>
          {t(
            "The shared browser starts when an agent first uses a browser tool. Start it now to sign in to the target before a task runs.",
          )}
        </span>
        {status.error && <span className="text-red-600 dark:text-red-400">{status.error}</span>}
        <Button size="sm" disabled={busy} onClick={() => void run(() => api.browserStart())}>
          {busy ? <Loader2Icon className="size-3.5 animate-spin" /> : <PlayIcon className="size-3.5" />}
          {t("Start browser")}
        </Button>
      </>
    );
  }

  return (
    <div className="flex flex-1 flex-col gap-3">
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">{t("Shared browser")}</h1>
          <p className="text-muted-foreground text-sm">
            {t(
              "The browser your agents drive. Take over to solve a CAPTCHA, enter a code or sign in, then hand it back; the agent continues in the same tab.",
            )}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {holder === "operator" && (
            <Badge className="gap-1 bg-amber-500/15 text-amber-700 dark:text-amber-300">
              <HandIcon className="size-3" />
              {t("You have control")}
            </Badge>
          )}
          {holder === "agent" && (
            <Badge variant="secondary" className="gap-1">
              <BotIcon className="size-3" />
              {t("Agent driving: {agent}", { agent: status.agent })}
            </Badge>
          )}
          {holder === "idle" && status.running && <Badge variant="outline">{t("Idle")}</Badge>}
          {status.running && !connected && <Badge variant="outline">{t("Reconnecting…")}</Badge>}
        </div>
      </div>

      {!status.enabled && (
        <div className="rounded-lg border p-4 text-sm text-muted-foreground">
          {t(
            "The shared browser is turned off on this server (-shared-browser=false). Each agent run uses a private headless browser instead.",
          )}
        </div>
      )}

      {status.enabled && status.captcha && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border border-amber-500/50 bg-amber-500/10 px-3 py-2 text-sm">
          <ShieldAlertIcon className="size-4 text-amber-600 dark:text-amber-400" />
          <span>
            {t("Human check on this page: {kind}. The agent's page actions wait until you deal with it.", {
              kind: status.captcha.kind,
            })}
          </span>
          {!status.operator && (
            <Button size="sm" className="ml-auto h-7" disabled={busy} onClick={() => void control("take")}>
              <HandIcon className="size-3.5" />
              {t("Take over")}
            </Button>
          )}
        </div>
      )}

      {status.enabled && (
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="icon"
            variant="outline"
            className="size-8"
            title={t("Back")}
            disabled={!status.running || busy}
            onClick={() => void control("back")}
          >
            <ArrowLeftIcon className="size-4" />
          </Button>
          <Button
            size="icon"
            variant="outline"
            className="size-8"
            title={t("Forward")}
            disabled={!status.running || busy}
            onClick={() => void control("forward")}
          >
            <ArrowRightIcon className="size-4" />
          </Button>
          <Button
            size="icon"
            variant="outline"
            className="size-8"
            title={t("Reload")}
            disabled={!status.running || busy}
            onClick={() => void control("reload")}
          >
            <RotateCwIcon className="size-4" />
          </Button>
          <form className="min-w-56 flex-1" onSubmit={submitAddress}>
            <Input
              className="h-8 font-mono text-xs"
              value={address}
              placeholder={t("Address")}
              disabled={!status.running}
              onFocus={() => setEditingAddress(true)}
              onBlur={() => setEditingAddress(false)}
              onChange={(e) => setAddress(e.target.value)}
            />
          </form>
          {status.tabs.length > 0 && (
            <Select value={status.viewed} onValueChange={(v) => v && void control("select_tab", v)}>
              <SelectTrigger className="h-8 w-56 text-xs">
                <SelectValue placeholder={t("Tab")} />
              </SelectTrigger>
              <SelectContent>
                {status.tabs.map((tab) => (
                  <SelectItem key={tab.id} value={tab.id} className="text-xs">
                    {tabLabel(tab)}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          )}
          <Button
            size="icon"
            variant="outline"
            className="size-8"
            title={t("New tab")}
            disabled={!status.running || busy}
            onClick={() => void control("new_tab", "")}
          >
            <PlusIcon className="size-4" />
          </Button>
          <Button
            size="icon"
            variant="outline"
            className="size-8"
            title={t("Close tab")}
            disabled={!status.running || busy || !status.viewed}
            onClick={() => void control("close_tab")}
          >
            <XIcon className="size-4" />
          </Button>
          {status.running &&
            (status.operator ? (
              <Button size="sm" className="h-8" disabled={busy} onClick={() => void control("release")}>
                <UndoIcon className="size-3.5" />
                {t("Hand back")}
              </Button>
            ) : (
              <Button size="sm" variant="outline" className="h-8" disabled={busy} onClick={() => void control("take")}>
                <HandIcon className="size-3.5" />
                {t("Take over")}
              </Button>
            ))}
        </div>
      )}

      {error && <div className="text-sm text-red-600 dark:text-red-400">{error}</div>}

      {status.enabled && (
        <div
          ref={viewRef}
          // biome-ignore lint/a11y/noNoninteractiveTabindex: the live view receives keyboard input for the remote page.
          tabIndex={0}
          role="application"
          aria-label={t("Shared browser view")}
          className={cn(
            "relative overflow-hidden rounded-lg border bg-muted/30 outline-none",
            status.operator ? "ring-2 ring-amber-500/70" : "focus:ring-2 focus:ring-ring",
          )}
          onKeyDown={onKey("keyDown")}
          onKeyUp={onKey("keyUp")}
          onPaste={onPaste}
          onContextMenu={(e) => e.preventDefault()}
        >
          {status.running && frame ? (
            // biome-ignore lint/performance/noImgElement: a live JPEG stream, not a static asset.
            <img
              ref={imgRef}
              alt={t("Live view of the shared browser")}
              src={`data:image/jpeg;base64,${frame}`}
              draggable={false}
              className="block h-auto w-full select-none"
              style={{ aspectRatio: `${status.width} / ${status.height}` }}
              onMouseDown={onMouseDown}
              onMouseUp={onMouseUp}
              onMouseMove={onMouseMove}
              onWheel={onWheel}
            />
          ) : (
            <div
              className="flex w-full flex-col items-center justify-center gap-3 p-8 text-center text-sm text-muted-foreground"
              style={{ aspectRatio: `${status.width} / ${status.height}` }}
            >
              <GlobeIcon className="size-10 opacity-40" />
              {placeholder}
            </div>
          )}
        </div>
      )}

      {status.enabled && status.running && (
        <p className="text-muted-foreground text-xs">
          {status.operator
            ? t("Click and type in the view. Agent browser tools are paused until you press Hand back.")
            : t("Clicking or typing in the view takes control from the agent.")}
        </p>
      )}
    </div>
  );
}

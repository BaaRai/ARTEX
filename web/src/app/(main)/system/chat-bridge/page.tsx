"use client";

import * as React from "react";

import { PlugZapIcon, SaveIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { t } from "@/i18n";
import { api } from "@/lib/api";
import type { ChatBridge, ChatBridgeStatus } from "@/lib/types";

// v1 ships one transport. The page is written against the kind so a second chat app only needs
// its own fields here; the backend is already transport-agnostic.
const KIND = "telegram";
// How often the live status ("bot online") is refreshed while the page is open.
const STATUS_POLL_MS = 5000;

interface Form {
  enabled: boolean;
  token: string; // empty = keep the stored token
  baseURL: string;
  chats: string; // one chat ID per line
  users: string; // one user ID per line, for groups
}

function formFrom(b: ChatBridge | undefined): Form {
  return {
    enabled: b?.enabled ?? false,
    token: "",
    baseURL: typeof b?.config.base_url === "string" ? b.config.base_url : "",
    chats: (b?.allowed_chats ?? []).join("\n"),
    users: (b?.allowed_users ?? []).join("\n"),
  };
}

function parseIDs(raw: string): string[] {
  return raw
    .split(/[\s,]+/)
    .map((s) => s.trim())
    .filter(Boolean);
}

function StatusBadge({ status }: { status: ChatBridgeStatus }) {
  switch (status.state) {
    case "online":
      return <Badge>{t("Online")}</Badge>;
    case "connecting":
      return <Badge variant="secondary">{t("Connecting")}</Badge>;
    case "error":
      return <Badge variant="destructive">{t("Error")}</Badge>;
    default:
      return <Badge variant="outline">{t("Off")}</Badge>;
  }
}

export default function ChatBridgePage() {
  const [bridge, setBridge] = React.useState<ChatBridge | undefined>();
  const [form, setForm] = React.useState<Form>(formFrom(undefined));
  const [loaded, setLoaded] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [testing, setTesting] = React.useState(false);

  const load = React.useCallback(async (resetForm: boolean) => {
    const list = await api.chatBridges();
    const b = list.find((x) => x.kind === KIND);
    setBridge(b);
    if (resetForm) setForm(formFrom(b));
    setLoaded(true);
  }, []);

  React.useEffect(() => {
    load(true).catch((e) =>
      toast.error(t("Failed to load the chat bot settings: {error}", { error: (e as Error).message })),
    );
    // Only the status is refreshed; the form keeps what the operator is typing.
    const id = window.setInterval(() => {
      load(false).catch(() => {
        // A missed refresh is retried on the next tick; the status card keeps its last value.
      });
    }, STATUS_POLL_MS);
    return () => window.clearInterval(id);
  }, [load]);

  const storedToken = typeof bridge?.config.bot_token === "string" ? bridge.config.bot_token : "";

  async function save() {
    const config: Record<string, unknown> = { base_url: form.baseURL.trim() };
    // An empty token field keeps the stored token: the masked value tells the server "unchanged".
    config.bot_token = form.token.trim() || storedToken;
    setSaving(true);
    try {
      const saved = await api.chatBridgeSave(KIND, {
        enabled: form.enabled,
        config,
        allowed_chats: parseIDs(form.chats),
        allowed_users: parseIDs(form.users),
      });
      setBridge(saved);
      setForm(formFrom(saved));
      toast.success(t("Saved"));
    } catch (e) {
      toast.error(t("Save failed: {error}", { error: (e as Error).message }));
    } finally {
      setSaving(false);
    }
  }

  async function test() {
    setTesting(true);
    try {
      const r = await api.chatBridgeTest(KIND);
      toast.success(t("Connected to @{bot} in {ms} ms", { bot: r.bot.username, ms: String(r.latency_ms) }));
    } catch (e) {
      toast.error(t("Connection test failed: {error}", { error: (e as Error).message }));
    } finally {
      setTesting(false);
    }
  }

  const status: ChatBridgeStatus = bridge?.status ?? { state: "off" };
  const chats = parseIDs(form.chats);
  const chatCount = chats.length;
  // Telegram group and supergroup IDs are negative; private chats are the user's own positive ID.
  const groupAllowed = chats.some((id) => id.startsWith("-"));
  const userCount = parseIDs(form.users).length;

  return (
    <div className="flex flex-1 flex-col gap-4 md:gap-6">
      <div>
        <h1 className="font-semibold text-xl tracking-tight">{t("Chat bot")}</h1>
        <p className="text-muted-foreground text-sm">
          {t(
            "Talk to ARTEX from Telegram. Only the chats you allow below can use the bot; every other chat gets one refusal and is then ignored.",
          )}
        </p>
      </div>

      <Card className="gap-3">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base">
            {t("Status")}
            <StatusBadge status={status} />
          </CardTitle>
        </CardHeader>
        <CardContent className="grid gap-1 text-sm">
          {status.bot && (
            <p>
              {t("Bot")}: <span className="font-mono">@{status.bot.username}</span>
            </p>
          )}
          {status.state === "off" && (
            <p className="text-muted-foreground">{t("The bot is off. Enter a token and switch it on.")}</p>
          )}
          {status.last_poll_at && (
            <p className="text-muted-foreground">
              {t("Last poll: {time}", { time: new Date(status.last_poll_at).toLocaleString() })}
            </p>
          )}
          {status.state === "error" && status.last_error && (
            <p className="break-words text-destructive">{status.last_error}</p>
          )}
        </CardContent>
      </Card>

      <Card className="gap-3">
        <CardHeader>
          <CardTitle className="text-base">Telegram</CardTitle>
        </CardHeader>
        <CardContent className="grid gap-5">
          <div className="flex items-center gap-3">
            <Switch
              id="cb-enabled"
              checked={form.enabled}
              disabled={!loaded}
              onCheckedChange={(v) => setForm((f) => ({ ...f, enabled: v }))}
            />
            <Label htmlFor="cb-enabled">{t("Enabled")}</Label>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="cb-token">{t("Bot token")}</Label>
            <Input
              id="cb-token"
              type="password"
              autoComplete="off"
              placeholder={
                storedToken
                  ? t("Stored ({hint}); leave empty to keep it", {
                      hint: storedToken.replace(/^__masked__:?/, "") || "…",
                    })
                  : "123456789:AA…"
              }
              value={form.token}
              onChange={(e) => setForm((f) => ({ ...f, token: e.target.value }))}
            />
            <p className="text-muted-foreground text-xs">
              {t(
                "Create a bot with @BotFather and paste its token. Give ARTEX its own bot: a token used by another poller or a webhook cannot be shared.",
              )}
            </p>
          </div>

          <div className="grid gap-2">
            <Label htmlFor="cb-chats">{t("Allowed chat IDs")}</Label>
            <Textarea
              id="cb-chats"
              rows={4}
              className="font-mono"
              placeholder={"123456789\n-1001234567890"}
              value={form.chats}
              onChange={(e) => setForm((f) => ({ ...f, chats: e.target.value }))}
            />
            <p className="text-muted-foreground text-xs">
              {t(
                "One per line. To find a chat ID, message the bot from that chat: its refusal names the chat ID. In a group, only the allowed users below can use the bot.",
              )}
            </p>
            {form.enabled && chatCount === 0 && (
              <p className="text-amber-600 text-xs dark:text-amber-400">
                {t(
                  "No chat is allowed yet: the bot refuses every chat and replies with its chat ID. Add your chat ID here to start using it.",
                )}
              </p>
            )}
          </div>

          <div className="grid gap-2">
            <Label htmlFor="cb-users">{t("Allowed user IDs (groups)")}</Label>
            <Textarea
              id="cb-users"
              rows={3}
              className="font-mono"
              placeholder={"123456789"}
              value={form.users}
              onChange={(e) => setForm((f) => ({ ...f, users: e.target.value }))}
            />
            <p className="text-muted-foreground text-xs">
              {t(
                "One per line. Needed only for groups: in an allowed group the bot answers only these users, so joining the group grants nothing. A member who is refused gets their user ID in the refusal.",
              )}
            </p>
            {form.enabled && groupAllowed && userCount === 0 && (
              <p className="text-amber-600 text-xs dark:text-amber-400">
                {t("A group is allowed but no user is: the bot refuses every member of that group.")}
              </p>
            )}
          </div>

          <div className="grid gap-2">
            <Label htmlFor="cb-base">{t("API URL (optional)")}</Label>
            <Input
              id="cb-base"
              placeholder="https://api.telegram.org"
              value={form.baseURL}
              onChange={(e) => setForm((f) => ({ ...f, baseURL: e.target.value }))}
            />
            <p className="text-muted-foreground text-xs">
              {t("Only for a self-hosted Bot API server or a relay. Changing it requires entering the token again.")}
            </p>
          </div>

          <div className="flex flex-wrap gap-2">
            <Button onClick={save} disabled={!loaded || saving}>
              <SaveIcon />
              {saving ? t("Saving…") : t("Save")}
            </Button>
            <Button variant="outline" onClick={test} disabled={!bridge?.configured || testing}>
              <PlugZapIcon />
              {testing ? t("Testing…") : t("Test connection")}
            </Button>
          </div>
          <p className="text-muted-foreground text-xs">
            {t(
              "Commands: /tasks, /task <id>, /pause, /resume, /findings. Any other message goes to the main agent of the chat's selected task, in the same conversation as the web chat, and its answer is sent back.",
            )}
          </p>
          <p className="text-muted-foreground text-xs">
            {t(
              "The bot uses long polling, so ARTEX opens no inbound port. Messages it sends contain summaries and links, never evidence bodies or credentials.",
            )}{" "}
            {t("Links use the Link-back URL from the notification settings.")}
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

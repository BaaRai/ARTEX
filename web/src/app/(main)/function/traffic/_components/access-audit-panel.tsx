"use client";

import * as React from "react";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { t } from "@/i18n";
import type { AccessAuditPage } from "@/lib/access-audit";
import { api } from "@/lib/api";

function localTime(date: Date): string {
  const local = new Date(date.getTime() - date.getTimezoneOffset() * 60000);
  return local.toISOString().slice(0, 16);
}

export function AccessAuditPanel() {
  const formID = React.useId();
  const [url, setURL] = React.useState("");
  const [host, setHost] = React.useState("");
  const [task, setTask] = React.useState("");
  const [from, setFrom] = React.useState(() => localTime(new Date(Date.now() - 86400000)));
  const [to, setTo] = React.useState(() => localTime(new Date(Date.now() + 60000)));
  const [source, setSource] = React.useState("");
  const [page, setPage] = React.useState<AccessAuditPage | null>(null);
  const [query, setQuery] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");

  async function load(next = false) {
    setBusy(true);
    setError("");
    if (!next) setPage(null);
    try {
      const params = next
        ? new URLSearchParams(query)
        : new URLSearchParams({
            url,
            host,
            task,
            source,
            from: new Date(from).toISOString(),
            to: new Date(to).toISOString(),
            limit: "100",
          });
      if (next && page?.next) params.set("before", String(page.next));
      const result = await api.accessAudit(params.toString());
      setPage(result);
      if (!next) setQuery(params.toString());
    } catch (e) {
      setError(e instanceof Error ? e.message : t("Access audit unavailable"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-4">
      <Alert>
        <AlertDescription>
          {t(
            "Audit coverage is partial across all companies. No match does not prove no access. Direct shell/MCP traffic, opaque tunnels and remote SSRF/RCE hops are not covered. Query parameters, credentials, headers and bodies are not stored; URL paths may still be sensitive.",
          )}
        </AlertDescription>
      </Alert>
      <form
        className="grid gap-3 md:grid-cols-3"
        onSubmit={(e) => {
          e.preventDefault();
          void load();
        }}
      >
        <label htmlFor={`${formID}-url`} className="space-y-1 text-sm">
          {t("URL or path contains")}
          <Input id={`${formID}-url`} value={url} onChange={(e) => setURL(e.target.value)} maxLength={8192} />
        </label>
        <label htmlFor={`${formID}-host`} className="space-y-1 text-sm">
          {t("Exact hostname")}
          <Input id={`${formID}-host`} value={host} onChange={(e) => setHost(e.target.value)} maxLength={255} />
        </label>
        <label htmlFor={`${formID}-task`} className="space-y-1 text-sm">
          {t("Task ID")}
          <Input id={`${formID}-task`} value={task} onChange={(e) => setTask(e.target.value)} maxLength={64} />
        </label>
        <label htmlFor={`${formID}-from`} className="space-y-1 text-sm">
          {t("From (local time)")}
          <Input
            id={`${formID}-from`}
            type="datetime-local"
            required
            value={from}
            onChange={(e) => setFrom(e.target.value)}
          />
        </label>
        <label htmlFor={`${formID}-to`} className="space-y-1 text-sm">
          {t("To (local time, exclusive)")}
          <Input
            id={`${formID}-to`}
            type="datetime-local"
            required
            value={to}
            onChange={(e) => setTo(e.target.value)}
          />
        </label>
        <label className="space-y-1 text-sm">
          {t("Observation source")}
          <select
            className="h-9 w-full rounded-md border bg-background px-3"
            value={source}
            onChange={(e) => setSource(e.target.value)}
          >
            <option value="">{t("All")}</option>
            <option value="native_http">native_http</option>
            <option value="webfetch_trace">webfetch_trace</option>
            <option value="recording_proxy">recording_proxy</option>
            <option value="extension_ingest">extension_ingest</option>
            <option value="har_import">har_import</option>
          </select>
        </label>
        <Button type="submit" disabled={busy}>
          {busy ? t("Loading…") : t("Search")}
        </Button>
      </form>
      {error && (
        <p role="alert" className="text-destructive">
          {error}
        </p>
      )}
      {page && (
        <>
          <p className="text-muted-foreground text-sm">
            {new Date(page.from).toLocaleString()} – {new Date(page.to).toLocaleString()}
          </p>
          <p className="text-muted-foreground text-sm">
            {t("Retention: {days} days, up to {max} events. Write failures since startup: {failures}.", {
              days: page.retention_days,
              max: page.max_events,
              failures: page.write_failures_since_start,
            })}
          </p>
          <p className="text-muted-foreground text-sm">
            {t(
              "Attempts and writes do not prove origin receipt. Response observations may come from a proxy. Imported exchanges are external reports. Several events can describe one request; unknown attribution stays empty.",
            )}
          </p>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>{t("Observed at")}</TableHead>
                <TableHead>{t("Observation source")}</TableHead>
                <TableHead>{t("URL")}</TableHead>
                <TableHead>{t("Attribution")}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {page.events.map((event) => (
                <TableRow key={event.id}>
                  <TableCell className="whitespace-nowrap align-top">
                    {new Date(event.observed_at).toLocaleString()}
                    <div className="text-muted-foreground text-xs">#{event.id}</div>
                  </TableCell>
                  <TableCell className="align-top font-mono text-xs">
                    {event.source}
                    <br />
                    {event.outcome}
                    <br />
                    {event.method} {event.status || ""}
                  </TableCell>
                  <TableCell className="max-w-xl whitespace-normal break-all align-top font-mono text-xs">
                    {event.url}
                  </TableCell>
                  <TableCell className="max-w-sm whitespace-normal break-all align-top text-xs">
                    <dl>
                      {event.task_id && (
                        <>
                          <dt>{t("Task ID")}</dt>
                          <dd>{event.task_id}</dd>
                        </>
                      )}
                      {!!event.conversation_id && (
                        <>
                          <dt>{t("Conversation ID")}</dt>
                          <dd>{event.conversation_id}</dd>
                        </>
                      )}
                      {event.agent && (
                        <>
                          <dt>{t("Agent")}</dt>
                          <dd>{event.agent}</dd>
                        </>
                      )}
                      {event.run_id && (
                        <>
                          <dt>{t("Run ID")}</dt>
                          <dd className="font-mono">{event.run_id}</dd>
                        </>
                      )}
                      {event.tool && (
                        <>
                          <dt>{t("Tool")}</dt>
                          <dd>{event.tool}</dd>
                        </>
                      )}
                      {event.tool_use_id && (
                        <>
                          <dt>{t("Tool call ID")}</dt>
                          <dd className="font-mono">{event.tool_use_id}</dd>
                        </>
                      )}
                      {event.request_id && (
                        <>
                          <dt>{t("Request ID")}</dt>
                          <dd className="font-mono">{event.request_id}</dd>
                        </>
                      )}
                    </dl>
                    {!event.task_id && !event.conversation_id && t("Unknown")}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
          {page.events.length === 0 && <p>{t("No matching observations in the retained coverage.")}</p>}
          {page.next > 0 && (
            <Button disabled={busy} onClick={() => void load(true)}>
              {t("Older observations")}
            </Button>
          )}
        </>
      )}
    </div>
  );
}

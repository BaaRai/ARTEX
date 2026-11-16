"use client";

import * as React from "react";

import { Loader2Icon, PlusIcon, SendIcon, XIcon } from "lucide-react";

import { HttpCodeBlock, statusTone } from "@/components/http-code-block";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { ApiError, api } from "@/lib/api";
import { parseRawRequest, splitReplayHeaders } from "@/lib/replay";
import type { TrafficExchange, TrafficReplayHeader, TrafficReplayResult } from "@/lib/types";
import { cn } from "@/lib/utils";

type HeaderRow = TrafficReplayHeader & { key: number };

type ReplayForm = {
  method: string;
  url: string;
  rows: HeaderRow[];
  body: string;
  // Headers of the recorded request that the server would not send.
  dropped: string[];
  binary: boolean;
  bodyPreviewOnly: boolean;
};

let rowKey = 0;
const toRows = (headers: TrafficReplayHeader[]): HeaderRow[] => headers.map((h) => ({ ...h, key: ++rowKey }));

function initialForm(exchange: TrafficExchange, raw: string): ReplayForm {
  const parsed = parseRawRequest(raw);
  const { sent, notSent } = splitReplayHeaders(parsed.headers);
  return {
    method: exchange.method.toUpperCase(),
    url: exchange.url,
    rows: toRows(sent),
    body: parsed.binary ? "" : parsed.body,
    dropped: notSent.filter((n) => n.toLowerCase() !== "host"),
    binary: parsed.binary,
    bodyPreviewOnly: parsed.bodyPreviewOnly,
  };
}

const ERROR_TITLES: Record<number, string> = {
  400: "Invalid request",
  413: "Request body too large",
  502: "Proxy or upstream error",
  503: "Traffic capture is disabled",
  504: "Timed out",
};

// The server's message, under a title that says what the status means.
function describeError(e: unknown): { title: string; message: string } {
  const message = e instanceof Error ? e.message : String(e);
  if (!(e instanceof ApiError)) return { title: "Replay failed", message };
  return { title: ERROR_TITLES[e.status] ?? `Replay failed (${e.status})`, message };
}

// ReplayPanel edits the selected exchange's request and sends it again through
// the proxy. Host comes from the URL and hop-by-hop headers are not sent.
export function ReplayPanel({
  exchange,
  raw,
  methods,
  onRecorded,
}: {
  exchange: TrafficExchange;
  // The recorded request, with Host filled in.
  raw: string;
  methods: string[];
  // Called when the replayed exchange was recorded, so the list can refetch.
  onRecorded: () => void;
}) {
  const [form, setForm] = React.useState<ReplayForm>(() => initialForm(exchange, raw));
  const [sending, setSending] = React.useState(false);
  const [result, setResult] = React.useState<TrafficReplayResult | null>(null);
  const [error, setError] = React.useState<{ title: string; message: string } | null>(null);
  // Answers for an older exchange are dropped.
  const generation = React.useRef(0);
  const methodListId = React.useId();

  React.useEffect(() => {
    generation.current++;
    setForm(initialForm(exchange, raw));
    setSending(false);
    setResult(null);
    setError(null);
  }, [exchange, raw]);

  const update = (patch: Partial<ReplayForm>) => setForm((f) => ({ ...f, ...patch }));
  const updateRow = (key: number, patch: Partial<TrafficReplayHeader>) =>
    setForm((f) => ({ ...f, rows: f.rows.map((r) => (r.key === key ? { ...r, ...patch } : r)) }));

  // Rows the user typed that the server would not send either.
  const ignored = new Set(splitReplayHeaders(form.rows).notSent.map((n) => n.toLowerCase()));

  const send = () => {
    const gen = ++generation.current;
    setSending(true);
    setError(null);
    setResult(null);
    api
      .trafficReplay({
        method: form.method.trim(),
        url: form.url.trim(),
        headers: splitReplayHeaders(form.rows).sent,
        body: form.body,
        body_base64: false,
      })
      .then((r) => {
        if (gen !== generation.current) return;
        setResult(r);
        if (r.recorded) onRecorded();
      })
      .catch((e: unknown) => {
        if (gen === generation.current) setError(describeError(e));
      })
      .finally(() => {
        if (gen === generation.current) setSending(false);
      });
  };

  const canSend =
    !sending && !form.binary && !form.bodyPreviewOnly && form.method.trim() !== "" && form.url.trim() !== "";
  // Repeated headers (Set-Cookie) share a name, so each keeps its original position as key.
  const sortedHeaders = (result?.headers ?? [])
    .map((h, position) => ({ ...h, position }))
    .sort((a, b) => a.name.toLowerCase().localeCompare(b.name.toLowerCase()));

  return (
    <div className="space-y-4 p-5">
      <div className="flex gap-2">
        <Input
          aria-label="Method"
          className="w-32 font-mono uppercase"
          list={methodListId}
          value={form.method}
          onChange={(e) => update({ method: e.target.value.toUpperCase() })}
        />
        <datalist id={methodListId}>
          {methods.map((m) => (
            <option key={m} value={m} />
          ))}
        </datalist>
        <Input
          aria-label="URL"
          className="flex-1 font-mono"
          value={form.url}
          onChange={(e) => update({ url: e.target.value })}
        />
      </div>

      <div className="space-y-2">
        <div className="flex items-center justify-between">
          <span className="font-medium text-sm">Headers</span>
          <Button
            size="sm"
            variant="outline"
            onClick={() => update({ rows: [...form.rows, ...toRows([{ name: "", value: "" }])] })}
          >
            <PlusIcon />
            Add header
          </Button>
        </div>
        <p className="text-muted-foreground text-xs">Host is taken from the URL.</p>
        {form.dropped.length > 0 && (
          <p className="text-muted-foreground text-xs">
            Not sent (hop-by-hop or recomputed by the server): {form.dropped.join(", ")}
          </p>
        )}
        {form.rows.map((row) => (
          <div key={row.key} className="flex items-center gap-2">
            <Input
              aria-label="Header name"
              className={cn("w-48 font-mono text-xs", ignored.has(row.name.trim().toLowerCase()) && "line-through")}
              placeholder="Name"
              value={row.name}
              onChange={(e) => updateRow(row.key, { name: e.target.value })}
            />
            <Input
              aria-label="Header value"
              className="flex-1 font-mono text-xs"
              placeholder="Value"
              value={row.value}
              onChange={(e) => updateRow(row.key, { value: e.target.value })}
            />
            <Button
              size="icon"
              variant="ghost"
              aria-label="Remove header"
              onClick={() => update({ rows: form.rows.filter((r) => r.key !== row.key) })}
            >
              <XIcon />
            </Button>
          </div>
        ))}
        {ignored.size > 0 && (
          <p className="text-muted-foreground text-xs">Struck-through headers are not sent by the server.</p>
        )}
      </div>

      <div className="space-y-2">
        <span className="font-medium text-sm">Body</span>
        {form.binary ? (
          <p className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-amber-700 text-xs dark:text-amber-400">
            The recorded body is binary (it contains NUL bytes). Replaying a binary body is not supported.
          </p>
        ) : (
          <>
            <Textarea
              aria-label="Body"
              className="font-mono text-xs"
              value={form.body}
              onChange={(e) => update({ body: e.target.value })}
            />
            {form.bodyPreviewOnly && (
              <p className="text-amber-700 text-xs dark:text-amber-400">
                The recorded body was too large to keep inline; only its preview is loaded, so replay is disabled for
                this exchange.
              </p>
            )}
          </>
        )}
      </div>

      <Button onClick={send} disabled={!canSend}>
        {sending ? <Loader2Icon className="animate-spin" /> : <SendIcon />}
        Send
      </Button>

      {error && (
        <div className="space-y-1 rounded-md border border-red-500/40 bg-red-500/10 p-3 text-xs">
          <div className="font-medium text-red-600 dark:text-red-400">{error.title}</div>
          <div className="break-all">{error.message}</div>
        </div>
      )}

      {result && (
        <div className="space-y-3">
          <div className="flex flex-wrap items-center gap-2 text-xs">
            <Badge variant="secondary" className={cn("font-mono tabular-nums", statusTone(result.status))}>
              {result.status}
            </Badge>
            <span className="text-muted-foreground tabular-nums">{result.duration_ms} ms</span>
            {result.truncated && <Badge variant="outline">truncated</Badge>}
          </div>
          {!result.recorded && (
            <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3 text-amber-700 text-xs dark:text-amber-400">
              Passthrough host: the response is shown here but was not recorded.
            </div>
          )}
          {sortedHeaders.length > 0 && (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Header</TableHead>
                  <TableHead>Value</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {sortedHeaders.map((h) => (
                  <TableRow key={h.position}>
                    <TableCell className="align-top font-mono text-xs">{h.name}</TableCell>
                    <TableCell className="whitespace-normal break-all font-mono text-xs">{h.value}</TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
          <div className="-mx-5">
            <HttpCodeBlock raw={result.raw} />
          </div>
        </div>
      )}
    </div>
  );
}

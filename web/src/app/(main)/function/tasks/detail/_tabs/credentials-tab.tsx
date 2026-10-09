"use client";

import * as React from "react";

import { CopyIcon, EyeIcon, EyeOffIcon, PlusIcon, TrashIcon } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api";
import type { TaskCredential } from "@/lib/types";

// Credentials tab (#3): the task's test accounts — user-declared (program alias
// accounts) and agent-recorded (accounts the workers registered themselves).
// Passwords render masked; reveal and copy are explicit.
export function CredentialsTab({ taskId }: { taskId: string }) {
  const [items, setItems] = React.useState<TaskCredential[] | null>(null);
  const [error, setError] = React.useState("");
  const [revealed, setRevealed] = React.useState<Set<number>>(new Set());
  const [domain, setDomain] = React.useState("");
  const [username, setUsername] = React.useState("");
  const [password, setPassword] = React.useState("");
  const [note, setNote] = React.useState("");
  const [adding, setAdding] = React.useState(false);

  const load = React.useCallback(() => {
    api
      .taskCredentials(taskId)
      .then((r) => {
        setItems(r);
        setError("");
      })
      .catch((e) => setError((e as Error).message));
  }, [taskId]);

  React.useEffect(() => {
    load();
    // Agent runs can record credentials mid-task; a light poll keeps the tab current.
    const timer = setInterval(load, 15000);
    return () => clearInterval(timer);
  }, [load]);

  async function add() {
    if (!domain.trim() || !username.trim()) return;
    setAdding(true);
    try {
      await api.addTaskCredential(taskId, {
        domain: domain.trim(),
        username: username.trim(),
        password, // empty = "agent generates and records it"
        note: note.trim(),
      });
      setDomain("");
      setUsername("");
      setPassword("");
      setNote("");
      load();
    } catch (e) {
      setError((e as Error).message);
    } finally {
      setAdding(false);
    }
  }

  async function remove(id: number) {
    try {
      await api.deleteTaskCredential(taskId, id);
      load();
    } catch (e) {
      setError((e as Error).message);
    }
  }

  function toggle(id: number) {
    setRevealed((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>Test accounts</CardTitle>
        <CardDescription>
          Test accounts for this task. Entries with an empty password mean the agent registers that account and records it. Agent-recorded accounts appear here automatically.
        </CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4">
        {error ? <p className="text-destructive text-sm">{error}</p> : null}
        {items === null ? (
          <Skeleton className="h-24 w-full" />
        ) : items.length === 0 ? (
          <Empty>
            <EmptyHeader>
              <EmptyTitle>No test accounts</EmptyTitle>
              <EmptyDescription>
                Add one below, or declare them when creating the task — the agent reuses them for authenticated testing.
              </EmptyDescription>
            </EmptyHeader>
          </Empty>
        ) : (
          <div className="flex flex-col divide-y">
            {items.map((c) => (
              <div key={c.id} className="flex flex-wrap items-center gap-2 py-2.5">
                <code className="text-sm font-medium">{c.domain}</code>
                <span className="text-muted-foreground text-sm">{c.username}</span>
                <Badge variant={c.source === "agent" ? "secondary" : "outline"}>
                  {c.source === "agent" ? "agent-recorded" : "declared"}
                </Badge>
                <div className="ml-auto flex items-center gap-1">
                  <code className="max-w-40 truncate rounded bg-muted px-2 py-0.5 text-sm">
                    {c.password === "" ? "— agent generates —" : revealed.has(c.id) ? c.password : "•".repeat(Math.min(c.password.length, 12))}
                  </code>
                  {c.password !== "" ? (
                    <>
                      <Button size="icon" variant="ghost" className="size-7" aria-label={revealed.has(c.id) ? "Hide password" : "Reveal password"} onClick={() => toggle(c.id)}>
                        {revealed.has(c.id) ? <EyeOffIcon /> : <EyeIcon />}
                      </Button>
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-7"
                        aria-label="Copy password"
                        onClick={() => navigator.clipboard?.writeText(c.password).catch(() => {})}
                      >
                        <CopyIcon />
                      </Button>
                    </>
                  ) : null}
                  <Button size="icon" variant="ghost" className="size-7" aria-label="Delete credential" onClick={() => remove(c.id)}>
                    <TrashIcon />
                  </Button>
                </div>
                {c.note ? <p className="text-muted-foreground w-full text-xs">{c.note}</p> : null}
              </div>
            ))}
          </div>
        )}
        <div className="grid gap-2 sm:grid-cols-[1fr_1fr_1fr_auto]">
          <Input placeholder="domain (e.g. target.example.com)" value={domain} onChange={(e) => setDomain(e.target.value)} />
          <Input placeholder="email or username" value={username} onChange={(e) => setUsername(e.target.value)} />
          <Input
            placeholder="password (empty = agent generates)"
            type="text"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          <Button variant="outline" disabled={adding || !domain.trim() || !username.trim()} onClick={add}>
            <PlusIcon /> Add
          </Button>
          <Input className="sm:col-span-4" placeholder="note (optional, e.g. program alias account)" value={note} onChange={(e) => setNote(e.target.value)} />
        </div>
      </CardContent>
    </Card>
  );
}

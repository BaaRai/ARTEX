"use client";

import Link from "next/link";

import {
  ArrowRightIcon,
  CheckCircle2Icon,
  CircleIcon,
  HourglassIcon,
  OctagonAlertIcon,
  ShieldAlertIcon,
  XCircleIcon,
} from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { APPROVALS_URL } from "@/hooks/use-pending-approvals";
import type { Finding, LLMProfile, Task } from "@/lib/types";
import { cn } from "@/lib/utils";

interface SetupStep {
  done: boolean;
  label: string;
  hint: string;
  href: string;
  action: string;
}

/**
 * First-run path. Shown until a model is active and a task exists, so a new user is never left
 * on a dashboard of zeros wondering why nothing runs. Disappears by itself once complete.
 */
export function SetupChecklist({
  loaded,
  profiles,
  llmConfigured,
  taskCount,
}: {
  /** Don't flash the checklist before the first load finishes. */
  loaded: boolean;
  profiles: LLMProfile[];
  llmConfigured: boolean;
  taskCount: number;
}) {
  const hasProfile = profiles.length > 0;
  const hasActive = llmConfigured || profiles.some((p) => p.is_default);
  const steps: SetupStep[] = [
    {
      done: hasProfile,
      label: "Add a model",
      hint: "Agents need an LLM endpoint and API key to think.",
      href: "/system/llm",
      action: "Add model",
    },
    {
      done: hasActive,
      label: "Make one model active",
      hint: "The active profile (starred) is the one agents use unless told otherwise.",
      href: "/system/llm",
      action: "Choose model",
    },
    {
      done: taskCount > 0,
      label: "Create your first task",
      hint: "Describe the target and the goal; agents start exploring right away.",
      href: "/function/tasks?new=1",
      action: "New task",
    },
  ];
  const remaining = steps.filter((s) => !s.done).length;
  if (!loaded || remaining === 0) return null;
  const next = steps.find((s) => !s.done);

  return (
    <Card className="gap-3 border-info/30 bg-info/5">
      <CardHeader>
        <CardTitle className="text-base">Get ARTEX ready</CardTitle>
        <CardDescription>
          {steps.length - remaining} of {steps.length} done
        </CardDescription>
      </CardHeader>
      <CardContent>
        <ol className="flex flex-col gap-2">
          {steps.map((s) => (
            <li key={s.label} className="flex items-start gap-3">
              {s.done ? (
                <CheckCircle2Icon className="mt-0.5 size-4 shrink-0 text-success" aria-hidden />
              ) : (
                <CircleIcon className="mt-0.5 size-4 shrink-0 text-muted-foreground" aria-hidden />
              )}
              <div className="min-w-0 flex-1">
                <div className={cn("font-medium text-sm", s.done && "text-muted-foreground line-through")}>
                  {s.label}
                  <span className="sr-only">{s.done ? " (done)" : " (to do)"}</span>
                </div>
                {!s.done && <div className="text-muted-foreground text-xs">{s.hint}</div>}
              </div>
              {s === next && (
                <Link
                  prefetch={false}
                  href={s.href}
                  className="inline-flex h-7 shrink-0 items-center gap-1 rounded-md bg-primary px-2.5 font-medium text-primary-foreground text-xs hover:bg-primary/90 focus-visible:outline-2 focus-visible:outline-ring focus-visible:outline-offset-2"
                >
                  {s.action}
                  <ArrowRightIcon className="size-3" aria-hidden />
                </Link>
              )}
            </li>
          ))}
        </ol>
      </CardContent>
    </Card>
  );
}

interface AttentionItem {
  key: string;
  icon: typeof ShieldAlertIcon;
  tone: string;
  text: string;
  href: string;
}

/**
 * What needs a person right now, at the top of the dashboard: approvals the agents are blocked
 * on, stalled tasks, tasks that failed in the last day, and critical findings nobody has reviewed.
 * Empty = all clear.
 */
export function NeedsAttention({
  loaded,
  pendingApprovals,
  tasks,
  findings,
  llmConfigured,
}: {
  loaded: boolean;
  pendingApprovals: number;
  tasks: Task[];
  findings: Finding[];
  llmConfigured: boolean;
}) {
  if (!loaded) return null;

  // Only failures from the last day: an old failed task should not keep the strip lit forever.
  // A failure with no usable timestamp counts as recent, so a missing field never hides it.
  const since = Date.now() / 1000 - 24 * 3600;
  // 0 means unset in these fields, so take the first positive one.
  const failedAt = (t: Task) =>
    [t.completed_unix, t.last_activity_unix, t.created_unix].find((v) => (v ?? 0) > 0) ?? Number.POSITIVE_INFINITY;
  const failed = tasks.filter((t) => (t.status === "failed" || t.status === "timeout") && failedAt(t) >= since).length;
  // engine_mode comes from the task list (server/engine_mode.go): a running intent with no model
  // call and no activity for over two minutes.
  const stalled = tasks.filter((t) => t.status === "running" && t.engine_mode === "stalled");
  const unreviewedCritical = findings.filter((f) => f.severity === "critical" && f.status === "pending").length;
  const plural = (n: number, one: string, many: string) => `${n} ${n === 1 ? one : many}`;

  const items: AttentionItem[] = [];
  if (pendingApprovals > 0)
    items.push({
      key: "approvals",
      icon: ShieldAlertIcon,
      tone: "text-warning",
      text: `${plural(pendingApprovals, "tool call is", "tool calls are")} waiting for your approval`,
      href: APPROVALS_URL,
    });
  if (!llmConfigured)
    items.push({
      key: "llm",
      icon: XCircleIcon,
      tone: "text-destructive",
      text: "No active model: agents can't run",
      href: "/system/llm",
    });
  if (stalled.length > 0)
    items.push({
      key: "stalled",
      icon: HourglassIcon,
      tone: "text-warning",
      text:
        stalled.length === 1
          ? `“${stalled[0].name || stalled[0].description || `Task ${stalled[0].id}`}” looks stuck: no activity for over 2 minutes`
          : `${stalled.length} tasks look stuck: no activity for over 2 minutes`,
      href: stalled.length === 1 ? `/function/tasks/detail?id=${encodeURIComponent(stalled[0].id)}` : "/function/tasks",
    });
  if (failed > 0)
    items.push({
      key: "failed",
      icon: XCircleIcon,
      tone: "text-destructive",
      text: `${plural(failed, "task", "tasks")} failed or timed out in the last 24 hours`,
      href: "/function/tasks",
    });
  if (unreviewedCritical > 0)
    items.push({
      key: "critical",
      icon: OctagonAlertIcon,
      tone: "text-sev-critical",
      text: `${plural(unreviewedCritical, "critical finding", "critical findings")} not reviewed yet`,
      href: "/function/findings",
    });

  if (items.length === 0) {
    return (
      <div className="flex items-center gap-2 rounded-lg border bg-muted/20 px-3 py-2 text-muted-foreground text-sm">
        <CheckCircle2Icon className="size-4 text-success" aria-hidden />
        Nothing needs your attention right now.
      </div>
    );
  }

  return (
    <section aria-labelledby="needs-attention" className="rounded-lg border border-warning/40 bg-warning/5 p-3">
      <h2 id="needs-attention" className="mb-2 font-semibold text-sm">
        Needs attention
      </h2>
      <ul className="flex flex-col gap-1">
        {items.map(({ key, icon: Icon, tone, text, href }) => (
          <li key={key}>
            <Link
              prefetch={false}
              href={href}
              className="group flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-background/60 focus-visible:outline-2 focus-visible:outline-ring focus-visible:outline-offset-2"
            >
              <Icon className={cn("size-4 shrink-0", tone)} aria-hidden />
              <span className="flex-1">{text}</span>
              <ArrowRightIcon className="size-3.5 text-muted-foreground group-hover:text-foreground" aria-hidden />
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

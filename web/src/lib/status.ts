// Centralised status → color/label semantics, reused across the whole app.
// Spec §8.3: intent / coverage / task / severity each have a consistent color set.

import { t } from "@/i18n";

// Semantic tones map onto the --success / --warning / --info / --destructive / --sev-* tokens
// in globals.css. Each hue has one meaning:
//   info (blue)     the system is working on it: running, in progress, sending
//   success (green) finished well
//   warning (amber) stopped and waiting on a person: paused
//   danger (red)    failed or blocked
//   slate/neutral   waiting its turn or no longer relevant: queued, pending, ignored
// Severity has its own scale (sev-*) so "Medium" never looks like a status.
export type Tone =
  | "neutral"
  | "slate"
  | "info"
  | "success"
  | "warning"
  | "danger"
  | "violet"
  | "sev-critical"
  | "sev-high"
  | "sev-medium"
  | "sev-low";

export const toneClasses: Record<Tone, string> = {
  neutral: "bg-muted text-muted-foreground border-transparent",
  slate: "bg-slate-500/10 text-slate-700 dark:text-slate-300 border-slate-500/25",
  info: "bg-info/12 text-info border-info/25",
  success: "bg-success/12 text-success border-success/25",
  warning: "bg-warning/12 text-warning border-warning/30",
  danger: "bg-destructive/10 text-destructive border-destructive/25",
  violet: "bg-violet-500/12 text-violet-700 dark:text-violet-300 border-violet-500/25",
  // Critical is the only filled badge (plus an icon, see StatusBadge); High is an outline in a
  // different hue, so the two never read as the same level.
  "sev-critical": "bg-sev-critical text-white border-sev-critical",
  "sev-high": "bg-transparent text-sev-high border-sev-high",
  "sev-medium": "bg-sev-medium/12 text-sev-medium border-sev-medium/30",
  "sev-low": "bg-sev-low/12 text-sev-low border-sev-low/30",
};

export const toneDot: Record<Tone, string> = {
  neutral: "bg-muted-foreground",
  slate: "bg-slate-500",
  info: "bg-info",
  success: "bg-success",
  warning: "bg-warning",
  danger: "bg-destructive",
  violet: "bg-violet-500",
  "sev-critical": "bg-white",
  "sev-high": "bg-sev-high",
  "sev-medium": "bg-sev-medium",
  "sev-low": "bg-sev-low",
};

/** Text colour for a severity count ("3" critical) where a full badge is too heavy. */
export const severityText: Record<"critical" | "high" | "medium" | "low", string> = {
  critical: "text-sev-critical",
  high: "text-sev-high",
  medium: "text-sev-medium",
  low: "text-sev-low",
};

interface StatusMeta {
  label: string;
  tone: Tone;
}

const intent: Record<string, StatusMeta> = {
  open: { label: t("Open"), tone: "slate" },
  running: { label: t("Running"), tone: "info" },
  paused: { label: t("Paused"), tone: "warning" },
  done: { label: t("Completed"), tone: "success" },
  // blocked = model/API/network failures exhausted the retries; this intent barely explored anything (not an interception).
  blocked: { label: t("Error"), tone: "danger" },
  // exhausted = the step/time budget cut the run short midway, only partial results written back (not "direction exhausted").
  exhausted: { label: t("Budget exhausted"), tone: "violet" },
  // stopped = legacy soft-deleted state (kept for historical data).
  stopped: { label: t("Stopped"), tone: "slate" },
  // deleted = the user soft-deleted this intent (nodes and lineage kept; see delete_reason for the reason).
  deleted: { label: t("Deleted"), tone: "slate" },
};

const task: Record<string, StatusMeta> = {
  created: { label: t("Created"), tone: "slate" },
  queued: { label: t("Queued"), tone: "slate" },
  running: { label: t("Running"), tone: "info" },
  paused: { label: t("Paused"), tone: "warning" },
  done: { label: t("Completed"), tone: "success" },
  failed: { label: t("Failed"), tone: "danger" },
  timeout: { label: t("Timed out"), tone: "danger" },
};

const severity: Record<string, StatusMeta> = {
  critical: { label: t("Critical"), tone: "sev-critical" },
  high: { label: t("High"), tone: "sev-high" },
  medium: { label: t("Medium"), tone: "sev-medium" },
  low: { label: t("Low"), tone: "sev-low" },
};

const finding: Record<string, StatusMeta> = {
  pending: { label: t("Pending"), tone: "slate" },
  in_progress: { label: t("In progress"), tone: "info" },
  confirmed: { label: t("Confirmed"), tone: "danger" },
  resolved: { label: t("Resolved"), tone: "success" },
  fixed: { label: t("Fixed"), tone: "success" },
  false_positive: { label: t("False positive"), tone: "slate" },
  ignored: { label: t("Ignored"), tone: "neutral" },
  duplicate: { label: t("Duplicate"), tone: "neutral" },
  risk_accepted: { label: t("Risk accepted"), tone: "violet" },
};

const engine: Record<string, StatusMeta> = {
  exploring: { label: t("Exploring"), tone: "info" },
  paused: { label: t("Paused"), tone: "warning" },
  stalled: { label: t("Stalled"), tone: "danger" },
  idle: { label: t("Idle"), tone: "neutral" },
};

const goal: Record<string, StatusMeta> = {
  open: { label: t("In progress"), tone: "info" },
  met: { label: t("Met"), tone: "success" },
  abandoned: { label: t("Abandoned"), tone: "slate" },
};

const audit: Record<string, StatusMeta> = {
  allow: { label: t("Allow"), tone: "success" },
  block: { label: t("Block"), tone: "danger" },
};

const node: Record<string, StatusMeta> = {
  observed: { label: t("Observed"), tone: "slate" },
  confirmed: { label: t("Confirmed"), tone: "success" },
  tombstoned: { label: t("Tombstoned"), tone: "neutral" },
};

// Notification delivery states. sending is info (claimed and sending now), pending is slate
// (waiting its turn); neither is a problem.
const delivery: Record<string, StatusMeta> = {
  pending: { label: t("Pending"), tone: "slate" },
  sending: { label: t("Sending"), tone: "info" },
  sent: { label: t("Sent"), tone: "success" },
  failed: { label: t("Failed"), tone: "danger" },
  skipped: { label: t("Skipped"), tone: "neutral" },
};

const maps = {
  intent,
  task,
  severity,
  finding,
  engine,
  goal,
  audit,
  node,
  delivery,
} as const;

export type StatusDomain = keyof typeof maps;

export function statusMeta(domain: StatusDomain, key: string): StatusMeta {
  return maps[domain][key] ?? { label: t(key), tone: "neutral" };
}

/**
 * One primary status per task. A running task adds what its engine is doing as a sub-label
 * ("Running · idle"), so the list, the detail page and the dashboard never show two
 * different words for the same task. A stalled engine turns the badge red.
 */
export function taskStatusMeta(status: string, engineMode?: string): StatusMeta & { detail?: string } {
  const base = statusMeta("task", status);
  if (status !== "running" || !engineMode) return base;
  if (engineMode === "paused") return statusMeta("task", "paused");
  const engineMeta = statusMeta("engine", engineMode);
  return {
    ...base,
    detail: engineMeta.label.toLowerCase(),
    tone: engineMode === "stalled" ? "danger" : base.tone,
  };
}

import { OctagonAlertIcon } from "lucide-react";

import { engineGlossary, glossary } from "@/lib/glossary";
import { type StatusDomain, statusMeta, type Tone, taskStatusMeta, toneClasses, toneDot } from "@/lib/status";
import { cn } from "@/lib/utils";

// Hover text for the states whose label alone is easy to misread.
const STATE_HELP: Partial<Record<StatusDomain, Record<string, string>>> = {
  intent: { exhausted: glossary.intentExhausted, blocked: glossary.intentError },
  engine: engineGlossary,
};

export function StatusBadge({
  domain,
  value,
  dot = false,
  className,
}: {
  domain: StatusDomain;
  value: string;
  dot?: boolean;
  className?: string;
}) {
  const meta = statusMeta(domain, value);
  return (
    <span
      title={STATE_HELP[domain]?.[value]}
      className={cn(
        "inline-flex items-center gap-1.5 whitespace-nowrap rounded-md border px-2 py-0.5 font-medium text-xs",
        toneClasses[meta.tone],
        className,
      )}
    >
      {meta.tone === "sev-critical" ? (
        // Critical is told apart by shape as well as colour (WCAG 1.4.1).
        <OctagonAlertIcon className="size-3" aria-hidden />
      ) : (
        dot && <span className={cn("size-1.5 rounded-full", toneDot[meta.tone])} />
      )}
      {meta.label}
    </span>
  );
}

export function ToneDot({ tone }: { tone: Tone }) {
  return <span className={cn("size-2 rounded-full", toneDot[tone])} />;
}

/** One badge for finding severity everywhere: Critical filled with an icon, High outlined, Medium and Low soft. */
export function SeverityBadge({ value, className }: { value: string; className?: string }) {
  return <StatusBadge domain="severity" value={value} dot className={className} />;
}

/** A task's single status: "Running · idle", "Paused", "Completed". See taskStatusMeta. */
export function TaskStatusBadge({
  status,
  engineMode,
  className,
}: {
  status: string;
  engineMode?: string;
  className?: string;
}) {
  const meta = taskStatusMeta(status, engineMode);
  return (
    <span
      title={meta.detail && engineMode ? engineGlossary[engineMode] : undefined}
      className={cn(
        "inline-flex items-center gap-1.5 whitespace-nowrap rounded-md border px-2 py-0.5 font-medium text-xs",
        toneClasses[meta.tone],
        className,
      )}
    >
      <span className={cn("size-1.5 rounded-full", toneDot[meta.tone])} />
      {meta.label}
      {meta.detail ? <span className="font-normal opacity-80"> · {meta.detail}</span> : null}
    </span>
  );
}

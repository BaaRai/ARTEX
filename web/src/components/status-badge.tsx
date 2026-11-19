"use client";

import { cn } from "@/lib/utils";
import {
  toneClasses,
  toneDot,
  type StatusDomain,
  type Tone,
} from "@/lib/status";
import { useStatusMeta } from "@/lib/use-status-meta";

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
  const statusMeta = useStatusMeta();
  const meta = statusMeta(domain, value);
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md border px-2 py-0.5 text-xs font-medium whitespace-nowrap",
        toneClasses[meta.tone],
        className,
      )}
    >
      {dot && (
        <span className={cn("size-1.5 rounded-full", toneDot[meta.tone])} />
      )}
      {meta.label}
    </span>
  );
}

export function ToneDot({ tone }: { tone: Tone }) {
  return <span className={cn("size-2 rounded-full", toneDot[tone])} />;
}

"use client";

import type { ReactNode } from "react";

import { InfoIcon } from "lucide-react";

import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { t } from "@/i18n";
import { type GlossaryTerm, glossary } from "@/lib/glossary";
import { cn } from "@/lib/utils";

/**
 * An agent term with its plain-language definition one hover or Tab away. The info button is
 * a real button, so keyboard and screen-reader users get the definition too.
 */
export function Term({
  name,
  children,
  className,
}: {
  name: GlossaryTerm;
  /** The visible word; omit to show only the info icon (e.g. next to an existing label). */
  children?: ReactNode;
  className?: string;
}) {
  const definition = glossary[name];
  return (
    <span className={cn("inline-flex items-center gap-0.5", className)}>
      {children}
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            aria-label={t("What does this mean? {definition}", { definition })}
            className="inline-flex size-4 shrink-0 items-center justify-center rounded-full text-muted-foreground hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
          >
            <InfoIcon className="size-3" aria-hidden />
          </button>
        </TooltipTrigger>
        <TooltipContent className="max-w-xs text-pretty">{definition}</TooltipContent>
      </Tooltip>
    </span>
  );
}

/** "What do these mean?" next to a row of term-labelled filters or a legend. */
export function TermLegend({ terms, label = t("What do these mean?") }: { terms: GlossaryTerm[]; label?: string }) {
  return (
    <Popover>
      <PopoverTrigger asChild>
        <button
          type="button"
          className="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-muted-foreground text-xs hover:bg-accent hover:text-foreground focus-visible:outline-2 focus-visible:outline-ring"
        >
          <InfoIcon className="size-3" aria-hidden />
          {label}
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" className="w-80">
        <dl className="flex flex-col gap-2 text-xs">
          {terms.map((t) => (
            <div key={t}>
              <dt className="font-medium capitalize">{t}</dt>
              <dd className="text-muted-foreground">{glossary[t]}</dd>
            </div>
          ))}
        </dl>
      </PopoverContent>
    </Popover>
  );
}

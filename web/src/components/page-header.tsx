"use client";

import { Fragment, type ReactNode } from "react";

import Link from "next/link";

import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { useRouteMeta } from "@/hooks/use-route-meta";
import { cn } from "@/lib/utils";

/** "Tasks › Task name" above a detail page's title. Renders nothing on top-level pages. */
export function PageBreadcrumbs({ current, className }: { current?: ReactNode; className?: string }) {
  const { title, breadcrumbs } = useRouteMeta();
  if (breadcrumbs.length === 0) return null;
  return (
    <Breadcrumb className={className}>
      <BreadcrumbList className="text-xs">
        {breadcrumbs.map((c) => (
          // The separator is its own <li>, so it is a sibling of the item, not inside it.
          <Fragment key={c.url}>
            <BreadcrumbItem>
              <BreadcrumbLink asChild>
                <Link prefetch={false} href={c.url}>
                  {c.title}
                </Link>
              </BreadcrumbLink>
            </BreadcrumbItem>
            <BreadcrumbSeparator />
          </Fragment>
        ))}
        <BreadcrumbItem className="min-w-0">
          <BreadcrumbPage className="truncate">{current ?? title}</BreadcrumbPage>
        </BreadcrumbItem>
      </BreadcrumbList>
    </Breadcrumb>
  );
}

/**
 * The one page header: breadcrumbs on detail pages, an h1 that matches the nav label
 * (from navigation/route-meta), a one-line description and a right-aligned action slot.
 */
export function PageHeader({
  title,
  description,
  actions,
  meta,
  className,
}: {
  /** Defaults to the nav label for this route. Pass it only for detail pages (the object's name). */
  title?: ReactNode;
  description?: ReactNode;
  /** Primary and secondary actions, right-aligned. */
  actions?: ReactNode;
  /** Small inline extras after the title, e.g. a count badge. */
  meta?: ReactNode;
  className?: string;
}) {
  const route = useRouteMeta();
  const heading = title ?? route.title;
  return (
    <div className={cn("flex flex-col gap-1", className)}>
      <PageBreadcrumbs current={typeof heading === "string" ? heading : undefined} />
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <h1 className="truncate font-semibold text-xl tracking-tight">{heading}</h1>
            {meta}
          </div>
          {description !== undefined ? <p className="mt-0.5 text-muted-foreground text-sm">{description}</p> : null}
        </div>
        {actions !== undefined ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </div>
    </div>
  );
}

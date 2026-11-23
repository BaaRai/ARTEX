"use client";

import { useMemo } from "react";

import { usePathname } from "next/navigation";

import { type RouteMeta, routeMeta } from "@/navigation/route-meta";
import { sidebarItems } from "@/navigation/sidebar/sidebar-items";

function decode(p: string) {
  try {
    return decodeURIComponent(p);
  } catch {
    return p;
  }
}

/** Title, group and breadcrumbs for the current page, from the sidebar definition. */
export function useRouteMeta(): RouteMeta {
  const pathname = usePathname();
  return useMemo(() => routeMeta(sidebarItems, decode(pathname ?? "/")), [pathname]);
}

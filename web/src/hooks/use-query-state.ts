"use client";

import * as React from "react";

import { usePathname } from "next/navigation";

import { NAVIGATION_EVENT, readQuery, updateQuery } from "@/lib/navigation";

function subscribe(notify: () => void) {
  window.addEventListener("popstate", notify);
  window.addEventListener(NAVIGATION_EVENT, notify);
  return () => {
    window.removeEventListener("popstate", notify);
    window.removeEventListener(NAVIGATION_EVENT, notify);
  };
}

const snapshot = () => window.location.search;
const serverSnapshot = () => "";

// A stable SSR snapshot supports static export without a Suspense wrapper.
// Read the URL on back/forward rather than mirroring component state into it.
export function useQueryState(key: string, fallback: string, allowed?: readonly string[]) {
  usePathname(); // Re-read when a Next client navigation changes the page.
  const search = React.useSyncExternalStore(subscribe, snapshot, serverSnapshot);
  const value = readQuery(search, key, fallback, allowed);
  const setValue = React.useCallback(
    (next: React.SetStateAction<string>) => {
      const current = readQuery(window.location.search, key, fallback, allowed);
      const resolved = typeof next === "function" ? next(current) : next;
      if (resolved !== current && (!allowed || allowed.includes(resolved))) updateQuery({ [key]: resolved });
    },
    [key, fallback, allowed],
  );
  return [value, setValue] as const;
}

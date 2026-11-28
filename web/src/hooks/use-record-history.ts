"use client";

import * as React from "react";

import { usePathname } from "next/navigation";

import { useQueryState } from "@/hooks/use-query-state";

// Inspectors have no detail route. Cache visited rows in memory so back/forward
// can reopen them even after changing pages or filters. Do not put payloads in
// history.state, localStorage or URLs. A reload resolves against the loaded rows.
const visited = new Map<string, { id: string | number }>();

export function useRecordHistory<T extends { id: string | number }>(key: string, rows: readonly T[]) {
  const pathname = usePathname();
  const [id, setID] = useQueryState(key, "");
  const prefix = `${pathname}:${key}:`;
  const selected = (visited.get(prefix + id) as T | undefined) ?? rows.find((row) => String(row.id) === id) ?? null;
  const select = React.useCallback(
    (row: T | null) => {
      if (row) {
        const cacheKey = prefix + String(row.id);
        visited.delete(cacheKey);
        visited.set(cacheKey, row);
        // Bound retention when an operator inspects a large log.
        if (visited.size > 100) {
          const oldest = visited.keys().next().value;
          if (oldest !== undefined) visited.delete(oldest);
        }
      }
      setID(row === null ? "" : String(row.id));
    },
    [prefix, setID],
  );
  return [selected, select] as const;
}

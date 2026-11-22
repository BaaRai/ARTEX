"use client";

import { useSyncExternalStore } from "react";

import { api } from "@/lib/api";
import { setVisibleInterval } from "@/lib/polling";

// One shared poll of /api/intercept/pending for every consumer (sidebar badge,
// header pill, tab title), so adding indicators does not multiply requests.
const POLL_MS = 5000;

let count = 0;
let stopPolling: (() => void) | undefined;
const listeners = new Set<() => void>();

async function poll() {
  try {
    const list = await api.interceptPending();
    if (list.length !== count) {
      count = list.length;
      for (const l of listeners) l();
    }
  } catch {
    // Keep the last known count; the next poll retries.
  }
}

function subscribe(listener: () => void) {
  listeners.add(listener);
  if (!stopPolling) {
    void poll();
    stopPolling = setVisibleInterval(poll, POLL_MS);
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0 && stopPolling) {
      stopPolling();
      stopPolling = undefined;
    }
  };
}

const getSnapshot = () => count;
const getServerSnapshot = () => 0;

/** Number of tool calls currently waiting for a human allow/deny decision. */
export function usePendingApprovals(): number {
  return useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
}

export const APPROVALS_URL = "/system/intercept/approvals";

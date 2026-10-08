"use client";

import * as React from "react";

import Link from "next/link";

import { ArrowUpCircleIcon } from "lucide-react";

import { api } from "@/lib/api";

/**
 * Top-bar "new version" badge: checked once per full page load; lights up next to the version
 * number when an update exists, and clicking jumps to the "Version and updates" card on the system settings page.
 *
 * The backend caches GitHub query results for 30 minutes, so checking once per mount here is safe
 * — the unauthenticated GitHub API allows only 60 requests/hour/IP; without that cache layer, a few extra tabs
 * would drain the quota, and a real update check would then stall.
 *
 * Failed checks stay silent: the top bar is not the place for errors — the reason shows on the settings page's "Check for updates".
 */
export function UpdateBadge() {
  const [latest, setLatest] = React.useState("");

  React.useEffect(() => {
    let alive = true;
    api
      .checkUpdate()
      .then((r) => {
        // has_update already includes the "version is comparable" check, so development builds never light this up.
        if (alive && r.has_update && r.latest) setLatest(r.latest.replace(/^v(?=\d)/, ""));
      })
      .catch(() => {
        // Silent: no network / GitHub rate limiting must not raise an error in the top bar.
      });
    return () => {
      alive = false;
    };
  }, []);

  if (!latest) return null;

  return (
    <Link
      href="/system/settings"
      title={`New version ${latest} available — click to update`}
      className="inline-flex items-center gap-1.5 rounded-full bg-primary px-2.5 py-1 font-medium text-primary-foreground text-xs transition-opacity hover:opacity-90"
    >
      {/* Breathing dot: the top bar is crowded and plain text is easy to miss; the animation makes it instantly visible. */}
      <span className="relative flex size-1.5">
        <span className="absolute inline-flex size-full animate-ping rounded-full bg-primary-foreground opacity-75" />
        <span className="relative inline-flex size-1.5 rounded-full bg-primary-foreground" />
      </span>
      <ArrowUpCircleIcon className="size-3.5" />
      <span className="hidden sm:inline">New version {latest}</span>
      <span className="sm:hidden">New version</span>
    </Link>
  );
}

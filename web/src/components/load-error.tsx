"use client";

import { AlertCircleIcon, RefreshCwIcon } from "lucide-react";

import { Alert, AlertAction, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";

/**
 * Shown when a page's data failed to load. Never render defaults or "nothing here
 * yet" in that case: an empty list invites the user to recreate things that exist,
 * and default form values can overwrite the real config on Save.
 */
export function LoadError({
  what,
  error,
  onRetry,
  retrying = false,
}: {
  /** What failed, in plain words: "settings", "model profiles". */
  what: string;
  error?: string | null;
  onRetry: () => void;
  retrying?: boolean;
}) {
  return (
    <Alert variant="destructive" role="alert">
      <AlertCircleIcon />
      <AlertTitle>Couldn't load {what}</AlertTitle>
      <AlertDescription>
        {error ? `${error}. ` : ""}Nothing is shown or saved until it loads. Check that the ARTEX server is running,
        then try again.
      </AlertDescription>
      <AlertAction>
        <Button size="sm" variant="outline" onClick={onRetry} disabled={retrying}>
          <RefreshCwIcon className={retrying ? "animate-spin" : undefined} />
          Retry
        </Button>
      </AlertAction>
    </Alert>
  );
}

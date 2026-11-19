import type { LLMTransition } from "@/lib/types";

/**
 * Maps a structured LLM transition into an `llm` namespace message key + ICU values,
 * so the toast can be rendered in the active locale instead of showing the backend's
 * English `reason`/`summary`. Returns null when the transition lacks the profile
 * names a message needs, letting the caller fall back to the raw summary.
 */
export function llmTransitionToast(
  transition: LLMTransition,
): { key: string; values: Record<string, string> } | null {
  const from = transition.previous?.name || transition.previous?.model;
  const to = transition.next?.name || transition.next?.model;
  switch (transition.mode) {
    case "exhausted":
      return from ? { key: "transition.exhausted", values: { from } } : null;
    case "automatic":
      return from && to ? { key: "transition.automatic", values: { from, to } } : null;
    case "manual":
      return from && to ? { key: "transition.manual", values: { from, to } } : null;
    default:
      return null;
  }
}

// Only explicit navigation writes history. Rendering and polling never do.
export const NAVIGATION_EVENT = "artex:navigation";

export function updateQuery(values: Record<string, string | null>, mode: "push" | "replace" = "push") {
  const url = new URL(window.location.href);
  for (const [key, value] of Object.entries(values)) {
    if (value === null) url.searchParams.delete(key);
    else url.searchParams.set(key, value);
  }
  if (url.href === window.location.href) return;
  // Next patches these native methods to retain its router state and update
  // useSearchParams. Do not copy private Next history flags into a new entry.
  window.history[mode === "push" ? "pushState" : "replaceState"](null, "", url);
  window.dispatchEvent(new Event(NAVIGATION_EVENT));
}

export function readQuery(search: string, key: string, fallback: string, allowed?: readonly string[]) {
  const value = new URLSearchParams(search).get(key);
  return value !== null && (!allowed || allowed.includes(value)) ? value : fallback;
}

export function findingReturnHref(task: string | null) {
  return task ? `/function/tasks/detail?id=${encodeURIComponent(task)}&tab=findings` : "/function/findings";
}

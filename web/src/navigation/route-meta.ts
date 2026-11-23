// Route → title, group and breadcrumbs, derived from the sidebar so page titles, tab titles
// and nav labels cannot drift apart. Kept free of "@/" imports so node --test can load it.
import type { NavGroup } from "./sidebar/sidebar-items";

const APP_NAME = "ARTEX";

// Detail pages are query-string routes under their list page (static export has no dynamic segments).
const DETAIL_TITLES: Record<string, string> = {
  "/function/tasks/detail": "Task",
  "/function/findings/detail": "Finding",
  "/system/agents/detail": "Agent",
};

export interface Crumb {
  title: string;
  url: string;
}

export interface RouteMeta {
  /** Page title; matches the nav label for list pages. Undefined outside the nav (login, 404). */
  title?: string;
  /** Sidebar group label. */
  group?: string;
  /** Links above the page title, nearest parent last. Empty on top-level pages. */
  breadcrumbs: Crumb[];
  /** For document.title: "Findings · ARTEX", "Task · Tasks · ARTEX". */
  documentTitle: string;
}

export function normalizePath(p: string): string {
  const trimmed = p.replace(/\/+$/, "");
  return trimmed === "" ? "/" : trimmed;
}

function matchesUrl(path: string, url: string) {
  const u = normalizePath(url);
  return path === u || path.startsWith(`${u}/`);
}

function itemUrls(item: NavGroup["items"][number]): { url: string; title: string }[] {
  return "subItems" in item && item.subItems ? item.subItems : [{ url: item.url, title: item.title }];
}

/**
 * The single nav url that owns the path: the longest matching prefix, so a detail page
 * (/function/tasks/detail) belongs to its list and /system/intercept/assets does not
 * also claim /system/intercept.
 */
export function findActiveUrl(groups: readonly NavGroup[], path: string): string | undefined {
  const p = normalizePath(path);
  let best: string | undefined;
  for (const group of groups) {
    for (const item of group.items) {
      for (const { url } of itemUrls(item)) {
        if (matchesUrl(p, url) && (!best || normalizePath(url).length > normalizePath(best).length)) best = url;
      }
    }
  }
  return best;
}

export function routeMeta(groups: readonly NavGroup[], pathname: string): RouteMeta {
  const path = normalizePath(pathname);
  const activeUrl = findActiveUrl(groups, path);
  if (!activeUrl) return { breadcrumbs: [], documentTitle: APP_NAME };

  let listTitle = "";
  let group: string | undefined;
  for (const g of groups) {
    for (const item of g.items) {
      const hit = itemUrls(item).find((u) => u.url === activeUrl);
      if (hit) {
        listTitle = hit.title;
        group = g.label;
      }
    }
  }

  const listUrl = normalizePath(activeUrl);
  if (path === listUrl) {
    return { title: listTitle, group, breadcrumbs: [], documentTitle: `${listTitle} · ${APP_NAME}` };
  }

  const detail = DETAIL_TITLES[path] ?? listTitle;
  return {
    title: detail,
    group,
    breadcrumbs: [{ title: listTitle, url: listUrl }],
    documentTitle: `${detail} · ${listTitle} · ${APP_NAME}`,
  };
}

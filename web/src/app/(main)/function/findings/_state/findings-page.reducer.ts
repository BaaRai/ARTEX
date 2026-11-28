import { createList, isListAction, type ListAction, type ListState, listReducer } from "@/lib/reducers/list";
import {
  createPagination,
  isPaginationAction,
  type Pagination,
  type PaginationAction,
  paginationReducer,
  toFirstPage,
} from "@/lib/reducers/pagination";
import {
  createRequests,
  isRequestAction,
  type RequestAction,
  type Requests,
  requestReducer,
} from "@/lib/reducers/request";
import { isSelectionAction, type SelectionAction, selectionReducer } from "@/lib/reducers/selection";
import { mergePatch, setSlice } from "@/lib/reducers/slice";
import type {
  ActiveFindingRetest,
  Finding,
  FindingAssetNode,
  FindingGroup,
  FindingReportState,
  FindingStats,
  FindingStatus,
  Severity,
  VerificationStatus,
} from "@/lib/types";

import type { FindingEdit, FindingReport } from "../_components/findings-table";

// List views: flat = one big cross-task table (default); grouped = collapsible groups by task;
// asset = left asset tree + the findings under that subtree on the right.
export type FindingView = "flat" | "grouped" | "asset";

export const FINDING_VIEWS: FindingView[] = ["flat", "grouped", "asset"];

// One-shot snapshot of the asset tree. Unlike the other two views, the asset view doesn't poll:
// it re-queries on view entry, filter changes, or finding mutations made on this page.
export interface AssetTreeState {
  nodes: FindingAssetNode[];
  findingTotal: number;
  truncated: boolean;
  droppedKinds: string[];
  loaded: boolean;
  loading: boolean;
}

export const EMPTY_ASSET_TREE: AssetTreeState = {
  nodes: [],
  findingTotal: 0,
  truncated: false,
  droppedKinds: [],
  loaded: false,
  loading: false,
};

// In the grouped view, every expanded task group carries its own pagination state, independent of the others.
export interface GroupFindingsState {
  items: Finding[];
  total: number;
  page: number;
  pageSize: number;
  loaded: boolean;
  loading: boolean;
}

// The flat view's list (shared list slice). Its page number lives in its own pagination slice (not inside the
// snapshot), so a filter change resets it and triggers a reload.
export const EMPTY_FLAT_STATE: ListState<Finding> = createList();

export const EMPTY_STATS: FindingStats = {
  total: 0,
  pending: 0,
  submitted: 0,
  accepted: 0,
  critical: 0,
  high: 0,
  medium: 0,
  low: 0,
  vulnclasses: [],
  tasks: [],
};

export type FindingSort = "severity" | "time";
export type ExportScope = "filtered" | "all" | "selected";
export type ExportFormat = "md-single" | "md-zip" | "csv" | "json";

/** Finding filters shared by every view. */
export type FindingsFilters = {
  severity: "all" | Severity;
  status: "all" | FindingStatus;
  reportState: "all" | FindingReportState;
  verificationStatus: "all" | VerificationStatus;
  vulnclass: string;
  task: string;
  sort: FindingSort;
  /** Raw search box value, updated on every keystroke. */
  search: string;
  /** Trimmed, debounced search actually sent to the API. */
  query: string;
};

/** Filters persisted to localStorage (everything except the search box). */
export type FindingListPreferences = Omit<FindingsFilters, "search" | "query">;

export type AssetViewState = {
  tree: AssetTreeState;
  /** Selected asset node key; null = all assets. */
  scope: string | null;
};

export type GroupedViewState = {
  groups: FindingGroup[];
  groupTotal: number;
  /** Findings across all groups under the current filters (the group API's finding_total). */
  findingTotal: number;
  expanded: Set<string>;
  findings: Record<string, GroupFindingsState>;
};

export type FindingStatsState = {
  data: FindingStats;
  loaded: boolean;
};

/** User-triggered requests: bulk status/report-stage update, export, deepen submit, inline save. */
export type FindingsRequestKey = "bulkUpdate" | "export" | "deepen" | "save";

/** Export dialog: scope (current filter / all / selected) × format (single md / per-finding md zip / csv / json). */
export type ExportDialogState = {
  open: boolean;
  scope: ExportScope;
  format: ExportFormat;
};

export type DeepenDialogState = {
  finding: Finding | null;
  description: string;
};

export type RetestState = {
  /** Finding shown in the retest dialog. */
  finding: Finding | null;
  /** Active retests keyed by finding_id. */
  active: Record<string, ActiveFindingRetest>;
};

/** The single expanded row and its inline edit buffer (name/category/severity). */
export type ExpandedRowState = {
  /** The expanded row's findingRowKey; null = all collapsed. */
  key: string | null;
  edit: FindingEdit | null;
};

export type FindingsPageState = {
  view: FindingView;
  /** False until saved list preferences have been read from localStorage. */
  preferencesHydrated: boolean;
  filters: FindingsFilters;
  flat: ListState<Finding>;
  /** The flat (and asset) view's pager; one-based pages. */
  pagination: Pagination;
  asset: AssetViewState;
  grouped: GroupedViewState;
  /** The grouped view's pager over task groups; one-based pages. */
  groupPagination: Pagination;
  stats: FindingStatsState;
  /** Export selection: tracked by finding_id (the standalone table's id), preserved across pages. */
  selection: Set<string>;
  requests: Requests<FindingsRequestKey>;
  exportDialog: ExportDialogState;
  deepen: DeepenDialogState;
  retest: RetestState;
  expandedRow: ExpandedRowState;
  /** Detailed-report cache keyed by findingRowKey. */
  reports: Record<string, FindingReport>;
};

export type FindingEditUpdate = FindingEdit | null | ((current: FindingEdit | null) => FindingEdit | null);

export type FindingsPageAction =
  | PaginationAction
  | ListAction<Finding>
  | SelectionAction<string>
  | RequestAction<FindingsRequestKey>
  /** A `pagination/*` action for the grouped view's pager. */
  | { type: "groupPagination"; action: PaginationAction }
  | { type: "viewChanged"; view: FindingView }
  | { type: "preferencesRestored"; view?: FindingView; filters: Partial<FindingListPreferences> }
  | { type: "filterChanged"; patch: Partial<FindingsFilters> }
  | { type: "searchDebounced" }
  | { type: "filterFingerprintChanged" }
  | { type: "assetScopeSelected"; scope: string | null }
  | { type: "assetTreeLoadStarted" }
  | {
      type: "assetTreeLoaded";
      nodes: FindingAssetNode[];
      findingTotal: number;
      truncated: boolean;
      droppedKinds: string[];
    }
  | { type: "assetTreeLoadFailed" }
  | { type: "groupsLoaded"; groups: FindingGroup[]; groupTotal: number; findingTotal: number }
  | { type: "groupToggled"; key: string; open: boolean }
  | { type: "groupLoadStarted"; key: string; page: number; pageSize: number }
  | { type: "groupLoaded"; key: string; items: Finding[]; total: number; page: number; pageSize: number }
  | { type: "groupLoadFailed"; key: string; page: number; pageSize: number }
  | { type: "findingsUpdated"; update: (current: Finding[]) => Finding[] }
  | { type: "findingTotalsDecremented" }
  | { type: "findingDeleted"; findingId: string; rowKey: string }
  | { type: "statsLoaded"; stats: FindingStats }
  | { type: "statsRefreshed"; stats: FindingStats }
  | { type: "selectionToggled"; ids: string[]; checked: boolean }
  | { type: "exportOpened" }
  | { type: "exportDialogChanged"; patch: Partial<ExportDialogState> }
  | { type: "deepenOpened"; finding: Finding }
  | { type: "deepenClosed" }
  | { type: "deepenDescriptionChanged"; description: string }
  | { type: "retestTargetChanged"; finding: Finding | null }
  | { type: "activeRetestsLoaded"; active: Record<string, ActiveFindingRetest> }
  | { type: "retestStarted"; retest: ActiveFindingRetest }
  | { type: "rowExpanded"; key: string; edit: FindingEdit }
  | { type: "rowCollapsed" }
  | { type: "editChanged"; edit: FindingEditUpdate }
  | { type: "reportLoadStarted"; key: string }
  | { type: "reportLoaded"; key: string; text: string }
  | { type: "reportLoadFailed"; key: string };

export function createInitialFindingsPageState(): FindingsPageState {
  return {
    view: "flat",
    preferencesHydrated: false,
    filters: {
      severity: "all",
      status: "all",
      reportState: "all",
      verificationStatus: "all",
      vulnclass: "all",
      task: "all",
      sort: "severity",
      search: "",
      query: "",
    },
    flat: EMPTY_FLAT_STATE,
    pagination: createPagination(20, 1),
    asset: { tree: EMPTY_ASSET_TREE, scope: null },
    grouped: {
      groups: [],
      groupTotal: 0,
      findingTotal: 0,
      expanded: new Set(),
      findings: {},
    },
    groupPagination: createPagination(10, 1),
    stats: { data: EMPTY_STATS, loaded: false },
    selection: new Set(),
    requests: createRequests(["bulkUpdate", "export", "deepen", "save"]),
    exportDialog: { open: false, scope: "filtered", format: "md-single" },
    deepen: { finding: null, description: "" },
    retest: { finding: null, active: {} },
    expandedRow: { key: null, edit: null },
    reports: {},
  };
}

function withFilters(state: FindingsPageState, patch: Partial<FindingsFilters>): FindingsPageState {
  const keys = Object.keys(patch) as (keyof FindingsFilters)[];
  if (keys.every((key) => patch[key] === undefined || patch[key] === state.filters[key])) return state;
  return { ...state, filters: { ...state.filters, ...patch } };
}

function withGroupFindings(state: FindingsPageState, findings: Record<string, GroupFindingsState>): FindingsPageState {
  return { ...state, grouped: { ...state.grouped, findings } };
}

function withReport(state: FindingsPageState, key: string, report: FindingReport): FindingsPageState {
  return { ...state, reports: { ...state.reports, [key]: report } };
}

export function findingsPageReducer(state: FindingsPageState, action: FindingsPageAction): FindingsPageState {
  if (isPaginationAction(action)) return setSlice(state, "pagination", paginationReducer(state.pagination, action));
  if (isListAction<Finding>(action)) return setSlice(state, "flat", listReducer(state.flat, action));
  if (isSelectionAction<string>(action)) return setSlice(state, "selection", selectionReducer(state.selection, action));
  if (isRequestAction<FindingsRequestKey>(action)) {
    return setSlice(state, "requests", requestReducer(state.requests, action));
  }
  switch (action.type) {
    case "groupPagination":
      return setSlice(state, "groupPagination", paginationReducer(state.groupPagination, action.action));
    case "viewChanged":
      return { ...state, view: action.view };
    case "preferencesRestored": {
      const restored = withFilters(state, action.filters);
      return { ...restored, view: action.view ?? restored.view, preferencesHydrated: true };
    }
    case "filterChanged":
      return withFilters(state, action.patch);
    case "searchDebounced":
      return withFilters(state, { query: state.filters.search.trim() });
    // Reset every view's pagination and expansion when a shared finding filter changes. The
    // previously selected asset node may no longer be in the tree, so fall back to "All assets".
    case "filterFingerprintChanged":
      return {
        ...state,
        flat: EMPTY_FLAT_STATE,
        pagination: toFirstPage(state.pagination),
        asset: { tree: EMPTY_ASSET_TREE, scope: null },
        grouped: { ...state.grouped, expanded: new Set(), findings: {} },
        groupPagination: toFirstPage(state.groupPagination),
        expandedRow: { ...state.expandedRow, key: null },
      };
    case "assetScopeSelected":
      if (action.scope === state.asset.scope) return state;
      return { ...state, asset: { ...state.asset, scope: action.scope } };
    case "assetTreeLoadStarted":
      return { ...state, asset: { ...state.asset, tree: { ...state.asset.tree, loading: true } } };
    case "assetTreeLoaded":
      return {
        ...state,
        asset: {
          ...state.asset,
          tree: {
            nodes: action.nodes,
            findingTotal: action.findingTotal,
            truncated: action.truncated,
            droppedKinds: action.droppedKinds,
            loaded: true,
            loading: false,
          },
        },
      };
    case "assetTreeLoadFailed":
      return { ...state, asset: { ...state.asset, tree: { ...state.asset.tree, loading: false } } };
    case "groupsLoaded":
      return {
        ...state,
        grouped: {
          ...state.grouped,
          groups: action.groups,
          groupTotal: action.groupTotal,
          findingTotal: action.findingTotal,
        },
      };
    case "groupToggled": {
      const expanded = new Set(state.grouped.expanded);
      if (action.open) expanded.add(action.key);
      else expanded.delete(action.key);
      return { ...state, grouped: { ...state.grouped, expanded } };
    }
    case "groupLoadStarted": {
      const current = state.grouped.findings[action.key];
      return withGroupFindings(state, {
        ...state.grouped.findings,
        [action.key]: {
          items: current?.items ?? [],
          total: current?.total ?? 0,
          page: action.page,
          pageSize: action.pageSize,
          loaded: current?.loaded ?? false,
          loading: true,
        },
      });
    }
    case "groupLoaded":
      return withGroupFindings(state, {
        ...state.grouped.findings,
        [action.key]: {
          items: action.items,
          total: action.total,
          page: action.page,
          pageSize: action.pageSize,
          loaded: true,
          loading: false,
        },
      });
    case "groupLoadFailed":
      return withGroupFindings(state, {
        ...state.grouped.findings,
        [action.key]: {
          ...(state.grouped.findings[action.key] ?? {
            items: [],
            total: 0,
            page: action.page,
            pageSize: action.pageSize,
            loaded: false,
          }),
          loading: false,
        },
      });
    // Rewrites the same findings in both view caches at once, so switching views never shows stale state.
    case "findingsUpdated": {
      const findings: Record<string, GroupFindingsState> = {};
      for (const [key, group] of Object.entries(state.grouped.findings)) {
        findings[key] = { ...group, items: action.update(group.items) };
      }
      return {
        ...state,
        flat: { ...state.flat, items: action.update(state.flat.items) },
        grouped: { ...state.grouped, findings },
      };
    }
    case "findingTotalsDecremented":
      return {
        ...state,
        flat: { ...state.flat, total: Math.max(0, state.flat.total - 1) },
        grouped: { ...state.grouped, findingTotal: Math.max(0, state.grouped.findingTotal - 1) },
      };
    case "findingDeleted": {
      const deleted = findingsPageReducer(state, { type: "findingTotalsDecremented" });
      return {
        ...deleted,
        selection: selectionReducer(deleted.selection, {
          type: "selection/set",
          ids: [...deleted.selection].filter((id) => id !== action.findingId),
        }),
        expandedRow:
          deleted.expandedRow.key === action.rowKey ? { ...deleted.expandedRow, key: null } : deleted.expandedRow,
      };
    }
    case "statsLoaded":
      return { ...state, stats: { data: action.stats, loaded: true } };
    case "statsRefreshed":
      return { ...state, stats: { ...state.stats, data: action.stats } };
    // Checks or unchecks the given ids (one row or a whole page). Not `selection/toggled`: that flips a single id,
    // while the table passes the checkbox's target state.
    case "selectionToggled":
      return setSlice(
        state,
        "selection",
        selectionReducer(state.selection, { type: "selection/changed", ids: action.ids, selected: action.checked }),
      );
    // Default the scope to "Selected" if any rows are checked, else "Current filter".
    case "exportOpened":
      return {
        ...state,
        exportDialog: {
          ...state.exportDialog,
          open: true,
          scope: state.selection.size > 0 ? "selected" : "filtered",
        },
      };
    case "exportDialogChanged":
      return setSlice(state, "exportDialog", mergePatch(state.exportDialog, action.patch));
    case "deepenOpened":
      return { ...state, deepen: { ...state.deepen, finding: action.finding, description: "" } };
    case "deepenClosed":
      return { ...state, deepen: { ...state.deepen, finding: null, description: "" } };
    case "deepenDescriptionChanged":
      return { ...state, deepen: { ...state.deepen, description: action.description } };
    case "retestTargetChanged":
      return { ...state, retest: { ...state.retest, finding: action.finding } };
    case "activeRetestsLoaded":
      return { ...state, retest: { ...state.retest, active: action.active } };
    case "retestStarted":
      return {
        ...state,
        retest: { ...state.retest, active: { ...state.retest.active, [action.retest.finding_id]: action.retest } },
      };
    case "rowExpanded":
      return { ...state, expandedRow: { ...state.expandedRow, key: action.key, edit: action.edit } };
    case "rowCollapsed":
      return { ...state, expandedRow: { ...state.expandedRow, key: null, edit: null } };
    case "editChanged": {
      const edit = typeof action.edit === "function" ? action.edit(state.expandedRow.edit) : action.edit;
      if (edit === state.expandedRow.edit) return state;
      return { ...state, expandedRow: { ...state.expandedRow, edit } };
    }
    case "reportLoadStarted":
      return withReport(state, action.key, { status: "loading", text: "" });
    case "reportLoaded":
      return withReport(state, action.key, { status: "done", text: action.text });
    case "reportLoadFailed":
      return withReport(state, action.key, { status: "error", text: "" });
  }
}

import type { api } from "@/lib/api";
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
import { setSlice } from "@/lib/reducers/slice";
import type { SSProject, SSTask } from "@/lib/types";

export type Dimension = "project" | "task";

export type SyncResultData = Awaited<ReturnType<typeof api.ssSync>>;

/** The upstream list endpoints take a fixed page size of 50. */
export const SYNC_PAGE_SIZE = 50;

/** What the list shows: dimension and search text. */
export type SyncBrowse = {
  dimension: Dimension;
  search: string;
};

/** Projects and tasks are kept apart: each dimension renders its own row type. */

export type SyncList = {
  projects: SSProject[];
  tasks: SSTask[];
  loading: boolean;
};

/** Options sent with the sync request. */
export type SyncOptions = {
  assetTypes: Record<string, boolean>;
  createCompany: boolean;
};

export type SyncWorkbenchState = {
  browse: SyncBrowse;
  /** One-based pages. */
  pagination: Pagination;
  list: SyncList;
  /** Selected project IDs or task names. */
  selection: Set<string>;
  options: SyncOptions;
  /** Last sync result; cleared when a new sync starts. */
  syncResult: SyncResultData | null;
  requests: Requests<"sync">;
};

export type SyncWorkbenchAction =
  | PaginationAction
  | SelectionAction<string>
  | RequestAction<"sync">
  | { type: "dimensionChanged"; dimension: Dimension }
  | { type: "searchChanged"; search: string }
  | { type: "searchSubmitted" }
  /** Previous / next relative to the current page (the slice keeps it at or above page 1). */
  | { type: "pageStepped"; delta: number }
  | { type: "listLoadStarted" }
  | { type: "projectsLoaded"; projects: SSProject[] }
  | { type: "tasksLoaded"; tasks: SSTask[] }
  | { type: "listLoadFinished" }
  | { type: "allRowsToggled"; ids: string[] }
  | { type: "assetTypeToggled"; key: string; checked: boolean }
  | { type: "createCompanyChanged"; checked: boolean }
  | { type: "syncStarted" }
  | { type: "syncSucceeded"; result: SyncResultData };

export const initialSyncWorkbenchState: SyncWorkbenchState = {
  browse: { dimension: "project", search: "" },
  pagination: createPagination(SYNC_PAGE_SIZE, 1),
  list: { projects: [], tasks: [], loading: false },
  selection: new Set(),
  options: { assetTypes: { subdomain: true, service: true, app: true }, createCompany: true },
  syncResult: null,
  requests: createRequests(["sync"]),
};

export function syncWorkbenchReducer(state: SyncWorkbenchState, action: SyncWorkbenchAction): SyncWorkbenchState {
  if (isPaginationAction(action)) return setSlice(state, "pagination", paginationReducer(state.pagination, action));
  if (isSelectionAction<string>(action)) return setSlice(state, "selection", selectionReducer(state.selection, action));
  if (isRequestAction<"sync">(action)) return setSlice(state, "requests", requestReducer(state.requests, action));
  switch (action.type) {
    case "dimensionChanged":
      return {
        ...state,
        browse: { ...state.browse, dimension: action.dimension },
        pagination: toFirstPage(state.pagination),
      };
    case "searchChanged":
      return { ...state, browse: { ...state.browse, search: action.search } };
    case "searchSubmitted":
      return setSlice(state, "pagination", toFirstPage(state.pagination));
    case "pageStepped":
      return setSlice(
        state,
        "pagination",
        paginationReducer(state.pagination, {
          type: "pagination/pageChanged",
          page: state.pagination.page + action.delta,
        }),
      );
    case "listLoadStarted":
      // Every reload drops the selection.
      return {
        ...state,
        list: { ...state.list, loading: true },
        selection: selectionReducer(state.selection, { type: "selection/cleared" }),
      };
    case "projectsLoaded":
      return { ...state, list: { ...state.list, projects: action.projects } };
    case "tasksLoaded":
      return { ...state, list: { ...state.list, tasks: action.tasks } };
    case "listLoadFinished":
      return { ...state, list: { ...state.list, loading: false } };
    case "allRowsToggled": {
      // All rows checked → clear; otherwise check exactly this page's rows.
      const next: SelectionAction<string> =
        state.selection.size === action.ids.length
          ? { type: "selection/cleared" }
          : { type: "selection/set", ids: action.ids };
      return setSlice(state, "selection", selectionReducer(state.selection, next));
    }
    case "assetTypeToggled":
      return {
        ...state,
        options: { ...state.options, assetTypes: { ...state.options.assetTypes, [action.key]: action.checked } },
      };
    case "createCompanyChanged":
      return { ...state, options: { ...state.options, createCompany: action.checked } };
    case "syncStarted":
      return {
        ...state,
        syncResult: null,
        requests: requestReducer(state.requests, { type: "request/started", key: "sync" }),
      };
    case "syncSucceeded":
      return { ...state, syncResult: action.result };
  }
}

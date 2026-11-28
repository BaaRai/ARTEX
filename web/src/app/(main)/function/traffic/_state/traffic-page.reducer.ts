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
import type { TrafficDetail, TrafficExchange, TrafficHARImportResult, TrafficHost, TrafficResp } from "@/lib/types";

export const DEFAULT_PAGE_SIZE = 50;

export type TrafficViewTab = "exchanges" | "hooks";
export type HostCountSortDirection = "asc" | "desc";
export type DeleteMode = "filter" | "selected" | "all";

/** Free-text filters, each typed into its own input and debounced before reaching the server. */
export type TextFilters = {
  host: string;
  query: string;
  /** Response-body content (issue #177). */
  body: string;
  path: string;
  /** Response-size range in bytes. */
  respMin: string;
  respMax: string;
};

export type TrafficFilters = {
  /** Raw input values, updated on every keystroke. */
  input: TextFilters;
  /** Trimmed, debounced values actually sent to the API. */
  applied: TextFilters;
  method: string;
  /** Status-class bucket ("all", "2xx", …); applies immediately. */
  status: string;
  /** "all" | "rewritten" | "original"; applies immediately. */
  rewritten: string;
};

export type TrafficList = {
  /** Last successful snapshot; null until the first load. */
  traffic: TrafficResp | null;
  /** Bumped to force a refetch of the current page. */
  reloadTick: number;
};

/** Target picker used for batch deletion by host. */
export type TrafficHostPicker = {
  hosts: TrafficHost[];
  /** Hosts checked in the picker. */
  selected: string[];
  open: boolean;
  countSortDirection: HostCountSortDirection;
};

/** Exchange shown in the detail sheet and its lazily loaded raw messages. */
export type TrafficInspector = {
  selected: TrafficExchange | null;
  detail: TrafficDetail | null;
  loading: boolean;
  tab: string;
};

/** Busy flags: batch deletion and HAR 1.2 import/export (the import error is shown in an alert). */
export type TrafficRequestKey = "delete" | "harImport" | "harExport";

export type TrafficPageState = {
  viewTab: TrafficViewTab;
  filters: TrafficFilters;
  pagination: Pagination;
  list: TrafficList;
  hostPicker: TrafficHostPicker;
  inspector: TrafficInspector;
  /** Flows checked in the table for linking to a finding. */
  selection: Set<string>;
  /** The link-to-finding dialog is open. */
  linking: boolean;
  /** Delete confirmation dialog mode; null = closed. */
  deleteMode: DeleteMode | null;
  /** Result of the last HAR import, shown until dismissed. */
  harSummary: TrafficHARImportResult | null;
  requests: Requests<TrafficRequestKey>;
};

export type TrafficPageAction =
  | PaginationAction
  | SelectionAction<string>
  | RequestAction<TrafficRequestKey>
  | { type: "fieldsChanged"; patch: Partial<Pick<TrafficPageState, "viewTab" | "linking">> }
  | { type: "filterInputChanged"; field: keyof TextFilters; value: string }
  | { type: "filtersDebounced" }
  | { type: "methodChanged"; method: string }
  | { type: "statusFilterChanged"; status: string }
  | { type: "rewrittenFilterChanged"; rewritten: string }
  | { type: "advancedFiltersCleared" }
  | { type: "previousPageRequested" }
  | { type: "nextPageRequested"; pageCount: number }
  | { type: "reloadRequested" }
  | { type: "trafficLoaded"; traffic: TrafficResp }
  | { type: "hostsLoaded"; hosts: TrafficHost[] }
  | { type: "hostPickerChanged"; patch: Partial<TrafficHostPicker> }
  | { type: "hostCountSortToggled" }
  | { type: "hostToggled"; host: string }
  | { type: "flowsSelectionChanged"; ids: string[]; checked: boolean }
  | { type: "inspectorChanged"; patch: Partial<TrafficInspector> }
  | { type: "detailCleared" }
  | { type: "detailLoadStarted" }
  | { type: "detailLoaded"; detail: TrafficDetail }
  | { type: "detailLoadFinished" }
  | { type: "deleteRequested"; mode: DeleteMode }
  | { type: "deleteDialogClosed" }
  | { type: "deleteSucceeded"; mode: DeleteMode }
  | { type: "harImportCleared" }
  | { type: "harImported"; summary: TrafficHARImportResult }
  | { type: "harSummaryDismissed" };

const emptyTextFilters: TextFilters = { host: "", query: "", body: "", path: "", respMin: "", respMax: "" };
const TEXT_FILTER_FIELDS = Object.keys(emptyTextFilters) as (keyof TextFilters)[];

/** Built per mount so every page gets its own selection Set. */
export function createTrafficPageState(): TrafficPageState {
  return {
    viewTab: "exchanges",
    filters: {
      input: { ...emptyTextFilters },
      applied: { ...emptyTextFilters },
      method: "all",
      status: "all",
      rewritten: "all",
    },
    pagination: createPagination(DEFAULT_PAGE_SIZE),
    list: { traffic: null, reloadTick: 0 },
    hostPicker: { hosts: [], selected: [], open: false, countSortDirection: "desc" },
    inspector: { selected: null, detail: null, loading: false, tab: "request" },
    selection: new Set(),
    linking: false,
    deleteMode: null,
    harSummary: null,
    requests: createRequests<TrafficRequestKey>(["delete", "harImport", "harExport"]),
  };
}

// Every applied filter change sends the user back to the first page.
function withFilters(state: TrafficPageState, filters: TrafficFilters): TrafficPageState {
  return { ...state, filters, pagination: toFirstPage(state.pagination) };
}

function withPage(state: TrafficPageState, page: number): TrafficPageState {
  return setSlice(state, "pagination", paginationReducer(state.pagination, { type: "pagination/pageChanged", page }));
}

// A completed reload (manual, delete, import) lands on the newest page.
function reloadedFromFirstPage(state: TrafficPageState): Pick<TrafficPageState, "pagination" | "list"> {
  return { pagination: toFirstPage(state.pagination), list: { ...state.list, reloadTick: state.list.reloadTick + 1 } };
}

function isStream(detail: TrafficDetail): boolean {
  return detail.stream_kind === "websocket" || detail.stream_kind === "sse";
}

export function trafficPageReducer(state: TrafficPageState, action: TrafficPageAction): TrafficPageState {
  if (isPaginationAction(action)) return setSlice(state, "pagination", paginationReducer(state.pagination, action));
  if (isSelectionAction<string>(action)) return setSlice(state, "selection", selectionReducer(state.selection, action));
  if (isRequestAction<TrafficRequestKey>(action)) {
    return setSlice(state, "requests", requestReducer(state.requests, action));
  }
  switch (action.type) {
    case "fieldsChanged":
      return mergePatch<TrafficPageState>(state, action.patch);
    case "filterInputChanged":
      return {
        ...state,
        filters: { ...state.filters, input: { ...state.filters.input, [action.field]: action.value } },
      };
    case "filtersDebounced": {
      const { input, applied } = state.filters;
      const next = { ...applied };
      for (const field of TEXT_FILTER_FIELDS) next[field] = input[field].trim();
      if (TEXT_FILTER_FIELDS.every((field) => next[field] === applied[field])) return state;
      return withFilters(state, { ...state.filters, applied: next });
    }
    case "methodChanged":
      if (action.method === state.filters.method) return state;
      return withFilters(state, { ...state.filters, method: action.method });
    case "statusFilterChanged":
      if (action.status === state.filters.status) return state;
      return withFilters(state, { ...state.filters, status: action.status });
    case "rewrittenFilterChanged":
      if (action.rewritten === state.filters.rewritten) return state;
      return withFilters(state, { ...state.filters, rewritten: action.rewritten });
    case "advancedFiltersCleared": {
      const { input, applied, status, rewritten } = state.filters;
      const filters: TrafficFilters = {
        ...state.filters,
        input: { ...input, body: "", path: "", respMin: "", respMax: "" },
        applied: { ...applied, body: "", path: "", respMin: "", respMax: "" },
        status: "all",
        rewritten: "all",
      };
      const appliedChanged =
        Boolean(applied.body || applied.path || applied.respMin || applied.respMax) ||
        status !== "all" ||
        rewritten !== "all";
      return appliedChanged ? withFilters(state, filters) : { ...state, filters };
    }
    // Previous only stops at the first page (the slice clamps it); it does not clamp to the
    // page count, since the list may have shrunk under the user. Next stops at the last page.
    case "previousPageRequested":
      return withPage(state, state.pagination.page - 1);
    case "nextPageRequested":
      return withPage(state, Math.min(action.pageCount - 1, state.pagination.page + 1));
    case "reloadRequested":
      return { ...state, list: { ...state.list, reloadTick: state.list.reloadTick + 1 } };
    case "trafficLoaded":
      return { ...state, list: { ...state.list, traffic: action.traffic } };
    case "hostsLoaded":
      return { ...state, hostPicker: { ...state.hostPicker, hosts: action.hosts } };
    case "hostPickerChanged":
      return setSlice(state, "hostPicker", mergePatch(state.hostPicker, action.patch));
    case "hostCountSortToggled":
      return {
        ...state,
        hostPicker: {
          ...state.hostPicker,
          countSortDirection: state.hostPicker.countSortDirection === "desc" ? "asc" : "desc",
        },
      };
    case "hostToggled": {
      const prev = state.hostPicker.selected;
      const selected = prev.includes(action.host) ? prev.filter((x) => x !== action.host) : [...prev, action.host];
      return { ...state, hostPicker: { ...state.hostPicker, selected } };
    }
    case "flowsSelectionChanged": {
      // Bulk check/uncheck (one row, or the whole page from the header checkbox).
      const ids = new Set(state.selection);
      for (const id of action.ids) {
        if (action.checked) ids.add(id);
        else ids.delete(id);
      }
      return setSlice(state, "selection", selectionReducer(state.selection, { type: "selection/set", ids }));
    }
    case "inspectorChanged":
      return setSlice(state, "inspector", mergePatch(state.inspector, action.patch));
    case "detailCleared":
      return { ...state, inspector: { ...state.inspector, tab: "request", detail: null } };
    case "detailLoadStarted":
      return { ...state, inspector: { ...state.inspector, tab: "request", loading: true, detail: null } };
    case "detailLoaded":
      // A WebSocket/SSE exchange opens on its Stream tab.
      return {
        ...state,
        inspector: {
          ...state.inspector,
          detail: action.detail,
          tab: isStream(action.detail) ? "stream" : state.inspector.tab,
        },
      };
    case "detailLoadFinished":
      return { ...state, inspector: { ...state.inspector, loading: false } };
    case "deleteRequested":
      return { ...state, deleteMode: action.mode };
    case "deleteDialogClosed":
      return { ...state, deleteMode: null };
    case "deleteSucceeded":
      return {
        ...state,
        ...reloadedFromFirstPage(state),
        deleteMode: null,
        inspector: { ...state.inspector, selected: null, detail: null },
        // Host-scoped batch and full purge also drop the picker's checked hosts.
        hostPicker: action.mode === "filter" ? state.hostPicker : { ...state.hostPicker, selected: [], open: false },
      };
    case "harImportCleared":
      // A new import attempt drops the previous summary and error.
      return {
        ...state,
        harSummary: null,
        requests: requestReducer<TrafficRequestKey>(state.requests, { type: "request/errorCleared", key: "harImport" }),
      };
    case "harImported":
      return { ...state, ...reloadedFromFirstPage(state), harSummary: action.summary };
    case "harSummaryDismissed":
      return { ...state, harSummary: null };
  }
}

import { createList, isListAction, type ListAction, type ListState, listReducer } from "@/lib/reducers/list";
import {
  createPagination,
  isPaginationAction,
  type Pagination,
  type PaginationAction,
  paginationReducer,
  toFirstPage,
} from "@/lib/reducers/pagination";
import { isSelectionAction, type SelectionAction, selectionReducer } from "@/lib/reducers/selection";
import { mergePatch, setSlice } from "@/lib/reducers/slice";
import type { Asset, Company } from "@/lib/types";

export const DEFAULT_PAGE_SIZE = 50;

/** A new value, or an updater applied to the current one (the shape of a React state setter argument). */
export type NumberUpdate = number | ((prev: number) => number);

export type AssetsFilters = {
  /** Active tab: "company" or an asset type. */
  tab: string;
  /** DSL search text for the active data tab. */
  query: string;
};

/** What the current list page belongs to, next to the shared list slice. */
export type AssetsListInfo = {
  /** Tab the rows belong to, so rows of another tab are never shown. */
  rowsTab: string;
  dslError: string;
  /** Bumped to force a refetch with unchanged filters. */
  refreshKey: number;
};

/** Companies and per-type counts (tab badges). */
export type AssetsMeta = {
  companies: Company[];
  counts: Record<string, number>;
};

export type AssetsDeleteDialog = {
  ids: number[];
  open: boolean;
  deleting: boolean;
};

export type CompanyDeleteDialog = {
  target: Company | null;
  /** Also delete the company's assets. */
  deleteAssets: boolean;
  deleting: boolean;
};

export type AssetsPageState = {
  filters: AssetsFilters;
  /** Zero-based pages. */
  pagination: Pagination;
  /** Current server-side page of the active data tab; `loaded` is true once the first load for the tab finished. */
  list: ListState<Asset>;
  listInfo: AssetsListInfo;
  meta: AssetsMeta;
  /** Selected asset IDs. */
  selection: Set<number>;
  assetDelete: AssetsDeleteDialog;
  companyDelete: CompanyDeleteDialog;
};

export type AssetsPageAction =
  | PaginationAction
  | ListAction<Asset>
  | SelectionAction<number>
  | { type: "tabChanged"; tab: string }
  | { type: "queryChanged"; query: string }
  | { type: "pageSizeChanged"; size: NumberUpdate }
  | { type: "pageChanged"; page: NumberUpdate }
  | { type: "listLoaded"; tab: string; rows: Asset[]; total: number }
  | { type: "listLoadFailed"; tab: string; error: string }
  | { type: "companiesLoaded"; companies: Company[] }
  | { type: "countsLoaded"; counts: Record<string, number> }
  | { type: "refreshRequested" }
  | { type: "pageRowsToggled"; ids: number[] }
  | { type: "assetDeleteChanged"; patch: Partial<AssetsDeleteDialog> }
  | { type: "assetDeleteStarted" }
  | { type: "assetDeleteFinished" }
  | { type: "companyDeleteRequested"; company: Company }
  | { type: "companyDeleteDismissed" }
  | { type: "companyDeleteAssetsChanged"; deleteAssets: boolean }
  | { type: "companyDeleteStarted" }
  | { type: "companyDeleteFinished" };

export const initialAssetsPageState: AssetsPageState = {
  filters: { tab: "company", query: "" },
  pagination: createPagination(DEFAULT_PAGE_SIZE),
  list: createList(),
  listInfo: { rowsTab: "", dslError: "", refreshKey: 0 },
  meta: { companies: [], counts: {} },
  selection: new Set(),
  assetDelete: { ids: [], open: false, deleting: false },
  companyDelete: { target: null, deleteAssets: false, deleting: false },
};

function resolve(update: NumberUpdate, prev: number): number {
  return typeof update === "function" ? update(prev) : update;
}

function withPagination(state: AssetsPageState, action: PaginationAction): AssetsPageState {
  return setSlice(state, "pagination", paginationReducer(state.pagination, action));
}

function withList(state: AssetsPageState, action: ListAction<Asset>): AssetsPageState {
  return setSlice(state, "list", listReducer(state.list, action));
}

function withSelection(state: AssetsPageState, action: SelectionAction<number>): AssetsPageState {
  return setSlice(state, "selection", selectionReducer(state.selection, action));
}

export function assetsPageReducer(state: AssetsPageState, action: AssetsPageAction): AssetsPageState {
  if (isPaginationAction(action)) return withPagination(state, action);
  if (isListAction<Asset>(action)) return withList(state, action);
  if (isSelectionAction<number>(action)) return withSelection(state, action);
  switch (action.type) {
    case "tabChanged":
      if (action.tab === state.filters.tab) return state;
      // Switching tabs resets the query, page, selection and the tab-local list.
      return {
        ...state,
        filters: { tab: action.tab, query: "" },
        pagination: toFirstPage(state.pagination),
        list: listReducer(state.list, { type: "list/cleared" }),
        listInfo: { ...state.listInfo, rowsTab: "", dslError: "" },
        selection: selectionReducer(state.selection, { type: "selection/cleared" }),
      };
    case "queryChanged":
      if (action.query === state.filters.query) return state;
      return {
        ...state,
        filters: { ...state.filters, query: action.query },
        pagination: toFirstPage(state.pagination),
      };
    // Children call these with values or React-style updaters; resolve against the current value, then let the
    // pagination slice apply it.
    case "pageSizeChanged":
      return withPagination(state, {
        type: "pagination/sizeChanged",
        size: resolve(action.size, state.pagination.size),
      });
    case "pageChanged":
      return withPagination(state, {
        type: "pagination/pageChanged",
        page: resolve(action.page, state.pagination.page),
      });
    case "listLoaded":
      return {
        ...withList(state, { type: "list/loaded", items: action.rows, total: action.total }),
        listInfo: { ...state.listInfo, rowsTab: action.tab, dslError: "" },
      };
    case "listLoadFailed":
      return {
        ...withList(state, { type: "list/loaded", items: [], total: 0 }),
        listInfo: { ...state.listInfo, rowsTab: action.tab, dslError: action.error },
      };
    case "companiesLoaded":
      return { ...state, meta: { ...state.meta, companies: action.companies } };
    case "countsLoaded":
      return { ...state, meta: { ...state.meta, counts: action.counts } };
    case "refreshRequested":
      return {
        ...withSelection(state, { type: "selection/cleared" }),
        listInfo: { ...state.listInfo, refreshKey: state.listInfo.refreshKey + 1 },
      };
    case "pageRowsToggled": {
      // Select the whole page, or deselect it when every row on it is already selected.
      const allSelected = action.ids.every((id) => state.selection.has(id));
      return setSlice(
        state,
        "selection",
        selectionReducer(state.selection, { type: "selection/changed", ids: action.ids, selected: !allSelected }),
      );
    }
    case "assetDeleteChanged":
      return setSlice(state, "assetDelete", mergePatch(state.assetDelete, action.patch));
    case "assetDeleteStarted":
      return { ...state, assetDelete: { ...state.assetDelete, deleting: true } };
    case "assetDeleteFinished":
      return { ...state, assetDelete: { ...state.assetDelete, deleting: false, open: false } };
    case "companyDeleteRequested":
      return { ...state, companyDelete: { ...state.companyDelete, target: action.company, deleteAssets: false } };
    case "companyDeleteDismissed":
      return { ...state, companyDelete: { ...state.companyDelete, target: null, deleteAssets: false } };
    case "companyDeleteAssetsChanged":
      return { ...state, companyDelete: { ...state.companyDelete, deleteAssets: action.deleteAssets } };
    case "companyDeleteStarted":
      return { ...state, companyDelete: { ...state.companyDelete, deleting: true } };
    case "companyDeleteFinished":
      return { ...state, companyDelete: { target: null, deleteAssets: false, deleting: false } };
  }
}

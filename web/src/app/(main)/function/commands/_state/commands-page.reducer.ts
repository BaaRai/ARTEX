import { createList, isListAction, type ListAction, type ListState, listReducer } from "@/lib/reducers/list";
import {
  createPagination,
  isPaginationAction,
  type Pagination,
  type PaginationAction,
  paginationReducer,
  toFirstPage,
} from "@/lib/reducers/pagination";
import { setSlice } from "@/lib/reducers/slice";
import type { CommandRecord, ToolStat } from "@/lib/types";

export const DEFAULT_PAGE_SIZE = 50;

export type CommandsFilters = {
  /** Raw search box value, updated on every keystroke. */
  query: string;
  /** Trimmed, debounced query actually sent to the API. */
  debouncedQuery: string;
  /** Task ID filter, digits only. */
  task: string;
};

/** Per-tool call counts, fetched only while the stats dialog is open. */
export type CommandsStats = {
  open: boolean;
  items: ToolStat[];
  loading: boolean;
};

export type CommandsPageState = {
  filters: CommandsFilters;
  pagination: Pagination;
  list: ListState<CommandRecord>;
  stats: CommandsStats;
  /** Execution shown in the detail sheet. */
  selected: CommandRecord | null;
};

export type CommandsPageAction =
  | PaginationAction
  | ListAction<CommandRecord>
  | { type: "queryChanged"; query: string }
  | { type: "queryDebounced" }
  | { type: "taskFilterChanged"; task: string }
  | { type: "statsOpenChanged"; open: boolean }
  | { type: "statsLoadStarted" }
  | { type: "statsLoaded"; items: ToolStat[] }
  | { type: "statsLoadFinished" }
  | { type: "selected"; command: CommandRecord | null };

export const initialCommandsPageState: CommandsPageState = {
  filters: { query: "", debouncedQuery: "", task: "" },
  pagination: createPagination(DEFAULT_PAGE_SIZE),
  list: createList(),
  stats: { open: false, items: [], loading: false },
  selected: null,
};

export function commandsPageReducer(state: CommandsPageState, action: CommandsPageAction): CommandsPageState {
  if (isPaginationAction(action)) return setSlice(state, "pagination", paginationReducer(state.pagination, action));
  if (isListAction<CommandRecord>(action)) return setSlice(state, "list", listReducer(state.list, action));
  switch (action.type) {
    case "queryChanged":
      return { ...state, filters: { ...state.filters, query: action.query } };
    case "queryDebounced": {
      const debouncedQuery = state.filters.query.trim();
      if (debouncedQuery === state.filters.debouncedQuery) return state;
      // Every filter change sends the user back to the first page.
      return { ...state, filters: { ...state.filters, debouncedQuery }, pagination: toFirstPage(state.pagination) };
    }
    case "taskFilterChanged": {
      const task = action.task.replace(/\D/g, "");
      if (task === state.filters.task) return state;
      return { ...state, filters: { ...state.filters, task }, pagination: toFirstPage(state.pagination) };
    }
    case "statsOpenChanged":
      return { ...state, stats: { ...state.stats, open: action.open } };
    case "statsLoadStarted":
      return { ...state, stats: { ...state.stats, loading: true } };
    case "statsLoaded":
      return { ...state, stats: { ...state.stats, items: action.items } };
    case "statsLoadFinished":
      return { ...state, stats: { ...state.stats, loading: false } };
    case "selected":
      return { ...state, selected: action.command };
  }
}

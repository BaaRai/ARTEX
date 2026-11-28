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
import { setSlice } from "@/lib/reducers/slice";
import type { LLMRecordDetail, LLMRecordItem, LLMTask } from "@/lib/types";

export const DEFAULT_PAGE_SIZE = 50;

export type LLMRecordsFilters = {
  /** Raw session search box value, updated on every keystroke. */
  session: string;
  /** Trimmed, debounced session actually sent to the API. */
  debouncedSession: string;
  model: string;
  /** Picked task ID: filters the list and is the target of "delete task conversations". */
  task: string;
};

/** Recording on/off toggle (settings.llm_record; default off). */
export type LLMRecordsRecording = {
  enabled: boolean;
  /** Flips once the settings call resolves, so the empty state does not claim "recording is off" too early. */
  known: boolean;
  busy: boolean;
};

/** Inline detail panel for the selected record. */
export type LLMRecordsDetail = {
  selected: LLMRecordItem | null;
  data: LLMRecordDetail | null;
  loading: boolean;
  /** User's choice of raw HTTP view over the normalized view. */
  rawView: boolean;
};

/** User-triggered requests: deleting a task's conversations. */
export type LLMRecordsRequestKey = "deleteTask";

export type LLMRecordsPageState = {
  filters: LLMRecordsFilters;
  /** Zero-based pages. */
  pagination: Pagination;
  list: ListState<LLMRecordItem>;
  /** Bumped to force a list refetch with unchanged filters. */
  reloadTick: number;
  /** Tasks that have records, for the task picker. */
  tasks: LLMTask[];
  recording: LLMRecordsRecording;
  detail: LLMRecordsDetail;
  /** Whether the "delete task conversations" dialog is open. */
  deleteDialogOpen: boolean;
  requests: Requests<LLMRecordsRequestKey>;
};

export type LLMRecordsPageAction =
  | PaginationAction
  | ListAction<LLMRecordItem>
  | RequestAction<LLMRecordsRequestKey>
  | { type: "sessionChanged"; session: string }
  | { type: "sessionDebounced" }
  | { type: "modelFilterChanged"; model: string }
  | { type: "taskFilterChanged"; task: string }
  | { type: "tasksLoaded"; tasks: LLMTask[] }
  | { type: "recordingLoaded"; enabled: boolean }
  | { type: "recordingToggleStarted"; on: boolean }
  | { type: "recordingSaved"; enabled: boolean }
  | { type: "recordingSaveFailed"; on: boolean }
  | { type: "recordingToggleFinished" }
  | { type: "recordSelected"; record: LLMRecordItem | null }
  | { type: "detailCleared" }
  | { type: "detailLoadStarted" }
  | { type: "detailLoaded"; detail: LLMRecordDetail }
  | { type: "detailLoadFinished" }
  | { type: "rawViewToggled" }
  | { type: "deleteDialogOpenChanged"; open: boolean }
  | { type: "taskRecordsDeleted" };

export const initialLLMRecordsPageState: LLMRecordsPageState = {
  filters: { session: "", debouncedSession: "", model: "", task: "" },
  pagination: createPagination(DEFAULT_PAGE_SIZE),
  list: createList(),
  reloadTick: 0,
  tasks: [],
  recording: { enabled: false, known: false, busy: false },
  detail: { selected: null, data: null, loading: false, rawView: false },
  deleteDialogOpen: false,
  requests: createRequests(["deleteTask"]),
};

// Every filter change sends the user back to the first page.
function withFilters(state: LLMRecordsPageState, patch: Partial<LLMRecordsFilters>): LLMRecordsPageState {
  return {
    ...state,
    filters: { ...state.filters, ...patch },
    pagination: toFirstPage(state.pagination),
  };
}

export function llmRecordsPageReducer(state: LLMRecordsPageState, action: LLMRecordsPageAction): LLMRecordsPageState {
  if (isPaginationAction(action)) return setSlice(state, "pagination", paginationReducer(state.pagination, action));
  if (isListAction<LLMRecordItem>(action)) return setSlice(state, "list", listReducer(state.list, action));
  if (isRequestAction<LLMRecordsRequestKey>(action)) {
    return setSlice(state, "requests", requestReducer(state.requests, action));
  }
  switch (action.type) {
    case "sessionChanged":
      return { ...state, filters: { ...state.filters, session: action.session } };
    case "sessionDebounced": {
      const debouncedSession = state.filters.session.trim();
      if (debouncedSession === state.filters.debouncedSession) return state;
      return withFilters(state, { debouncedSession });
    }
    case "modelFilterChanged":
      if (action.model === state.filters.model) return state;
      return withFilters(state, { model: action.model });
    case "taskFilterChanged":
      if (action.task === state.filters.task) return state;
      return withFilters(state, { task: action.task });
    case "tasksLoaded":
      return { ...state, tasks: action.tasks };
    case "recordingLoaded":
      return { ...state, recording: { ...state.recording, enabled: action.enabled, known: true } };
    case "recordingToggleStarted":
      // Optimistic: show the new value while the request is in flight.
      return { ...state, recording: { ...state.recording, enabled: action.on, busy: true } };
    case "recordingSaved":
      return { ...state, recording: { ...state.recording, enabled: action.enabled } };
    case "recordingSaveFailed":
      return { ...state, recording: { ...state.recording, enabled: !action.on } };
    case "recordingToggleFinished":
      return { ...state, recording: { ...state.recording, busy: false } };
    case "recordSelected":
      return { ...state, detail: { ...state.detail, selected: action.record } };
    case "detailCleared":
      return { ...state, detail: { ...state.detail, data: null } };
    case "detailLoadStarted":
      return { ...state, detail: { ...state.detail, data: null, loading: true } };
    case "detailLoaded":
      return { ...state, detail: { ...state.detail, data: action.detail } };
    case "detailLoadFinished":
      return { ...state, detail: { ...state.detail, loading: false } };
    case "rawViewToggled":
      return { ...state, detail: { ...state.detail, rawView: !state.detail.rawView } };
    case "deleteDialogOpenChanged":
      return { ...state, deleteDialogOpen: action.open };
    case "taskRecordsDeleted":
      // Close the dialog, drop the selection and task filter, and refetch from the first page.
      return {
        ...state,
        filters: { ...state.filters, task: "" },
        pagination: toFirstPage(state.pagination),
        reloadTick: state.reloadTick + 1,
        detail: { ...state.detail, selected: null, data: null },
        deleteDialogOpen: false,
      };
  }
}

import {
  createRequests,
  isRequestAction,
  type RequestAction,
  type Requests,
  requestReducer,
} from "@/lib/reducers/request";
import { mergePatch, setSlice } from "@/lib/reducers/slice";
import type {
  TamperDryRunResult,
  TamperHook,
  TamperHookMatchers,
  TamperHookPhase,
  TamperHookVersion,
} from "@/lib/types";

/** Create / edit dialog form. */
export interface EditorState {
  mode: "create" | "edit";
  id?: number;
  enabled: boolean;
  name: string;
  phase: TamperHookPhase;
  priority: string;
  note: string;
  methods: string;
  host: string;
  urlRegex: string;
  headers: string;
  bodyRegex: string;
  status: string;
  respHeaders: string;
  respBodyRegex: string;
  source: string;
}

export interface DryRunTarget {
  label: string;
  hookId?: number;
  inline?: {
    name: string;
    phase: TamperHookPhase;
    matchers: TamperHookMatchers;
    source: string;
  };
}

export type HooksList = {
  hooks: TamperHook[];
  enabledByEnv: boolean;
  loading: boolean;
  error: string | null;
  /** Hook whose enabled switch is mid-request. */
  togglingId: number | null;
};

/** Busy flags for the editor's save (with its error), the delete confirmation and the rollback confirmation. */
export type HookRequestKey = "save" | "delete" | "rollback";

/** Versions sheet and its rollback confirmation. */
export type HookVersions = {
  /** Hook whose history is shown; null = sheet closed. */
  hook: TamperHook | null;
  items: TamperHookVersion[];
  loading: boolean;
  /** Version whose source is expanded. */
  expanded: number | null;
  rollbackTarget: TamperHookVersion | null;
};

/** Editable synthetic HTTP message fed to the dry-run (also filled from a recorded exchange). */
export type DryRunInput = {
  method: string;
  url: string;
  reqHeaders: string;
  reqBody: string;
  status: string;
  respHeaders: string;
  respBody: string;
};

export type HookDryRun = {
  /** null = sheet closed. */
  target: DryRunTarget | null;
  inputMode: "synthetic" | "exchange";
  exchangeId: string;
  loadingExchange: boolean;
  exchangeWarning: string | null;
  input: DryRunInput;
  running: boolean;
  error: string | null;
  result: TamperDryRunResult | null;
};

export type TamperHooksPanelState = {
  list: HooksList;
  /** Create / edit dialog form; null = dialog closed. */
  editor: EditorState | null;
  /** Hook awaiting delete confirmation; null = confirmation closed. */
  deleteTarget: TamperHook | null;
  versionHistory: HookVersions;
  dryRun: HookDryRun;
  requests: Requests<HookRequestKey>;
};

export type TamperHooksPanelAction =
  | RequestAction<HookRequestKey>
  | { type: "hooksLoadStarted" }
  | { type: "hooksLoaded"; hooks: TamperHook[]; enabledByEnv: boolean }
  | { type: "hooksLoadFailed"; error: string }
  | { type: "hooksLoadFinished" }
  | { type: "toggleStarted"; id: number }
  | { type: "hookUpdated"; hook: TamperHook }
  | { type: "toggleFinished" }
  | { type: "editorOpened"; form: EditorState }
  | { type: "editorFieldsChanged"; patch: Partial<EditorState> }
  | { type: "editorClosed" }
  | { type: "deleteRequested"; hook: TamperHook }
  | { type: "deleteDialogClosed" }
  | { type: "versionsOpened"; hook: TamperHook }
  | { type: "versionsLoaded"; versions: TamperHookVersion[] }
  | { type: "versionsLoadFinished" }
  | { type: "versionHistoryChanged"; patch: Partial<HookVersions> }
  | { type: "rolledBack"; hook: TamperHook }
  | { type: "dryRunOpened"; target: DryRunTarget }
  | { type: "dryRunChanged"; patch: Partial<HookDryRun> }
  | { type: "dryRunInputChanged"; patch: Partial<DryRunInput> }
  | { type: "exchangeLoadStarted" }
  | { type: "exchangeLoaded"; input: DryRunInput; warning: string | null }
  | { type: "exchangeLoadFailed"; error: string }
  | { type: "exchangeLoadFinished" }
  | { type: "dryRunStarted" }
  | { type: "dryRunSucceeded"; result: TamperDryRunResult }
  | { type: "dryRunFailed"; error: string }
  | { type: "dryRunFinished" };

export const initialTamperHooksPanelState: TamperHooksPanelState = {
  list: { hooks: [], enabledByEnv: true, loading: true, error: null, togglingId: null },
  editor: null,
  deleteTarget: null,
  versionHistory: { hook: null, items: [], loading: false, expanded: null, rollbackTarget: null },
  dryRun: {
    target: null,
    inputMode: "synthetic",
    exchangeId: "",
    loadingExchange: false,
    exchangeWarning: null,
    input: {
      method: "GET",
      url: "https://example.test/api/v1/items",
      reqHeaders: "Accept: application/json",
      reqBody: "",
      status: "200",
      respHeaders: "Content-Type: application/json",
      respBody: '{"ok":true}',
    },
    running: false,
    error: null,
    result: null,
  },
  requests: createRequests<HookRequestKey>(["save", "delete", "rollback"]),
};

// Opening or closing the editor drops the last save error.
function withoutSaveError(state: TamperHooksPanelState): Requests<HookRequestKey> {
  return requestReducer<HookRequestKey>(state.requests, { type: "request/errorCleared", key: "save" });
}

export function tamperHooksPanelReducer(
  state: TamperHooksPanelState,
  action: TamperHooksPanelAction,
): TamperHooksPanelState {
  if (isRequestAction<HookRequestKey>(action)) {
    return setSlice(state, "requests", requestReducer(state.requests, action));
  }
  switch (action.type) {
    case "hooksLoadStarted":
      return { ...state, list: { ...state.list, loading: true, error: null } };
    case "hooksLoaded":
      return { ...state, list: { ...state.list, hooks: action.hooks, enabledByEnv: action.enabledByEnv } };
    case "hooksLoadFailed":
      return { ...state, list: { ...state.list, error: action.error } };
    case "hooksLoadFinished":
      return { ...state, list: { ...state.list, loading: false } };
    case "toggleStarted":
      return { ...state, list: { ...state.list, togglingId: action.id } };
    case "hookUpdated":
      return {
        ...state,
        list: { ...state.list, hooks: state.list.hooks.map((h) => (h.id === action.hook.id ? action.hook : h)) },
      };
    case "toggleFinished":
      return { ...state, list: { ...state.list, togglingId: null } };
    case "editorOpened":
      return { ...state, editor: action.form, requests: withoutSaveError(state) };
    case "editorFieldsChanged":
      if (!state.editor) return state;
      return { ...state, editor: { ...state.editor, ...action.patch } };
    case "editorClosed":
      return { ...state, editor: null, requests: withoutSaveError(state) };
    case "deleteRequested":
      return { ...state, deleteTarget: action.hook };
    case "deleteDialogClosed":
      return { ...state, deleteTarget: null };
    case "versionsOpened":
      return {
        ...state,
        versionHistory: { ...state.versionHistory, hook: action.hook, items: [], expanded: null, loading: true },
      };
    case "versionsLoaded":
      return { ...state, versionHistory: { ...state.versionHistory, items: action.versions } };
    case "versionsLoadFinished":
      return { ...state, versionHistory: { ...state.versionHistory, loading: false } };
    case "versionHistoryChanged":
      return setSlice(state, "versionHistory", mergePatch(state.versionHistory, action.patch));
    case "rolledBack":
      return { ...state, versionHistory: { ...state.versionHistory, rollbackTarget: null, hook: action.hook } };
    case "dryRunOpened":
      // Keeps the input fields and mode from the last run; clears its outcome.
      return {
        ...state,
        dryRun: { ...state.dryRun, target: action.target, error: null, result: null, exchangeWarning: null },
      };
    case "dryRunChanged":
      return setSlice(state, "dryRun", mergePatch(state.dryRun, action.patch));
    case "dryRunInputChanged":
      return { ...state, dryRun: { ...state.dryRun, input: { ...state.dryRun.input, ...action.patch } } };
    case "exchangeLoadStarted":
      return { ...state, dryRun: { ...state.dryRun, loadingExchange: true, error: null, exchangeWarning: null } };
    case "exchangeLoaded":
      return {
        ...state,
        dryRun: {
          ...state.dryRun,
          input: action.input,
          // No warning leaves whatever is there (cleared when the load started).
          exchangeWarning: action.warning ?? state.dryRun.exchangeWarning,
        },
      };
    case "exchangeLoadFailed":
      return { ...state, dryRun: { ...state.dryRun, error: action.error } };
    case "exchangeLoadFinished":
      return { ...state, dryRun: { ...state.dryRun, loadingExchange: false } };
    case "dryRunStarted":
      return { ...state, dryRun: { ...state.dryRun, running: true, error: null, result: null } };
    case "dryRunSucceeded":
      return { ...state, dryRun: { ...state.dryRun, result: action.result } };
    case "dryRunFailed":
      return { ...state, dryRun: { ...state.dryRun, error: action.error } };
    case "dryRunFinished":
      return { ...state, dryRun: { ...state.dryRun, running: false } };
  }
}

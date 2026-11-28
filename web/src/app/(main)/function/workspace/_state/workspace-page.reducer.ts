import { createList, isListAction, type ListAction, type ListState, listReducer } from "@/lib/reducers/list";
import { setSlice } from "@/lib/reducers/slice";
import type { WorkspaceEntry, WorkspaceFile } from "@/lib/types";

/** File open in the view / edit sheet. */
export type WorkspaceEditor = {
  file: WorkspaceFile;
  content: string;
  dirty: boolean;
  /** Kept on the editor (not a shared request flag): reopening a file starts idle, and a save that settles after
   * the sheet closed is ignored. */
  saving: boolean;
};

/** "New folder" dialog. */
export type WorkspaceMkdir = {
  open: boolean;
  name: string;
};

export type WorkspacePageState = {
  /** Directory currently listed in the table, as returned by the server. */
  path: string;
  /** Its entries. */
  list: ListState<WorkspaceEntry>;
  editor: WorkspaceEditor | null;
  mkdir: WorkspaceMkdir;
};

export type WorkspacePageAction =
  | ListAction<WorkspaceEntry>
  | { type: "directoryLoaded"; path: string; entries: WorkspaceEntry[] }
  | { type: "fileOpened"; file: WorkspaceFile }
  | { type: "editorClosed" }
  | { type: "editorContentChanged"; content: string }
  | { type: "saveStarted" }
  | { type: "saveSucceeded" }
  | { type: "saveFailed" }
  | { type: "mkdirOpenChanged"; open: boolean }
  | { type: "mkdirNameChanged"; name: string }
  | { type: "directoryCreated" };

export const initialWorkspacePageState: WorkspacePageState = {
  path: "",
  list: createList(),
  editor: null,
  mkdir: { open: false, name: "" },
};

function withEditor(
  state: WorkspacePageState,
  patch: (editor: WorkspaceEditor) => WorkspaceEditor,
): WorkspacePageState {
  if (!state.editor) return state;
  return { ...state, editor: patch(state.editor) };
}

export function workspacePageReducer(state: WorkspacePageState, action: WorkspacePageAction): WorkspacePageState {
  if (isListAction<WorkspaceEntry>(action)) return setSlice(state, "list", listReducer(state.list, action));
  switch (action.type) {
    case "directoryLoaded":
      return {
        ...state,
        path: action.path,
        list: listReducer(state.list, { type: "list/loaded", items: action.entries }),
      };
    case "fileOpened":
      return {
        ...state,
        editor: { file: action.file, content: action.file.content ?? "", dirty: false, saving: false },
      };
    case "editorClosed":
      return { ...state, editor: null };
    case "editorContentChanged":
      return withEditor(state, (e) => ({ ...e, content: action.content, dirty: true }));
    case "saveStarted":
      return withEditor(state, (e) => ({ ...e, saving: true }));
    case "saveSucceeded":
      return withEditor(state, (e) => ({ ...e, dirty: false, saving: false }));
    case "saveFailed":
      return withEditor(state, (e) => ({ ...e, saving: false }));
    case "mkdirOpenChanged":
      return { ...state, mkdir: { ...state.mkdir, open: action.open } };
    case "mkdirNameChanged":
      return { ...state, mkdir: { ...state.mkdir, name: action.name } };
    case "directoryCreated":
      return { ...state, mkdir: { open: false, name: "" } };
  }
}

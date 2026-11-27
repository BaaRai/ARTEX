// biome-ignore-all lint/suspicious/noUnnecessaryConditions: Biome cannot narrow generic action unions, so it flags every switch case; TypeScript checks exhaustiveness here.
// Shared slice for a server-loaded list: rows, total count and the loading cycle.
// `loaded` turns true once a load settles with `list/loadFinished`, so a page can tell
// "still loading for the first time" apart from "loaded and empty". A failed load ends
// with `list/loadFailed` instead, which stops loading but leaves `loaded` as it was, so a
// failed first load does not look like an empty list.

export type ListState<T> = {
  items: T[];
  total: number;
  loading: boolean;
  loaded: boolean;
};

export type ListAction<T> =
  | { type: "list/loadStarted" }
  | { type: "list/loaded"; items: T[]; total?: number }
  | { type: "list/loadFinished" }
  | { type: "list/loadFailed" }
  | { type: "list/cleared" };

export function createList<T>(): ListState<T> {
  return { items: [], total: 0, loading: false, loaded: false };
}

export function isListAction<T>(action: { type: string }): action is ListAction<T> {
  return action.type.startsWith("list/");
}

export function listReducer<T>(state: ListState<T>, action: ListAction<T>): ListState<T> {
  switch (action.type) {
    case "list/loadStarted":
      return state.loading ? state : { ...state, loading: true };
    case "list/loaded":
      return { ...state, items: action.items, total: action.total ?? action.items.length };
    case "list/loadFinished":
      return !state.loading && state.loaded ? state : { ...state, loading: false, loaded: true };
    case "list/loadFailed":
      return state.loading ? { ...state, loading: false } : state;
    case "list/cleared":
      return { ...state, items: [], total: 0, loaded: false };
  }
}

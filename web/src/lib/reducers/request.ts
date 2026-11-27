// biome-ignore-all lint/suspicious/noUnnecessaryConditions: Biome cannot narrow generic action unions, so it flags every switch case; TypeScript checks exhaustiveness here.
// Shared slice for user-triggered requests (save, delete, run...): whether each one
// is in flight and its last error. One state holds several named requests, e.g.
// `createRequests(["save", "delete"])`, so a page needs no separate busy flags.

export type RequestStatus = { pending: boolean; error: string };

export type Requests<K extends string> = Record<K, RequestStatus>;

export type RequestAction<K extends string> =
  | { type: "request/started"; key: K }
  | { type: "request/failed"; key: K; error: string }
  | { type: "request/finished"; key: K }
  | { type: "request/errorCleared"; key: K };

const IDLE: RequestStatus = { pending: false, error: "" };

export function createRequests<K extends string>(keys: readonly K[]): Requests<K> {
  return Object.fromEntries(keys.map((k) => [k, IDLE])) as Requests<K>;
}

export function isRequestAction<K extends string>(action: { type: string }): action is RequestAction<K> {
  return action.type.startsWith("request/");
}

function update<K extends string>(state: Requests<K>, key: K, next: RequestStatus): Requests<K> {
  const cur = state[key];
  if (cur.pending === next.pending && cur.error === next.error) return state;
  return { ...state, [key]: next };
}

export function requestReducer<K extends string>(state: Requests<K>, action: RequestAction<K>): Requests<K> {
  const cur = state[action.key];
  switch (action.type) {
    case "request/started":
      // Starting clears the previous error, like a fresh attempt.
      return update(state, action.key, { pending: true, error: "" });
    case "request/failed":
      return update(state, action.key, { ...cur, error: action.error });
    case "request/finished":
      return update(state, action.key, { ...cur, pending: false });
    case "request/errorCleared":
      return update(state, action.key, { ...cur, error: "" });
  }
}

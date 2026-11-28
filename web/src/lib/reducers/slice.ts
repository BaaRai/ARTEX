// Helpers for page reducers: replace one slice of the state, or merge a patch into a
// struct, returning the same object when nothing changed so React can skip the re-render.

export function setSlice<S, K extends keyof S>(state: S, key: K, next: S[K]): S {
  return next === state[key] ? state : { ...state, [key]: next };
}

/** Merge `changes` into `obj`; returns `obj` itself when every value is already equal (===). */
export function mergePatch<T extends object>(obj: T, changes: Partial<T>): T {
  for (const key of Object.keys(changes) as (keyof T)[]) {
    if (obj[key] !== changes[key]) return { ...obj, ...changes };
  }
  return obj;
}

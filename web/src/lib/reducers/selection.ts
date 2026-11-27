// biome-ignore-all lint/suspicious/noUnnecessaryConditions: Biome cannot narrow generic action unions, so it flags every switch case; TypeScript checks exhaustiveness here.
// Shared slice for multi-select (checkbox rows). Always produces a new Set on change
// and returns the same Set when nothing changed, so React can skip re-renders.

export type SelectionAction<K> =
  | { type: "selection/toggled"; id: K }
  | { type: "selection/set"; ids: Iterable<K> }
  /** Check (`selected: true`) or uncheck several ids at once, e.g. a row or a whole page checkbox. */
  | { type: "selection/changed"; ids: Iterable<K>; selected: boolean }
  | { type: "selection/cleared" }
  /** Drop selected ids that are no longer present (e.g. after a reload). */
  | { type: "selection/pruned"; keep: Iterable<K> };

export function isSelectionAction<K>(action: { type: string }): action is SelectionAction<K> {
  return action.type.startsWith("selection/");
}

export function selectionReducer<K>(state: Set<K>, action: SelectionAction<K>): Set<K> {
  switch (action.type) {
    case "selection/toggled": {
      const next = new Set(state);
      if (next.has(action.id)) next.delete(action.id);
      else next.add(action.id);
      return next;
    }
    case "selection/set": {
      const next = new Set(action.ids);
      return next.size === state.size && [...next].every((id) => state.has(id)) ? state : next;
    }
    case "selection/changed": {
      const next = new Set(state);
      for (const id of action.ids) {
        if (action.selected) next.add(id);
        else next.delete(id);
      }
      return next.size === state.size && [...next].every((id) => state.has(id)) ? state : next;
    }
    case "selection/cleared":
      return state.size === 0 ? state : new Set();
    case "selection/pruned": {
      const keep = new Set(action.keep);
      const next = new Set([...state].filter((id) => keep.has(id)));
      return next.size === state.size ? state : next;
    }
  }
}

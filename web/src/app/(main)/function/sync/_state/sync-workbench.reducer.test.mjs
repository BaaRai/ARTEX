import { initialSyncWorkbenchState, syncWorkbenchReducer } from "./sync-workbench.reducer.ts";
import assert from "node:assert/strict";
import test from "node:test";

const reduce = (actions, state = initialSyncWorkbenchState) => actions.reduce(syncWorkbenchReducer, state);

test("select-all toggles between this page's rows and nothing; reload drops the selection", () => {
  const all = reduce([
    { type: "selection/toggled", id: "a" },
    { type: "allRowsToggled", ids: ["a", "b"] },
  ]);
  assert.deepEqual([...all.selection], ["a", "b"]);
  assert.equal(syncWorkbenchReducer(all, { type: "allRowsToggled", ids: ["a", "b"] }).selection.size, 0);
  const reloading = syncWorkbenchReducer(all, { type: "listLoadStarted" });
  assert.equal(reloading.selection.size, 0);
  assert.equal(reloading.list.loading, true);
});

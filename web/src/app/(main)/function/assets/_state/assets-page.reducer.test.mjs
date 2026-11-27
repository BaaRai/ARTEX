import { assetsPageReducer, initialAssetsPageState } from "./assets-page.reducer.ts";
import assert from "node:assert/strict";
import test from "node:test";

const reduce = (actions, state = initialAssetsPageState) => actions.reduce(assetsPageReducer, state);

const loadedIpTab = () =>
  reduce([
    { type: "tabChanged", tab: "ip" },
    { type: "queryChanged", query: "port=443" },
    { type: "pageChanged", page: 2 },
    { type: "listLoaded", tab: "ip", rows: [{ id: 1 }], total: 120 },
    { type: "list/loadFinished" },
    { type: "selection/toggled", id: 1 },
  ]);

test("switching tabs resets query, page, selection and the list", () => {
  const s = assetsPageReducer(loadedIpTab(), { type: "tabChanged", tab: "app" });
  assert.deepEqual(s.filters, { tab: "app", query: "" });
  assert.equal(s.pagination.page, 0);
  assert.equal(s.selection.size, 0);
  assert.deepEqual(
    [s.list.items, s.listInfo.rowsTab, s.list.total, s.list.loaded, s.listInfo.dslError],
    [[], "", 0, false, ""],
  );
});

test("page and size accept values and updater functions", () => {
  const s = reduce([
    { type: "pageChanged", page: 3 },
    { type: "pageChanged", page: (p) => p + 1 },
  ]);
  assert.equal(s.pagination.page, 4);
  const sized = assetsPageReducer(s, { type: "pageSizeChanged", size: (n) => n * 2 });
  assert.deepEqual(sized.pagination, { page: 0, size: 100, firstPage: 0 });
  assert.equal(assetsPageReducer(sized, { type: "pageSizeChanged", size: 100 }), sized);
  assert.equal(assetsPageReducer(sized, { type: "pageChanged", page: (p) => p - 1 }), sized, "clamped to page 0");
});

test("a failed load shows the error and empties the list", () => {
  const s = reduce(
    [{ type: "listLoadFailed", tab: "ip", error: "bad dsl" }, { type: "list/loadFinished" }],
    loadedIpTab(),
  );
  assert.deepEqual(
    [s.list.items, s.listInfo.rowsTab, s.list.total, s.listInfo.dslError, s.list.loaded, s.list.loading],
    [[], "ip", 0, "bad dsl", true, false],
  );
  const ok = assetsPageReducer(s, { type: "listLoaded", tab: "ip", rows: [], total: 0 });
  assert.equal(ok.listInfo.dslError, "");
});

test("toggling a page selects all rows, then deselects only that page", () => {
  const s = reduce([
    { type: "selection/toggled", id: 9 },
    { type: "selection/toggled", id: 1 },
    { type: "pageRowsToggled", ids: [1, 2] },
  ]);
  assert.deepEqual([...s.selection].sort(), [1, 2, 9]);
  const off = assetsPageReducer(s, { type: "pageRowsToggled", ids: [1, 2] });
  assert.deepEqual([...off.selection], [9]);
  assert.deepEqual([...s.selection].sort(), [1, 2, 9], "previous state not mutated");
});

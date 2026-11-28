import {
  createInitialFindingsPageState,
  EMPTY_ASSET_TREE,
  EMPTY_FLAT_STATE,
  findingsPageReducer,
} from "./findings-page.reducer.ts";
import assert from "node:assert/strict";
import test from "node:test";

const reduce = (actions, state = createInitialFindingsPageState()) => actions.reduce(findingsPageReducer, state);

test("preferences restore filters and view and mark hydration", () => {
  const s = reduce([{ type: "preferencesRestored", view: "grouped", filters: { severity: "high", sort: "time" } }]);
  assert.equal(s.view, "grouped");
  assert.equal(s.filters.severity, "high");
  assert.equal(s.filters.sort, "time");
  assert.equal(s.filters.status, "all");
  assert.equal(s.preferencesHydrated, true);
  const empty = reduce([{ type: "preferencesRestored", filters: {} }]);
  assert.equal(empty.view, "flat");
  assert.equal(empty.preferencesHydrated, true);
});

test("filter fingerprint change resets pagination, expansion and snapshots but keeps the edit buffer", () => {
  const edit = { name: "x", vulnclass: "xss", severity: "high" };
  const s = reduce([
    { type: "groupPagination", action: { type: "pagination/pageChanged", page: 3 } },
    { type: "pagination/pageChanged", page: 4 },
    { type: "list/loaded", items: [{ id: 1 }], total: 1 },
    { type: "list/loadFinished" },
    { type: "groupToggled", key: "7", open: true },
    { type: "groupLoadStarted", key: "7", page: 1, pageSize: 10 },
    { type: "assetScopeSelected", scope: "host:a" },
    { type: "rowExpanded", key: "finding:1", edit },
    { type: "filterFingerprintChanged" },
  ]);
  assert.equal(s.groupPagination.page, 1);
  assert.equal(s.grouped.expanded.size, 0);
  assert.deepEqual(s.grouped.findings, {});
  assert.equal(s.pagination.page, 1);
  assert.equal(s.flat, EMPTY_FLAT_STATE);
  assert.equal(s.asset.scope, null);
  assert.equal(s.asset.tree, EMPTY_ASSET_TREE);
  assert.equal(s.expandedRow.key, null);
  assert.equal(s.expandedRow.edit, edit);
});

const _groupPagination = (action) => ({ type: "groupPagination", action });

test("group load lifecycle keeps the previous snapshot while loading and on failure", () => {
  const item = { id: 1 };
  const loaded = reduce([
    { type: "groupLoaded", key: "7", items: [item], total: 11, page: 2, pageSize: 10 },
    { type: "groupLoadStarted", key: "7", page: 1, pageSize: 10 },
  ]);
  assert.deepEqual(loaded.grouped.findings["7"], {
    items: [item],
    total: 11,
    page: 1,
    pageSize: 10,
    loaded: true,
    loading: true,
  });
  const failed = findingsPageReducer(loaded, { type: "groupLoadFailed", key: "7", page: 1, pageSize: 10 });
  assert.equal(failed.grouped.findings["7"].loading, false);
  assert.deepEqual(failed.grouped.findings["7"].items, [item]);
  const fresh = reduce([{ type: "groupLoadFailed", key: "9", page: 2, pageSize: 20 }]);
  assert.deepEqual(fresh.grouped.findings["9"], {
    items: [],
    total: 0,
    page: 2,
    pageSize: 20,
    loaded: false,
    loading: false,
  });
});

test("findingsUpdated rewrites the flat list and every group cache", () => {
  const s = reduce([
    { type: "list/loaded", items: [{ id: 1 }, { id: 2 }], total: 2 },
    { type: "groupLoaded", key: "a", items: [{ id: 2 }], total: 1, page: 1, pageSize: 10 },
    { type: "findingsUpdated", update: (items) => items.filter((f) => f.id !== 2) },
  ]);
  assert.deepEqual(s.flat.items, [{ id: 1 }]);
  assert.deepEqual(s.grouped.findings.a.items, []);
});

test("finding deletion drops it from the selection, decrements totals and collapses its row", () => {
  const s = reduce([
    { type: "list/loaded", items: [], total: 1 },
    { type: "groupsLoaded", groups: [], groupTotal: 1, findingTotal: 0 },
    { type: "selectionToggled", ids: ["f1", "f2"], checked: true },
    { type: "rowExpanded", key: "finding:f1", edit: { name: "", vulnclass: "", severity: "low" } },
    { type: "findingDeleted", findingId: "f1", rowKey: "finding:f1" },
  ]);
  assert.deepEqual([...s.selection], ["f2"]);
  assert.equal(s.flat.total, 0);
  assert.equal(s.grouped.findingTotal, 0);
  assert.equal(s.expandedRow.key, null);
  const other = reduce([
    { type: "rowExpanded", key: "finding:f2", edit: { name: "", vulnclass: "", severity: "low" } },
    { type: "findingDeleted", findingId: "f1", rowKey: "finding:f1" },
  ]);
  assert.equal(other.expandedRow.key, "finding:f2");
});

test("export dialog defaults its scope to the selection", () => {
  assert.equal(reduce([{ type: "exportOpened" }]).exportDialog.scope, "filtered");
  const s = reduce([
    { type: "exportDialogChanged", patch: { scope: "all" } },
    { type: "selectionToggled", ids: ["a"], checked: true },
    { type: "exportOpened" },
  ]);
  assert.equal(s.exportDialog.open, true);
  assert.equal(s.exportDialog.scope, "selected");
});

test("editChanged accepts values and functional updates", () => {
  const s = reduce([
    { type: "rowExpanded", key: "k", edit: { name: "a", vulnclass: "xss", severity: "low" } },
    { type: "editChanged", edit: (e) => (e ? { ...e, name: "b" } : e) },
  ]);
  assert.equal(s.expandedRow.edit.name, "b");
  const collapsed = reduce([{ type: "rowCollapsed" }], s);
  assert.equal(collapsed.expandedRow.key, null);
  assert.equal(collapsed.expandedRow.edit, null);
  assert.equal(findingsPageReducer(collapsed, { type: "editChanged", edit: (e) => e }), collapsed);
});

import { createTrafficPageState, trafficPageReducer } from "./traffic-page.reducer.ts";
import assert from "node:assert/strict";
import test from "node:test";

const reduce = (actions, state = createTrafficPageState()) => actions.reduce(trafficPageReducer, state);
const input = (field, value) => ({ type: "filterInputChanged", field, value });

test("debounce applies every input trimmed and resets to the first page", () => {
  const s = reduce([
    { type: "pagination/pageChanged", page: 3 },
    input("host", " example.test "),
    input("respMax", "1024 "),
    { type: "filtersDebounced" },
  ]);
  assert.deepEqual(s.filters.applied, {
    host: "example.test",
    query: "",
    body: "",
    path: "",
    respMin: "",
    respMax: "1024",
  });
  assert.equal(s.filters.input.host, " example.test ");
  assert.equal(s.pagination.page, 0);
});

test("debounce with unchanged trimmed values returns the same state", () => {
  const applied = reduce([
    input("query", "login"),
    { type: "filtersDebounced" },
    { type: "pagination/pageChanged", page: 2 },
  ]);
  const typed = reduce([input("query", " login  ")], applied);
  assert.equal(trafficPageReducer(typed, { type: "filtersDebounced" }), typed);
  assert.equal(typed.pagination.page, 2);
});

test("clearing advanced filters keeps host/query and resets the page only if something applied", () => {
  const filtered = reduce([
    input("host", "a.test"),
    input("body", "token"),
    { type: "filtersDebounced" },
    { type: "statusFilterChanged", status: "4xx" },
    { type: "pagination/pageChanged", page: 2 },
  ]);
  const cleared = trafficPageReducer(filtered, { type: "advancedFiltersCleared" });
  assert.equal(cleared.filters.applied.host, "a.test");
  assert.equal(cleared.filters.input.body, "");
  assert.equal(cleared.filters.applied.body, "");
  assert.equal(cleared.filters.status, "all");
  assert.equal(cleared.pagination.page, 0);

  // Only a pending (not yet applied) advanced input: cleared, page kept.
  const pending = reduce([input("path", "/api"), { type: "pagination/pageChanged", page: 3 }]);
  const s = trafficPageReducer(pending, { type: "advancedFiltersCleared" });
  assert.equal(s.filters.input.path, "");
  assert.equal(s.pagination.page, 3);
});

test("previous/next page clamp like the old functional updaters", () => {
  assert.equal(reduce([{ type: "previousPageRequested" }]).pagination.page, 0);
  const s = reduce([
    { type: "nextPageRequested", pageCount: 2 },
    { type: "nextPageRequested", pageCount: 2 },
  ]);
  assert.equal(s.pagination.page, 1);
  // Previous does not clamp to the page count (the list may have shrunk under the user).
  const far = reduce([{ type: "pagination/pageChanged", page: 5 }, { type: "previousPageRequested" }]);
  assert.equal(far.pagination.page, 4);
  // Already on the first page: nothing changes.
  const first = createTrafficPageState();
  assert.equal(trafficPageReducer(first, { type: "previousPageRequested" }), first);
});

test("detail lifecycle: loading resets the tab, stream exchanges open on Stream", () => {
  const s = reduce([
    { type: "inspectorChanged", patch: { tab: "replay" } },
    { type: "detailLoadStarted" },
    { type: "detailLoaded", detail: { req: "r", resp: "", stream_kind: "websocket" } },
    { type: "detailLoadFinished" },
  ]);
  assert.equal(s.inspector.tab, "stream");
  assert.equal(s.inspector.loading, false);
  const plain = reduce([{ type: "detailLoadStarted" }, { type: "detailLoaded", detail: { req: "r", resp: "" } }]);
  assert.equal(plain.inspector.tab, "request");
  const cleared = reduce([{ type: "inspectorChanged", patch: { tab: "response" } }, { type: "detailCleared" }], s);
  assert.equal(cleared.inspector.tab, "request");
  assert.equal(cleared.inspector.detail, null);
});

test("delete success clears selection, refetches from the first page; filter mode keeps picked hosts", () => {
  const base = reduce([
    { type: "inspectorChanged", patch: { selected: { id: "x" } } },
    { type: "hostPickerChanged", patch: { selected: ["a"] } },
    { type: "hostPickerChanged", patch: { open: true } },
    { type: "pagination/pageChanged", page: 3 },
    { type: "deleteRequested", mode: "filter" },
    { type: "request/started", key: "delete" },
  ]);
  const byFilter = trafficPageReducer(base, { type: "deleteSucceeded", mode: "filter" });
  assert.equal(byFilter.deleteMode, null);
  assert.equal(byFilter.requests.delete.pending, true);
  assert.equal(byFilter.inspector.selected, null);
  assert.equal(byFilter.pagination.page, 0);
  assert.equal(byFilter.list.reloadTick, base.list.reloadTick + 1);
  assert.deepEqual(byFilter.hostPicker.selected, ["a"]);
  assert.equal(byFilter.hostPicker.open, true);

  const byHosts = trafficPageReducer(base, { type: "deleteSucceeded", mode: "selected" });
  assert.deepEqual(byHosts.hostPicker.selected, []);
  assert.equal(byHosts.hostPicker.open, false);
  assert.equal(reduce([{ type: "request/finished", key: "delete" }], byHosts).requests.delete.pending, false);
});

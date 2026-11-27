import { createList, listReducer } from "@/lib/reducers/list";
import { createPagination, lastPage, paginationReducer, toFirstPage } from "@/lib/reducers/pagination";
import { createRequests, requestReducer } from "@/lib/reducers/request";
import { selectionReducer } from "@/lib/reducers/selection";
import { mergePatch, setSlice } from "@/lib/reducers/slice";

import assert from "node:assert/strict";
import test from "node:test";

test("pagination: clamps to the first page and skips no-op changes", () => {
  const zero = createPagination(50);
  assert.equal(paginationReducer(zero, { type: "pagination/pageChanged", page: -3 }), zero);
  const one = createPagination(20, 1);
  assert.equal(paginationReducer(one, { type: "pagination/pageChanged", page: 0 }).page, 1);
  const p2 = paginationReducer(one, { type: "pagination/pageChanged", page: 2 });
  assert.equal(p2.page, 2);
  assert.equal(paginationReducer(p2, { type: "pagination/pageChanged", page: 2 }), p2);
});

test("pagination: size change returns to the first page; same size is a no-op", () => {
  const p = paginationReducer(createPagination(20, 1), { type: "pagination/pageChanged", page: 4 });
  assert.equal(paginationReducer(p, { type: "pagination/sizeChanged", size: 20 }), p);
  assert.deepEqual(paginationReducer(p, { type: "pagination/sizeChanged", size: 50 }), {
    page: 1,
    size: 50,
    firstPage: 1,
  });
});

test("pagination: toFirstPage and lastPage for 0- and 1-based pagers", () => {
  const zero = createPagination(10);
  assert.equal(toFirstPage(zero), zero);
  assert.equal(lastPage(zero, 0), 0);
  assert.equal(lastPage(zero, 25), 2);
  const one = createPagination(10, 1);
  assert.equal(lastPage(one, 0), 1);
  assert.equal(lastPage(one, 25), 3);
  assert.equal(toFirstPage({ ...one, page: 3 }).page, 1);
});

test("list: load cycle sets loaded once settled; total defaults to item count", () => {
  let s = listReducer(createList(), { type: "list/loadStarted" });
  assert.equal(s.loading, true);
  assert.equal(listReducer(s, { type: "list/loadStarted" }), s);
  s = listReducer(s, { type: "list/loaded", items: [1, 2] });
  assert.equal(s.total, 2);
  s = listReducer(s, { type: "list/loaded", items: [1], total: 40 });
  assert.equal(s.total, 40);
  s = listReducer(s, { type: "list/loadFinished" });
  assert.deepEqual([s.loading, s.loaded], [false, true]);
  assert.equal(listReducer(s, { type: "list/loadFinished" }), s);
  assert.deepEqual(listReducer(s, { type: "list/cleared" }), { items: [], total: 0, loading: false, loaded: false });
});

test("request: start clears the error, failure keeps pending until finished", () => {
  let s = createRequests(["save", "delete"]);
  s = requestReducer(s, { type: "request/started", key: "save" });
  assert.deepEqual(s.save, { pending: true, error: "" });
  s = requestReducer(s, { type: "request/failed", key: "save", error: "boom" });
  assert.deepEqual(s.save, { pending: true, error: "boom" });
  s = requestReducer(s, { type: "request/finished", key: "save" });
  assert.deepEqual(s.save, { pending: false, error: "boom" });
  assert.deepEqual(s.delete, { pending: false, error: "" });
  assert.equal(requestReducer(s, { type: "request/finished", key: "save" }), s);
  s = requestReducer(s, { type: "request/started", key: "save" });
  assert.equal(s.save.error, "");
});

test("selection: toggle, set, prune and clear return new Sets only on change", () => {
  const empty = new Set();
  assert.equal(selectionReducer(empty, { type: "selection/cleared" }), empty);
  let s = selectionReducer(empty, { type: "selection/toggled", id: "a" });
  s = selectionReducer(s, { type: "selection/toggled", id: "b" });
  assert.deepEqual([...s], ["a", "b"]);
  assert.equal(selectionReducer(s, { type: "selection/set", ids: ["b", "a"] }), s);
  assert.equal(selectionReducer(s, { type: "selection/pruned", keep: ["a", "b", "c"] }), s);
  assert.deepEqual([...selectionReducer(s, { type: "selection/pruned", keep: ["b"] })], ["b"]);
  assert.deepEqual([...selectionReducer(s, { type: "selection/toggled", id: "a" })], ["b"]);
});

test("setSlice keeps the same state object when the slice is unchanged", () => {
  const state = { a: { x: 1 }, b: 2 };
  assert.equal(setSlice(state, "a", state.a), state);
  const next = setSlice(state, "a", { x: 2 });
  assert.deepEqual(next, { a: { x: 2 }, b: 2 });
  assert.notEqual(next, state);
});

test("list: a failed load stops loading without marking the list as loaded", () => {
  const loading = listReducer(createList(), { type: "list/loadStarted" });
  const failed = listReducer(loading, { type: "list/loadFailed" });
  assert.deepEqual([failed.loading, failed.loaded], [false, false]);
  assert.equal(listReducer(failed, { type: "list/loadFailed" }), failed);
});

test("selection/changed checks or unchecks several ids; no-op returns the same Set", () => {
  const s = new Set(["a"]);
  assert.deepEqual(
    [...selectionReducer(s, { type: "selection/changed", ids: ["b", "c"], selected: true })],
    ["a", "b", "c"],
  );
  assert.deepEqual([...selectionReducer(s, { type: "selection/changed", ids: ["a", "z"], selected: false })], []);
  assert.equal(selectionReducer(s, { type: "selection/changed", ids: ["a"], selected: true }), s);
  assert.equal(selectionReducer(s, { type: "selection/changed", ids: ["z"], selected: false }), s);
});

test("mergePatch merges changes and keeps the object when nothing changed", () => {
  const form = { name: "a", size: 1 };
  assert.equal(mergePatch(form, { name: "a" }), form);
  assert.deepEqual(mergePatch(form, { size: 2 }), { name: "a", size: 2 });
});

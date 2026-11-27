import { initialLLMRecordsPageState, llmRecordsPageReducer } from "./llm-records-page.reducer.ts";
import assert from "node:assert/strict";
import test from "node:test";

const reduce = (actions, state = initialLLMRecordsPageState) => actions.reduce(llmRecordsPageReducer, state);

test("debounced session trims and resets to the first page", () => {
  const s = reduce([
    { type: "pagination/pageChanged", page: 3 },
    { type: "sessionChanged", session: "  abc " },
    { type: "sessionDebounced" },
  ]);
  assert.equal(s.filters.debouncedSession, "abc");
  assert.equal(s.pagination.page, 0);
});

test("recording toggle is optimistic, takes the server value, and reverts on failure", () => {
  const loaded = reduce([{ type: "recordingLoaded", enabled: false }]);
  assert.deepEqual(loaded.recording, { enabled: false, known: true, busy: false });
  const started = llmRecordsPageReducer(loaded, { type: "recordingToggleStarted", on: true });
  assert.deepEqual(started.recording, { enabled: true, known: true, busy: true });
  const failed = reduce([{ type: "recordingSaveFailed", on: true }, { type: "recordingToggleFinished" }], started);
  assert.deepEqual(failed.recording, { enabled: false, known: true, busy: false });
  const saved = reduce([{ type: "recordingSaved", enabled: false }], started);
  assert.equal(saved.recording.enabled, false);
});

test("detail load clears old data; clearing keeps the loading flag", () => {
  const rec = { id: 1 };
  const s = reduce([
    { type: "recordSelected", record: rec },
    { type: "detailLoadStarted" },
    { type: "detailLoaded", detail: { id: 1 } },
    { type: "detailLoadFinished" },
  ]);
  assert.deepEqual(s.detail.data, { id: 1 });
  const next = llmRecordsPageReducer(s, { type: "detailLoadStarted" });
  assert.deepEqual([next.detail.data, next.detail.loading], [null, true]);
  const cleared = reduce([{ type: "recordSelected", record: null }, { type: "detailCleared" }], next);
  assert.deepEqual([cleared.detail.selected, cleared.detail.data, cleared.detail.loading], [null, null, true]);
});

test("deleting a task's records resets the view and bumps the reload tick", () => {
  const s = reduce([
    { type: "taskFilterChanged", task: "7" },
    { type: "pagination/pageChanged", page: 2 },
    { type: "recordSelected", record: { id: 1 } },
    { type: "detailLoaded", detail: { id: 1 } },
    { type: "deleteDialogOpenChanged", open: true },
    { type: "request/started", key: "deleteTask" },
    { type: "taskRecordsDeleted" },
    { type: "request/finished", key: "deleteTask" },
  ]);
  assert.equal(s.filters.task, "");
  assert.equal(s.pagination.page, 0);
  assert.equal(s.reloadTick, 1);
  assert.deepEqual([s.detail.selected, s.detail.data], [null, null]);
  assert.equal(s.deleteDialogOpen, false);
  assert.deepEqual(s.requests.deleteTask, { pending: false, error: "" });
});

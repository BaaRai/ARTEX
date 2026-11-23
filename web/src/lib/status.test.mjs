import assert from "node:assert/strict";
import test from "node:test";

import { statusMeta, taskStatusMeta } from "./status.ts";

test("a running task shows one status with the engine state as a sub-label", () => {
  assert.deepEqual(taskStatusMeta("running", "idle"), { label: "Running", detail: "idle", tone: "info" });
  assert.deepEqual(taskStatusMeta("running", "stalled"), { label: "Running", detail: "stalled", tone: "danger" });
  assert.deepEqual(taskStatusMeta("running", "exploring"), { label: "Running", detail: "exploring", tone: "info" });
});

test("non-running tasks ignore the engine state", () => {
  assert.deepEqual(taskStatusMeta("done", "idle"), { label: "Completed", tone: "success" });
  assert.deepEqual(taskStatusMeta("paused", "paused"), { label: "Paused", tone: "warning" });
  assert.deepEqual(taskStatusMeta("running", undefined), { label: "Running", tone: "info" });
});

test("a paused engine reads as a paused task, not 'Running · paused'", () => {
  assert.deepEqual(taskStatusMeta("running", "paused"), { label: "Paused", tone: "warning" });
});

test("amber means only 'waiting on a person'", () => {
  assert.equal(statusMeta("task", "queued").tone, "slate");
  assert.equal(statusMeta("task", "timeout").tone, "danger");
  assert.equal(statusMeta("finding", "pending").tone, "slate");
  assert.equal(statusMeta("severity", "medium").tone, "sev-medium");
});

test("severity levels each have their own tone", () => {
  const tones = ["critical", "high", "medium", "low"].map((s) => statusMeta("severity", s).tone);
  assert.equal(new Set(tones).size, 4);
});

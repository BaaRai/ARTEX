import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
function moduleFor(name, globals = {}) {
  const output = ts.transpileModule(fs.readFileSync(path.join(web, "src/lib", name + ".ts"), "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const exports = {};
  vm.runInNewContext(output, { exports, ...globals });
  return exports;
}
function setup(hidden = false) {
  let sequence = 0;
  const timers = new Map();
  const listeners = new Set();
  const document = {
    visibilityState: hidden ? "hidden" : "visible",
    addEventListener: (_, callback) => listeners.add(callback),
    removeEventListener: (_, callback) => listeners.delete(callback),
  };
  const { setVisibleInterval } = moduleFor("polling", {
    document,
    setTimeout: (callback) => {
      const id = ++sequence;
      timers.set(id, callback);
      return id;
    },
    clearTimeout: (id) => timers.delete(id),
  });
  return {
    setVisibleInterval,
    timers,
    listeners,
    visibility(hidden) {
      document.visibilityState = hidden ? "hidden" : "visible";
      for (const callback of listeners) callback();
    },
    tick() {
      const entries = [...timers];
      timers.clear();
      for (const [, callback] of entries) callback();
    },
  };
}
async function settle() {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}
test("hidden tabs make no scheduled requests; returning refreshes immediately and cleanup stops work", async () => {
  const s = setup(true);
  let calls = 0;
  const stop = s.setVisibleInterval(() => {
    calls++;
  }, 5000);
  s.tick();
  await settle();
  assert.equal(calls, 0);
  assert.equal(s.timers.size, 0);
  s.visibility(false);
  await settle();
  assert.equal(calls, 1);
  assert.equal(s.timers.size, 1);
  s.visibility(true);
  assert.equal(s.timers.size, 0);
  s.visibility(false);
  await settle();
  assert.equal(calls, 2);
  stop();
  s.tick();
  s.visibility(false);
  await settle();
  assert.equal(calls, 2);
  assert.equal(s.listeners.size, 0);
  assert.equal(s.timers.size, 0);
});
test("a slow poll cannot overlap on timer or visibility changes; dispose while running cannot revive it", async () => {
  const s = setup();
  let calls = 0;
  let release;
  const stop = s.setVisibleInterval(() => {
    calls++;
    return new Promise((resolve) => {
      release = resolve;
    });
  }, 2000);
  s.tick();
  assert.equal(calls, 1);
  s.visibility(true);
  s.visibility(false);
  s.tick();
  assert.equal(calls, 1);
  release();
  await settle();
  assert.equal(s.timers.size, 1);
  s.tick();
  assert.equal(calls, 2);
  stop();
  release();
  await settle();
  assert.equal(s.timers.size, 0);
});
test("a failed background refresh retries on the next interval", async () => {
  const s = setup();
  let calls = 0;
  const stop = s.setVisibleInterval(async () => {
    if (++calls === 1) throw Error("temporary");
  }, 1000);
  s.tick();
  await settle();
  assert.equal(s.timers.size, 1);
  s.tick();
  await settle();
  assert.equal(calls, 2);
  stop();
});
test("concurrent identical reads share transport, while identities and URLs remain independent", async () => {
  const { InflightReads } = moduleFor("inflight");
  const reads = new InflightReads();
  let calls = 0;
  let release;
  const load = () => {
    calls++;
    return new Promise((resolve) => {
      release = resolve;
    });
  };
  const first = reads.run("account-a:/task/1", load);
  const duplicate = reads.run("account-a:/task/1", load);
  assert.equal(first, duplicate);
  assert.equal(calls, 1);
  await reads.run("account-b:/task/1", async () => {
    calls++;
    return "b";
  });
  await reads.run("account-a:/task/2", async () => {
    calls++;
    return "two";
  });
  assert.equal(calls, 3);
  release("first");
  await first;
  assert.equal(await reads.run("account-a:/task/1", async () => "fresh"), "fresh");
});
test("writes invalidate pending reads without allowing older completion to evict a newer request", async () => {
  const { InflightReads } = moduleFor("inflight");
  const reads = new InflightReads();
  let firstDone, secondDone;
  const first = reads.run(
    "key",
    () =>
      new Promise((resolve) => {
        firstDone = resolve;
      }),
  );
  reads.clear();
  const second = reads.run(
    "key",
    () =>
      new Promise((resolve) => {
        secondDone = resolve;
      }),
  );
  firstDone("old");
  await first;
  assert.equal(
    reads.run("key", async () => "unexpected"),
    second,
  );
  secondDone("new");
  assert.equal(await second, "new");
});
test("failed shared reads are removed so retries can succeed", async () => {
  const { InflightReads } = moduleFor("inflight");
  const reads = new InflightReads();
  await assert.rejects(
    reads.run("key", async () => {
      throw Error("temporary");
    }),
  );
  assert.equal(await reads.run("key", async () => 42), 42);
});

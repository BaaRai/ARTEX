import ts from "typescript";

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { setImmediate } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const modules = new Map();

// Run the same TypeScript modules as the UI, without a browser or live server.
// Only the locale, mock switch and artificial mock delay are replaced.
function loadModule(file) {
  if (modules.has(file)) return modules.get(file);
  const exports = {};
  modules.set(file, exports);
  const output = ts.transpileModule(fs.readFileSync(file, "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  vm.runInNewContext(output, {
    exports,
    Error,
    URL,
    URLSearchParams,
    TextEncoder,
    structuredClone,
    setTimeout: (callback) => callback(),
    require: (name) => {
      if (name === "@/i18n") return { t: (key) => key };
      if (name === "@/lib/mock/enabled") return { MOCK: true };
      const base = name.startsWith("@/")
        ? path.join(web, "src", name.slice(2))
        : path.resolve(path.dirname(file), name);
      return loadModule(`${base}.ts`);
    },
  });
  return exports;
}

const { createTrafficStreamPager } = loadModule(path.join(web, "src/lib/traffic-stream.ts"));
const { api } = loadModule(path.join(web, "src/lib/api.ts"));
const plain = (value) => JSON.parse(JSON.stringify(value));
// Await an event-loop turn so promises crossing the vm context also settle.
const flush = () => setImmediate();

function message(seq, direction = "c2s") {
  return { seq, direction, ts: seq, kind: 1, type: "text", size: 1, payload: String(seq) };
}

function pendingPager() {
  const calls = [];
  let state;
  const pager = createTrafficStreamPager(
    (...args) =>
      new Promise((resolve, reject) => {
        calls.push({ args, resolve, reject });
      }),
    (next) => {
      state = next;
    },
  );
  return { pager, calls, state: () => state };
}

test("API pages the selected direction and keeps original seqs and the limit warning", async () => {
  const all = await api.trafficStream("x-ws");
  assert.equal(all.total, 121);
  assert.equal(all.items.length, 50);
  assert.deepEqual(plain(all.items.slice(0, 2).map((item) => item.direction)), ["c2s", "s2c"]);

  for (const direction of ["c2s", "s2c"]) {
    const first = await api.trafficStream("x-ws", 0, 50, direction);
    const last = await api.trafficStream("x-ws", 50, 50, direction);
    assert.equal(first.total, 61);
    assert.equal(last.total, 61);
    assert.equal(first.items.length, 50);
    assert.equal(last.items.length, 11);
    assert.equal(first.items[0].seq, direction === "c2s" ? 1 : 2);
    assert.equal(last.items[0].seq, direction === "c2s" ? 101 : 102);
    assert.ok(
      [...first.items, ...last.items].every((item) => item.direction === direction || item.direction === "meta"),
    );
    assert.equal(last.items.at(-1).type, "meta");
    assert.equal(last.items.at(-1).seq, 121);
  }
});

test("mock stream details expose both protocols and an empty SSE client direction", async () => {
  assert.equal((await api.trafficExchange("x-ws")).stream_kind, "websocket");
  assert.equal((await api.trafficExchange("x-sse")).stream_kind, "sse");
  assert.equal((await api.trafficExchange("x-9")).stream_kind, undefined);
  assert.deepEqual(plain(await api.trafficStream("x-sse", 0, 50, "c2s")), { items: [], total: 0 });
  assert.equal((await api.trafficStream("x-sse", 0, 50, "s2c")).total, 3);
});

test("changing the filter or exchange resets pagination and discards a stale response", async () => {
  const p = pendingPager();
  p.pager.select("ws", "c2s");
  p.calls[0].resolve({ items: [message(1), message(3)], total: 60 });
  await flush();
  p.pager.loadMore();

  p.pager.select("ws", "s2c");
  assert.deepEqual(plain(p.state().items), []);
  assert.equal(p.state().total, 0);
  assert.equal(p.state().loadingMore, false);
  assert.deepEqual(p.calls[2].args, ["ws", 0, 50, "s2c"]);
  p.calls[1].resolve({ items: [message(5)], total: 60 });
  await flush();
  assert.deepEqual(plain(p.state().items), []);
  assert.equal(p.state().loading, true);

  p.pager.select("sse", "s2c");
  p.calls[2].resolve({ items: [message(2, "s2c")], total: 60 });
  p.calls[3].resolve({ items: [message(1, "s2c")], total: 1 });
  await flush();
  assert.deepEqual(p.calls[3].args, ["sse", 0, 50, "s2c"]);
  assert.deepEqual(plain(p.state().items.map((item) => item.seq)), [1]);
  assert.equal(p.state().total, 1);
});

test("Load more uses the filtered offset, and Refresh invalidates a pending page and its error", async () => {
  const p = pendingPager();
  p.pager.select("ws", "s2c");
  p.calls[0].resolve({ items: [message(2, "s2c"), message(4, "s2c")], total: 4 });
  await flush();
  p.pager.loadMore();
  p.pager.loadMore();
  assert.equal(p.calls.length, 2);
  assert.deepEqual(p.calls[1].args, ["ws", 2, 50, "s2c"]);

  p.pager.refresh();
  assert.deepEqual(p.calls[2].args, ["ws", 0, 50, "s2c"]);
  p.calls[1].reject(new Error("old page failed"));
  await flush();
  assert.equal(p.state().error, null);
  assert.equal(p.state().loading, true);
  p.calls[2].resolve({ items: [message(2, "s2c")], total: 2 });
  await flush();
  p.pager.loadMore();
  assert.deepEqual(p.calls[3].args, ["ws", 1, 50, "s2c"]);
  p.calls[3].resolve({ items: [{ ...message(121, "meta"), type: "meta" }], total: 2 });
  await flush();
  assert.deepEqual(plain(p.state().items.map((item) => item.seq)), [2, 121]);
  p.pager.loadMore();
  assert.equal(p.calls.length, 4);
});

test("cancellation suppresses late results, and a current error can be refreshed", async () => {
  const p = pendingPager();
  p.pager.select("ws", "");
  p.pager.cancel();
  p.calls[0].resolve({ items: [message(1)], total: 1 });
  await flush();
  assert.deepEqual(plain(p.state().items), []);
  p.pager.select("sse", "c2s");
  p.calls[1].reject(new Error("connection lost"));
  await flush();
  assert.equal(p.state().error, "connection lost");
  assert.equal(p.state().loading, false);
  p.pager.refresh();
  assert.deepEqual(p.calls[2].args, ["sse", 0, 50, "c2s"]);
  p.calls[2].resolve({ items: [], total: 0 });
  await flush();
  assert.equal(p.state().error, null);
  assert.equal(p.state().loading, false);
  assert.equal(p.state().total, 0);
});

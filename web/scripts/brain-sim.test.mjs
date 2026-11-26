import { test } from "node:test";
import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import path from "node:path";
import { fileURLToPath } from "node:url";
import ts from "typescript";

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
function load() {
  const output = ts.transpileModule(fs.readFileSync(path.join(web, "src/lib/brain-sim.ts"), "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const exports = {};
  vm.runInNewContext(output, { exports, Math, Map, Set });
  return exports;
}
const { Brain, CORE_ID, project, fitDistance, placeTool, neuronKind } = load();

const nodes = [
  { id: "1", type: "goal", state: "open", priority: 8, label: "Get domain admin" },
  { id: "2", type: "intent", state: "running", priority: 6, label: "Enumerate SMB" },
  { id: "3", type: "intent", state: "done", priority: 5, label: "Check web login" },
  { id: "4", type: "fact", state: "", priority: 3, label: "SMB signing off" },
  { id: "5", type: "finding", state: "confirmed", priority: 9, label: "Weak creds" },
];
const edges = [
  { src: "1", dst: "2", rel: "spawns" },
  { src: "1", dst: "3", rel: "spawns" },
  { src: "2", dst: "4", rel: "yields" },
  { src: "4", dst: "5", rel: "proves" },
  { src: "3", dst: "4", rel: "covers" },
];
const act = (seq, kind, extra = {}) => ({
  seq,
  worker: "work#1",
  kind,
  summary: `${kind} ${seq}`,
  ts: "2026-11-26T03:00:00Z",
  ...extra,
});

test("graph nodes become neurons hanging off the core by structural parent", () => {
  const b = new Brain();
  const { added } = b.syncGraph(nodes, edges, false);
  assert.equal(added.length, 5);
  assert.equal(b.structuralCount, 6); // + core
  assert.equal(b.neurons.get("1").parent.id, CORE_ID);
  assert.equal(b.neurons.get("2").parent.id, "1");
  assert.equal(b.neurons.get("4").parent.id, "2"); // first structural parent by BFS order
  assert.equal(b.neurons.get("5").depth, 4);
  assert.equal(b.synapses.length, 5);
  assert.equal(b.signals.length, 0, "no animation on the initial load");
});

test("origin facts map to the begin kind", () => {
  assert.equal(neuronKind("fact", "origin"), "begin");
  assert.equal(neuronKind("intent", "running"), "intent");
  assert.equal(neuronKind("something-new", ""), "fact");
});

test("a new node arriving later sprouts with a growth signal from the core", () => {
  const b = new Brain();
  b.syncGraph(nodes, edges, false);
  const more = [...nodes, { id: "6", type: "fact", state: "", priority: 2, label: "Port 445 open" }];
  const { added } = b.syncGraph(more, [...edges, { src: "2", dst: "6", rel: "yields" }], true);
  assert.equal(added.length, 1);
  assert.equal(added[0].growth, 0);
  assert.equal(b.signals.length, 1);
  assert.equal(b.signals[0].path.map((n) => n.id).join(","), [CORE_ID, "1", "2", "6"].join(","));
});

test("removed graph nodes drop out and their tool neurons move to the core", () => {
  const b = new Brain();
  b.syncGraph(nodes, edges, false);
  b.ingest(act(1, "tool_use", { intent_id: "3", tool: "curl", tool_use_id: "t1" }), false);
  b.syncGraph(
    nodes.filter((n) => n.id !== "3"),
    edges,
    false,
  );
  assert.equal(b.neurons.has("3"), false);
  assert.equal(b.neurons.get("tool:t1").parent.id, CORE_ID);
});

test("tool calls sprout tool neurons on their intent and results pair back by id", () => {
  const b = new Brain();
  b.syncGraph(nodes, edges, false);
  const tool = b.ingest(act(10, "tool_use", { intent_id: "2", tool: "nmap", tool_use_id: "u1" }));
  assert.equal(tool.kind, "tool");
  assert.equal(tool.label, "nmap");
  assert.equal(tool.parent.id, "2");
  assert.equal(b.toolCount, 1);
  assert.equal(
    b.signals
      .at(-1)
      .path.map((n) => n.id)
      .join(","),
    [CORE_ID, "1", "2", "tool:u1"].join(","),
  );
  const back = b.ingest(act(11, "tool_result", { intent_id: "2", tool_use_id: "u1", is_error: true }));
  assert.equal(back, tool);
  assert.equal(tool.error, true);
  assert.equal(b.signals.at(-1).tone, "error");
  assert.equal(
    b.signals
      .at(-1)
      .path.map((n) => n.id)
      .join(","),
    ["tool:u1", "2"].join(","),
  );
});

test("events without a known intent fire the core", () => {
  const b = new Brain();
  b.syncGraph(nodes, edges, false);
  const n = b.ingest(act(20, "thinking", { worker: "planner" }));
  assert.equal(n.id, CORE_ID);
  assert.ok(b.core.energy > 0);
  assert.equal(b.ingest(act(21, "text", { intent_id: "999" })).id, CORE_ID);
});

test("tool neurons are capped, oldest first", () => {
  const b = new Brain({ maxTools: 3 });
  b.syncGraph(nodes, edges, false);
  for (let i = 0; i < 5; i++) b.ingest(act(i, "tool_use", { intent_id: "2", tool: "x", tool_use_id: `c${i}` }), false);
  assert.equal(b.toolCount, 3);
  assert.equal(b.toolCalls, 5);
  assert.equal(b.neurons.has("tool:c0"), false);
  assert.equal(b.neurons.has("tool:c4"), true);
});

test("signals travel their path and heat the links they cross", () => {
  const b = new Brain();
  b.syncGraph(nodes, edges, false);
  b.settle();
  b.ingest(act(30, "tool_use", { intent_id: "2", tool: "nmap", tool_use_id: "s1" }));
  assert.equal(b.signals.length, 1);
  for (let i = 0; i < 120; i++) b.step(1 / 60);
  assert.equal(b.signals.length, 0, "the signal reached the tool neuron");
  assert.ok(b.neurons.get("2").linkHeat > 0);
});

test("layout settles to a finite, stable shape that is the same every time", () => {
  const run = () => {
    const b = new Brain();
    b.syncGraph(nodes, edges, false);
    b.settle(300);
    return JSON.stringify(
      [...b.neurons.values()].map((n) => [n.id, Math.round(n.x), Math.round(n.y), Math.round(n.z)]),
    );
  };
  const a = run();
  assert.equal(a, run());
  for (const [, x, y, z] of JSON.parse(a)) assert.ok(Number.isFinite(x) && Number.isFinite(y) && Number.isFinite(z));
  const b = new Brain();
  b.syncGraph(nodes, edges, false);
  b.settle(300);
  assert.ok(b.extent() < 1000);
  // Deeper neurons sit farther from the core.
  const r = (id) => Math.hypot(b.neurons.get(id).x, b.neurons.get(id).y, b.neurons.get(id).z);
  assert.ok(r("5") > r("1"));
});

test("tool tufts grow outward from their parent", () => {
  const b = new Brain();
  b.syncGraph(nodes, edges, false);
  b.settle();
  const tool = b.ingest(act(40, "tool_use", { intent_id: "2", tool: "nmap", tool_use_id: "p1" }), false);
  const [x, y, z] = placeTool(tool);
  const parent = b.neurons.get("2");
  assert.ok(Math.hypot(x, y, z) > Math.hypot(parent.x, parent.y, parent.z));
});

test("projection centres the target and shrinks with distance", () => {
  const cam = { yaw: 0, pitch: 0, dist: 500, tx: 0, ty: 0, tz: 0 };
  const c = project(cam, 0, 0, 0, 800, 600);
  assert.equal(c.sx, 400);
  assert.equal(c.sy, 300);
  const near = project(cam, 0, 0, -200, 800, 600);
  const far = project(cam, 0, 0, 200, 800, 600);
  assert.ok(near.scale > far.scale);
  assert.equal(project(cam, 0, 0, -600, 800, 600).scale, 0, "behind the camera");
  assert.ok(fitDistance(400, 800, 600) > fitDistance(100, 800, 600));
});

import ts from "typescript";

import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import vm from "node:vm";

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
function load() {
  const output = ts.transpileModule(fs.readFileSync(path.join(web, "src/lib/attack-path.ts"), "utf8"), {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText;
  const exports = {};
  vm.runInNewContext(output, { exports, Math, Map, Set });
  return exports;
}
const { buildAttackGraph, layoutAttackGraph, chainTo, findingRisk, ENTRY_ID } = load();

const ids = (list) => list.map((n) => n.id).join(",");

// company → root → sub → service → endpoint, plus a second host.
const assets = [
  { key: "c:1", kind: "company", label: "Lab", in_scope: true },
  { key: "a:10", kind: "root_domain", label: "lab.test", in_scope: true },
  { key: "a:11", kind: "subdomain", label: "app.lab.test", in_scope: true, tested: true },
  { key: "a:12", kind: "service", label: "https://app.lab.test", in_scope: true },
  { key: "a:13", kind: "endpoint", label: "POST /login", in_scope: true },
  { key: "a:20", kind: "ip", label: "10.0.0.5", in_scope: true },
];
const contains = [
  { src: "a:10", dst: "c:1" },
  { src: "a:11", dst: "a:10" },
  { src: "a:12", dst: "a:11" },
  { src: "a:13", dst: "a:12" },
];
const f = (id, severity, status, assetIds, extra = {}) => ({
  id,
  name: `F${id}`,
  severity,
  status,
  assets: assetIds.map((a) => ({ id: String(a), type: "endpoint", label: `asset ${a}` })),
  ...extra,
});

test("risk weighs severity by triage status and verification", () => {
  assert.equal(findingRisk({ severity: "critical", status: "confirmed" }), 10);
  assert.equal(findingRisk({ severity: "high", status: "pending" }), 4.2);
  assert.equal(findingRisk({ severity: "high", status: "confirmed", verification_status: "verified" }), 7.5);
  assert.equal(findingRisk({ severity: "critical", status: "fixed" }), 0);
  assert.equal(findingRisk({ severity: "critical", status: "confirmed", verification_status: "false_positive" }), 0);
  assert.equal(findingRisk({ severity: "odd", status: "something-new" }), 0.7, "unknown values count as low, pending");
});

test("assets hang off the entry point by containment and findings off their deepest asset", () => {
  const g = buildAttackGraph({ assets, contains, findings: [f("100", "high", "confirmed", [11, 13])] });
  assert.equal(g.nodes.get("c:1").parent.id, ENTRY_ID);
  assert.equal(g.nodes.get("a:20").parent.id, ENTRY_ID);
  assert.equal(g.nodes.get("a:13").depth, 5);
  const fin = g.nodes.get("f:100");
  assert.equal(fin.asset.id, "a:13", "the endpoint is deeper than the subdomain");
  assert.equal(fin.depth, 6);
  assert.equal(g.assets, 6);
  assert.equal(g.chains.length, 1);
  assert.equal(ids(g.chains[0].hops), "entry,c:1,a:10,a:11,a:12,a:13,f:100");
  assert.equal(g.nodes.get("a:10").subtreeRisk, 6);
  assert.equal(g.nodes.get("a:10").worst, "high");
});

test("a finding on an asset outside the coverage graph gets its own out-of-scope asset", () => {
  const g = buildAttackGraph({ assets, contains, findings: [f("101", "medium", "confirmed", [99])] });
  const a = g.nodes.get("a:99");
  assert.equal(a.inScope, false);
  assert.equal(a.parent.id, ENTRY_ID);
  assert.equal(g.nodes.get("f:101").asset, a);
  const bare = buildAttackGraph({ assets, contains, findings: [f("102", "low", "pending", [])] });
  assert.equal(bare.nodes.get("f:102").asset.id, ENTRY_ID);
});

test("exploration lineage chains findings and the riskiest chain wins", () => {
  const findings = [
    f("1", "medium", "confirmed", [12]), // exposed admin panel
    f("2", "high", "confirmed", [13]), // weak creds found while working from finding 1
    f("3", "critical", "pending", [20]), // reached from finding 2
    f("4", "high", "confirmed", [20]), // unrelated
  ];
  const graphNodes = [
    { id: "1", type: "finding" },
    { id: "2", type: "finding" },
    { id: "3", type: "finding" },
    { id: "4", type: "finding" },
    { id: "50", type: "intent" },
    { id: "51", type: "fact" },
    { id: "52", type: "intent" },
    { id: "53", type: "intent" },
  ];
  const graphEdges = [
    { src: "1", dst: "50", rel: "derived_from" },
    { src: "50", dst: "51", rel: "yields" },
    { src: "51", dst: "52", rel: "derived_from" },
    { src: "52", dst: "2", rel: "yields" },
    { src: "2", dst: "53", rel: "derived_from" },
    { src: "53", dst: "3", rel: "yields" },
    { src: "60", dst: "4", rel: "covers" }, // digests don't make chains
  ];
  const g = buildAttackGraph({ assets, contains, findings, graphNodes, graphEdges });
  assert.equal(ids(g.nodes.get("f:2").enabledBy), "f:1");
  assert.equal(ids(g.nodes.get("f:3").enabledBy), "f:2", "only the nearest finding upstream");
  const top = g.chains[0];
  assert.equal(ids(top.findings), "f:1,f:2,f:3");
  assert.equal(top.score, 3 + 6 + 7);
  assert.equal(top.worst, "critical");
  assert.equal(ids(top.hops), "entry,c:1,a:10,a:11,a:12,f:1,f:2,f:3");
  assert.equal(ids(g.chains[1].findings), "f:4", "the next chain skips findings already covered");
  assert.ok(g.nodes.get("f:3").depth > g.nodes.get("f:2").depth);
  assert.equal(ids(chainTo(g, g.nodes.get("f:2")).findings), "f:1,f:2");
});

test("closed findings stay visible but break chains and carry no risk", () => {
  const findings = [f("1", "critical", "fixed", [12]), f("2", "high", "confirmed", [13])];
  const graphNodes = [
    { id: "1", type: "finding" },
    { id: "2", type: "finding" },
  ];
  const graphEdges = [{ src: "1", dst: "2", rel: "derived_from" }];
  const g = buildAttackGraph({ assets, contains, findings, graphNodes, graphEdges });
  assert.equal(g.active, 1);
  assert.equal(g.closed, 1);
  assert.equal(g.chains.length, 1);
  assert.equal(ids(g.chains[0].findings), "f:2");
  assert.equal(g.nodes.get("a:12").subtreeRisk, 6, "the fixed finding adds nothing");
});

test("loops in containment or lineage don't hang or break the graph", () => {
  const loopy = buildAttackGraph({
    assets: [
      { key: "a:1", kind: "subdomain", label: "x" },
      { key: "a:2", kind: "service", label: "y" },
    ],
    contains: [
      { src: "a:1", dst: "a:2" },
      { src: "a:2", dst: "a:1" },
    ],
    findings: [f("1", "high", "confirmed", [1]), f("2", "high", "confirmed", [2])],
    graphNodes: [
      { id: "1", type: "finding" },
      { id: "2", type: "finding" },
    ],
    graphEdges: [
      { src: "1", dst: "2", rel: "derived_from" },
      { src: "2", dst: "1", rel: "derived_from" },
    ],
  });
  for (const n of loopy.nodes.values()) {
    let hops = 0;
    for (let p = n.parent; p; p = p.parent) assert.ok(hops++ < 10, `${n.id} has a finite parent chain`);
  }
  assert.ok(loopy.chains.length >= 1);
  assert.ok(Number.isFinite(loopy.chains[0].score));
});

test("large scopes keep every asset on the way to a finding", () => {
  const many = [];
  const links = [];
  for (let i = 0; i < 50; i++) {
    many.push({ key: `a:${i}`, kind: i ? "subdomain" : "root_domain", label: `h${i}` });
    if (i) links.push({ src: `a:${i}`, dst: "a:0" });
  }
  many.push({ key: "a:500", kind: "service", label: "deep" });
  links.push({ src: "a:500", dst: "a:49" });
  const g = buildAttackGraph(
    { assets: many, contains: links, findings: [f("9", "high", "confirmed", [500])] },
    { maxAssets: 10 },
  );
  assert.ok(g.nodes.has("a:500") && g.nodes.has("a:49") && g.nodes.has("a:0"));
  assert.equal(g.nodes.get("f:9").asset.id, "a:500");
  assert.equal(g.assets, 10);
  assert.equal(g.droppedAssets, 41);
});

test("layout runs along the path axis and is the same every time", () => {
  const run = () => {
    const g = buildAttackGraph({
      assets,
      contains,
      findings: [f("1", "high", "confirmed", [13]), f("2", "low", "pending", [20])],
    });
    layoutAttackGraph(g);
    return g;
  };
  const a = run();
  const b = run();
  const pos = (g) => JSON.stringify([...g.nodes.values()].map((n) => [n.id, n.x, n.y, n.z]));
  assert.equal(pos(a), pos(b));
  for (const n of a.nodes.values()) assert.ok(Number.isFinite(n.x + n.y + n.z), n.id);
  const e = a.nodes.get(ENTRY_ID);
  assert.equal(e.y, 0);
  assert.equal(e.z, 0);
  assert.ok(a.nodes.get("a:13").x > a.nodes.get("a:10").x, "deeper layers sit further along");
  assert.ok(a.nodes.get("f:1").x > a.nodes.get("a:13").x);
});

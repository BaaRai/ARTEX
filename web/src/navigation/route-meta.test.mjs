import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

import { findActiveUrl, normalizePath, routeMeta } from "./route-meta.ts";

// sidebar-items.ts imports t() from "@/i18n", which node cannot resolve, so the tests use
// this copy of its structure (English titles, same urls and groups).
const group = (id, label, items) => ({ id, label, items: items.map(([url, title]) => ({ id: url, url, title })) });
const sidebarItems = [
  group(1, "Overview", [["/dashboard", "Dashboard"], ["/chat", "Chat"]]),
  group(2, "Testing", [
    ["/function/tasks", "Tasks"],
    ["/function/findings", "Findings"],
    ["/function/assets", "Assets"],
    ["/function/traffic", "Traffic"],
    ["/function/commands", "Tool runs"],
    ["/function/llm-records", "LLM calls"],
  ]),
  group(3, "Guardrails", [
    ["/system/intercept/approvals", "Approvals"],
    ["/system/intercept", "Command rules"],
    ["/system/intercept/assets", "Target blocklist"],
  ]),
  group(4, "Data", [["/function/sync", "Asset sync"], ["/function/workspace", "Workspace"]]),
  group(5, "AI configuration", [
    ["/system/llm", "Models"],
    ["/system/agents", "Agents"],
    ["/system/mcp", "MCP servers"],
    ["/system/skills", "Skills"],
    ["/system/tools", "Tools"],
  ]),
  group(6, "Admin", [["/system/notify", "Notifications"], ["/system/logs", "Logs"], ["/system/settings", "Settings"]]),
];

test("normalizePath strips trailing slashes but keeps the root", () => {
  assert.equal(normalizePath("/function/tasks/"), "/function/tasks");
  assert.equal(normalizePath("/"), "/");
  assert.equal(normalizePath(""), "/");
});

test("findActiveUrl picks the longest prefix", () => {
  assert.equal(findActiveUrl(sidebarItems, "/system/intercept/assets"), "/system/intercept/assets");
  assert.equal(findActiveUrl(sidebarItems, "/system/intercept"), "/system/intercept");
  assert.equal(findActiveUrl(sidebarItems, "/function/tasks/detail"), "/function/tasks");
  assert.equal(findActiveUrl(sidebarItems, "/nowhere"), undefined);
});

test("the fixture matches the sidebar definition", () => {
  const src = readFileSync(new URL("./sidebar/sidebar-items.ts", import.meta.url), "utf8");
  for (const g of sidebarItems) {
    assert.ok(src.includes(`label: t("${g.label}")`), g.label);
    for (const item of g.items) assert.ok(src.includes(`title: t("${item.title}"), url: "${item.url}"`), item.url);
  }
});

test("nav groups are named by job", () => {
  assert.deepEqual(
    sidebarItems.map((g) => g.label),
    ["Overview", "Testing", "Guardrails", "Data", "AI configuration", "Admin"],
  );
});

test("every route title matches its nav label", () => {
  for (const group of sidebarItems) {
    for (const item of group.items) {
      const meta = routeMeta(sidebarItems, `${item.url}/`);
      assert.equal(meta.title, item.title, item.url);
      assert.equal(meta.group, group.label, item.url);
      assert.deepEqual(meta.breadcrumbs, [], item.url);
    }
  }
});

test("renamed pages", () => {
  assert.equal(routeMeta(sidebarItems, "/system/llm/").title, "Models");
  assert.equal(routeMeta(sidebarItems, "/system/intercept/").title, "Command rules");
  assert.equal(routeMeta(sidebarItems, "/system/intercept/assets/").title, "Target blocklist");
  assert.equal(routeMeta(sidebarItems, "/function/commands/").title, "Tool runs");
  assert.equal(routeMeta(sidebarItems, "/function/llm-records/").title, "LLM calls");
});

test("detail pages get a title and a breadcrumb to their list", () => {
  const task = routeMeta(sidebarItems, "/function/tasks/detail/");
  assert.equal(task.title, "Task");
  assert.equal(task.group, "Testing");
  assert.deepEqual(task.breadcrumbs, [{ title: "Tasks", url: "/function/tasks" }]);

  assert.equal(routeMeta(sidebarItems, "/function/findings/detail").title, "Finding");
  assert.equal(routeMeta(sidebarItems, "/system/agents/detail").title, "Agent");
});

test("unknown routes fall back to the app name", () => {
  const meta = routeMeta(sidebarItems, "/login/");
  assert.equal(meta.title, undefined);
  assert.equal(meta.documentTitle, "ARTEX");
});

test("document title puts the page first", () => {
  assert.equal(routeMeta(sidebarItems, "/function/findings/").documentTitle, "Findings · ARTEX");
  assert.equal(routeMeta(sidebarItems, "/function/tasks/detail").documentTitle, "Task · Tasks · ARTEX");
});

import assert from "node:assert/strict";
import test from "node:test";

import { findActiveUrl, normalizePath, routeMeta } from "./route-meta.ts";
import { sidebarItems } from "./sidebar/sidebar-items.ts";

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

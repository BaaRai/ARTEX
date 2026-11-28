import { findingReturnHref, NAVIGATION_EVENT, readQuery, updateQuery } from "./navigation.ts";
import assert from "node:assert/strict";
import test from "node:test";

function browser(href) {
  const entries = [href];
  let index = 0;
  let notifications = 0;
  globalThis.window = {
    location: { href },
    history: {
      pushState(_state, _title, url) {
        entries.splice(++index, entries.length, String(url));
        window.location.href = String(url);
      },
      replaceState(_state, _title, url) {
        entries[index] = window.location.href = String(url);
      },
    },
    dispatchEvent(event) {
      assert.equal(event.type, NAVIGATION_EVENT);
      notifications++;
    },
  };
  return {
    entries,
    go(delta) {
      index += delta;
      window.location.href = entries[index];
    },
    get notifications() {
      return notifications;
    },
    get search() {
      return new URL(window.location.href).search;
    },
  };
}

test("task tabs survive back and forward without erasing id, focus or hash", () => {
  const b = browser("https://artex.test/function/tasks/detail?id=7&approval=9#event");
  updateQuery({ tab: "findings" });
  updateQuery({ tab: "report" });
  b.go(-1);
  assert.equal(readQuery(b.search, "tab", "sessions"), "findings");
  b.go(-1);
  assert.equal(readQuery(b.search, "tab", "sessions"), "sessions");
  b.go(1);
  assert.equal(readQuery(b.search, "tab", "sessions"), "findings");
  assert.equal(new URL(window.location.href).searchParams.get("id"), "7");
  assert.equal(new URL(window.location.href).searchParams.get("approval"), "9");
  assert.equal(new URL(window.location.href).hash, "#event");
});

test("repeated selection and reads do not create history entries", () => {
  const b = browser("https://artex.test/function/tasks?tab=archived");
  updateQuery({ tab: "archived" });
  readQuery(b.search, "tab", "current");
  assert.equal(b.entries.length, 1);
  assert.equal(b.notifications, 0);
});

test("a new selection after back drops only the forward branch", () => {
  const b = browser("https://artex.test/chat?c=1");
  updateQuery({ c: "2" });
  updateQuery({ c: "3" });
  b.go(-1);
  updateQuery({ c: "4" });
  assert.equal(b.entries.length, 3);
  assert.equal(readQuery(b.search, "c", ""), "4");
});

test("chat switches remove focus only from the new entry and preserve draft", () => {
  const b = browser("https://artex.test/chat?c=1&approval=99");
  updateQuery({ c: "2", approval: null });
  updateQuery({ c: "", approval: null });
  assert.equal(new URL(window.location.href).searchParams.get("c"), "");
  b.go(-1);
  assert.equal(readQuery(b.search, "c", ""), "2");
  b.go(-1);
  assert.equal(new URL(window.location.href).searchParams.get("approval"), "99");
});

test("restoring a preference replaces only the initial entry", () => {
  const b = browser("https://artex.test/function/findings");
  updateQuery({ view: "grouped" }, "replace");
  assert.equal(b.entries.length, 1);
  updateQuery({ view: "asset" });
  b.go(-1);
  assert.equal(readQuery(b.search, "view", "flat"), "grouped");
});

test("missing and invalid tabs fall back without a history write", () => {
  assert.equal(readQuery("?tab=unknown", "tab", "sessions", ["sessions", "findings"]), "sessions");
  assert.equal(readQuery("?id=1", "tab", "sessions"), "sessions");
});

test("finding return destinations are local and preserve the task context", () => {
  assert.equal(findingReturnHref(null), "/function/findings");
  assert.equal(findingReturnHref("7"), "/function/tasks/detail?id=7&tab=findings");
  assert.equal(findingReturnHref("x&tab=report"), "/function/tasks/detail?id=x%26tab%3Dreport&tab=findings");
});

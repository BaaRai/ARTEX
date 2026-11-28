import { commandsPageReducer, initialCommandsPageState } from "./commands-page.reducer.ts";
import assert from "node:assert/strict";
import test from "node:test";

const reduce = (actions, state = initialCommandsPageState) => actions.reduce(commandsPageReducer, state);

test("debounced query trims and resets to the first page", () => {
  const s = reduce([
    { type: "pagination/pageChanged", page: 3 },
    { type: "queryChanged", query: "  nmap " },
    { type: "queryDebounced" },
  ]);
  assert.equal(s.filters.debouncedQuery, "nmap");
  assert.equal(s.pagination.page, 0);
});

test("debounce with an unchanged trimmed query keeps the page", () => {
  const s = reduce([
    { type: "queryChanged", query: "nmap" },
    { type: "queryDebounced" },
    { type: "pagination/pageChanged", page: 2 },
    { type: "queryChanged", query: "nmap " },
    { type: "queryDebounced" },
  ]);
  assert.equal(s.pagination.page, 2);
});

test("task filter keeps digits only and resets the page", () => {
  const s = reduce([
    { type: "pagination/pageChanged", page: 4 },
    { type: "taskFilterChanged", task: "1a2b" },
  ]);
  assert.equal(s.filters.task, "12");
  assert.equal(s.pagination.page, 0);
});

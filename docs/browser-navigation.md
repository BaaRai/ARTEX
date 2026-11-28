# Browser navigation

Page navigation is represented in query parameters. User actions push a history
entry; restoring an initial saved preference replaces the current entry. Rendering,
polling and back/forward handling never mirror stale component state into the URL.
The native history API integrates with Next's router and preserves other query
parameters and the hash. A stable server snapshot supports the static export.

| View | Query parameter |
| --- | --- |
| Task detail, finding detail, task archive, assets, models, rules, tools, notifications | `tab` |
| Findings grouping | `view` |
| Conversation, including an explicit empty draft | `c` |
| Workspace directory | `path` |
| Traffic / tool execution / LLM record inspector | `exchange` / `command` / `record` |
| Return from a finding opened inside a task | `from_task` |

`context_task` retains its existing meaning for inherited finding access. The new
return parameter does not change which finding data the API supplies.

Inspector payloads remain in memory (at most 100 visited rows). Back/forward can
restore visited rows across client navigations; after a full reload, the selected
record must be in the loaded list. They are not arbitrary-record permalink APIs.
Payloads, credentials, and form contents are not written into the URL or browser
storage. Creation/edit dialogs and filters remain ordinary form state.

## Regression checks

Run `npm run test:navigation` in `web/` for the history logic. The test is included
in CI and the repository verification script.

For the browser checks, use a Python environment with Playwright and its Chromium
browser installed, then start the mock frontend:

```sh
cd web
NEXT_PUBLIC_MOCK=1 npm run dev -- --hostname 127.0.0.1 --port 18791
```

In another terminal:

```sh
ARTEX_TEST_URL=http://127.0.0.1:18791 python3 web/scripts/navigation-browser.py
```

The browser script uses the existing demo fixtures, emits a JSON result and a
screenshot alongside the script, and exits unsuccessfully if a case fails. It
covers back, forward, refresh, repeated tab selection, invalid tabs, task → finding
→ contextual return, chat selection/draft, record inspectors, and workspace
directories. `ARTEX_TEST_FILTER` selects cases whose names contain the given text.
Use this script against the mock frontend, not a production dataset.

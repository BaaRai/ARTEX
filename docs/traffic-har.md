# Traffic HAR 1.2 Import and Export

ARTEX supports importing and exporting captured HTTP traffic using the **HAR 1.2** JSON format so operators can bring captures from browser DevTools or external proxies into the Traffic store and export filtered subsets out of ARTEX.

Both routes require the standard administrator JWT (`Authorization: Bearer <token>`) like the rest of `/api/traffic`, and return `503` when the traffic store is unavailable.

---

## Import: `POST /api/traffic/import/har`

### Request format

Send a raw HAR 1.2 JSON document directly as the HTTP request body (`Content-Type: application/json`), capped at the standard API JSON body limit (**32 MiB**; oversized bodies return `413 Request Entity Too Large`).

```bash
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  --data-binary @capture.har \
  http://127.0.0.1:8787/api/traffic/import/har
```

The Traffic page also provides an **Import HAR** file picker that reads a `.har` / `.json` file, posts it to this endpoint, displays loading/error state and a count summary, and refreshes the table.

### Validation & error handling

The request is rejected with `400 Bad Request` naming the reason when:

- the body is empty or malformed JSON (including trailing content after the root object),
- the `log` object is missing,
- `log.version` is not `"1.2"`,
- `log.entries` is missing or `null`,
- `log.entries` exceeds the per-import entry cap (`2000` entries).

Individual malformed entries (invalid `startedDateTime`, non-HTTP(S) URL, invalid HTTP method, header names or values containing CR/LF/NUL, out-of-range status, invalid base64 or unsupported body encoding) do **not** fail the whole import: they are counted in `failed` and reported in `errors` (up to 50 error details) while valid entries continue importing. HTTP/2 pseudo-headers starting with `:` (such as `:authority` or `:method` from browser DevTools) are skipped cleanly.

### Response

```json
{
  "imported": 12,
  "skipped": 3,
  "failed": 1,
  "truncated": false,
  "errors": [
    { "index": 4, "url": "notaurl", "reason": "invalid request url \"notaurl\"" }
  ]
}
```

### Deterministic IDs & idempotence

Each imported entry is assigned a deterministic ID:

```text
har-<hex(sha256(ts_unix_seconds + NUL + METHOD + NUL + canonical_url + NUL + status + NUL + sha256(req_body) + NUL + sha256(resp_body)))>
```

- `ts_unix_seconds` is `startedDateTime` parsed as RFC 3339 and truncated to UTC Unix seconds (matching `exchanges.ts`).
- Existing rows are never overwritten (`INSERT OR IGNORE`): re-importing the same HAR (or an exported HAR from ARTEX) skips existing IDs and increments `skipped`. Duplicate entries inside the same HAR file are likewise deduplicated into `skipped`.

### Storage, bodies, and base64

- `host` is derived from `URL.Hostname()` and `url_template` from `db.TemplatePath(URL.EscapedPath())`; `path` stays empty.
- `response.content.text` and `request.postData.text` are stored as raw UTF-8 bytes when `encoding` is empty, and decoded via standard base64 when `encoding` is `"base64"`.
- `req_len` and `resp_len` record the actual decoded byte lengths.
- `content_type` comes from `response.content.mimeType` (falling back to the response `Content-Type` header).
- Bodies larger than the 256 KiB inline threshold spill to the content-addressed blob store (`_blobs/sha256/...`) with `blob_refs` rows, and searchable text bodies are added to the `ex_fts` full-text index — identical to proxy capture and `ImportArchive`.

### Capture rewrite rules are NOT applied

HAR import writes directly to the traffic store without passing through the MITM capture proxy. **Capture rewrite rules are never applied to imported exchanges**, no `exchange_rewrites` rows are created, and imported rows always have `rewritten: false`.

### Import limits

| Limit | Default | Behavior when exceeded |
|---|---|---|
| Raw HTTP request body | 32 MiB | `413 Request Entity Too Large` |
| Entries per HAR | 2,000 | `400 Bad Request` |
| Total decoded request + response body bytes | 32 MiB | Stops importing remaining entries, sets `"truncated": true`, counts remaining entries in `skipped`, and appends an explanatory item in `errors`. |

---

## Export: `GET /api/traffic/export/har`

### Request & filters

Returns an `application/json` HAR 1.2 document (`Content-Disposition: attachment; filename="artex-traffic.har"`) ordered deterministically by `(ts ASC, id ASC)`.

It reuses the same filter parser and SQL predicate builder as `GET /api/traffic`, supporting:

- `host`, `method`, `path`, `status`, `q`, `body`, `rewritten` (`all` | `rewritten` | `original` | `true` | `false`), `resp_min`, `resp_max`.

### Mapping & fidelity

- `log.version` is `"1.2"` and `log.creator` is `{"name": "ARTEX", "version": "<build-version>"}`.
- `startedDateTime` is formatted from `exchanges.ts` as UTC RFC 3339.
- Request and response headers are parsed from the stored raw heads (`req_head` / `resp_head`). ARTEX does **not** invent `time`, `serverIPAddress`, or headers that are not present in the store.
- Bodies are read in full from inline columns or spilled blobs: valid NUL-free UTF-8 bodies are emitted as plain `text`, and binary / non-UTF-8 bodies are emitted with `encoding: "base64"`.

### Export limits & truncation reporting

| Limit | Default | `truncatedReason` |
|---|---|---|
| Exported entries | 1,000 | `max_entries` |
| Total raw request + response body bytes | 32 MiB | `max_bytes` |

Truncation is never silent. Every export reports it in both HTTP response headers and HAR 1.2 custom (`_`-prefixed) `log` fields:

- Headers:
  - `X-ARTEX-HAR-Truncated: true|false`
  - `X-ARTEX-HAR-Truncated-Reason: max_entries|max_bytes` (when truncated)
  - `X-ARTEX-HAR-Exported: <count>`
  - `X-ARTEX-HAR-Matched: <total matching rows>`
- JSON (`log` object, omitted when not truncated):
  - `_truncated: true`
  - `_truncatedReason: "max_entries" | "max_bytes"`
  - `_totalMatched: <total matching rows>`

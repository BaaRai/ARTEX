# Capture-proxy rewrite rules

A rewrite rule matches a request or a response that goes through the capture proxy and describes
how to change it: the method, the path and query, headers, the body, the status code.

**Status: rules are stored, validated and compiled. The proxy does not apply them yet.** Creating
or enabling a rule changes nothing in what the proxy forwards or records. Applying the rules, the
per-exchange trace, the archive format and the web UI come in a later change.

Rules are a convenience for the operator, not a security control: a rule that fails leaves the
message unchanged (fail open).

## Rule

| Field | Meaning |
|---|---|
| `name` | Required. |
| `enabled` | A rule is created disabled, whatever the request says. Enable it with the toggle or an update. |
| `phase` | `request` or `response`. |
| `priority` | Rules run by priority ascending, then id ascending: the highest priority runs last and wins. Each rule sees the output of the previous one. |
| `matchers` | Object, see below. |
| `actions` | Array, at least one, see below. |
| `note` | Free text. |

### Matchers

All matchers of a rule must hold. An absent matcher is true.

| Matcher | Phase | Meaning |
|---|---|---|
| `methods` | both | List of methods, case-insensitive. |
| `host` | both | Host name without port, case-insensitive: an exact name, or a suffix glob `*.example.com` (which does not match `example.com`). No other wildcard. |
| `url_regex` | both | Applied to the path and query. |
| `headers` | both | List of `{name, value_regex}` on the request headers. The name is case-insensitive; an empty `value_regex` tests presence. |
| `body_regex` | both | Applied to the request body. |
| `status` | response | List of codes (`404`) or classes (`4xx`), any of. |
| `resp_headers` | response | Like `headers`, on the response. |
| `resp_body_regex` | response | Applied to the response body. |

In the response phase, the request matchers see the request as it was sent to the target, that is,
after the request-phase rules.

Regular expressions use Go's `regexp` syntax (RE2).

### Actions

| Action | Phase | Fields | Effect |
|---|---|---|---|
| `set_method` | request | `value` | Replaces the method. `CONNECT` is refused. |
| `set_path_query` | request | `value` | Replaces the path and query. Must start with `/`. |
| `replace_url` | request | `regex`, `value` | Regex replacement on the path and query only. |
| `delete_header` | both | `name` | Removes every value of the header. |
| `set_header` | both | `name`, `value` | Replaces every value of the header with one. |
| `replace_header` | both | `name`, `regex`, `value` | Regex replacement on each value. |
| `set_body` | both | `value` | Replaces the body. |
| `replace_body` | both | `regex`, `value` | Regex replacement on the body. |
| `set_status` | response | `value` | Replaces the status code: 200 to 599, except 204 and 304. |

In a replacement, `value` is a template: `$1` or `${name}` insert a capture group.

Within a rule, actions run in a fixed order whatever the order they are listed in: method and URL,
`delete_header`, `set_header` and `replace_header` (in listed order), body, status. A rule is
atomic: if one action fails, none of the rule's actions applies.

A rule can never change the scheme, the host or the port. These header targets are refused: `Host`,
`Content-Length`, `Transfer-Encoding`, `Connection`, `Keep-Alive`, `TE`, `Trailer`, `Expect`,
`Upgrade` and every `Proxy-*` header. A header value cannot contain a line break or a NUL byte.

A rule that reads or rewrites a body is skipped whole, its header, URL and status actions included,
when that body is binary, larger than 1 MiB, compressed (`Content-Encoding` other than `identity`),
streamed, or absent by protocol (response to `HEAD`, status 1xx, 204 or 304).

### Sensitive actions

An action marked `"sensitive": true` keeps its `value` out of every API response. A read returns
the action without `value` and with `"value_set": true`.

On update, a sensitive action sent without `value` keeps the stored value, provided the stored
rule has exactly one sensitive action with the same type, header name (case-insensitive) and
`regex`, whatever its position: actions can be reordered without sending their values. When the
stored rule has no such action, or several (same type, same name, same `regex`), the update is
refused with HTTP 400 and the value must be sent again. An explicit `"value": ""` is a value.

A stored sensitive value is not written to a new destination. When an update keeps at least one
stored value, the `host` matcher must stay the same (compared case-insensitively); otherwise the
update is refused with HTTP 400 and the value must be sent again or cleared. The `url_regex`
matcher is not part of this comparison, an accepted limit: a kept value follows a rule whose
`url_regex` changes on the same host.

The flag covers the action's value only. Matchers are not masked. A value an action places in the
URL, marked sensitive or not, is sent to the target as part of the URL.

## Limits

| Limit | Value |
|---|---|
| Rules | 100, of which 50 enabled |
| Regular expression | 1 KiB |
| Value or replacement template | 1 MiB |

A request over a limit is refused with HTTP 400.

## API

All routes require the administrator token, like the rest of `/api`.

| Route | Effect |
|---|---|
| `GET /api/traffic/rewrite-rules` | `{"rules": [...]}` in execution order. |
| `POST /api/traffic/rewrite-rules` | Creates a rule (disabled). |
| `PUT /api/traffic/rewrite-rules/{id}` | Replaces a rule. |
| `DELETE /api/traffic/rewrite-rules/{id}` | Deletes a rule. |
| `POST /api/traffic/rewrite-rules/{id}/toggle` | Body `{"enabled": true}`. |

A rule the validation refuses, or a limit reached, answers 400; an unknown id answers 404.

```json
{
  "name": "staging token",
  "phase": "request",
  "priority": 10,
  "matchers": {"host": "*.staging.example.com", "methods": ["GET", "POST"]},
  "actions": [
    {"type": "set_header", "name": "Authorization", "value": "Bearer ...", "sensitive": true},
    {"type": "replace_url", "regex": "^/v1/", "value": "/v2/"}
  ],
  "note": ""
}
```

A rule in a response also carries `load_error` when it is enabled but left out of the running
snapshot (for example a rule saved by an older version that the current validation refuses), and
`last_error` (`class`, `at`, `count`) for its most recent run-time error. `last_error` is kept in
memory and lost at restart.

Rules are never exposed as an agent tool.

## Storage

Rules live in the PostgreSQL table `traffic_rewrite_rules` (migration
`0005_traffic_rewrite_rules.sql`). They are global configuration and are not part of a task
archive. The `matchers` and `actions` columns are encrypted at rest with the instance secret key,
like the other stored secrets: a database backup without `keys/secret.key` (or `ARTEX_SECRET_KEY`)
cannot restore them, and ARTEX refuses to start under a key that cannot open them.

The server compiles the enabled rules into an immutable snapshot at startup and after every rule
change, and hands it to the proxy without a restart.

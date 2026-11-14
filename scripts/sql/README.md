# Online task-query indexes

`20261113-task-query-indexes.sql` adds two PostgreSQL indexes for existing queries:

- `activity(tool_use_id) INCLUDE (is_error) WHERE kind = 'tool_result'` supports the tool-result join in `db/commands.go`, which does not constrain the result row's exploration ID.
- `llm_usage((COALESCE(task_id, '')), model)` covers the token counters used by the per-task model aggregation. The expression matches the actual predicate.

Run with `psql -X -v ON_ERROR_STOP=1 -d <database> -f scripts/sql/20261113-task-query-indexes.sql` outside a transaction. Use the deployment's normal PostgreSQL authentication. Both indexes are created concurrently, with a 5-second lock timeout and bounded maintenance resources. The final ordinary `VACUUM ANALYZE` updates visibility and statistics for index-only scans; it is not `VACUUM FULL`.

This is an explicit, one-time maintenance migration, not an application-startup schema change. It intentionally errors if an index of the same name already exists. On an already-updated database, verify the definitions and `pg_index.indisvalid` / `indisready` rather than rerunning it. If interrupted, inspect for an invalid index before retrying; concurrent index creation can leave one behind. A partial run may have created the first index before the second failed.

`20261113-task-query-indexes.rollback.sql` removes only these two indexes concurrently. Review their current definitions before applying a rollback. No application or database restart is required.

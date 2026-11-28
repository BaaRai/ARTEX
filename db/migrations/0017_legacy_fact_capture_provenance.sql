-- Older record_fact writes matched the shared proxy store without checking
-- ownership. They overwrote the declared source, so it cannot be recovered
-- from the fact. An inconsistent or missing label cannot authenticate a
-- retained capture ID either. Keep the independent activity link and evidence.
-- Pass an exploration ID after restoring an old archive, or NULL on upgrade.
CREATE OR REPLACE FUNCTION repair_legacy_fact_captures(target_exploration bigint)
RETURNS void LANGUAGE sql AS $$
    WITH repaired AS (
        UPDATE exploration_nodes
        SET payload = (payload - 'provenance_traffic_id') ||
                jsonb_build_object('source', 'inference', 'provenance', 'unverified',
                    'provenance_note', 'The original capture association did not establish task ownership.') ||
                CASE WHEN (payload->>'confidence') ~* '^[[:space:]]*observed[[:space:]]*$'
                     THEN '{"confidence":"inferred"}'::jsonb ELSE '{}'::jsonb END,
            content_version = content_version + 1
        WHERE (target_exploration IS NULL OR exploration_id = target_exploration)
          AND kind = 'fact' AND jsonb_typeof(payload) = 'object'
          AND (payload->>'provenance' = 'captured_traffic' OR payload ? 'provenance_traffic_id')
        RETURNING exploration_id, id
    ), retired AS (
        -- Cached digest bodies can repeat the old source or confidence. Retire
        -- only affected active digests and release their members for compaction.
        UPDATE exploration_nodes d SET state = 'superseded'
        FROM exploration_edges e JOIN repaired r
          ON r.exploration_id = e.exploration_id AND r.id = e.dst_id
        WHERE e.rel = 'covers' AND e.src_id = d.id AND e.exploration_id = d.exploration_id
          AND d.kind = 'digest' AND d.state = 'active'
        RETURNING d.exploration_id, d.id
    )
    DELETE FROM exploration_edges e USING retired d
    WHERE e.exploration_id = d.exploration_id AND e.src_id = d.id AND e.rel = 'covers';
$$;

SELECT repair_legacy_fact_captures(NULL);

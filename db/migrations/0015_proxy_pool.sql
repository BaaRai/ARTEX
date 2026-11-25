-- Restores the ARTEX proxy pool (dropped in 0.7.0): a list of egress proxies
-- with enable/disable, active selection, last-test timestamp, detected egress
-- IP and failure count. One entry may be active at a time (enforced in the
-- application layer when activating).
CREATE TABLE IF NOT EXISTS proxy_pool (
    id          BIGSERIAL PRIMARY KEY,
    address     TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    active      BOOLEAN NOT NULL DEFAULT false,
    last_tested TIMESTAMPTZ,
    egress_ip   TEXT,
    fail_count  INTEGER NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_proxy_pool_active   ON proxy_pool(active)   WHERE active;
CREATE INDEX IF NOT EXISTS idx_proxy_pool_enabled  ON proxy_pool(enabled)  WHERE enabled;

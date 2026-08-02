-- Vendor driver-pack fetch jobs (docs/DRIVERPACK_VENDOR_FETCH.md).
--
-- One row per requested download from a vendor catalog. The api process
-- drains the queue itself (claim via FOR UPDATE SKIP LOCKED on a tick):
-- packs are 0.5-2 GiB, so the work is durable-queued rather than done
-- inline in a request handler. heartbeat_at lets a restarted api reclaim
-- a job whose runner died mid-download.

BEGIN;

CREATE TABLE vendor_fetch_jobs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor          text NOT NULL,               -- 'Lenovo'
    model           text NOT NULL,               -- catalog model name
    mtypes          text[] NOT NULL DEFAULT '{}',-- machine types -> dmi-product-prefix rules
    os_family       text NOT NULL,               -- 'windows'
    os_version      text NOT NULL,               -- '11 22H2'
    url             text NOT NULL,
    expected_sha256 text NOT NULL,
    state           text NOT NULL DEFAULT 'queued'
                    CHECK (state IN ('queued','running','completed','failed')),
    error           text,
    pack_version_id uuid REFERENCES driver_pack_versions(id) ON DELETE SET NULL,
    size_bytes      bigint,
    requested_by    uuid REFERENCES users(id),
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    heartbeat_at    timestamptz,
    finished_at     timestamptz
);

CREATE INDEX idx_vendor_fetch_jobs_state ON vendor_fetch_jobs(state, created_at);

COMMIT;

-- Wake-on-LAN scheduling. The API cannot reach remote L2 segments, so
-- wake requests are queued here and drained by the edge agent at the
-- machine's site, which broadcasts the magic packet on its LAN.
--
-- site scoping: a machine's site lives in machines.attributes->>'site'
-- (operator-set); the request row snapshots it (plus the MAC) so the
-- edge poller needs no joins. 'default' matches agents with no
-- EDGE_SITE configured.

BEGIN;

CREATE TABLE wake_requests (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    machine_id    uuid NOT NULL REFERENCES machines(id) ON DELETE CASCADE,
    mac           macaddr NOT NULL,
    site          text NOT NULL DEFAULT 'default',
    requested_by  uuid REFERENCES users(id),
    scheduled_at  timestamptz NOT NULL DEFAULT now(),
    claimed_at    timestamptz,
    claimed_by    text,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_wake_requests_due
    ON wake_requests(site, scheduled_at)
    WHERE claimed_at IS NULL;

COMMIT;

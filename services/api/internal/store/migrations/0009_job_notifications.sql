-- Job-completion webhook notifications: one nullable timestamp is the
-- whole delivery state machine. The worker claims unnotified terminal
-- jobs (FOR UPDATE SKIP LOCKED) and POSTs a webhook, marking before the
-- POST (at-most-once; the UI/SSE stays the authoritative record).
-- Keyed on finished_at, which TransitionJob sets at terminal states.

BEGIN;

ALTER TABLE deployment_jobs ADD COLUMN notified_at timestamptz;

CREATE INDEX deployment_jobs_unnotified_terminal_idx
    ON deployment_jobs (finished_at)
    WHERE state IN ('completed','failed','cancelled') AND notified_at IS NULL;

COMMIT;

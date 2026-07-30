-- Job read permission for the operator and viewer roles. The API now
-- enforces perm checks on the job/read endpoints; without this seed the
-- non-admin roles would lose visibility they previously had implicitly.

BEGIN;

INSERT INTO role_permissions (role_id, permission) VALUES
    ('00000000-0000-0000-0000-000000000002', 'job.read'),
    ('00000000-0000-0000-0000-000000000003', 'job.read')
ON CONFLICT DO NOTHING;

COMMIT;

-- User-administration permissions. user.read is seeded for the operator
-- role; user.write is deliberately NOT seeded — only the admin role's
-- '*' grants it, so role changes stay an admin-only operation.

BEGIN;

INSERT INTO role_permissions (role_id, permission) VALUES
    ('00000000-0000-0000-0000-000000000002', 'user.read')
ON CONFLICT DO NOTHING;

COMMIT;

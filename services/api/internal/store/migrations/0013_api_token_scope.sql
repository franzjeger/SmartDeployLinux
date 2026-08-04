-- Optional role-scoping for API tokens. An empty scope_roles (the default,
-- and the value every pre-existing token keeps) means the token carries
-- the owner's full permissions — unchanged Phase 31 behavior. A non-empty
-- scope_roles restricts the token to the permissions the owner holds
-- *through those specific roles*: effective perms = the owner's perms
-- intersected with the named roles, computed at verification time (so if
-- the owner later loses a scoped role, the token loses it too). A token
-- can only be scoped to roles the owner has, so this can never escalate.

BEGIN;

ALTER TABLE api_tokens
    ADD COLUMN scope_roles text[] NOT NULL DEFAULT '{}';

COMMIT;

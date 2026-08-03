-- Long-lived API tokens (personal access tokens) for headless clients —
-- SDKs, deployctl, CI, automation — that cannot run the interactive OIDC
-- device flow. A token authenticates AS its owning user, inheriting that
-- user's roles/permissions; it can never grant more than the owner already
-- has. Tokens are stored only as a peppered SHA-256 hash (never
-- plaintext), are individually revocable, and may carry an expiry.
-- See docs/SECURITY.md §"API tokens".

BEGIN;

CREATE TABLE api_tokens (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name          text NOT NULL,
    token_hash    text UNIQUE NOT NULL,          -- tokens.HashAPIToken(secret, pepper)
    token_prefix  text NOT NULL,                 -- 'dpsk_xxxxxxx', for display only
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at    timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz,                   -- NULL = never expires
    last_used_at  timestamptz,
    revoked_at    timestamptz
);

CREATE INDEX idx_api_tokens_user ON api_tokens(user_id, created_at DESC);

-- Self-service: any operator may manage their own tokens. Because a token
-- inherits only its owner's permissions it cannot escalate, so this is
-- safe to grant broadly; the admin role's '*' already covers it.
INSERT INTO role_permissions (role_id, permission) VALUES
    ('00000000-0000-0000-0000-000000000002', 'apitoken.read'),
    ('00000000-0000-0000-0000-000000000002', 'apitoken.write')
ON CONFLICT DO NOTHING;

COMMIT;

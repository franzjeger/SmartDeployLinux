-- 0001_init.sql
-- Initial schema. Materializes the ER model in docs/ARCHITECTURE.md.
-- Forward-only migrations; never edit a shipped migration in place.

BEGIN;

CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

-- Users -----------------------------------------------------------------

CREATE TABLE users (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email           citext UNIQUE NOT NULL,
    oidc_subject    text UNIQUE,
    display_name    text,
    created_at      timestamptz NOT NULL DEFAULT now(),
    disabled_at     timestamptz
);

CREATE TABLE roles (
    id    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name  text UNIQUE NOT NULL
);

CREATE TABLE role_permissions (
    role_id     uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    permission  text NOT NULL,
    PRIMARY KEY (role_id, permission)
);

CREATE TABLE user_roles (
    user_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    role_id  uuid NOT NULL REFERENCES roles(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

-- Blob store ------------------------------------------------------------

CREATE TABLE blobs (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    sha256       text UNIQUE NOT NULL,
    size_bytes   bigint NOT NULL CHECK (size_bytes >= 0),
    s3_bucket    text NOT NULL,
    s3_key       text NOT NULL,
    signature    text,
    created_at   timestamptz NOT NULL DEFAULT now()
);

-- Images ----------------------------------------------------------------

CREATE TABLE images (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name         text UNIQUE NOT NULL,
    os_family    text NOT NULL CHECK (os_family IN ('windows','linux')),
    os_version   text NOT NULL,
    arch         text NOT NULL CHECK (arch IN ('amd64','arm64')),
    description  text,
    created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE image_versions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    image_id     uuid NOT NULL REFERENCES images(id) ON DELETE CASCADE,
    version_tag  text NOT NULL,
    blob_id      uuid NOT NULL REFERENCES blobs(id),
    signature    text,
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (image_id, version_tag)
);

-- Driver packs ----------------------------------------------------------

CREATE TABLE driver_packs (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    vendor       text NOT NULL,
    model        text NOT NULL,
    os_family    text NOT NULL,
    os_version   text NOT NULL,
    UNIQUE (vendor, model, os_family, os_version)
);

CREATE TABLE driver_pack_versions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pack_id      uuid NOT NULL REFERENCES driver_packs(id) ON DELETE CASCADE,
    version_tag  text NOT NULL,
    blob_id      uuid NOT NULL REFERENCES blobs(id),
    created_at   timestamptz NOT NULL DEFAULT now(),
    UNIQUE (pack_id, version_tag)
);

-- Match rules: how a driver_pack_version is selected for a target
-- machine. match_type is one of:
--   'dmi-vendor', 'dmi-product', 'dmi-baseboard', 'pci-vid-did',
--   'usb-vid-did', 'os-version'.
-- Multiple rules on a pack version are AND'd; first pack version with
-- *any* matching rule wins per device class. See driverpack/match.go.
CREATE TABLE driver_match_rules (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    pack_version_id uuid NOT NULL REFERENCES driver_pack_versions(id) ON DELETE CASCADE,
    match_type      text NOT NULL,
    match_value     text NOT NULL
);

CREATE INDEX idx_driver_match_rules_pack ON driver_match_rules(pack_version_id);
CREATE INDEX idx_driver_match_rules_lookup ON driver_match_rules(match_type, match_value);

-- Deployment profiles ---------------------------------------------------

CREATE TABLE deployment_profiles (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name               text UNIQUE NOT NULL,
    image_id           uuid NOT NULL REFERENCES images(id),
    answer_file_vars   jsonb NOT NULL DEFAULT '{}'::jsonb,
    post_install_url   text,
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE answer_file_templates (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    profile_id  uuid NOT NULL REFERENCES deployment_profiles(id) ON DELETE CASCADE,
    kind        text NOT NULL CHECK (kind IN ('unattend','autoinstall','kickstart','preseed','ignition','cloud-init')),
    body        text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (profile_id, kind)
);

-- Machines --------------------------------------------------------------

CREATE TABLE machines (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    asset_tag           text UNIQUE,
    mac_primary         macaddr,
    uuid_smbios         uuid,
    vendor              text,
    model               text,
    default_profile_id  uuid REFERENCES deployment_profiles(id),
    attributes          jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_machines_mac ON machines(mac_primary);
CREATE INDEX idx_machines_uuid ON machines(uuid_smbios);

-- Auth codes ------------------------------------------------------------
--
-- code_hash is argon2id of the cleartext code. Nothing else stores it.
CREATE TABLE auth_codes (
    id                  uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code_hash           text UNIQUE NOT NULL,
    machine_id          uuid NOT NULL REFERENCES machines(id),
    profile_id          uuid NOT NULL REFERENCES deployment_profiles(id),
    issued_by           uuid NOT NULL REFERENCES users(id),
    issued_from_ip      inet,
    binding_cidr        cidr,
    issued_at           timestamptz NOT NULL DEFAULT now(),
    expires_at          timestamptz NOT NULL,
    redeemed_at         timestamptz,
    redeemed_from_ip    inet,
    attempts            int NOT NULL DEFAULT 0,
    locked_at           timestamptz,
    label               text
);

CREATE INDEX idx_auth_codes_pending ON auth_codes(expires_at)
    WHERE redeemed_at IS NULL AND locked_at IS NULL;

-- One-shot tokens spawned by a successful redemption. Used by the
-- in-installer phone-home to fetch a domain-join cred or similar
-- *exactly once*.
CREATE TABLE one_shot_tokens (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    auth_code_id    uuid NOT NULL REFERENCES auth_codes(id) ON DELETE CASCADE,
    token_hash      text UNIQUE NOT NULL,
    purpose         text NOT NULL,
    expires_at      timestamptz NOT NULL,
    consumed_at     timestamptz,
    consumed_from_ip inet
);

-- Deployment jobs -------------------------------------------------------

CREATE TYPE deployment_state AS ENUM (
    'pending',         -- code issued, not yet redeemed
    'bootstrapped',    -- redemption successful, target on tailnet
    'imaging',         -- target has chainloaded, install in progress
    'post_install',    -- post-install playbook running
    'completed',
    'failed',
    'cancelled'
);

CREATE TABLE deployment_jobs (
    id              uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    machine_id      uuid NOT NULL REFERENCES machines(id),
    profile_id      uuid NOT NULL REFERENCES deployment_profiles(id),
    auth_code_id    uuid REFERENCES auth_codes(id),
    state           deployment_state NOT NULL DEFAULT 'pending',
    created_at      timestamptz NOT NULL DEFAULT now(),
    started_at      timestamptz,
    finished_at     timestamptz,
    result          jsonb
);

CREATE INDEX idx_deployment_jobs_state ON deployment_jobs(state);
CREATE INDEX idx_deployment_jobs_machine ON deployment_jobs(machine_id);

CREATE TABLE deployment_events (
    id        bigserial PRIMARY KEY,
    job_id    uuid NOT NULL REFERENCES deployment_jobs(id) ON DELETE CASCADE,
    phase     text NOT NULL,
    message   text NOT NULL,
    data      jsonb,
    at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_deployment_events_job ON deployment_events(job_id, at);

-- Bootstrap stick inventory --------------------------------------------

CREATE TABLE bootstrap_sticks (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    image_sha256     text NOT NULL,
    tailnet          text NOT NULL,
    deploy_url       text NOT NULL,
    ca_fingerprint   text NOT NULL,
    built_by         uuid NOT NULL REFERENCES users(id),
    built_at         timestamptz NOT NULL DEFAULT now(),
    label            text,
    retired_at       timestamptz
);

CREATE INDEX idx_bootstrap_sticks_ca ON bootstrap_sticks(ca_fingerprint)
    WHERE retired_at IS NULL;

-- Audit -----------------------------------------------------------------

CREATE TABLE audit_events (
    id          bigserial PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    actor_id    uuid,                -- nullable for unauthenticated actions
    actor_kind  text NOT NULL,       -- 'user', 'stick', 'system'
    action      text NOT NULL,
    subject_id  uuid,
    subject_kind text,
    data        jsonb,
    source_ip   inet
);

CREATE INDEX idx_audit_events_at ON audit_events(at DESC);
CREATE INDEX idx_audit_events_actor ON audit_events(actor_id, at DESC);
CREATE INDEX idx_audit_events_subject ON audit_events(subject_id, at DESC);

-- Rate limit table for /redeem source IPs.
-- Reaped periodically by the worker.
CREATE TABLE redeem_attempts (
    id          bigserial PRIMARY KEY,
    source_ip   inet NOT NULL,
    at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_redeem_attempts_lookup ON redeem_attempts(source_ip, at);

-- Job queue (used by the worker, backed by LISTEN/NOTIFY) --------------

CREATE TABLE jobs (
    id           bigserial PRIMARY KEY,
    queue        text NOT NULL,
    payload      jsonb NOT NULL,
    state        text NOT NULL DEFAULT 'pending'
                 CHECK (state IN ('pending','running','done','failed')),
    attempts     int NOT NULL DEFAULT 0,
    max_attempts int NOT NULL DEFAULT 3,
    locked_by    text,
    locked_at    timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    finished_at  timestamptz,
    last_error   text
);

CREATE INDEX idx_jobs_pending ON jobs(queue, created_at)
    WHERE state = 'pending';

-- Seed roles ------------------------------------------------------------

INSERT INTO roles (id, name) VALUES
    ('00000000-0000-0000-0000-000000000001', 'admin'),
    ('00000000-0000-0000-0000-000000000002', 'operator'),
    ('00000000-0000-0000-0000-000000000003', 'viewer')
ON CONFLICT DO NOTHING;

INSERT INTO role_permissions (role_id, permission) VALUES
    ('00000000-0000-0000-0000-000000000001', '*'),
    ('00000000-0000-0000-0000-000000000002', 'machine.read'),
    ('00000000-0000-0000-0000-000000000002', 'machine.write'),
    ('00000000-0000-0000-0000-000000000002', 'deployment.create'),
    ('00000000-0000-0000-0000-000000000002', 'image.read'),
    ('00000000-0000-0000-0000-000000000002', 'driverpack.read'),
    ('00000000-0000-0000-0000-000000000002', 'audit.read'),
    ('00000000-0000-0000-0000-000000000003', 'machine.read'),
    ('00000000-0000-0000-0000-000000000003', 'image.read'),
    ('00000000-0000-0000-0000-000000000003', 'audit.read')
ON CONFLICT DO NOTHING;

COMMIT;

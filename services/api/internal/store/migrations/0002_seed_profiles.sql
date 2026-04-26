-- 0002_seed_profiles.sql
-- Seed default profiles so the UI has something to show on first run.
-- Idempotent against any pre-existing rows: lookups are by name/sha256
-- rather than fixed UUIDs, so this migration applies cleanly even on
-- databases that already have manually-created images/profiles.
--
-- Operators replace the placeholder blobs with real ingested image
-- blobs via deployctl images upload (TODO).

BEGIN;

-- Placeholder blobs.
INSERT INTO blobs (sha256, size_bytes, s3_bucket, s3_key) VALUES
    ('placeholder-ubuntu-24.04', 0, 'images', 'ubuntu-2404/install.iso'),
    ('placeholder-windows-11',   0, 'images', 'win11/install.wim')
ON CONFLICT (sha256) DO NOTHING;

-- Images.
INSERT INTO images (name, os_family, os_version, arch, description) VALUES
    ('ubuntu-2404', 'linux',   '24.04', 'amd64', 'Ubuntu 24.04 LTS (placeholder)'),
    ('windows-11',  'windows', '11',    'amd64', 'Windows 11 (placeholder)')
ON CONFLICT (name) DO NOTHING;

-- One image_version per image, pointing at its placeholder blob.
INSERT INTO image_versions (image_id, version_tag, blob_id)
SELECT
    (SELECT id FROM images WHERE name = 'ubuntu-2404'),
    '0.0-placeholder',
    (SELECT id FROM blobs  WHERE sha256 = 'placeholder-ubuntu-24.04')
WHERE NOT EXISTS (
    SELECT 1 FROM image_versions iv
    JOIN images i ON i.id = iv.image_id
    WHERE i.name = 'ubuntu-2404' AND iv.version_tag = '0.0-placeholder'
);

INSERT INTO image_versions (image_id, version_tag, blob_id)
SELECT
    (SELECT id FROM images WHERE name = 'windows-11'),
    '0.0-placeholder',
    (SELECT id FROM blobs  WHERE sha256 = 'placeholder-windows-11')
WHERE NOT EXISTS (
    SELECT 1 FROM image_versions iv
    JOIN images i ON i.id = iv.image_id
    WHERE i.name = 'windows-11' AND iv.version_tag = '0.0-placeholder'
);

-- Default deployment profiles.
INSERT INTO deployment_profiles (name, image_id, answer_file_vars)
SELECT
    'ubuntu-2404-baseline',
    (SELECT id FROM images WHERE name = 'ubuntu-2404'),
    '{"hostname_template":"{{asset_tag}}"}'::jsonb
WHERE NOT EXISTS (
    SELECT 1 FROM deployment_profiles WHERE name = 'ubuntu-2404-baseline'
);

INSERT INTO deployment_profiles (name, image_id, answer_file_vars)
SELECT
    'win11-baseline',
    (SELECT id FROM images WHERE name = 'windows-11'),
    '{"hostname_template":"{{asset_tag}}"}'::jsonb
WHERE NOT EXISTS (
    SELECT 1 FROM deployment_profiles WHERE name = 'win11-baseline'
);

COMMIT;

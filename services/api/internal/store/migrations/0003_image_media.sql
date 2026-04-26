-- 0003_image_media.sql
-- Add a JSONB `media` column to images so operators can register images
-- that live at external URLs (upstream Ubuntu mirrors, their own internal
-- HTTP storage, etc.) without having to upload them through deployserver.
--
-- Expected shape (all keys optional; renderer picks what it needs based
-- on os_family + boot mode):
--   {
--     "kernel_url":   "https://releases.ubuntu.com/24.04/.../linux",
--     "initrd_url":   "https://releases.ubuntu.com/24.04/.../initrd",
--     "wimboot_url":  "http://internal/wimboot/wimboot",     // Windows
--     "bootwim_url":  "http://internal/winpe/boot.wim",      // Windows
--     "wim_url":      "http://internal/win11/install.wim",   // Windows
--     "kernel_args":  "console=ttyS0,115200 quiet"            // optional
--   }

BEGIN;

ALTER TABLE images
    ADD COLUMN IF NOT EXISTS media jsonb NOT NULL DEFAULT '{}'::jsonb;

-- Backfill the placeholder seeds from migration 0002 with sensible
-- upstream-mirror URLs so operators can immediately try a real deploy
-- against Ubuntu's netboot.
UPDATE images SET media = jsonb_build_object(
    'kernel_url', 'https://releases.ubuntu.com/24.04/ubuntu-24.04.2-live-server-amd64.iso',
    'initrd_url', 'https://releases.ubuntu.com/24.04/ubuntu-24.04.2-live-server-amd64.iso',
    'kernel_args', 'autoinstall ds=nocloud-net'
) WHERE name = 'ubuntu-2404' AND media = '{}'::jsonb;

UPDATE images SET media = jsonb_build_object(
    'wimboot_url', '',
    'bootwim_url', '',
    'wim_url',     ''
) WHERE name = 'windows-11' AND media = '{}'::jsonb;

COMMIT;

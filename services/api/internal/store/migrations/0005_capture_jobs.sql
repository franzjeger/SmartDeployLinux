-- Golden-image capture support. A capture job boots the same WinPE
-- environment as a deploy, but runs capture.cmd instead of deploy.cmd:
-- it captures the sysprepped OS volume to a WIM, uploads it to the
-- object store via a presigned URL, and the server registers the blob
-- as a new version of the target image.
--
-- The intent (kind + capture target) is stored on the auth code at
-- issuance and copied onto the deployment job at redeem, so the
-- redeem/boot path needs no new parameters.

BEGIN;

ALTER TABLE auth_codes
    ADD COLUMN kind text NOT NULL DEFAULT 'deploy'
        CHECK (kind IN ('deploy','capture')),
    ADD COLUMN capture_image_id uuid REFERENCES images(id),
    ADD COLUMN capture_version_tag text;

ALTER TABLE deployment_jobs
    ADD COLUMN kind text NOT NULL DEFAULT 'deploy'
        CHECK (kind IN ('deploy','capture')),
    ADD COLUMN capture_image_id uuid REFERENCES images(id),
    ADD COLUMN capture_version_tag text;

COMMIT;

-- Interactive PXE menu: boot tokens minted server-side from an
-- unauthenticated (network-trusted) LAN PXE boot need an auth_codes row
-- with no human issuer. The broker path always sets issued_by, so
-- nothing regresses; menu-minted rows carry label 'pxe-menu'.

BEGIN;

ALTER TABLE auth_codes ALTER COLUMN issued_by DROP NOT NULL;

COMMIT;

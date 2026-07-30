-- Sites: named locations with an optional local image mirror (the edge
-- agent's caching blob proxy). When a machine's attributes.site matches
-- a site with a mirror_base_url, the render pipeline rewrites its image
-- media URLs (/static/*, /blobs/*) to the mirror — so a room of
-- machines pulls the multi-GB image over the WAN once and over the LAN
-- N times. This is the reliable equivalent of WDS multicast for the
-- WAN leg; the LAN leg stays unicast HTTP from the local box.

BEGIN;

CREATE TABLE sites (
    name            text PRIMARY KEY,
    mirror_base_url text,          -- e.g. http://192.168.10.2:8090 (edge box)
    description     text,
    created_at      timestamptz NOT NULL DEFAULT now()
);

COMMIT;

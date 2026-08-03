"""The operation table.

Every client method issues exactly one of these, and the parity test
(tests/test_parity.py) asserts this set is in exact bijection with the
paths+methods in the published OpenAPI spec — so a spec change not
reflected here (or vice versa) fails the build. Because the client
references the named constants directly, removing one is also a static
type-check error (pyright/mypy report an undefined name), not merely a
test failure.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Operation:
    id: str
    method: str
    path: str


OP_AUTH_CONFIG = Operation("authConfig", "GET", "/api/v1/auth/config")
OP_ME = Operation("me", "GET", "/api/v1/me")

OP_LIST_MACHINES = Operation("listMachines", "GET", "/api/v1/machines")
OP_CREATE_MACHINE = Operation("createMachine", "POST", "/api/v1/machines")
OP_GET_MACHINE = Operation("getMachine", "GET", "/api/v1/machines/{id}")
OP_UPDATE_MACHINE = Operation("updateMachine", "PATCH", "/api/v1/machines/{id}")
OP_DELETE_MACHINE = Operation("deleteMachine", "DELETE", "/api/v1/machines/{id}")
OP_WAKE_MACHINE = Operation("wakeMachine", "POST", "/api/v1/machines/{id}/wake")
OP_LIST_WAKES = Operation("listWakes", "GET", "/api/v1/machines/{id}/wake")

OP_LIST_PROFILES = Operation("listProfiles", "GET", "/api/v1/profiles")
OP_CREATE_PROFILE = Operation("createProfile", "POST", "/api/v1/profiles")
OP_GET_PROFILE = Operation("getProfile", "GET", "/api/v1/profiles/{id}")
OP_UPDATE_PROFILE = Operation("updateProfile", "PATCH", "/api/v1/profiles/{id}")
OP_DELETE_PROFILE = Operation("deleteProfile", "DELETE", "/api/v1/profiles/{id}")
OP_UPSERT_TEMPLATE = Operation("upsertProfileTemplate", "PUT", "/api/v1/profiles/{id}/templates")
OP_DELETE_TEMPLATE = Operation("deleteProfileTemplate", "DELETE", "/api/v1/profiles/{id}/templates/{kind}")
OP_PREVIEW_PROFILE = Operation("previewProfileTemplate", "POST", "/api/v1/profiles/{id}/preview")

OP_LIST_IMAGES = Operation("listImages", "GET", "/api/v1/images")
OP_CREATE_IMAGE = Operation("createImage", "POST", "/api/v1/images")
OP_GET_IMAGE = Operation("getImage", "GET", "/api/v1/images/{id}")
OP_UPDATE_IMAGE = Operation("updateImage", "PATCH", "/api/v1/images/{id}")
OP_DELETE_IMAGE = Operation("deleteImage", "DELETE", "/api/v1/images/{id}")
OP_LIST_IMAGE_VERSIONS = Operation("listImageVersions", "GET", "/api/v1/images/{id}/versions")
OP_CREATE_IMAGE_VERSION = Operation("createImageVersion", "POST", "/api/v1/images/{id}/versions")
OP_CREATE_BLOB = Operation("createBlob", "POST", "/api/v1/blobs")

OP_LIST_DRIVER_PACKS = Operation("listDriverPacks", "GET", "/api/v1/driver-packs")
OP_CREATE_DRIVER_PACK = Operation("createDriverPack", "POST", "/api/v1/driver-packs")
OP_DELETE_DRIVER_VERSION = Operation("deleteDriverPackVersion", "DELETE", "/api/v1/driver-packs/versions/{id}")

OP_SEARCH_VENDOR_PACKS = Operation("searchVendorDriverPacks", "GET", "/api/v1/vendor-driverpacks")
OP_FETCH_VENDOR_PACK = Operation("fetchVendorDriverPack", "POST", "/api/v1/vendor-driverpacks/fetch")
OP_LIST_VENDOR_FETCH_JOBS = Operation("listVendorFetchJobs", "GET", "/api/v1/vendor-driverpacks/jobs")

OP_LIST_CATALOG = Operation("listCatalog", "GET", "/api/v1/catalog")
OP_INSTALL_CATALOG = Operation("installFromCatalog", "POST", "/api/v1/catalog/install")

OP_LIST_JOBS = Operation("listJobs", "GET", "/api/v1/jobs")
OP_GET_JOB = Operation("getJob", "GET", "/api/v1/jobs/{id}")
OP_CANCEL_JOB = Operation("cancelJob", "POST", "/api/v1/jobs/{id}/cancel")

OP_QUERY_AUDIT = Operation("queryAudit", "GET", "/api/v1/audit")

OP_REPORT_SUMMARY = Operation("reportSummary", "GET", "/api/v1/reports/summary")
OP_REPORT_DAILY = Operation("reportDaily", "GET", "/api/v1/reports/daily")
OP_REPORT_BY_PROFILE = Operation("reportByProfile", "GET", "/api/v1/reports/by-profile")
OP_REPORT_BY_SITE = Operation("reportBySite", "GET", "/api/v1/reports/by-site")
OP_REPORT_JOBS_CSV = Operation("reportJobsCSV", "GET", "/api/v1/reports/jobs.csv")

OP_ISSUE_DEPLOYMENT = Operation("issueDeployment", "POST", "/api/v1/deployments/issue")
OP_BULK_DEPLOY = Operation("bulkDeploy", "POST", "/api/v1/deployments/bulk")

OP_LIST_USERS = Operation("listUsers", "GET", "/api/v1/users")
OP_LIST_ROLES = Operation("listRoles", "GET", "/api/v1/roles")
OP_GRANT_ROLE = Operation("grantUserRole", "POST", "/api/v1/users/{id}/roles")
OP_REVOKE_ROLE = Operation("revokeUserRole", "DELETE", "/api/v1/users/{id}/roles/{role}")

OP_LIST_SITES = Operation("listSites", "GET", "/api/v1/sites")
OP_UPSERT_SITE = Operation("upsertSite", "PUT", "/api/v1/sites")
OP_DELETE_SITE = Operation("deleteSite", "DELETE", "/api/v1/sites/{name}")

OP_LIST_STICKS = Operation("listSticks", "GET", "/api/v1/bootstrap-sticks")
OP_REGISTER_STICK = Operation("registerStick", "POST", "/api/v1/bootstrap-sticks")
OP_STICK_CONFIG = Operation("stickConfig", "GET", "/api/v1/bootstrap-sticks/config")


#: Every operation the SDK implements. Checked against the spec by the parity test.
ALL_OPERATIONS: list[Operation] = [
    OP_AUTH_CONFIG, OP_ME,
    OP_LIST_MACHINES, OP_CREATE_MACHINE, OP_GET_MACHINE, OP_UPDATE_MACHINE,
    OP_DELETE_MACHINE, OP_WAKE_MACHINE, OP_LIST_WAKES,
    OP_LIST_PROFILES, OP_CREATE_PROFILE, OP_GET_PROFILE, OP_UPDATE_PROFILE,
    OP_DELETE_PROFILE, OP_UPSERT_TEMPLATE, OP_DELETE_TEMPLATE, OP_PREVIEW_PROFILE,
    OP_LIST_IMAGES, OP_CREATE_IMAGE, OP_GET_IMAGE, OP_UPDATE_IMAGE, OP_DELETE_IMAGE,
    OP_LIST_IMAGE_VERSIONS, OP_CREATE_IMAGE_VERSION, OP_CREATE_BLOB,
    OP_LIST_DRIVER_PACKS, OP_CREATE_DRIVER_PACK, OP_DELETE_DRIVER_VERSION,
    OP_SEARCH_VENDOR_PACKS, OP_FETCH_VENDOR_PACK, OP_LIST_VENDOR_FETCH_JOBS,
    OP_LIST_CATALOG, OP_INSTALL_CATALOG,
    OP_LIST_JOBS, OP_GET_JOB, OP_CANCEL_JOB,
    OP_QUERY_AUDIT,
    OP_REPORT_SUMMARY, OP_REPORT_DAILY, OP_REPORT_BY_PROFILE, OP_REPORT_BY_SITE, OP_REPORT_JOBS_CSV,
    OP_ISSUE_DEPLOYMENT, OP_BULK_DEPLOY,
    OP_LIST_USERS, OP_LIST_ROLES, OP_GRANT_ROLE, OP_REVOKE_ROLE,
    OP_LIST_SITES, OP_UPSERT_SITE, OP_DELETE_SITE,
    OP_LIST_STICKS, OP_REGISTER_STICK, OP_STICK_CONFIG,
]

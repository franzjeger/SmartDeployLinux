// The operation table. Every client method issues exactly one of these,
// and the parity test (test/parity.test.ts) asserts this set is in exact
// bijection with the paths+methods in the published OpenAPI spec — so a
// spec change not reflected here (or vice versa) fails the build. This is
// the guarantee a code generator gives, enforced on a hand-written client.

export interface Operation {
  readonly id: string;
  readonly method: string;
  readonly path: string;
}

function op(id: string, method: string, path: string): Operation {
  return { id, method, path };
}

/**
 * OPERATIONS maps an operation id to its HTTP method + path template.
 * ALL_OPERATIONS is derived from it, so there is no second list to keep
 * in sync.
 */
export const OPERATIONS = {
  authConfig: op("authConfig", "GET", "/api/v1/auth/config"),
  me: op("me", "GET", "/api/v1/me"),

  listMachines: op("listMachines", "GET", "/api/v1/machines"),
  createMachine: op("createMachine", "POST", "/api/v1/machines"),
  getMachine: op("getMachine", "GET", "/api/v1/machines/{id}"),
  updateMachine: op("updateMachine", "PATCH", "/api/v1/machines/{id}"),
  deleteMachine: op("deleteMachine", "DELETE", "/api/v1/machines/{id}"),
  wakeMachine: op("wakeMachine", "POST", "/api/v1/machines/{id}/wake"),
  listWakes: op("listWakes", "GET", "/api/v1/machines/{id}/wake"),

  listProfiles: op("listProfiles", "GET", "/api/v1/profiles"),
  createProfile: op("createProfile", "POST", "/api/v1/profiles"),
  getProfile: op("getProfile", "GET", "/api/v1/profiles/{id}"),
  updateProfile: op("updateProfile", "PATCH", "/api/v1/profiles/{id}"),
  deleteProfile: op("deleteProfile", "DELETE", "/api/v1/profiles/{id}"),
  upsertProfileTemplate: op("upsertProfileTemplate", "PUT", "/api/v1/profiles/{id}/templates"),
  deleteProfileTemplate: op("deleteProfileTemplate", "DELETE", "/api/v1/profiles/{id}/templates/{kind}"),

  listImages: op("listImages", "GET", "/api/v1/images"),
  createImage: op("createImage", "POST", "/api/v1/images"),
  getImage: op("getImage", "GET", "/api/v1/images/{id}"),
  updateImage: op("updateImage", "PATCH", "/api/v1/images/{id}"),
  deleteImage: op("deleteImage", "DELETE", "/api/v1/images/{id}"),
  listImageVersions: op("listImageVersions", "GET", "/api/v1/images/{id}/versions"),
  createImageVersion: op("createImageVersion", "POST", "/api/v1/images/{id}/versions"),
  createBlob: op("createBlob", "POST", "/api/v1/blobs"),

  listDriverPacks: op("listDriverPacks", "GET", "/api/v1/driver-packs"),
  createDriverPack: op("createDriverPack", "POST", "/api/v1/driver-packs"),
  deleteDriverPackVersion: op("deleteDriverPackVersion", "DELETE", "/api/v1/driver-packs/versions/{id}"),
  searchVendorDriverPacks: op("searchVendorDriverPacks", "GET", "/api/v1/vendor-driverpacks"),
  fetchVendorDriverPack: op("fetchVendorDriverPack", "POST", "/api/v1/vendor-driverpacks/fetch"),
  listVendorFetchJobs: op("listVendorFetchJobs", "GET", "/api/v1/vendor-driverpacks/jobs"),

  listCatalog: op("listCatalog", "GET", "/api/v1/catalog"),
  installFromCatalog: op("installFromCatalog", "POST", "/api/v1/catalog/install"),

  listJobs: op("listJobs", "GET", "/api/v1/jobs"),
  getJob: op("getJob", "GET", "/api/v1/jobs/{id}"),
  cancelJob: op("cancelJob", "POST", "/api/v1/jobs/{id}/cancel"),

  queryAudit: op("queryAudit", "GET", "/api/v1/audit"),

  reportSummary: op("reportSummary", "GET", "/api/v1/reports/summary"),
  reportDaily: op("reportDaily", "GET", "/api/v1/reports/daily"),
  reportByProfile: op("reportByProfile", "GET", "/api/v1/reports/by-profile"),
  reportBySite: op("reportBySite", "GET", "/api/v1/reports/by-site"),
  reportJobsCSV: op("reportJobsCSV", "GET", "/api/v1/reports/jobs.csv"),

  issueDeployment: op("issueDeployment", "POST", "/api/v1/deployments/issue"),
  bulkDeploy: op("bulkDeploy", "POST", "/api/v1/deployments/bulk"),

  listUsers: op("listUsers", "GET", "/api/v1/users"),
  listRoles: op("listRoles", "GET", "/api/v1/roles"),
  grantUserRole: op("grantUserRole", "POST", "/api/v1/users/{id}/roles"),
  revokeUserRole: op("revokeUserRole", "DELETE", "/api/v1/users/{id}/roles/{role}"),

  listSites: op("listSites", "GET", "/api/v1/sites"),
  upsertSite: op("upsertSite", "PUT", "/api/v1/sites"),
  deleteSite: op("deleteSite", "DELETE", "/api/v1/sites/{name}"),

  listSticks: op("listSticks", "GET", "/api/v1/bootstrap-sticks"),
  registerStick: op("registerStick", "POST", "/api/v1/bootstrap-sticks"),
  stickConfig: op("stickConfig", "GET", "/api/v1/bootstrap-sticks/config"),
} as const satisfies Record<string, Operation>;

/** Every operation the SDK implements. Checked against the spec by the parity test. */
export const ALL_OPERATIONS: readonly Operation[] = Object.values(OPERATIONS);

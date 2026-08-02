package sdk

// Operation identifies one API endpoint. Every SDK method calls do/doRaw
// with exactly one of these, and the parity test asserts this set is in
// exact bijection with the paths+methods in the published OpenAPI spec —
// so a spec change that isn't reflected here (or vice versa) fails CI.
type Operation struct {
	ID     string
	Method string
	Path   string
}

var (
	opAuthConfig          = Operation{"authConfig", "GET", "/api/v1/auth/config"}
	opMe                  = Operation{"me", "GET", "/api/v1/me"}
	opListMachines        = Operation{"listMachines", "GET", "/api/v1/machines"}
	opCreateMachine       = Operation{"createMachine", "POST", "/api/v1/machines"}
	opGetMachine          = Operation{"getMachine", "GET", "/api/v1/machines/{id}"}
	opUpdateMachine       = Operation{"updateMachine", "PATCH", "/api/v1/machines/{id}"}
	opDeleteMachine       = Operation{"deleteMachine", "DELETE", "/api/v1/machines/{id}"}
	opWakeMachine         = Operation{"wakeMachine", "POST", "/api/v1/machines/{id}/wake"}
	opListWakes           = Operation{"listWakes", "GET", "/api/v1/machines/{id}/wake"}
	opListProfiles        = Operation{"listProfiles", "GET", "/api/v1/profiles"}
	opCreateProfile       = Operation{"createProfile", "POST", "/api/v1/profiles"}
	opGetProfile          = Operation{"getProfile", "GET", "/api/v1/profiles/{id}"}
	opUpdateProfile       = Operation{"updateProfile", "PATCH", "/api/v1/profiles/{id}"}
	opDeleteProfile       = Operation{"deleteProfile", "DELETE", "/api/v1/profiles/{id}"}
	opUpsertTemplate      = Operation{"upsertProfileTemplate", "PUT", "/api/v1/profiles/{id}/templates"}
	opDeleteTemplate      = Operation{"deleteProfileTemplate", "DELETE", "/api/v1/profiles/{id}/templates/{kind}"}
	opListImages          = Operation{"listImages", "GET", "/api/v1/images"}
	opCreateImage         = Operation{"createImage", "POST", "/api/v1/images"}
	opGetImage            = Operation{"getImage", "GET", "/api/v1/images/{id}"}
	opUpdateImage         = Operation{"updateImage", "PATCH", "/api/v1/images/{id}"}
	opDeleteImage         = Operation{"deleteImage", "DELETE", "/api/v1/images/{id}"}
	opListImageVersions   = Operation{"listImageVersions", "GET", "/api/v1/images/{id}/versions"}
	opCreateImageVersion  = Operation{"createImageVersion", "POST", "/api/v1/images/{id}/versions"}
	opCreateBlob          = Operation{"createBlob", "POST", "/api/v1/blobs"}
	opListDriverPacks     = Operation{"listDriverPacks", "GET", "/api/v1/driver-packs"}
	opCreateDriverPack    = Operation{"createDriverPack", "POST", "/api/v1/driver-packs"}
	opDeleteDriverVersion = Operation{"deleteDriverPackVersion", "DELETE", "/api/v1/driver-packs/versions/{id}"}
	opSearchVendorPacks   = Operation{"searchVendorDriverPacks", "GET", "/api/v1/vendor-driverpacks"}
	opFetchVendorPack     = Operation{"fetchVendorDriverPack", "POST", "/api/v1/vendor-driverpacks/fetch"}
	opListVendorFetchJobs = Operation{"listVendorFetchJobs", "GET", "/api/v1/vendor-driverpacks/jobs"}
	opListCatalog         = Operation{"listCatalog", "GET", "/api/v1/catalog"}
	opInstallCatalog      = Operation{"installFromCatalog", "POST", "/api/v1/catalog/install"}
	opListJobs            = Operation{"listJobs", "GET", "/api/v1/jobs"}
	opGetJob              = Operation{"getJob", "GET", "/api/v1/jobs/{id}"}
	opCancelJob           = Operation{"cancelJob", "POST", "/api/v1/jobs/{id}/cancel"}
	opQueryAudit          = Operation{"queryAudit", "GET", "/api/v1/audit"}
	opReportSummary       = Operation{"reportSummary", "GET", "/api/v1/reports/summary"}
	opReportDaily         = Operation{"reportDaily", "GET", "/api/v1/reports/daily"}
	opReportByProfile     = Operation{"reportByProfile", "GET", "/api/v1/reports/by-profile"}
	opReportBySite        = Operation{"reportBySite", "GET", "/api/v1/reports/by-site"}
	opReportJobsCSV       = Operation{"reportJobsCSV", "GET", "/api/v1/reports/jobs.csv"}
	opIssueDeployment     = Operation{"issueDeployment", "POST", "/api/v1/deployments/issue"}
	opBulkDeploy          = Operation{"bulkDeploy", "POST", "/api/v1/deployments/bulk"}
	opListUsers           = Operation{"listUsers", "GET", "/api/v1/users"}
	opListRoles           = Operation{"listRoles", "GET", "/api/v1/roles"}
	opGrantRole           = Operation{"grantUserRole", "POST", "/api/v1/users/{id}/roles"}
	opRevokeRole          = Operation{"revokeUserRole", "DELETE", "/api/v1/users/{id}/roles/{role}"}
	opListSites           = Operation{"listSites", "GET", "/api/v1/sites"}
	opUpsertSite          = Operation{"upsertSite", "PUT", "/api/v1/sites"}
	opDeleteSite          = Operation{"deleteSite", "DELETE", "/api/v1/sites/{name}"}
	opListSticks          = Operation{"listSticks", "GET", "/api/v1/bootstrap-sticks"}
	opRegisterStick       = Operation{"registerStick", "POST", "/api/v1/bootstrap-sticks"}
	opStickConfig         = Operation{"stickConfig", "GET", "/api/v1/bootstrap-sticks/config"}
)

// AllOperations is every operation the SDK implements. The parity test
// checks this list against the OpenAPI spec.
var AllOperations = []Operation{
	opAuthConfig, opMe,
	opListMachines, opCreateMachine, opGetMachine, opUpdateMachine, opDeleteMachine, opWakeMachine, opListWakes,
	opListProfiles, opCreateProfile, opGetProfile, opUpdateProfile, opDeleteProfile, opUpsertTemplate, opDeleteTemplate,
	opListImages, opCreateImage, opGetImage, opUpdateImage, opDeleteImage, opListImageVersions, opCreateImageVersion, opCreateBlob,
	opListDriverPacks, opCreateDriverPack, opDeleteDriverVersion,
	opSearchVendorPacks, opFetchVendorPack, opListVendorFetchJobs,
	opListCatalog, opInstallCatalog,
	opListJobs, opGetJob, opCancelJob,
	opQueryAudit,
	opReportSummary, opReportDaily, opReportByProfile, opReportBySite, opReportJobsCSV,
	opIssueDeployment, opBulkDeploy,
	opListUsers, opListRoles, opGrantRole, opRevokeRole,
	opListSites, opUpsertSite, opDeleteSite,
	opListSticks, opRegisterStick, opStickConfig,
}

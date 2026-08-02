package sdk

import (
	"context"
	"net/url"
	"strconv"
)

// --- identity ---------------------------------------------------------

// AuthConfig fetches the public OIDC discovery info (no auth required).
func (c *Client) AuthConfig(ctx context.Context) (*AuthConfig, error) {
	var out AuthConfig
	return &out, c.do(ctx, opAuthConfig, nil, nil, nil, &out)
}

// Me returns the authenticated principal.
func (c *Client) Me(ctx context.Context) (*Me, error) {
	var out Me
	return &out, c.do(ctx, opMe, nil, nil, nil, &out)
}

// --- machines ---------------------------------------------------------

func (c *Client) ListMachines(ctx context.Context) ([]Machine, error) {
	var out []Machine
	return out, c.do(ctx, opListMachines, nil, nil, nil, &out)
}

func (c *Client) CreateMachine(ctx context.Context, in CreateMachineInput) (*Machine, error) {
	var out Machine
	return &out, c.do(ctx, opCreateMachine, nil, nil, in, &out)
}

func (c *Client) GetMachine(ctx context.Context, id string) (*Machine, error) {
	var out Machine
	return &out, c.do(ctx, opGetMachine, map[string]string{"id": id}, nil, nil, &out)
}

func (c *Client) UpdateMachine(ctx context.Context, id string, in UpdateMachineInput) (*Machine, error) {
	var out Machine
	return &out, c.do(ctx, opUpdateMachine, map[string]string{"id": id}, nil, in, &out)
}

func (c *Client) DeleteMachine(ctx context.Context, id string) error {
	return c.do(ctx, opDeleteMachine, map[string]string{"id": id}, nil, nil, nil)
}

func (c *Client) WakeMachine(ctx context.Context, id string, in WakeInput) (*WakeResult, error) {
	var out WakeResult
	return &out, c.do(ctx, opWakeMachine, map[string]string{"id": id}, nil, in, &out)
}

func (c *Client) ListWakes(ctx context.Context, id string) ([]WakeRequest, error) {
	var out []WakeRequest
	return out, c.do(ctx, opListWakes, map[string]string{"id": id}, nil, nil, &out)
}

// --- profiles ---------------------------------------------------------

func (c *Client) ListProfiles(ctx context.Context) ([]Profile, error) {
	var out []Profile
	return out, c.do(ctx, opListProfiles, nil, nil, nil, &out)
}

func (c *Client) CreateProfile(ctx context.Context, in CreateProfileInput) (*Profile, error) {
	var out Profile
	return &out, c.do(ctx, opCreateProfile, nil, nil, in, &out)
}

// GetProfile returns the profile plus its answer-file templates as a raw
// map (the detail shape is loose).
func (c *Client) GetProfile(ctx context.Context, id string) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, opGetProfile, map[string]string{"id": id}, nil, nil, &out)
}

func (c *Client) UpdateProfile(ctx context.Context, id string, patch map[string]any) error {
	return c.do(ctx, opUpdateProfile, map[string]string{"id": id}, nil, patch, nil)
}

func (c *Client) DeleteProfile(ctx context.Context, id string) error {
	return c.do(ctx, opDeleteProfile, map[string]string{"id": id}, nil, nil, nil)
}

// UpsertProfileTemplate sets the answer-file template of the given kind
// (autoinstall|kickstart|preseed|cloud-init|ignition|unattend).
func (c *Client) UpsertProfileTemplate(ctx context.Context, id, kind, body string) error {
	return c.do(ctx, opUpsertTemplate, map[string]string{"id": id}, nil,
		map[string]string{"kind": kind, "body": body}, nil)
}

func (c *Client) DeleteProfileTemplate(ctx context.Context, id, kind string) error {
	return c.do(ctx, opDeleteTemplate, map[string]string{"id": id, "kind": kind}, nil, nil, nil)
}

// --- images -----------------------------------------------------------

func (c *Client) ListImages(ctx context.Context) ([]Image, error) {
	var out []Image
	return out, c.do(ctx, opListImages, nil, nil, nil, &out)
}

func (c *Client) CreateImage(ctx context.Context, in CreateImageInput) (*Image, error) {
	var out Image
	return &out, c.do(ctx, opCreateImage, nil, nil, in, &out)
}

func (c *Client) GetImage(ctx context.Context, id string) (*Image, error) {
	var out Image
	return &out, c.do(ctx, opGetImage, map[string]string{"id": id}, nil, nil, &out)
}

func (c *Client) UpdateImage(ctx context.Context, id string, patch map[string]any) error {
	return c.do(ctx, opUpdateImage, map[string]string{"id": id}, nil, patch, nil)
}

func (c *Client) DeleteImage(ctx context.Context, id string) error {
	return c.do(ctx, opDeleteImage, map[string]string{"id": id}, nil, nil, nil)
}

func (c *Client) ListImageVersions(ctx context.Context, id string) ([]ImageVersion, error) {
	var out []ImageVersion
	return out, c.do(ctx, opListImageVersions, map[string]string{"id": id}, nil, nil, &out)
}

// CreateImageVersion links an uploaded blob as a new version of the image.
func (c *Client) CreateImageVersion(ctx context.Context, imageID, blobID, versionTag string) (map[string]string, error) {
	var out map[string]string
	body := map[string]string{"blob_id": blobID}
	if versionTag != "" {
		body["version_tag"] = versionTag
	}
	return out, c.do(ctx, opCreateImageVersion, map[string]string{"id": imageID}, nil, body, &out)
}

// CreateBlob registers a blob and returns a presigned upload URL.
func (c *Client) CreateBlob(ctx context.Context, in CreateBlobInput) (*BlobUpload, error) {
	var out BlobUpload
	return &out, c.do(ctx, opCreateBlob, nil, nil, in, &out)
}

// --- driver packs -----------------------------------------------------

func (c *Client) ListDriverPacks(ctx context.Context) ([]DriverPack, error) {
	var out []DriverPack
	return out, c.do(ctx, opListDriverPacks, nil, nil, nil, &out)
}

func (c *Client) CreateDriverPack(ctx context.Context, in CreateDriverPackInput) (map[string]string, error) {
	var out map[string]string
	return out, c.do(ctx, opCreateDriverPack, nil, nil, in, &out)
}

func (c *Client) DeleteDriverPackVersion(ctx context.Context, versionID string) error {
	return c.do(ctx, opDeleteDriverVersion, map[string]string{"id": versionID}, nil, nil, nil)
}

// --- vendor driver-pack catalogs --------------------------------------

// SearchVendorDriverPacks searches the vendor driver-pack catalogs by
// model name or machine type.
func (c *Client) SearchVendorDriverPacks(ctx context.Context, query string) ([]VendorPackEntry, error) {
	q := url.Values{}
	q.Set("q", query)
	var out []VendorPackEntry
	return out, c.do(ctx, opSearchVendorPacks, nil, q, nil, &out)
}

// FetchVendorDriverPack queues a server-side download+ingest of a pack.
// The URL must come from a SearchVendorDriverPacks result.
func (c *Client) FetchVendorDriverPack(ctx context.Context, url string) (map[string]string, error) {
	var out map[string]string
	return out, c.do(ctx, opFetchVendorPack, nil, nil, map[string]string{"url": url}, &out)
}

// ListVendorFetchJobs returns recent vendor fetch jobs, newest first.
func (c *Client) ListVendorFetchJobs(ctx context.Context, limit int) ([]VendorFetchJob, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var out []VendorFetchJob
	return out, c.do(ctx, opListVendorFetchJobs, nil, q, nil, &out)
}

// --- catalog ----------------------------------------------------------

// ListCatalog returns the distro net-install catalog as a raw map.
func (c *Client) ListCatalog(ctx context.Context) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, opListCatalog, nil, nil, nil, &out)
}

func (c *Client) InstallFromCatalog(ctx context.Context, entryID, name string) (map[string]any, error) {
	var out map[string]any
	body := map[string]string{"entry_id": entryID}
	if name != "" {
		body["name"] = name
	}
	return out, c.do(ctx, opInstallCatalog, nil, nil, body, &out)
}

// --- jobs -------------------------------------------------------------

func (c *Client) ListJobs(ctx context.Context, f JobFilter) ([]Job, error) {
	q := url.Values{}
	if f.State != "" {
		q.Set("state", f.State)
	}
	if f.MachineID != "" {
		q.Set("machine", f.MachineID)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	var out []Job
	return out, c.do(ctx, opListJobs, nil, q, nil, &out)
}

func (c *Client) GetJob(ctx context.Context, id string) (*JobDetail, error) {
	var out JobDetail
	return &out, c.do(ctx, opGetJob, map[string]string{"id": id}, nil, nil, &out)
}

func (c *Client) CancelJob(ctx context.Context, id string) error {
	return c.do(ctx, opCancelJob, map[string]string{"id": id}, nil, nil, nil)
}

// --- deployments ------------------------------------------------------

func (c *Client) IssueDeployment(ctx context.Context, in IssueDeploymentInput) (*IssueResult, error) {
	var out IssueResult
	return &out, c.do(ctx, opIssueDeployment, nil, nil, in, &out)
}

func (c *Client) BulkDeploy(ctx context.Context, in BulkDeployInput) ([]BulkResult, error) {
	var wrap struct {
		Results []BulkResult `json:"results"`
	}
	return wrap.Results, c.do(ctx, opBulkDeploy, nil, nil, in, &wrap)
}

// --- reports ----------------------------------------------------------

func (c *Client) ReportSummary(ctx context.Context, since string) (*ReportSummary, error) {
	var out ReportSummary
	return &out, c.do(ctx, opReportSummary, nil, sinceQuery(since), nil, &out)
}

func (c *Client) ReportDaily(ctx context.Context, days int) ([]ReportDay, error) {
	q := url.Values{}
	if days > 0 {
		q.Set("days", strconv.Itoa(days))
	}
	var out []ReportDay
	return out, c.do(ctx, opReportDaily, nil, q, nil, &out)
}

func (c *Client) ReportByProfile(ctx context.Context, since string) ([]ReportGroup, error) {
	var out []ReportGroup
	return out, c.do(ctx, opReportByProfile, nil, sinceQuery(since), nil, &out)
}

func (c *Client) ReportBySite(ctx context.Context, since string) ([]ReportGroup, error) {
	var out []ReportGroup
	return out, c.do(ctx, opReportBySite, nil, sinceQuery(since), nil, &out)
}

// ReportJobsCSV returns the raw CSV export bytes.
func (c *Client) ReportJobsCSV(ctx context.Context, since string) ([]byte, error) {
	return c.doRaw(ctx, opReportJobsCSV, nil, sinceQuery(since), nil)
}

// --- audit ------------------------------------------------------------

func (c *Client) QueryAudit(ctx context.Context, since, actionPrefix string) ([]AuditEvent, error) {
	q := url.Values{}
	if since != "" {
		q.Set("since", since)
	}
	if actionPrefix != "" {
		q.Set("action", actionPrefix)
	}
	var out []AuditEvent
	return out, c.do(ctx, opQueryAudit, nil, q, nil, &out)
}

// --- users & roles ----------------------------------------------------

func (c *Client) ListUsers(ctx context.Context) ([]User, error) {
	var out []User
	return out, c.do(ctx, opListUsers, nil, nil, nil, &out)
}

func (c *Client) ListRoles(ctx context.Context) ([]Role, error) {
	var out []Role
	return out, c.do(ctx, opListRoles, nil, nil, nil, &out)
}

func (c *Client) GrantRole(ctx context.Context, userID, role string) error {
	return c.do(ctx, opGrantRole, map[string]string{"id": userID}, nil,
		map[string]string{"role": role}, nil)
}

func (c *Client) RevokeRole(ctx context.Context, userID, role string) error {
	return c.do(ctx, opRevokeRole, map[string]string{"id": userID, "role": role}, nil, nil, nil)
}

// --- sites ------------------------------------------------------------

func (c *Client) ListSites(ctx context.Context) ([]Site, error) {
	var out []Site
	return out, c.do(ctx, opListSites, nil, nil, nil, &out)
}

func (c *Client) UpsertSite(ctx context.Context, in SiteInput) (*Site, error) {
	var out Site
	return &out, c.do(ctx, opUpsertSite, nil, nil, in, &out)
}

func (c *Client) DeleteSite(ctx context.Context, name string) error {
	return c.do(ctx, opDeleteSite, map[string]string{"name": name}, nil, nil, nil)
}

// --- bootstrap sticks -------------------------------------------------

func (c *Client) ListSticks(ctx context.Context) ([]map[string]any, error) {
	var out []map[string]any
	return out, c.do(ctx, opListSticks, nil, nil, nil, &out)
}

func (c *Client) RegisterStick(ctx context.Context, in RegisterStickInput) (map[string]any, error) {
	var out map[string]any
	return out, c.do(ctx, opRegisterStick, nil, nil, in, &out)
}

func (c *Client) StickConfig(ctx context.Context, tailnet string) (*StickConfig, error) {
	q := url.Values{}
	if tailnet != "" {
		q.Set("tailnet", tailnet)
	}
	var out StickConfig
	return &out, c.do(ctx, opStickConfig, nil, q, nil, &out)
}

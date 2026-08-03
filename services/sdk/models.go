package sdk

import (
	"encoding/json"
	"time"
)

// AuthConfig is the public pre-auth OIDC discovery result.
type AuthConfig struct {
	Issuer   string `json:"issuer"`
	ClientID string `json:"client_id"`
	DevMode  bool   `json:"dev_mode"`
}

// Me is the authenticated principal.
type Me struct {
	UserID  string `json:"user_id"`
	DevMode bool   `json:"dev_mode"`
}

// Machine mirrors the server's machine JSON. Note the field keys are
// Go-style (ID, AssetTag, …) because the server serializes the struct
// without json tags — the SDK matches the wire format faithfully.
type Machine struct {
	ID               string          `json:"ID"`
	AssetTag         *string         `json:"AssetTag"`
	MACPrimary       *string         `json:"MACPrimary"`
	UUIDSMBIOS       *string         `json:"UUIDSMBIOS"`
	Vendor           *string         `json:"Vendor"`
	Model            *string         `json:"Model"`
	DefaultProfileID *string         `json:"DefaultProfileID"`
	Attributes       json.RawMessage `json:"Attributes"`
	CreatedAt        time.Time       `json:"CreatedAt"`
}

type CreateMachineInput struct {
	AssetTag         *string        `json:"asset_tag,omitempty"`
	MACPrimary       *string        `json:"mac_primary,omitempty"`
	UUIDSMBIOS       *string        `json:"uuid_smbios,omitempty"`
	Vendor           *string        `json:"vendor,omitempty"`
	Model            *string        `json:"model,omitempty"`
	DefaultProfileID *string        `json:"default_profile_id,omitempty"`
	Attributes       map[string]any `json:"attributes,omitempty"`
}

type UpdateMachineInput struct {
	AssetTag         *string        `json:"asset_tag,omitempty"`
	MACPrimary       *string        `json:"mac_primary,omitempty"`
	Vendor           *string        `json:"vendor,omitempty"`
	Model            *string        `json:"model,omitempty"`
	DefaultProfileID *string        `json:"default_profile_id,omitempty"` // "" clears it
	Attributes       map[string]any `json:"attributes,omitempty"`
}

type WakeInput struct {
	At   string `json:"at,omitempty"` // RFC3339; empty = next agent poll
	Site string `json:"site,omitempty"`
}

type WakeResult struct {
	WakeID string `json:"wake_id"`
	Site   string `json:"site"`
}

type WakeRequest struct {
	ID          string     `json:"id"`
	MachineID   string     `json:"machine_id"`
	MAC         string     `json:"mac"`
	Site        string     `json:"site"`
	ScheduledAt time.Time  `json:"scheduled_at"`
	ClaimedAt   *time.Time `json:"claimed_at"`
	ClaimedBy   *string    `json:"claimed_by"`
}

type Profile struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ImageID   string    `json:"image_id"`
	ImageName string    `json:"image_name"`
	OSFamily  string    `json:"os_family"`
	OSVersion string    `json:"os_version"`
	CreatedAt time.Time `json:"created_at"`
}

type CreateProfileInput struct {
	Name           string         `json:"name"`
	ImageID        string         `json:"image_id"`
	AnswerFileVars map[string]any `json:"answer_file_vars,omitempty"`
}

type ImageMedia struct {
	KernelURL    string `json:"kernel_url,omitempty"`
	InitrdURL    string `json:"initrd_url,omitempty"`
	WimbootURL   string `json:"wimboot_url,omitempty"`
	BootWimURL   string `json:"bootwim_url,omitempty"`
	WimURL       string `json:"wim_url,omitempty"`
	DeployMethod string `json:"deploy_method,omitempty"`
}

type Image struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	OSFamily      string          `json:"os_family"`
	OSVersion     string          `json:"os_version"`
	Arch          string          `json:"arch"`
	Description   *string         `json:"description"`
	VersionsCount int             `json:"versions_count"`
	Media         json.RawMessage `json:"media"`
	CreatedAt     time.Time       `json:"created_at"`
}

type CreateImageInput struct {
	Name        string      `json:"name"`
	OSFamily    string      `json:"os_family"`
	OSVersion   string      `json:"os_version"`
	Arch        string      `json:"arch"`
	Description *string     `json:"description,omitempty"`
	Media       *ImageMedia `json:"media,omitempty"`
}

type ImageVersion struct {
	ID         string    `json:"id"`
	ImageID    string    `json:"image_id"`
	VersionTag string    `json:"version_tag"`
	BlobID     string    `json:"blob_id"`
	BlobKey    string    `json:"blob_key"`
	BlobSHA256 string    `json:"blob_sha256"`
	SizeBytes  int64     `json:"size_bytes"`
	CreatedAt  time.Time `json:"created_at"`
}

type CreateBlobInput struct {
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
	Filename  string `json:"filename,omitempty"`
	Role      string `json:"role,omitempty"` // images (default) | drivers | blobs
}

type BlobUpload struct {
	BlobID           string `json:"blob_id"`
	Bucket           string `json:"bucket"`
	Key              string `json:"key"`
	UploadURL        string `json:"upload_url"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

type DriverRule struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type DriverPack struct {
	PackID     string       `json:"pack_id"`
	Vendor     string       `json:"vendor"`
	Model      string       `json:"model"`
	OSFamily   string       `json:"os_family"`
	OSVersion  string       `json:"os_version"`
	VersionID  string       `json:"version_id"`
	VersionTag string       `json:"version_tag"`
	BlobSHA256 string       `json:"blob_sha256"`
	SizeBytes  int64        `json:"size_bytes"`
	Rules      []DriverRule `json:"rules"`
	CreatedAt  time.Time    `json:"created_at"`
}

type CreateDriverPackInput struct {
	Vendor     string       `json:"vendor"`
	Model      string       `json:"model"`
	OSFamily   string       `json:"os_family"`
	OSVersion  string       `json:"os_version,omitempty"`
	VersionTag string       `json:"version_tag,omitempty"`
	BlobID     string       `json:"blob_id"`
	Rules      []DriverRule `json:"rules"`
}

type Job struct {
	ID          string     `json:"id"`
	MachineID   string     `json:"machine_id"`
	MachineTag  *string    `json:"machine_asset_tag"`
	ProfileID   string     `json:"profile_id"`
	ProfileName string     `json:"profile_name"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
}

type JobEvent struct {
	ID      int64     `json:"id"`
	Phase   string    `json:"phase"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

type JobDetail struct {
	Job    Job        `json:"job"`
	Events []JobEvent `json:"events"`
}

// JobFilter narrows ListJobs.
type JobFilter struct {
	State     string
	MachineID string
	Limit     int
}

type IssueDeploymentInput struct {
	MachineID         string `json:"machine_id"`
	ProfileID         string `json:"profile_id"`
	TTLSeconds        int    `json:"ttl_seconds,omitempty"`
	IssuedFor         string `json:"issued_for,omitempty"`
	BindingCIDR       string `json:"binding_cidr,omitempty"`
	Kind              string `json:"kind,omitempty"`
	CaptureImageID    string `json:"capture_image_id,omitempty"`
	CaptureVersionTag string `json:"capture_version_tag,omitempty"`
}

type IssueResult struct {
	Code      string    `json:"code"`
	ExpiresAt time.Time `json:"expires_at"`
}

type BulkDeployInput struct {
	MachineIDs []string `json:"machine_ids"`
	ProfileID  string   `json:"profile_id"`
	TTLSeconds int      `json:"ttl_seconds,omitempty"`
	IssuedFor  string   `json:"issued_for,omitempty"`
	Wake       bool     `json:"wake,omitempty"`
}

type BulkResult struct {
	MachineID string `json:"machine_id"`
	Code      string `json:"code,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	WakeID    string `json:"wake_id,omitempty"`
	Error     string `json:"error,omitempty"`
}

type ReportSummary struct {
	Window          string  `json:"window"`
	Total           int     `json:"total"`
	Completed       int     `json:"completed"`
	Failed          int     `json:"failed"`
	Cancelled       int     `json:"cancelled"`
	Active          int     `json:"active"`
	Captures        int     `json:"captures"`
	SuccessRate     float64 `json:"success_rate"`
	AvgDurationSecs float64 `json:"avg_duration_secs"`
	MachinesTotal   int     `json:"machines_total"`
	ImagesTotal     int     `json:"images_total"`
}

type ReportDay struct {
	Day       string `json:"day"`
	Completed int    `json:"completed"`
	Failed    int    `json:"failed"`
}

type ReportGroup struct {
	Name            string  `json:"name"`
	Total           int     `json:"total"`
	Completed       int     `json:"completed"`
	Failed          int     `json:"failed"`
	AvgDurationSecs float64 `json:"avg_duration_secs"`
}

type User struct {
	ID         string    `json:"id"`
	Email      string    `json:"email"`
	OIDCLinked bool      `json:"oidc_linked"`
	Roles      []string  `json:"roles"`
	CreatedAt  time.Time `json:"created_at"`
}

type Role struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
}

type Site struct {
	Name          string    `json:"name"`
	MirrorBaseURL *string   `json:"mirror_base_url"`
	Description   *string   `json:"description"`
	CreatedAt     time.Time `json:"created_at"`
}

type SiteInput struct {
	Name          string  `json:"name"`
	MirrorBaseURL *string `json:"mirror_base_url"`
	Description   *string `json:"description"`
}

type RegisterStickInput struct {
	ImageSHA256   string  `json:"image_sha256"`
	Tailnet       string  `json:"tailnet"`
	DeployURL     string  `json:"deploy_url"`
	CAFingerprint string  `json:"ca_fingerprint"`
	Label         *string `json:"label,omitempty"`
}

type StickConfig struct {
	ConfigJSON       string `json:"config_json"`
	CAPEM            string `json:"ca_pem"`
	MakeStickCommand string `json:"make_stick_command"`
	DeployURL        string `json:"deploy_url"`
	ControlURL       string `json:"control_url"`
	Tailnet          string `json:"tailnet"`
}

// AuditEvent is intentionally loose — the audit payload's `data` field
// varies by action.
type AuditEvent struct {
	Action      string          `json:"action"`
	ActorID     *string         `json:"actor_id"`
	ActorKind   string          `json:"actor_kind"`
	SubjectID   *string         `json:"subject_id"`
	SubjectKind string          `json:"subject_kind"`
	SourceIP    string          `json:"source_ip"`
	Data        json.RawMessage `json:"data"`
	At          time.Time       `json:"at"`
}

// VendorPackEntry is one downloadable pack from a vendor catalog.
type VendorPackEntry struct {
	Vendor    string   `json:"vendor"`
	Model     string   `json:"model"`
	Types     []string `json:"types"`
	OSFamily  string   `json:"os_family"`
	OSVersion string   `json:"os_version"`
	URL       string   `json:"url"`
	SHA256    string   `json:"sha256"`
	Date      string   `json:"date"`
}

// VendorFetchJob is a queued/running/finished vendor pack download.
type VendorFetchJob struct {
	ID            string   `json:"id"`
	Vendor        string   `json:"vendor"`
	Model         string   `json:"model"`
	MTypes        []string `json:"mtypes"`
	OSFamily      string   `json:"os_family"`
	OSVersion     string   `json:"os_version"`
	URL           string   `json:"url"`
	State         string   `json:"state"`
	Error         *string  `json:"error"`
	PackVersionID *string  `json:"pack_version_id"`
	SizeBytes     *int64   `json:"size_bytes"`
	CreatedAt     string   `json:"created_at"`
	StartedAt     *string  `json:"started_at"`
	FinishedAt    *string  `json:"finished_at"`
}

// ProfilePreview is the result of rendering a profile template.
type ProfilePreview struct {
	Rendered  string `json:"rendered"`
	YAMLValid bool   `json:"yaml_valid"`
	YAMLError string `json:"yaml_error"`
	Fallback  bool   `json:"fallback"`
}

// APIToken is a long-lived personal access token. The secret is never
// returned after creation — only its display prefix.
type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	UserID     string     `json:"user_id"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}

type CreateAPITokenInput struct {
	Name string `json:"name"`
	// ExpiresInDays, when set, makes the token expire; omit for no expiry.
	ExpiresInDays *int `json:"expires_in_days,omitempty"`
}

// CreatedAPIToken is the create response: the stored record plus the
// plaintext secret, which is returned exactly once.
type CreatedAPIToken struct {
	APIToken
	Token string `json:"token"`
}

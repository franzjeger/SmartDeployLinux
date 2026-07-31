// Types mirroring the deployserver operator API wire format. They match
// the Go SDK's models one-for-one. Note the differing key styles: the
// server serializes its Machine struct without JSON tags, so Machine uses
// Go-style keys (ID, AssetTag, …); every other payload uses snake_case.
//
// Timestamps arrive as RFC 3339 strings and are kept as `string` here —
// the SDK does not silently reinterpret the wire format.

export interface AuthConfig {
  issuer: string;
  client_id: string;
  dev_mode: boolean;
}

export interface Me {
  user_id: string;
  dev_mode: boolean;
}

export interface Machine {
  ID: string;
  AssetTag: string | null;
  MACPrimary: string | null;
  UUIDSMBIOS: string | null;
  Vendor: string | null;
  Model: string | null;
  DefaultProfileID: string | null;
  Attributes: unknown;
  CreatedAt: string;
}

export interface CreateMachineInput {
  asset_tag?: string;
  mac_primary?: string;
  uuid_smbios?: string;
  vendor?: string;
  model?: string;
  default_profile_id?: string;
  attributes?: Record<string, unknown>;
}

export interface UpdateMachineInput {
  asset_tag?: string;
  mac_primary?: string;
  vendor?: string;
  model?: string;
  /** "" clears the default profile. */
  default_profile_id?: string;
  attributes?: Record<string, unknown>;
}

export interface WakeInput {
  /** RFC 3339; empty = next agent poll. */
  at?: string;
  site?: string;
}

export interface WakeResult {
  wake_id: string;
  site: string;
}

export interface WakeRequest {
  id: string;
  machine_id: string;
  mac: string;
  site: string;
  scheduled_at: string;
  claimed_at: string | null;
  claimed_by: string | null;
}

export interface Profile {
  id: string;
  name: string;
  image_id: string;
  image_name: string;
  os_family: string;
  os_version: string;
  created_at: string;
}

export interface CreateProfileInput {
  name: string;
  image_id: string;
  answer_file_vars?: Record<string, unknown>;
}

export interface ImageMedia {
  kernel_url?: string;
  initrd_url?: string;
  wimboot_url?: string;
  bootwim_url?: string;
  wim_url?: string;
  deploy_method?: string;
}

export interface Image {
  id: string;
  name: string;
  os_family: string;
  os_version: string;
  arch: string;
  description: string | null;
  versions_count: number;
  media: unknown;
  created_at: string;
}

export interface CreateImageInput {
  name: string;
  os_family: string;
  os_version: string;
  arch: string;
  description?: string;
  media?: ImageMedia;
}

export interface ImageVersion {
  id: string;
  image_id: string;
  version_tag: string;
  blob_id: string;
  blob_key: string;
  blob_sha256: string;
  size_bytes: number;
  created_at: string;
}

export interface CreateBlobInput {
  sha256: string;
  size_bytes: number;
  filename?: string;
  /** images (default) | drivers | blobs */
  role?: string;
}

export interface BlobUpload {
  blob_id: string;
  bucket: string;
  key: string;
  upload_url: string;
  expires_in_seconds: number;
}

export interface DriverRule {
  type: string;
  value: string;
}

export interface DriverPack {
  pack_id: string;
  vendor: string;
  model: string;
  os_family: string;
  os_version: string;
  version_id: string;
  version_tag: string;
  blob_sha256: string;
  size_bytes: number;
  rules: DriverRule[];
  created_at: string;
}

export interface CreateDriverPackInput {
  vendor: string;
  model: string;
  os_family: string;
  os_version?: string;
  version_tag?: string;
  blob_id: string;
  rules: DriverRule[];
}

export interface Job {
  id: string;
  machine_id: string;
  machine_asset_tag: string | null;
  profile_id: string;
  profile_name: string;
  state: string;
  created_at: string;
  started_at: string | null;
  finished_at: string | null;
}

export interface JobEvent {
  id: number;
  phase: string;
  message: string;
  at: string;
}

export interface JobDetail {
  job: Job;
  events: JobEvent[];
}

/** JobFilter narrows listJobs. */
export interface JobFilter {
  state?: string;
  machineId?: string;
  limit?: number;
}

export interface IssueDeploymentInput {
  machine_id: string;
  profile_id: string;
  ttl_seconds?: number;
  issued_for?: string;
  binding_cidr?: string;
  kind?: string;
  capture_image_id?: string;
  capture_version_tag?: string;
}

export interface IssueResult {
  code: string;
  expires_at: string;
}

export interface BulkDeployInput {
  machine_ids: string[];
  profile_id: string;
  ttl_seconds?: number;
  issued_for?: string;
  wake?: boolean;
}

export interface BulkResult {
  machine_id: string;
  code?: string;
  expires_at?: string;
  wake_id?: string;
  error?: string;
}

export interface ReportSummary {
  window: string;
  total: number;
  completed: number;
  failed: number;
  cancelled: number;
  active: number;
  captures: number;
  success_rate: number;
  avg_duration_secs: number;
  machines_total: number;
  images_total: number;
}

export interface ReportDay {
  day: string;
  completed: number;
  failed: number;
}

export interface ReportGroup {
  name: string;
  total: number;
  completed: number;
  failed: number;
  avg_duration_secs: number;
}

export interface User {
  id: string;
  email: string;
  oidc_linked: boolean;
  roles: string[];
  created_at: string;
}

export interface Role {
  name: string;
  permissions: string[];
}

export interface Site {
  name: string;
  mirror_base_url: string | null;
  description: string | null;
  created_at: string;
}

export interface SiteInput {
  name: string;
  mirror_base_url: string | null;
  description: string | null;
}

export interface RegisterStickInput {
  image_sha256: string;
  tailnet: string;
  deploy_url: string;
  ca_fingerprint: string;
  label?: string;
}

export interface StickConfig {
  config_json: string;
  ca_pem: string;
  make_stick_command: string;
  deploy_url: string;
  control_url: string;
  tailnet: string;
}

/** AuditEvent is intentionally loose — `data` varies by action. */
export interface AuditEvent {
  action: string;
  actor_id: string | null;
  actor_kind: string;
  subject_id: string | null;
  subject_kind: string;
  source_ip: string;
  data: unknown;
  at: string;
}

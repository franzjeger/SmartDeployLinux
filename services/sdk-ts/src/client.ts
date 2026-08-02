import { OPERATIONS, type Operation } from "./operations.js";
import type {
  AuditEvent,
  AuthConfig,
  BlobUpload,
  BulkDeployInput,
  BulkResult,
  CreateBlobInput,
  CreateDriverPackInput,
  CreateImageInput,
  CreateMachineInput,
  CreateProfileInput,
  DriverPack,
  Image,
  ImageVersion,
  IssueDeploymentInput,
  IssueResult,
  Job,
  JobDetail,
  JobFilter,
  Machine,
  Me,
  Profile,
  RegisterStickInput,
  ReportDay,
  ReportGroup,
  ReportSummary,
  Role,
  Site,
  SiteInput,
  StickConfig,
  UpdateMachineInput,
  User,
  WakeInput,
  WakeRequest,
  WakeResult,
  ProfilePreview,
  VendorPackEntry,
  VendorFetchJob,
} from "./models.js";

/** Options configures a {@link DeployClient}. */
export interface Options {
  /** Base URL of the server, e.g. https://deploy.example.com. Required. */
  baseUrl: string;
  /** OIDC ID token (from `deployctl auth login`) or a service bearer. */
  token?: string;
  /**
   * Custom fetch implementation (for tests, proxies, or older runtimes).
   * Defaults to the global `fetch` (Node 18+ and all modern browsers).
   */
  fetch?: typeof fetch;
}

/**
 * ApiError carries a non-2xx response. `message` is the server's error
 * body (or its `error` JSON field) so failures are actionable.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly method: string;
  readonly path: string;

  constructor(status: number, method: string, path: string, message: string) {
    super(`deployserver ${method} ${path}: ${status}: ${message}`);
    this.name = "ApiError";
    this.status = status;
    this.method = method;
    this.path = path;
  }
}

/** True when `err` is a 404 ApiError. */
export function isNotFound(err: unknown): boolean {
  return err instanceof ApiError && err.status === 404;
}

/** True when `err` is a 403 (missing RBAC permission). */
export function isForbidden(err: unknown): boolean {
  return err instanceof ApiError && err.status === 403;
}

type PathParams = Record<string, string>;
type QueryValue = string | number | undefined;
type Query = Record<string, QueryValue>;

interface RequestOpts {
  params?: PathParams;
  query?: Query;
  body?: unknown;
}

/**
 * DeployClient is the typed client for the deployserver operator API. Its
 * method surface is kept in exact correspondence with the OpenAPI spec by
 * the parity test.
 *
 * ```ts
 * const c = new DeployClient({ baseUrl: "https://deploy.example.com", token });
 * const machines = await c.listMachines();
 * ```
 */
export class DeployClient {
  readonly #baseUrl: string;
  readonly #token: string | undefined;
  readonly #fetch: typeof fetch;

  constructor(options: Options) {
    if (!options.baseUrl) {
      throw new Error("sdk: baseUrl required");
    }
    this.#baseUrl = options.baseUrl.replace(/\/+$/, "");
    this.#token = options.token;
    // Bind the global fetch to globalThis so it works when stored on the
    // instance (browsers throw "Illegal invocation" for a detached fetch).
    this.#fetch = options.fetch ?? globalThis.fetch.bind(globalThis);
  }

  // --- transport ------------------------------------------------------

  #fill(path: string, params: PathParams | undefined): string {
    if (!params) return path;
    let out = path;
    for (const [k, v] of Object.entries(params)) {
      out = out.replaceAll(`{${k}}`, encodeURIComponent(v));
    }
    return out;
  }

  async #raw(op: Operation, opts: RequestOpts): Promise<{ status: number; body: string }> {
    const path = this.#fill(op.path, opts.params);
    let url = this.#baseUrl + path;
    if (opts.query) {
      const usp = new URLSearchParams();
      for (const [k, v] of Object.entries(opts.query)) {
        if (v !== undefined && v !== "") usp.set(k, String(v));
      }
      const qs = usp.toString();
      if (qs) url += `?${qs}`;
    }

    const headers: Record<string, string> = { Accept: "application/json" };
    let payload: string | undefined;
    if (opts.body !== undefined) {
      headers["Content-Type"] = "application/json";
      payload = JSON.stringify(opts.body);
    }
    if (this.#token) headers["Authorization"] = `Bearer ${this.#token}`;

    let resp: Response;
    try {
      resp = await this.#fetch(url, { method: op.method, headers, body: payload });
    } catch (cause) {
      throw new Error(`sdk: ${op.method} ${path}: ${(cause as Error).message}`, { cause });
    }
    const text = await resp.text();
    if (!resp.ok) {
      throw new ApiError(resp.status, op.method, path, errorMessage(text));
    }
    return { status: resp.status, body: text };
  }

  async #json<T>(op: Operation, opts: RequestOpts = {}): Promise<T> {
    const { body } = await this.#raw(op, opts);
    return (body ? JSON.parse(body) : undefined) as T;
  }

  async #text(op: Operation, opts: RequestOpts = {}): Promise<string> {
    const { body } = await this.#raw(op, opts);
    return body;
  }

  async #void(op: Operation, opts: RequestOpts = {}): Promise<void> {
    await this.#raw(op, opts);
  }

  // --- identity -------------------------------------------------------

  /** Public pre-auth OIDC discovery info (no auth required). */
  authConfig(): Promise<AuthConfig> {
    return this.#json(OPERATIONS.authConfig);
  }

  /** The authenticated principal. */
  me(): Promise<Me> {
    return this.#json(OPERATIONS.me);
  }

  // --- machines -------------------------------------------------------

  listMachines(): Promise<Machine[]> {
    return this.#json(OPERATIONS.listMachines);
  }

  createMachine(input: CreateMachineInput): Promise<Machine> {
    return this.#json(OPERATIONS.createMachine, { body: input });
  }

  getMachine(id: string): Promise<Machine> {
    return this.#json(OPERATIONS.getMachine, { params: { id } });
  }

  updateMachine(id: string, input: UpdateMachineInput): Promise<Machine> {
    return this.#json(OPERATIONS.updateMachine, { params: { id }, body: input });
  }

  deleteMachine(id: string): Promise<void> {
    return this.#void(OPERATIONS.deleteMachine, { params: { id } });
  }

  wakeMachine(id: string, input: WakeInput = {}): Promise<WakeResult> {
    return this.#json(OPERATIONS.wakeMachine, { params: { id }, body: input });
  }

  listWakes(id: string): Promise<WakeRequest[]> {
    return this.#json(OPERATIONS.listWakes, { params: { id } });
  }

  // --- profiles -------------------------------------------------------

  listProfiles(): Promise<Profile[]> {
    return this.#json(OPERATIONS.listProfiles);
  }

  createProfile(input: CreateProfileInput): Promise<Profile> {
    return this.#json(OPERATIONS.createProfile, { body: input });
  }

  /** The profile plus its answer-file templates, as a loose object. */
  getProfile(id: string): Promise<Record<string, unknown>> {
    return this.#json(OPERATIONS.getProfile, { params: { id } });
  }

  updateProfile(id: string, patch: Record<string, unknown>): Promise<void> {
    return this.#void(OPERATIONS.updateProfile, { params: { id }, body: patch });
  }

  deleteProfile(id: string): Promise<void> {
    return this.#void(OPERATIONS.deleteProfile, { params: { id } });
  }

  /**
   * Set the answer-file template of the given kind
   * (autoinstall|kickstart|preseed|cloud-init|ignition|unattend).
   */
  upsertProfileTemplate(id: string, kind: string, body: string): Promise<void> {
    return this.#void(OPERATIONS.upsertProfileTemplate, { params: { id }, body: { kind, body } });
  }

  deleteProfileTemplate(id: string, kind: string): Promise<void> {
    return this.#void(OPERATIONS.deleteProfileTemplate, { params: { id, kind } });
  }

  // --- images ---------------------------------------------------------

  listImages(): Promise<Image[]> {
    return this.#json(OPERATIONS.listImages);
  }

  createImage(input: CreateImageInput): Promise<Image> {
    return this.#json(OPERATIONS.createImage, { body: input });
  }

  getImage(id: string): Promise<Image> {
    return this.#json(OPERATIONS.getImage, { params: { id } });
  }

  updateImage(id: string, patch: Record<string, unknown>): Promise<void> {
    return this.#void(OPERATIONS.updateImage, { params: { id }, body: patch });
  }

  deleteImage(id: string): Promise<void> {
    return this.#void(OPERATIONS.deleteImage, { params: { id } });
  }

  listImageVersions(id: string): Promise<ImageVersion[]> {
    return this.#json(OPERATIONS.listImageVersions, { params: { id } });
  }

  /** Link an uploaded blob as a new version of the image. */
  createImageVersion(imageId: string, blobId: string, versionTag?: string): Promise<Record<string, string>> {
    const body: Record<string, string> = { blob_id: blobId };
    if (versionTag) body["version_tag"] = versionTag;
    return this.#json(OPERATIONS.createImageVersion, { params: { id: imageId }, body });
  }

  /** Register a blob and get back a presigned upload URL. */
  createBlob(input: CreateBlobInput): Promise<BlobUpload> {
    return this.#json(OPERATIONS.createBlob, { body: input });
  }

  // --- driver packs ---------------------------------------------------

  listDriverPacks(): Promise<DriverPack[]> {
    return this.#json(OPERATIONS.listDriverPacks);
  }

  createDriverPack(input: CreateDriverPackInput): Promise<Record<string, string>> {
    return this.#json(OPERATIONS.createDriverPack, { body: input });
  }

  deleteDriverPackVersion(versionId: string): Promise<void> {
    return this.#void(OPERATIONS.deleteDriverPackVersion, { params: { id: versionId } });
  }

  /** Render a profile template with a synthetic machine and check YAML validity. */
  previewProfileTemplate(profileId: string, kind: "autoinstall" | "cloud-init", body?: string): Promise<ProfilePreview> {
    return this.#json(OPERATIONS.previewProfileTemplate, {
      params: { id: profileId },
      body: body === undefined ? { kind } : { kind, body },
    });
  }

  // --- vendor driver-pack catalogs ------------------------------------

  /** Search the vendor driver-pack catalogs by model name or machine type. */
  searchVendorDriverPacks(query: string): Promise<VendorPackEntry[]> {
    return this.#json(OPERATIONS.searchVendorDriverPacks, { query: { q: query } });
  }

  /** Queue a server-side download+ingest. `url` must come from a search result. */
  fetchVendorDriverPack(url: string): Promise<Record<string, string>> {
    return this.#json(OPERATIONS.fetchVendorDriverPack, { body: { url } });
  }

  /** Recent vendor fetch jobs, newest first. */
  listVendorFetchJobs(limit?: number): Promise<VendorFetchJob[]> {
    return this.#json(OPERATIONS.listVendorFetchJobs, {
      query: limit ? { limit: String(limit) } : undefined,
    });
  }

  // --- catalog --------------------------------------------------------

  /** The distro net-install catalog, as a loose object. */
  listCatalog(): Promise<Record<string, unknown>> {
    return this.#json(OPERATIONS.listCatalog);
  }

  installFromCatalog(entryId: string, name?: string): Promise<Record<string, unknown>> {
    const body: Record<string, string> = { entry_id: entryId };
    if (name) body["name"] = name;
    return this.#json(OPERATIONS.installFromCatalog, { body });
  }

  // --- jobs -----------------------------------------------------------

  listJobs(filter: JobFilter = {}): Promise<Job[]> {
    return this.#json(OPERATIONS.listJobs, {
      query: { state: filter.state, machine: filter.machineId, limit: filter.limit },
    });
  }

  getJob(id: string): Promise<JobDetail> {
    return this.#json(OPERATIONS.getJob, { params: { id } });
  }

  cancelJob(id: string): Promise<void> {
    return this.#void(OPERATIONS.cancelJob, { params: { id } });
  }

  // --- audit ----------------------------------------------------------

  queryAudit(opts: { since?: string; action?: string } = {}): Promise<AuditEvent[]> {
    return this.#json(OPERATIONS.queryAudit, { query: { since: opts.since, action: opts.action } });
  }

  // --- reports --------------------------------------------------------

  reportSummary(since?: string): Promise<ReportSummary> {
    return this.#json(OPERATIONS.reportSummary, { query: { since } });
  }

  reportDaily(days?: number): Promise<ReportDay[]> {
    return this.#json(OPERATIONS.reportDaily, { query: { days } });
  }

  reportByProfile(since?: string): Promise<ReportGroup[]> {
    return this.#json(OPERATIONS.reportByProfile, { query: { since } });
  }

  reportBySite(since?: string): Promise<ReportGroup[]> {
    return this.#json(OPERATIONS.reportBySite, { query: { since } });
  }

  /** The raw CSV export text. */
  reportJobsCSV(since?: string): Promise<string> {
    return this.#text(OPERATIONS.reportJobsCSV, { query: { since } });
  }

  // --- deployments ----------------------------------------------------

  issueDeployment(input: IssueDeploymentInput): Promise<IssueResult> {
    return this.#json(OPERATIONS.issueDeployment, { body: input });
  }

  async bulkDeploy(input: BulkDeployInput): Promise<BulkResult[]> {
    const wrap = await this.#json<{ results?: BulkResult[] }>(OPERATIONS.bulkDeploy, { body: input });
    return wrap.results ?? [];
  }

  // --- users & roles --------------------------------------------------

  listUsers(): Promise<User[]> {
    return this.#json(OPERATIONS.listUsers);
  }

  listRoles(): Promise<Role[]> {
    return this.#json(OPERATIONS.listRoles);
  }

  grantRole(userId: string, role: string): Promise<void> {
    return this.#void(OPERATIONS.grantUserRole, { params: { id: userId }, body: { role } });
  }

  revokeRole(userId: string, role: string): Promise<void> {
    return this.#void(OPERATIONS.revokeUserRole, { params: { id: userId, role } });
  }

  // --- sites ----------------------------------------------------------

  listSites(): Promise<Site[]> {
    return this.#json(OPERATIONS.listSites);
  }

  upsertSite(input: SiteInput): Promise<Site> {
    return this.#json(OPERATIONS.upsertSite, { body: input });
  }

  deleteSite(name: string): Promise<void> {
    return this.#void(OPERATIONS.deleteSite, { params: { name } });
  }

  // --- bootstrap sticks -----------------------------------------------

  listSticks(): Promise<Array<Record<string, unknown>>> {
    return this.#json(OPERATIONS.listSticks);
  }

  registerStick(input: RegisterStickInput): Promise<Record<string, unknown>> {
    return this.#json(OPERATIONS.registerStick, { body: input });
  }

  stickConfig(tailnet?: string): Promise<StickConfig> {
    return this.#json(OPERATIONS.stickConfig, { query: { tailnet } });
  }
}

/**
 * errorMessage extracts a human message from an error body: the JSON
 * `error` field when present, else the trimmed raw text (capped).
 */
function errorMessage(raw: string): string {
  try {
    const j = JSON.parse(raw) as { error?: unknown };
    if (typeof j.error === "string" && j.error) return j.error;
  } catch {
    // not JSON — fall through
  }
  const s = raw.trim();
  return s.length > 300 ? `${s.slice(0, 300)}…` : s;
}

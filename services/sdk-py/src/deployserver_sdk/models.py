"""Types mirroring the deployserver operator API wire format.

These are ``TypedDict``s: at runtime the values are the plain ``dict``s
that ``json.loads`` returns (zero conversion, zero cost), while type
checkers see precise field names and types. They match the Go and
TypeScript SDK models one-for-one.

Note the differing key styles: the server serializes its Machine struct
without JSON tags, so :class:`Machine` uses Go-style keys (``ID``,
``AssetTag``, …); every other payload uses ``snake_case``. Timestamps
arrive as RFC 3339 strings and are kept as ``str``.
"""

from __future__ import annotations

from typing import Any, NotRequired, TypedDict


class AuthConfig(TypedDict):
    issuer: str
    client_id: str
    dev_mode: bool


class Me(TypedDict):
    user_id: str
    dev_mode: bool


class Machine(TypedDict):
    ID: str
    AssetTag: str | None
    MACPrimary: str | None
    UUIDSMBIOS: str | None
    Vendor: str | None
    Model: str | None
    DefaultProfileID: str | None
    Attributes: Any
    CreatedAt: str


class CreateMachineInput(TypedDict, total=False):
    asset_tag: str
    mac_primary: str
    uuid_smbios: str
    vendor: str
    model: str
    default_profile_id: str
    attributes: dict[str, Any]


class UpdateMachineInput(TypedDict, total=False):
    asset_tag: str
    mac_primary: str
    vendor: str
    model: str
    #: "" clears the default profile.
    default_profile_id: str
    attributes: dict[str, Any]


class WakeInput(TypedDict, total=False):
    #: RFC 3339; empty = next agent poll.
    at: str
    site: str


class WakeResult(TypedDict):
    wake_id: str
    site: str


class WakeRequest(TypedDict):
    id: str
    machine_id: str
    mac: str
    site: str
    scheduled_at: str
    claimed_at: str | None
    claimed_by: str | None


class Profile(TypedDict):
    id: str
    name: str
    image_id: str
    image_name: str
    os_family: str
    os_version: str
    created_at: str


class CreateProfileInput(TypedDict):
    name: str
    image_id: str
    answer_file_vars: NotRequired[dict[str, Any]]


class ImageMedia(TypedDict, total=False):
    kernel_url: str
    initrd_url: str
    wimboot_url: str
    bootwim_url: str
    wim_url: str
    deploy_method: str


class Image(TypedDict):
    id: str
    name: str
    os_family: str
    os_version: str
    arch: str
    description: str | None
    versions_count: int
    media: Any
    created_at: str


class CreateImageInput(TypedDict):
    name: str
    os_family: str
    os_version: str
    arch: str
    description: NotRequired[str]
    media: NotRequired[ImageMedia]


class ImageVersion(TypedDict):
    id: str
    image_id: str
    version_tag: str
    blob_id: str
    blob_key: str
    blob_sha256: str
    size_bytes: int
    created_at: str


class CreateBlobInput(TypedDict):
    sha256: str
    size_bytes: int
    filename: NotRequired[str]
    #: images (default) | drivers | blobs
    role: NotRequired[str]


class BlobUpload(TypedDict):
    blob_id: str
    bucket: str
    key: str
    upload_url: str
    expires_in_seconds: int


class DriverRule(TypedDict):
    type: str
    value: str


class DriverPack(TypedDict):
    pack_id: str
    vendor: str
    model: str
    os_family: str
    os_version: str
    version_id: str
    version_tag: str
    blob_sha256: str
    size_bytes: int
    rules: list[DriverRule]
    created_at: str


class CreateDriverPackInput(TypedDict):
    vendor: str
    model: str
    os_family: str
    blob_id: str
    rules: list[DriverRule]
    os_version: NotRequired[str]
    version_tag: NotRequired[str]


class Job(TypedDict):
    id: str
    machine_id: str
    machine_asset_tag: str | None
    profile_id: str
    profile_name: str
    state: str
    created_at: str
    started_at: str | None
    finished_at: str | None


class JobEvent(TypedDict):
    id: int
    phase: str
    message: str
    at: str


class JobDetail(TypedDict):
    job: Job
    events: list[JobEvent]


class IssueDeploymentInput(TypedDict):
    machine_id: str
    profile_id: str
    ttl_seconds: NotRequired[int]
    issued_for: NotRequired[str]
    binding_cidr: NotRequired[str]
    kind: NotRequired[str]
    capture_image_id: NotRequired[str]
    capture_version_tag: NotRequired[str]


class IssueResult(TypedDict):
    code: str
    expires_at: str


class BulkDeployInput(TypedDict):
    machine_ids: list[str]
    profile_id: str
    ttl_seconds: NotRequired[int]
    issued_for: NotRequired[str]
    wake: NotRequired[bool]


class BulkResult(TypedDict):
    machine_id: str
    code: NotRequired[str]
    expires_at: NotRequired[str]
    wake_id: NotRequired[str]
    error: NotRequired[str]


class ReportSummary(TypedDict):
    window: str
    total: int
    completed: int
    failed: int
    cancelled: int
    active: int
    captures: int
    success_rate: float
    avg_duration_secs: float
    machines_total: int
    images_total: int


class ReportDay(TypedDict):
    day: str
    completed: int
    failed: int


class ReportGroup(TypedDict):
    name: str
    total: int
    completed: int
    failed: int
    avg_duration_secs: float


class User(TypedDict):
    id: str
    email: str
    oidc_linked: bool
    roles: list[str]
    created_at: str


class Role(TypedDict):
    name: str
    permissions: list[str]


class Site(TypedDict):
    name: str
    mirror_base_url: str | None
    description: str | None
    created_at: str


class SiteInput(TypedDict):
    name: str
    mirror_base_url: str | None
    description: str | None


class RegisterStickInput(TypedDict):
    image_sha256: str
    tailnet: str
    deploy_url: str
    ca_fingerprint: str
    label: NotRequired[str]


class StickConfig(TypedDict):
    config_json: str
    ca_pem: str
    make_stick_command: str
    deploy_url: str
    control_url: str
    tailnet: str


class AuditEvent(TypedDict):
    action: str
    actor_id: str | None
    actor_kind: str
    subject_id: str | None
    subject_kind: str
    source_ip: str
    #: Loose — the audit payload's shape varies by action.
    data: Any
    at: str


class VendorPackEntry(TypedDict):
    """One downloadable pack from a vendor catalog."""

    vendor: str
    model: str
    types: list[str]
    os_family: str
    os_version: str
    url: str
    sha256: str
    date: str


class VendorFetchJob(TypedDict):
    """A queued/running/finished vendor pack download."""

    id: str
    vendor: str
    model: str
    mtypes: list[str]
    os_family: str
    os_version: str
    url: str
    state: str
    error: str | None
    pack_version_id: str | None
    size_bytes: int | None
    created_at: str
    started_at: str | None
    finished_at: str | None


class ProfilePreview(TypedDict):
    """Result of rendering a profile template with a synthetic machine."""

    rendered: str
    yaml_valid: bool
    yaml_error: str
    fallback: bool

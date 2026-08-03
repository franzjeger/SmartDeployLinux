"""The typed client. HTTP goes through the standard library only
(``urllib.request``) — the SDK has zero runtime dependencies.
"""

from __future__ import annotations

import json
import urllib.error
import urllib.parse
import urllib.request
from typing import Any, Callable, Mapping, Optional, cast

from .models import (
    APIToken,
    AuditEvent,
    AuthConfig,
    BlobUpload,
    CreateAPITokenInput,
    CreatedAPIToken,
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
    Machine,
    Me,
    Profile,
    ProfilePreview,
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
    VendorFetchJob,
    VendorPackEntry,
    WakeInput,
    WakeRequest,
    WakeResult,
)
from .operations import (
    OP_AUTH_CONFIG,
    OP_BULK_DEPLOY,
    OP_CANCEL_JOB,
    OP_CREATE_API_TOKEN,
    OP_CREATE_BLOB,
    OP_CREATE_DRIVER_PACK,
    OP_CREATE_IMAGE,
    OP_CREATE_IMAGE_VERSION,
    OP_CREATE_MACHINE,
    OP_CREATE_PROFILE,
    OP_DELETE_DRIVER_VERSION,
    OP_DELETE_IMAGE,
    OP_DELETE_MACHINE,
    OP_DELETE_PROFILE,
    OP_DELETE_SITE,
    OP_DELETE_TEMPLATE,
    OP_FETCH_VENDOR_PACK,
    OP_GET_IMAGE,
    OP_GET_JOB,
    OP_GET_MACHINE,
    OP_GET_PROFILE,
    OP_GRANT_ROLE,
    OP_INSTALL_CATALOG,
    OP_ISSUE_DEPLOYMENT,
    OP_LIST_API_TOKENS,
    OP_LIST_CATALOG,
    OP_LIST_DRIVER_PACKS,
    OP_LIST_IMAGE_VERSIONS,
    OP_LIST_IMAGES,
    OP_LIST_JOBS,
    OP_LIST_MACHINES,
    OP_LIST_PROFILES,
    OP_LIST_ROLES,
    OP_LIST_SITES,
    OP_LIST_STICKS,
    OP_LIST_USERS,
    OP_LIST_VENDOR_FETCH_JOBS,
    OP_LIST_WAKES,
    OP_ME,
    OP_PREVIEW_PROFILE,
    OP_QUERY_AUDIT,
    OP_REGISTER_STICK,
    OP_REPORT_BY_PROFILE,
    OP_REPORT_BY_SITE,
    OP_REPORT_DAILY,
    OP_REPORT_JOBS_CSV,
    OP_REPORT_SUMMARY,
    OP_REVOKE_API_TOKEN,
    OP_REVOKE_ROLE,
    OP_SEARCH_VENDOR_PACKS,
    OP_STICK_CONFIG,
    OP_UPDATE_IMAGE,
    OP_UPDATE_MACHINE,
    OP_UPDATE_PROFILE,
    OP_UPSERT_SITE,
    OP_UPSERT_TEMPLATE,
    OP_WAKE_MACHINE,
    Operation,
)

DEFAULT_TIMEOUT = 30.0

#: A urllib-compatible opener: ``open(request, timeout=...) -> response``.
Opener = Callable[..., Any]


class ApiError(Exception):
    """A non-2xx response. ``message`` is the server's error body (or its
    JSON ``error`` field) so failures are actionable."""

    def __init__(self, status: int, method: str, path: str, message: str) -> None:
        super().__init__(f"deployserver {method} {path}: {status}: {message}")
        self.status = status
        self.method = method
        self.path = path
        self.api_message = message


def is_not_found(err: object) -> bool:
    """True when ``err`` is a 404 :class:`ApiError`."""
    return isinstance(err, ApiError) and err.status == 404


def is_forbidden(err: object) -> bool:
    """True when ``err`` is a 403 (missing RBAC permission)."""
    return isinstance(err, ApiError) and err.status == 403


class DeployClient:
    """Typed client for the deployserver operator API. Its method surface
    is kept in exact correspondence with the OpenAPI spec by the parity
    test.

    >>> c = DeployClient("https://deploy.example.com", token=tok)
    >>> machines = c.list_machines()
    """

    def __init__(
        self,
        base_url: str,
        token: Optional[str] = None,
        *,
        timeout: float = DEFAULT_TIMEOUT,
        opener: Optional[urllib.request.OpenerDirector] = None,
    ) -> None:
        if not base_url:
            raise ValueError("base_url required")
        self._base_url = base_url.rstrip("/")
        self._token = token
        self._timeout = timeout
        self._open: Opener = opener.open if opener is not None else urllib.request.urlopen

    # --- transport ------------------------------------------------------

    def _fill(self, path: str, params: Optional[Mapping[str, str]]) -> str:
        if not params:
            return path
        out = path
        for key, value in params.items():
            out = out.replace("{" + key + "}", urllib.parse.quote(value, safe=""))
        return out

    def _raw(
        self,
        op: Operation,
        *,
        params: Optional[Mapping[str, str]] = None,
        query: Optional[Mapping[str, object]] = None,
        body: object = None,
    ) -> str:
        path = self._fill(op.path, params)
        url = self._base_url + path
        if query:
            items = [(k, str(v)) for k, v in query.items() if v is not None and v != ""]
            if items:
                url += "?" + urllib.parse.urlencode(items)

        data: Optional[bytes] = None
        headers: dict[str, str] = {"Accept": "application/json"}
        if body is not None:
            data = json.dumps(body).encode("utf-8")
            headers["Content-Type"] = "application/json"
        if self._token:
            headers["Authorization"] = f"Bearer {self._token}"

        req = urllib.request.Request(url, data=data, headers=headers, method=op.method)
        try:
            with self._open(req, timeout=self._timeout) as resp:
                raw: bytes = resp.read()
        except urllib.error.HTTPError as exc:
            detail = exc.read().decode("utf-8", errors="replace")
            raise ApiError(exc.code, op.method, path, _error_message(detail)) from None
        except urllib.error.URLError as exc:
            raise RuntimeError(f"sdk: {op.method} {path}: {exc.reason}") from exc
        return raw.decode("utf-8")

    def _json(
        self,
        op: Operation,
        *,
        params: Optional[Mapping[str, str]] = None,
        query: Optional[Mapping[str, object]] = None,
        body: object = None,
    ) -> Any:
        text = self._raw(op, params=params, query=query, body=body)
        return json.loads(text) if text else None

    def _none(
        self,
        op: Operation,
        *,
        params: Optional[Mapping[str, str]] = None,
        query: Optional[Mapping[str, object]] = None,
        body: object = None,
    ) -> None:
        self._raw(op, params=params, query=query, body=body)

    # --- identity -------------------------------------------------------

    def auth_config(self) -> AuthConfig:
        """Public pre-auth OIDC discovery info (no auth required)."""
        return self._json(OP_AUTH_CONFIG)

    def me(self) -> Me:
        """The authenticated principal."""
        return self._json(OP_ME)

    # --- machines -------------------------------------------------------

    def list_machines(self) -> list[Machine]:
        return self._json(OP_LIST_MACHINES)

    def create_machine(self, input: CreateMachineInput) -> Machine:
        return self._json(OP_CREATE_MACHINE, body=input)

    def get_machine(self, machine_id: str) -> Machine:
        return self._json(OP_GET_MACHINE, params={"id": machine_id})

    def update_machine(self, machine_id: str, input: UpdateMachineInput) -> Machine:
        return self._json(OP_UPDATE_MACHINE, params={"id": machine_id}, body=input)

    def delete_machine(self, machine_id: str) -> None:
        self._none(OP_DELETE_MACHINE, params={"id": machine_id})

    def wake_machine(self, machine_id: str, input: Optional[WakeInput] = None) -> WakeResult:
        return self._json(OP_WAKE_MACHINE, params={"id": machine_id}, body=input or {})

    def list_wakes(self, machine_id: str) -> list[WakeRequest]:
        return self._json(OP_LIST_WAKES, params={"id": machine_id})

    # --- profiles -------------------------------------------------------

    def list_profiles(self) -> list[Profile]:
        return self._json(OP_LIST_PROFILES)

    def create_profile(self, input: CreateProfileInput) -> Profile:
        return self._json(OP_CREATE_PROFILE, body=input)

    def get_profile(self, profile_id: str) -> dict[str, Any]:
        """The profile plus its answer-file templates, as a loose dict."""
        return self._json(OP_GET_PROFILE, params={"id": profile_id})

    def update_profile(self, profile_id: str, patch: Mapping[str, Any]) -> None:
        self._none(OP_UPDATE_PROFILE, params={"id": profile_id}, body=patch)

    def delete_profile(self, profile_id: str) -> None:
        self._none(OP_DELETE_PROFILE, params={"id": profile_id})

    def upsert_profile_template(self, profile_id: str, kind: str, body: str) -> None:
        """Set the answer-file template of the given kind
        (autoinstall|kickstart|preseed|cloud-init|ignition|unattend)."""
        self._none(OP_UPSERT_TEMPLATE, params={"id": profile_id}, body={"kind": kind, "body": body})

    def delete_profile_template(self, profile_id: str, kind: str) -> None:
        self._none(OP_DELETE_TEMPLATE, params={"id": profile_id, "kind": kind})

    def preview_profile_template(
        self, profile_id: str, kind: str, body: Optional[str] = None
    ) -> ProfilePreview:
        """Render a template against a synthetic machine and report whether
        the output parses (YAML kinds)."""
        payload: dict[str, str] = {"kind": kind}
        if body is not None:
            payload["body"] = body
        return self._json(OP_PREVIEW_PROFILE, params={"id": profile_id}, body=payload)

    # --- images ---------------------------------------------------------

    def list_images(self) -> list[Image]:
        return self._json(OP_LIST_IMAGES)

    def create_image(self, input: CreateImageInput) -> Image:
        return self._json(OP_CREATE_IMAGE, body=input)

    def get_image(self, image_id: str) -> Image:
        return self._json(OP_GET_IMAGE, params={"id": image_id})

    def update_image(self, image_id: str, patch: Mapping[str, Any]) -> None:
        self._none(OP_UPDATE_IMAGE, params={"id": image_id}, body=patch)

    def delete_image(self, image_id: str) -> None:
        self._none(OP_DELETE_IMAGE, params={"id": image_id})

    def list_image_versions(self, image_id: str) -> list[ImageVersion]:
        return self._json(OP_LIST_IMAGE_VERSIONS, params={"id": image_id})

    def create_image_version(
        self, image_id: str, blob_id: str, version_tag: Optional[str] = None
    ) -> dict[str, str]:
        """Link an uploaded blob as a new version of the image."""
        body: dict[str, str] = {"blob_id": blob_id}
        if version_tag:
            body["version_tag"] = version_tag
        return self._json(OP_CREATE_IMAGE_VERSION, params={"id": image_id}, body=body)

    def create_blob(self, input: CreateBlobInput) -> BlobUpload:
        """Register a blob and get back a presigned upload URL."""
        return self._json(OP_CREATE_BLOB, body=input)

    # --- driver packs ---------------------------------------------------

    def list_driver_packs(self) -> list[DriverPack]:
        return self._json(OP_LIST_DRIVER_PACKS)

    def create_driver_pack(self, input: CreateDriverPackInput) -> dict[str, str]:
        return self._json(OP_CREATE_DRIVER_PACK, body=input)

    def delete_driver_pack_version(self, version_id: str) -> None:
        self._none(OP_DELETE_DRIVER_VERSION, params={"id": version_id})

    # --- vendor driver packs -------------------------------------------

    def search_vendor_driver_packs(self, query: str) -> list[VendorPackEntry]:
        """Search vendor driver-pack catalogs (Dell/HP/Lenovo/…)."""
        return self._json(OP_SEARCH_VENDOR_PACKS, query={"q": query})

    def fetch_vendor_driver_pack(self, url: str) -> dict[str, str]:
        """Queue a background download of a vendor pack by URL; returns the
        fetch-job id."""
        return self._json(OP_FETCH_VENDOR_PACK, body={"url": url})

    def list_vendor_fetch_jobs(self, limit: Optional[int] = None) -> list[VendorFetchJob]:
        """Recent vendor fetch jobs, newest first."""
        return self._json(OP_LIST_VENDOR_FETCH_JOBS, query={"limit": limit})

    # --- catalog --------------------------------------------------------

    def list_catalog(self) -> dict[str, Any]:
        """The distro net-install catalog, as a loose dict."""
        return self._json(OP_LIST_CATALOG)

    def install_from_catalog(self, entry_id: str, name: Optional[str] = None) -> dict[str, Any]:
        body: dict[str, str] = {"entry_id": entry_id}
        if name:
            body["name"] = name
        return self._json(OP_INSTALL_CATALOG, body=body)

    # --- jobs -----------------------------------------------------------

    def list_jobs(
        self,
        *,
        state: Optional[str] = None,
        machine_id: Optional[str] = None,
        limit: Optional[int] = None,
    ) -> list[Job]:
        return self._json(OP_LIST_JOBS, query={"state": state, "machine": machine_id, "limit": limit})

    def get_job(self, job_id: str) -> JobDetail:
        return self._json(OP_GET_JOB, params={"id": job_id})

    def cancel_job(self, job_id: str) -> None:
        self._none(OP_CANCEL_JOB, params={"id": job_id})

    # --- audit ----------------------------------------------------------

    def query_audit(
        self, *, since: Optional[str] = None, action: Optional[str] = None
    ) -> list[AuditEvent]:
        return self._json(OP_QUERY_AUDIT, query={"since": since, "action": action})

    # --- reports --------------------------------------------------------

    def report_summary(self, since: Optional[str] = None) -> ReportSummary:
        return self._json(OP_REPORT_SUMMARY, query={"since": since})

    def report_daily(self, days: Optional[int] = None) -> list[ReportDay]:
        return self._json(OP_REPORT_DAILY, query={"days": days})

    def report_by_profile(self, since: Optional[str] = None) -> list[ReportGroup]:
        return self._json(OP_REPORT_BY_PROFILE, query={"since": since})

    def report_by_site(self, since: Optional[str] = None) -> list[ReportGroup]:
        return self._json(OP_REPORT_BY_SITE, query={"since": since})

    def report_jobs_csv(self, since: Optional[str] = None) -> str:
        """The raw CSV export text."""
        return self._raw(OP_REPORT_JOBS_CSV, query={"since": since})

    # --- deployments ----------------------------------------------------

    def issue_deployment(self, input: IssueDeploymentInput) -> IssueResult:
        return self._json(OP_ISSUE_DEPLOYMENT, body=input)

    def bulk_deploy(self, input: BulkDeployInput) -> list[BulkResult]:
        wrap = self._json(OP_BULK_DEPLOY, body=input)
        results: list[BulkResult] = wrap.get("results", []) if wrap else []
        return results

    # --- users & roles --------------------------------------------------

    def list_users(self) -> list[User]:
        return self._json(OP_LIST_USERS)

    def list_roles(self) -> list[Role]:
        return self._json(OP_LIST_ROLES)

    def grant_role(self, user_id: str, role: str) -> None:
        self._none(OP_GRANT_ROLE, params={"id": user_id}, body={"role": role})

    def revoke_role(self, user_id: str, role: str) -> None:
        self._none(OP_REVOKE_ROLE, params={"id": user_id, "role": role})

    # --- API tokens -----------------------------------------------------

    def create_api_token(self, input: CreateAPITokenInput) -> CreatedAPIToken:
        """Mint a long-lived token. The ``token`` field of the result is the
        plaintext secret, returned only once."""
        return self._json(OP_CREATE_API_TOKEN, body=input)

    def list_api_tokens(self) -> list[APIToken]:
        """The caller's own tokens (never their secrets)."""
        return self._json(OP_LIST_API_TOKENS)

    def revoke_api_token(self, token_id: str) -> None:
        self._none(OP_REVOKE_API_TOKEN, params={"id": token_id})

    # --- sites ----------------------------------------------------------

    def list_sites(self) -> list[Site]:
        return self._json(OP_LIST_SITES)

    def upsert_site(self, input: SiteInput) -> Site:
        return self._json(OP_UPSERT_SITE, body=input)

    def delete_site(self, name: str) -> None:
        self._none(OP_DELETE_SITE, params={"name": name})

    # --- bootstrap sticks -----------------------------------------------

    def list_sticks(self) -> list[dict[str, Any]]:
        return self._json(OP_LIST_STICKS)

    def register_stick(self, input: RegisterStickInput) -> dict[str, Any]:
        return self._json(OP_REGISTER_STICK, body=input)

    def stick_config(self, tailnet: Optional[str] = None) -> StickConfig:
        return self._json(OP_STICK_CONFIG, query={"tailnet": tailnet})


def _error_message(raw: str) -> str:
    """Extract a human message from an error body: the JSON ``error``
    field when present, else the trimmed raw text (capped)."""
    try:
        parsed: object = json.loads(raw)
    except ValueError:
        parsed = None
    if isinstance(parsed, dict):
        data = cast("dict[str, object]", parsed)
        err = data.get("error")
        if isinstance(err, str) and err:
            return err
    stripped = raw.strip()
    return stripped[:300] + "…" if len(stripped) > 300 else stripped

"""deployserver-sdk — typed Python client for the deployserver operator API.

Its surface is derived from, and kept in exact correspondence with, the
OpenAPI 3.1 spec the server publishes at ``/api/openapi.yaml``: a parity
test asserts every documented operation has exactly one SDK method and
vice versa, so the client cannot silently drift from the API.

Runtime dependencies: none (the standard library only).

    from deployserver_sdk import DeployClient, is_not_found

    c = DeployClient("https://deploy.example.com", token=tok)
    for m in c.list_machines():
        print(m["ID"], m["AssetTag"])
"""

from __future__ import annotations

from ._client import ApiError, DeployClient, is_forbidden, is_not_found
from .operations import ALL_OPERATIONS, Operation

__version__ = "1.0.0"

__all__ = [
    "DeployClient",
    "ApiError",
    "is_not_found",
    "is_forbidden",
    "Operation",
    "ALL_OPERATIONS",
    "__version__",
]

"""Parity + sync gates. Uses PyYAML (a test-only dependency) to parse the
embedded spec. This is what makes the hand-written client generated-grade:
its operation set must match the published contract exactly."""

from __future__ import annotations

import unittest
from pathlib import Path

import yaml

from deployserver_sdk.operations import ALL_OPERATIONS

_PKG_ROOT = Path(__file__).resolve().parents[1]          # services/sdk-py
_EMBEDDED_SPEC = _PKG_ROOT / "openapi.yaml"
_SOURCE_SPEC = _PKG_ROOT.parent / "api" / "internal" / "apispec" / "openapi.yaml"

_HTTP_METHODS = {"get", "post", "put", "patch", "delete"}


def _spec_operation_set() -> set[str]:
    doc = yaml.safe_load(_EMBEDDED_SPEC.read_text(encoding="utf-8"))
    result: set[str] = set()
    for path, ops in (doc.get("paths") or {}).items():
        assert path.startswith("/api/v1/"), f"spec path {path} is not under /api/v1"
        for method in ops:
            if method in _HTTP_METHODS:
                result.add(f"{method.upper()} {path}")
    assert result, "spec produced no operations — copy broken?"
    return result


class TestParity(unittest.TestCase):
    def test_operation_parity_bijection(self) -> None:
        """ALL_OPERATIONS must be in exact bijection with the spec's
        paths+methods — the guarantee a code generator gives."""
        spec = _spec_operation_set()

        sdk: set[str] = set()
        for op in ALL_OPERATIONS:
            key = f"{op.method} {op.path}"
            self.assertNotIn(key, sdk, f"ALL_OPERATIONS lists {key} more than once")
            sdk.add(key)

        self.assertEqual(
            spec - sdk, set(), "spec documents operations the SDK does not implement"
        )
        self.assertEqual(
            sdk - spec, set(), "SDK implements operations the spec does not document"
        )

    def test_operations_well_formed(self) -> None:
        ids: set[str] = set()
        for op in ALL_OPERATIONS:
            self.assertTrue(op.id and op.method and op.path, f"empty field: {op}")
            self.assertEqual(op.method, op.method.upper())
            self.assertTrue(op.path.startswith("/api/v1/"))
            self.assertNotIn(op.id, ids, f"duplicate operation id {op.id}")
            ids.add(op.id)
        self.assertTrue(ALL_OPERATIONS)

    def test_embedded_spec_matches_source(self) -> None:
        """The embedded copy must be byte-identical to the api source of
        truth. Skips when the source is absent (standalone checkout)."""
        if not _SOURCE_SPEC.exists():
            self.skipTest(f"api source spec absent at {_SOURCE_SPEC} — skipping sync check")
        self.assertEqual(
            _SOURCE_SPEC.read_bytes(),
            _EMBEDDED_SPEC.read_bytes(),
            "embedded openapi.yaml is out of sync with the api source; run: make sync-sdk-spec",
        )


if __name__ == "__main__":
    unittest.main()

import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { parse } from "yaml";

import { ALL_OPERATIONS } from "../src/operations.js";

// Resolved from this module's location (cwd-independent). After compile
// this file lives at dist/test/parity.test.js.
const EMBEDDED_SPEC = new URL("../../openapi.yaml", import.meta.url);
const SOURCE_SPEC = new URL("../../../api/internal/apispec/openapi.yaml", import.meta.url);

const HTTP_METHODS = new Set(["get", "post", "put", "patch", "delete"]);

/** Parse the embedded spec into a set of "METHOD path" keys. */
function specOperationSet(): Set<string> {
  const doc = parse(readFileSync(EMBEDDED_SPEC, "utf8")) as {
    paths?: Record<string, Record<string, unknown>>;
  };
  const set = new Set<string>();
  for (const [path, ops] of Object.entries(doc.paths ?? {})) {
    assert.ok(path.startsWith("/api/v1/"), `spec path ${path} is not under /api/v1`);
    for (const method of Object.keys(ops)) {
      if (HTTP_METHODS.has(method)) set.add(`${method.toUpperCase()} ${path}`);
    }
  }
  assert.ok(set.size > 0, "spec produced no operations — embed broken?");
  return set;
}

// The core quality gate: the operations the SDK implements
// (ALL_OPERATIONS) must be in EXACT bijection with the operations the
// OpenAPI spec documents. A spec endpoint with no SDK method, or an SDK
// method with no spec endpoint, fails here — the guarantee a code
// generator gives, enforced on a hand-written client.
test("operation parity: SDK ↔ spec is an exact bijection", () => {
  const spec = specOperationSet();

  const sdk = new Set<string>();
  for (const op of ALL_OPERATIONS) {
    const key = `${op.method} ${op.path}`;
    assert.ok(!sdk.has(key), `ALL_OPERATIONS lists ${key} more than once`);
    sdk.add(key);
  }

  const missingFromSdk = [...spec].filter((k) => !sdk.has(k));
  const missingFromSpec = [...sdk].filter((k) => !spec.has(k));
  assert.deepEqual(missingFromSdk, [], `spec documents operations the SDK does not implement: ${missingFromSdk}`);
  assert.deepEqual(missingFromSpec, [], `SDK implements operations the spec does not document: ${missingFromSpec}`);
  assert.equal(sdk.size, spec.size);
});

// Guards against copy-paste slips in the operation table.
test("operations are well-formed", () => {
  const ids = new Set<string>();
  for (const op of ALL_OPERATIONS) {
    assert.ok(op.id && op.method && op.path, `operation has an empty field: ${JSON.stringify(op)}`);
    assert.equal(op.method, op.method.toUpperCase(), `method ${op.method} is not upper-case`);
    assert.ok(op.path.startsWith("/api/v1/"), `path ${op.path} is not under /api/v1/`);
    assert.ok(!ids.has(op.id), `duplicate operation id ${op.id}`);
    ids.add(op.id);
  }
  assert.ok(ALL_OPERATIONS.length > 0);
});

// The embedded spec must be byte-for-byte identical to the api module's
// source of truth, so the SDK is never built against a stale contract.
// When the api source is absent (a standalone SDK checkout) the test
// skips — the parity test above still guards SDK↔spec correspondence.
test("embedded spec matches the api source of truth", (t) => {
  let source: Buffer;
  try {
    source = readFileSync(SOURCE_SPEC);
  } catch (err) {
    if ((err as NodeJS.ErrnoException).code === "ENOENT") {
      t.skip("api source spec absent (standalone checkout) — skipping sync check");
      return;
    }
    throw err;
  }
  const embedded = readFileSync(EMBEDDED_SPEC);
  assert.ok(
    source.equals(embedded),
    "embedded openapi.yaml is out of sync with the api source; refresh it with: npm run sync-spec",
  );
});

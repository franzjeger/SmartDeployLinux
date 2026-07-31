import { test, before, after } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import type { AddressInfo } from "node:net";

import { DeployClient, ApiError, isNotFound, isForbidden } from "../src/index.js";

// A single test server whose response is set per-test via `respond`, and
// which records the last request it saw in `captured`.
interface Captured {
  method: string;
  path: string;
  query: string;
  auth: string;
  accept: string;
  ctype: string;
  body: string;
}

let server: http.Server;
let baseUrl = "";
let captured: Captured;
let respond: (res: http.ServerResponse) => void = (res) => res.end("{}");

before(async () => {
  server = http.createServer((req, res) => {
    const chunks: Buffer[] = [];
    req.on("data", (c: Buffer) => chunks.push(c));
    req.on("end", () => {
      const [path, query = ""] = (req.url ?? "").split("?");
      captured = {
        method: req.method ?? "",
        path: path ?? "",
        query,
        auth: req.headers["authorization"] ?? "",
        accept: req.headers["accept"] ?? "",
        ctype: req.headers["content-type"] ?? "",
        body: Buffer.concat(chunks).toString(),
      };
      respond(res);
    });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const addr = server.address() as AddressInfo;
  baseUrl = `http://127.0.0.1:${addr.port}`;
});

after(() => {
  server.close();
});

function client(token: string | undefined = "tok-123"): DeployClient {
  return new DeployClient({ baseUrl, token });
}

function json(body: string, status = 200): (res: http.ServerResponse) => void {
  return (res) => {
    res.writeHead(status, { "Content-Type": "application/json" });
    res.end(body);
  };
}

test("constructor rejects an empty baseUrl", () => {
  assert.throws(() => new DeployClient({ baseUrl: "" }), /baseUrl required/);
});

test("constructor trims a trailing slash", async () => {
  respond = json("[]");
  const c = new DeployClient({ baseUrl: `${baseUrl}/`, token: "t" });
  await c.listMachines();
  assert.equal(captured.path, "/api/v1/machines");
});

test("listMachines: method, auth, accept, decode", async () => {
  respond = json(`[{"ID":"m1","AssetTag":"A-1","CreatedAt":"2026-01-02T03:04:05Z"}]`);
  const got = await client().listMachines();
  assert.equal(captured.method, "GET");
  assert.equal(captured.path, "/api/v1/machines");
  assert.equal(captured.auth, "Bearer tok-123");
  assert.equal(captured.accept, "application/json");
  assert.equal(got.length, 1);
  assert.equal(got[0]?.ID, "m1");
  assert.equal(got[0]?.AssetTag, "A-1");
});

test("path params are percent-encoded into a single segment", async () => {
  respond = json(`{"ID":"weird/id"}`);
  await client().getMachine("weird/id");
  // encodeURIComponent turns the slash into %2F, so it stays one segment.
  assert.equal(captured.path, "/api/v1/machines/weird%2Fid");
});

test("createMachine sends a JSON body with content-type", async () => {
  respond = json(`{"ID":"new"}`, 201);
  const out = await client().createMachine({ asset_tag: "asset-9" });
  assert.equal(captured.method, "POST");
  assert.equal(captured.ctype, "application/json");
  assert.deepEqual(JSON.parse(captured.body), { asset_tag: "asset-9" });
  assert.equal(out.ID, "new");
});

test("404 is classified and message surfaced", async () => {
  respond = json(`{"error":"machine not found"}`, 404);
  await assert.rejects(client().getMachine("nope"), (err: unknown) => {
    assert.ok(isNotFound(err));
    assert.ok(!isForbidden(err));
    assert.ok(err instanceof ApiError && err.message.includes("machine not found"));
    return true;
  });
});

test("403 is classified", async () => {
  respond = json(`{"error":"missing permission machines.write"}`, 403);
  await assert.rejects(client().deleteMachine("m1"), (err: unknown) => {
    assert.ok(isForbidden(err));
    return true;
  });
});

test("listJobs encodes query params", async () => {
  respond = json("[]");
  await client().listJobs({ state: "running", machineId: "m1", limit: 25 });
  const q = captured.query;
  for (const want of ["state=running", "machine=m1", "limit=25"]) {
    assert.ok(q.includes(want), `query ${q} missing ${want}`);
  }
});

test("bulkDeploy unwraps { results }", async () => {
  respond = json(`{"results":[{"machine_id":"m1","code":"A1B2C3"},{"machine_id":"m2","error":"no route"}]}`);
  const res = await client().bulkDeploy({ machine_ids: ["m1", "m2"], profile_id: "p1" });
  assert.equal(res.length, 2);
  assert.equal(res[0]?.code, "A1B2C3");
  assert.equal(res[1]?.error, "no route");
});

test("reportJobsCSV returns raw text", async () => {
  const csv = "id,state\nj1,completed\nj2,failed\n";
  respond = (res) => {
    res.writeHead(200, { "Content-Type": "text/csv" });
    res.end(csv);
  };
  const got = await client().reportJobsCSV("2026-01-01T00:00:00Z");
  assert.equal(got, csv);
  assert.ok(captured.query.includes("since="));
});

test("no Authorization header when token is empty", async () => {
  respond = json(`{"issuer":"x","client_id":"y","dev_mode":true}`);
  // Construct without a token directly — client()'s default would supply one.
  await new DeployClient({ baseUrl }).authConfig();
  assert.equal(captured.auth, "");
});

test("non-JSON error body falls back to raw text", async () => {
  respond = (res) => {
    res.writeHead(502);
    res.end("upstream exploded");
  };
  await assert.rejects(client().deleteMachine("m1"), (err: unknown) => {
    assert.ok(err instanceof ApiError && err.message.includes("upstream exploded"));
    return true;
  });
});

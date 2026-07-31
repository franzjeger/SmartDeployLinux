/**
 * @your-org/deployserver-sdk — typed client for the deployserver operator API.
 *
 * Its surface is derived from, and kept in exact correspondence with, the
 * OpenAPI 3.1 spec the server publishes at `/api/openapi.yaml`: a parity
 * test asserts every documented operation has exactly one SDK method and
 * vice versa, so the client cannot silently drift from the API.
 *
 * Runtime dependencies: none. HTTP goes through the global `fetch`
 * (Node 18+ and modern browsers); a custom implementation can be injected
 * via {@link Options.fetch}.
 *
 * ```ts
 * import { DeployClient, isNotFound } from "@your-org/deployserver-sdk";
 *
 * const c = new DeployClient({ baseUrl: "https://deploy.example.com", token });
 * const machines = await c.listMachines();
 * ```
 */
export { DeployClient, ApiError, isNotFound, isForbidden, type Options } from "./client.js";
export { OPERATIONS, ALL_OPERATIONS, type Operation } from "./operations.js";
export type * from "./models.js";

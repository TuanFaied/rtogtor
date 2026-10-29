/**
 * RtoGtoR: relational ↔ graph migration with explicit mappings.
 *
 * ```ts
 * import { Connection, Mapping, Migration } from "@rtogtor/sdk";
 *
 * const mapping = await Mapping.fromFile("mapping.yaml");
 * await using m = new Migration({
 *   source: Connection.postgresql({ urlEnv: "SOURCE_URL" }),
 *   destination: Connection.neo4j({ uri: "bolt://localhost:7687", authEnv: "NEO4J" }),
 *   mapping,
 * });
 * const plan = await m.plan();
 * const report = await m.run({ dryRun: false });
 * ```
 *
 * All migration logic runs in the bundled RtoGtoR engine; this package is a thin, typed client.
 */
export { Connection, type Neo4jOptions, type PostgresqlOptions } from "./connection.js";
export { Engine, type CallOptions } from "./engine.js";
export { type Diagnostic, RtoGtoRError, type RtoGtoRErrorOptions } from "./errors.js";
export {
  ErrorCodeInfos,
  ErrorCodes,
  type ErrorCodeInfo,
  type ErrorSeverity,
} from "./generated/errorCodes.js";
export type * from "./generated/model.js";
export { ENGINE_PATH_VARIABLE, locateEngine } from "./locate.js";
export { Mapping } from "./mapping.js";
export { Migration, type MigrationOptions, type RunOptions } from "./migration.js";
export { type EngineOptions, SUPPORTED_ENGINE_RANGE } from "./session.js";

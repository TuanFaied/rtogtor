import type { Connection } from "./connection.js";
import type {
  CanonicalizeResult,
  Failure,
  FailuresResult,
  HandshakeResult,
  IdentityResult,
  KeyComponent,
  Plan,
  Report,
  RunState,
  SchemaMetadata,
  ValidateMappingResult,
} from "./generated/model.js";
import type { Mapping } from "./mapping.js";
import { type EngineOptions, Session, SUPPORTED_ENGINE_RANGE } from "./session.js";

/** Per-call options. */
export interface CallOptions {
  /** Aborting cancels the request in the engine. */
  signal?: AbortSignal;
}

/**
 * A running RtoGtoR engine (a child process). All migration logic runs in the engine; this
 * class only sends requests. Close it (or use `await using`) to stop the process.
 *
 * Connections with literal credentials can only be used through `Migration`; with `Engine`,
 * use connections that name environment variables.
 */
export class Engine implements AsyncDisposable {
  /** Engine versions this SDK supports. */
  static readonly supportedEngineVersions = SUPPORTED_ENGINE_RANGE;

  /** @internal */
  readonly session: Session;

  private constructor(session: Session) {
    this.session = session;
  }

  /** Versions reported by the engine. */
  get info(): HandshakeResult {
    return this.session.info;
  }

  /** Starts the engine and checks its version (RTG-PROTO-007) and presence (RTG-PROTO-008). */
  static async start(options: EngineOptions = {}): Promise<Engine> {
    return new Engine(await Session.start(options, {}));
  }

  /** @internal */
  static async startWith(options: EngineOptions, env: Record<string, string>): Promise<Engine> {
    return new Engine(await Session.start(options, env));
  }

  /** Runs every validation of a mapping, including semantic checks the schema cannot express. */
  async validateMapping(mapping: Mapping, options: CallOptions = {}): Promise<ValidateMappingResult> {
    return (await this.session.call(
      "validateMapping",
      { mapping: mapping.document },
      options,
    )) as ValidateMappingResult;
  }

  /** Inspects a PostgreSQL database's schema (read-only). */
  async inspect(
    source: Connection,
    options: CallOptions & { schemas?: string[] } = {},
  ): Promise<SchemaMetadata> {
    return (await this.session.call(
      "inspect",
      { source: source.toWire(undefined), schemas: options.schemas },
      options,
    )) as SchemaMetadata;
  }

  /** Plans a mapping against inspected metadata, without connecting to any database. */
  async plan(mapping: Mapping, sourceMetadata: SchemaMetadata, options: CallOptions = {}): Promise<Plan> {
    return (await this.session.call("plan", { mapping: mapping.document, sourceMetadata }, options)) as Plan;
  }

  /** Reads a run's report. */
  async report(runId: string, stateDir?: string): Promise<Report> {
    return (await this.session.call("report", { runId, stateDir })) as Report;
  }

  /** Reads a run's state. */
  async runState(runId: string, stateDir?: string): Promise<RunState> {
    return (await this.session.call("runState", { runId, stateDir })) as RunState;
  }

  /** Lists a run's failed records. */
  async failures(runId: string, stateDir?: string): Promise<Failure[]> {
    return ((await this.session.call("failures", { runId, stateDir })) as FailuresResult).failures;
  }

  /** Derives an `rtg_id` (specs/identity.md) with the engine's rules. */
  async identity(entityType: string, key: KeyComponent[]): Promise<IdentityResult> {
    const wire = key.map((k) => ({ type: k.type, value: k.value ?? null }));
    return (await this.session.call("identity", { entityType, key: wire })) as IdentityResult;
  }

  /** Returns the canonical text form of a value (specs/values.md). */
  async canonicalize(type: string, value: string): Promise<string> {
    return ((await this.session.call("canonicalize", { type, value })) as CanonicalizeResult).canonical;
  }

  /** Stops the engine process. */
  close(): Promise<void> {
    return this.session.close();
  }

  [Symbol.asyncDispose](): Promise<void> {
    return this.close();
  }
}

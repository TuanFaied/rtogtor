import type { Connection } from "./connection.js";
import { Engine, type CallOptions } from "./engine.js";
import type {
  WireConnection,
  Failure,
  Plan,
  Progress,
  Report,
  RetryPolicy,
  Verification,
} from "./generated/model.js";
import type { Mapping } from "./mapping.js";
import type { EngineOptions } from "./session.js";

/** Options for {@link Migration}. */
export interface MigrationOptions {
  source: Connection;
  /** May be omitted for dry runs. */
  destination?: Connection;
  mapping: Mapping;
  engine?: EngineOptions;
}

/** Options for {@link Migration.run}. */
export interface RunOptions extends CallOptions {
  /** Read and convert everything, write nothing (not even constraints). */
  dryRun?: boolean;
  /** Rows per batch (engine default 1000). Ignored when resuming. */
  batchSize?: number;
  /** Resume this interrupted run. */
  resumeRunId?: string;
  /** Directory holding run state (engine default `.rtogtor`). */
  stateDir?: string;
  /** Called after every committed batch. */
  onProgress?: (progress: Progress) => void;
  /**
   * Retry policy for transient errors. Unset keeps a resumed run's policy, or uses the engine
   * default (3 attempts, 200 ms to 5 s backoff).
   */
  retry?: RetryPolicy;
}

/**
 * Migrates a source to a destination with a mapping. It starts its own engine process on first
 * use; `await using` (or `close()`) stops it.
 *
 * ```ts
 * await using m = new Migration({ source, destination, mapping });
 * const plan = await m.plan();
 * const report = await m.run({ dryRun: false });
 * ```
 */
export class Migration implements AsyncDisposable {
  private readonly options: MigrationOptions;
  private engine: Promise<Engine> | undefined;
  private wireSource: WireConnection | undefined;
  private wireDestination: WireConnection | undefined;
  private closed = false;

  constructor(options: MigrationOptions) {
    this.options = options;
  }

  private start(): Promise<Engine> {
    if (this.closed) return Promise.reject(new Error("The migration is closed."));
    if (this.engine === undefined) {
      // Literal credentials reach the engine as private environment variables.
      const env: Record<string, string> = {};
      this.wireSource = this.options.source.toWire(env);
      this.wireDestination = this.options.destination?.toWire(env);
      this.engine = Engine.startWith(this.options.engine ?? {}, env);
    }
    return this.engine;
  }

  /**
   * Inspects the databases and plans the migration. Nothing is written. Graph-to-relational
   * mappings also inspect the destination, to check the tables that already exist.
   */
  async plan(options: CallOptions = {}): Promise<Plan> {
    const engine = await this.start();
    return (await engine.session.call(
      "plan",
      { mapping: this.options.mapping.document, source: this.wireSource, destination: this.wireDestination },
      options,
    )) as Plan;
  }

  /**
   * Runs the migration (or a dry run) and returns its report. If the run fails or is
   * interrupted, the rejected `RtoGtoRError` carries the report; when aborted through `signal`,
   * the abort reason's `cause` is that error. Resume with `resumeRunId: report.runId`.
   */
  async run(options: RunOptions = {}): Promise<Report> {
    if (this.options.destination === undefined && options.dryRun !== true) {
      throw new TypeError("A destination connection is required unless dryRun is set.");
    }
    const engine = await this.start();
    const { onProgress } = options;
    return (await engine.session.call(
      "migrate",
      {
        mapping: this.options.mapping.document,
        source: this.wireSource,
        destination: this.wireDestination,
        dryRun: options.dryRun ?? false,
        batchSize: options.batchSize,
        retry: options.retry,
        resumeRunId: options.resumeRunId,
        stateDir: options.stateDir,
      },
      {
        signal: options.signal,
        onEvent: (name, data) => {
          if (name === "progress") onProgress?.(data as Progress);
        },
      },
    )) as Report;
  }

  /**
   * Compares a run's source and destination; the result is added to the run's report. `values`
   * also compares every migrated value (RTG-VERIFY-005), reading all mapped data once more.
   */
  async verify(
    runId: string,
    options: CallOptions & { stateDir?: string; values?: boolean } = {},
  ): Promise<Verification> {
    if (this.options.destination === undefined) {
      throw new TypeError("Verification needs a destination connection.");
    }
    const engine = await this.start();
    return (await engine.session.call(
      "verify",
      {
        runId,
        source: this.wireSource,
        destination: this.wireDestination,
        stateDir: options.stateDir,
        values: options.values,
      },
      options,
    )) as Verification;
  }

  /** Reads a run's report. */
  async report(runId: string, stateDir?: string): Promise<Report> {
    return (await this.start()).report(runId, stateDir);
  }

  /** Lists a run's failed records. */
  async failures(runId: string, stateDir?: string): Promise<Failure[]> {
    return (await this.start()).failures(runId, stateDir);
  }

  /** Stops the engine process. */
  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    if (this.engine !== undefined) {
      await (await this.engine.catch(() => undefined))?.close();
    }
  }

  [Symbol.asyncDispose](): Promise<void> {
    return this.close();
  }
}

import { ErrorCodeInfos, type ErrorSeverity } from "./generated/errorCodes.js";
import type { ProtocolError, Report } from "./generated/model.js";

/** One problem reported by the engine or the SDK. */
export interface Diagnostic {
  readonly code: string;
  readonly severity: ErrorSeverity;
  readonly retryable: boolean;
  readonly message: string;
  readonly path?: string | undefined;
}

/** Options for {@link RtoGtoRError}. */
export interface RtoGtoRErrorOptions {
  path?: string | undefined;
  diagnostics?: readonly Diagnostic[] | undefined;
  retryable?: boolean | undefined;
  report?: Report | undefined;
  cause?: unknown;
}

/**
 * An RtoGtoR failure carrying a stable code from specs/error-codes.json. The SDK never
 * invents codes: they come from the engine or from the specification.
 */
export class RtoGtoRError extends Error {
  /** The stable code, for example `"RTG-MAP-005"`. */
  readonly code: string;
  /** Severity of the code. */
  readonly severity: ErrorSeverity;
  /** Whether retrying the same operation unchanged may succeed. */
  readonly retryable: boolean;
  /** Location of the problem (for example a JSON Pointer), if known. */
  readonly path: string | undefined;
  /** Every problem, when the error stands for several. */
  readonly diagnostics: readonly Diagnostic[];
  /** For a failed or interrupted run: its report. Resume with `run({ resumeRunId: report.runId })`. */
  readonly report: Report | undefined;

  constructor(code: string, message: string, options: RtoGtoRErrorOptions = {}) {
    super(`${code}: ${message}`, { cause: options.cause });
    this.name = "RtoGtoRError";
    this.code = code;
    const info = ErrorCodeInfos[code];
    this.severity = info?.severity ?? "fatal";
    this.retryable = options.retryable ?? info?.retryable ?? false;
    this.path = options.path;
    this.diagnostics = options.diagnostics ?? [];
    this.report = options.report;
  }

  /** @internal */
  static fromWire(e: ProtocolError): RtoGtoRError {
    return new RtoGtoRError(e.code, e.message, {
      path: e.path,
      retryable: e.retryable,
      report: e.report,
      diagnostics: (e.diagnostics ?? []).map((d) => ({
        code: d.code,
        severity: d.severity as ErrorSeverity,
        retryable: d.retryable,
        message: d.message,
        path: d.path,
      })),
    });
  }
}

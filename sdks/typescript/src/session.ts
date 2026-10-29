import { type ChildProcessWithoutNullStreams, spawn } from "node:child_process";
import { once } from "node:events";
import { createInterface } from "node:readline";
import { ErrorCodes } from "./generated/errorCodes.js";
import type { HandshakeResult, ProtocolError } from "./generated/model.js";
import { RtoGtoRError } from "./errors.js";
import { locateEngine } from "./locate.js";

export const PROTOCOL_VERSION = "1.0";
export const SUPPORTED_ENGINE_RANGE = ">= 0.1.0-0, < 0.2.0";
export const SDK_VERSION = "0.1.0-dev.0";

/** Engine versions this SDK supports; before 1.0 a minor version may break compatibility. */
export function isSupportedEngine(version: string): boolean {
  const core = (version.split(/[-+]/)[0] ?? "").split(".");
  if (core.length !== 3 || !core.every((p) => /^\d+$/.test(p))) return false;
  return core[0] === "0" && core[1] === "1";
}

/** Options for starting the engine. */
export interface EngineOptions {
  /** Path of the rtogtor binary. Defaults to RTOGTOR_ENGINE_PATH, then the platform package. */
  enginePath?: string;
  /** Extra environment variables for the engine process, for example connection URLs. */
  env?: Record<string, string>;
  /** How long to wait for the engine to exit on close before killing it (ms). */
  shutdownTimeoutMs?: number;
}

interface Pending {
  resolve: (value: unknown) => void;
  reject: (error: Error) => void;
  onEvent: ((name: string, data: unknown) => void) | undefined;
}

/** One engine process speaking JSON Lines (specs/protocol.md). @internal */
export class Session {
  readonly info: HandshakeResult;
  private readonly proc: ChildProcessWithoutNullStreams;
  private readonly pending = new Map<string, Pending>();
  private readonly stderrTail: string[] = [];
  private nextId = 1;
  private closed = false;
  private terminal: RtoGtoRError | undefined;
  private readonly shutdownTimeoutMs: number;

  private constructor(proc: ChildProcessWithoutNullStreams, shutdownTimeoutMs: number) {
    this.proc = proc;
    this.shutdownTimeoutMs = shutdownTimeoutMs;
    this.info = undefined as unknown as HandshakeResult;
    createInterface({ input: proc.stdout }).on("line", (line) => {
      this.dispatch(line);
    });
    createInterface({ input: proc.stderr }).on("line", (line) => {
      this.stderrTail.push(line);
      if (this.stderrTail.length > 20) this.stderrTail.shift();
    });
    proc.on("exit", () => {
      this.failAll();
    });
    proc.stdin.on("error", () => {
      /* reported through the exit handler */
    });
  }

  static async start(options: EngineOptions, env: Record<string, string>): Promise<Session> {
    const path = locateEngine(options.enginePath);
    const proc = spawn(path, ["serve", "--stdio"], {
      env: { ...process.env, ...options.env, ...env },
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    });
    const spawned = await Promise.race([
      once(proc, "spawn").then(() => undefined),
      once(proc, "error").then(([e]) => e as Error),
    ]);
    if (spawned instanceof Error) {
      throw new RtoGtoRError(ErrorCodes.Proto008, `cannot start the engine at ${path}: ${spawned.message}`, {
        cause: spawned,
      });
    }
    const session = new Session(proc, options.shutdownTimeoutMs ?? 5000);
    try {
      const info = (await session.call("handshake", {
        client: { name: "@rtogtor/sdk", version: SDK_VERSION },
        protocolVersions: [PROTOCOL_VERSION],
      })) as HandshakeResult;
      (session as { info: HandshakeResult }).info = info;
      if (!isSupportedEngine(info.engineVersion) || !info.protocolVersion.startsWith("1.")) {
        throw new RtoGtoRError(
          ErrorCodes.Proto007,
          `engine ${info.engineVersion} (protocol ${info.protocolVersion}) is not supported by this SDK, which needs ${SUPPORTED_ENGINE_RANGE}`,
        );
      }
    } catch (e) {
      await session.close();
      throw e;
    }
    return session;
  }

  get pid(): number | undefined {
    return this.proc.pid;
  }

  get exited(): boolean {
    return this.proc.exitCode !== null || this.proc.signalCode !== null;
  }

  private write(message: object): void {
    this.proc.stdin.write(JSON.stringify(message) + "\n");
  }

  /**
   * Sends a request. Aborting `signal` asks the engine to cancel it; the promise then rejects
   * with the signal's reason, whose `cause` is the engine's error (and any run report).
   */
  call(
    method: string,
    params: object,
    options: { signal?: AbortSignal | undefined; onEvent?: (name: string, data: unknown) => void } = {},
  ): Promise<unknown> {
    if (this.closed) {
      return Promise.reject(new RtoGtoRError(ErrorCodes.Proto008, "the engine session is closed"));
    }
    if (this.terminal) return Promise.reject(this.terminal);
    const id = String(this.nextId++);
    const { signal, onEvent } = options;
    return new Promise((resolve, reject) => {
      const onAbort = (): void => {
        this.write({
          protocolVersion: PROTOCOL_VERSION,
          id: `cancel-${id}`,
          method: "cancel",
          params: { requestId: id },
        });
      };
      this.pending.set(id, {
        resolve: (v) => {
          signal?.removeEventListener("abort", onAbort);
          resolve(v);
        },
        reject: (e) => {
          signal?.removeEventListener("abort", onAbort);
          if (signal?.aborted) {
            const reason: unknown = signal.reason;
            if (reason instanceof Error && reason.cause === undefined) {
              (reason as { cause?: unknown }).cause = e;
            }
            reject(reason instanceof Error ? reason : new Error("aborted", { cause: e }));
          } else {
            reject(e);
          }
        },
        onEvent,
      });
      signal?.addEventListener("abort", onAbort, { once: true });
      this.write({ protocolVersion: PROTOCOL_VERSION, id, method, params });
      if (signal?.aborted) onAbort();
    });
  }

  private dispatch(line: string): void {
    if (line.trim() === "") return;
    const message = JSON.parse(line) as {
      id?: string;
      event?: string;
      data?: unknown;
      result?: unknown;
      error?: ProtocolError;
    };
    if (message.id === undefined) return;
    const p = this.pending.get(message.id);
    if (p === undefined) return; // for example the answer to a cancel request
    if (message.event !== undefined) {
      p.onEvent?.(message.event, message.data);
      return;
    }
    this.pending.delete(message.id);
    if (message.error !== undefined) {
      p.reject(RtoGtoRError.fromWire(message.error));
    } else {
      p.resolve(message.result);
    }
  }

  private failAll(): void {
    const exit = this.proc.exitCode === null ? "" : ` with exit code ${this.proc.exitCode}`;
    const tail = this.stderrTail.length ? `; last engine output: ${this.stderrTail.join(" | ")}` : "";
    const error = this.closed
      ? new RtoGtoRError(ErrorCodes.Proto008, "the engine session was closed")
      : new RtoGtoRError(ErrorCodes.Proto008, `the engine process ended${exit}${tail}`);
    this.terminal = error;
    for (const p of this.pending.values()) p.reject(error);
    this.pending.clear();
  }

  /** Asks the engine to shut down, then kills it if it does not exit in time. */
  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    if (this.exited) return;
    const exited = once(this.proc, "exit");
    try {
      this.write({ protocolVersion: PROTOCOL_VERSION, id: "shutdown", method: "shutdown", params: {} });
      this.proc.stdin.end();
    } catch {
      /* already gone */
    }
    let timer: NodeJS.Timeout | undefined;
    const timedOut = new Promise<"timeout">((resolve) => {
      timer = setTimeout(() => {
        resolve("timeout");
      }, this.shutdownTimeoutMs);
    });
    if ((await Promise.race([exited, timedOut])) === "timeout") {
      this.proc.kill("SIGKILL");
      await exited;
    }
    clearTimeout(timer);
  }
}

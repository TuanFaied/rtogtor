import { accessSync, chmodSync, constants, existsSync, statSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join } from "node:path";
import { ErrorCodes } from "./generated/errorCodes.js";
import { RtoGtoRError } from "./errors.js";

/** Environment variable overriding the engine binary. */
export const ENGINE_PATH_VARIABLE = "RTOGTOR_ENGINE_PATH";

/** The npm package holding the engine for this platform, e.g. `@rtogtor/sdk-linux-x64`. */
export function platformPackage(): string {
  return `@rtogtor/sdk-${process.platform}-${process.arch}`;
}

/**
 * Finds the engine: the explicit path, `RTOGTOR_ENGINE_PATH`, or the platform package
 * installed through optionalDependencies (the esbuild pattern).
 */
export function locateEngine(explicit?: string): string {
  const exe = process.platform === "win32" ? "rtogtor.exe" : "rtogtor";
  const candidates: string[] = [];
  const fromEnv = process.env[ENGINE_PATH_VARIABLE];
  if (explicit) {
    candidates.push(explicit);
  } else if (fromEnv) {
    candidates.push(fromEnv);
  } else {
    try {
      const require = createRequire(import.meta.url);
      candidates.push(join(dirname(require.resolve(`${platformPackage()}/package.json`)), "bin", exe));
    } catch {
      candidates.push(`${platformPackage()} (not installed)`);
    }
  }
  for (const c of candidates) {
    if (existsSync(c)) {
      ensureExecutable(c);
      return c;
    }
  }
  throw new RtoGtoRError(
    ErrorCodes.Proto008,
    `the RtoGtoR engine for ${process.platform}-${process.arch} was not found (looked in: ${candidates.join(", ")}). ` +
      `Set ${ENGINE_PATH_VARIABLE} to an rtogtor binary, or install ${platformPackage()}.`,
  );
}

/** Packages built on another OS may lack the execute bit. */
function ensureExecutable(path: string): void {
  if (process.platform === "win32") return;
  try {
    accessSync(path, constants.X_OK);
  } catch {
    try {
      chmodSync(path, statSync(path).mode | 0o111);
    } catch (e) {
      throw new RtoGtoRError(ErrorCodes.Proto008, `the engine at ${path} is not executable: ${String(e)}`, {
        cause: e,
      });
    }
  }
}

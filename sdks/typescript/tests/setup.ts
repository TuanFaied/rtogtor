// Tests use the engine built by `npm run build:engine` unless RTOGTOR_ENGINE_PATH is set.
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

if (!process.env["RTOGTOR_ENGINE_PATH"]) {
  const exe = process.platform === "win32" ? "rtogtor.exe" : "rtogtor";
  const local = join(dirname(fileURLToPath(import.meta.url)), "..", "engine-bin", exe);
  if (!existsSync(local)) {
    throw new Error(`No engine for tests: run "npm run build:engine" or set RTOGTOR_ENGINE_PATH.`);
  }
  process.env["RTOGTOR_ENGINE_PATH"] = local;
}

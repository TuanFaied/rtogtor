// Builds the engine for this machine into engine-bin/ for local tests.
import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const out = join(here, "..", "engine-bin");
mkdirSync(out, { recursive: true });
const exe = process.platform === "win32" ? "rtogtor.exe" : "rtogtor";
execFileSync("go", ["build", "-o", join(out, exe), "./cmd/rtogtor"], {
  cwd: join(here, "..", "..", "..", "engine"),
  env: { ...process.env, CGO_ENABLED: "0" },
  stdio: "inherit",
});
console.log("built", join(out, exe));

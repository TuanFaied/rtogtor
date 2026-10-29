// Packs @rtogtor/sdk and its per-platform engine packages (the esbuild pattern) into
// ../../dist/npm. Each platform package holds bin/rtogtor[.exe] for one platform and declares
// `os`/`cpu`, so npm installs only the matching one through the main package's
// optionalDependencies.
//
//   node scripts/pack.mjs            # every platform
//   node scripts/pack.mjs win32-x64  # selected platforms (the main package is always packed)
import { execFileSync } from "node:child_process";
import { chmodSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = join(dirname(fileURLToPath(import.meta.url)), "..");
const repo = join(here, "..", "..");
const out = join(repo, "dist", "npm");
const staging = join(out, "staging");
const pkg = JSON.parse(readFileSync(join(here, "package.json"), "utf8"));

// npm platform -> Go target
const PLATFORMS = {
  "linux-x64": ["linux", "amd64"],
  "linux-arm64": ["linux", "arm64"],
  "darwin-x64": ["darwin", "amd64"],
  "darwin-arm64": ["darwin", "arm64"],
  "win32-x64": ["windows", "amd64"],
};

const npm = process.platform === "win32" ? "npm.cmd" : "npm";
const run = (cmd, args, cwd, env) => {
  console.log("+", cmd, args.join(" "));
  // Windows needs a shell to run npm.cmd; other commands must not go through one, or arguments
  // containing spaces (-ldflags "-s -w") would be split.
  execFileSync(cmd, args, { cwd, env: env ?? process.env, stdio: "inherit", shell: cmd === "npm.cmd" });
};

const selected = process.argv.slice(2).length ? process.argv.slice(2) : Object.keys(PLATFORMS);
for (const p of selected) {
  if (!(p in PLATFORMS))
    throw new Error(`unknown platform ${p}; choose from ${Object.keys(PLATFORMS).join(", ")}`);
}

rmSync(staging, { recursive: true, force: true });
mkdirSync(staging, { recursive: true });

for (const platform of selected) {
  const [goos, goarch] = PLATFORMS[platform];
  const [os, cpu] = platform.split("-");
  const dir = join(staging, `sdk-${platform}`);
  const exe = goos === "windows" ? "rtogtor.exe" : "rtogtor";
  mkdirSync(join(dir, "bin"), { recursive: true });
  run(
    "go",
    ["build", "-trimpath", "-ldflags", "-s -w", "-o", join(dir, "bin", exe), "./cmd/rtogtor"],
    join(repo, "engine"),
    {
      ...process.env,
      CGO_ENABLED: "0",
      GOOS: goos,
      GOARCH: goarch,
    },
  );
  chmodSync(join(dir, "bin", exe), 0o755);
  writeFileSync(
    join(dir, "package.json"),
    JSON.stringify(
      {
        name: `@rtogtor/sdk-${platform}`,
        version: pkg.version,
        description: `The RtoGtoR engine for ${platform}, used by @rtogtor/sdk.`,
        license: pkg.license,
        repository: pkg.repository,
        os: [os],
        cpu: [cpu],
        files: ["bin"],
        preferUnplugged: true,
      },
      null,
      2,
    ) + "\n",
  );
  writeFileSync(
    join(dir, "README.md"),
    `# @rtogtor/sdk-${platform}\n\nThe RtoGtoR engine binary for ${platform}. Install \`@rtogtor/sdk\` instead; it depends on this package.\n`,
  );
  run(npm, ["pack", "--pack-destination", out], dir);
}

run(npm, ["run", "build"], here);
run(npm, ["pack", "--pack-destination", out], here);
rmSync(staging, { recursive: true, force: true });

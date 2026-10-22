// Installs the packed @rtogtor/sdk and this platform's engine package into a fresh project,
// like a user would, and exercises the bundled engine (no RTOGTOR_ENGINE_PATH).
import { execFileSync } from "node:child_process";
import { mkdtempSync, readdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = join(dirname(fileURLToPath(import.meta.url)), "..");
const repo = join(here, "..", "..");
const dist = join(repo, "dist", "npm");
const tarballs = readdirSync(dist).filter((f) => f.endsWith(".tgz"));
const main = tarballs.find((f) => /^rtogtor-sdk-\d/.test(f));
const platform = tarballs.find((f) => f.startsWith(`rtogtor-sdk-${process.platform}-${process.arch}-`));
if (!main || !platform) throw new Error(`missing tarballs in ${dist}: ${tarballs.join(", ")}`);

const project = mkdtempSync(join(tmpdir(), "rtogtor-npm-smoke-"));
writeFileSync(
  join(project, "package.json"),
  JSON.stringify({ name: "smoke", private: true, type: "module" }),
);
const env = { ...process.env };
delete env.RTOGTOR_ENGINE_PATH;
const npm = process.platform === "win32" ? "npm.cmd" : "npm";
execFileSync(npm, ["install", "--no-audit", "--no-fund", join(dist, main), join(dist, platform)], {
  cwd: project,
  env,
  stdio: "inherit",
  shell: process.platform === "win32",
});

const repoForJs = JSON.stringify(repo);
writeFileSync(
  join(project, "smoke.mjs"),
  `import { readFileSync } from "node:fs";
import { join } from "node:path";
import { Engine, Mapping } from "@rtogtor/sdk";
const repo = ${repoForJs};
// Plain JavaScript on Node 22 has no \`await using\` syntax (Node 24 does; TypeScript compiles it).
const engine = await Engine.start();
try {
  console.log("installed package engine", engine.info.engineVersion, "protocol", engine.info.protocolVersion);
  const mapping = await Mapping.fromFile(join(repo, "examples", "ecommerce", "mapping.yaml"));
  const metadata = JSON.parse(readFileSync(join(repo, "conformance", "cases", "ecommerce", "expected-schema-metadata.json"), "utf8"));
  const plan = await engine.plan(mapping, metadata);
  console.log("planned", plan.steps.length, "steps, mapping hash", plan.mapping.hash.slice(0, 12));
  if (plan.steps.length !== 13) process.exitCode = 1;
} finally {
  await engine.close();
}
`,
);
execFileSync(process.execPath, ["smoke.mjs"], { cwd: project, env, stdio: "inherit" });
execFileSync(npm, ["exec", "--", "rtogtor", "version"], {
  cwd: project,
  env,
  stdio: "inherit",
  shell: process.platform === "win32",
});
console.log("smoke test passed in", project);

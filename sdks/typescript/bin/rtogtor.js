#!/usr/bin/env node
// `npx rtogtor ...` runs the bundled engine's CLI.
import { spawnSync } from "node:child_process";
import { locateEngine } from "../dist/locate.js";

const result = spawnSync(locateEngine(), process.argv.slice(2), { stdio: "inherit", windowsHide: true });
if (result.error) {
  console.error(result.error.message);
  process.exit(1);
}
process.exit(result.status ?? 1);

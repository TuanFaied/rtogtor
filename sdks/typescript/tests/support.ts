import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

export const REPO = join(dirname(fileURLToPath(import.meta.url)), "..", "..", "..");

export function repoPath(...parts: string[]): string {
  return join(REPO, ...parts);
}

export function readJson(...parts: string[]): unknown {
  return JSON.parse(readFileSync(repoPath(...parts), "utf8"));
}

export const integrationConfigured = Boolean(
  process.env["RTOGTOR_TEST_POSTGRES_URL"] && process.env["RTOGTOR_TEST_NEO4J_URL"],
);

/** RFC 8785 canonical JSON for RtoGtoR documents (integers only). */
export function jcs(value: unknown): string {
  if (value === null) return "null";
  if (typeof value === "boolean") return value ? "true" : "false";
  if (typeof value === "number") {
    if (!Number.isInteger(value)) throw new Error(`unexpected non-integer ${value}`);
    return String(value);
  }
  if (typeof value === "string") return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(jcs).join(",")}]`;
  if (typeof value === "object") {
    // JavaScript string comparison is by UTF-16 code units, as RFC 8785 requires.
    const entries = Object.entries(value as Record<string, unknown>)
      .filter(([, v]) => v !== undefined)
      .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
    return `{${entries.map(([k, v]) => `${JSON.stringify(k)}:${jcs(v)}`).join(",")}}`;
  }
  throw new Error(`unsupported value of type ${typeof value}`);
}

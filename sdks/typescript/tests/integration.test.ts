// End-to-end tests against disposable databases. PostgreSQL must hold the e-commerce example in
// schema public; Neo4j nodes are written under labels with a random suffix and deleted
// afterwards (cleanup failures fail the test).
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomUUID } from "node:crypto";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { Connection, ErrorCodes, Mapping, Migration, type Progress, RtoGtoRError } from "../src/index.js";
import { integrationConfigured, repoPath } from "./support.js";

const source = (): Connection => Connection.postgresql({ urlEnv: "RTOGTOR_TEST_POSTGRES_URL" });
const destination = (): Connection => Connection.neo4j({ urlEnv: "RTOGTOR_TEST_NEO4J_URL" });

/** Neo4j HTTP API (cleanup only). It answers 200 even when a statement fails. */
async function cypher(statements: string[]): Promise<void> {
  const bolt = new URL(process.env["RTOGTOR_TEST_NEO4J_URL"] ?? "");
  const http = process.env["RTOGTOR_TEST_NEO4J_HTTP"] ?? `http://${bolt.hostname}:7474`;
  const auth = Buffer.from(
    `${decodeURIComponent(bolt.username)}:${decodeURIComponent(bolt.password)}`,
  ).toString("base64");
  const res = await fetch(`${http}/db/neo4j/tx/commit`, {
    method: "POST",
    headers: { "Content-Type": "application/json", Authorization: `Basic ${auth}` },
    body: JSON.stringify({ statements: statements.map((statement) => ({ statement })) }),
  });
  const body = (await res.json()) as { errors: unknown[] };
  if (!res.ok || body.errors.length > 0)
    throw new Error(`Neo4j cleanup failed: ${JSON.stringify(body.errors)}`);
}

describe.skipIf(!integrationConfigured)("integration", () => {
  let labels: string[] = [];
  let stateDir = "";
  let mapping: Mapping;

  beforeEach(async () => {
    const suffix = randomUUID().replaceAll("-", "").slice(0, 8);
    const base = await Mapping.fromFile(repoPath("examples", "ecommerce", "mapping.yaml"));
    labels = [];
    const entities = base.document.entities.map((e) => {
      const l = e.labels.map((x) => `${x}_${suffix}`);
      labels.push(...l);
      return { ...e, labels: l };
    });
    mapping = Mapping.fromDocument({ ...base.document, entities });
    stateDir = mkdtempSync(join(tmpdir(), "rtogtor-ts-"));
  });

  afterEach(async () => {
    // Data and schema changes cannot share a transaction.
    await cypher(labels.map((l) => `MATCH (n:\`${l}\`) DETACH DELETE n`));
    await cypher(labels.map((l) => `DROP CONSTRAINT \`rtg_${l}_rtg_id\` IF EXISTS`));
    rmSync(stateDir, { recursive: true, force: true });
  });

  it("plans, dry-runs, migrates and verifies", async () => {
    await using m = new Migration({ source: source(), destination: destination(), mapping });
    expect((await m.plan()).steps).toHaveLength(13);

    const dry = await m.run({ dryRun: true, stateDir });
    expect(dry.dryRun).toBe(true);
    expect(dry.status).toBe("succeeded");

    const progress: Progress[] = [];
    const report = await m.run({ stateDir, batchSize: 2, onProgress: (p) => progress.push(p) });
    expect(report.status).toBe("succeeded");
    expect(report.totals.written).toBe(24);
    expect(progress.some((p) => p.stepId === "nodes:Customer" && p.done)).toBe(true);

    expect((await m.verify(report.runId, { stateDir })).status).toBe("passed");
    expect((await m.report(report.runId, stateDir)).verification?.status).toBe("passed");
    expect(await m.failures(report.runId, stateDir)).toEqual([]);
  });

  it("resumes an aborted run from a new session", async () => {
    let runId: string;
    {
      await using m = new Migration({ source: source(), destination: destination(), mapping });
      const controller = new AbortController();
      const error = await m
        .run({
          stateDir,
          batchSize: 1,
          signal: controller.signal,
          onProgress: () => {
            controller.abort();
          },
        })
        .then(
          () => undefined,
          (e: unknown) => e,
        );
      const cause = (error as Error).cause as RtoGtoRError;
      expect(cause.code).toBe(ErrorCodes.Exec004);
      expect(cause.report?.status).toBe("interrupted");
      runId = cause.report?.runId ?? "";
    }
    await using m = new Migration({ source: source(), destination: destination(), mapping });
    const report = await m.run({ stateDir, resumeRunId: runId });
    expect(report.status).toBe("succeeded");
    expect(report.attempts).toBe(2);
    expect((await m.verify(runId, { stateDir })).status).toBe("passed");
  });
});

// The shared conformance suite (conformance/), through the public API. When
// RTOGTOR_CONFORMANCE_OUT is set, results are written per conformance/runner-protocol.md.
import { createHash } from "node:crypto";
import { readFileSync, writeFileSync } from "node:fs";
import fc from "fast-check";
import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { Engine, type KeyComponent, Mapping, RtoGtoRError, type SchemaMetadata } from "../src/index.js";
import { SDK_VERSION } from "../src/session.js";
import { jcs, readJson, repoPath } from "./support.js";

let engine: Engine;
const results: { case: string; status: string; detail: string | null }[] = [];
const record = (id: string, ok: boolean, detail: string | null = null): void => {
  results.push({ case: id, status: ok ? "passed" : "failed", detail });
};

beforeAll(async () => {
  engine = await Engine.start();
});
afterAll(async () => {
  const out = process.env["RTOGTOR_CONFORMANCE_OUT"];
  if (out) {
    const doc = {
      runnerProtocol: "1",
      sdk: "typescript",
      sdkVersion: SDK_VERSION,
      engineVersion: engine.info.engineVersion,
      specVersion: engine.info.specVersion,
      results: [...results].sort((a, b) => (a.case < b.case ? -1 : a.case > b.case ? 1 : 0)),
    };
    writeFileSync(out, JSON.stringify(doc, null, 2));
  }
  await engine.close();
});

async function errorCode(p: Promise<unknown>): Promise<string> {
  try {
    await p;
    return "no error";
  } catch (e) {
    return e instanceof RtoGtoRError ? e.code : String(e);
  }
}

interface IdentityVectors {
  vectors: {
    name: string;
    entityType: string;
    key: KeyComponent[];
    canonicalKey: KeyComponent[];
    serializationHex: string;
    rtgId: string;
  }[];
  invalid: { name: string; entityType: string; key: KeyComponent[]; errorCode: string }[];
}

describe("conformance", () => {
  it("identity vectors", async () => {
    const doc = readJson("conformance", "vectors", "identity.json") as IdentityVectors;
    const failures: string[] = [];
    for (const v of doc.vectors) {
      const r = await engine.identity(v.entityType, v.key);
      const ok =
        r.rtgId === v.rtgId &&
        r.serializationHex === v.serializationHex &&
        JSON.stringify(r.canonicalKey.map((k) => k.value)) ===
          JSON.stringify(v.canonicalKey.map((k) => k.value));
      record(`vectors/identity/${v.name}`, ok);
      if (!ok) failures.push(v.name);
    }
    for (const v of doc.invalid) {
      const code = await errorCode(engine.identity(v.entityType, v.key));
      record(`vectors/identity/invalid/${v.name}`, code === v.errorCode, code);
      if (code !== v.errorCode) failures.push(`${v.name} -> ${code}`);
    }
    expect(failures).toEqual([]);
  });

  it("value vectors", async () => {
    const doc = readJson("conformance", "vectors", "values.json") as {
      valid: { type: string; input: string; canonical: string }[];
      invalid: { type: string; input: string; errorCode: string }[];
    };
    const failures: string[] = [];
    for (const [i, v] of doc.valid.entries()) {
      const got = await engine.canonicalize(v.type, v.input);
      record(`vectors/values/valid/${String(i).padStart(3, "0")}-${v.type}`, got === v.canonical, got);
      if (got !== v.canonical) failures.push(`${v.type} ${v.input} -> ${got}`);
    }
    for (const [i, v] of doc.invalid.entries()) {
      const code = await errorCode(engine.canonicalize(v.type, v.input));
      record(`vectors/values/invalid/${String(i).padStart(3, "0")}-${v.type}`, code === v.errorCode, code);
      if (code !== v.errorCode) failures.push(`${v.type} ${v.input} -> ${code}`);
    }
    expect(failures).toEqual([]);
  });

  it("e-commerce plan case", async () => {
    const mapping = await Mapping.fromFile(repoPath("examples", "ecommerce", "mapping.yaml"));
    const metadata = readJson(
      "conformance",
      "cases",
      "ecommerce",
      "expected-schema-metadata.json",
    ) as SchemaMetadata;
    const plan = await engine.plan(mapping, metadata);
    const got = jcs(plan);
    const want = readFileSync(
      repoPath("conformance", "cases", "ecommerce", "expected-plan.json"),
      "utf8",
    ).trimEnd();
    record("cases/ecommerce/plan", got === want);
    expect(got).toBe(want);
    expect(plan.steps).toHaveLength(13);
  });
});

/** rtg-id/v1 (specs/identity.md §3), written independently of the engine. */
function referenceRtgId(entity: string, parts: [string, string][]): string {
  const tags: Record<string, string> = { int64: "i64", string: "str" };
  const lp = (s: string): Buffer => {
    const b = Buffer.from(s, "utf8");
    const len = Buffer.alloc(4);
    len.writeUInt32BE(b.length);
    return Buffer.concat([len, b]);
  };
  const count = Buffer.alloc(4);
  count.writeUInt32BE(parts.length);
  const chunks = [Buffer.from("rtg-id/v1\u0000", "ascii"), lp(entity), count];
  for (const [t, v] of parts) chunks.push(Buffer.from(`${tags[t] ?? "?"}:`, "ascii"), lp(v));
  return createHash("sha256").update(Buffer.concat(chunks)).digest("hex");
}

describe("properties", () => {
  it("engine identities match an independent implementation", async () => {
    const component = fc.oneof(
      fc
        .bigInt({ min: -(2n ** 63n), max: 2n ** 63n - 1n })
        .map((n): [string, string] => ["int64", n.toString()]),
      fc.string({ unit: "grapheme", maxLength: 20 }).map((s): [string, string] => ["string", s]),
    );
    await fc.assert(
      fc.asyncProperty(
        fc.string({ unit: "grapheme", minLength: 1, maxLength: 12 }),
        fc.array(component, { minLength: 1, maxLength: 4 }),
        async (entity, parts) => {
          const r = await engine.identity(
            entity,
            parts.map(([type, value]) => ({ type, value })),
          );
          expect(r.rtgId).toBe(referenceRtgId(entity, parts));
        },
      ),
      { numRuns: 150 },
    );
  });
});

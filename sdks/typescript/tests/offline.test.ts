import { describe, expect, it } from "vitest";
import { Connection, ErrorCodeInfos, ErrorCodes, locateEngine, Mapping, RtoGtoRError } from "../src/index.js";
import { isSupportedEngine } from "../src/session.js";
import { readJson, repoPath } from "./support.js";

const MINIMAL = `specVersion: "0.1.0"
name: test
direction: relational-to-graph
source: { kind: postgresql }
destination: { kind: neo4j }
entities:
  - name: Customer
    table: customers
    labels: [Customer]
    key: [id]
    properties:
      - { column: id, property: id, type: int64 }
`;

function rejection(fn: () => unknown): RtoGtoRError {
  try {
    fn();
  } catch (e) {
    if (e instanceof RtoGtoRError) return e;
    throw e;
  }
  throw new Error("expected an RtoGtoRError");
}

describe("mapping", () => {
  it("loads the example", async () => {
    const m = await Mapping.fromFile(repoPath("examples", "ecommerce", "mapping.yaml"));
    expect(m.document.name).toBe("ecommerce");
    expect(m.document.entities.find((e) => e.name === "OrderItem")?.key).toEqual(["order_id", "line_no"]);
    expect(m.document.relationships?.[0]?.foreignKey?.direction).toBe("referenced-to-owner");
  });

  it("gives the same document from YAML and JSON", () => {
    const fromYaml = Mapping.fromYaml(MINIMAL);
    expect(Mapping.fromJson(fromYaml.toJson()).document).toEqual(fromYaml.document);
  });

  it.each([
    ['specVersion: "0.1.0"', "specVersion: 0.1", ErrorCodes.Map001, "/specVersion"],
    ['specVersion: "0.1.0"', 'specVersion: "0.2.0"', ErrorCodes.Spec001, "/specVersion"],
    ["type: int64 }", "type: integer }", ErrorCodes.Map001, "/entities/0/properties/0/type"],
    ["name: test", "name: test\nname: again", ErrorCodes.Map002, undefined],
  ])("rejects %s -> %s with %s", (from, to, code, path) => {
    const e = rejection(() => Mapping.fromYaml(MINIMAL.replace(from, to)));
    expect(e.code).toBe(code);
    expect(e.severity).toBe("error");
    if (path !== undefined) expect([...e.diagnostics.map((d) => d.path), e.path]).toContain(path);
  });

  it("follows YAML 1.2, not 1.1", () => {
    expect(Mapping.fromYaml(MINIMAL.replace("name: test", "name: yes")).document.name).toBe("yes");
  });

  it("rejects duplicate JSON keys", () => {
    const e = rejection(() => Mapping.fromJson('{"name": "a", "name": "b"}'));
    expect(e.code).toBe(ErrorCodes.Map002);
    expect(e.message).toMatch(/invalid JSON/);
  });

  it("reports every schema problem", () => {
    const e = rejection(() => Mapping.fromJson('{"specVersion": "0.1.0", "name": "x"}'));
    expect(e.code).toBe(ErrorCodes.Map001);
    expect(e.diagnostics.length).toBeGreaterThan(0);
  });

  it("can be built in code", () => {
    const doc = { ...Mapping.fromYaml(MINIMAL).document, name: "renamed" };
    expect(Mapping.fromDocument(doc).document.name).toBe("renamed");
  });
});

describe("error codes", () => {
  it("match the specification", () => {
    const spec = readJson("specs", "error-codes.json") as {
      codes: { code: string; severity: string; retryable: boolean }[];
    };
    expect(Object.keys(ErrorCodeInfos)).toHaveLength(spec.codes.length);
    for (const c of spec.codes) {
      expect(ErrorCodeInfos[c.code]?.severity).toBe(c.severity);
      expect(ErrorCodeInfos[c.code]?.retryable).toBe(c.retryable);
    }
    expect(new RtoGtoRError(ErrorCodes.Conn001, "x").retryable).toBe(true);
  });
});

describe("connections", () => {
  it("never show credentials", () => {
    const pg = Connection.postgresql({ url: "postgres://app:S3cret@db/x" });
    const neo = Connection.neo4j({ uri: "neo4j://db:7687", user: "neo4j", password: "Pa55word" });
    expect(String(pg)).toBe("postgresql connection");
    expect(JSON.stringify({ pg, neo })).not.toMatch(/S3cret|Pa55word/);
    const env: Record<string, string> = {};
    const wire = JSON.stringify([pg.toWire(env), neo.toWire(env)]);
    expect(wire).not.toMatch(/S3cret|Pa55word/);
    expect(Object.values(env)).toContain("postgres://app:S3cret@db/x");
    expect(() => pg.toWire(undefined)).toThrow(/literal credentials/);
  });

  it("check their arguments", () => {
    expect(() => Connection.postgresql({})).toThrow(/exactly one/);
    expect(() => Connection.postgresql({ url: "mysql://x" })).toThrow(/postgres:\/\//);
    expect(() => Connection.neo4j({ uri: "neo4j://u:p@host:7687" })).toThrow(/credentials/);
    const wire = Connection.neo4j({ uri: "bolt://localhost:7687", authEnv: "NEO4J" }).toWire(undefined);
    expect(wire.userEnv).toBe("NEO4J_USER");
    expect(wire.passwordEnv).toBe("NEO4J_PASSWORD");
  });
});

describe("engine discovery", () => {
  it.each([
    ["0.1.0-dev", true],
    ["0.1.7", true],
    ["0.2.0", false],
    ["1.1.0", false],
    ["garbage", false],
  ])("supports engine %s: %s", (v, ok) => {
    expect(isSupportedEngine(v)).toBe(ok);
  });

  it("reports a missing engine clearly", () => {
    const e = rejection(() => locateEngine(repoPath("no-such-rtogtor")));
    expect(e.code).toBe(ErrorCodes.Proto008);
  });
});

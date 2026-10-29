import { afterAll, beforeAll, describe, expect, it } from "vitest";
import { Connection, Engine, ErrorCodes, Mapping, RtoGtoRError } from "../src/index.js";

let engine: Engine;
beforeAll(async () => {
  engine = await Engine.start();
});
afterAll(async () => {
  await engine.close();
});

async function rejection(p: Promise<unknown>): Promise<RtoGtoRError> {
  try {
    await p;
  } catch (e) {
    if (e instanceof RtoGtoRError) return e;
    throw e;
  }
  throw new Error("expected an RtoGtoRError");
}

const BLACKHOLE = "postgres://u:p@10.255.255.1:5432/db?connect_timeout=60";

describe("engine", () => {
  it("reports versions", () => {
    expect(engine.info.specVersion).toBe("0.1.0");
    expect(engine.info.identityAlgorithm).toBe("rtg-id/v1");
    expect(engine.info.protocolVersion).toBe("1.0");
    expect(engine.info.engineVersion).toMatch(/^0\.1\./);
  });

  it("returns errors with codes", async () => {
    const e = await rejection(engine.inspect(Connection.postgresql({ urlEnv: "RTOGTOR_TEST_SURELY_UNSET" })));
    expect(e.code).toBe(ErrorCodes.Conn002);
    expect(e.message).toContain("RTOGTOR_TEST_SURELY_UNSET");
    expect((await rejection(engine.canonicalize("int64", "1.5"))).code).toBe(ErrorCodes.Val001);
  });

  it("validates semantics", async () => {
    const mapping = Mapping.fromYaml(`specVersion: "0.1.0"
name: bad
direction: relational-to-graph
source: { kind: postgresql }
destination: { kind: neo4j }
entities:
  - name: OrderItem
    table: order_items
    labels: [OrderItem]
    key: [order_id, line_no]
    properties: [{ column: order_id, property: orderId, type: int64 }]
`);
    const result = await engine.validateMapping(mapping);
    expect(result.valid).toBe(false);
    expect(result.diagnostics).toContainEqual(
      expect.objectContaining({ code: ErrorCodes.Map005, path: "/entities/0/key/1" }),
    );
  });

  it("cancels a request through AbortSignal and stays usable", async () => {
    await using e = await Engine.start({ env: { RTOGTOR_TEST_BLACKHOLE: BLACKHOLE } });
    const start = Date.now();
    const error = await e
      .inspect(Connection.postgresql({ urlEnv: "RTOGTOR_TEST_BLACKHOLE" }), {
        signal: AbortSignal.timeout(300),
      })
      .then(
        () => undefined,
        (err: unknown) => err,
      );
    expect(error).toBeInstanceOf(Error);
    expect((error as Error).name).toBe("TimeoutError");
    expect(((error as Error).cause as RtoGtoRError).code).toBe(ErrorCodes.Exec004);
    expect(Date.now() - start).toBeLessThan(10_000);
    expect(await e.canonicalize("decimal", "0012.500")).toBe("12.5");
  });

  it("stops the process on close", async () => {
    const e = await Engine.start();
    await e.close();
    expect(e.session.exited).toBe(true);
    expect((await rejection(e.canonicalize("int64", "1"))).code).toBe(ErrorCodes.Proto008);
  });

  it("matches concurrent requests to their responses", async () => {
    const results = await Promise.all(
      Array.from({ length: 50 }, (_, i) => engine.canonicalize("int64", `+00${i}`)),
    );
    expect(results).toEqual(Array.from({ length: 50 }, (_, i) => String(i)));
  });

  it("reports unknown runs", async () => {
    expect((await rejection(engine.report("01234567-89ab-7def-8123-456789abcdef", "."))).code).toBe(
      ErrorCodes.Run001,
    );
  });
});

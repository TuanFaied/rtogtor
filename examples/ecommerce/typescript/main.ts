// Migrates the e-commerce example from PostgreSQL to Neo4j and back,
// verifying every value both ways.
//
//   export POSTGRES_URL='postgres://user:password@localhost:5432/ecommerce'
//   export NEO4J_URL='neo4j://neo4j:password@localhost:7687'
//   npm install && npm start
//
// Load ../schema.sql into the PostgreSQL database first. The run is safe to
// repeat: writes are idempotent.
import { fileURLToPath } from "node:url";
import { Connection, Mapping, Migration, RtoGtoRError } from "@rtogtor/sdk";

const postgres = Connection.postgresql({ urlEnv: "POSTGRES_URL" });
const neo4j = Connection.neo4j({ urlEnv: "NEO4J_URL" });

async function migrate(
  file: string,
  source: Connection,
  destination: Connection,
): Promise<void> {
  const mapping = await Mapping.fromFile(
    fileURLToPath(new URL(`../${file}`, import.meta.url)),
  );
  // `await using m = …` works when compiled with tsc; Node 22 cannot run it
  // from type-stripped source, so close explicitly.
  const m = new Migration({ source, destination, mapping });
  try {
    await run(m, mapping.document.name);
  } finally {
    await m.close();
  }
}

async function run(m: Migration, name: string): Promise<void> {
  const plan = await m.plan();
  console.log(
    `${name}: ${plan.source.product} -> ${plan.destination.product}, ${plan.steps.length} steps`,
  );

  let report;
  try {
    report = await m.run({
      batchSize: 500,
      onProgress: (p) => {
        if (p.done)
          console.log(
            `  ${p.stepId.padEnd(36)} ${String(p.written).padStart(6)} written`,
          );
      },
    });
  } catch (e) {
    if (e instanceof RtoGtoRError && e.report !== undefined) {
      throw new Error(
        `run ${e.report.runId} stopped (${e.message}); resume it with resumeRunId: "${e.report.runId}"`,
        {
          cause: e,
        },
      );
    }
    throw e;
  }

  const verification = await m.verify(report.runId, { values: true });
  console.log(
    `  run ${report.runId} ${report.status}, ${report.totals.written} records written; verification ${verification.status}`,
  );
  if (report.status !== "succeeded" || verification.status !== "passed") {
    throw new Error(
      `${name} did not round-trip cleanly; see rtogtor report --run-id ${report.runId}`,
    );
  }
}

// PostgreSQL -> Neo4j, then Neo4j -> PostgreSQL with the reverse mapping.
await migrate("mapping.yaml", postgres, neo4j);
await migrate("mapping-reverse.yaml", neo4j, postgres);

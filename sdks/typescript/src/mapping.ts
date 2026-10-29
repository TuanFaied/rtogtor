import { readFile } from "node:fs/promises";
import { basename, extname } from "node:path";
import { Ajv2020, type ErrorObject } from "ajv/dist/2020.js";
import { isMap, isScalar, isSeq, parseAllDocuments, type Node as YamlNode } from "yaml";
import { ErrorCodes } from "./generated/errorCodes.js";
import mappingSchema from "./generated/mapping.schema.json" with { type: "json" };
import type { MappingDocument } from "./generated/model.js";
import { type Diagnostic, RtoGtoRError } from "./errors.js";

const ajv = new Ajv2020({ allErrors: true, strict: false });
const validateMapping = ajv.compile<MappingDocument>(mappingSchema);

function diagnostics(errors: readonly ErrorObject[]): Diagnostic[] {
  const seen = new Set<string>();
  const out: Diagnostic[] = [];
  for (const e of errors) {
    const message = e.message ?? "invalid";
    const key = `${e.instancePath}\u0000${message}`;
    if (!seen.has(key)) {
      seen.add(key);
      out.push({
        code: ErrorCodes.Map001,
        severity: "error",
        retryable: false,
        message,
        path: e.instancePath,
      });
    }
  }
  return out.sort((a, b) => (a.path ?? "").localeCompare(b.path ?? "") || a.message.localeCompare(b.message));
}

/**
 * A mapping (specs/mapping.schema.json), loaded from JSON or YAML and validated locally against
 * the schema. Semantic checks (unique names, key coverage, foreign-key arity) are the engine's:
 * see `Engine.validateMapping`, or simply plan the migration.
 */
export class Mapping {
  /** The typed mapping. */
  readonly document: MappingDocument;

  private constructor(json: unknown) {
    if (typeof json === "object" && json !== null && "specVersion" in json) {
      const v = json.specVersion;
      // specVersion 1.0.x, or 0.1.x: the draft that became 1.0.0 without schema changes.
      if (typeof v === "string" && !v.startsWith("1.0.") && !v.startsWith("0.1.")) {
        throw new RtoGtoRError(
          ErrorCodes.Spec001,
          `mapping declares specVersion ${v}; this SDK supports 1.0.x (and 0.1.x)`,
          {
            path: "/specVersion",
          },
        );
      }
    }
    if (!validateMapping(json)) {
      const problems = diagnostics(validateMapping.errors ?? []);
      const first = problems[0] ?? { message: "invalid mapping", path: "" };
      const more =
        problems.length > 1
          ? ` (and ${problems.length - 1} more problem${problems.length > 2 ? "s" : ""})`
          : "";
      throw new RtoGtoRError(ErrorCodes.Map001, first.message + more, {
        path: first.path,
        diagnostics: problems,
      });
    }
    this.document = json;
  }

  /** Loads a `.json`, `.yaml` or `.yml` file. */
  static async fromFile(path: string): Promise<Mapping> {
    let text: string;
    try {
      text = await readFile(path, "utf8");
    } catch (e) {
      throw new RtoGtoRError(ErrorCodes.Map002, `cannot read the mapping file: ${String(e)}`, { cause: e });
    }
    switch (extname(path).toLowerCase()) {
      case ".json":
        return Mapping.fromJson(text);
      case ".yaml":
      case ".yml":
        return Mapping.fromYaml(text);
      default:
        throw new RtoGtoRError(
          ErrorCodes.Map002,
          `cannot infer the format of "${basename(path)}": use a .json, .yaml or .yml extension`,
        );
    }
  }

  /** Parses a JSON mapping. Duplicate object keys are rejected. */
  static fromJson(text: string): Mapping {
    let json: unknown;
    try {
      // The YAML 1.2 parser accepts JSON and, unlike JSON.parse, rejects duplicate keys.
      json = parseSingleDocument(text);
    } catch (e) {
      if (e instanceof RtoGtoRError) {
        throw new RtoGtoRError(
          ErrorCodes.Map002,
          e.message.replace(/^RTG-MAP-002: (invalid YAML: )?/, "invalid JSON: "),
        );
      }
      throw e;
    }
    return new Mapping(json);
  }

  /**
   * Parses a YAML mapping. Plain scalars are typed with the YAML 1.2 core schema, as the engine
   * does: `specVersion: 0.1` is a number (and invalid); quote it.
   */
  static fromYaml(text: string): Mapping {
    return new Mapping(parseSingleDocument(text));
  }

  /** Creates a mapping from a typed document, for example one built in code. */
  static fromDocument(document: MappingDocument): Mapping {
    return new Mapping(JSON.parse(JSON.stringify(document)));
  }

  /** The mapping as JSON. */
  toJson(): string {
    return JSON.stringify(this.document);
  }
}

function parseSingleDocument(text: string): unknown {
  const docs = parseAllDocuments(text, {
    version: "1.2",
    schema: "core",
    uniqueKeys: true,
    intAsBigInt: true,
  });
  if (!Array.isArray(docs)) {
    throw new RtoGtoRError(ErrorCodes.Map002, "mapping document is empty");
  }
  if (docs.length !== 1) {
    throw new RtoGtoRError(
      ErrorCodes.Map002,
      docs.length === 0
        ? "mapping document is empty"
        : "a mapping file must contain exactly one YAML document",
    );
  }
  const doc = docs[0];
  if (doc === undefined) {
    throw new RtoGtoRError(ErrorCodes.Map002, "mapping document is empty");
  }
  const firstError = doc.errors[0];
  if (firstError !== undefined) {
    throw new RtoGtoRError(ErrorCodes.Map002, `invalid YAML: ${firstError.message}`);
  }
  return toJsonValue(doc.contents);
}

/** Converts YAML nodes to JSON values, rejecting what JSON cannot represent. */
function toJsonValue(node: YamlNode | null): unknown {
  if (node === null) return null;
  if (isMap(node)) {
    const out: Record<string, unknown> = {};
    for (const pair of node.items) {
      const key = isScalar(pair.key) ? pair.key.value : undefined;
      if (typeof key !== "string") {
        throw new RtoGtoRError(ErrorCodes.Map002, "YAML mapping keys must be strings");
      }
      out[key] = toJsonValue(pair.value as YamlNode | null);
    }
    return out;
  }
  if (isSeq(node)) {
    return node.items.map((item) => toJsonValue(item as YamlNode | null));
  }
  if (isScalar(node)) {
    const v = node.value;
    if (typeof v === "bigint") {
      if (v > BigInt(Number.MAX_SAFE_INTEGER) || v < BigInt(Number.MIN_SAFE_INTEGER)) {
        throw new RtoGtoRError(ErrorCodes.Map002, `integer ${v} is too large to be represented exactly`);
      }
      return Number(v);
    }
    if (typeof v === "number" && !Number.isFinite(v)) {
      throw new RtoGtoRError(
        ErrorCodes.Map002,
        `YAML value ${String(node.source)} cannot be represented as JSON`,
      );
    }
    return v;
  }
  throw new RtoGtoRError(ErrorCodes.Map002, "unsupported YAML node");
}

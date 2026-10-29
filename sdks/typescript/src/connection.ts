import type { WireConnection } from "./generated/model.js";

/** Options for {@link Connection.postgresql}. Pass exactly one. */
export interface PostgresqlOptions {
  /** A `postgres://` URL. Prefer `urlEnv` so the URL stays out of your code. */
  url?: string;
  /** Name of an environment variable holding the URL. */
  urlEnv?: string;
}

/** Options for {@link Connection.neo4j}. Pass `uri` or `urlEnv`. */
export interface Neo4jOptions {
  /** A `neo4j://` or `bolt://` URI without credentials. */
  uri?: string;
  /** Prefix of the environment variables `{authEnv}_USER` and `{authEnv}_PASSWORD`. */
  authEnv?: string;
  /** Alternatively, a variable holding the full URL, which may contain `user:password@`. */
  urlEnv?: string;
  /** Literal user name (prefer `authEnv`). */
  user?: string;
  /** Literal password (prefer `authEnv`). */
  password?: string;
  /** Database name, if not the server default. */
  database?: string;
}

const secrets = Symbol("secrets");

/**
 * Identifies a database. Credentials never appear in protocol messages or output: the engine
 * reads them from environment variables of its process. Literal credentials are handed to the
 * engine through private environment variables.
 */
export class Connection {
  readonly product: "postgresql" | "neo4j";
  readonly database: string | undefined;
  private readonly [secrets]: {
    urlEnv?: string | undefined;
    url?: string | undefined;
    uri?: string | undefined;
    userEnv?: string | undefined;
    passwordEnv?: string | undefined;
    user?: string | undefined;
    password?: string | undefined;
  };

  private constructor(product: "postgresql" | "neo4j", s: Connection[typeof secrets], database?: string) {
    this.product = product;
    this.database = database;
    this[secrets] = s;
  }

  /** A PostgreSQL connection. */
  static postgresql(options: PostgresqlOptions): Connection {
    if ((options.url === undefined) === (options.urlEnv === undefined)) {
      throw new TypeError("Pass exactly one of url and urlEnv.");
    }
    if (options.url !== undefined && !/^postgres(ql)?:\/\//.test(options.url)) {
      throw new TypeError("A PostgreSQL URL must start with postgres:// or postgresql://.");
    }
    return new Connection("postgresql", { url: options.url, urlEnv: options.urlEnv });
  }

  /** A Neo4j connection. */
  static neo4j(options: Neo4jOptions): Connection {
    const { uri, authEnv, urlEnv, user, password, database } = options;
    if ((uri === undefined) === (urlEnv === undefined)) {
      throw new TypeError("Pass exactly one of uri and urlEnv.");
    }
    if (urlEnv !== undefined && (authEnv ?? user ?? password) !== undefined) {
      throw new TypeError("With urlEnv, put the credentials in the URL.");
    }
    if (authEnv !== undefined && (user ?? password) !== undefined) {
      throw new TypeError("Pass either authEnv or user/password, not both.");
    }
    if (uri !== undefined) {
      const parsed = URL.canParse(uri) ? new URL(uri) : undefined;
      if (parsed && (parsed.username || parsed.password)) {
        throw new TypeError("The URI must not contain credentials; use authEnv or user/password.");
      }
    }
    return new Connection(
      "neo4j",
      {
        uri,
        urlEnv,
        userEnv: authEnv === undefined ? undefined : `${authEnv}_USER`,
        passwordEnv: authEnv === undefined ? undefined : `${authEnv}_PASSWORD`,
        user,
        password,
      },
      database,
    );
  }

  /** Never includes the URL or credentials. */
  toString(): string {
    return `${this.product} connection`;
  }

  /** Keeps credentials out of JSON.stringify. */
  toJSON(): string {
    return this.toString();
  }

  /** Keeps credentials out of console.log / util.inspect. */
  [Symbol.for("nodejs.util.inspect.custom")](): string {
    return `<${this.toString()}>`;
  }

  /**
   * Protocol form. Literal secrets are assigned to private environment variables in `env`.
   * @internal
   */
  toWire(env: Record<string, string> | undefined): WireConnection {
    const s = this[secrets];
    const secret = (value: string): string => {
      if (env === undefined) {
        throw new Error(
          "Connections with literal credentials can only be used through Migration; use urlEnv or authEnv with Engine methods.",
        );
      }
      const name = `RTOGTOR_SDK_SECRET_${Object.keys(env).length}`;
      env[name] = value;
      return name;
    };
    const wire: WireConnection = { product: this.product };
    if (s.urlEnv !== undefined || s.url !== undefined) {
      wire.urlEnv = s.urlEnv ?? secret(s.url ?? "");
    } else {
      wire.uri = s.uri;
      const userEnv = s.userEnv ?? (s.user === undefined ? undefined : secret(s.user));
      const passwordEnv = s.passwordEnv ?? (s.password === undefined ? undefined : secret(s.password));
      if (userEnv !== undefined) wire.userEnv = userEnv;
      if (passwordEnv !== undefined) wire.passwordEnv = passwordEnv;
    }
    if (this.database !== undefined) wire.database = this.database;
    return wire;
  }
}

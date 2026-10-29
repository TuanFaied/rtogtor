namespace RtoGtoR;

/// <summary>
/// Identifies a database. Credentials are never sent in protocol messages and never
/// printed: the engine reads them from environment variables of its process. Literal
/// credentials passed to a factory method are handed to the engine process through
/// private environment variables.
/// </summary>
public sealed class Connection
{
    private readonly string? _urlEnv;
    private readonly string? _url;
    private readonly string? _uri;
    private readonly string? _userEnv;
    private readonly string? _passwordEnv;
    private readonly string? _user;
    private readonly string? _password;

    private Connection(string product, string? urlEnv, string? url, string? uri, string? userEnv, string? passwordEnv, string? user, string? password, string? database)
    {
        Product = product;
        _urlEnv = urlEnv;
        _url = url;
        _uri = uri;
        _userEnv = userEnv;
        _passwordEnv = passwordEnv;
        _user = user;
        _password = password;
        Database = database;
    }

    /// <summary>The database product: <c>postgresql</c> or <c>neo4j</c>.</summary>
    public string Product { get; }

    /// <summary>The Neo4j database, if not the server default.</summary>
    public string? Database { get; }

    /// <summary>A PostgreSQL connection from a URL, or from an environment variable holding the URL.</summary>
    /// <param name="url">A <c>postgres://</c> URL. Prefer <paramref name="urlEnv"/> so the URL stays out of your code.</param>
    /// <param name="urlEnv">Name of an environment variable holding the URL.</param>
    public static Connection PostgreSql(string? url = null, string? urlEnv = null)
    {
        if ((url is null) == (urlEnv is null))
        {
            throw new ArgumentException("Pass exactly one of url and urlEnv.");
        }
        if (url is not null && !url.StartsWith("postgres://", StringComparison.Ordinal) && !url.StartsWith("postgresql://", StringComparison.Ordinal))
        {
            throw new ArgumentException("A PostgreSQL URL must start with postgres:// or postgresql://.", nameof(url));
        }
        return new Connection("postgresql", urlEnv, url, null, null, null, null, null, null);
    }

    /// <summary>A Neo4j connection.</summary>
    /// <param name="uri">A <c>neo4j://</c> or <c>bolt://</c> URI without credentials.</param>
    /// <param name="authEnv">Prefix of environment variables <c>{authEnv}_USER</c> and <c>{authEnv}_PASSWORD</c>.</param>
    /// <param name="urlEnv">Alternatively, an environment variable holding the full URL, which may contain <c>user:password@</c>.</param>
    /// <param name="user">Literal user name (prefer <paramref name="authEnv"/>).</param>
    /// <param name="password">Literal password (prefer <paramref name="authEnv"/>).</param>
    /// <param name="database">Database name, if not the server default.</param>
    public static Connection Neo4j(string? uri = null, string? authEnv = null, string? urlEnv = null, string? user = null, string? password = null, string? database = null)
    {
        if ((uri is null) == (urlEnv is null))
        {
            throw new ArgumentException("Pass exactly one of uri and urlEnv.");
        }
        if (urlEnv is not null && (authEnv ?? user ?? password) is not null)
        {
            throw new ArgumentException("With urlEnv, put the credentials in the URL.");
        }
        if (authEnv is not null && (user ?? password) is not null)
        {
            throw new ArgumentException("Pass either authEnv or user/password, not both.");
        }
        if (uri is not null && Uri.TryCreate(uri, UriKind.Absolute, out var parsed) && parsed.UserInfo.Length > 0)
        {
            throw new ArgumentException("The URI must not contain credentials; use authEnv or user/password.", nameof(uri));
        }
        return new Connection("neo4j", urlEnv, null, uri,
            authEnv is null ? null : authEnv + "_USER", authEnv is null ? null : authEnv + "_PASSWORD",
            user, password, database);
    }

    /// <summary>Never includes the URL or credentials.</summary>
    public override string ToString() => Product + " connection";

    /// <summary>Whether starting the engine needs private environment variables for literal credentials.</summary>
    internal bool HasLiteralSecrets => _url is not null || _user is not null || _password is not null;

    /// <summary>
    /// Builds the protocol representation. Literal secrets are assigned to private environment
    /// variables in <paramref name="env"/>, which the caller passes to the engine process.
    /// </summary>
    internal Model.WireConnection ToWire(IDictionary<string, string>? env)
    {
        string Secret(string value)
        {
            if (env is null)
            {
                throw new InvalidOperationException(
                    "Connections with literal credentials can only be used through Migration. Use urlEnv or authEnv with Engine methods.");
            }
            var name = $"RTOGTOR_SDK_SECRET_{env.Count}";
            env[name] = value;
            return name;
        }

        if (_urlEnv is not null || _url is not null)
        {
            return new Model.WireConnection { Product = Product, UrlEnv = _urlEnv ?? Secret(_url!), Database = Database };
        }
        return new Model.WireConnection
        {
            Product = Product,
            Uri = _uri,
            UserEnv = _userEnv ?? (_user is null ? null : Secret(_user)),
            PasswordEnv = _passwordEnv ?? (_password is null ? null : Secret(_password)),
            Database = Database,
        };
    }
}

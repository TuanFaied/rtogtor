using System.Text.Json;
using System.Text.Json.Nodes;
using RtoGtoR.Internal;
using RtoGtoR.Model;

namespace RtoGtoR;

/// <summary>Options for starting the engine.</summary>
public sealed class EngineOptions
{
    /// <summary>
    /// Path of the rtogtor binary. Defaults to <c>RTOGTOR_ENGINE_PATH</c>, then the binary bundled
    /// in the package for the current platform.
    /// </summary>
    public string? EnginePath { get; init; }

    /// <summary>Extra environment variables for the engine process, for example connection URLs.</summary>
    public IDictionary<string, string> Environment { get; init; } = new Dictionary<string, string>(StringComparer.Ordinal);

    /// <summary>How long to wait for the engine to exit on dispose before killing it.</summary>
    public TimeSpan ShutdownTimeout { get; init; } = TimeSpan.FromSeconds(5);
}

/// <summary>
/// A running RtoGtoR engine (a child process). All migration logic runs in the engine; this
/// class only sends requests. Dispose it to stop the process.
/// </summary>
/// <remarks>
/// Connections built from literal credentials can only be used through <see cref="Migration"/>;
/// with <see cref="Engine"/>, use connections that name environment variables.
/// </remarks>
public sealed class Engine : IAsyncDisposable
{
    private readonly EngineSession _session;

    private Engine(EngineSession session) => _session = session;

    /// <summary>Engine versions this SDK supports.</summary>
    public static string SupportedEngineVersions => EngineSession.SupportedEngineRange;

    /// <summary>Versions reported by the engine.</summary>
    public HandshakeResult Info => _session.Info;

    internal EngineSession Session => _session;

    /// <summary>Starts the engine and checks that its version is supported.</summary>
    /// <exception cref="RtoGtoRException">RTG-PROTO-008 if the engine cannot be started; RTG-PROTO-007 if its version is not supported.</exception>
    public static async Task<Engine> StartAsync(EngineOptions? options = null, CancellationToken cancellationToken = default)
    {
        var session = await EngineSession.StartAsync(options ?? new EngineOptions(), new Dictionary<string, string>(), cancellationToken).ConfigureAwait(false);
        return new Engine(session);
    }

    internal static async Task<Engine> StartAsync(EngineOptions options, IDictionary<string, string> environment, CancellationToken cancellationToken)
    {
        var session = await EngineSession.StartAsync(options, environment, cancellationToken).ConfigureAwait(false);
        return new Engine(session);
    }

    internal async Task<T> CallAsync<T>(string method, object parameters, CancellationToken cancellationToken, Action<string, JsonElement>? onEvent = null)
    {
        var result = await _session.CallAsync(method, parameters, onEvent, cancellationToken).ConfigureAwait(false);
        return RtoGtoRJson.Deserialize<T>(result);
    }

    /// <summary>Runs every validation of a mapping, including semantic checks the schema cannot express.</summary>
    public Task<ValidateMappingResult> ValidateMappingAsync(Mapping mapping, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(mapping);
        return CallAsync<ValidateMappingResult>("validateMapping", new { mapping = mapping.Json }, cancellationToken);
    }

    /// <summary>Inspects a PostgreSQL or Neo4j database's schema (read-only). Schemas apply to PostgreSQL.</summary>
    public Task<SchemaMetadata> InspectAsync(Connection source, IEnumerable<string>? schemas = null, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(source);
        return CallAsync<SchemaMetadata>("inspect", new { source = source.ToWire(null), schemas = schemas?.ToArray() }, cancellationToken);
    }

    /// <summary>
    /// Plans a mapping against inspected metadata, without connecting to any database.
    /// Graph-to-relational mappings also need the destination's metadata (its existing tables).
    /// </summary>
    public Task<Plan> PlanAsync(Mapping mapping, SchemaMetadata sourceMetadata, CancellationToken cancellationToken = default) =>
        PlanAsync(mapping, sourceMetadata, null, cancellationToken);

    /// <inheritdoc cref="PlanAsync(Mapping, SchemaMetadata, CancellationToken)"/>
    public Task<Plan> PlanAsync(Mapping mapping, SchemaMetadata sourceMetadata, SchemaMetadata? destinationMetadata, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(mapping);
        ArgumentNullException.ThrowIfNull(sourceMetadata);
        var source = JsonSerializer.SerializeToElement(sourceMetadata, RtoGtoRJson.Options);
        JsonElement? destination = destinationMetadata is null ? null : JsonSerializer.SerializeToElement(destinationMetadata, RtoGtoRJson.Options);
        return CallAsync<Plan>("plan", new { mapping = mapping.Json, sourceMetadata = source, destinationMetadata = destination }, cancellationToken);
    }

    /// <summary>Reads a run's report.</summary>
    public Task<Report> ReportAsync(string runId, string? stateDirectory = null, CancellationToken cancellationToken = default) =>
        CallAsync<Report>("report", new { runId, stateDir = stateDirectory }, cancellationToken);

    /// <summary>Reads a run's state.</summary>
    public Task<RunState> RunStateAsync(string runId, string? stateDirectory = null, CancellationToken cancellationToken = default) =>
        CallAsync<RunState>("runState", new { runId, stateDir = stateDirectory }, cancellationToken);

    /// <summary>Lists a run's failed records.</summary>
    public async Task<IReadOnlyList<Failure>> FailuresAsync(string runId, string? stateDirectory = null, CancellationToken cancellationToken = default) =>
        (await CallAsync<FailuresResult>("failures", new { runId, stateDir = stateDirectory }, cancellationToken).ConfigureAwait(false)).Failures;

    /// <summary>Derives an <c>rtg_id</c> (specs/identity.md) using the engine's rules.</summary>
    public Task<IdentityResult> IdentityAsync(string entityType, IEnumerable<KeyComponent> key, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(key);
        // A JsonArray keeps explicit nulls: a null key value is meaningful (RTG-ID-001).
        var wire = new JsonArray();
        foreach (var k in key)
        {
            wire.Add(new JsonObject { ["type"] = k.Type, ["value"] = k.Value });
        }
        return CallAsync<IdentityResult>("identity", new { entityType, key = wire }, cancellationToken);
    }

    /// <summary>Returns the canonical text form of a value (specs/values.md).</summary>
    public async Task<string> CanonicalizeAsync(string type, string value, CancellationToken cancellationToken = default) =>
        (await CallAsync<CanonicalizeResult>("canonicalize", new { type, value }, cancellationToken).ConfigureAwait(false)).Canonical;

    /// <summary>Stops the engine process.</summary>
    public ValueTask DisposeAsync() => _session.DisposeAsync();
}

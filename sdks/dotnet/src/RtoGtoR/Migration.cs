using System.Text.Json;
using RtoGtoR.Model;

namespace RtoGtoR;

/// <summary>Options for <see cref="Migration.RunAsync"/>.</summary>
public sealed class RunOptions
{
    /// <summary>Read and convert everything, write nothing (not even constraints).</summary>
    public bool DryRun { get; init; }

    /// <summary>Rows per batch (engine default 1000). Ignored when resuming.</summary>
    public int? BatchSize { get; init; }

    /// <summary>Resume this interrupted run instead of starting a new one.</summary>
    public string? ResumeRunId { get; init; }

    /// <summary>Directory holding run state (engine default <c>.rtogtor</c>).</summary>
    public string? StateDirectory { get; init; }

    /// <summary>Receives a report after every committed batch.</summary>
    public IProgress<Progress>? Progress { get; init; }

    /// <summary>
    /// Retry policy for transient errors. Null keeps a resumed run's policy, or uses the engine
    /// default (3 attempts, 200 ms to 5 s backoff).
    /// </summary>
    public RetryPolicy? Retry { get; init; }
}

/// <summary>Options for <see cref="Migration.VerifyAsync(string, VerifyOptions, CancellationToken)"/>.</summary>
public sealed class VerifyOptions
{
    /// <summary>Directory holding run state (engine default <c>.rtogtor</c>).</summary>
    public string? StateDirectory { get; init; }

    /// <summary>
    /// Also compare every migrated value, not only counts and keys (RTG-VERIFY-005). Reads all
    /// mapped data once more.
    /// </summary>
    public bool Values { get; init; }
}

/// <summary>
/// Migrates a source to a destination with a mapping. It starts its own engine process on
/// first use; disposing it stops the process.
/// </summary>
/// <example>
/// <code>
/// var mapping = await Mapping.FromFileAsync("mapping.yaml");
/// await using var m = new Migration(
///     source: Connection.PostgreSql(urlEnv: "SOURCE_URL"),
///     destination: Connection.Neo4j("bolt://localhost:7687", authEnv: "NEO4J"),
///     mapping: mapping);
/// var plan = await m.PlanAsync();
/// var report = await m.RunAsync(new RunOptions { DryRun = false });
/// </code>
/// </example>
public sealed class Migration : IAsyncDisposable
{
    private readonly Connection _source;
    private readonly Connection? _destination;
    private readonly Mapping _mapping;
    private readonly EngineOptions _options;
    private readonly SemaphoreSlim _startLock = new(1, 1);
    private Engine? _engine;
    private Model.WireConnection? _wireSource;
    private Model.WireConnection? _wireDestination;
    private bool _disposed;

    /// <summary>Creates a migration. The destination may be null for dry runs.</summary>
    public Migration(Connection source, Connection? destination, Mapping mapping, EngineOptions? engineOptions = null)
    {
        _source = source ?? throw new ArgumentNullException(nameof(source));
        _destination = destination;
        _mapping = mapping ?? throw new ArgumentNullException(nameof(mapping));
        _options = engineOptions ?? new EngineOptions();
    }

    private async Task<Engine> EngineAsync(CancellationToken cancellationToken)
    {
        ObjectDisposedException.ThrowIf(_disposed, this);
        await _startLock.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            if (_engine is null)
            {
                // Literal credentials reach the engine as private environment variables.
                var env = new Dictionary<string, string>(StringComparer.Ordinal);
                _wireSource = _source.ToWire(env);
                _wireDestination = _destination?.ToWire(env);
                _engine = await Engine.StartAsync(_options, env, cancellationToken).ConfigureAwait(false);
            }
            return _engine;
        }
        finally
        {
            _startLock.Release();
        }
    }

    /// <summary>
    /// Inspects the databases and plans the migration. Nothing is written. Graph-to-relational
    /// mappings also inspect the destination, to check the tables that already exist.
    /// </summary>
    public async Task<Plan> PlanAsync(CancellationToken cancellationToken = default)
    {
        var engine = await EngineAsync(cancellationToken).ConfigureAwait(false);
        return await engine.CallAsync<Plan>("plan", new { mapping = _mapping.Json, source = _wireSource, destination = _wireDestination }, cancellationToken).ConfigureAwait(false);
    }

    /// <summary>
    /// Runs the migration (or a dry run) and returns its report. If the run fails or is
    /// interrupted, the <see cref="RtoGtoRException"/> carries the report; resume with
    /// <see cref="RunOptions.ResumeRunId"/> = <c>exception.Report.RunId</c>. Cancelling the token
    /// stops the run after the current batch is abandoned; it can be resumed.
    /// </summary>
    public async Task<Report> RunAsync(RunOptions? options = null, CancellationToken cancellationToken = default)
    {
        options ??= new RunOptions();
        if (_destination is null && !options.DryRun)
        {
            throw new InvalidOperationException("A destination connection is required unless DryRun is set.");
        }
        var engine = await EngineAsync(cancellationToken).ConfigureAwait(false);
        Action<string, JsonElement>? onEvent = options.Progress is null ? null : (name, data) =>
        {
            if (name == "progress")
            {
                options.Progress.Report(RtoGtoRJson.Deserialize<Progress>(data));
            }
        };
        return await engine.CallAsync<Report>("migrate", new
        {
            mapping = _mapping.Json,
            source = _wireSource,
            destination = _wireDestination,
            dryRun = options.DryRun,
            batchSize = options.BatchSize,
            resumeRunId = options.ResumeRunId,
            stateDir = options.StateDirectory,
            retry = options.Retry,
        }, cancellationToken, onEvent).ConfigureAwait(false);
    }

    /// <summary>Compares a run's source and destination; the result is added to the run's report.</summary>
    public Task<Verification> VerifyAsync(string runId, string? stateDirectory = null, CancellationToken cancellationToken = default) =>
        VerifyAsync(runId, new VerifyOptions { StateDirectory = stateDirectory }, cancellationToken);

    /// <inheritdoc cref="VerifyAsync(string, string?, CancellationToken)"/>
    public async Task<Verification> VerifyAsync(string runId, VerifyOptions options, CancellationToken cancellationToken = default)
    {
        ArgumentNullException.ThrowIfNull(options);
        if (_destination is null)
        {
            throw new InvalidOperationException("Verification needs a destination connection.");
        }
        var engine = await EngineAsync(cancellationToken).ConfigureAwait(false);
        return await engine.CallAsync<Verification>("verify", new
        {
            runId,
            source = _wireSource,
            destination = _wireDestination,
            stateDir = options.StateDirectory,
            values = options.Values,
        }, cancellationToken).ConfigureAwait(false);
    }

    /// <summary>Reads a run's report.</summary>
    public async Task<Report> ReportAsync(string runId, string? stateDirectory = null, CancellationToken cancellationToken = default) =>
        await (await EngineAsync(cancellationToken).ConfigureAwait(false)).ReportAsync(runId, stateDirectory, cancellationToken).ConfigureAwait(false);

    /// <summary>Lists a run's failed records.</summary>
    public async Task<IReadOnlyList<Failure>> FailuresAsync(string runId, string? stateDirectory = null, CancellationToken cancellationToken = default) =>
        await (await EngineAsync(cancellationToken).ConfigureAwait(false)).FailuresAsync(runId, stateDirectory, cancellationToken).ConfigureAwait(false);

    /// <summary>Stops the engine process.</summary>
    public async ValueTask DisposeAsync()
    {
        if (_disposed)
        {
            return;
        }
        _disposed = true;
        if (_engine is not null)
        {
            await _engine.DisposeAsync().ConfigureAwait(false);
        }
        _startLock.Dispose();
    }
}

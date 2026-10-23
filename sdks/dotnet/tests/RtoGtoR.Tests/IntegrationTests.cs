using System.Net.Http.Headers;
using System.Text;
using RtoGtoR.Model;

namespace RtoGtoR.Tests;

/// <summary>
/// End-to-end tests against disposable databases. The PostgreSQL database must hold the
/// e-commerce example (examples/ecommerce/schema.sql) in schema public; Neo4j nodes are
/// written under labels with a random suffix and deleted afterwards.
/// </summary>
public sealed class IntegrationTests : IAsyncLifetime
{
    private readonly string _suffix = Guid.NewGuid().ToString("N")[..8];
    private readonly string _stateDir = Path.Combine(Path.GetTempPath(), "rtogtor-tests-" + Guid.NewGuid().ToString("N"));
    private readonly List<string> _labels = [];

    public Task InitializeAsync() => Task.CompletedTask;

    public async Task DisposeAsync()
    {
        if (_labels.Count > 0)
        {
            // Data and schema changes cannot share a transaction, so they go in separate requests.
            await CypherAsync(_labels.Select(l => $"MATCH (n:`{l}`) DETACH DELETE n"));
            await CypherAsync(_labels.Select(l => $"DROP CONSTRAINT `rtg_{l}_rtg_id` IF EXISTS"));
        }
        if (Directory.Exists(_stateDir))
        {
            Directory.Delete(_stateDir, recursive: true);
        }
    }

    private static Connection Source => Connection.PostgreSql(urlEnv: "RTOGTOR_TEST_POSTGRES_URL");

    private static Connection Destination => Connection.Neo4j(urlEnv: "RTOGTOR_TEST_NEO4J_URL");

    /// <summary>The example mapping with suffixed labels, built through the typed model.</summary>
    private async Task<Mapping> MappingAsync()
    {
        var m = await Mapping.FromFileAsync(Repo.Path("examples", "ecommerce", "mapping.yaml"));
        var entities = m.Document.Entities.Select(e =>
        {
            var labels = e.Labels.Select(l => l + "_" + _suffix).ToList();
            _labels.AddRange(labels);
            return e with { Labels = labels };
        }).ToList();
        return Mapping.FromDocument(m.Document with { Entities = entities });
    }

    /// <summary>
    /// Runs Cypher through Neo4j's HTTP API (test cleanup only). The API answers 200 even when a
    /// statement fails, so the response's "errors" array is checked: a failed cleanup must fail
    /// the test rather than silently leave data behind.
    /// </summary>
    private static async Task CypherAsync(IEnumerable<string> statements)
    {
        var bolt = new Uri(Environment.GetEnvironmentVariable("RTOGTOR_TEST_NEO4J_URL")!);
        var http = Environment.GetEnvironmentVariable("RTOGTOR_TEST_NEO4J_HTTP") ?? $"http://{bolt.Host}:7474";
        using var client = new HttpClient();
        client.DefaultRequestHeaders.Authorization = new AuthenticationHeaderValue("Basic",
            Convert.ToBase64String(Encoding.UTF8.GetBytes(Uri.UnescapeDataString(bolt.UserInfo))));
        var body = RtoGtoRJson.Serialize(new { statements = statements.Select(s => new { statement = s }) });
        using var content = new StringContent(body, Encoding.UTF8, "application/json");
        using var response = await client.PostAsync(new Uri(http + "/db/neo4j/tx/commit"), content);
        response.EnsureSuccessStatusCode();
        using var result = System.Text.Json.JsonDocument.Parse(await response.Content.ReadAsStringAsync());
        var errors = result.RootElement.GetProperty("errors");
        if (errors.GetArrayLength() > 0)
        {
            throw new InvalidOperationException("Neo4j cleanup failed: " + errors.GetRawText());
        }
    }

    [IntegrationFact]
    public async Task Plan_dry_run_migrate_verify()
    {
        await using var m = new Migration(Source, Destination, await MappingAsync());

        var plan = await m.PlanAsync();
        Assert.Equal(13, plan.Steps.Count);

        var dry = await m.RunAsync(new RunOptions { DryRun = true, StateDirectory = _stateDir });
        Assert.True(dry.DryRun);
        Assert.Equal("succeeded", dry.Status);

        var progress = new List<Progress>();
        var report = await m.RunAsync(new RunOptions
        {
            StateDirectory = _stateDir,
            BatchSize = 2,
            Progress = new SyncProgress<Progress>(progress.Add),
        });
        Assert.Equal("succeeded", report.Status);
        Assert.Equal(24, report.Totals.Written);
        Assert.Contains(progress, p => p.StepId == "nodes:Customer" && p.Done);

        var verification = await m.VerifyAsync(report.RunId, _stateDir);
        Assert.Equal("passed", verification.Status);
        Assert.All(verification.Entities, e => Assert.Equal(0, e.Missing));

        var stored = await m.ReportAsync(report.RunId, _stateDir);
        Assert.Equal("passed", stored.Verification!.Status);
        Assert.Empty(await m.FailuresAsync(report.RunId, _stateDir));
    }

    [IntegrationFact]
    public async Task Cancelled_run_can_be_resumed()
    {
        var mapping = await MappingAsync();
        string runId;
        await using (var m = new Migration(Source, Destination, mapping))
        {
            using var cts = new CancellationTokenSource();
            var options = new RunOptions
            {
                StateDirectory = _stateDir,
                BatchSize = 1,
                Progress = new SyncProgress<Progress>(_ => cts.Cancel()),
            };
            var cancelled = await Assert.ThrowsAnyAsync<OperationCanceledException>(() => m.RunAsync(options, cts.Token));
            var inner = Assert.IsType<RtoGtoRException>(cancelled.InnerException);
            Assert.Equal(ErrorCodes.Exec004, inner.Code);
            Assert.Equal("interrupted", inner.Report!.Status);
            runId = inner.Report.RunId;
        }

        // A new SDK session (a new engine process) resumes the run from its checkpoint.
        await using (var m = new Migration(Source, Destination, mapping))
        {
            var report = await m.RunAsync(new RunOptions { StateDirectory = _stateDir, ResumeRunId = runId });
            Assert.Equal("succeeded", report.Status);
            Assert.Equal(2, report.Attempts);
            var verification = await m.VerifyAsync(runId, _stateDir);
            Assert.Equal("passed", verification.Status);
        }
    }

    /// <summary>A migration whose engine process injects the given test faults.</summary>
    private static Migration Faulty(Mapping mapping, string fault) =>
        new(Source, Destination, mapping, new EngineOptions
        {
            Environment = new Dictionary<string, string>(StringComparer.Ordinal) { ["RTOGTOR_TEST_FAULT"] = fault },
        });

    [IntegrationFact]
    public async Task Resumes_after_a_fault_between_commit_and_checkpoint()
    {
        // The worst crash point: the batch is committed but not checkpointed.
        await using var m = Faulty(await MappingAsync(), "step=relationships:OrderContainsItem;batch=1;point=before-checkpoint");
        var error = await Assert.ThrowsAsync<RtoGtoRException>(() =>
            m.RunAsync(new RunOptions { StateDirectory = _stateDir, BatchSize = 1 }));
        Assert.Equal(ErrorCodes.Exec004, error.Code);
        Assert.Equal("interrupted", error.Report!.Status);

        // The same engine process: the fault fired once.
        var report = await m.RunAsync(new RunOptions { StateDirectory = _stateDir, ResumeRunId = error.Report.RunId });
        Assert.Equal("succeeded", report.Status);
        Assert.Equal(2, report.Attempts);
        Assert.Equal(24, report.Totals.Written);
        var verification = await m.VerifyAsync(report.RunId, new VerifyOptions { StateDirectory = _stateDir, Values = true });
        Assert.Equal("passed", verification.Status);
        Assert.All(verification.Entities, e => Assert.Equal(0, e.ValueMismatches));
    }

    [IntegrationFact]
    public async Task Retries_transient_faults_per_the_retry_policy()
    {
        var mapping = await MappingAsync();
        var retry = new RetryPolicy { MaxAttempts = 3, InitialBackoffMs = 1, MaxBackoffMs = 5 };
        await using (var m = Faulty(mapping, "step=nodes:Customer;point=write;times=2"))
        {
            var report = await m.RunAsync(new RunOptions { StateDirectory = _stateDir, Retry = retry });
            Assert.Equal("succeeded", report.Status);
            Assert.Equal(2, report.Retries);
            Assert.Equal(retry, report.Retry);
        }
        await using (var m = Faulty(mapping, "step=nodes:Customer;point=write;times=5"))
        {
            var error = await Assert.ThrowsAsync<RtoGtoRException>(() => m.RunAsync(new RunOptions
            {
                StateDirectory = _stateDir,
                Retry = new RetryPolicy { MaxAttempts = 2, InitialBackoffMs = 1, MaxBackoffMs = 1 },
            }));
            Assert.Equal(ErrorCodes.Exec003, error.Code);
            Assert.Equal("interrupted", error.Report!.Status);
            Assert.Equal(1, error.Report.Retries);
        }
    }

    /// <summary>Calls the handler synchronously (Progress&lt;T&gt; would post to the thread pool).</summary>
    private sealed class SyncProgress<T>(Action<T> handler) : IProgress<T>
    {
        public void Report(T value) => handler(value);
    }
}

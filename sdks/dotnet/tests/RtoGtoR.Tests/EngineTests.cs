using System.Diagnostics;
using RtoGtoR.Model;

namespace RtoGtoR.Tests;

/// <summary>Tests that run the real engine binary (built by build-engine.sh) without databases.</summary>
public class EngineTests
{
    [Fact]
    public async Task Handshake_reports_versions()
    {
        await using var engine = await Engine.StartAsync();
        Assert.Equal("0.1.0", engine.Info.SpecVersion);
        Assert.Equal("rtg-id/v1", engine.Info.IdentityAlgorithm);
        Assert.Equal("1.0", engine.Info.ProtocolVersion);
        Assert.StartsWith("0.1.", engine.Info.EngineVersion, StringComparison.Ordinal);
    }

    [Fact]
    public async Task Engine_errors_carry_codes()
    {
        await using var engine = await Engine.StartAsync();
        var e = await Assert.ThrowsAsync<RtoGtoRException>(() =>
            engine.InspectAsync(Connection.PostgreSql(urlEnv: "RTOGTOR_TEST_SURELY_UNSET")));
        Assert.Equal(ErrorCodes.Conn002, e.Code);
        Assert.Contains("RTOGTOR_TEST_SURELY_UNSET", e.Message, StringComparison.Ordinal);

        var invalid = await Assert.ThrowsAsync<RtoGtoRException>(() => engine.CanonicalizeAsync("int64", "1.5"));
        Assert.Equal(ErrorCodes.Val001, invalid.Code);
    }

    [Fact]
    public async Task Semantic_validation_happens_in_the_engine()
    {
        await using var engine = await Engine.StartAsync();
        // Schema-valid (so it loads locally) but semantically wrong: key column not mapped.
        var mapping = Mapping.FromYaml("""
            specVersion: "0.1.0"
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
            """);
        var result = await engine.ValidateMappingAsync(mapping);
        Assert.False(result.Valid);
        Assert.Contains(result.Diagnostics, d => d.Code == ErrorCodes.Map005 && d.Path == "/entities/0/key/1");
    }

    [Fact]
    public async Task Cancellation_stops_a_request_and_the_engine_stays_usable()
    {
        await using var engine = await Engine.StartAsync(new EngineOptions
        {
            // A non-routable address: connecting hangs until cancelled.
            Environment = { ["RTOGTOR_TEST_BLACKHOLE"] = "postgres://u:p@10.255.255.1:5432/db?connect_timeout=60" },
        });
        using var cts = new CancellationTokenSource(TimeSpan.FromMilliseconds(300));
        var watch = Stopwatch.StartNew();
        await Assert.ThrowsAnyAsync<OperationCanceledException>(() =>
            engine.InspectAsync(Connection.PostgreSql(urlEnv: "RTOGTOR_TEST_BLACKHOLE"), cancellationToken: cts.Token));
        Assert.True(watch.Elapsed < TimeSpan.FromSeconds(10), $"cancellation took {watch.Elapsed}");
        Assert.Equal("12.5", await engine.CanonicalizeAsync("decimal", "0012.500"));
    }

    [Fact]
    public async Task Dispose_ends_the_engine_process()
    {
        var engine = await Engine.StartAsync();
        var pid = engine.Session.ProcessId;
        await engine.DisposeAsync();
        Assert.True(engine.Session.HasExited);
        Assert.Throws<ArgumentException>(() => Process.GetProcessById(pid));
        await Assert.ThrowsAsync<ObjectDisposedException>(() => engine.CanonicalizeAsync("int64", "1"));
    }

    [Fact]
    public async Task Concurrent_requests_are_matched_to_their_responses()
    {
        await using var engine = await Engine.StartAsync();
        var tasks = Enumerable.Range(0, 50).Select(i => engine.CanonicalizeAsync("int64", "+00" + i)).ToArray();
        var results = await Task.WhenAll(tasks);
        Assert.Equal(Enumerable.Range(0, 50).Select(i => i.ToString(System.Globalization.CultureInfo.InvariantCulture)), results);
    }

    [Fact]
    public async Task Failed_runs_and_unknown_runs_report_codes()
    {
        await using var engine = await Engine.StartAsync();
        var e = await Assert.ThrowsAsync<RtoGtoRException>(() =>
            engine.ReportAsync("01234567-89ab-7def-8123-456789abcdef", Path.GetTempPath()));
        Assert.Equal(ErrorCodes.Run001, e.Code);
    }
}

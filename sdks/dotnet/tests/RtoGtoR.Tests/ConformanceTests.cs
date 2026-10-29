using System.Text.Json;
using System.Text.Json.Nodes;
using RtoGtoR.Model;

namespace RtoGtoR.Tests;

/// <summary>
/// The shared conformance suite (conformance/), run through the SDK's public API. Results are
/// also written per conformance/runner-protocol.md when RTOGTOR_CONFORMANCE_OUT is set.
/// </summary>
public sealed class ConformanceTests : IClassFixture<ConformanceTests.Fixture>
{
    private readonly Fixture _fx;

    public ConformanceTests(Fixture fx) => _fx = fx;

    public sealed class Fixture : IAsyncLifetime
    {
        public Engine Engine { get; private set; } = null!;

        public List<JsonObject> Results { get; } = [];

        public async Task InitializeAsync() => Engine = await Engine.StartAsync();

        public async Task DisposeAsync()
        {
            await Engine.DisposeAsync();
            if (Environment.GetEnvironmentVariable("RTOGTOR_CONFORMANCE_OUT") is { Length: > 0 } path)
            {
                var doc = new JsonObject
                {
                    ["runnerProtocol"] = "1",
                    ["sdk"] = "dotnet",
                    ["sdkVersion"] = Internal.EngineSession.SdkVersion,
                    ["engineVersion"] = Engine.Info.EngineVersion,
                    ["specVersion"] = Engine.Info.SpecVersion,
                    ["results"] = new JsonArray([.. Results.OrderBy(r => (string)r["case"]!, StringComparer.Ordinal)]),
                };
                await File.WriteAllTextAsync(path, doc.ToJsonString(new JsonSerializerOptions { WriteIndented = true }));
            }
        }

        public void Record(string caseId, bool passed, string? detail = null)
        {
            lock (Results)
            {
                Results.Add(new JsonObject { ["case"] = caseId, ["status"] = passed ? "passed" : "failed", ["detail"] = detail });
            }
        }
    }

    private static JsonElement Vectors(string file) => JsonDocument.Parse(Repo.Read("conformance", "vectors", file)).RootElement;

    private static List<KeyComponent> Key(JsonElement key) =>
        key.EnumerateArray().Select(k => new KeyComponent
        {
            Type = k.GetProperty("type").GetString()!,
            Value = k.GetProperty("value").ValueKind == JsonValueKind.Null ? null : k.GetProperty("value").GetString(),
        }).ToList();

    [Fact]
    public async Task Identity_vectors()
    {
        var doc = Vectors("identity.json");
        var failures = new List<string>();
        foreach (var v in doc.GetProperty("vectors").EnumerateArray())
        {
            var name = v.GetProperty("name").GetString()!;
            var r = await _fx.Engine.IdentityAsync(v.GetProperty("entityType").GetString()!, Key(v.GetProperty("key")));
            var canonical = Key(v.GetProperty("canonicalKey"));
            var ok = r.RtgId == v.GetProperty("rtgId").GetString()
                && r.SerializationHex == v.GetProperty("serializationHex").GetString()
                && r.CanonicalKey.Select(k => k.Value).SequenceEqual(canonical.Select(k => k.Value));
            _fx.Record("vectors/identity/" + name, ok);
            if (!ok)
            {
                failures.Add(name);
            }
        }
        foreach (var v in doc.GetProperty("invalid").EnumerateArray())
        {
            var name = v.GetProperty("name").GetString()!;
            var e = await Assert.ThrowsAsync<RtoGtoRException>(() =>
                _fx.Engine.IdentityAsync(v.GetProperty("entityType").GetString()!, Key(v.GetProperty("key"))));
            var ok = e.Code == v.GetProperty("errorCode").GetString();
            _fx.Record("vectors/identity/invalid/" + name, ok, e.Code);
            if (!ok)
            {
                failures.Add(name + " -> " + e.Code);
            }
        }
        Assert.Empty(failures);
    }

    [Fact]
    public async Task Value_vectors()
    {
        var doc = Vectors("values.json");
        var failures = new List<string>();
        var i = 0;
        foreach (var v in doc.GetProperty("valid").EnumerateArray())
        {
            var (type, input) = (v.GetProperty("type").GetString()!, v.GetProperty("input").GetString()!);
            var got = await _fx.Engine.CanonicalizeAsync(type, input);
            var ok = got == v.GetProperty("canonical").GetString();
            _fx.Record($"vectors/values/valid/{i++:D3}-{type}", ok, got);
            if (!ok)
            {
                failures.Add($"{type} {input} -> {got}");
            }
        }
        i = 0;
        foreach (var v in doc.GetProperty("invalid").EnumerateArray())
        {
            var (type, input) = (v.GetProperty("type").GetString()!, v.GetProperty("input").GetString()!);
            var e = await Assert.ThrowsAsync<RtoGtoRException>(() => _fx.Engine.CanonicalizeAsync(type, input));
            var ok = e.Code == v.GetProperty("errorCode").GetString();
            _fx.Record($"vectors/values/invalid/{i++:D3}-{type}", ok, e.Code);
            if (!ok)
            {
                failures.Add($"{type} {input} -> {e.Code}");
            }
        }
        Assert.Empty(failures);
    }

    /// <summary>
    /// The e-commerce case: the mapping loaded from YAML by the SDK, planned against the case's
    /// metadata, must give a plan whose typed model serializes back to exactly
    /// expected-plan.json. This checks YAML parsing, the protocol and the generated types.
    /// </summary>
    [Fact]
    public async Task Ecommerce_plan_case()
    {
        var mapping = await Mapping.FromFileAsync(Repo.Path("examples", "ecommerce", "mapping.yaml"));
        var metadata = RtoGtoRJson.Deserialize<SchemaMetadata>(Repo.Read("conformance", "cases", "ecommerce", "expected-schema-metadata.json"));
        var plan = await _fx.Engine.PlanAsync(mapping, metadata);

        var got = Jcs.Canonicalize(JsonNode.Parse(RtoGtoRJson.Serialize(plan)));
        var want = Repo.Read("conformance", "cases", "ecommerce", "expected-plan.json").TrimEnd('\n');
        _fx.Record("cases/ecommerce/plan", got == want);
        Assert.Equal(want, got);
        Assert.Equal(13, plan.Steps.Count);
        Assert.True(plan.Steps.Single(s => s.Id == "relationships:CustomerReferredBy").SelfReference);
    }

    /// <summary>The graph-to-relational plan must also round-trip through the typed model.</summary>
    [Fact]
    public async Task Ecommerce_reverse_plan_case()
    {
        var mapping = await Mapping.FromFileAsync(Repo.Path("examples", "ecommerce", "mapping-reverse.yaml"));
        var source = RtoGtoRJson.Deserialize<SchemaMetadata>(Repo.Read("conformance", "cases", "ecommerce-reverse", "source-metadata.json"));
        var destination = RtoGtoRJson.Deserialize<SchemaMetadata>(Repo.Read("conformance", "cases", "ecommerce-reverse", "destination-metadata.json"));
        var plan = await _fx.Engine.PlanAsync(mapping, source, destination);

        var got = Jcs.Canonicalize(JsonNode.Parse(RtoGtoRJson.Serialize(plan)));
        var want = Repo.Read("conformance", "cases", "ecommerce-reverse", "expected-plan.json").TrimEnd('\n');
        _fx.Record("cases/ecommerce-reverse/plan", got == want);
        Assert.Equal(want, got);
        Assert.Equal(10, plan.Steps.Count);
    }
}

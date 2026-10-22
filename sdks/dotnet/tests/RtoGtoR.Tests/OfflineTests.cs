using System.Text.Json;
using RtoGtoR.Model;

namespace RtoGtoR.Tests;

/// <summary>Tests that need no engine and no database.</summary>
public class OfflineTests
{
    private const string Minimal = """
        specVersion: "0.1.0"
        name: test
        direction: relational-to-graph
        source: { kind: postgresql }
        destination: { kind: neo4j }
        entities:
          - name: Customer
            table: customers
            labels: [Customer]
            key: [id]
            properties:
              - { column: id, property: id, type: int64 }
        """;

    [Fact]
    public async Task Example_mapping_loads()
    {
        var m = await Mapping.FromFileAsync(Repo.Path("examples", "ecommerce", "mapping.yaml"));
        Assert.Equal("ecommerce", m.Document.Name);
        Assert.Equal(4, m.Document.Entities.Count);
        var item = m.Document.Entities.Single(e => e.Name == "OrderItem");
        Assert.Equal(["order_id", "line_no"], item.Key);
        Assert.Equal("referenced-to-owner", m.Document.Relationships![0].ForeignKey!.Direction);
    }

    [Fact]
    public void Yaml_and_json_give_the_same_document()
    {
        var fromYaml = Mapping.FromYaml(Minimal);
        var fromJson = Mapping.FromJson(fromYaml.ToJson());
        Assert.Equal(RtoGtoRJson.Serialize(fromYaml.Document), RtoGtoRJson.Serialize(fromJson.Document));
    }

    [Theory]
    [InlineData("specVersion: \"0.1.0\"", "specVersion: 0.1", ErrorCodes.Map001, "/specVersion")] // a number, as in the engine
    [InlineData("specVersion: \"0.1.0\"", "specVersion: \"0.2.0\"", ErrorCodes.Spec001, "/specVersion")]
    [InlineData("type: int64 }", "type: integer }", ErrorCodes.Map001, "/entities/0/properties/0/type")]
    [InlineData("name: test", "name: test\nname: again", ErrorCodes.Map002, null)]
    public void Invalid_yaml_is_rejected_with_codes(string from, string to, string code, string? path)
    {
        var e = Assert.Throws<RtoGtoRException>(() => Mapping.FromYaml(Minimal.Replace(from, to, StringComparison.Ordinal)));
        Assert.Equal(code, e.Code);
        Assert.Equal(ErrorSeverity.Error, e.Severity);
        if (path is not null)
        {
            Assert.Contains(e.Diagnostics.Select(d => d.Path).Append(e.Path), p => p == path);
        }
    }

    [Fact]
    public void Json_duplicate_keys_are_rejected()
    {
        var e = Assert.Throws<RtoGtoRException>(() => Mapping.FromJson("""{"name":"a","name":"b"}"""));
        Assert.Equal(ErrorCodes.Map002, e.Code);
        Assert.Contains("duplicate key", e.Message, StringComparison.Ordinal);
    }

    [Fact]
    public void Every_schema_problem_is_reported()
    {
        var e = Assert.Throws<RtoGtoRException>(() => Mapping.FromJson("""{"specVersion":"0.1.0","name":"x"}"""));
        Assert.Equal(ErrorCodes.Map001, e.Code);
        Assert.NotEmpty(e.Diagnostics);
        Assert.All(e.Diagnostics, d => Assert.Equal(ErrorCodes.Map001, d.Code));
    }

    [Fact]
    public void Mapping_can_be_built_in_code()
    {
        var doc = Mapping.FromYaml(Minimal).Document with { Name = "renamed" };
        Assert.Equal("renamed", Mapping.FromDocument(doc).Document.Name);
    }

    [Fact]
    public void Error_codes_match_the_specification()
    {
        using var spec = JsonDocument.Parse(Repo.Read("specs", "error-codes.json"));
        var codes = spec.RootElement.GetProperty("codes").EnumerateArray().ToList();
        Assert.Equal(codes.Count, ErrorCodes.All.Count);
        foreach (var c in codes)
        {
            var info = ErrorCodes.All[c.GetProperty("code").GetString()!];
            Assert.Equal(c.GetProperty("retryable").GetBoolean(), info.Retryable);
            Assert.Equal(c.GetProperty("severity").GetString(), info.Severity.ToString().ToLowerInvariant());
        }
        Assert.True(new RtoGtoRException(ErrorCodes.Conn001, "x").Retryable);
    }

    [Fact]
    public void Connections_never_print_credentials()
    {
        var pg = Connection.PostgreSql(url: "postgres://app:S3cret@db/x");
        var neo = Connection.Neo4j("neo4j://db:7687", user: "neo4j", password: "Pa55word");
        Assert.Equal("postgresql connection", pg.ToString());
        Assert.Equal("neo4j connection", neo.ToString());

        // Literal secrets travel through private environment variables, never in messages.
        var env = new Dictionary<string, string>();
        var wire = RtoGtoRJson.Serialize(pg.ToWire(env)) + RtoGtoRJson.Serialize(neo.ToWire(env));
        Assert.DoesNotContain("S3cret", wire, StringComparison.Ordinal);
        Assert.DoesNotContain("Pa55word", wire, StringComparison.Ordinal);
        Assert.Contains("postgres://app:S3cret@db/x", env.Values);

        // Engine methods (outside a Migration) refuse literal credentials.
        Assert.Throws<InvalidOperationException>(() => pg.ToWire(null));
    }

    [Fact]
    public void Connection_arguments_are_checked()
    {
        Assert.Throws<ArgumentException>(() => Connection.PostgreSql());
        Assert.Throws<ArgumentException>(() => Connection.PostgreSql(url: "mysql://x"));
        Assert.Throws<ArgumentException>(() => Connection.Neo4j("neo4j://u:p@host:7687"));
        var neo = Connection.Neo4j("bolt://localhost:7687", authEnv: "NEO4J").ToWire(null);
        Assert.Equal("NEO4J_USER", neo.UserEnv);
        Assert.Equal("NEO4J_PASSWORD", neo.PasswordEnv);
    }

    [Theory]
    [InlineData("0.1.0-dev", true)]
    [InlineData("0.1.7", true)]
    [InlineData("0.2.0", false)]
    [InlineData("1.1.0", false)]
    [InlineData("garbage", false)]
    public void Engine_version_range(string version, bool supported) =>
        Assert.Equal(supported, Internal.EngineSession.IsSupportedEngine(version));

    [Fact]
    public void Missing_engine_is_reported_clearly()
    {
        var e = Assert.Throws<RtoGtoRException>(() => Internal.EngineLocator.Locate(Path.Combine(Path.GetTempPath(), "no-such-rtogtor")));
        Assert.Equal(ErrorCodes.Proto008, e.Code);
    }
}

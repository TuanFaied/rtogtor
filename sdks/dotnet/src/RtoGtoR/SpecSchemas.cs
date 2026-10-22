using System.Text.Json;
using Json.Schema;

namespace RtoGtoR;

/// <summary>The specification's JSON Schemas, embedded in the SDK for local validation.</summary>
internal static class SpecSchemas
{
    private const string BaseUri = "https://github.com/TuanFaied/rtogtor/specs/";

    // Registration order: a schema's cross-file references must already be registered.
    private static readonly string[] Files =
    [
        "mapping.schema.json", "schema-metadata.schema.json", "run-state.schema.json",
        "plan.schema.json", "report.schema.json", "protocol.schema.json",
    ];

    private static readonly Lazy<Dictionary<string, JsonSchema>> Schemas = new(Load);

    private static Dictionary<string, JsonSchema> Load()
    {
        var options = new BuildOptions { SchemaRegistry = new SchemaRegistry() };
        var asm = typeof(SpecSchemas).Assembly;
        var result = new Dictionary<string, JsonSchema>(StringComparer.Ordinal);
        foreach (var file in Files)
        {
            using var stream = asm.GetManifestResourceStream("RtoGtoR.Specs." + file)
                ?? throw new InvalidOperationException($"The SDK is missing its embedded schema {file}.");
            using var reader = new StreamReader(stream);
            result[file] = JsonSchema.FromText(reader.ReadToEnd(), options, new Uri(BaseUri + file));
        }
        return result;
    }

    /// <summary>Validates a document; returns one diagnostic per leaf error, ordered by path.</summary>
    public static IReadOnlyList<Diagnostic> Validate(string file, JsonElement instance, string code)
    {
        var results = Schemas.Value[file].Evaluate(instance, new EvaluationOptions { OutputFormat = OutputFormat.List });
        if (results.IsValid)
        {
            return [];
        }
        var problems = new SortedSet<(string Path, string Message)>();
        foreach (var detail in results.Details ?? [])
        {
            if (detail.Errors is null)
            {
                continue;
            }
            foreach (var (_, message) in detail.Errors)
            {
                problems.Add((detail.InstanceLocation.ToString(), message));
            }
        }
        if (problems.Count == 0)
        {
            problems.Add(("", "the document does not match " + file));
        }
        return problems.Select(p => new Diagnostic(code, ErrorSeverity.Error, false, p.Message, p.Path)).ToList();
    }
}

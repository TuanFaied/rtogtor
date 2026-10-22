using System.Globalization;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;
using System.Text.RegularExpressions;
using RtoGtoR.Model;
using YamlDotNet.Core;
using YamlDotNet.RepresentationModel;

namespace RtoGtoR;

/// <summary>
/// A mapping (specs/mapping.schema.json), loaded from JSON or YAML and validated locally
/// against the schema. Semantic checks (unique names, key coverage, foreign-key arity) are the
/// engine's: see <see cref="Engine.ValidateMappingAsync"/>, or simply plan the migration.
/// </summary>
public sealed partial class Mapping
{
    private const string SupportedSpecMajorMinor = "0.1.";

    private Mapping(JsonElement json)
    {
        Json = json;
        Document = RtoGtoRJson.Deserialize<MappingDocument>(json);
    }

    /// <summary>The typed mapping.</summary>
    public MappingDocument Document { get; }

    /// <summary>The mapping as JSON, exactly as sent to the engine.</summary>
    internal JsonElement Json { get; }

    /// <summary>Loads a <c>.json</c>, <c>.yaml</c> or <c>.yml</c> file.</summary>
    /// <exception cref="RtoGtoRException">RTG-MAP-002 if the file cannot be read or parsed; RTG-SPEC-001 or RTG-MAP-001 if it is invalid.</exception>
    public static async Task<Mapping> FromFileAsync(string path, CancellationToken cancellationToken = default)
    {
        string text;
        try
        {
            text = await File.ReadAllTextAsync(path, Encoding.UTF8, cancellationToken).ConfigureAwait(false);
        }
        catch (Exception e) when (e is IOException or UnauthorizedAccessException)
        {
            throw new RtoGtoRException(ErrorCodes.Map002, "cannot read the mapping file: " + e.Message, inner: e);
        }
        return Path.GetExtension(path).ToLowerInvariant() switch
        {
            ".json" => FromJson(text),
            ".yaml" or ".yml" => FromYaml(text),
            _ => throw new RtoGtoRException(ErrorCodes.Map002, $"cannot infer the format of \"{Path.GetFileName(path)}\": use a .json, .yaml or .yml extension"),
        };
    }

    /// <summary>Parses a JSON mapping. Duplicate object keys are rejected.</summary>
    public static Mapping FromJson(string json)
    {
        JsonDocument doc;
        try
        {
            CheckDuplicateKeys(json);
            doc = JsonDocument.Parse(json);
        }
        catch (JsonException e)
        {
            throw new RtoGtoRException(ErrorCodes.Map002, "invalid JSON: " + e.Message, inner: e);
        }
        return Validated(doc.RootElement.Clone());
    }

    /// <summary>
    /// Parses a YAML mapping. Plain scalars are typed with the YAML 1.2 core schema (as the
    /// engine does): <c>specVersion: 0.1</c> is a number and is rejected; quote it.
    /// </summary>
    public static Mapping FromYaml(string yaml)
    {
        JsonNode? node;
        try
        {
            var stream = new YamlStream();
            stream.Load(new StringReader(yaml));
            if (stream.Documents.Count != 1)
            {
                throw new RtoGtoRException(ErrorCodes.Map002, stream.Documents.Count == 0
                    ? "mapping document is empty"
                    : "a mapping file must contain exactly one YAML document");
            }
            node = ToJson(stream.Documents[0].RootNode);
        }
        catch (YamlException e)
        {
            throw new RtoGtoRException(ErrorCodes.Map002, "invalid YAML: " + e.Message, inner: e);
        }
        using var doc = JsonDocument.Parse(node?.ToJsonString() ?? "null");
        return Validated(doc.RootElement.Clone());
    }

    /// <summary>Creates a mapping from a typed document, for example one built in code.</summary>
    public static Mapping FromDocument(MappingDocument document)
    {
        ArgumentNullException.ThrowIfNull(document);
        return FromJson(RtoGtoRJson.Serialize(document));
    }

    /// <summary>The mapping as JSON.</summary>
    public string ToJson() => Json.GetRawText();

    private static Mapping Validated(JsonElement json)
    {
        // Check the version first: other versions have other schemas.
        if (json.ValueKind == JsonValueKind.Object && json.TryGetProperty("specVersion", out var v) &&
            v.ValueKind == JsonValueKind.String && !v.GetString()!.StartsWith(SupportedSpecMajorMinor, StringComparison.Ordinal))
        {
            throw new RtoGtoRException(ErrorCodes.Spec001, $"mapping declares specVersion {v.GetString()}; this SDK supports 0.1.x", "/specVersion");
        }
        var problems = SpecSchemas.Validate("mapping.schema.json", json, ErrorCodes.Map001);
        if (problems.Count > 0)
        {
            var first = problems[0];
            var more = problems.Count > 1 ? $" (and {problems.Count - 1} more problem{(problems.Count > 2 ? "s" : "")})" : "";
            throw new RtoGtoRException(ErrorCodes.Map001, first.Message + more, first.Path, problems);
        }
        return new Mapping(json);
    }

    /// <summary>Rejects duplicate keys, which System.Text.Json would silently accept.</summary>
    private static void CheckDuplicateKeys(string json)
    {
        var reader = new Utf8JsonReader(Encoding.UTF8.GetBytes(json));
        var stack = new Stack<HashSet<string>?>();
        while (reader.Read())
        {
            switch (reader.TokenType)
            {
                case JsonTokenType.StartObject:
                    stack.Push(new HashSet<string>(StringComparer.Ordinal));
                    break;
                case JsonTokenType.StartArray:
                    stack.Push(null);
                    break;
                case JsonTokenType.EndObject or JsonTokenType.EndArray:
                    stack.Pop();
                    break;
                case JsonTokenType.PropertyName when stack.Peek() is { } keys && !keys.Add(reader.GetString()!):
                    throw new JsonException($"duplicate key \"{reader.GetString()}\"");
            }
        }
    }

    // YAML 1.2 core schema resolution of plain scalars, as in the engine's YAML reader.
    [GeneratedRegex(@"^[-+]?[0-9]+$")]
    private static partial Regex Int10();

    [GeneratedRegex(@"^0o[0-7]+$")]
    private static partial Regex Int8();

    [GeneratedRegex(@"^0x[0-9a-fA-F]+$")]
    private static partial Regex Int16();

    [GeneratedRegex(@"^[-+]?(\.[0-9]+|[0-9]+(\.[0-9]*)?)([eE][-+]?[0-9]+)?$")]
    private static partial Regex Float();

    private static JsonNode? ToJson(YamlNode node)
    {
        switch (node)
        {
            case YamlMappingNode map:
                var obj = new JsonObject();
                foreach (var (k, v) in map.Children)
                {
                    if (k is not YamlScalarNode key)
                    {
                        throw new RtoGtoRException(ErrorCodes.Map002, "YAML mapping keys must be strings");
                    }
                    var name = key.Value ?? "";
                    if (obj.ContainsKey(name))
                    {
                        throw new RtoGtoRException(ErrorCodes.Map002, $"invalid YAML: mapping key \"{name}\" already defined");
                    }
                    obj[name] = ToJson(v);
                }
                return obj;
            case YamlSequenceNode seq:
                var arr = new JsonArray();
                foreach (var item in seq.Children)
                {
                    arr.Add(ToJson(item));
                }
                return arr;
            case YamlScalarNode scalar:
                return Scalar(scalar);
            default:
                throw new RtoGtoRException(ErrorCodes.Map002, "unsupported YAML node");
        }
    }

    private static JsonValue? Scalar(YamlScalarNode s)
    {
        var value = s.Value ?? "";
        var tag = s.Tag.IsEmpty ? "" : s.Tag.Value;
        if (s.Style != ScalarStyle.Plain || tag == "tag:yaml.org,2002:str")
        {
            return JsonValue.Create(value);
        }
        switch (value)
        {
            case "" or "~" or "null" or "Null" or "NULL":
                return null;
            case "true" or "True" or "TRUE":
                return JsonValue.Create(true);
            case "false" or "False" or "FALSE":
                return JsonValue.Create(false);
            case ".inf" or ".Inf" or ".INF" or "+.inf" or "-.inf" or ".nan" or ".NaN" or ".NAN":
                // Not representable in JSON; the engine rejects them too.
                throw new RtoGtoRException(ErrorCodes.Map002, $"YAML value {value} cannot be represented as JSON");
        }
        var inv = CultureInfo.InvariantCulture;
        if (Int10().IsMatch(value) && long.TryParse(value, NumberStyles.AllowLeadingSign, inv, out var l))
        {
            return JsonValue.Create(l);
        }
        if (Int8().IsMatch(value))
        {
            return JsonValue.Create(Convert.ToInt64(value[2..], 8));
        }
        if (Int16().IsMatch(value))
        {
            return JsonValue.Create(Convert.ToInt64(value[2..], 16));
        }
        if (Float().IsMatch(value) && double.TryParse(value, NumberStyles.Float, inv, out var d))
        {
            return JsonValue.Create(d);
        }
        return JsonValue.Create(value);
    }
}

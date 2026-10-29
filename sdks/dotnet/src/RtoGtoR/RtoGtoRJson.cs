using System.Text.Encodings.Web;
using System.Text.Json;
using System.Text.Json.Serialization;

namespace RtoGtoR;

/// <summary>JSON settings for the generated model types in <c>RtoGtoR.Model</c>.</summary>
public static class RtoGtoRJson
{
    /// <summary>
    /// Options matching the specification: property names come from the schemas, absent optional
    /// values stay absent, and required members must be present.
    /// </summary>
    public static JsonSerializerOptions Options { get; } = new()
    {
        DefaultIgnoreCondition = JsonIgnoreCondition.Never,
        Encoder = JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
        NumberHandling = JsonNumberHandling.Strict,
        ReadCommentHandling = JsonCommentHandling.Disallow,
        UnmappedMemberHandling = JsonUnmappedMemberHandling.Skip,
    };

    /// <summary>Serializes a model value.</summary>
    public static string Serialize<T>(T value) => JsonSerializer.Serialize(value, Options);

    /// <summary>Deserializes a model value.</summary>
    public static T Deserialize<T>(JsonElement element) =>
        element.Deserialize<T>(Options) ?? throw new JsonException($"Expected a {typeof(T).Name}, got null.");

    /// <summary>Deserializes a model value.</summary>
    public static T Deserialize<T>(string json) =>
        JsonSerializer.Deserialize<T>(json, Options) ?? throw new JsonException($"Expected a {typeof(T).Name}, got null.");
}

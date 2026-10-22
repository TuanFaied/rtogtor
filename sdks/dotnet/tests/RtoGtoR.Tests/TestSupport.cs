using System.Globalization;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace RtoGtoR.Tests;

internal static class Repo
{
    /// <summary>Finds a file relative to the repository root.</summary>
    public static string Path(params string[] parts)
    {
        var dir = new DirectoryInfo(AppContext.BaseDirectory);
        while (dir is not null && !(Directory.Exists(System.IO.Path.Combine(dir.FullName, "specs")) && Directory.Exists(System.IO.Path.Combine(dir.FullName, "conformance"))))
        {
            dir = dir.Parent;
        }
        if (dir is null)
        {
            throw new InvalidOperationException("Repository root not found; run the tests from the repository.");
        }
        return System.IO.Path.Combine([dir.FullName, .. parts]);
    }

    public static string Read(params string[] parts) => File.ReadAllText(Path(parts), Encoding.UTF8);
}

/// <summary>
/// RFC 8785 (JSON Canonicalization Scheme) for test comparisons. RtoGtoR documents contain
/// integers only, which keeps number formatting simple; anything else fails loudly.
/// </summary>
internal static class Jcs
{
    public static string Canonicalize(JsonNode? node)
    {
        var sb = new StringBuilder();
        Write(sb, node);
        return sb.ToString();
    }

    private static void Write(StringBuilder sb, JsonNode? node)
    {
        switch (node)
        {
            case null:
                sb.Append("null");
                break;
            case JsonObject obj:
                sb.Append('{');
                var first = true;
                // string.CompareOrdinal compares UTF-16 code units, as RFC 8785 requires.
                foreach (var (key, value) in obj.OrderBy(p => p.Key, StringComparer.Ordinal))
                {
                    if (!first)
                    {
                        sb.Append(',');
                    }
                    first = false;
                    WriteString(sb, key);
                    sb.Append(':');
                    Write(sb, value);
                }
                sb.Append('}');
                break;
            case JsonArray arr:
                sb.Append('[');
                for (var i = 0; i < arr.Count; i++)
                {
                    if (i > 0)
                    {
                        sb.Append(',');
                    }
                    Write(sb, arr[i]);
                }
                sb.Append(']');
                break;
            case JsonValue v:
                switch (v.GetValueKind())
                {
                    case JsonValueKind.String:
                        WriteString(sb, v.GetValue<string>());
                        break;
                    case JsonValueKind.True:
                        sb.Append("true");
                        break;
                    case JsonValueKind.False:
                        sb.Append("false");
                        break;
                    case JsonValueKind.Number:
                        sb.Append(v.GetValue<long>().ToString(CultureInfo.InvariantCulture));
                        break;
                    default:
                        throw new NotSupportedException(v.GetValueKind().ToString());
                }
                break;
        }
    }

    private static void WriteString(StringBuilder sb, string s)
    {
        sb.Append('"');
        foreach (var c in s)
        {
            switch (c)
            {
                case '"': sb.Append("\\\""); break;
                case '\\': sb.Append("\\\\"); break;
                case '\b': sb.Append("\\b"); break;
                case '\f': sb.Append("\\f"); break;
                case '\n': sb.Append("\\n"); break;
                case '\r': sb.Append("\\r"); break;
                case '\t': sb.Append("\\t"); break;
                case < ' ': sb.Append(CultureInfo.InvariantCulture, $"\\u{(int)c:x4}"); break;
                default: sb.Append(c); break;
            }
        }
        sb.Append('"');
    }
}

/// <summary>A fact that runs only when the integration databases are configured.</summary>
internal sealed class IntegrationFactAttribute : FactAttribute
{
    public IntegrationFactAttribute()
    {
        if (string.IsNullOrEmpty(Environment.GetEnvironmentVariable("RTOGTOR_TEST_POSTGRES_URL")) ||
            string.IsNullOrEmpty(Environment.GetEnvironmentVariable("RTOGTOR_TEST_NEO4J_URL")))
        {
            Skip = "Set RTOGTOR_TEST_POSTGRES_URL and RTOGTOR_TEST_NEO4J_URL (disposable databases) to run integration tests.";
        }
    }
}

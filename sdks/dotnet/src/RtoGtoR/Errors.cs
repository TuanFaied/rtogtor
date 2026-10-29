using RtoGtoR.Model;

namespace RtoGtoR;

/// <summary>Severity of an error code (specs/error-codes.md).</summary>
public enum ErrorSeverity
{
    /// <summary>Informational.</summary>
    Info,

    /// <summary>The operation succeeded, with a caveat.</summary>
    Warning,

    /// <summary>The operation failed.</summary>
    Error,

    /// <summary>The engine cannot continue.</summary>
    Fatal,
}

/// <summary>Definition of one stable error code.</summary>
/// <param name="Code">The code, for example <c>RTG-MAP-005</c>.</param>
/// <param name="Title">Short title.</param>
/// <param name="Severity">Severity.</param>
/// <param name="Retryable">Whether retrying the same operation unchanged may succeed.</param>
/// <param name="Meaning">What happened.</param>
/// <param name="Remediation">What to do about it.</param>
public sealed record ErrorCodeInfo(string Code, string Title, ErrorSeverity Severity, bool Retryable, string Meaning, string Remediation);

/// <summary>One problem reported by the engine or the SDK.</summary>
/// <param name="Code">Stable error code.</param>
/// <param name="Severity">Severity.</param>
/// <param name="Retryable">Whether retrying may succeed.</param>
/// <param name="Message">Description. Never contains credentials.</param>
/// <param name="Path">Location, such as a JSON Pointer into a mapping, if any.</param>
public sealed record Diagnostic(string Code, ErrorSeverity Severity, bool Retryable, string Message, string? Path);

/// <summary>
/// An RtoGtoR failure carrying a stable error code from specs/error-codes.json. The SDK never
/// invents codes: they come from the engine or from the specification.
/// </summary>
public class RtoGtoRException : Exception
{
    /// <summary>Creates an exception for a code defined in the specification.</summary>
    public RtoGtoRException(string code, string message, string? path = null, IReadOnlyList<Diagnostic>? diagnostics = null, Exception? inner = null)
        : base($"{code}: {message}", inner)
    {
        Code = code;
        Path = path;
        Diagnostics = diagnostics ?? [];
        if (ErrorCodes.All.TryGetValue(code, out var info))
        {
            Severity = info.Severity;
            Retryable = info.Retryable;
        }
        else
        {
            Severity = ErrorSeverity.Fatal;
        }
    }

    /// <summary>The stable error code, for example <c>RTG-MAP-005</c>.</summary>
    public string Code { get; }

    /// <summary>Severity of the code.</summary>
    public ErrorSeverity Severity { get; }

    /// <summary>Whether retrying the same operation unchanged may succeed.</summary>
    public bool Retryable { get; init; }

    /// <summary>Location of the problem, if known.</summary>
    public string? Path { get; }

    /// <summary>Every problem, when the error stands for several (for example an invalid mapping).</summary>
    public IReadOnlyList<Diagnostic> Diagnostics { get; }

    /// <summary>For a failed or interrupted run: its report. Pass <c>Report.RunId</c> to <see cref="RunOptions.ResumeRunId"/>.</summary>
    public Report? Report { get; init; }

    internal static ErrorSeverity ParseSeverity(string s) => s switch
    {
        "info" => ErrorSeverity.Info,
        "warning" => ErrorSeverity.Warning,
        "error" => ErrorSeverity.Error,
        _ => ErrorSeverity.Fatal,
    };

    internal static RtoGtoRException FromWire(ProtocolError e)
    {
        var diagnostics = e.Diagnostics?.Select(d => new Diagnostic(d.Code, ParseSeverity(d.Severity), d.Retryable, d.Message, d.Path)).ToList();
        return new RtoGtoRException(e.Code, e.Message, e.Path, diagnostics) { Retryable = e.Retryable, Report = e.Report };
    }
}

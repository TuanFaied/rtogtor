using System.Runtime.InteropServices;

namespace RtoGtoR.Internal;

/// <summary>Finds the engine binary bundled with the package.</summary>
internal static class EngineLocator
{
    /// <summary>Environment variable overriding the engine binary.</summary>
    public const string PathVariable = "RTOGTOR_ENGINE_PATH";

    public static string RuntimeId
    {
        get
        {
            var os = OperatingSystem.IsWindows() ? "win" : OperatingSystem.IsMacOS() ? "osx" : "linux";
            var arch = RuntimeInformation.OSArchitecture switch
            {
                Architecture.X64 => "x64",
                Architecture.Arm64 => "arm64",
                var other => other.ToString().ToLowerInvariant(),
            };
            return os + "-" + arch;
        }
    }

    public static string ExecutableName => OperatingSystem.IsWindows() ? "rtogtor.exe" : "rtogtor";

    /// <summary>Returns the engine path, or throws RTG-PROTO-008 listing where it looked.</summary>
    public static string Locate(string? explicitPath)
    {
        var candidates = new List<string>();
        if (!string.IsNullOrEmpty(explicitPath))
        {
            candidates.Add(explicitPath);
        }
        else if (Environment.GetEnvironmentVariable(PathVariable) is { Length: > 0 } fromEnv)
        {
            candidates.Add(fromEnv);
        }
        else
        {
            var baseDir = AppContext.BaseDirectory;
            // Portable apps: NuGet copies native assets to runtimes/<rid>/native.
            candidates.Add(Path.Combine(baseDir, "runtimes", RuntimeId, "native", ExecutableName));
            // RID-specific builds and publishes: native assets go next to the app.
            candidates.Add(Path.Combine(baseDir, ExecutableName));
        }
        foreach (var c in candidates)
        {
            if (File.Exists(c))
            {
                EnsureExecutable(c);
                return c;
            }
        }
        throw new RtoGtoRException(ErrorCodes.Proto008,
            $"the RtoGtoR engine for {RuntimeId} was not found (looked in: {string.Join(", ", candidates)}). " +
            $"Set {PathVariable} to an rtogtor binary, or use a package that includes your platform.");
    }

    /// <summary>NuGet does not preserve Unix permissions, so the bundled binary may lack its execute bit.</summary>
    private static void EnsureExecutable(string path)
    {
        if (OperatingSystem.IsWindows())
        {
            return;
        }
        const UnixFileMode Execute = UnixFileMode.UserExecute | UnixFileMode.GroupExecute | UnixFileMode.OtherExecute;
        var mode = File.GetUnixFileMode(path);
        if ((mode & UnixFileMode.UserExecute) == 0)
        {
            try
            {
                File.SetUnixFileMode(path, mode | Execute);
            }
            catch (UnauthorizedAccessException e)
            {
                throw new RtoGtoRException(ErrorCodes.Proto008, $"the engine at {path} is not executable and its permissions cannot be changed: {e.Message}", inner: e);
            }
        }
    }
}

using System.Collections.Concurrent;
using System.Diagnostics;
using System.Globalization;
using System.Text;
using System.Text.Json;
using RtoGtoR.Model;

namespace RtoGtoR.Internal;

/// <summary>
/// One engine process speaking the JSON Lines protocol (specs/protocol.md). Requests may be
/// concurrent; responses are matched by id. Disposing ends the process.
/// </summary>
internal sealed class EngineSession : IAsyncDisposable
{
    public const string ProtocolVersion = "1.0";

    private static readonly JsonSerializerOptions ParamsOptions = new()
    {
        PropertyNamingPolicy = JsonNamingPolicy.CamelCase,
        DefaultIgnoreCondition = System.Text.Json.Serialization.JsonIgnoreCondition.WhenWritingNull,
        Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
    };

    private readonly Process _process;
    private readonly StreamWriter _stdin;
    private readonly SemaphoreSlim _writeLock = new(1, 1);
    private readonly ConcurrentDictionary<string, Pending> _pending = new(StringComparer.Ordinal);
    private readonly LinkedList<string> _stderrTail = new();
    private readonly Task _stdoutTask;
    private readonly Task _stderrTask;
    private readonly TimeSpan _shutdownTimeout;
    private long _nextId;
    private int _disposed;
    private bool _exited;
    private Exception? _terminal;

    private sealed record Pending(TaskCompletionSource<JsonElement> Completion, Action<string, JsonElement>? OnEvent);

    private EngineSession(Process process, TimeSpan shutdownTimeout)
    {
        _process = process;
        _shutdownTimeout = shutdownTimeout;
        _stdin = new StreamWriter(process.StandardInput.BaseStream, new UTF8Encoding(false)) { AutoFlush = true, NewLine = "\n" };
        _stdoutTask = Task.Run(ReadStdoutAsync);
        _stderrTask = Task.Run(ReadStderrAsync);
    }

    public HandshakeResult Info { get; private set; } = null!;

    /// <summary>Process ID, for tests.</summary>
    internal int ProcessId => _process.Id;

    internal bool HasExited => _exited || _process.HasExited;

    public static async Task<EngineSession> StartAsync(EngineOptions options, IDictionary<string, string> environment, CancellationToken cancellationToken)
    {
        var path = EngineLocator.Locate(options.EnginePath);
        var psi = new ProcessStartInfo(path)
        {
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            UseShellExecute = false,
            CreateNoWindow = true,
            StandardOutputEncoding = new UTF8Encoding(false),
            StandardErrorEncoding = new UTF8Encoding(false),
        };
        psi.ArgumentList.Add("serve");
        psi.ArgumentList.Add("--stdio");
        foreach (var (k, v) in options.Environment)
        {
            psi.Environment[k] = v;
        }
        foreach (var (k, v) in environment)
        {
            psi.Environment[k] = v;
        }

        Process process;
        try
        {
            process = Process.Start(psi) ?? throw new InvalidOperationException("Process.Start returned null");
        }
        catch (Exception e) when (e is System.ComponentModel.Win32Exception or InvalidOperationException)
        {
            throw new RtoGtoRException(ErrorCodes.Proto008, $"cannot start the engine at {path}: {e.Message}", inner: e);
        }

        var session = new EngineSession(process, options.ShutdownTimeout);
        try
        {
            var result = await session.CallAsync("handshake", new
            {
                client = new { name = "RtoGtoR.NET", version = SdkVersion },
                protocolVersions = new[] { ProtocolVersion },
            }, null, cancellationToken).ConfigureAwait(false);
            session.Info = RtoGtoRJson.Deserialize<HandshakeResult>(result);
            if (!IsSupportedEngine(session.Info.EngineVersion) || !session.Info.ProtocolVersion.StartsWith("1.", StringComparison.Ordinal))
            {
                throw new RtoGtoRException(ErrorCodes.Proto007,
                    $"engine {session.Info.EngineVersion} (protocol {session.Info.ProtocolVersion}) is not supported by this SDK, which needs {SupportedEngineRange}");
            }
        }
        catch
        {
            await session.DisposeAsync().ConfigureAwait(false);
            throw;
        }
        return session;
    }

    public static string SdkVersion =>
        typeof(EngineSession).Assembly.GetName().Version?.ToString(3) ?? "0.0.0";

    /// <summary>Engine versions this SDK supports. Before 1.0, minor versions may break compatibility.</summary>
    public const string SupportedEngineRange = ">= 1.0.0-0, < 2.0.0";

    internal static bool IsSupportedEngine(string version)
    {
        var core = version.Split('-', '+')[0].Split('.');
        return core.Length == 3
            && int.TryParse(core[0], NumberStyles.None, CultureInfo.InvariantCulture, out var major)
            && int.TryParse(core[1], NumberStyles.None, CultureInfo.InvariantCulture, out var minor)
            && int.TryParse(core[2], NumberStyles.None, CultureInfo.InvariantCulture, out _)
            && major == 1 && minor >= 0;
    }

    /// <summary>Sends a request and returns its result, or throws the engine's error.</summary>
    public async Task<JsonElement> CallAsync(string method, object parameters, Action<string, JsonElement>? onEvent, CancellationToken cancellationToken)
    {
        ObjectDisposedException.ThrowIf(Volatile.Read(ref _disposed) != 0, this);
        if (_terminal is not null)
        {
            throw _terminal;
        }
        var id = Interlocked.Increment(ref _nextId).ToString(CultureInfo.InvariantCulture);
        var pending = new Pending(new TaskCompletionSource<JsonElement>(TaskCreationOptions.RunContinuationsAsynchronously), onEvent);
        _pending[id] = pending;

        var line = BuildRequest(id, method, parameters);
        try
        {
            await WriteLineAsync(line, CancellationToken.None).ConfigureAwait(false);
        }
        catch (Exception e) when (e is IOException or ObjectDisposedException)
        {
            _pending.TryRemove(id, out _);
            throw EngineGone(e);
        }

        // Cancellation asks the engine to stop the request; its response then arrives as
        // RTG-EXEC-004 and is reported as OperationCanceledException.
        using var registration = cancellationToken.Register(() =>
        {
            if (method != "cancel" && Volatile.Read(ref _disposed) == 0)
            {
                var cancelLine = BuildRequest("cancel-" + id, "cancel", new { requestId = id });
                _ = WriteLineAsync(cancelLine, CancellationToken.None);
            }
        });
        try
        {
            return await pending.Completion.Task.ConfigureAwait(false);
        }
        catch (RtoGtoRException e) when (cancellationToken.IsCancellationRequested)
        {
            // The caller cancelled: whatever the request ended with (normally RTG-EXEC-004), it
            // is reported as a cancellation, with the engine's error (and any report) inside.
            throw new OperationCanceledException("The request was cancelled.", e, cancellationToken);
        }
    }

    private static string BuildRequest(string id, string method, object parameters)
    {
        var buffer = new MemoryStream();
        using (var w = new Utf8JsonWriter(buffer))
        {
            w.WriteStartObject();
            w.WriteString("protocolVersion", ProtocolVersion);
            w.WriteString("id", id);
            w.WriteString("method", method);
            w.WritePropertyName("params");
            JsonSerializer.Serialize(w, parameters, parameters.GetType(), ParamsOptions);
            w.WriteEndObject();
        }
        return Encoding.UTF8.GetString(buffer.ToArray());
    }

    private async Task WriteLineAsync(string line, CancellationToken cancellationToken)
    {
        await _writeLock.WaitAsync(cancellationToken).ConfigureAwait(false);
        try
        {
            await _stdin.WriteLineAsync(line).ConfigureAwait(false);
        }
        finally
        {
            _writeLock.Release();
        }
    }

    private async Task ReadStdoutAsync()
    {
        var reader = _process.StandardOutput;
        try
        {
            while (await reader.ReadLineAsync().ConfigureAwait(false) is { } line)
            {
                if (line.Length == 0)
                {
                    continue;
                }
                Dispatch(line);
            }
        }
        catch (Exception e) when (e is IOException or ObjectDisposedException)
        {
            // The process is gone; handled below.
        }
        FailAll(EngineGone(null));
    }

    private void Dispatch(string line)
    {
        using var doc = JsonDocument.Parse(line);
        var root = doc.RootElement;
        if (!root.TryGetProperty("id", out var idElement) || idElement.GetString() is not { } id)
        {
            return;
        }
        if (root.TryGetProperty("event", out var ev))
        {
            if (_pending.TryGetValue(id, out var p) && p.OnEvent is not null && root.TryGetProperty("data", out var data))
            {
                p.OnEvent(ev.GetString() ?? "", data.Clone());
            }
            return;
        }
        if (!_pending.TryRemove(id, out var pending))
        {
            return; // for example the response to a cancel request
        }
        if (root.TryGetProperty("error", out var error))
        {
            pending.Completion.TrySetException(RtoGtoRException.FromWire(RtoGtoRJson.Deserialize<ProtocolError>(error)));
        }
        else if (root.TryGetProperty("result", out var result))
        {
            pending.Completion.TrySetResult(result.Clone());
        }
    }

    private async Task ReadStderrAsync()
    {
        var reader = _process.StandardError;
        try
        {
            while (await reader.ReadLineAsync().ConfigureAwait(false) is { } line)
            {
                lock (_stderrTail)
                {
                    _stderrTail.AddLast(line);
                    if (_stderrTail.Count > 20)
                    {
                        _stderrTail.RemoveFirst();
                    }
                }
            }
        }
        catch (Exception e) when (e is IOException or ObjectDisposedException)
        {
            // The process is gone.
        }
    }

    private RtoGtoRException EngineGone(Exception? inner)
    {
        string tail;
        lock (_stderrTail)
        {
            tail = string.Join(" | ", _stderrTail);
        }
        var exit = _process.HasExited ? $" with exit code {_process.ExitCode}" : "";
        var detail = tail.Length > 0 ? $"; last engine output: {tail}" : "";
        return new RtoGtoRException(ErrorCodes.Proto008, $"the engine process ended{exit}{detail}", inner: inner);
    }

    private void FailAll(Exception e)
    {
        _terminal ??= e;
        foreach (var id in _pending.Keys)
        {
            if (_pending.TryRemove(id, out var p))
            {
                p.Completion.TrySetException(Volatile.Read(ref _disposed) != 0
                    ? new ObjectDisposedException(nameof(Engine), "The engine was disposed while the request was running.")
                    : e);
            }
        }
    }

    public async ValueTask DisposeAsync()
    {
        if (Interlocked.Exchange(ref _disposed, 1) != 0)
        {
            return;
        }
        try
        {
            if (!_process.HasExited)
            {
                // Ask politely, then close stdin, then kill.
                try
                {
                    await WriteLineAsync(BuildRequest("shutdown", "shutdown", new { }), CancellationToken.None).ConfigureAwait(false);
                    _stdin.Close();
                }
                catch (Exception e) when (e is IOException or ObjectDisposedException)
                {
                    // Already gone.
                }
                using var timeout = new CancellationTokenSource(_shutdownTimeout);
                try
                {
                    await _process.WaitForExitAsync(timeout.Token).ConfigureAwait(false);
                }
                catch (OperationCanceledException)
                {
                    _process.Kill(entireProcessTree: true);
                    await _process.WaitForExitAsync(CancellationToken.None).ConfigureAwait(false);
                }
            }
            await Task.WhenAll(_stdoutTask, _stderrTask).ConfigureAwait(false);
        }
        finally
        {
            _exited = true;
            _process.Dispose();
            _writeLock.Dispose();
        }
    }
}

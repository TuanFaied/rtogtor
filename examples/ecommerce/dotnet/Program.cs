// Migrates the e-commerce example from PostgreSQL to Neo4j and back,
// verifying every value both ways.
//
//   export POSTGRES_URL='postgres://user:password@localhost:5432/ecommerce'
//   export NEO4J_URL='neo4j://neo4j:password@localhost:7687'
//   dotnet run
//
// Load ../schema.sql into the PostgreSQL database first. The run is safe to
// repeat: writes are idempotent.
using RtoGtoR;
using RtoGtoR.Model;

var postgres = Connection.PostgreSql(urlEnv: "POSTGRES_URL");
var neo4j = Connection.Neo4j(urlEnv: "NEO4J_URL");
using var cts = new CancellationTokenSource();
Console.CancelKeyPress += (_, e) => { e.Cancel = true; cts.Cancel(); };

try
{
    // PostgreSQL -> Neo4j, then Neo4j -> PostgreSQL with the reverse mapping.
    await MigrateAsync("mapping.yaml", postgres, neo4j, cts.Token);
    await MigrateAsync("mapping-reverse.yaml", neo4j, postgres, cts.Token);
    return 0;
}
catch (RtoGtoRException e) when (e.Report is not null)
{
    Console.Error.WriteLine($"error: run {e.Report.RunId} stopped ({e.Code}: {e.Message}); resume it with ResumeRunId = \"{e.Report.RunId}\"");
    return 1;
}
catch (Exception e) when (e is RtoGtoRException or InvalidOperationException)
{
    Console.Error.WriteLine($"error: {e.Message}");
    return 1;
}

static async Task MigrateAsync(string file, Connection source, Connection destination, CancellationToken cancellationToken)
{
    var mapping = await Mapping.FromFileAsync(Path.Combine("..", file));
    await using var m = new Migration(source, destination, mapping);

    var plan = await m.PlanAsync(cancellationToken);
    Console.WriteLine($"{mapping.Document.Name}: {plan.Source.Product} -> {plan.Destination.Product}, {plan.Steps.Count} steps");

    var report = await m.RunAsync(new RunOptions
    {
        BatchSize = 500,
        Progress = new ConsoleProgress(),
    }, cancellationToken);

    var verification = await m.VerifyAsync(report.RunId, new VerifyOptions { Values = true }, cancellationToken);
    Console.WriteLine($"  run {report.RunId} {report.Status}, {report.Totals.Written} records written; verification {verification.Status}");
    if (report.Status != "succeeded" || verification.Status != "passed")
    {
        throw new InvalidOperationException($"{mapping.Document.Name} did not round-trip cleanly; see rtogtor report --run-id {report.RunId}");
    }
}

/// <summary>Prints each finished step (synchronously, unlike Progress&lt;T&gt;).</summary>
internal sealed class ConsoleProgress : IProgress<Progress>
{
    public void Report(Progress value)
    {
        if (value.Done)
        {
            Console.WriteLine($"  {value.StepId,-36} {value.Written,6} written");
        }
    }
}

using RtoGtoR;
using RtoGtoR.Model;

// Package smoke test: the engine must be found inside the installed package, start, and plan.
if (Environment.GetEnvironmentVariable("RTOGTOR_ENGINE_PATH") is { Length: > 0 })
{
    Console.Error.WriteLine("Unset RTOGTOR_ENGINE_PATH: the smoke test must use the bundled engine.");
    return 2;
}
var repo = args.Length > 0 ? args[0] : "../../..";
await using var engine = await Engine.StartAsync();
Console.WriteLine($"engine {engine.Info.EngineVersion}, spec {engine.Info.SpecVersion}, protocol {engine.Info.ProtocolVersion}");

var mapping = await Mapping.FromFileAsync(Path.Combine(repo, "examples", "ecommerce", "mapping.yaml"));
var metadata = RtoGtoRJson.Deserialize<SchemaMetadata>(
    await File.ReadAllTextAsync(Path.Combine(repo, "conformance", "cases", "ecommerce", "expected-schema-metadata.json")));
var plan = await engine.PlanAsync(mapping, metadata);
Console.WriteLine($"planned {plan.Steps.Count} steps for mapping {plan.Mapping.Name} ({plan.Mapping.Hash[..12]})");
return plan.Steps.Count == 13 ? 0 : 1;

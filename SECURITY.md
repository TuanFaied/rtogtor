# Security Policy

## Supported versions

RtoGtoR is pre-alpha. Until 1.0, only the latest commit on `main` receives
security fixes.

## Reporting a vulnerability

**Please do not open a public issue for security problems.**

Report vulnerabilities privately through GitHub:
[Report a vulnerability](https://github.com/TuanFaied/rtogtor/security/advisories/new).

Please include:

- A description of the issue and its impact
- Steps to reproduce, or a proof of concept
- The affected component (engine, a specific SDK, spec) and version or commit

You can expect an acknowledgement within 7 days. We will keep you informed
while we investigate and credit you in the advisory unless you prefer
otherwise.

## Scope

Of particular interest:

- Credentials or connection strings leaking into logs, plans, reports, run
  state, error messages or process arguments
- Destructive operations running without the explicit `--allow-destructive`
  safeguard
- Data being silently altered or discarded during migration
- Injection through mapping files into generated SQL or Cypher

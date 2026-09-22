# Agent Guidance

This file applies to the whole repository. It gives coding agents the project
context, boundaries, and verification commands needed to make safe changes.

## Start Here

- Read [README.md](README.md) for the product purpose, supported platforms, and
  user-facing entry points.
- Read [docs/architecture.md](docs/architecture.md) before changing component
  initialization, collection, conversion, export, or server behavior.
- Read [docs/development.md](docs/development.md) and
  [CONTRIBUTING.md](CONTRIBUTING.md) before making or submitting changes.
- Read [docs/configuration.md](docs/configuration.md) before adding or changing
  flags, environment variables, or defaults.

## Architecture

Fleet Intelligence Agent collects GPU and system telemetry and exposes it over
the HTTP API, Prometheus, file export, or OTLP remote export.

The primary runtime path is:

1. `internal/registry/` initializes components backed by
   `fleet-intelligence-sdk`.
2. `internal/exporter/collector/` gathers health, metrics, events, machine
   information, and attestation data.
3. `internal/exporter/converter/` converts collected data into CSV or OTLP.
4. `internal/exporter/writer/` writes files or sends data to remote endpoints.
5. `internal/server/` serves the REST API and Prometheus metrics.

Important locations:

| Path | Purpose |
|---|---|
| `cmd/fleetint/` | CLI entry point and subcommands |
| `internal/config/` | Environment-variable and flag configuration |
| `internal/server/` | HTTP and Prometheus server |
| `internal/exporter/` | Collection, conversion, and export pipeline |
| `internal/registry/` | Component registration and initialization |
| `internal/scan/` | One-time health scan |
| `internal/enrollment/` | Backend credential exchange |
| `otelcol/auth/sakauth/` | Separate Go module for collector authentication |
| `third_party/fleet-intelligence-sdk/` | Vendored SDK and separate Go module |

## Permitted Work

Make focused changes needed for the requested issue. Preserve existing public
APIs, configuration defaults, wire formats, and persisted state unless the task
explicitly requires a compatible migration.

Do not:

- Commit credentials, enrollment tokens, private keys, `.env` files, or other
  secrets.
- Modify generated files or vendored dependencies unless the task requires it.
- Reformat, rename, or refactor unrelated code.
- Change telemetry schemas, persisted state, or external API behavior without
  tracing all producers and consumers and adding compatibility tests.
- Push directly to `main` or merge a pull request.

The state directory `/var/lib/fleetint` contains node identity and enrollment
credentials. Changes affecting it must preserve upgrades, restarts, and crash
recovery.

## Development Commands

The project requires Go 1.26.6 or newer.

```bash
make fleetint          # Build the fleetint binary
make fmt               # Format Go source
make lint              # Run golangci-lint, or gofmt and go vet fallback
make test              # Run root and sakauth tests with the race detector
make vuln              # Run govulncheck for the root and sakauth modules
make docker-test       # Run the full test suite in the project container
make package-snapshot  # Build local release packages
```

The repository contains three Go modules. Dependency or vulnerability changes
must cover each applicable module:

- `.`
- `otelcol/auth/sakauth`
- `third_party/fleet-intelligence-sdk`

Run the narrowest relevant tests while iterating, then run `make docker-test`
before considering a behavior change complete. If Docker or GPU-dependent tests
cannot run, report the exact limitation and remaining risk.

## Coding Conventions

- Follow standard Go conventions and keep changes simple and local.
- Use the standard library instead of adding one-use wrappers or abstractions.
- Propagate context cancellation through blocking and outbound operations.
- Make cleanup methods such as `Stop`, `Close`, and `Shutdown` idempotent.
- Keep user-facing errors generic while retaining diagnostic detail in logs.
- Add contract-focused tests for every behavior change, including failure and
  repeated-cleanup paths where applicable.
- Use `github.com/dsx-ai-factory/fleet-intelligence-agent` as the local import
  prefix configured by `.golangci.yml`.

Go map iteration is nondeterministic. Sort by a stable identifier before map
data becomes an ordered artifact such as a slice, CSV row sequence, OTLP
attribute list, hash input, or user-visible history. Preserve order when it has
real semantics such as chronology, priority, ranking, or topology.

## Contribution Workflow

- Start changes from `main` on a focused feature branch.
- Keep commits atomic and use the `type: description` commit format documented
  in [CONTRIBUTING.md](CONTRIBUTING.md).
- Sign every commit for DCO compliance with `git commit -s`.
- Include verification results and any residual risk in the pull request.

# Contributing to Fleet Intelligence Agent

## Code of Conduct

All contributors must follow the project [Code of Conduct](CODE_OF_CONDUCT.md).

## Issue Tracking

Please start all enhancement, bugfix, or change requests by opening a GitHub issue. Include clear reproduction steps, expected behavior, and environment details. Issues will be triaged and prioritized by maintainers before code review.

## Development

### Prerequisites
- Go 1.26.6+ (see `go.mod`)
- Make
- golangci-lint (optional locally, required in CI)

First clone the source code from GitHub

```bash
git clone https://github.com/dsx-ai-factory/fleet-intelligence-agent.git
```

Build Fleet Intelligence Agent from source

```bash
cd fleetint
make all           # or: make fleetint

./bin/fleetint -h
```

Common development targets:

```bash
make fmt   # format code with gofmt
make lint  # run linting (golangci-lint if available)
make test  # run unit tests with coverage
```

## Testing

We highly recommend writing tests for new features or bug fixes and ensuring all tests pass before submitting a PR.

To run tests locally:

```bash
make test
```

## Documentation

The agent documentation lives in `docs/` and is published to
[docs.nvidia.com/fleet-intel/agent](https://docs.nvidia.com/fleet-intel/agent).
Published docs are built from release tags — editing `docs/*.md` on your branch
is all that is needed; the CI publish workflow handles versioning automatically
when a release is cut.

### Prerequisites

Install the Fern CLI (requires Node.js 18+):

```bash
npm install -g fern-api
```

### Previewing HEAD docs (your working changes)

To preview the current state of `docs/` as it would appear in the docs site:

```bash
fern docs dev
```

This uses the `navigation:` block in `fern/docs.yml` to serve your local
`docs/*.md` files directly. No version selector is shown — this mode is
intentional for quickly reviewing in-progress edits.

### Previewing with the version selector

To preview with the full version selector (current and previous versions as
they appear on the published site), first fetch the versioned content from
release tags:

```bash
python3 scripts/docs-fetch-versions.py --patch-fern-docs
fern docs dev
```

The `--patch-fern-docs` flag temporarily replaces the `navigation:` block in
`fern/docs.yml` with a versioned `versions:` block. When you are done
previewing, restore the file:

```bash
git checkout fern/docs.yml
```

> **Note:** `fern/versions/` is gitignored. Its contents are generated locally
> by the script and by CI at publish time — never commit them.

### Adding or removing a doc page

1. Add or remove the `.md` file under `docs/`.
2. Update the `navigation:` block in `fern/docs.yml` to match.
3. If the filename is non-obvious, add a title entry to `TITLE_MAP` in
   `scripts/docs-fetch-versions.py`.
4. Run `fern check` to validate the configuration before opening a PR.

## Developer Certificate of Origin (DCO)

```
Developer Certificate of Origin
Version 1.1

Copyright (C) 2004, 2006 The Linux Foundation and its contributors.

Everyone is permitted to copy and distribute verbatim copies of this
license document, but changing it is not allowed.
```

```
Developer's Certificate of Origin 1.1

By making a contribution to this project, I certify that:

(a) The contribution was created in whole or in part by me and I
    have the right to submit it under the open source license
    indicated in the file; or

(b) The contribution is based upon previous work that, to the best
    of my knowledge, is covered under an appropriate open source
    license and I have the right under that license to submit that
    work with modifications, whether created in whole or in part
    by me, under the same open source license (unless I am
    permitted to submit under a different license), as indicated
    in the file; or

(c) The contribution was provided directly to me by some other
    person who certified (a), (b) or (c) and I have not modified
    it.

(d) I understand and agree that this project and the contribution
    are public and that a record of the contribution (including all
    personal information I submit with it, including my sign-off) is
    maintained indefinitely and may be redistributed consistent with
    this project or the open source license(s) involved.
```

### How to Sign Your Work

To sign your work and agree to the DCO, you must add a sign-off to every git commit. This is done by using the `-s` flag when committing:

```bash
git commit -s -m "Your commit message"
```

This will append a line that looks like:

```
Signed-off-by: Your Name <your.email@example.com>
```

You must use your real name and a valid email address. Anonymous contributions or contributions under pseudonyms are not accepted.

If you forget to add the sign-off to a commit, you can amend it:

```bash
git commit --amend --signoff
```

For more information about the DCO, see: https://developercertificate.org/

## Releases

Maintainers should follow the documented [release process](RELEASE.md). Release
artifacts are built and published by CI from protected version tags.

## Pull Request Process

1. **Fork the Repository**: Create a personal fork of the Fleet Intelligence Agent repository on GitHub.

2. **Create a Branch from Main**: Create a new branch for your changes from the main branch:
   ```bash
   git checkout -b your-feature-name
   ```

3. **Make Your Changes**: Implement your changes following the coding standards outlined below.

4. **Test Your Changes**: Ensure all tests pass and add new tests for your changes if applicable.

5. **Submit Pull Request**: Follow the commit and PR conventions below, including DCO sign-offs, a standalone description, linked issues, validation, and breaking-change notes.

6. **Code Review**: Address feedback from maintainers. Maintainers squash merge approved PRs according to the policy below.

## Commit and Pull Request Conventions

Use the same format for commit subjects and pull request titles:

```text
<type>(<scope>): <description>
```

- Allowed types: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `chore`, `revert`.
- Scope is optional and lowercase, for example `cli`, `inventory`, or `deps`.
- Write a concise, imperative description, for example `fix(inventory): handle missing chassis`.
- For breaking changes, add `!` after the type/scope, for example `feat(cli)!: remove deprecated flag`, and explain the impact and migration in the PR body.
- Sign off every commit with `git commit -s`, using your real name and a valid email address, to comply with the DCO.
- Name branches `<type>/<short-dash-separated-name>`, for example `fix/missing-chassis` or `docs/configuration-guide`. Use one of the allowed types above.
- Target `main` unless maintainers specify a release branch.

PR descriptions must explain the behavior change, link related GitHub issues
(use `Closes #123` when appropriate), report validation and remaining limitations,
and highlight breaking changes. Add relevant tests and documentation, follow the
PR template, and ensure CI passes. PR titles describe the final user-visible
result and may appear in release notes.

Maintainers should **squash merge** each focused PR, using the PR title as the
final commit subject. Contributors may keep separate review commits; they do not
need to squash before review. Preserve all contributors' DCO `Signed-off-by:`
trailers in the final squash commit and verify the final message before merging.

### PR title validation

The **Contribution conventions / PR title** GitHub Actions check validates PR
titles when a PR is opened, edited, or updated. Maintainers should require this
check in branch protection and configure squash merging to use the PR title as
the final commit subject. Local commit-format hooks are optional; individual
commit messages do not become target-branch subjects when squash merging.
Contributors must still sign off every commit, and maintainers must preserve
those sign-offs in the final squash commit.

## Coding Standards

Ensure your code is clean, readable, and well-commented. We use the following tools and guidelines:

### Go Code Standards
- Follow standard Go conventions and idioms
- Use `gofmt` for code formatting (run `make fmt`)
- Use `golangci-lint` for linting (run `make lint`)
- Import grouping: third-party imports must be separated from local imports. goimports is configured with `local-prefixes: github.com/dsx-ai-factory/fleet-intelligence-agent` in `.golangci.yml`. If imports are regrouped incorrectly, run `make fmt` and `make lint`.

To run linting locally:

```bash
make lint
```

### Deterministic output

Go randomizes map iteration order on every run. Anything the backend stores, diffs,
hashes, or shows as history must not carry that randomness, or unchanged machine
state reads as a stream of changes that never happened. A single component leaking
map order into `extra_info` produced one alert with 4,824 pages of history entries
that differed only by GPU ordering.

Iterating a map is fine. The rule applies the moment the result becomes an **ordered
artifact**:

- a slice built by `append` inside `for k, v := range someMap`
- a string built with `strings.Join` from such a slice
- a JSON array — including any struct marshaled into `ExtraInfo["data"]`
- `HealthState.Reason`, `HealthState.Incidents`, machine-info and inventory device lists
- OTLP `KeyValueList` and log-record order, Prometheus label order, CSV row order

Sort by a stable key before the value leaves the producer, not at each place it is
rendered. Canonical keys: GPU → UUID, mount target → target path, component → name.
A plain `map[string]string` passed to `encoding/json` is already safe, because
`encoding/json` sorts map keys — the exposure is a *slice* built from map iteration.

`persistenceModesFromFieldValues` in the SDK is the reference implementation: it
copies the device slice, sorts by UUID → BusID → ID, and only then builds output.
The export pipeline uses `slices.Sorted(maps.Keys(m))` to walk maps in lexical key
order, and `internal/inventory/hash.go` has the generic order-insensitive normalizer
for whole structs.

Any change that adds or reshapes one of these output boundaries needs a test.
`TestOTLPConversionIsStableAcrossRepeatedConversions` in
`internal/exporter/converter/ordering_test.go` is the shape to copy: run the same
input repeatedly and require identical output. Do not write a test that asserts the
result is *one of* several acceptable orderings — that locks the bug in.

### General Guidelines
- Write clear, descriptive commit messages
- Keep commits focused and atomic
- Sign off every commit with `git commit -s`
- Add comments for non-trivial logic
- Update documentation when adding or changing features
- Ensure backward compatibility when possible

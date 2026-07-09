# Contributing to Fleet Intelligence Agent

## Issue Tracking

Please start all enhancement, bugfix, or change requests by opening a GitHub issue. Include clear reproduction steps, expected behavior, and environment details. Issues will be triaged and prioritized by maintainers before code review.

## Development

### Prerequisites
- Go 1.26.5+ (see `go.mod`)
- Make
- golangci-lint (optional locally, required in CI)

First clone the source code from GitHub

```bash
git clone https://github.com/NVIDIA/fleet-intelligence-agent.git
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

## Pull Request Process

1. **Fork the Repository**: Create a personal fork of the Fleet Intelligence Agent repository on GitHub.

2. **Create a Branch from Main**: Create a new branch for your changes from the main branch:
   ```bash
   git checkout -b your-feature-name
   ```

3. **Make Your Changes**: Implement your changes following the coding standards outlined below.

4. **Test Your Changes**: Ensure all tests pass and add new tests for your changes if applicable.

5. **Squash Commits**: Before finalizing your pull request, squash multiple commits into a single, clean commit:
   ```bash
   # Interactive rebase to squash commits (replace N with number of commits)
   git rebase -i HEAD~N
   
   # Or squash all commits in your feature branch
   git rebase -i main
   ```
   Choose "squash" (or "s") for commits you want to combine.

6. **Sign-off Final Commit**: Make sure your final squashed commit is signed off according to the DCO requirements:
   ```bash
   # If you need to add sign-off to your final commit
   git commit --amend --signoff
   ```
   Your final commit should have:
   - A properly formatted commit message (see format below)
   - Proper DCO sign-off
   - A single logical change

   **Commit Message Format:**
   ```
   feat: add GPU temperature monitoring

   - Implement temperature threshold checking
   - Add Prometheus metrics for temperature alerts
   - Include unit tests for temperature validation

   Signed-off-by: Your Name <your.email@example.com>
   ```
   
   Format: `type: brief description`
   - **Type**: `feat`, `fix`, `docs`, `test`, `refactor`, `perf`, `chore`
   - **Description**: Clear, imperative mood summary (e.g., "Add feature" not "Added feature")

7. **Submit Pull Request**: Create a pull request against the main branch with:
   - A clear title in the same default format as commits, for example `feat: add GPU temperature monitoring`
   - A description with a concise summary of the change
   - If there is a related GitHub issue, reference it in the PR body using a format like `[#123]`
   - Summary of changes made
   - Any breaking changes highlighted

8. **Code Review**: Address any feedback from maintainers during the review process.

## Coding Standards

Ensure your code is clean, readable, and well-commented. We use the following tools and guidelines:

### Go Code Standards
- Follow standard Go conventions and idioms
- Use `gofmt` for code formatting (run `make fmt`)
- Use `golangci-lint` for linting (run `make lint`)
- Import grouping: third-party imports must be separated from local imports. goimports is configured with `local-prefixes: github.com/NVIDIA/fleet-intelligence-agent` in `.golangci.yml`. If imports are regrouped incorrectly, run `make fmt` and `make lint`.

To run linting locally:

```bash
make lint
```

### General Guidelines
- Write clear, descriptive commit messages
- Keep commits focused and atomic
- Sign off every commit with `git commit -s`
- Add comments for non-trivial logic
- Update documentation when adding or changing features
- Ensure backward compatibility when possible

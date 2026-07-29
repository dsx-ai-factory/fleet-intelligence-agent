# Development

## Setup

### Clone Repository

```bash
git clone https://github.com/NVIDIA/fleet-intelligence-agent.git
cd fleet-intelligence-agent
```

### Prerequisites

- **Go**: 1.26.2
- **GoReleaser**: For building packages (install from [goreleaser.com](https://goreleaser.com/install/))
- **For ARM64 cross-compilation** (optional): `gcc-aarch64-linux-gnu` and `g++-aarch64-linux-gnu`

### Build

```bash
# Build the binary
make fleetint

# Build all targets
make all

# Run tests
make test
```

## Project Structure

```
fleetint/
├── cmd/
│   └── fleetint/          # Main CLI application
├── internal/
│   ├── cmdutil/            # Command utilities
│   ├── config/             # Configuration management
│   ├── exporter/           # Data export functionality
│   │   ├── collector/      # Data collection
│   │   ├── converter/      # Format conversion (CSV, OTLP)
│   │   └── writer/         # Output writers (file, HTTP)
│   ├── machineinfo/        # Machine information gathering
│   ├── server/             # HTTP API server
│   └── version/            # Version information
├── deployments/
│   └── packages/           # Package build configurations
│       ├── scripts/        # Install/uninstall scripts
│       └── systemd/        # Systemd service files
└── docs/                   # Documentation
```

## Building Packages

Packages are built using [GoReleaser](https://goreleaser.com), which handles .deb, .rpm, and tarball creation automatically.

### Install GoReleaser

```bash
# Using Homebrew (macOS/Linux)
brew install goreleaser

# Using snap (Linux)
snap install --classic goreleaser

# Or download from https://github.com/goreleaser/goreleaser/releases
```

### Build Snapshot Packages

Build packages without a git tag (for testing):

```bash
# Using Make
make package-snapshot

# Or directly with GoReleaser
goreleaser release --snapshot --clean
```

This creates in the `dist/` directory:
- `.deb` packages for Debian/Ubuntu (amd64, arm64)
- `.rpm` packages for RHEL/Rocky/AlmaLinux 8, 9, 10 (x86_64, aarch64)
- Binary tarballs for direct installation
- Checksums and metadata

### Create a Release

To create an official release, tag a version and push it to GitHub. The GitHub Actions workflow will automatically build and publish the release:

```bash
# Tag a version
git tag v0.2.0

# Push the tag to trigger the release workflow
git push origin v0.2.0
```

The GitHub Actions workflow will:
- Build packages for all supported platforms (amd64, arm64)
- Create `.deb` packages for Ubuntu
- Create `.rpm` packages for RHEL/Rocky/AlmaLinux 8, 9, 10
- Generate binary tarballs
- Publish the release with all artifacts to GitHub Releases

## Testing

### Run Tests with Coverage

```bash
make test
```

This runs all tests and generates a coverage report in `coverage/coverage.html`.

### Run Linting

```bash
make lint
```

Runs `golangci-lint` if installed, otherwise runs basic Go formatting and vet checks.

### Format Code

```bash
make fmt
```

### Vulnerability Scanning

```bash
make vuln
```

Scans the built binary for known vulnerabilities using `govulncheck`.

### Manual Testing

```bash
# Build and run locally
make fleetint
./bin/fleetint scan

# Test with different configurations
./bin/fleetint run --log-level=debug --listen-address=127.0.0.1:8080

# Test offline mode
./bin/fleetint run --offline-mode --path=/tmp/test --duration=00:01:00 --format=csv
```

## Development Workflow

1. **Create a branch** for your changes
2. **Make changes** and add tests
3. **Run tests**: `make test`
4. **Format code**: `make fmt`
5. **Run linting**: `make lint`
6. **Build locally**: `make fleetint`
7. **Test changes** manually
8. **Build packages** (optional): `make package-snapshot`
9. **Submit PR** with description of changes

## Code Guidelines

- Follow standard Go conventions and idioms
- Add tests for new functionality
- Update documentation for user-facing changes
- Ensure all tests pass before submitting PRs
- Use `make fmt` to format code before committing
- Keep commits focused and write clear commit messages

## Available Make Targets

```bash
make help        # Show all available targets
make all         # Build all binaries
make fleetint   # Build fleetint binary
make test        # Run tests with coverage
make lint        # Run linting tools
make fmt         # Format Go code
make vuln        # Run vulnerability check
make clean       # Clean up binaries and artifacts
make package-snapshot  # Build packages (no git tag required)
```

## Related Projects

- **[leptonai/gpud](https://github.com/leptonai/gpud)**: Upstream dependency providing core monitoring functionality

## Contributing

See [CONTRIBUTING.md](https://github.com/NVIDIA/fleet-intelligence-agent/blob/main/CONTRIBUTING.md) for detailed contribution guidelines.

# Stack-Converter

[![Go Build & Test](https://github.com/stackvity/stack-converter/actions/workflows/ci.yaml/badge.svg)](https://github.com/stackvity/stack-converter/actions/workflows/ci.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/stackvity/stack-converter)](https://goreportcard.com/report/github.com/stackvity/stack-converter)
[![Go Reference](https://pkg.go.dev/badge/github.com/stackvity/stack-converter/converter.svg)](https://pkg.go.dev/github.com/stackvity/stack-converter/converter)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Conventional Commits](https://img.shields.io/badge/Conventional%20Commits-1.0.0-%23FE5196?logo=conventionalcommits&logoColor=white)](https://conventionalcommits.org)
[![GitHub release (latest SemVer)](https://img.shields.io/github/v/release/stackvity/stack-converter)](https://github.com/stackvity/stack-converter/releases)
[![Codecov](https://codecov.io/gh/stackvity/stack-converter/branch/main/graph/badge.svg?token=YOUR_CODECOV_TOKEN_HERE)](https://codecov.io/gh/stackvity/stack-converter) <!-- Replace YOUR_CODECOV_TOKEN_HERE if using Codecov -->

`stack-converter` is an advanced, open-source system designed to generate structured Markdown documentation directly from source code directories. It provides both a core Go library (`converter`) and a sophisticated Command Line Interface (CLI) tool.

**Vision:** To establish `stack-converter` as the industry-leading system for generating Markdown documentation from source code, known for its accuracy, performance, customization, and developer experience.

**Purpose:** Automate the creation and maintenance of documentation that accurately reflects the current state of a codebase, reducing manual effort and preventing "documentation rot."

## Features

Based on `proposal.md`:

- **Rich Terminal UI (TUI):** Interactive display for real-time progress and status (using `bubbletea`).
- **Intelligent Language Detection:** Content-based analysis (`go-enry`) with extension fallbacks and user overrides.
- **Performance Focus:**
  - **Parallel Processing:** Utilizes multiple CPU cores (`--concurrency`).
  - **Content-Based Caching:** Smart cache (`.stackconverter.cache`) invalidates based on content and relevant configuration changes.
  - **Git Differentials:** Process only changed files using `--git-diff-only` or `--git-since REF`.
- **Deep Customization:**
  - **Layered Configuration:** Flags > Environment Variables (`STACKCONVERTER_*`) > Profile (`--profile`) > Config File (`stack-converter.yaml`) > Defaults.
  - **Go Templating:** Customize Markdown output using Go's `text/template` engine (`--template`).
  - **Front Matter:** Optionally prepend YAML or TOML front matter for static site generators (`frontMatter` config).
- **Robust & Deterministic:** Handles character encodings, binary files, large files predictably. Aims for identical output for identical input+config.
- **Workflow Integration:**
  - **Watch Mode:** Automatically re-run on file changes (`--watch`).
  - **CI/CD Friendly:** Non-interactive modes (`--no-tui`), structured JSON output (`--output-format json`), reliable exit codes.
  - **Pre-commit Hook Support:** Designed to work efficiently within pre-commit checks.
- **Extensibility:**
  - **Plugins:** Add custom processing steps via external scripts/executables (JSON/stdio interface).
  - **Go Library:** Core logic exposed as a reusable Go package (`pkg/converter/`).

## Installation

Choose one of the following methods:

### Option A: Download Pre-compiled Binary (Recommended for most users)

**Prerequisites:**

- A tool to download files (e.g., `curl`, `wget`, web browser).
- A tool to extract archives (e.g., `tar` for `.tar.gz`, standard tools for `.zip`).
- A tool to verify checksums (e.g., `sha256sum` on Linux/macOS, `certutil` on Windows).
- _(Optional)_ `gpg` installed for signature verification.

**Steps:**

1.  **Visit GitHub Releases:** Go to the [**Releases**](https://github.com/stackvity/stack-converter/releases) page.
2.  **Download:** Find the latest release and download the archive (`.tar.gz` for Linux/macOS, `.zip` for Windows) appropriate for your operating system and architecture (e.g., `stack-converter_X.Y.Z_linux_amd64.tar.gz`).
3.  **Verify (Recommended):**
    - Download the `checksums.txt` file from the same release.
    - Verify the integrity using the checksum:
      - **Linux/macOS:** `sha256sum -c checksums.txt --ignore-missing` (Check output for `OK`).
      - **Windows (PowerShell):** `(Get-FileHash stack-converter_*.zip -Algorithm SHA256).Hash.ToLower() -eq ((Get-Content checksums.txt | Select-String -Pattern "stack-converter_.*.zip").Line -split '\s+')[0]` (Should output `True`).
    - **(Optional) GPG Signature Verification:**
      - Download the corresponding `.asc` signature file (e.g., `checksums.txt.asc` or `stack-converter_*.zip.asc`).
      - **Obtain the GPG Public Key:** The public key used for signing can typically be found linked from the project's security documentation or website. You might import it via:
        ```bash
        # Example: Replace [URL_TO_KEY] or [KEY_FINGERPRINT] with actual values
        # curl -sL [URL_TO_KEY] | gpg --import
        # OR
        # gpg --keyserver keyserver.ubuntu.com --recv-keys [KEY_FINGERPRINT]
        ```
      - Verify the signature: `gpg --verify checksums.txt.asc checksums.txt` (or verify the specific binary archive signature). Look for "Good signature from..." in the output.
4.  **Extract:** Unpack the archive (e.g., `tar -xzf stack-converter_*.tar.gz` or using Windows Explorer). This will contain the `stack-converter` executable.
5.  **Install (Move to PATH - Recommended):** To run `stack-converter` from any directory, move the extracted executable to a directory listed in your system's `PATH` environment variable and ensure it has execute permissions.
    - **Linux/macOS:**
      ```bash
      sudo mv stack-converter /usr/local/bin/
      sudo chmod +x /usr/local/bin/stack-converter
      ```
    - **Windows:**
      1.  Create a directory if needed (e.g., `C:\bin` or `C:\Tools`).
      2.  Add this directory to your System or User `Path` environment variable. (Search "Edit environment variables for your account").
      3.  Move `stack-converter.exe` to that directory.
6.  **Verify:** Open a **new** terminal/PowerShell window (to ensure PATH changes are recognized) and run:
    ```bash
    stack-converter --version
    ```
    It should print the version information in the format `stack-converter version X.Y.Z (commit: ..., built: ...)`.

### Option B: Using `go install`

**Prerequisites:**

- **Go >= 1.19** installed (check `go version`).
- **Git** installed (check `git --version`).
- Your Go binary path (`$GOPATH/bin` or `$HOME/go/bin`) must be in your system `PATH`. Check Go documentation for setup.

```bash
# Install the latest tagged release
go install github.com/stackvity/stack-converter/cmd/stack-converter@latest

# Verify installation
stack-converter --version
```

### Option C: Build from Source

**Prerequisites:**

- **Go >= 1.19** installed.
- **Git** installed.
- **`make`** installed (recommended).

```bash
# Clone the repository
git clone https://github.com/stackvity/stack-converter.git
cd stack-converter

# Optional: Checkout a specific version tag
# git checkout vX.Y.Z

# Build using Makefile (preferred, includes version info)
make build
# The executable will be in ./bin/stack-converter

# OR Build using go build (may lack embedded version info)
# go build -ldflags="-s -w" -o stack-converter ./cmd/stack-converter

# Verify build
./bin/stack-converter --version
# Expected output includes version, commit, and build date.

# Optional: Move the executable to your PATH (see Option A, Step 5)
# sudo mv ./bin/stack-converter /usr/local/bin/
```

## Basic Usage

```bash
# Simple run: Process 'src/' directory, output to 'docs/'
stack-converter -i ./src -o ./docs

# Use a specific configuration file and profile
stack-converter --config ./configs/prod.yaml --profile website

# Run in watch mode for local development
stack-converter -i ./lib -o ./build/docs --watch

# Run in CI, processing only changes since main branch, output JSON report
stack-converter --profile ci --git-since origin/main --output-format json --no-tui > report.json

# Get detailed help on all flags
stack-converter --help
```

## Configuration

`stack-converter` uses a layered configuration system ([proposal.md Section 2.3](docs/proposal.md#23-configuration--control-detailed)) with the following precedence (highest first):

1.  **Command-line Flags:** e.g., `--concurrency 16`
2.  **Environment Variables:** e.g., `STACKCONVERTER_CONCURRENCY=8`
3.  **Profile:** e.g., settings in `profiles.ci` activated by `--profile ci`
4.  **Configuration File:** e.g., `stack-converter.yaml` found in `.`, `$HOME/.config/stack-converter/`, etc.
5.  **Built-in Defaults:** Sensible defaults defined in the application.

See the example `configs/stack-converter.yaml` for a detailed overview of configurable options.

**Ignoring Files:** Create a `.stackconverterignore` file (uses `.gitignore` syntax) in your input directory or parent directories, or use the `--ignore` flag / `ignore:` list in `stack-converter.yaml`.

## Documentation

- **Proposal Document (`docs/proposal.md`):** The main design and feature specification.
- **Technology Stack (`docs/techstack.md`):** Overview of technologies used.
- **System Outline (`docs/system-outline.md`):** High-level overview and NFRs.
- **CI/CD (`docs/cicd.md`):** Details on the build, test, and release pipeline.
- **API Contracts:**
  - **Plugin Schema (`docs/plugin_schema.md`):** Contract for external plugins.
  - **Report Schema (`docs/report_schema.md`):** Contract for JSON report output.
  - **Template Guide (`docs/template_guide.md`):** Guide for custom template authors.
  - **Library API (Godoc):** [pkg.go.dev/github.com/stackvity/stack-converter/converter](https://pkg.go.dev/github.com/stackvity/stack-converter/converter)
- **Development & Contribution (`CONTRIBUTING.md`):** Guidelines for contributors.

## Contributing

We welcome contributions! Please see `CONTRIBUTING.md` for details on reporting bugs, suggesting features, and submitting code changes.

## License

`stack-converter` is licensed under the MIT License. See the `LICENSE` file for details.

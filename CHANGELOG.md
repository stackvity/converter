# Changelog

All notable changes to `stack-converter` will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html). Release notes are primarily generated automatically by `goreleaser` based on Conventional Commits included in merged Pull Requests since the last tag. This file may contain curated summaries or highlights from those notes.

_(Note: TD-011 tracks the potential future automation of updating this specific file.)_

## [Unreleased]

### Added

- Initial project structure based on `folder-structure.md`.
- Basic CLI setup using Cobra (`--help`, `--version`).
- Initial Go module setup (`go.mod`).
- Core data structures (`Options`, `Report`) defined.
- Layered configuration setup using Viper (Defaults, File, Env, Flags, Profiles).
- Core library interfaces defined (`Hooks`, `CacheManager`, `GitClient`, `PluginRunner`, etc.).
- Basic `slog` logging integration.
- Initial `Makefile` for build, test, lint, fmt, run, clean.
- CI workflow (`ci.yaml`) for builds, tests, linting, formatting, vetting, vulnerability scans, coverage upload.
- GoReleaser configuration (`.goreleaser.yaml`) for cross-platform builds and checksums.
- Release workflow (`release.yaml`) using GoReleaser.
- Documentation structure (`README.md`, `CONTRIBUTING.md`, `docs/`).
- Initial definition of Plugin Schema (`docs/plugin_schema.md`), Report Schema (`docs/report_schema.md`), Template Guide (`docs/template_guide.md`).
- Requirements Traceability Matrix setup (`docs/RTM.md`).
- Initial definition of exported errors (`pkg/converter/errors.go`).

### Changed

- N/A

### Deprecated

- N/A

### Removed

- N/A

### Fixed

- N/A

### Security

- N/A

## [0.1.0] - YYYY-MM-DD <!-- Replace with actual release date -->

### Added

- _(Features completed in Sprint 1 will be listed here based on Conventional Commits)_
- Foundational project setup (`SC-EPIC-004`).
- Core data structure definitions (`SC-EPIC-001` part).

_(More releases and change details will be added here as the project progresses and versions are tagged and released via the GoReleaser workflow.)_

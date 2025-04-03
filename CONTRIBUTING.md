# Contributing to Stack-Converter

Thank you for your interest in contributing to `stack-converter`! We appreciate your help in making this tool better. Please take a moment to review these guidelines to ensure a smooth contribution process.

## Code of Conduct

This project adheres to the Contributor Covenant Code of Conduct. By participating, you are expected to uphold this code. Please report unacceptable behavior to the project maintainers. [**Link to CODE_OF_CONDUCT.md - To Be Created**]

## How Can I Contribute?

- **Reporting Bugs:** If you find a bug, please [search existing issues](https://github.com/stackvity/stack-converter/issues) first. If it hasn't been reported, [create a new issue](https://github.com/stackvity/stack-converter/issues/new/choose) using the "Bug report" template. Provide clear steps to reproduce, expected behavior, actual behavior, your environment details (OS, Go version, tool version), and relevant logs or screenshots.
- **Suggesting Enhancements:** Have an idea for a new feature or an improvement to an existing one? [Search existing issues and discussions](https://github.com/stackvity/stack-converter/discussions) first. If it's a new idea, [create a new issue](https://github.com/stackvity/stack-converter/issues/new/choose) using the "Feature request" template or start a discussion. Provide a clear description of the enhancement and its potential value.
- **Improving Documentation:** Found typos, unclear explanations, or missing information in the `README.md`, `--help` output, or files within the `/docs` directory? Feel free to submit a Pull Request with your corrections or suggestions. For significant documentation changes, please open an issue first.
- **Writing Code:** If you'd like to fix a bug or implement a new feature:
  1.  Find or create an issue describing the work. Discuss the approach with maintainers if it's a significant change.
  2.  Follow the Development Workflow outlined below.
  3.  Submit a Pull Request.

## Development Workflow

1.  **Fork & Clone:** Fork the repository on GitHub and clone your fork locally:
    ```bash
    git clone https://github.com/YOUR_USERNAME/stack-converter.git
    cd stack-converter
    ```
2.  **Prerequisites:** Ensure you have the necessary tools installed (see [README.md#Installation - Option C: Build from Source](README.md#option-c-build-from-source)). The key prerequisites are:
    - Go (version >= 1.19, as specified in `go.mod`)
    - Git (latest stable recommended)
    - `make` (recommended for using development targets)
3.  **Install Dev Tools:** Run `make install-tools` to install development dependencies like `golangci-lint`. Ensure `$GOPATH/bin` or `$HOME/go/bin` is in your system `PATH`.
4.  **Branching:** Create a new branch for your feature or bug fix from the `main` branch. Use a descriptive name (e.g., `feature/add-xml-support`, `fix/cache-invalidation-bug-123`).
    ```bash
    git checkout main
    git pull origin main # Ensure your main is up-to-date
    git checkout -b feature/my-new-feature
    ```
    _(Ref: `cicd.md#2.3` - GitHub Flow)_
5.  **Code:** Write your code, adhering to the project's style guidelines (see below).
6.  **Test:**
    - Write unit tests for new or modified logic in the `converter` library (`pkg/converter/`). Maintain or improve test coverage (>80% target).
    - Write integration tests for CLI changes or interactions between CLI and library.
    - Run tests frequently: `make test` (includes `-race` detector).
7.  **Format & Lint:** Ensure your code is correctly formatted and passes lint checks:
    ```bash
    make fmt
    make lint
    ```
8.  **Build:** Verify the project builds successfully: `make build`.
9.  **Commit:** Make small, atomic commits using the **Conventional Commits** format. This is **required** for automated release notes.
    - **Format:** `<type>[optional scope]: <description>`
    - **Types:** `feat`, `fix`, `build`, `chore`, `ci`, `docs`, `style`, `refactor`, `perf`, `test`.
    - **Example:** `feat: add support for TOML front matter`
    - **Example:** `fix: correct cache invalidation on template change`
    - **Example:** `docs: update plugin schema documentation`
    - _(Ref: `cicd.md#7.5`)_
10. **Documentation:** Update relevant documentation (`README.md`, `/docs` files, Godoc comments) to reflect your changes. If your changes impact external contracts (API, Schemas), ensure those documents are updated accurately. **Crucially, update the Requirements Traceability Matrix (`/docs/RTM.md`)** to link your changes back to requirements, user stories, or test cases as appropriate (see `SC-STORY-072` for process).
11. **Push:** Push your branch to your fork: `git push origin feature/my-new-feature`.
12. **Pull Request:** Open a Pull Request (PR) against the `stackvity/stack-converter` `main` branch.
    - Use the PR template (`.github/PULL_REQUEST_TEMPLATE.md`).
    - Provide a clear description of the changes and link the related issue(s).
    - Ensure all CI checks pass.
13. **Review & Merge:** Wait for code review from maintainers. Address any feedback. Once approved and CI is green, a maintainer will merge the PR. _(Ref: `cicd.md#7.1`)_

## Code Style & Conventions

- **Go:** Follow standard Go formatting (`gofmt -s`). Use `goimports` for import management. Adhere to principles from [Effective Go](https://go.dev/doc/effective_go).
- **Linting:** Pass all checks configured in `.golangci.yaml`. Run `make lint` locally.
- **Documentation:** Write comprehensive Godoc comments for all exported functions, types, variables, constants, and especially interface methods (detailing contracts, including thread-safety). Use Markdown for documentation files in `/docs`.
- **Error Handling:** Use standard Go error handling (`if err != nil`). Wrap errors for context using `fmt.Errorf("...: %w", err)`. Prefer returning specific, potentially exported error variables/types (`pkg/converter/errors.go`) where appropriate for callers to check using `errors.Is` / `errors.As`.
- **Interface Placement:** Public interfaces consumed by the library (`converter.Options` fields like `Hooks`, `CacheManager`, etc.) are defined within the `pkg/converter/` package (or subpackages like `pkg/converter/cache/`). Concrete implementations for the default CLI reside in the `internal/cli/` subpackages (e.g., `internal/cli/hooks/`, `internal/cli/runner/`). This maintains a clear separation between the library's contract and the CLI's specific implementation.

## Testing

- **Unit Tests:** Placed in `*_test.go` files alongside the code being tested (primarily within `pkg/converter/` and its subpackages). Use the standard `testing` package and mocks (from `internal/testutil`) to isolate units. Aim for >80% coverage for the `converter` library.
- **Integration Tests:** Test interactions between CLI and LIB, or LIB components with filesystem/Git fixtures. May reside in `*_test.go` files or the `/test/e2e` directory.
- **End-to-End Tests:** Execute the final binary against fixtures. Reside in `/test/e2e`.
- **Race Detection:** Run tests with the `-race` flag (`make test` includes this). Fix any detected data races.
- See `test-case.md` for detailed test suites and strategy.

## Reporting Issues

Please report bugs or suggest features using the [GitHub Issues](https://github.com/stackvity/stack-converter/issues) page. Use the provided templates for clarity.

Thank you for contributing!

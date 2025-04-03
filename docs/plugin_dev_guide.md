# Stack-Converter Plugin Development Guide

**Document Version:** 1.0
**Applies to Stack-Converter Version:** v3.x (Protocol Schema `1.0`)
**Date:** [Current Date]
**Status:** Draft

## 1. Introduction

Welcome to the `stack-converter` Plugin Development Guide! This guide provides information on how to create external plugins to extend the functionality of the `stack-converter` tool.

Plugins allow you to inject custom logic into the documentation generation pipeline at specific stages, enabling advanced customization, integration with other tools, or specialized analysis beyond the core features of `stack-converter`.

**Prerequisites:** Familiarity with the core concepts of `stack-converter` (see `README.md` and `docs/proposal.md`) and the command line. Basic understanding of JSON.

**Authoritative Protocol Specification:** This guide explains the practical aspects of plugin development. For the **definitive, versioned technical specification** of the JSON/stdio communication protocol, including the exact structure of `PluginInput` and `PluginOutput`, please refer to:

- [**`docs/plugin_schema.md`**](./plugin_schema.md)

**Schema Versioning:** The current stable protocol version is **`1.0`**. Plugins **must** check the `$schemaVersion` field in the input they receive and **must** return the correct `$schemaVersion` ("1.0") in their output. Refer to the "Versioning" section in `docs/plugin_schema.md` for compatibility details.

## 2. Plugin Concepts

### 2.1. How Plugins Work

`stack-converter` executes configured plugins as separate, external processes. Communication happens via standard input (`stdin`) and standard output (`stdout`) using JSON messages.

**Workflow:**

1.  **Configuration:** You define your plugin in the `stack-converter.yaml` file, specifying its name, stage, command, and applicability rules.
2.  **Invocation:** When `stack-converter` processes a file and reaches a configured plugin stage (and the file matches `appliesTo` rules), it starts your plugin process using the specified `command`.
3.  **Input:** `stack-converter` sends a JSON object (`PluginInput`) containing file path, current content, metadata, and plugin-specific config to your plugin's `stdin`.
4.  **Execution:** Your plugin reads and parses the JSON from `stdin`, performs its custom logic, and prepares a result.
5.  **Output:** Your plugin marshals a JSON object (`PluginOutput`) containing any results (modified content, updated metadata, final output for formatters) or errors, and writes it to `stdout`.
6.  **Termination:** Your plugin exits with **code 0** for success (even if reporting a functional error via the JSON `error` field). A **non-zero exit code** signifies a crash or execution failure.
7.  **Integration:** `stack-converter` reads the plugin's `stdout`, parses the `PluginOutput` JSON, checks for errors, and integrates the results (e.g., updating content/metadata) before proceeding. It also captures `stderr` for logging.

### 2.2. Plugin Stages

Plugins operate at specific stages in the processing pipeline:

- **`preprocessor`:** Runs _after_ the initial file content is read and decoded to UTF-8, but _before_ language detection, comment extraction, templating, or front matter generation.
  - **Input:** Receives original (or prior preprocessor-modified) UTF-8 source content.
  - **Output:** Can return modified source content and/or updated metadata.
  - **Use Cases:** Stripping specific comments, code formatting (use with caution), injecting markers, extracting preliminary metadata.
- **`postprocessor`:** Runs _after_ the Go template has been executed and potential front matter has been generated, but _before_ the final content is written to the output file.
  - **Input:** Receives the generated Markdown content (including front matter, if any).
  - **Output:** Can return modified Markdown content and/or updated metadata (less common for postprocessors).
  - **Use Cases:** Adding standard headers/footers, modifying links, performing final linting/validation on Markdown output.
- **`formatter`:** Runs _instead_ of the standard Go templating and postprocessing stages. It is responsible for generating the _entire_ final output content for the file.
  - **Input:** Receives original UTF-8 source content and metadata.
  - **Output:** **Must** return the final file content in the `output` field of `PluginOutput`. Can optionally update metadata. Should generally _not_ return content in the `content` field.
  - **Use Cases:** Implementing entirely different output formats (e.g., converting reStructuredText to HTML, generating JSON from code), advanced syntax highlighting using external tools.

## 3. Configuring Plugins

Plugins are configured in your `stack-converter.yaml` file under the top-level `plugins` key. This key maps stage names to lists of plugin configurations.

```yaml
# In stack-converter.yaml
plugins:
  preprocessors: # List of plugins for the preprocessor stage
    - name: "strip-internal" # Descriptive name for logs
      enabled: true # Set to false to disable this plugin instance
      # Command to execute. MUST be path to executable + arguments as separate strings.
      # Avoid shell metacharacters here; stack-converter executes directly.
      command:
        [
          "python",
          "scripts/plugins/strip_internal.py",
          "--prefix",
          "// INTERNAL:",
        ]
      # Optional list of glob patterns. Plugin only runs if file path matches.
      # Empty list or omitted means it applies to ALL files in this stage.
      appliesTo:
        - "*.go"
        - "pkg/**/*.java"
      # Optional map passed as "config" field in PluginInput JSON.
      config:
        logLevel: "info"
        replacementText: "[Internal Code Removed]"

  postprocessors: # List of plugins for the postprocessor stage
    - name: "add-footer"
      enabled: true
      command: ["bash", "scripts/plugins/add_footer.sh"]
      # appliesTo omitted: runs for all files after templating

  formatters: # List of plugins for the formatter stage
    - name: "rst-to-html"
      enabled: false # Disabled for now
      command: ["rst2html.py", "--stylesheet-path=/path/to/styles.css"]
      appliesTo: ["*.rst"]
# Add other stages if they become available (e.g., languageHandlers)
```

**Configuration Fields:**

- `name` (string, required): A user-friendly name for identification in logs and reports.
- `enabled` (boolean, required): `true` to activate the plugin, `false` to disable it.
- `command` (list of strings, required): The command and its arguments to execute. The first element is the executable path (must be findable via PATH or absolute/relative path), subsequent elements are arguments passed directly. **Crucially, do not rely on shell expansion or interpretation.**
- `appliesTo` (list of strings, optional): Glob patterns matching file paths (relative to input root, using `/` separators). If omitted or empty, the plugin applies to all files processed in its stage.
- `config` (map, optional): A map of key-value pairs specific to your plugin. This map is passed directly to your plugin within the `PluginInput` JSON, allowing you to control its behavior without changing the command line.

## 4. The Communication Contract (`PluginInput` & `PluginOutput`)

Your plugin interacts with `stack-converter` by reading `PluginInput` JSON from `stdin` and writing `PluginOutput` JSON to `stdout`.

**Refer to [docs/plugin_schema.md](./plugin_schema.md) for the definitive JSON Schema.**

### 4.1. Reading Input (`PluginInput`)

Your plugin must read the entire standard input stream until EOF, then parse the content as a single JSON object conforming to the `PluginInput` schema.

**Key `PluginInput` Fields:**

- `$schemaVersion` (string): Must be `"1.0"`. Your plugin should check this for compatibility.
- `stage` (string): The stage your plugin is running in (`"preprocessor"`, `"postprocessor"`, `"formatter"`).
- `filePath` (string): Relative path of the source file.
- `content` (string): Current content (UTF-8 source code for pre/formatter, Markdown for postprocessor).
- `metadata` (object): Rich metadata about the file (paths, language, size, hash, Git info if enabled, comments if enabled, front matter if enabled). See [docs/template_guide.md](./template_guide.md) for details on available metadata fields. _Note: Metadata reflects changes made by previous plugins in the same stage._
- `config` (object): The `config` map from your plugin's configuration in `stack-converter.yaml`.

### 4.2. Writing Output (`PluginOutput`)

Upon successful completion of its logic, your plugin **must** write a single JSON object conforming to the `PluginOutput` schema to `stdout` and then **exit with code 0**.

**Key `PluginOutput` Fields:**

- `$schemaVersion` (string): **Must be `"1.0"`.**
- `error` (string): **Required.** Must be an empty string (`""`) for success. If your plugin encountered a functional problem (e.g., invalid input format specific to the plugin's logic), set this field to a descriptive error message. **Do NOT use this for execution crashes - use non-zero exit code for those.** `stack-converter` will treat a non-empty `error` field as a file processing failure.
- `content` (string | null): Optional. If non-null, this string replaces the current content for subsequent processing stages. Preprocessors and postprocessors use this. Formatters typically leave this null.
- `metadata` (object | null): Optional. If non-null, this map contains metadata keys/values to add or update. `stack-converter` performs a _merge_ operation: keys provided here overwrite existing keys in the file's metadata. New keys are added.
- `output` (string | null): Optional. **Used only by `formatter` stage plugins.** If non-null, this string is treated as the _final_ content for the output file, completely bypassing standard templating and postprocessing stages.

**Important:**

- If `error` is non-empty, `stack-converter` ignores `content`, `metadata`, and `output`.
- If `error` is empty, your plugin _can_ return null/omit `content`, `metadata`, and `output` if it made no changes or only performed validation.
- A `formatter` providing a non-null `output` value should generally leave `content` as null.

## 5. Logging and Error Handling within Plugins

- **Logging:** Use **standard error (`stderr`)** for all diagnostic logging (debug messages, warnings, progress indicators) within your plugin. `stack-converter` captures `stderr` and includes it in its own logs (especially with `-v`) or error reports. **Do NOT write JSON or output intended for `stack-converter` to `stderr`.**
- **Functional Errors:** If your plugin can perform its core task but encounters a problem related to the input data or its internal logic (e.g., failed to parse a custom syntax it expects), report this by setting the `"error"` field in the `PluginOutput` JSON and **exit with code 0**.
- **Execution Errors:** If your plugin crashes, cannot start, encounters an unrecoverable error (e.g., cannot read a required external resource), or fails due to an internal bug, it **must exit with a non-zero exit code**. `stack-converter` will detect this and treat it as a file processing failure. Writing details to `stderr` before exiting is highly recommended.

## 6. Best Practices

- **Check Schema Version:** Always check the `$schemaVersion` in the `PluginInput` for compatibility.
- **Handle Errors Gracefully:** Use the JSON `error` field for functional errors and non-zero exit codes for crashes. Provide informative error messages on `stderr`.
- **Be Idempotent (If Possible):** Design your plugin so that running it multiple times on the same input produces the same output. This helps with reruns and debugging.
- **Performance:** Keep plugins reasonably fast. Complex or long-running operations might slow down the overall conversion process. Consider optimizing your plugin's logic. `stack-converter` applies a timeout via the context.
- **Security:** Be mindful of security if your plugin interacts with the filesystem or external services. Sanitize any inputs derived from `PluginInput` if used in commands or file paths. Remember the plugin runs with the user's permissions.
- **Cross-Platform:** If writing scripts (Shell, Python, etc.), consider cross-platform compatibility or provide clear platform requirements.
- **Dependencies:** Keep external dependencies minimal or provide clear instructions for installing them. Consider bundling simple scripts or using standard system tools where possible.
- **Statelessness:** Aim for stateless plugins where possible. Rely on the `PluginInput` for all necessary context. If state is needed between files (e.g., aggregating data), this plugin model is less suitable; consider alternative approaches or requesting features in `stack-converter` itself.

## 7. Examples

See the example plugins provided in the `stack-converter` repository under the `scripts/plugins/test/` directory. These examples demonstrate:

- Reading `PluginInput` JSON from `stdin`.
- Parsing JSON (e.g., using Python's `json` module or shell tools like `jq`).
- Performing simple content modification.
- Adding/modifying metadata.
- Writing `PluginOutput` JSON to `stdout`.
- Reporting functional errors via the `error` field.
- Exiting with non-zero codes on failure.
- Logging diagnostic messages to `stderr`.

**Direct Links to Examples:**

- [**`scripts/plugins/test/success_modify.py`**](../../scripts/plugins/test/success_modify.py) - (Python example modifying content)
- [**`scripts/plugins/test/add_metadata.py`**](../../scripts/plugins/test/add_metadata.py) - (Python example adding metadata)
- [**`scripts/plugins/test/fail_exit.sh`**](../../scripts/plugins/test/fail_exit.sh) - (Shell example failing with non-zero exit)
- [**`scripts/plugins/test/report_error.py`**](../../scripts/plugins/test/report_error.py) - (Python example reporting error via JSON)
- [**`scripts/plugins/test/stderr.py`**](../../scripts/plugins/test/stderr.py) - (Python example writing to stderr)

_(These links assume the guide resides in `docs/` relative to the project root)_

These examples serve as crucial references and are used in the project's integration tests (`SC-INT-PLUGIN-*`) to verify the `PluginRunner` implementation.

## 8. Debugging Tips

- **Run Manually:** Execute your plugin directly from the command line. Create a sample `input.json` file mimicking `PluginInput` and pipe it to your plugin:
  ```bash
  cat input.json | python your_plugin.py --args > output.json 2> error.log
  ```
  Check `output.json` for valid `PluginOutput` JSON and `error.log` for `stderr` messages. Check the exit code (`echo $?` on Linux/macOS, `$LASTEXITCODE` in PowerShell).
- **Log Extensively:** Add generous logging to `stderr` within your plugin, printing received input, intermediate steps, and the final output JSON before writing it to `stdout`.
- **Use `stack-converter -v`:** Run `stack-converter` with the verbose flag (`-v`). This will print the `stderr` captured from your plugin, especially if it fails, along with runner-level diagnostics.
- **Validate JSON:** Use JSON linters or online validators to ensure the JSON your plugin outputs to `stdout` is valid and matches the `PluginOutput` schema from `docs/plugin_schema.md`.
- **Check Permissions:** Ensure your plugin script/executable has execute permissions (`chmod +x`).
- **Check Paths:** Ensure the `command` path in `stack-converter.yaml` is correct and the executable/script is findable in the system `PATH` or via a relative/absolute path from where `stack-converter` is run.
- **Check Runtimes:** Ensure the required runtime (Python, Node, etc.) is installed and accessible in the environment where `stack-converter` is running.
- **Check Exit Codes:** Ensure your plugin explicitly exits with code 0 on success (even when reporting functional errors via JSON). Non-zero indicates a crash.

## 9. Security Considerations

- **For Plugin Authors:**
  - **Input Sanitization:** Treat data within `PluginInput` (especially `filePath`, `content`, and `config` values) as potentially untrusted if your plugin uses it to construct file paths, shell commands, or database queries. Sanitize or validate inputs appropriately.
  - **Resource Usage:** Avoid excessive CPU, memory, or disk usage that could impact the host system.
  - **External Calls:** Be cautious when making network calls or accessing external resources; handle errors gracefully.
- **For Users Enabling Plugins:**
  - **Trust:** Only run plugins from trusted sources. Review plugin code if possible.
  - **Permissions:** Remember plugins run with the same permissions as the `stack-converter` process itself. Avoid running `stack-converter` with elevated privileges if running untrusted plugins.
  - **Configuration:** Carefully review the `command` specified in `stack-converter.yaml` for any enabled plugins. Ensure it doesn't contain unexpected shell commands.

## 10. Getting Help

If you encounter issues developing plugins or have suggestions for improving the plugin system, please:

1.  Consult the [`docs/plugin_schema.md`](./plugin_schema.md) document for the definitive protocol specification.
2.  Review the example plugins in `scripts/plugins/test/`.
3.  Search existing [GitHub Issues](https://github.com/stackvity/stack-converter/issues) and [Discussions](https://github.com/stackvity/stack-converter/discussions).
4.  If your issue isn't covered, please [open a new issue](https://github.com/stackvity/stack-converter/issues/new/choose) with details about your plugin, configuration, `stack-converter` version, and the specific problem you're encountering.

# --- END OF REVISED FILE docs/plugin_dev_guide.md ---

// --- START OF FINAL REVISED FILE cmd/stack-converter/root.go ---
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time" // Keep time import, might be needed by cli.Run or future root logic

	"github.com/spf13/cobra"
	// "github.com/spf13/pflag" // No longer needed directly in root.go after test refactor review
	"github.com/stackvity/stack-converter/internal/cli"        // Assumed internal package for run logic
	"github.com/stackvity/stack-converter/internal/cli/config" // Assumed internal package for config logic
	"github.com/stackvity/stack-converter/pkg/converter"
	"golang.org/x/term" // For reliable TTY detection
)

var (
	// These are set during build time using -ldflags
	version = "dev"     // Default version
	commit  = "none"    // Default commit hash
	date    = "unknown" // Default build date

	// Flags persistent across commands
	cfgFile     string // Path to config file
	profileName string // Name of profile to use
	verbose     bool   // Verbose logging flag
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "stack-converter -i <inputDir> -o <outputDir>",
	Short: "Generates Markdown documentation from source code directories.",
	Long: `stack-converter automatically scans source code repositories,
processes files based on configuration, and generates structured Markdown documentation.

It features:
  - Parallel processing for speed.
  - Content-based caching for fast incremental runs.
  - Git integration for differential processing.
  - Customizable output via Go templates.
  - Extensibility via plugins.
  - An interactive Terminal UI (TUI) for monitoring progress.`,
	Version: fmt.Sprintf("%s (commit: %s, built: %s)", version, commit, date),
	Args:    cobra.NoArgs, // Expect flags for input/output, not positional args
	RunE: func(cmd *cobra.Command, args []string) error { // minimal comment
		// Create a context that listens for interrupt signals
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel() // Ensure cancellation propagates even if Run exits early

		// Load configuration (delegated)
		// FIX: Pass the global 'version' variable as the appVersion argument
		opts, logger, err := config.LoadAndValidate(cfgFile, profileName, version, verbose, cmd.Flags())
		if err != nil {
			// config.LoadAndValidate should log the specific error to stderr already
			// Return the error to signal failure to cobra, which will print it and exit non-zero.
			return err
		}

		// Add a small delay to allow TUI to initialize properly if enabled
		// Checks Stderr as TUI/Progress Bar typically write there.
		// TODO: Consider making this 100ms delay configurable via Options or a hidden flag if it causes issues in fast/slow environments.
		if term.IsTerminal(int(os.Stderr.Fd())) && !verbose && opts.TuiEnabled {
			time.Sleep(100 * time.Millisecond)
		}

		// Execute the main application logic (delegated)
		// Pass the cancellable context here.
		return cli.Run(ctx, opts, logger)
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
func Execute() { // minimal comment
	rootCmd.SetVersionTemplate(`{{.Use}} version {{.Version}}` + "\n")
	// Error execution is handled implicitly by Cobra's Execute() return value.
	// Cobra prints the error and exits non-zero if RunE returns an error.
	_ = rootCmd.Execute()
}

// init registers persistent flags for the root command.
func init() { // minimal comment
	// Persistent flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "Configuration file path (default is search standard locations like ., $HOME/.config/stack-converter/)")
	rootCmd.PersistentFlags().StringVar(&profileName, "profile", "", "Name of configuration profile to use")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "Enable verbose (debug) logging output (disables TUI)")

	// Required Input/Output flags
	rootCmd.PersistentFlags().StringP("input", "i", "", "Required. Source code directory path.")
	rootCmd.PersistentFlags().StringP("output", "o", "", "Required. Output directory path for Markdown files.")
	_ = rootCmd.MarkPersistentFlagRequired("input")
	_ = rootCmd.MarkPersistentFlagRequired("output")

	// --- Local Flags for the root command ---
	// Note: Flag names should align with Viper keys used in internal/cli/config/config.go
	// or use explicit viper.BindPFlag calls in the LoadAndValidate function.

	// Core behavior flags
	rootCmd.Flags().BoolP("force", "f", false, "Overwrite output directory without prompting if it exists and is non-empty")
	rootCmd.Flags().Bool("no-tui", false, "Disable interactive Terminal UI even if in a TTY")
	rootCmd.Flags().StringArray("ignore", []string{}, "Glob patterns for files/directories to ignore (can be specified multiple times)")
	rootCmd.Flags().String("onError", string(converter.DefaultOnErrorMode), `Behavior on non-fatal file errors ("continue" or "stop")`)

	// Performance & Caching flags
	rootCmd.Flags().Int("concurrency", converter.DefaultConcurrency, "Number of parallel workers (0 for auto-detect CPU cores)")
	rootCmd.Flags().Bool("no-cache", false, "Force reprocessing by ignoring cache reads (still writes cache)")
	rootCmd.Flags().Bool("clear-cache", false, "Delete the cache file before starting")

	// Git Integration flags
	rootCmd.Flags().Bool("git-metadata", converter.DefaultGitMetadataEnabled, "Include Git metadata (commit, author, date) in templates")
	rootCmd.Flags().Bool("git-diff-only", converter.DefaultGitDiffOnly, "Process only files changed in the Git index/working tree vs HEAD")
	rootCmd.Flags().String("git-since", "", "Process only files changed since the specified Git reference (commit/tag/branch)") // Default comes from config

	// Output & Formatting flags
	rootCmd.Flags().String("output-format", string(converter.DefaultOutputFormat), `Final report format ("text", "json")`)
	rootCmd.Flags().String("template", "", "Path to a custom Go template file for Markdown generation")

	// File Handling flags
	rootCmd.Flags().Int64("large-file-threshold", converter.DefaultLargeFileThresholdMB, "File size threshold in Megabytes (MB) to trigger large file handling")
	rootCmd.Flags().String("large-file-mode", string(converter.DefaultLargeFileMode), `Mode for large files ("skip", "truncate", "error")`)
	rootCmd.Flags().String("large-file-truncate-cfg", converter.DefaultLargeFileTruncateCfg, `Truncation config for large files (e.g., "1MB", "1000 lines")`)
	rootCmd.Flags().String("binary-mode", string(converter.DefaultBinaryMode), `Mode for binary files ("skip", "placeholder", "error")`)

	// Workflow flags
	rootCmd.Flags().Bool("watch", false, "Enable watch mode to automatically re-run on file changes")
	// FIX: Add watch-debounce flag definition to match its usage in config binding
	rootCmd.Flags().String("watch-debounce", converter.DefaultWatchDebounceString, "Watch debounce duration string (e.g., '300ms', '1s')")
	rootCmd.Flags().Bool("extract-comments", converter.DefaultAnalysisExtractComments, "Enable experimental extraction of documentation comments")
}

// --- END OF FINAL REVISED FILE cmd/stack-converter/root.go ---

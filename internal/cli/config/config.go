// --- START OF FINAL REVISED FILE internal/cli/config/config.go ---
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect" // Required for DecodeHook
	"runtime"
	"slices" // Requires Go 1.21+
	"strings"
	"text/template" // Import standard template package
	"time"

	// Needed indirectly via viper
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/stackvity/stack-converter/pkg/converter"
	"github.com/stackvity/stack-converter/pkg/converter/git"

	// Use alias for the library's template helper functions to avoid conflict
	tmplhelper "github.com/stackvity/stack-converter/pkg/converter/template"
)

const (
	EnvPrefix         = "STACKCONVERTER"
	DefaultConfigName = "stack-converter"
	// Added default template name for internal use
	defaultTemplateName = "default"
)

// LoadAndValidate loads configuration from all sources (defaults, file, profile, env, flags),
// validates the merged configuration, derives necessary values (e.g., absolute paths),
// loads templates, sets up the logger, and returns the populated Options struct or an error.
func LoadAndValidate(cfgFile, profileName string, verbose bool, flags *pflag.FlagSet) (converter.Options, *slog.Logger, error) {
	var opts converter.Options
	v := viper.New()

	// Initialize a temporary basic logger for early loading errors
	tempLogHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	tempLogger := slog.New(tempLogHandler)

	setDefaults(v)

	// --- Load Config File ---
	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		if err != nil { // Check error from UserHomeDir
			tempLogger.Error("Failed to get user home directory", slog.Any("error", err))
			// Return error instead of relying on cobra.CheckErr here
			return opts, tempLogger, fmt.Errorf("failed to get user home directory: %w", err)
		}

		v.SetConfigName(DefaultConfigName)
		v.SetConfigType("yaml") // Explicitly set type
		v.AddConfigPath(".")
		// Use DefaultConfigName consistently for directory paths
		v.AddConfigPath(filepath.Join(home, ".config", DefaultConfigName)) // Linux standard
		v.AddConfigPath(filepath.Join(home, "."+DefaultConfigName))        // Alternative home dotfile
		// v.AddConfigPath(filepath.Join(home)) // Avoid searching entire home dir, too broad
	}

	if err := v.ReadInConfig(); err != nil {
		var configFileNotFoundError viper.ConfigFileNotFoundError
		if errors.As(err, &configFileNotFoundError) && cfgFile == "" {
			// Config file not found is OK if not explicitly specified
			tempLogger.Debug("No configuration file found, using defaults/env/flags.")
		} else {
			// Any other error (parse error, permissions) or if file was specified but not found
			configFileUsed := cfgFile
			if configFileUsed == "" {
				configFileUsed = fmt.Sprintf("searched locations for %s.yaml/json/toml", DefaultConfigName)
			}
			tempLogger.Error("Error reading configuration file", slog.String("path", configFileUsed), slog.Any("error", err))
			return opts, tempLogger, fmt.Errorf("error reading config file '%s': %w", configFileUsed, err) // Return error
		}
	} else {
		opts.ConfigFilePath = v.ConfigFileUsed()
		tempLogger.Debug("Using configuration file", slog.String("path", opts.ConfigFilePath))
	}

	// --- Apply Profile ---
	opts.ProfileName = profileName
	if profileName != "" {
		profileKey := "profiles." + profileName
		if !v.IsSet(profileKey) { // Check if the profile key exists
			configPath := v.ConfigFileUsed()
			if configPath == "" {
				configPath = "(no config file found)"
			}
			err := fmt.Errorf("profile '%s' not found in config file '%s'", profileName, configPath)
			tempLogger.Error(err.Error())
			return opts, tempLogger, err
		}
		profileSettings := v.Sub(profileKey) // Get profile settings
		if profileSettings == nil {          // Extra check if Sub returns nil unexpectedly
			err := fmt.Errorf("failed to load profile '%s' settings from config file '%s'", profileName, v.ConfigFileUsed())
			tempLogger.Error(err.Error())
			return opts, tempLogger, err
		}
		// Merge profile settings onto the current viper instance state
		if err := v.MergeConfigMap(profileSettings.AllSettings()); err != nil {
			tempLogger.Error("Error merging profile", slog.String("profile", profileName), slog.Any("error", err))
			return opts, tempLogger, fmt.Errorf("error merging profile '%s': %w", profileName, err)
		}
		tempLogger.Debug("Applied configuration profile", slog.String("profile", profileName))
	}

	// --- Bind Environment Variables ---
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv() // Reads matching environment variables

	// --- Bind Flags (Highest Priority) ---
	// Use BindPFlags to bind only the flags defined in the provided FlagSet.
	// Map flag names to config keys explicitly if they differ significantly.
	// Viper keys corresponding to flags (set via v.BindPFlags)
	flagKeys := []string{
		"input", "output", "config", "profile", "verbose", "force",
		"no-tui", "ignore", "onError", "concurrency", "no-cache",
		"clear-cache", "git-metadata", "git-diff-only", "git-since",
		"output-format", "template", "large-file-threshold",
		"large-file-mode", "large-file-truncate-cfg", "binary-mode",
		"watch", "extract-comments",
		// Add other flags defined in root.go init() here
		// Ensure these keys match the flag names used in rootCmd.Flags() / PersistentFlags()
	}

	for _, key := range flagKeys {
		flag := flags.Lookup(key)
		if flag != nil {
			// Use BindPFlag to bind each flag individually
			// This provides more control and clarity than BindPFlags.
			err := v.BindPFlag(key, flag)
			if err != nil {
				tempLogger.Error("Error binding flag", slog.String("flag", key), slog.Any("error", err))
				return opts, tempLogger, fmt.Errorf("error binding flag '--%s': %w", key, err)
			}
		} else {
			// Log if a flag expected to be bound is missing (potential dev error)
			tempLogger.Debug("Flag lookup failed during binding", slog.String("flag", key))
		}
	}

	// Rename viper keys if they differ from flag names (or struct field names if mapstructure tags are different)
	// Example: If flag is --template but struct field is TemplatePath and config key is templateFile
	v.RegisterAlias("templateFile", "template") // Alias --template flag to templateFile config key

	// Apply decode hook for specific types if needed (e.g., time.Duration)
	// Use mapstructure tags (`mapstructure:"..."`) on the Options struct fields
	// for automatic mapping during Unmarshal. Viper uses the "mapstructure" tag by default.
	decodeHook := viper.DecodeHook(stringToTimeDurationHookFunc())
	unmarshalOpts := []viper.DecoderConfigOption{
		decodeHook,
		// REMOVED: Rely on viper's default use of "mapstructure" tag.
	}

	// --- Unmarshal Final Configuration ---
	if err := v.Unmarshal(&opts, unmarshalOpts...); err != nil {
		tempLogger.Error("Error unmarshalling configuration", slog.Any("error", err))
		// Check if the error is related to the specific duration parsing hook for better feedback
		if strings.Contains(err.Error(), "invalid duration format") {
			return opts, tempLogger, fmt.Errorf("error processing configuration: %w", err)
		}
		return opts, tempLogger, fmt.Errorf("error unmarshalling configuration: %w", err)
	}

	// --- Explicitly Handle Flag Overrides for Booleans ---
	// Viper/Cobra binding can sometimes be tricky with boolean flags.
	// Ensure explicit flags always win.
	if flags.Changed("verbose") { // Check if the flag was explicitly set
		opts.Verbose, _ = flags.GetBool("verbose") // Ignore error, default is false
	}
	if flags.Changed("force") {
		opts.ForceOverwrite, _ = flags.GetBool("force")
	}
	if flags.Changed("no-tui") {
		noTui, _ := flags.GetBool("no-tui")
		if noTui {
			opts.TuiEnabled = false
		}
	}
	if flags.Changed("no-cache") {
		opts.IgnoreCacheRead, _ = flags.GetBool("no-cache")
	}
	if flags.Changed("clear-cache") {
		opts.ClearCache, _ = flags.GetBool("clear-cache")
	}
	if flags.Changed("watch") {
		opts.WatchMode, _ = flags.GetBool("watch")
	}
	if flags.Changed("git-metadata") {
		opts.GitMetadataEnabled, _ = flags.GetBool("git-metadata")
	}
	// Add other boolean flags if necessary

	// --- Setup Final Logger ---
	logLevel := slog.LevelInfo
	if opts.Verbose { // Use the potentially overridden opts.Verbose value
		logLevel = slog.LevelDebug
	}
	// Log to Stderr by default for CLI tools
	logHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel})
	logger := slog.New(logHandler) // Final logger instance
	opts.Logger = logHandler       // Inject the handler into Options for the library

	// --- Load Custom or Default Template ---
	// Use the TemplatePath field which should now be populated correctly via viper/flags/alias
	if opts.TemplatePath != "" {
		// Check if template path exists and is a file before parsing
		absTplPath, pathErr := filepath.Abs(opts.TemplatePath)
		if pathErr != nil {
			errMsg := fmt.Sprintf("Cannot resolve absolute path for template file '%s'", opts.TemplatePath)
			logger.Error(errMsg, slog.String("error", pathErr.Error()))
			return opts, logger, fmt.Errorf("%s: %w", errMsg, pathErr)
		}
		opts.TemplatePath = absTplPath // Store absolute path

		tplInfo, statErr := os.Stat(opts.TemplatePath)
		if statErr != nil {
			errMsg := fmt.Sprintf("Template file specified via 'templateFile' or --template ('%s') does not exist or cannot be accessed", opts.TemplatePath)
			logger.Error(errMsg, slog.String("error", statErr.Error()))
			// Wrap specific error for better context
			err := fmt.Errorf("%w: %w", converter.ErrConfigValidation, fmt.Errorf("%s: %w", errMsg, statErr))
			return opts, logger, err
		}
		if tplInfo.IsDir() {
			errMsg := fmt.Sprintf("Template path specified ('%s') is a directory, not a file", opts.TemplatePath)
			logger.Error(errMsg)
			err := fmt.Errorf("%w: %s", converter.ErrConfigValidation, errMsg)
			return opts, logger, err
		}

		// *** FIX: Load template using standard library functions ***
		templateName := filepath.Base(opts.TemplatePath)
		// Read the file content
		contentBytes, readErr := os.ReadFile(opts.TemplatePath)
		if readErr != nil {
			errMsg := fmt.Sprintf("Failed to read custom template file '%s'", opts.TemplatePath)
			logger.Error(errMsg, slog.String("error", readErr.Error()))
			err := fmt.Errorf("%w: %s: %w", converter.ErrConfigValidation, errMsg, readErr)
			return opts, logger, err
		}
		templateContent := string(contentBytes)

		// Parse the template using text/template
		// TODO: Add custom template functions here if needed using .Funcs()
		customTmpl, parseErr := template.New(templateName).Parse(templateContent)
		if parseErr != nil {
			logger.Error("Failed to parse custom template file", slog.String("path", opts.TemplatePath), slog.String("error", parseErr.Error()))
			// Wrap specific error
			err := fmt.Errorf("%w: failed to parse template '%s': %w", converter.ErrConfigValidation, opts.TemplatePath, parseErr)
			return opts, logger, err
		}
		opts.Template = customTmpl // Assign stdlib *template.Template

		logger.Debug("Loaded custom template", slog.String("path", opts.TemplatePath))
	} else {
		// Load embedded default template using the aliased helper function
		defaultTmpl, err := tmplhelper.LoadDefaultTemplate()
		if err != nil {
			// This is a critical internal error if the embedded template fails
			logger.Error("Critical: Failed to load embedded default template", slog.String("error", err.Error()))
			return opts, logger, fmt.Errorf("critical internal error: failed to load default template: %w", err)
		}
		opts.Template = defaultTmpl
		logger.Debug("Using embedded default template")
	}

	// --- Final Validation and Derivations ---
	if err := validateAndDeriveOptions(&opts, logger, flags); err != nil {
		// Specific error logged within validation function
		// Ensure the returned error includes context that it's a validation issue
		// The validateAndDeriveOptions should already return wrapped converter.ErrConfigValidation
		return opts, logger, err
	}

	logger.Debug("Configuration loading and validation complete",
		slog.String("configFile", opts.ConfigFilePath),
		slog.String("profile", opts.ProfileName),
		slog.Bool("verbose", opts.Verbose),
		slog.String("logLevel", logLevel.String()),
	)

	return opts, logger, nil
}

// setDefaults establishes the default values for configuration options in Viper.
func setDefaults(v *viper.Viper) {
	// --- Behavior & Control ---
	v.SetDefault("forceOverwrite", converter.DefaultForceOverwrite)
	v.SetDefault("verbose", converter.DefaultVerbose)
	v.SetDefault("tuiEnabled", converter.DefaultTuiEnabled)
	v.SetDefault("onError", string(converter.DefaultOnErrorMode)) // string representation

	// --- Performance & Caching ---
	v.SetDefault("concurrency", converter.DefaultConcurrency)
	v.SetDefault("cache", converter.DefaultCacheEnabled)

	// --- File Handling ---
	v.SetDefault("ignore", []string{}) // Default empty slice
	v.SetDefault("binaryMode", string(converter.DefaultBinaryMode))
	v.SetDefault("largeFileThresholdMB", converter.DefaultLargeFileThresholdMB)
	v.SetDefault("largeFileMode", string(converter.DefaultLargeFileMode))
	v.SetDefault("largeFileTruncateCfg", converter.DefaultLargeFileTruncateCfg)
	v.SetDefault("defaultEncoding", "") // Default empty

	// --- Language Detection & Templating ---
	v.SetDefault("languageMappings", map[string]string{}) // Default empty map
	v.SetDefault("languageDetectionConfidenceThreshold", converter.DefaultLanguageDetectionConfidenceThreshold)
	v.SetDefault("templateFile", "") // Default: use embedded template

	// --- Output & Formatting ---
	v.SetDefault("outputFormat", string(converter.DefaultOutputFormat))
	v.SetDefault("frontMatter.enabled", converter.DefaultFrontMatterEnabled) // Nested keys
	v.SetDefault("frontMatter.format", converter.DefaultFrontMatterFormat)
	v.SetDefault("frontMatter.static", map[string]interface{}{}) // Ensure nested maps exist
	v.SetDefault("frontMatter.include", []string{})

	// --- Workflow Features ---
	v.SetDefault("watch.debounce", converter.DefaultWatchDebounceString) // Nested key for WatchConfig
	v.SetDefault("git.diffOnly", converter.DefaultGitDiffOnly)           // Nested key for GitConfig
	v.SetDefault("git.sinceRef", converter.DefaultGitSinceRef)           // Nested key for GitConfig
	v.SetDefault("gitMetadata", converter.DefaultGitMetadataEnabled)
	v.SetDefault("analysis.extractComments", converter.DefaultAnalysisExtractComments) // Nested key
	v.SetDefault("analysis.commentStyles", []string{"godoc", "pydoc", "javadoc"})      // Default popular styles

	// --- Extensibility ---
	v.SetDefault("plugins", map[string]interface{}{}) // Ensure plugins key exists as map for viper merge

	// --- Internal/Derived (Defaults not set via viper, but conceptual defaults) ---
	// IgnoreCacheRead: false
	// ClearCache: false
	// WatchMode: false
	// GitDiffMode: GitDiffModeNone
}

// isValidEnumValue checks if a given string value is present in a slice of allowed enum values.
// Case-sensitive comparison.
func isValidEnumValue[T ~string](value T, allowedValues []T) bool {
	return slices.Contains(allowedValues, value)
}

// validateAndDeriveOptions performs semantic validation on the populated Options struct
// and calculates derived fields (e.g., absolute paths, byte thresholds).
// It wraps errors with converter.ErrConfigValidation.
func validateAndDeriveOptions(opts *converter.Options, logger *slog.Logger, flags *pflag.FlagSet) error {
	// === Path Validations ===
	if opts.InputPath == "" {
		err := fmt.Errorf("%w: input path is required (-i, --input)", converter.ErrConfigValidation)
		logger.Error(err.Error(), slog.String("key", "InputPath"))
		return err
	}
	absInput, err := filepath.Abs(opts.InputPath)
	if err != nil {
		err = fmt.Errorf("%w: cannot resolve absolute input path '%s': %w", converter.ErrConfigValidation, opts.InputPath, err)
		logger.Error(err.Error(), slog.String("key", "InputPath"), slog.String("value", opts.InputPath))
		return err
	}
	opts.InputPath = absInput
	info, err := os.Stat(opts.InputPath)
	if err != nil {
		if os.IsNotExist(err) {
			err = fmt.Errorf("%w: input path '%s' does not exist", converter.ErrConfigValidation, opts.InputPath)
			logger.Error(err.Error(), slog.String("key", "InputPath"), slog.String("value", opts.InputPath))
			return err
		}
		err = fmt.Errorf("%w: cannot access input path '%s': %w", converter.ErrConfigValidation, opts.InputPath, err)
		logger.Error(err.Error(), slog.String("key", "InputPath"), slog.String("value", opts.InputPath))
		return err
	}
	if !info.IsDir() {
		err = fmt.Errorf("%w: input path '%s' is not a directory", converter.ErrConfigValidation, opts.InputPath)
		logger.Error(err.Error(), slog.String("key", "InputPath"), slog.String("value", opts.InputPath))
		return err
	}
	logger.Debug("Validated input path", slog.String("path", opts.InputPath))

	if opts.OutputPath == "" {
		err := fmt.Errorf("%w: output path is required (-o, --output)", converter.ErrConfigValidation)
		logger.Error(err.Error(), slog.String("key", "OutputPath"))
		return err
	}
	absOutput, err := filepath.Abs(opts.OutputPath)
	if err != nil {
		err = fmt.Errorf("%w: cannot resolve absolute output path '%s': %w", converter.ErrConfigValidation, opts.OutputPath, err)
		logger.Error(err.Error(), slog.String("key", "OutputPath"), slog.String("value", opts.OutputPath))
		return err
	}
	opts.OutputPath = absOutput
	// Attempt to create output directory to check writability early
	if mkdirErr := os.MkdirAll(opts.OutputPath, 0755); mkdirErr != nil {
		err := fmt.Errorf("%w: cannot create/access output directory '%s': %w", converter.ErrConfigValidation, opts.OutputPath, mkdirErr)
		logger.Error(err.Error(), slog.String("key", "OutputPath"), slog.String("value", opts.OutputPath))
		return err
	}
	logger.Debug("Resolved and verified output path", slog.String("path", opts.OutputPath))

	// --- Enum String Validations ---
	allowedOnError := []converter.OnErrorMode{converter.OnErrorContinue, converter.OnErrorStop}
	if !isValidEnumValue(opts.OnErrorMode, allowedOnError) {
		err := fmt.Errorf("%w: invalid value '%s' for key 'onError' (flag --onError). Allowed: %v", converter.ErrConfigValidation, opts.OnErrorMode, allowedOnError)
		logger.Error(err.Error(), slog.String("key", "onError"), slog.String("value", string(opts.OnErrorMode)))
		return err
	}
	allowedBinaryMode := []converter.BinaryMode{converter.BinarySkip, converter.BinaryPlaceholder, converter.BinaryError}
	if !isValidEnumValue(opts.BinaryMode, allowedBinaryMode) {
		err := fmt.Errorf("%w: invalid value '%s' for key 'binaryMode' (flag --binary-mode). Allowed: %v", converter.ErrConfigValidation, opts.BinaryMode, allowedBinaryMode)
		logger.Error(err.Error(), slog.String("key", "binaryMode"), slog.String("value", string(opts.BinaryMode)))
		return err
	}
	allowedLargeFileMode := []converter.LargeFileMode{converter.LargeFileSkip, converter.LargeFileTruncate, converter.LargeFileError}
	if !isValidEnumValue(opts.LargeFileMode, allowedLargeFileMode) {
		err := fmt.Errorf("%w: invalid value '%s' for key 'largeFileMode' (flag --large-file-mode). Allowed: %v", converter.ErrConfigValidation, opts.LargeFileMode, allowedLargeFileMode)
		logger.Error(err.Error(), slog.String("key", "largeFileMode"), slog.String("value", string(opts.LargeFileMode)))
		return err
	}
	allowedOutputFormat := []converter.OutputFormat{converter.OutputFormatText, converter.OutputFormatJSON}
	if !isValidEnumValue(opts.OutputFormat, allowedOutputFormat) {
		err := fmt.Errorf("%w: invalid value '%s' for key 'outputFormat' (flag --output-format). Allowed: %v", converter.ErrConfigValidation, opts.OutputFormat, allowedOutputFormat)
		logger.Error(err.Error(), slog.String("key", "outputFormat"), slog.String("value", string(opts.OutputFormat)))
		return err
	}
	if opts.FrontMatterConfig.Enabled {
		allowedFrontMatterFormat := []string{"yaml", "toml"} // Use string slice for direct comparison
		if !isValidEnumValue(opts.FrontMatterConfig.Format, allowedFrontMatterFormat) {
			err := fmt.Errorf("%w: invalid value '%s' for key 'frontMatter.format'. Allowed: %v", converter.ErrConfigValidation, opts.FrontMatterConfig.Format, allowedFrontMatterFormat)
			logger.Error(err.Error(), slog.String("key", "frontMatter.format"), slog.String("value", opts.FrontMatterConfig.Format))
			return err
		}
	}

	// --- Numeric Range Validations ---
	if opts.Concurrency < 0 {
		err := fmt.Errorf("%w: invalid value '%d' for key 'concurrency' (flag --concurrency). Must be >= 0", converter.ErrConfigValidation, opts.Concurrency)
		logger.Error(err.Error(), slog.String("key", "concurrency"), slog.Int("value", opts.Concurrency))
		return err
	}
	if opts.LargeFileThresholdMB < 0 {
		err := fmt.Errorf("%w: invalid value '%d' for key 'largeFileThresholdMB' (flag --large-file-threshold). Must be >= 0", converter.ErrConfigValidation, opts.LargeFileThresholdMB)
		logger.Error(err.Error(), slog.String("key", "largeFileThresholdMB"), slog.Int64("value", opts.LargeFileThresholdMB))
		return err
	}
	if opts.LanguageDetectionConfidenceThreshold < 0.0 || opts.LanguageDetectionConfidenceThreshold > 1.0 {
		err := fmt.Errorf("%w: invalid value '%f' for key 'languageDetectionConfidenceThreshold'. Must be between 0.0 and 1.0", converter.ErrConfigValidation, opts.LanguageDetectionConfidenceThreshold)
		logger.Error(err.Error(), slog.String("key", "languageDetectionConfidenceThreshold"), slog.Float64("value", opts.LanguageDetectionConfidenceThreshold))
		return err
	}

	// --- Derive options ---
	if opts.Concurrency == 0 {
		opts.Concurrency = runtime.NumCPU()
		logger.Debug("Concurrency not set, defaulting to number of CPUs", slog.Int("concurrency", opts.Concurrency))
	}

	opts.LargeFileThreshold = opts.LargeFileThresholdMB * 1024 * 1024

	if opts.CacheEnabled {
		opts.CacheFilePath = filepath.Join(opts.OutputPath, converter.CacheFileName)
		logger.Debug("Cache enabled", slog.String("path", opts.CacheFilePath))
	} else {
		opts.CacheFilePath = ""
		logger.Debug("Cache disabled")
	}

	// Parse WatchDebounce string after unmarshalling
	debounceDuration, err := time.ParseDuration(opts.WatchConfig.Debounce)
	if err != nil {
		// Check if a flag explicitly set an invalid debounce string
		if flags.Changed("watch-debounce") {
			err = fmt.Errorf("%w: invalid watch debounce duration '%s' specified via flag or config: %w", converter.ErrConfigValidation, opts.WatchConfig.Debounce, err)
			logger.Error(err.Error(), slog.String("key", "watch.debounce"), slog.String("value", opts.WatchConfig.Debounce))
			return err
		}
		// Otherwise, log warning and use default
		defaultDuration := converter.DefaultWatchDebounceDuration
		logger.Warn("Could not parse watch.debounce string, using default",
			slog.String("value", opts.WatchConfig.Debounce),
			slog.Duration("default", defaultDuration),
			slog.String("error", err.Error()))
		debounceDuration = defaultDuration
	}
	if debounceDuration < 0 {
		err = fmt.Errorf("%w: invalid negative watch debounce duration '%s' for key 'watch.debounce'", converter.ErrConfigValidation, opts.WatchConfig.Debounce)
		logger.Error(err.Error(), slog.String("key", "watch.debounce"), slog.String("value", opts.WatchConfig.Debounce))
		return err
	}
	opts.WatchDebounce = debounceDuration // Store the actual duration
	logger.Debug("Watch debounce set", slog.Duration("duration", opts.WatchDebounce))

	// Derive GitDiffMode from flags (using pflag Changed method for certainty)
	opts.GitDiffMode = converter.GitDiffModeNone // Default
	if gitDiffOnly, _ := flags.GetBool("git-diff-only"); gitDiffOnly {
		if flags.Changed("git-since") {
			err = fmt.Errorf("%w: cannot use --git-diff-only and --git-since flags simultaneously", converter.ErrConfigValidation)
			logger.Error(err.Error())
			return err
		}
		opts.GitDiffMode = converter.GitDiffModeDiffOnly
	} else if flags.Changed("git-since") {
		// opts.GitConfig.SinceRef should have been populated correctly by viper/flag binding
		if opts.GitConfig.SinceRef == "" {
			// If the flag was used but the value ended up empty (e.g., --git-since=), it's invalid.
			err = fmt.Errorf("%w: flag --git-since requires a non-empty reference (commit/tag/branch)", converter.ErrConfigValidation)
			logger.Error(err.Error())
			return err
		}
		opts.GitDiffMode = converter.GitDiffModeSince
	}
	logger.Debug("Git diff mode derived", slog.String("mode", string(opts.GitDiffMode)), slog.String("sinceRef", opts.GitConfig.SinceRef))

	// --- Derive GitChangedFiles (moved from processor, now done once upfront) ---
	// This requires the GitClient to be instantiated and potentially injected,
	// or initialized here if a default is acceptable.
	// If GitClient is nil and diff mode is active, we MUST return an error.
	if opts.GitDiffMode != converter.GitDiffModeNone {
		if opts.GitClient == nil {
			// Try initializing default GitClient if none provided
			logger.Debug("Git diff requested but no GitClient provided, initializing default (ExecGitClient)...")
			defaultGitClient := git.NewExecGitClient(logger.Handler()) // Example default
			// Check if git command is actually available
			if !defaultGitClient.IsGitAvailable() {
				err = fmt.Errorf("%w: Git diff requested (--%s) but git command not found in PATH and no GitClient provided", converter.ErrConfigValidation, opts.GitDiffMode)
				logger.Error(err.Error())
				return err
			}
			opts.GitClient = defaultGitClient
		}
		// Now opts.GitClient should be non-nil
		logger.Debug("Fetching changed files from Git...", slog.String("mode", string(opts.GitDiffMode)), slog.String("ref", opts.GitConfig.SinceRef))
		changedFilesList, gitErr := opts.GitClient.GetChangedFiles(opts.InputPath, string(opts.GitDiffMode), opts.GitConfig.SinceRef)
		if gitErr != nil {
			// Treat Git operational error as fatal during config validation if diffing was requested
			err = fmt.Errorf("%w: failed to get changed files for diff mode '%s': %w", converter.ErrGitOperation, opts.GitDiffMode, gitErr)
			logger.Error("Git operation failed", slog.String("error", err.Error()))
			return err
		}
		opts.GitChangedFiles = make(map[string]struct{}, len(changedFilesList))
		for _, f := range changedFilesList {
			// Normalize path separators consistently for map lookups
			normalizedPath := filepath.ToSlash(filepath.Clean(f))
			opts.GitChangedFiles[normalizedPath] = struct{}{}
		}
		logger.Debug("Fetched Git changed files", slog.Int("count", len(opts.GitChangedFiles)))

		// Add an explicit check for '--git-since' requiring *some* changes.
		// Running a diff that results in zero changes likely indicates user error (e.g., same ref, typo).
		// This check is primarily for '--git-since'. '--git-diff-only' finding zero changes is common.
		if opts.GitDiffMode == converter.GitDiffModeSince && len(opts.GitChangedFiles) == 0 {
			logger.Warn("No file changes detected between HEAD and the specified --git-since reference.", slog.String("ref", opts.GitConfig.SinceRef))
			// Continue processing, but warn the user.
		}
	}

	// Handle TUI enable/disable logic considering verbose flag
	if opts.Verbose {
		if opts.TuiEnabled && !flags.Changed("no-tui") { // Only log if TUI was previously enabled and not explicitly disabled by flag
			logger.Debug("Verbose mode enabled, TUI explicitly disabled")
		}
		opts.TuiEnabled = false // Verbose flag always overrides TUI setting
	} else if flags.Changed("no-tui") {
		if noTui, _ := flags.GetBool("no-tui"); noTui && opts.TuiEnabled {
			logger.Debug("TUI explicitly disabled via --no-tui flag")
			opts.TuiEnabled = false
		}
	}

	// Ensure EventHooks is set (should be done by caller, but default defensively)
	if opts.EventHooks == nil {
		opts.EventHooks = &converter.NoOpHooks{}
		logger.Debug("No event hooks provided by caller, defaulting to no-op hooks.")
	}

	// Log final important derived/validated settings
	logger.Debug("Final derived settings validated",
		slog.Int("concurrency", opts.Concurrency),
		slog.Int64("largeFileThresholdBytes", opts.LargeFileThreshold),
		slog.String("cacheFilePath", opts.CacheFilePath),
		slog.Duration("watchDebounceDuration", opts.WatchDebounce),
		slog.String("gitDiffMode", string(opts.GitDiffMode)),
		slog.Int("gitChangedFileCount", len(opts.GitChangedFiles)),
		slog.Bool("tuiEnabledEffective", opts.TuiEnabled),
	)

	return nil
}

// stringToTimeDurationHookFunc provides a Viper decode hook to parse strings into time.Duration.
// It only applies the conversion if the source is a string and the target is time.Duration.
func stringToTimeDurationHookFunc() viper.DecoderConfigOption {
	return viper.DecodeHook(func(f reflect.Type, t reflect.Type, data interface{}) (interface{}, error) {
		if f.Kind() != reflect.String || t != reflect.TypeOf(time.Duration(0)) {
			return data, nil // No conversion needed
		}
		durationStr, ok := data.(string)
		if !ok {
			// This should not happen due to the f.Kind() check, but handle defensively
			return data, fmt.Errorf("expected string for duration parsing, got %T", data)
		}
		// Allow empty string to represent zero duration (or default)
		if durationStr == "" {
			return time.Duration(0), nil
		}
		d, err := time.ParseDuration(durationStr)
		if err != nil {
			// Return error to viper to handle invalid duration format
			// Wrap with config validation error for consistency
			return nil, fmt.Errorf("%w: invalid duration format '%s': %w", converter.ErrConfigValidation, durationStr, err)
		}
		return d, nil
	})
}

// --- END OF FINAL REVISED FILE internal/cli/config/config.go ---

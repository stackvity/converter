// --- START OF FINAL REVISED FILE internal/cli/config/config.go ---
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath" // Required for DecodeHook
	"runtime"
	"slices" // Requires Go 1.21+
	"strings"
	"text/template" // Import standard template package
	"time"

	// Needed indirectly via viper
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	// FIX: Removed internal imports - default injection moved out
	// "github.com/stackvity/stack-converter/internal/cli/git"    // Import CLI git impl
	// "github.com/stackvity/stack-converter/internal/cli/runner" // Import CLI plugin runner impl
	"github.com/stackvity/stack-converter/pkg/converter"
	"github.com/stackvity/stack-converter/pkg/converter/analysis" // Import subpackages for defaults
	"github.com/stackvity/stack-converter/pkg/converter/encoding"
	libgit "github.com/stackvity/stack-converter/pkg/converter/git" // Import library git for interface
	"github.com/stackvity/stack-converter/pkg/converter/language"   // Import library plugin for interface
	libtemplate "github.com/stackvity/stack-converter/pkg/converter/template"

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
// loads templates, sets up the logger, **and optionally injects default dependencies if needed**.
// Returns the populated Options struct or an error.
// FIX: Added appVersion parameter
func LoadAndValidate(cfgFile, profileName, appVersion string, verbose bool, flags *pflag.FlagSet) (converter.Options, *slog.Logger, error) {
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
		"watch", "extract-comments", "watch-debounce", // Add watch-debounce if bound
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
	v.RegisterAlias("templateFile", "template")         // Alias --template flag to templateFile config key
	v.RegisterAlias("watch.debounce", "watch-debounce") // Alias --watch-debounce flag to watch.debounce config key

	// --- REMOVED Decode Hook Application ---

	// --- Unmarshal Final Configuration ---
	// *** Assign AppVersion before Unmarshal ***
	// FIX: Use the appVersion passed into the function
	opts.AppVersion = appVersion

	if err := v.Unmarshal(&opts); err != nil { // Call without hooks
		tempLogger.Error("Error unmarshalling configuration", slog.Any("error", err))
		return opts, tempLogger, fmt.Errorf("error unmarshalling configuration: %w", err)
	}

	// --- Explicitly Apply Flag Values for Core Paths (overriding others) ---
	// This ensures command-line flags for critical paths take absolute precedence
	// over any values potentially unmarshalled from files/env/defaults.
	if flags.Changed("input") {
		inputVal, _ := flags.GetString("input") // Ignore error, Cobra handles basic parsing
		if inputVal != "" {                     // Ensure non-empty value from flag
			opts.InputPath = inputVal
			tempLogger.Debug("Input path explicitly set from flag", slog.String("path", opts.InputPath))
		}
	}
	if flags.Changed("output") {
		outputVal, _ := flags.GetString("output")
		if outputVal != "" {
			opts.OutputPath = outputVal
			tempLogger.Debug("Output path explicitly set from flag", slog.String("path", opts.OutputPath))
		}
	}
	// --- END Explicit Flag Application for Core Paths ---

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

		// *** Load template using standard library functions ***
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

	// --- MANUALLY PARSE TIME.DURATION AFTER UNMARSHAL ---
	// The WatchConfig.Debounce field is populated as a string by Viper
	debounceDuration, err := time.ParseDuration(opts.WatchConfig.Debounce)
	if err != nil {
		// Check if a flag explicitly set an invalid debounce string
		if flags.Changed("watch-debounce") { // Use direct flag name if defined
			err = fmt.Errorf("%w: invalid watch debounce duration '%s' specified via flag or config: %w", converter.ErrConfigValidation, opts.WatchConfig.Debounce, err)
			logger.Error(err.Error(), slog.String("key", "watch.debounce"), slog.String("value", opts.WatchConfig.Debounce))
			// Decide if this should be fatal - likely yes if user set it explicitly via flag
			return opts, logger, err
		}
		// Otherwise, log warning and use default if parsing string from config/default fails
		defaultDuration := converter.DefaultWatchDebounceDuration
		logger.Warn("Could not parse watch.debounce string, using default",
			slog.String("value", opts.WatchConfig.Debounce),
			slog.Duration("default", defaultDuration),
			slog.String("error", err.Error()))
		debounceDuration = defaultDuration
	}
	// Check for negative duration after parsing
	if debounceDuration < 0 {
		err = fmt.Errorf("%w: invalid negative watch debounce duration '%s' for key 'watch.debounce'", converter.ErrConfigValidation, opts.WatchConfig.Debounce)
		logger.Error(err.Error(), slog.String("key", "watch.debounce"), slog.String("value", opts.WatchConfig.Debounce))
		return opts, logger, err
	}
	// Assign the parsed duration to the correct field in Options
	opts.WatchDebounce = debounceDuration
	// --------------------------------------------------------

	// --- Final Validation and Derivations (Includes Default Dependency Injection) ---
	// Now call validateAndDeriveOptions AFTER explicitly setting InputPath/OutputPath from flags
	// AND manually parsing WatchDebounce.
	// FIX: Pass the logger instance to validateAndDeriveOptions
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

// validateAndDeriveOptions performs semantic validation on the populated Options struct,
// **injects default non-interface dependencies if nil**, and calculates derived fields.
// **Dependency injection for interfaces (GitClient, PluginRunner, CacheManager etc.) is now handled externally.**
// It wraps errors with converter.ErrConfigValidation.
func validateAndDeriveOptions(opts *converter.Options, logger *slog.Logger, flags *pflag.FlagSet) error {
	// === Path Validations ===
	// InputPath and OutputPath should have been explicitly set from flags if provided
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
		err := fmt.Errorf("%w: cannot create or access output directory '%s': %w", converter.ErrConfigValidation, opts.OutputPath, mkdirErr)
		logger.Error(err.Error(), slog.String("key", "OutputPath"), slog.String("value", opts.OutputPath))
		return err
	}
	logger.Debug("Resolved and verified output path", slog.String("path", opts.OutputPath))

	// === Enum String Validations ===
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

	// === Numeric Range Validations ===
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

	// === Inject Default Non-Interface Dependencies (if nil) ===
	// FIX: Moved Interface injection out of this function.
	// Ensure logger and hooks are non-nil (should be handled by caller LoadAndValidate)
	if opts.Logger == nil {
		return fmt.Errorf("internal setup error: logger handler is nil in validateAndDeriveOptions")
	}
	if opts.EventHooks == nil {
		return fmt.Errorf("internal setup error: EventHooks is nil in validateAndDeriveOptions")
	}

	// --- CacheManager (Provide NoOp default if nil and enabled, but actual creation moved out) ---
	if opts.CacheManager == nil {
		if opts.CacheEnabled {
			logger.Warn("Cache enabled, but no CacheManager implementation provided. Defaulting to NoOpCacheManager.")
			opts.CacheManager = &converter.NoOpCacheManager{}
		} else {
			opts.CacheManager = &converter.NoOpCacheManager{} // Use NoOp if cache disabled
		}
	}

	// --- Inject other defaults IF they are still nil after LoadAndValidate populated opts ---
	// These are concrete types or interfaces where a default non-interface implementation exists.
	if opts.LanguageDetector == nil {
		opts.LanguageDetector = language.NewGoEnryDetector(opts.LanguageDetectionConfidenceThreshold, opts.LanguageMappingsOverride)
		logger.Debug("LanguageDetector not provided, using default (GoEnryDetector).")
	}
	if opts.EncodingHandler == nil {
		opts.EncodingHandler = encoding.NewGoCharsetEncodingHandler(opts.DefaultEncoding)
		logger.Debug("EncodingHandler not provided, using default (GoCharsetEncodingHandler).")
	}
	if opts.AnalysisEngine == nil {
		opts.AnalysisEngine = analysis.NewDefaultAnalysisEngine(opts.Logger)
		logger.Debug("AnalysisEngine not provided, using default (DefaultAnalysisEngine).")
	}
	if opts.TemplateExecutor == nil {
		opts.TemplateExecutor = libtemplate.NewGoTemplateExecutor() // Use correct package alias
		logger.Debug("TemplateExecutor not provided, using default (GoTemplateExecutor).")
	}
	// Note: GitClient and PluginRunner defaults are NOT handled here anymore.
	// Caller (CLI layer) is responsible for providing implementations if needed.
	// Validation that they ARE provided if needed occurs below.

	// === Final checks requiring potentially defaulted dependencies ===
	if opts.GitDiffMode != converter.GitDiffModeNone && opts.GitClient == nil {
		err = fmt.Errorf("%w: Git diff requested (--%s) but no GitClient implementation was provided", converter.ErrConfigValidation, opts.GitDiffMode)
		logger.Error(err.Error())
		return err
	}
	anyPluginsEnabled := false
	for _, p := range opts.PluginConfigs {
		if p.Enabled {
			anyPluginsEnabled = true
			break
		}
	}
	if anyPluginsEnabled && opts.PluginRunner == nil {
		err = fmt.Errorf("%w: Plugins are enabled but no PluginRunner implementation was provided", converter.ErrConfigValidation)
		logger.Error(err.Error())
		return err
	}

	// === Derive other options ===
	if opts.Concurrency == 0 {
		opts.Concurrency = runtime.NumCPU()
		logger.Debug("Concurrency not set, defaulting to number of CPUs", slog.Int("concurrency", opts.Concurrency))
	}

	opts.LargeFileThreshold = opts.LargeFileThresholdMB * 1024 * 1024

	// WatchDebounce is handled *after* unmarshal now in LoadAndValidate

	// Derive GitDiffMode from flags (using pflag Changed method for certainty)
	// This logic remains as it derives state from flags, not dependency injection
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

	// --- Derive GitChangedFiles (Requires GitClient) ---
	// This logic remains, but relies on GitClient having been injected previously.
	if opts.GitDiffMode != converter.GitDiffModeNone {
		// GitClient MUST exist at this point if diff mode is active (checked above).
		if opts.GitClient == nil {
			// This indicates an internal logic error if the check above passed.
			return fmt.Errorf("internal error: GitClient is nil despite diff mode '%s' being active", opts.GitDiffMode)
		}
		logger.Debug("Fetching changed files from Git...", slog.String("mode", string(opts.GitDiffMode)), slog.String("ref", opts.GitConfig.SinceRef))
		// Cast to the interface type defined in pkg/converter/git
		var libGitClient libgit.GitClient
		var ok bool
		if libGitClient, ok = opts.GitClient.(libgit.GitClient); !ok {
			// This indicates an internal type mismatch - should not happen if constructed correctly
			err = fmt.Errorf("internal error: opts.GitClient is not of expected type libgit.GitClient")
			logger.Error(err.Error())
			return err
		}

		// Use the interface method
		changedFilesList, gitErr := libGitClient.GetChangedFiles(opts.InputPath, string(opts.GitDiffMode), opts.GitConfig.SinceRef)
		if gitErr != nil {
			// Treat Git operational error as fatal during config validation if diffing was requested
			// FIX: Wrap the correct base error type from libgit
			err = fmt.Errorf("%w: failed to get changed files for diff mode '%s': %w", libgit.ErrGitOperation, opts.GitDiffMode, gitErr)
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
		if opts.GitDiffMode == converter.GitDiffModeSince && len(opts.GitChangedFiles) == 0 {
			logger.Warn("No file changes detected between HEAD and the specified --git-since reference.", slog.String("ref", opts.GitConfig.SinceRef))
		}
	}

	// Handle TUI enable/disable logic considering verbose flag
	// This logic remains as it derives state from flags/opts
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

	// Log final important derived/validated settings
	logger.Debug("Final derived settings validated",
		slog.Int("concurrency", opts.Concurrency),
		slog.Int64("largeFileThresholdBytes", opts.LargeFileThreshold),
		slog.String("cacheFilePath", opts.CacheFilePath),
		slog.Duration("watchDebounceDuration", opts.WatchDebounce), // Log the derived Duration
		slog.String("gitDiffMode", string(opts.GitDiffMode)),
		slog.Int("gitChangedFileCount", len(opts.GitChangedFiles)),
		slog.Bool("tuiEnabledEffective", opts.TuiEnabled),
	)

	return nil
}

// --- REMOVED stringToTimeDurationHookFunc ---

// --- END OF FINAL REVISED FILE internal/cli/config/config.go ---

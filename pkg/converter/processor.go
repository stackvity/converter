// --- START OF FINAL REVISED FILE pkg/converter/processor.go ---
package converter

import (
	"bufio" // **ADDED** for line truncation
	"bytes"
	"context"
	"crypto/sha256" // Keep standard json import
	"encoding/json"
	"fmt"
	"hash"
	"log/slog"
	"os"
	"path/filepath"
	"reflect" // Required for CalculateConfigHash example
	"sort"    // For deterministic hashing
	"strconv" // **ADDED** for parsing truncation config
	"strings"
	"time"

	// Import necessary subpackages
	"github.com/stackvity/stack-converter/pkg/converter/analysis"
	"github.com/stackvity/stack-converter/pkg/converter/cache" // Import cache package
	"github.com/stackvity/stack-converter/pkg/converter/encoding"
	"github.com/stackvity/stack-converter/pkg/converter/git" // Import git package
	"github.com/stackvity/stack-converter/pkg/converter/language"
	"github.com/stackvity/stack-converter/pkg/converter/plugin"   // Import plugin package
	"github.com/stackvity/stack-converter/pkg/converter/template" // Imports the template package types like TemplateMetadata
	// **ADD:** Imports for YAML/TOML if needed later for FrontMatter
	// "github.com/BurntSushi/toml"
	// "gopkg.in/yaml.v3"
)

// --- Constants ---
const truncationNotice = "\n\n[Content truncated due to file size limit]"

// --- FileProcessor ---

// FileProcessor handles the processing pipeline for a single file.
type FileProcessor struct {
	opts             *Options
	logger           *slog.Logger
	cacheManager     cache.CacheManager // Use type from cache package
	langDetector     language.LanguageDetector
	encodingHandler  encoding.EncodingHandler
	analysisEngine   analysis.AnalysisEngine
	gitClient        git.GitClient       // Use type from git package
	pluginRunner     plugin.PluginRunner // Use type from plugin package
	templateExecutor template.TemplateExecutor
	configHash       string
}

// NewFileProcessor creates a new FileProcessor.
func NewFileProcessor(
	opts *Options,
	loggerHandler slog.Handler,
	cacheMgr cache.CacheManager, // Use type from cache package
	langDet language.LanguageDetector,
	encHandler encoding.EncodingHandler,
	analysisEng analysis.AnalysisEngine,
	gitCl git.GitClient, // Use type from git package
	pluginRun plugin.PluginRunner, // Use type from plugin package
	tplExecutor template.TemplateExecutor,
) *FileProcessor {
	// Ensure logger exists even if handler is nil (though engine should provide valid one)
	logger := slog.New(loggerHandler).With(slog.String("component", "processor"))

	// --- Calculate Config Hash ---
	configHash, cfgHashErr := calculateConfigHash(opts, logger)
	if cfgHashErr != nil {
		logger.Error("Failed to calculate config hash during processor init, caching will be ineffective", slog.String("error", cfgHashErr.Error()))
		configHash = ""
	}

	return &FileProcessor{
		opts:             opts,
		logger:           logger,
		cacheManager:     cacheMgr, // Use resolved cache manager
		langDetector:     langDet,
		encodingHandler:  encHandler,
		analysisEngine:   analysisEng,
		gitClient:        gitCl,
		pluginRunner:     pluginRun,
		templateExecutor: tplExecutor,
		configHash:       configHash, // Store pre-calculated hash
	}
}

// ProcessFile executes the full processing pipeline for a given file path.
// **MODIFIED:** Integrated binary/large file handling, refined encoding/language detection calls.
// **FIXED:** Moved all variable declarations before any potential 'goto' jumps.
// **FIXED:** Changed type of 'cacheStatus' from Status to string and used correct constants.
func (p *FileProcessor) ProcessFile(ctx context.Context, absFilePath string) (result interface{}, status Status, err error) {
	startTime := time.Now()
	// Ensure relative path calculation happens first for logging/reporting consistency
	relPath, pathErr := filepath.Rel(p.opts.InputPath, absFilePath)
	if pathErr != nil {
		errMsg := fmt.Sprintf("Failed to calculate relative path for '%s' relative to '%s': %v", absFilePath, p.opts.InputPath, pathErr)
		p.logger.Error(errMsg)
		status = StatusFailed
		err = fmt.Errorf("%w: calculating relative path: %w", ErrConfigValidation, pathErr) // Use config validation error
		return ErrorInfo{Path: "", Error: errMsg, IsFatal: true}, status, err
	}
	relPath = filepath.ToSlash(relPath)
	logArgs := []any{slog.String("path", relPath)} // Initialize logArgs

	// --- Declare ALL variables used after potential 'goto' targets HERE ---
	var sourceContentBytes []byte
	var isBinary bool = false
	var detectedEncoding string = "utf-8" // Default encoding
	var textContent string
	var utf8ContentBytes []byte
	var languageName string = "unknown"
	var confidence float64 = 0.0
	var metadata template.TemplateMetadata
	var metadataMap map[string]interface{}
	var pluginsRun []string
	var formatterOutput PluginRunResult
	var formatterRan bool = false
	var finalContent string = ""
	var frontMatterBlock string = ""
	var hasFrontMatter bool = false
	var templateOutput bytes.Buffer
	var templateErr error
	var outputContent string = ""
	var finalOutputHash string = ""
	var readErr error
	var currentSourceHash string = ""
	var isCacheHit bool = false // Moved from line 215
	var outputHash string = ""  // Moved from line 216
	// FIX: Changed cacheStatus type to string and used correct constant
	var cacheStatus string = CacheStatusDisabled // Moved from line 217
	var isLarge bool = false                     // Moved from line 301
	var truncated bool = false                   // Moved from line 302
	var fileInfo os.FileInfo
	var statErr error
	var modTime time.Time
	var fileSize int64
	// --- End variable declarations ---

	logArgs = append(logArgs, slog.String("configHash", p.configHash)) // Add configHash now

	// --- Defer block ---
	defer func() {
		duration := time.Since(startTime)
		finalStatus := status
		message := ""
		if err != nil {
			if finalStatus != StatusFailed {
				p.logger.Warn("Processor returning error but status was not StatusFailed, correcting status", append(logArgs, slog.String("originalStatus", string(status)), slog.String("error", err.Error()))...)
				finalStatus = StatusFailed
			}
			if _, ok := result.(ErrorInfo); !ok {
				isFatal := p.opts.OnErrorMode == OnErrorStop
				result = ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: isFatal}
			}
			message = err.Error()
		} else if finalStatus == "" {
			finalStatus = StatusSuccess
			p.logger.Debug("Processor finished with no error/status, assuming success", logArgs...)
		}
		logLevel := slog.LevelDebug
		if finalStatus == StatusFailed {
			logLevel = slog.LevelError
		}
		p.logger.Log(ctx, logLevel, "Processor finished file task",
			append(logArgs, slog.String("status", string(finalStatus)), slog.Duration("duration", duration), slog.String("message", message))...)
		status = finalStatus
	}() // End defer

	// --- Pipeline Steps ---

	// 1. Check Context Cancellation Early
	select {
	case <-ctx.Done():
		p.logger.Debug("Processing cancelled before start", logArgs...)
		status = StatusFailed
		err = ctx.Err()
		return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
	default:
	}

	// 2. Get File Info (Stat)
	fileInfo, statErr = os.Stat(absFilePath)
	if statErr != nil {
		errMsg := fmt.Sprintf("Failed to stat file: %v", statErr)
		status = StatusFailed
		err = fmt.Errorf("%w: %w", ErrStatFailed, statErr)
		isFatal := p.opts.OnErrorMode == OnErrorStop
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
	}
	modTime = fileInfo.ModTime()
	fileSize = fileInfo.Size()

	// 3. Config Hash Check
	if p.configHash == "" {
		p.logger.Warn("Proceeding without cache check due to earlier config hash calculation failure.", logArgs...)
		sourceContentBytes, readErr = os.ReadFile(absFilePath)
		if readErr != nil {
			errMsg := fmt.Sprintf("Failed to read file (cache skipped): %v", readErr)
			status = StatusFailed
			err = fmt.Errorf("%w: %w", ErrReadFailed, readErr)
			isFatal := p.opts.OnErrorMode == OnErrorStop
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
		}
		goto ProcessAsMiss // Skip cache check logic
	}

	// 4. Check Cache
	sourceContentBytes, readErr = os.ReadFile(absFilePath)
	if readErr != nil {
		errMsg := fmt.Sprintf("Failed to read file for cache check: %v", readErr)
		status = StatusFailed
		err = fmt.Errorf("%w: %w", ErrReadFailed, readErr)
		isFatal := p.opts.OnErrorMode == OnErrorStop
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
	}
	currentSourceHash = fmt.Sprintf("%x", sha256.Sum256(sourceContentBytes))
	logArgs = append(logArgs, slog.String("sourceHash", currentSourceHash))

	// Variables isCacheHit, outputHash, cacheStatus declared at the top
	if p.opts.CacheEnabled && p.cacheManager != nil {
		// FIX: Use string constant CacheStatusMiss
		cacheStatus = CacheStatusMiss // Assume miss initially if cache enabled
		if !p.opts.IgnoreCacheRead {
			p.logger.Debug("Checking cache", logArgs...)
			isCacheHit, outputHash = p.cacheManager.Check(relPath, modTime, currentSourceHash, p.configHash)

			if isCacheHit {
				p.logger.Info("Cache hit", append(logArgs, slog.String("outputHash", outputHash))...)
				status = StatusCached
				// FIX: Use string constant CacheStatusHit
				cacheStatus = CacheStatusHit
				result = FileInfo{
					Path:        relPath,
					OutputPath:  generateOutputPath(relPath),
					Language:    "unknown (cached)",
					SizeBytes:   fileSize,
					ModTime:     modTime,
					CacheStatus: cacheStatus, // Use string variable
				}
				return result, status, nil // Return early on cache hit
			}
			// cacheStatus remains CacheStatusMiss
			p.logger.Debug("Cache miss", logArgs...)
		} else {
			// cacheStatus remains CacheStatusMiss
			p.logger.Debug("Cache read explicitly disabled (--no-cache), forcing miss.", logArgs...)
		}
	} else {
		// cacheStatus remains CacheStatusDisabled (initial value)
		p.logger.Debug("Cache disabled, proceeding with processing.", logArgs...)
	}

	// === Cache Miss or Cache Disabled ===
ProcessAsMiss: // Label for goto

	// --- Refined File Handling Start ---
	p.logger.Debug("Proceeding with file processing (cache miss or disabled)", logArgs...)

	// 5. File Content (sourceContentBytes already read)
	// 6. Source Hash (currentSourceHash already calculated)

	// *** 7. Detect Binary File FIRST ***
	// isBinary declared before goto
	if p.encodingHandler != nil {
		isBinary = p.encodingHandler.IsBinary(sourceContentBytes)
		if isBinary {
			p.logger.Debug("Binary file detected", logArgs...)
			switch p.opts.BinaryMode {
			case BinarySkip:
				p.logger.Info("Skipping binary file", logArgs...)
				status = StatusSkipped
				result = SkippedInfo{Path: relPath, Reason: SkipReasonBinary, Details: "Binary file detected"}
				return result, status, nil
			case BinaryError:
				errMsg := "binary file encountered"
				status = StatusFailed
				err = ErrBinaryFile
				isFatal := p.opts.OnErrorMode == OnErrorStop
				return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
			case BinaryPlaceholder:
				p.logger.Debug("Generating placeholder for binary file", logArgs...)
				textContent = fmt.Sprintf("[Binary content skipped: %s]", relPath)
				utf8ContentBytes = []byte(textContent)
				detectedEncoding = "utf-8" // Placeholder is UTF-8
				goto PrepareMetadata       // Skip encoding/language/analysis steps
			}
		}
	}

	// *** 8. Handle Encoding (if NOT binary) ***
	// utf8ContentBytes, detectedEncoding declared before goto
	if p.encodingHandler != nil {
		var encErr error
		var certainty bool
		utf8ContentBytes, detectedEncoding, certainty, encErr = p.encodingHandler.DetectAndDecode(sourceContentBytes)
		if encErr != nil {
			p.logger.Warn("Encoding detection/conversion failed, proceeding with potentially raw/lossy content", append(logArgs, slog.String("detectedEncoding", detectedEncoding), slog.Bool("certainty", certainty), slog.String("error", encErr.Error()))...)
		} else {
			p.logger.Debug("Encoding handled", append(logArgs, slog.String("detectedEncoding", detectedEncoding), slog.Bool("certainty", certainty))...)
		}
	} else {
		p.logger.Warn("Encoding handler not available, assuming UTF-8", logArgs...)
		utf8ContentBytes = sourceContentBytes
	}
	textContent = string(utf8ContentBytes) // textContent declared before goto

	// *** 9. Handle Large File (if NOT binary) ***
	// isLarge, truncated declared before goto
	isLarge = fileSize > p.opts.LargeFileThreshold
	if isLarge {
		p.logger.Debug("Large file detected", append(logArgs, slog.Int64("size", fileSize), slog.Int64("threshold", p.opts.LargeFileThreshold))...)
		switch p.opts.LargeFileMode {
		case LargeFileSkip:
			p.logger.Info("Skipping large file", logArgs...)
			status = StatusSkipped
			details := fmt.Sprintf("File size %d bytes > threshold %d bytes", fileSize, p.opts.LargeFileThreshold)
			result = SkippedInfo{Path: relPath, Reason: SkipReasonLarge, Details: details}
			return result, status, nil
		case LargeFileError:
			errMsg := fmt.Sprintf("large file encountered (size %d bytes > threshold %d bytes)", fileSize, p.opts.LargeFileThreshold)
			status = StatusFailed
			err = ErrLargeFile
			isFatal := p.opts.OnErrorMode == OnErrorStop
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
		case LargeFileTruncate:
			p.logger.Debug("Truncating large file", append(logArgs, slog.String("truncateCfg", p.opts.LargeFileTruncateCfg))...)
			truncatedBytes, truncErr := truncateContent(utf8ContentBytes, p.opts.LargeFileTruncateCfg)
			if truncErr != nil {
				p.logger.Warn("Failed to truncate content, proceeding with original large content", append(logArgs, slog.String("error", truncErr.Error()))...)
			} else {
				utf8ContentBytes = truncatedBytes
				textContent = string(utf8ContentBytes) + truncationNotice
				truncated = true
				p.logger.Debug("Content truncated successfully", logArgs...)
			}
		}
	}
	// --- Refined File Handling End ---

	// Check context cancellation after file handling
	select {
	case <-ctx.Done():
		p.logger.Debug("Processing cancelled after file handling", logArgs...)
		status = StatusFailed
		err = ctx.Err()
		return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
	default:
	}

	// *** 10. Detect Language (if NOT binary - use UTF-8 or truncated content) ***
PrepareMetadata: // Label for goto
	// languageName, confidence declared before goto
	if isBinary && p.opts.BinaryMode == BinaryPlaceholder {
		languageName = "binary_placeholder"
		confidence = 1.0
	} else if p.langDetector != nil {
		lang, conf, langErr := p.langDetector.Detect(utf8ContentBytes, relPath)
		if langErr != nil {
			p.logger.Warn("Language detection failed", append(logArgs, slog.String("error", langErr.Error()))...)
			languageName = "plaintext"
			confidence = 0.0
		} else {
			languageName = lang
			confidence = conf
		}
	} else {
		p.logger.Warn("Language detector not available, using basic extension mapping", logArgs...)
		ext := filepath.Ext(relPath)
		switch strings.ToLower(ext) {
		case ".go":
			languageName = "go"
		case ".py":
			languageName = "python"
		default:
			languageName = "plaintext"
		}
		confidence = 0.5
	}
	p.logger.Debug("Language detected", append(logArgs, slog.String("language", languageName), slog.Float64("confidence", confidence))...)

	// *** 11. Prepare Metadata Struct ***
	// metadata declared before goto
	metadata = template.TemplateMetadata{
		FilePath:           relPath,
		FileName:           filepath.Base(relPath),
		OutputPath:         generateOutputPath(relPath),
		Content:            textContent,
		DetectedLanguage:   languageName,
		LanguageConfidence: confidence,
		SizeBytes:          fileSize,
		ModTime:            modTime,
		ContentHash:        currentSourceHash,
		IsBinary:           isBinary,
		IsLarge:            isLarge,
		Truncated:          truncated,
		GitInfo:            nil,
		ExtractedComments:  nil,
		FrontMatter:        make(map[string]interface{}),
	}

	// *** 11b. Fetch Optional Metadata ***
	var commentsStr *string
	extractedComments := false
	if p.opts.AnalysisOptions.ExtractComments && p.analysisEngine != nil && !isBinary {
		comments, analysisErr := p.analysisEngine.ExtractDocComments(utf8ContentBytes, languageName, p.opts.AnalysisOptions.CommentStyles)
		if analysisErr != nil {
			p.logger.Warn("Failed to extract comments", append(logArgs, slog.String("error", analysisErr.Error()))...)
		} else if comments != "" {
			commentsStr = &comments
			extractedComments = true
			p.logger.Debug("Comments extracted successfully", logArgs...)
		}
	}
	metadata.ExtractedComments = commentsStr

	var gitInfoMap map[string]string
	if p.opts.GitMetadataEnabled && p.gitClient != nil {
		var gitErr error
		gitInfoMap, gitErr = p.gitClient.GetFileMetadata(p.opts.InputPath, absFilePath)
		if gitErr != nil {
			p.logger.Warn("Failed to get Git metadata", append(logArgs, slog.String("error", gitErr.Error()))...)
		} else if len(gitInfoMap) > 0 {
			metadata.GitInfo = &template.GitInfo{
				Commit:      gitInfoMap["commit"],
				Author:      gitInfoMap["author"],
				AuthorEmail: gitInfoMap["authorEmail"],
				DateISO:     gitInfoMap["dateISO"],
			}
			p.logger.Debug("Git metadata fetched successfully", logArgs...)
		}
	}

	p.logger.Debug("Template metadata populated", logArgs...)

	// --- Subsequent Steps ---
	// metadataMap declared before goto
	metadataMap = convertMetaToMap(&metadata)

	// --- 12. Preprocessor Plugins ---
	// pluginsRun declared before goto
	pluginsRun = []string{} // Initialize slice

	for _, pluginCfg := range p.opts.PluginConfigs {
		if pluginCfg.Enabled && pluginCfg.Stage == plugin.PluginStagePreprocessor && appliesTo(relPath, pluginCfg.AppliesTo) {
			p.logger.Debug("Running preprocessor plugin", append(logArgs, slog.String("plugin_name", pluginCfg.Name))...)
			pluginInput := plugin.PluginInput{
				SchemaVersion: plugin.PluginSchemaVersion,
				Stage:         plugin.PluginStagePreprocessor,
				FilePath:      relPath,
				Content:       textContent,
				Metadata:      metadataMap,
				Config:        pluginCfg.Config,
			}
			pluginOutput, runErr := p.pluginRunner.Run(ctx, plugin.PluginStagePreprocessor, pluginCfg, pluginInput)
			if runErr != nil {
				errMsg := fmt.Sprintf("Preprocessor plugin '%s' failed: %v", pluginCfg.Name, runErr)
				status = StatusFailed
				err = fmt.Errorf("%w: %w", plugin.ErrPluginExecution, runErr)
				isFatal := p.opts.OnErrorMode == OnErrorStop
				return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
			}
			pluginsRun = append(pluginsRun, pluginCfg.Name)
			if pluginOutput.Content != "" {
				textContent = pluginOutput.Content
				metadata.Content = textContent
			}
			if pluginOutput.Metadata != nil {
				updateMetadataFromMap(metadataMap, pluginOutput.Metadata)
				updateStructFromMap(&metadata, pluginOutput.Metadata)
			}
		}
	}

	// --- 12b. Formatter Plugins ---
	// formatterOutput, formatterRan declared before goto
	for _, pluginCfg := range p.opts.PluginConfigs {
		if pluginCfg.Enabled && pluginCfg.Stage == plugin.PluginStageFormatter && appliesTo(relPath, pluginCfg.AppliesTo) {
			p.logger.Debug("Running formatter plugin", append(logArgs, slog.String("plugin_name", pluginCfg.Name))...)
			pluginInput := plugin.PluginInput{
				SchemaVersion: plugin.PluginSchemaVersion,
				Stage:         plugin.PluginStageFormatter,
				FilePath:      relPath,
				Content:       textContent,
				Metadata:      metadataMap,
				Config:        pluginCfg.Config,
			}
			pluginOutput, runErr := p.pluginRunner.Run(ctx, plugin.PluginStageFormatter, pluginCfg, pluginInput)
			if runErr != nil {
				errMsg := fmt.Sprintf("Formatter plugin '%s' failed: %v", pluginCfg.Name, runErr)
				status = StatusFailed
				err = fmt.Errorf("%w: %w", plugin.ErrPluginExecution, runErr)
				isFatal := p.opts.OnErrorMode == OnErrorStop
				return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
			}
			pluginsRun = append(pluginsRun, pluginCfg.Name)
			if pluginOutput.Output != "" {
				formatterOutput.IsFinalOutput = true
				formatterOutput.Output = pluginOutput.Output
				formatterOutput.FinalPlugin = pluginCfg.Name
				if pluginOutput.Metadata != nil {
					updateMetadataFromMap(metadataMap, pluginOutput.Metadata)
					updateStructFromMap(&metadata, pluginOutput.Metadata)
				}
				formatterRan = true
				p.logger.Debug("Formatter plugin provided final output", append(logArgs, slog.String("plugin_name", pluginCfg.Name))...)
				break
			} else {
				p.logger.Warn("Formatter plugin did not provide output content", append(logArgs, slog.String("plugin_name", pluginCfg.Name))...)
			}
		}
	}

	// finalContent declared before goto
	if formatterRan && formatterOutput.IsFinalOutput {
		finalContent = formatterOutput.Output
		p.logger.Debug("Using output from formatter plugin, skipping template/postprocessing", append(logArgs, slog.String("formatter_plugin", formatterOutput.FinalPlugin))...)
		goto WriteOutput
	}

	// --- 13. Front Matter ---
	// frontMatterBlock, hasFrontMatter declared before goto
	if p.opts.FrontMatterConfig.Enabled {
		fmData := make(map[string]interface{})
		for k, v := range p.opts.FrontMatterConfig.Static {
			fmData[k] = v
		}
		for _, key := range p.opts.FrontMatterConfig.Include {
			if val, ok := metadataMap[key]; ok {
				fmData[key] = val
			} else {
				p.logger.Log(ctx, slog.LevelDebug-4, "Front matter include field not found in metadata", append(logArgs, slog.String("field", key))...)
			}
		}
		if len(fmData) > 0 {
			fmBytes, fmErr := generateFrontMatter(fmData, p.opts.FrontMatterConfig.Format)
			if fmErr != nil {
				errMsg := fmt.Sprintf("Failed to generate front matter: %v", fmErr)
				status = StatusFailed
				err = fmt.Errorf("%w: %w", ErrFrontMatterGen, fmErr)
				isFatal := p.opts.OnErrorMode == OnErrorStop
				return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
			}
			frontMatterBlock = string(fmBytes)
			hasFrontMatter = true
			p.logger.Debug("Front matter generated successfully", append(logArgs, slog.String("format", p.opts.FrontMatterConfig.Format))...)
		}
	} else {
		p.logger.Debug("Front matter generation skipped (disabled).", logArgs...)
	}

	// --- 14. Execute Template ---
	// templateOutput declared before goto
	p.logger.Debug("Executing template", append(logArgs, slog.String("templateName", p.opts.Template.Name()))...)
	if p.templateExecutor == nil {
		errMsg := "Internal error: TemplateExecutor is nil"
		p.logger.Error(errMsg, logArgs...)
		status = StatusFailed
		err = ErrConfigValidation
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err
	}

	// templateErr declared before goto
	templateErr = p.templateExecutor.Execute(&templateOutput, p.opts.Template, &metadata)
	if templateErr != nil {
		errMsg := fmt.Sprintf("Template execution failed: %v", templateErr)
		status = StatusFailed
		err = fmt.Errorf("%w: %w", ErrTemplateExecution, templateErr)
		isFatal := p.opts.OnErrorMode == OnErrorStop
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
	}
	// outputContent declared before goto
	outputContent = templateOutput.String()
	p.logger.Debug("Template executed successfully", logArgs...)

	// --- 15. Postprocessor Plugins ---
	for _, pluginCfg := range p.opts.PluginConfigs {
		if pluginCfg.Enabled && pluginCfg.Stage == plugin.PluginStagePostprocessor && appliesTo(relPath, pluginCfg.AppliesTo) {
			p.logger.Debug("Running postprocessor plugin", append(logArgs, slog.String("plugin_name", pluginCfg.Name))...)
			pluginInput := plugin.PluginInput{
				SchemaVersion: plugin.PluginSchemaVersion,
				Stage:         plugin.PluginStagePostprocessor,
				FilePath:      relPath,
				Content:       outputContent,
				Metadata:      metadataMap,
				Config:        pluginCfg.Config,
			}
			pluginOutput, runErr := p.pluginRunner.Run(ctx, plugin.PluginStagePostprocessor, pluginCfg, pluginInput)
			if runErr != nil {
				errMsg := fmt.Sprintf("Postprocessor plugin '%s' failed: %v", pluginCfg.Name, runErr)
				status = StatusFailed
				err = fmt.Errorf("%w: %w", plugin.ErrPluginExecution, runErr)
				isFatal := p.opts.OnErrorMode == OnErrorStop
				return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
			}
			pluginsRun = append(pluginsRun, pluginCfg.Name)
			if pluginOutput.Content != "" {
				outputContent = pluginOutput.Content
			}
			if pluginOutput.Metadata != nil {
				updateMetadataFromMap(metadataMap, pluginOutput.Metadata)
				updateStructFromMap(&metadata, pluginOutput.Metadata)
			}
		}
	}

	// finalContent declared before goto
	finalContent = frontMatterBlock + outputContent

WriteOutput:
	// --- 16. Prepare Final Content & Output Path ---
	outputRelPath := metadata.OutputPath
	if outputRelPath == "" {
		errMsg := "Internal error: generated empty output path"
		status = StatusFailed
		err = fmt.Errorf("%w: %s for source %s", ErrWriteFailed, errMsg, relPath)
		result = ErrorInfo{Path: relPath, Error: errMsg}
		return result, status, err
	}
	absOutputPath := filepath.Join(p.opts.OutputPath, outputRelPath)
	logArgs = append(logArgs, slog.String("outputPath", outputRelPath))

	// --- 17. Ensure Output Directory Exists ---
	outputDir := filepath.Dir(absOutputPath)
	if mkdirErr := os.MkdirAll(outputDir, 0755); mkdirErr != nil {
		errMsg := fmt.Sprintf("Failed to create output directory '%s': %v", outputDir, mkdirErr)
		status = StatusFailed
		err = fmt.Errorf("%w: %w", ErrMkdirFailed, mkdirErr)
		isFatal := p.opts.OnErrorMode == OnErrorStop
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
	}

	// --- 18. Write Output File ---
	p.logger.Debug("Writing output file", logArgs...)
	outputContentBytes := []byte(finalContent)
	writeErr := os.WriteFile(absOutputPath, outputContentBytes, 0644)
	if writeErr != nil {
		errMsg := fmt.Sprintf("Failed to write output file '%s': %v", absOutputPath, writeErr)
		status = StatusFailed
		err = fmt.Errorf("%w: %w", ErrWriteFailed, writeErr)
		isFatal := p.opts.OnErrorMode == OnErrorStop
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatal}, status, err
	}
	// finalOutputHash declared before goto
	finalOutputHash = fmt.Sprintf("%x", sha256.Sum256(outputContentBytes))
	p.logger.Info("Output file written successfully", append(logArgs, slog.String("outputHash", finalOutputHash))...)

	// --- 19. Update Cache ---
	if p.opts.CacheEnabled && p.cacheManager != nil && p.cacheManager != (*NoOpCacheManager)(nil) {
		p.logger.Debug("Updating cache entry (in memory)", append(logArgs, slog.String("outputHash", finalOutputHash))...)
		updateErr := p.cacheManager.Update(relPath, modTime, currentSourceHash, p.configHash, finalOutputHash)
		if updateErr != nil {
			p.logger.Warn("Failed to update cache entry in memory", append(logArgs, slog.String("error", updateErr.Error()))...)
		}
	} else {
		p.logger.Debug("Cache update skipped (disabled or no manager).", logArgs...)
	}

	// --- 20. Success ---
	status = StatusSuccess
	result = FileInfo{
		Path:               relPath,
		OutputPath:         outputRelPath,
		Language:           metadata.DetectedLanguage,
		LanguageConfidence: metadata.LanguageConfidence,
		SizeBytes:          fileSize,
		ModTime:            modTime,
		CacheStatus:        cacheStatus, // Use string variable
		ExtractedComments:  extractedComments,
		FrontMatter:        hasFrontMatter,
		PluginsRun:         pluginsRun,
	}
	return result, status, nil // Success
}

// CalculateConfigHash generates a stable hash representing relevant configuration.
// Moved this function to be a method on FileProcessor so it can access the processor's logger.
func (p *FileProcessor) CalculateConfigHash() (string, error) {
	p.logger.Debug("Calculating configuration hash")
	hasher := sha256.New()

	// Helper function to add string field to hash
	addToHash := func(h hash.Hash, key string, value string) {
		h.Write([]byte(key + ":" + value + ";"))
	}
	// Helper function to add bool field to hash
	addBoolToHash := func(h hash.Hash, key string, value bool) {
		strVal := "false"
		if value {
			strVal = "true"
		}
		h.Write([]byte(key + ":" + strVal + ";"))
	}

	// 1. Template Content/Path Hash
	if p.opts.Template != nil {
		if p.opts.TemplatePath != "" {
			addToHash(hasher, "TemplatePath", p.opts.TemplatePath)
			contentBytes, err := os.ReadFile(p.opts.TemplatePath)
			if err == nil {
				addToHash(hasher, "TemplateContentHash", fmt.Sprintf("%x", sha256.Sum256(contentBytes)))
				p.logger.Debug("Adding TemplatePath and ContentHash to config hash", slog.String("path", p.opts.TemplatePath))
			} else {
				p.logger.Warn("Could not read template file for hashing, relying on path only", slog.String("path", p.opts.TemplatePath), slog.Any("error", err))
			}
		} else {
			addToHash(hasher, "TemplatePath", "embedded-default")
			p.logger.Debug("Adding embedded template marker to config hash")
		}
	} else {
		addToHash(hasher, "TemplatePath", "nil-template")
		p.logger.Warn("No template loaded in options during config hash calculation")
	}

	// 2. Front Matter Configuration
	addBoolToHash(hasher, "FrontMatterEnabled", p.opts.FrontMatterConfig.Enabled)
	if p.opts.FrontMatterConfig.Enabled {
		addToHash(hasher, "FrontMatterFormat", p.opts.FrontMatterConfig.Format)
		staticKeys := make([]string, 0, len(p.opts.FrontMatterConfig.Static))
		for k := range p.opts.FrontMatterConfig.Static {
			staticKeys = append(staticKeys, k)
		}
		sort.Strings(staticKeys)
		for _, k := range staticKeys {
			valStr := fmt.Sprintf("%v", p.opts.FrontMatterConfig.Static[k])
			addToHash(hasher, "FrontMatterStatic_"+k, valStr)
		}
		includeKeys := make([]string, len(p.opts.FrontMatterConfig.Include))
		copy(includeKeys, p.opts.FrontMatterConfig.Include)
		sort.Strings(includeKeys)
		addToHash(hasher, "FrontMatterInclude", strings.Join(includeKeys, ","))
	}

	// 3. Analysis Options
	addBoolToHash(hasher, "AnalysisExtractComments", p.opts.AnalysisOptions.ExtractComments)
	if p.opts.AnalysisOptions.ExtractComments {
		styles := make([]string, len(p.opts.AnalysisOptions.CommentStyles))
		copy(styles, p.opts.AnalysisOptions.CommentStyles)
		sort.Strings(styles)
		addToHash(hasher, "AnalysisCommentStyles", strings.Join(styles, ","))
	}

	// 4. File Handling Modes
	addToHash(hasher, "BinaryMode", string(p.opts.BinaryMode))
	addToHash(hasher, "LargeFileMode", string(p.opts.LargeFileMode))
	if p.opts.LargeFileMode == LargeFileTruncate {
		addToHash(hasher, "LargeFileTruncateCfg", p.opts.LargeFileTruncateCfg)
	}

	// 5. Enabled Plugins (Sorted by Name, including Command, AppliesTo, and Config)
	// FIX: Iterate over plugin.PluginConfig slice
	enabledPlugins := make([]plugin.PluginConfig, 0)
	for _, pl := range p.opts.PluginConfigs {
		if pl.Enabled {
			pluginCopy := pl
			sort.Strings(pluginCopy.AppliesTo)
			enabledPlugins = append(enabledPlugins, pluginCopy)
		}
	}
	sort.Slice(enabledPlugins, func(i, j int) bool {
		return enabledPlugins[i].Name < enabledPlugins[j].Name
	})

	for _, pl := range enabledPlugins {
		hasher.Write([]byte("PluginStart:" + pl.Name + ";"))
		addToHash(hasher, "PluginStage", pl.Stage)
		addToHash(hasher, "PluginCommand", strings.Join(pl.Command, " "))
		addToHash(hasher, "PluginAppliesTo", strings.Join(pl.AppliesTo, ","))
		if len(pl.Config) > 0 {
			configKeys := make([]string, 0, len(pl.Config))
			for k := range pl.Config {
				configKeys = append(configKeys, k)
			}
			sort.Strings(configKeys)
			for _, k := range configKeys {
				valStr := fmt.Sprintf("%v", pl.Config[k])
				addToHash(hasher, "PluginConfig_"+k, valStr)
			}
		}
		hasher.Write([]byte("PluginEnd:" + pl.Name + ";"))
	}

	// 6. Other relevant options that influence output
	addBoolToHash(hasher, "GitMetadataEnabled", p.opts.GitMetadataEnabled)
	// Add other options here if they affect the final generated output,

	// 7. Application Version
	appVersion := p.opts.AppVersion
	if appVersion == "" {
		appVersion = "dev"
	}
	addToHash(hasher, "AppVersion", appVersion)

	// Calculate final hash
	hashBytes := hasher.Sum(nil)
	configHash := fmt.Sprintf("%x", hashBytes)
	p.logger.Debug("Calculated config hash", slog.String("hash", configHash))
	return configHash, nil
}

// calculateConfigHash generates a stable hash representing relevant configuration.
// This standalone version is kept for reference or potentially tests, but the
// processor now uses its own method `(p *FileProcessor) CalculateConfigHash()`.
func calculateConfigHash(opts *Options, logger *slog.Logger) (string, error) {
	logger.Debug("Calculating configuration hash (standalone function)")
	hasher := sha256.New()

	// Helper function to add string field to hash
	addToHash := func(h hash.Hash, key string, value string) {
		h.Write([]byte(key + ":" + value + ";"))
	}
	// Helper function to add bool field to hash
	addBoolToHash := func(h hash.Hash, key string, value bool) {
		strVal := "false"
		if value {
			strVal = "true"
		}
		h.Write([]byte(key + ":" + strVal + ";"))
	}

	// --- Hashing Logic (Mirrors the method version) ---

	// 1. Template Content/Path Hash
	if opts.Template != nil {
		if opts.TemplatePath != "" {
			addToHash(hasher, "TemplatePath", opts.TemplatePath)
			contentBytes, err := os.ReadFile(opts.TemplatePath)
			if err == nil {
				addToHash(hasher, "TemplateContentHash", fmt.Sprintf("%x", sha256.Sum256(contentBytes)))
			} else {
				logger.Warn("Standalone: Could not read template file for hashing", slog.String("path", opts.TemplatePath), slog.Any("error", err))
			}
		} else {
			addToHash(hasher, "TemplatePath", "embedded-default")
		}
	} else {
		addToHash(hasher, "TemplatePath", "nil-template")
	}

	// 2. Front Matter Configuration
	addBoolToHash(hasher, "FrontMatterEnabled", opts.FrontMatterConfig.Enabled)
	if opts.FrontMatterConfig.Enabled {
		addToHash(hasher, "FrontMatterFormat", opts.FrontMatterConfig.Format)
		staticKeys := make([]string, 0, len(opts.FrontMatterConfig.Static))
		for k := range opts.FrontMatterConfig.Static {
			staticKeys = append(staticKeys, k)
		}
		sort.Strings(staticKeys)
		for _, k := range staticKeys {
			valStr := fmt.Sprintf("%v", opts.FrontMatterConfig.Static[k])
			addToHash(hasher, "FrontMatterStatic_"+k, valStr)
		}
		includeKeys := make([]string, len(opts.FrontMatterConfig.Include))
		copy(includeKeys, opts.FrontMatterConfig.Include)
		sort.Strings(includeKeys)
		addToHash(hasher, "FrontMatterInclude", strings.Join(includeKeys, ","))
	}

	// 3. Analysis Options
	addBoolToHash(hasher, "AnalysisExtractComments", opts.AnalysisOptions.ExtractComments)
	if opts.AnalysisOptions.ExtractComments {
		styles := make([]string, len(opts.AnalysisOptions.CommentStyles))
		copy(styles, opts.AnalysisOptions.CommentStyles)
		sort.Strings(styles)
		addToHash(hasher, "AnalysisCommentStyles", strings.Join(styles, ","))
	}

	// 4. File Handling Modes
	addToHash(hasher, "BinaryMode", string(opts.BinaryMode))
	addToHash(hasher, "LargeFileMode", string(opts.LargeFileMode))
	if opts.LargeFileMode == LargeFileTruncate {
		addToHash(hasher, "LargeFileTruncateCfg", opts.LargeFileTruncateCfg)
	}

	// 5. Enabled Plugins
	// FIX: Use plugin.PluginConfig type
	enabledPlugins := make([]plugin.PluginConfig, 0)
	for _, pl := range opts.PluginConfigs {
		if pl.Enabled {
			pluginCopy := pl
			sort.Strings(pluginCopy.AppliesTo)
			enabledPlugins = append(enabledPlugins, pluginCopy)
		}
	}
	sort.Slice(enabledPlugins, func(i, j int) bool {
		return enabledPlugins[i].Name < enabledPlugins[j].Name
	})

	for _, pl := range enabledPlugins {
		hasher.Write([]byte("PluginStart:" + pl.Name + ";"))
		addToHash(hasher, "PluginStage", pl.Stage)
		addToHash(hasher, "PluginCommand", strings.Join(pl.Command, " "))
		addToHash(hasher, "PluginAppliesTo", strings.Join(pl.AppliesTo, ","))
		if len(pl.Config) > 0 {
			configKeys := make([]string, 0, len(pl.Config))
			for k := range pl.Config {
				configKeys = append(configKeys, k)
			}
			sort.Strings(configKeys)
			for _, k := range configKeys {
				valStr := fmt.Sprintf("%v", pl.Config[k])
				addToHash(hasher, "PluginConfig_"+k, valStr)
			}
		}
		hasher.Write([]byte("PluginEnd:" + pl.Name + ";"))
	}

	// 6. Other relevant options
	addBoolToHash(hasher, "GitMetadataEnabled", opts.GitMetadataEnabled)

	// 7. Application Version
	appVersion := opts.AppVersion
	if appVersion == "" {
		appVersion = "dev"
	}
	addToHash(hasher, "AppVersion", appVersion)

	// --- End Hashing Logic ---

	hashBytes := hasher.Sum(nil)
	configHash := fmt.Sprintf("%x", hashBytes)
	logger.Debug("Calculated config hash (standalone)", slog.String("hash", configHash))
	return configHash, nil
}

// generateOutputPath determines the output path for a source file.
// (Remains the same as previous step)
func generateOutputPath(relPath string) string {
	if relPath == "." || relPath == "" {
		return ""
	}
	// Handle hidden files/dirs starting with '.'
	dir, base := filepath.Split(relPath)
	ext := ""
	// Check if base itself is hidden (e.g., ".myfile" or ".")
	if base != "." && base != ".." {
		ext = filepath.Ext(base)
		base = strings.TrimSuffix(base, ext)
	} else if base == "." { // Handle case like "some/path/." -> "some/path.md"
		dir = strings.TrimSuffix(dir, "/")
		base = "" // No base name
		// Treat extension as empty in this case
	}

	// If base name is empty (e.g., after stripping extension from ".gitignore"), use original name
	if base == "" && ext != "" {
		// Example: ".gitignore" -> ".gitignore.md"
		return relPath + ".md"
	}
	// If extension is empty or it was just "."
	if ext == "" || ext == "." {
		// Example: "Makefile" -> "Makefile.md", "README" -> "README.md"
		return relPath + ".md"
	}
	// Avoid double .md extension
	if ext == ".md" {
		return relPath
	}
	// Default case: replace extension
	// Example: "src/main.go" -> "src/main.md"
	return filepath.Join(dir, base+".md")
}

// convertMetaToMap converts TemplateMetadata struct to map[string]interface{} for plugins/frontmatter.
// (Remains the same as previous step)
func convertMetaToMap(meta *template.TemplateMetadata) map[string]interface{} {
	m := make(map[string]interface{})
	if meta == nil {
		return m
	}
	v := reflect.ValueOf(*meta)
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		fieldName := t.Field(i).Name
		fieldValue := v.Field(i)

		if !t.Field(i).IsExported() || fieldName == "Options" {
			continue
		}

		fieldInterface := fieldValue.Interface()

		// Format time.Time as RFC3339 string for JSON compatibility
		if tm, ok := fieldInterface.(time.Time); ok {
			m[fieldName] = tm.UTC().Format(time.RFC3339) // Ensure consistent UTC format
			continue
		}

		// Handle pointers: Dereference if non-nil, otherwise set null
		if fieldValue.Kind() == reflect.Ptr {
			if fieldValue.IsNil() {
				m[fieldName] = nil
			} else {
				// Dereference common pointer types explicitly if needed,
				// otherwise use Elem().Interface()
				switch ptr := fieldInterface.(type) {
				case *string:
					m[fieldName] = *ptr
				case *template.GitInfo:
					// Recursively convert GitInfo struct if needed, or pass as is
					m[fieldName] = convertGitInfoToMap(ptr)
				default:
					// Fallback for other pointer types
					m[fieldName] = fieldValue.Elem().Interface()
				}
			}
			continue
		}

		// Ensure maps are initialized if nil
		if fieldValue.Kind() == reflect.Map {
			if fieldValue.IsNil() {
				// Ensure the map type matches TemplateMetadata.FrontMatter
				if fieldName == "FrontMatter" {
					m[fieldName] = make(map[string]interface{})
				} else {
					// Handle other potential map types if added later
					m[fieldName] = nil // Or create appropriate map type
				}
			} else {
				m[fieldName] = fieldInterface
			}
			continue
		}

		// Add other exported fields directly
		m[fieldName] = fieldInterface
	}
	// Explicitly remove the raw content from the map passed to plugins/frontmatter
	delete(m, "Content")
	return m
}

// Helper to convert GitInfo struct to map for metadata map
func convertGitInfoToMap(info *template.GitInfo) map[string]interface{} {
	if info == nil {
		return nil
	}
	m := make(map[string]interface{})
	m["Commit"] = info.Commit
	m["Author"] = info.Author
	m["AuthorEmail"] = info.AuthorEmail
	m["DateISO"] = info.DateISO // Already string
	return m
}

// updateMetadataFromMap merges metadata received from a plugin back onto the main metadata map.
func updateMetadataFromMap(target map[string]interface{}, source map[string]interface{}) {
	if target == nil || source == nil {
		return
	}
	for key, value := range source {
		target[key] = value // Overwrite existing keys, add new ones
	}
}

// updateStructFromMap attempts to update fields in the TemplateMetadata struct
// based on keys returned in a plugin's metadata map. This is more complex
// due to type differences and potential mismatches.
func updateStructFromMap(target *template.TemplateMetadata, source map[string]interface{}) {
	if target == nil || source == nil {
		return
	}
	v := reflect.ValueOf(target).Elem() // Get mutable struct value
	t := v.Type()

	for key, sourceValue := range source {
		// Skip keys that don't match field names in TemplateMetadata
		field, found := t.FieldByName(key)
		if !found || !field.IsExported() {
			continue
		}

		targetField := v.FieldByName(key)
		if !targetField.IsValid() || !targetField.CanSet() {
			continue
		}

		sourceValueReflect := reflect.ValueOf(sourceValue)

		// Handle type compatibility carefully
		if sourceValueReflect.IsValid() && sourceValueReflect.Type().AssignableTo(targetField.Type()) {
			targetField.Set(sourceValueReflect)
		} else if sourceValue == nil && (targetField.Kind() == reflect.Ptr || targetField.Kind() == reflect.Map || targetField.Kind() == reflect.Slice) {
			// Allow setting nil to pointers/maps/slices
			targetField.Set(reflect.Zero(targetField.Type()))
		} else {
			// Attempt basic type conversions if possible, otherwise log/ignore
			// Example: Convert string back to time.Time for ModTime if needed
			if targetField.Type() == reflect.TypeOf(time.Time{}) && sourceValueReflect.Kind() == reflect.String {
				if tm, err := time.Parse(time.RFC3339, sourceValueReflect.String()); err == nil {
					targetField.Set(reflect.ValueOf(tm))
				}
			}
			// Add more specific type conversions as needed, e.g., for GitInfo map back to struct? (complex)
			// Or simply ignore incompatible types.
		}
	}
}

// --- Placeholder Functions (to be implemented/refined in later steps) ---

// PluginRunResult holds results from running plugins for a stage.
// Placeholder - refine when implementing plugins (Step 7/8).
// FIX: Use plugin.PluginOutput type
type PluginRunResult struct {
	IsFinalOutput       bool
	plugin.PluginOutput // Embed the actual output structure
	FinalPlugin         string
}

// generateFrontMatter marshals data into YAML or TOML format including delimiters.
// Placeholder - refine when implementing Front Matter (Step 7).
func generateFrontMatter(data map[string]interface{}, format string) ([]byte, error) {
	var marshalledBytes []byte
	var err error
	var delimiter string

	switch strings.ToLower(format) {
	case "yaml":
		delimiter = "---\n"
		// Use gopkg.in/yaml.v3 for marshalling if added as dependency
		// marshalledBytes, err = yaml.Marshal(data)
		// Placeholder marshalling for now:
		marshalledBytes, err = json.MarshalIndent(data, "", "  ") // Use JSON as placeholder
		if err == nil {
			marshalledBytes = append([]byte(delimiter), marshalledBytes...)
			marshalledBytes = append(marshalledBytes, []byte("\n"+delimiter)...)
		}
	case "toml":
		delimiter = "+++\n"
		// Use github.com/BurntSushi/toml for marshalling if added as dependency
		// var buf bytes.Buffer
		// encoder := toml.NewEncoder(&buf)
		// err = encoder.Encode(data)
		// marshalledBytes = buf.Bytes()
		// Placeholder marshalling for now:
		marshalledBytes, err = json.MarshalIndent(data, "", "  ") // Use JSON as placeholder
		if err == nil {
			marshalledBytes = append([]byte(delimiter), marshalledBytes...)
			marshalledBytes = append(marshalledBytes, []byte("\n"+delimiter)...)
		}
	default:
		err = fmt.Errorf("unsupported front matter format: %s", format)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to marshal front matter data to %s: %w", format, err)
	}

	return marshalledBytes, nil
}

// truncateContent reduces content size based on config string (bytes or lines).
// **MODIFIED:** Implemented basic byte/line truncation.
func truncateContent(content []byte, cfg string) ([]byte, error) {
	cfg = strings.TrimSpace(strings.ToLower(cfg))

	if strings.HasSuffix(cfg, " lines") || strings.HasSuffix(cfg, " line") {
		linesStr := strings.Fields(cfg)[0]
		maxLines, err := strconv.Atoi(linesStr)
		if err != nil || maxLines <= 0 {
			return nil, fmt.Errorf("%w: invalid line count '%s' in truncation config '%s'", ErrTruncationFailed, linesStr, cfg)
		}

		var truncated bytes.Buffer
		scanner := bufio.NewScanner(bytes.NewReader(content))
		lineCount := 0
		for scanner.Scan() {
			lineCount++
			if lineCount > maxLines {
				break
			}
			truncated.Write(scanner.Bytes())
			truncated.WriteByte('\n') // Add newline back
		}
		if err := scanner.Err(); err != nil {
			return nil, fmt.Errorf("%w: error scanning lines during truncation: %w", ErrTruncationFailed, err)
		}
		if lineCount == 0 && len(content) > 0 { // Handle case where content might not have newlines
			return content, nil // Or should we still truncate by bytes as fallback? Keep original for now.
		}
		// Return exactly the content read up to maxLines
		return truncated.Bytes(), nil

	}

	// Assume byte-based truncation (e.g., "1MB", "500KB", "1024b" or just "1024")
	var limit int64
	var err error
	cfgLower := strings.ToLower(cfg)

	if strings.HasSuffix(cfgLower, "mb") {
		valStr := strings.TrimSuffix(cfgLower, "mb")
		val, parseErr := strconv.ParseInt(strings.TrimSpace(valStr), 10, 64)
		err = parseErr
		limit = val * 1024 * 1024
	} else if strings.HasSuffix(cfgLower, "kb") {
		valStr := strings.TrimSuffix(cfgLower, "kb")
		val, parseErr := strconv.ParseInt(strings.TrimSpace(valStr), 10, 64)
		err = parseErr
		limit = val * 1024
	} else if strings.HasSuffix(cfgLower, "b") {
		valStr := strings.TrimSuffix(cfgLower, "b")
		limit, err = strconv.ParseInt(strings.TrimSpace(valStr), 10, 64)
	} else {
		// Assume plain bytes if no unit
		limit, err = strconv.ParseInt(cfg, 10, 64)
	}

	if err != nil || limit < 0 {
		return nil, fmt.Errorf("%w: invalid byte size '%s' in truncation config", ErrTruncationFailed, cfg)
	}

	if int64(len(content)) <= limit {
		return content, nil // No truncation needed
	}
	// Return exactly limit bytes
	return content[:limit], nil
}

// appliesTo checks if a file path matches any of the provided glob patterns.
func appliesTo(filePath string, patterns []string) bool {
	if len(patterns) == 0 {
		return true // No patterns means apply to all
	}
	filePath = filepath.ToSlash(filePath) // Ensure consistent separators
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(pattern) // Ensure consistent separators
		matched, _ := filepath.Match(pattern, filePath)
		if matched {
			return true
		}
	}
	return false
}

// --- END OF FINAL REVISED FILE pkg/converter/processor.go ---

// --- START OF FINAL REVISED FILE pkg/converter/processor.go ---
package converter

import (
	"bufio" // **FIX:** Import bufio package
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json" // Keep standard json import

	// Import standard errors package
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
	// Import necessary subpackages
	"github.com/stackvity/stack-converter/pkg/converter/analysis"
	"github.com/stackvity/stack-converter/pkg/converter/encoding"
	"github.com/stackvity/stack-converter/pkg/converter/language"
	"github.com/stackvity/stack-converter/pkg/converter/template" // Imports the template package types like TemplateMetadata
	"gopkg.in/yaml.v3"
)

// --- FileProcessor ---

// FileProcessor handles the processing pipeline for a single file.
type FileProcessor struct {
	opts             *Options
	logger           *slog.Logger
	cacheManager     CacheManager // Interface remains for type definition
	langDetector     language.LanguageDetector
	encodingHandler  encoding.EncodingHandler
	analysisEngine   analysis.AnalysisEngine
	gitClient        GitClient
	pluginRunner     PluginRunner // Interface remains for type definition
	templateExecutor template.TemplateExecutor
}

// NewFileProcessor creates a new FileProcessor.
func NewFileProcessor(
	opts *Options,
	loggerHandler slog.Handler,
	cacheMgr CacheManager,
	langDet language.LanguageDetector,
	encHandler encoding.EncodingHandler,
	analysisEng analysis.AnalysisEngine,
	gitCl GitClient,
	pluginRun PluginRunner,
	tplExecutor template.TemplateExecutor,
) *FileProcessor { // minimal comment
	logger := slog.New(loggerHandler).With(slog.String("component", "processor"))
	return &FileProcessor{
		opts:             opts,
		logger:           logger,
		cacheManager:     cacheMgr,
		langDetector:     langDet,
		encodingHandler:  encHandler,
		analysisEngine:   analysisEng,
		gitClient:        gitCl,
		pluginRunner:     pluginRun,
		templateExecutor: tplExecutor,
	}
}

// ProcessFile executes the full processing pipeline for a given file path.
func (p *FileProcessor) ProcessFile(ctx context.Context, absFilePath string) (result interface{}, status Status, err error) { // minimal comment
	startTime := time.Now()
	relPath, pathErr := filepath.Rel(p.opts.InputPath, absFilePath)
	if pathErr != nil {
		errMsg := fmt.Sprintf("Failed to calculate relative path for %s: %v", absFilePath, pathErr)
		p.logger.Error(errMsg, slog.String("absolutePath", absFilePath))
		// Explicitly set status on error
		status = StatusFailed
		err = fmt.Errorf("%w: calculating relative path: %w", ErrReadFailed, pathErr)
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err // Early return with error info
	}
	relPath = filepath.ToSlash(relPath)
	logArgs := []any{slog.String("path", relPath)}

	// Defer block to log final status and update hooks
	defer func() {
		duration := time.Since(startTime)
		var message string
		finalStatus := status // Use the status set during processing

		if err != nil {
			// If an error was returned, status should already be StatusFailed
			// Ensure message reflects the error
			message = err.Error()
			if finalStatus != StatusFailed {
				p.logger.Warn("Error is set but status was not StatusFailed, correcting status", append(logArgs, slog.String("originalStatus", string(status)), slog.String("error", err.Error()))...)
				finalStatus = StatusFailed
			}
		} else if finalStatus == "" {
			// If no error and status wasn't explicitly set (e.g. skipped/cached), assume success.
			finalStatus = StatusSuccess
			message = "Successfully processed"
			p.logger.Debug("Processor finished with no error, setting final status to success", logArgs...)
		} else {
			// Status was explicitly set (Skipped or Success), generate appropriate message.
			switch finalStatus {
			case StatusSkipped:
				if si, ok := result.(SkippedInfo); ok {
					message = fmt.Sprintf("Skipped - %s: %s", si.Reason, si.Details)
				} else {
					message = "Skipped"
				}
			case StatusSuccess:
				message = "Successfully processed"
			default:
				// Should not happen if logic is correct
				message = string(finalStatus)
				p.logger.Error("Processor finished with unexpected final status", append(logArgs, slog.String("status", string(finalStatus)))...)
			}
		}

		logArgsWithStatus := append(logArgs, slog.String("status", string(finalStatus)), slog.Duration("duration", duration))
		logLevel := slog.LevelDebug
		logMsg := "File processing finished"
		if finalStatus == StatusFailed {
			logLevel = slog.LevelError
			logMsg = "File processing failed"
			if err != nil {
				logArgsWithStatus = append(logArgsWithStatus, slog.String("error", message)) // Use error message
			}
		} else if finalStatus == StatusSuccess {
			logLevel = slog.LevelInfo
		} else if finalStatus == StatusSkipped {
			logLevel = slog.LevelInfo
		}
		p.logger.Log(ctx, logLevel, logMsg, logArgsWithStatus...)

		// Send final status update via hook
		if p.opts.EventHooks != nil {
			if hookErr := p.opts.EventHooks.OnFileStatusUpdate(relPath, finalStatus, message, duration); hookErr != nil {
				p.logger.Warn("Event hook OnFileStatusUpdate (Final) failed", append(logArgsWithStatus, slog.String("hookError", hookErr.Error()))...)
			}
		}
	}()

	// Send initial processing status update
	if p.opts.EventHooks != nil {
		if hookErr := p.opts.EventHooks.OnFileStatusUpdate(relPath, StatusProcessing, "", 0); hookErr != nil {
			p.logger.Warn("Event hook OnFileStatusUpdate (Processing) failed", append(logArgs, slog.String("error", hookErr.Error()))...)
		}
	}

	// 1. Check Context Cancellation Early
	select {
	case <-ctx.Done():
		p.logger.Debug("Processing cancelled before start", logArgs...)
		status = StatusFailed // Set status
		err = ctx.Err()
		return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
	default:
	}

	// 2. Get File Info (Stat)
	fileInfo, statErr := os.Stat(absFilePath)
	if statErr != nil {
		errMsg := fmt.Sprintf("Failed to stat file: %v", statErr)
		err = fmt.Errorf("%w: %w", ErrStatFailed, statErr)
		status = StatusFailed // Set status
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}
	modTime := fileInfo.ModTime()
	fileSize := fileInfo.Size()

	// 3. Calculate Config Hash
	_, cfgHashErr := p.CalculateConfigHash() // Calculate but ignore value
	if cfgHashErr != nil {
		errMsg := fmt.Sprintf("Failed to calculate config hash: %v", cfgHashErr)
		err = fmt.Errorf("%w: %w", ErrConfigHashCalculation, cfgHashErr)
		status = StatusFailed              // Set status
		p.logger.Error(errMsg, logArgs...) // Log before returning
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err
	}

	var sourceContentBytes []byte
	cacheStatus := CacheStatusDisabled // Default status since cache logic is excluded
	var currentSourceHash string

	// 4. Check Cache (EXCLUDED LOGIC)
	p.logger.Debug("Cache check EXCLUDED based on prompt constraints.", logArgs...)
	cacheStatus = CacheStatusMiss // Treat as miss because check logic is removed

	// 5. Read File Content
	var readErr error
	sourceContentBytes, readErr = os.ReadFile(absFilePath)
	if readErr != nil {
		errMsg := fmt.Sprintf("Failed to read file: %v", readErr)
		err = fmt.Errorf("%w: %w", ErrReadFailed, readErr)
		status = StatusFailed // Set status
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}

	// Calculate source hash
	currentSourceHash = fmt.Sprintf("%x", sha256.Sum256(sourceContentBytes))

	// 6. Handle Encoding
	var utf8ContentBytes []byte
	detectedEncoding := "unknown"
	certainty := false
	var encodingErr error
	if p.encodingHandler != nil {
		utf8ContentBytes, detectedEncoding, certainty, encodingErr = p.encodingHandler.DetectAndDecode(sourceContentBytes)
		if encodingErr != nil {
			p.logger.Warn("Encoding conversion failed, proceeding with potentially corrupt data", append(logArgs, slog.String("detectedEncoding", detectedEncoding), slog.String("error", encodingErr.Error()))...)
			utf8ContentBytes = sourceContentBytes // Fallback to original bytes on error
			// Do not fail the file processing, just log the warning
		} else if !certainty {
			logMsg := "Encoding detection uncertain, using fallback/guess"
			if detectedEncoding != "" && detectedEncoding != "unknown" { // Be more specific
				logMsg = fmt.Sprintf("Encoding detection uncertain, proceeding with guessed encoding '%s'", detectedEncoding)
			}
			p.logger.Warn(logMsg, logArgs...)
		}
	} else {
		p.logger.Warn("Encoding handler not available, assuming UTF-8", logArgs...)
		utf8ContentBytes = sourceContentBytes
		detectedEncoding = "utf-8" // Assumption
		certainty = false
	}
	p.logger.Debug("Encoding handled", append(logArgs, slog.String("finalEncoding", detectedEncoding), slog.Bool("certain", certainty))...)
	textContent := string(utf8ContentBytes)

	// Check context cancellation
	select {
	case <-ctx.Done():
		p.logger.Debug("Processing cancelled after encoding", logArgs...)
		status = StatusFailed // Set status
		err = ctx.Err()
		return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
	default:
	}

	// 7. Detect Binary File
	isBinary := false
	if p.encodingHandler != nil {
		isBinary = p.encodingHandler.IsBinary(utf8ContentBytes) // Use potentially decoded bytes
	} else {
		p.logger.Warn("Encoding handler not available, cannot perform binary detection", logArgs...)
	}
	if isBinary {
		switch p.opts.BinaryMode {
		case BinarySkip:
			p.logger.Debug("Skipping binary file", logArgs...)
			status = StatusSkipped // Set status
			result = SkippedInfo{Path: relPath, Reason: SkipReasonBinary, Details: "Binary file detected"}
			return result, status, nil // Successful skip
		case BinaryPlaceholder:
			p.logger.Debug("Generating placeholder for binary file", logArgs...)
			textContent = fmt.Sprintf("[Binary content skipped: %s]", filepath.Base(relPath))
			// Continue processing with placeholder
		case BinaryError:
			err = ErrBinaryFile
			status = StatusFailed // Set status
			return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		default:
			p.logger.Error("Invalid binaryMode setting encountered during processing", append(logArgs, slog.String("mode", string(p.opts.BinaryMode)))...)
			err = fmt.Errorf("%w: invalid binaryMode '%s'", ErrConfigValidation, p.opts.BinaryMode)
			status = StatusFailed // Set status
			return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
		}
	}

	// 8. Detect Large File
	isLarge := fileSize > p.opts.LargeFileThreshold
	truncated := false
	if isLarge && !isBinary { // Do not apply large file logic to binaries
		switch p.opts.LargeFileMode {
		case LargeFileSkip:
			details := fmt.Sprintf("Size %d bytes > threshold %d bytes", fileSize, p.opts.LargeFileThreshold)
			p.logger.Debug("Skipping large file", append(logArgs, slog.String("details", details))...)
			status = StatusSkipped // Set status
			result = SkippedInfo{Path: relPath, Reason: SkipReasonLarge, Details: details}
			return result, status, nil // Successful skip
		case LargeFileTruncate:
			p.logger.Debug("Truncating large file", append(logArgs, slog.Int64("size", fileSize), slog.String("config", p.opts.LargeFileTruncateCfg))...)
			truncatedBytes, errTrunc := truncateContent(utf8ContentBytes, p.opts.LargeFileTruncateCfg)
			if errTrunc != nil {
				errMsg := fmt.Sprintf("Failed to truncate file: %v", errTrunc)
				err = fmt.Errorf("%w: %w", ErrTruncationFailed, errTrunc)
				status = StatusFailed // Set status
				return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
			}
			textContent = string(truncatedBytes) + "\n\n[Content truncated due to file size limit]"
			truncated = true
			// Continue processing with truncated content
		case LargeFileError:
			errMsg := fmt.Sprintf("Large file encountered (size %d bytes > threshold %d bytes)", fileSize, p.opts.LargeFileThreshold)
			err = ErrLargeFile
			status = StatusFailed // Set status
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		default:
			p.logger.Error("Invalid largeFileMode setting encountered during processing", append(logArgs, slog.String("mode", string(p.opts.LargeFileMode)))...)
			err = fmt.Errorf("%w: invalid largeFileMode '%s'", ErrConfigValidation, p.opts.LargeFileMode)
			status = StatusFailed // Set status
			return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
		}
	}

	// Check cancellation
	select {
	case <-ctx.Done():
		p.logger.Debug("Processing cancelled before language detection", logArgs...)
		status = StatusFailed // Set status
		err = ctx.Err()
		return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
	default:
	}

	// 9. Detect Language
	languageName := "unknown"
	confidence := 0.0
	if p.langDetector != nil {
		var langErr error
		// Use potentially truncated textContent
		languageName, confidence, langErr = p.langDetector.Detect([]byte(textContent), relPath)
		if langErr != nil {
			// Log warning, do not fail processing
			p.logger.Warn("Language detection failed", append(logArgs, slog.String("error", langErr.Error()))...)
			languageName = "unknown" // Fallback on error
			confidence = 0.0
		}
	} else {
		p.logger.Warn("Language detector not available, defaulting to 'unknown'", logArgs...)
	}
	p.logger.Debug("Language detected", append(logArgs, slog.String("language", languageName), slog.Float64("confidence", confidence))...)

	// 10. Prepare Metadata Struct
	var extractedComments *string // Initialize as nil pointer
	var gitInfo *template.GitInfo // Initialize as nil pointer
	metadata := template.TemplateMetadata{
		FilePath:           relPath,
		FileName:           filepath.Base(relPath),
		OutputPath:         generateOutputPath(relPath),
		Content:            textContent, // Use potentially modified content
		DetectedLanguage:   languageName,
		LanguageConfidence: confidence,
		SizeBytes:          fileSize,
		ModTime:            modTime,
		ContentHash:        currentSourceHash, // Hash of ORIGINAL content
		IsBinary:           isBinary,
		IsLarge:            isLarge,
		Truncated:          truncated,
		ExtractedComments:  extractedComments,
		GitInfo:            gitInfo,
		FrontMatter:        make(map[string]interface{}), // Initialize empty map
	}

	// 11. Extract Comments (Optional)
	if p.opts.AnalysisOptions.ExtractComments && p.analysisEngine != nil && !isBinary {
		// Use current metadata.Content (potentially truncated)
		commentsResult, analysisErr := p.analysisEngine.ExtractDocComments([]byte(metadata.Content), metadata.DetectedLanguage, p.opts.AnalysisOptions.CommentStyles)
		if analysisErr != nil {
			// Log warning, do not fail file processing
			p.logger.Warn("Comment extraction failed", append(logArgs, slog.String("error", analysisErr.Error()))...)
		}
		// Assign only if comments were actually found
		if commentsResult != "" {
			extractedCommentsValue := commentsResult // Create variable to take address of
			metadata.ExtractedComments = &extractedCommentsValue
			p.logger.Debug("Comments extracted", logArgs...)
		}
	}

	// 12. Fetch Git Metadata (Optional)
	if p.opts.GitMetadataEnabled && p.gitClient != nil {
		gitMetaMap, gitErr := p.gitClient.GetFileMetadata(p.opts.InputPath, absFilePath)
		if gitErr != nil {
			// Log warning, do not fail file processing
			p.logger.Warn("Git metadata fetch failed", append(logArgs, slog.String("error", gitErr.Error()))...)
		} else if len(gitMetaMap) > 0 {
			// Populate struct only if data is returned
			gitInfoData := template.GitInfo{
				Commit:      gitMetaMap["commit"],
				Author:      gitMetaMap["author"],
				AuthorEmail: gitMetaMap["authorEmail"],
				DateISO:     gitMetaMap["dateISO"],
			}
			metadata.GitInfo = &gitInfoData // Assign pointer
			p.logger.Debug("Git metadata fetched", logArgs...)
		}
	}

	outputContent := metadata.Content // Content before plugins/templating
	var pluginsRun []string           // Initialize empty, will remain empty

	// 13. Run Preprocessor Plugins (EXCLUDED LOGIC)
	p.logger.Debug("Preprocessor plugin execution EXCLUDED based on prompt constraints.", logArgs...)

	// 14. Generate Front Matter (Optional)
	var frontMatterBlock string
	if p.opts.FrontMatterConfig.Enabled {
		// Convert current metadata state to map for front matter data source
		pluginMetadataMapForFM := convertMetaToMap(&metadata)
		if pluginMetadataMapForFM == nil {
			errMsg := "Internal error converting metadata for front matter generation"
			err = ErrMetadataConversion
			status = StatusFailed // Set status
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err
		}
		fmData := make(map[string]interface{})
		// Copy static fields
		for k, v := range p.opts.FrontMatterConfig.Static {
			fmData[k] = v
		}
		// Include dynamic fields, potentially overwriting static
		for _, key := range p.opts.FrontMatterConfig.Include {
			if val, ok := pluginMetadataMapForFM[key]; ok {
				fmData[key] = val
			} else {
				p.logger.Warn("Front matter include key not found in metadata", append(logArgs, slog.String("key", key))...)
			}
		}
		metadata.FrontMatter = fmData // Update metadata struct with final FM data

		// Generate the actual block
		fmBytes, fmErr := generateFrontMatter(fmData, p.opts.FrontMatterConfig.Format)
		if fmErr != nil {
			errMsg := fmt.Sprintf("Failed to generate front matter: %v", fmErr)
			err = fmt.Errorf("%w: %w", ErrFrontMatterGen, fmErr)
			status = StatusFailed // Set status
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		}
		frontMatterBlock = string(fmBytes)
		p.logger.Debug("Front matter generated", logArgs...)
	}

	// Update metadata Content field *just before* templating to reflect pre-plugin state
	// (though pre-plugins are excluded here, this ensures correct data if they were present)
	metadata.Content = outputContent

	// Check cancellation
	select {
	case <-ctx.Done():
		p.logger.Debug("Processing cancelled before template execution", logArgs...)
		status = StatusFailed // Set status
		err = ctx.Err()
		return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
	default:
	}

	// 15. Execute Template
	var templateOutput bytes.Buffer
	if p.templateExecutor == nil {
		errMsg := "Internal error: TemplateExecutor is nil"
		err = ErrConfigValidation
		status = StatusFailed // Set status
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err
	}
	// Execute using the prepared metadata
	if templateErr := p.templateExecutor.Execute(&templateOutput, p.opts.Template, &metadata); templateErr != nil {
		errMsg := fmt.Sprintf("Template execution failed: %v", templateErr)
		err = fmt.Errorf("%w: %w", ErrTemplateExecution, templateErr)
		status = StatusFailed // Set status
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}
	outputContent = templateOutput.String() // outputContent now holds templated result
	p.logger.Debug("Template executed", logArgs...)

	// 16. Run Postprocessor Plugins (EXCLUDED LOGIC)
	p.logger.Debug("Postprocessor plugin execution EXCLUDED based on prompt constraints.", logArgs...)

	// 17. Run Formatter Plugins (EXCLUDED LOGIC)
	// FIX: Remove the unused variable declaration (formatterProvidedOutput) as it is always false due to excluded logic
	// formatterProvidedOutput := false
	p.logger.Debug("Formatter plugin execution EXCLUDED based on prompt constraints.", logArgs...)

	// 18. Prepare Final Content & Output Path
	finalContent := frontMatterBlock + outputContent // Combine potential FM and templated content

	outputRelPath := metadata.OutputPath
	if outputRelPath == "" {
		errMsg := fmt.Sprintf("Internal error: Generated empty output path for %s", relPath)
		err = fmt.Errorf("%w: %s", ErrWriteFailed, errMsg)
		status = StatusFailed // Set status
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}
	absOutputPath := filepath.Join(p.opts.OutputPath, outputRelPath)

	// 19. Ensure Output Directory Exists
	outputDir := filepath.Dir(absOutputPath)
	if mkdirErr := os.MkdirAll(outputDir, 0755); mkdirErr != nil {
		errMsg := fmt.Sprintf("Failed to create output directory %s: %v", outputDir, mkdirErr)
		err = fmt.Errorf("%w: %w", ErrMkdirFailed, mkdirErr)
		status = StatusFailed // Set status
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}

	// 20. Write Output File
	outputContentBytes := []byte(finalContent)
	if writeErr := os.WriteFile(absOutputPath, outputContentBytes, 0644); writeErr != nil {
		errMsg := fmt.Sprintf("Failed to write output file %s: %v", absOutputPath, writeErr)
		err = fmt.Errorf("%w: %w", ErrWriteFailed, writeErr)
		status = StatusFailed // Set status
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}
	// outputContentHash calculation removed (only needed for cache update)
	p.logger.Debug("Output file written", append(logArgs, slog.String("outputPath", outputRelPath))...)

	// 21. Update Cache (EXCLUDED LOGIC)
	p.logger.Debug("Cache update EXCLUDED based on prompt constraints.", logArgs...)

	// 22. Success
	status = StatusSuccess // Explicitly set success status
	result = FileInfo{
		Path:               relPath,
		OutputPath:         outputRelPath,
		Language:           metadata.DetectedLanguage,
		LanguageConfidence: metadata.LanguageConfidence,
		SizeBytes:          fileSize,
		ModTime:            modTime,
		CacheStatus:        cacheStatus, // Reflects actual status (miss/disabled)
		DurationMs:         time.Since(startTime).Milliseconds(),
		ExtractedComments:  metadata.ExtractedComments != nil,
		FrontMatter:        frontMatterBlock != "",
		PluginsRun:         pluginsRun, // Will be empty
	}
	return result, status, nil // Success: nil error
}

// CalculateConfigHash generates a stable hash representing relevant configuration.
func (p *FileProcessor) CalculateConfigHash() (string, error) { // minimal comment
	hasher := sha256.New()
	configSubset := make(map[string]interface{})

	// 1. Template Hash
	templateHash := "default"
	// FIX: Check standard template definition for default name comparison
	if p.opts.Template != nil && p.opts.Template.Name() != defaultTemplateName {
		if p.opts.TemplatePath != "" {
			tplBytes, err := os.ReadFile(p.opts.TemplatePath)
			if err != nil {
				p.logger.Error("Failed to read custom template file for hashing", slog.String("path", p.opts.TemplatePath), slog.String("error", err.Error()))
				return "", fmt.Errorf("%w: failed to read template file '%s' for hashing: %w", ErrConfigHashCalculation, p.opts.TemplatePath, err)
			}
			templateHash = fmt.Sprintf("sha256:%x", sha256.Sum256(tplBytes))
		} else {
			p.logger.Warn("Custom template provided without TemplatePath, using template name for hash", slog.String("templateName", p.opts.Template.Name()))
			templateHash = "custom_template_name:" + p.opts.Template.Name()
		}
	}
	configSubset["templateHash"] = templateHash

	// 2. FrontMatter Config
	sortedInclude := make([]string, len(p.opts.FrontMatterConfig.Include))
	copy(sortedInclude, p.opts.FrontMatterConfig.Include)
	sort.Strings(sortedInclude)
	staticFMBytes, errMarshalStatic := json.Marshal(p.opts.FrontMatterConfig.Static)
	if errMarshalStatic != nil {
		p.logger.Warn("Failed to marshal static front matter for hashing, using empty map", slog.Any("error", errMarshalStatic))
		staticFMBytes = []byte("{}")
	}
	configSubset["frontMatter"] = map[string]interface{}{
		"enabled": p.opts.FrontMatterConfig.Enabled,
		"format":  p.opts.FrontMatterConfig.Format,
		"static":  string(staticFMBytes),
		"include": sortedInclude,
	}

	// 3. Analysis Config
	sortedStyles := make([]string, len(p.opts.AnalysisOptions.CommentStyles))
	copy(sortedStyles, p.opts.AnalysisOptions.CommentStyles)
	sort.Strings(sortedStyles)
	configSubset["analysis"] = map[string]interface{}{
		"extractComments": p.opts.AnalysisOptions.ExtractComments,
		"commentStyles":   sortedStyles,
	}

	// 4. Enabled Plugin Config (EXCLUDED)
	configSubset["enabledPlugins"] = []string{} // Represent as empty list

	// 5. Other Relevant Options
	configSubset["binaryMode"] = p.opts.BinaryMode
	configSubset["largeFileMode"] = p.opts.LargeFileMode
	configSubset["largeFileTruncateCfg"] = p.opts.LargeFileTruncateCfg
	configSubset["defaultEncoding"] = p.opts.DefaultEncoding
	langMapBytes, errMarshalLangMap := json.Marshal(p.opts.LanguageMappingsOverride)
	if errMarshalLangMap != nil {
		p.logger.Warn("Failed to marshal language mappings for hashing, using empty map", slog.Any("error", errMarshalLangMap))
		langMapBytes = []byte("{}")
	}
	configSubset["languageMappingsOverride"] = string(langMapBytes)
	configSubset["languageDetectionConfidenceThreshold"] = p.opts.LanguageDetectionConfidenceThreshold

	// 6. Marshal the subset using JSON (keys are sorted by default by encoding/json)
	configBytes, errMarshalConfig := json.Marshal(configSubset)
	if errMarshalConfig != nil {
		return "", fmt.Errorf("%w: failed to marshal config subset for hashing: %w", ErrConfigHashCalculation, errMarshalConfig)
	}

	// 7. Hash the resulting JSON bytes
	hasher.Write(configBytes)
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// generateOutputPath determines the output path for a source file.
func generateOutputPath(relPath string) string { // minimal comment
	if relPath == "." || relPath == "" {
		return ""
	}
	ext := filepath.Ext(relPath)
	base := strings.TrimSuffix(relPath, ext)

	if base == "" && ext != "" { // Handle files like ".bashrc"
		return relPath + ".md"
	}
	if base == "." { // Handle input "."
		return relPath + ".md"
	}
	if strings.HasSuffix(base, ".md") { // Avoid double extension
		return base
	}
	return base + ".md"
}

// convertMetaToMap converts TemplateMetadata struct to map for plugins/frontmatter.
func convertMetaToMap(meta *template.TemplateMetadata) map[string]interface{} { // minimal comment
	m := make(map[string]interface{})
	v := reflect.ValueOf(*meta)
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		fieldName := t.Field(i).Name
		fieldValue := v.Field(i)

		if !t.Field(i).IsExported() || fieldName == "Options" {
			continue
		}

		fieldInterface := fieldValue.Interface()

		if tm, ok := fieldInterface.(time.Time); ok {
			m[fieldName] = tm.Format(time.RFC3339)
			continue
		}

		if fieldValue.Kind() == reflect.Ptr {
			if fieldValue.IsNil() {
				m[fieldName] = nil
			} else {
				// Directly use the element's interface value
				m[fieldName] = fieldValue.Elem().Interface()
			}
			continue
		}

		if fieldValue.Kind() == reflect.Map {
			if fieldValue.IsNil() {
				m[fieldName] = make(map[string]interface{})
			} else {
				// Assume map[string]interface{} type for FrontMatter
				m[fieldName] = fieldInterface
			}
			continue
		}

		m[fieldName] = fieldInterface
	}

	delete(m, "Content") // Content passed separately to plugins

	return m
}

// PluginRunResult holds results from running plugins for a stage.
type PluginRunResult struct {
	Content       string                 // Updated content after plugins ran
	Metadata      map[string]interface{} // Updated metadata after plugins ran (merged)
	PluginsRun    []string               // Names of plugins that actually ran and succeeded
	IsFinalOutput bool                   // True if a formatter provided final 'output'
	Output        string                 // The final output from a formatter
	FinalPlugin   string                 // Name of the formatter plugin that provided final output
}

// generateFrontMatter marshals data into YAML or TOML format including delimiters.
func generateFrontMatter(data map[string]interface{}, format string) ([]byte, error) { // minimal comment
	var buf bytes.Buffer
	var marshaledBytes []byte
	var err error

	if data == nil {
		data = make(map[string]interface{})
	}

	format = strings.ToLower(format)

	switch format {
	case "yaml":
		marshaledBytes, err = yaml.Marshal(data)
		if err == nil {
			buf.WriteString("---\n")
			buf.Write(marshaledBytes)
			if len(marshaledBytes) > 0 && !bytes.HasSuffix(marshaledBytes, []byte("\n")) {
				buf.WriteString("\n")
			}
			buf.WriteString("---\n")
		}
	case "toml":
		var tomlBuf bytes.Buffer
		encoder := toml.NewEncoder(&tomlBuf)
		err = encoder.Encode(data)
		if err == nil {
			marshaledBytes = tomlBuf.Bytes()
			buf.WriteString("+++\n")
			buf.Write(marshaledBytes)
			if len(marshaledBytes) > 0 && !bytes.HasSuffix(marshaledBytes, []byte("\n")) {
				buf.WriteString("\n")
			}
			buf.WriteString("+++\n")
		}
	default:
		err = fmt.Errorf("%w: unsupported front matter format: %s", ErrConfigValidation, format)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: failed to marshal front matter to %s: %w", ErrFrontMatterGen, format, err)
	}

	return buf.Bytes(), nil
}

// truncateContent reduces content size based on config string (bytes or lines).
func truncateContent(content []byte, cfg string) ([]byte, error) { // minimal comment
	cfg = strings.TrimSpace(strings.ToLower(cfg))
	var limit int64 = -1
	isLineLimit := false

	// Parse Line Limit
	if strings.HasSuffix(cfg, " lines") || strings.HasSuffix(cfg, " line") {
		lineStr := strings.Fields(cfg)[0]
		lineLimit, parseErr := strconv.ParseInt(lineStr, 10, 64)
		if parseErr != nil || lineLimit < 0 {
			return nil, fmt.Errorf("%w: invalid line limit format in truncate config '%s': %w", ErrConfigValidation, cfg, parseErr)
		}
		limit = lineLimit
		isLineLimit = true
	} else { // Parse Byte Limit
		sizeStr := cfg
		multiplier := int64(1)
		units := []struct {
			suffix string
			mult   int64
		}{
			{"gb", 1 << 30}, {"g", 1 << 30},
			{"mb", 1 << 20}, {"m", 1 << 20},
			{"kb", 1 << 10}, {"k", 1 << 10},
			{"b", 1},
		}
		numericPart := sizeStr
		unitFound := false
		for _, unit := range units {
			if strings.HasSuffix(sizeStr, unit.suffix) {
				numericPart = strings.TrimSuffix(sizeStr, unit.suffix)
				multiplier = unit.mult
				unitFound = true
				break
			}
		}
		if !unitFound {
			// Check if it's just a number (assume bytes)
			if _, parseErr := strconv.ParseInt(numericPart, 10, 64); parseErr != nil {
				return nil, fmt.Errorf("%w: invalid size format in truncate config '%s'", ErrConfigValidation, cfg)
			}
			multiplier = 1 // Assume bytes for plain number
		}
		numericPart = strings.TrimSpace(numericPart)
		byteLimit, parseErr := strconv.ParseInt(numericPart, 10, 64)
		if parseErr != nil || byteLimit < 0 {
			return nil, fmt.Errorf("%w: invalid numeric value in truncate size config '%s': %w", ErrConfigValidation, cfg, parseErr)
		}
		limit = byteLimit * multiplier
	}

	// Apply Truncation
	if isLineLimit {
		if limit == 0 {
			return []byte{}, nil
		}
		lineCount := int64(0)
		endIndex := -1
		// **FIX:** Use bufio.NewScanner with bytes.NewReader
		scanner := bufio.NewScanner(bytes.NewReader(content))
		var currentPos int
		for scanner.Scan() {
			lineCount++
			// Track position manually after each Scan
			currentPos += len(scanner.Bytes()) + 1 // Add 1 for the newline character
			if lineCount == limit {
				// Adjust position back by 1 if we added a newline
				// that wasn't actually there (e.g., last line without newline)
				if currentPos > len(content) {
					endIndex = len(content)
				} else {
					endIndex = currentPos - 1 // Position before the newline
				}
				break
			}
		}
		if err := scanner.Err(); err != nil {
			// Handle potential scanning errors
			return nil, fmt.Errorf("%w: error scanning content for line truncation: %w", ErrTruncationFailed, err)
		}

		if endIndex == -1 { // Fewer lines than limit
			return content, nil
		}
		// Slice up to the calculated end index
		// Ensure endIndex is within bounds
		if endIndex > len(content) {
			endIndex = len(content)
		} else if endIndex < 0 {
			endIndex = 0 // Should not happen if limit > 0
		}
		return content[:endIndex], nil

	} else { // Byte limit
		byteLimit := limit
		contentLen := int64(len(content))
		if contentLen <= byteLimit {
			return content, nil
		}
		if byteLimit == 0 {
			return []byte{}, nil
		}
		finalLimit := byteLimit
		// Ensure we don't cut in the middle of a multi-byte UTF-8 character
		for finalLimit > 0 && (content[finalLimit]&0xC0) == 0x80 {
			finalLimit--
		}
		// Handle case where finalLimit becomes 0 (all continuation bytes up to limit)
		// This ensures we don't create an invalid slice [:0] unless byteLimit was 0.
		if finalLimit < 0 { // Should technically not happen if byteLimit > 0
			finalLimit = 0
		}
		return content[:finalLimit], nil
	}
}

// FIX: Add constant definition required by CalculateConfigHash
const defaultTemplateName = "default"

// --- END OF FINAL REVISED FILE pkg/converter/processor.go ---

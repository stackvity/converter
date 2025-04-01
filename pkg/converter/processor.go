// --- START OF FINAL REVISED FILE pkg/converter/processor.go ---
package converter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json" // Import standard errors package
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
	cacheManager     CacheManager
	langDetector     language.LanguageDetector
	encodingHandler  encoding.EncodingHandler
	analysisEngine   analysis.AnalysisEngine
	gitClient        GitClient
	pluginRunner     PluginRunner
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
		return nil, StatusFailed, fmt.Errorf("%w: calculating relative path: %w", ErrReadFailed, pathErr)
	}
	relPath = filepath.ToSlash(relPath)
	logArgs := []any{slog.String("path", relPath)}

	// Defer block to log final status and update hooks
	defer func() {
		duration := time.Since(startTime)
		var message string
		finalStatus := status

		if err != nil {
			finalStatus = StatusFailed
			message = err.Error()
		} else if status == "" {
			finalStatus = StatusSuccess
			message = "Successfully processed"
			p.logger.Debug("Processor finished with no error, assuming success", logArgs...)
		} else {
			switch status {
			case StatusCached:
				message = "Retrieved from cache"
			case StatusSkipped:
				if si, ok := result.(SkippedInfo); ok {
					message = fmt.Sprintf("Skipped - %s: %s", si.Reason, si.Details)
				} else {
					message = "Skipped"
				}
			case StatusSuccess:
				message = "Successfully processed"
			default:
				message = string(status)
			}
		}

		logArgsWithStatus := append(logArgs, slog.String("status", string(finalStatus)), slog.Duration("duration", duration))
		logLevel := slog.LevelDebug
		logMsg := "File processing finished"
		if finalStatus == StatusFailed {
			logLevel = slog.LevelError
			logMsg = "File processing failed"
			if err != nil {
				logArgsWithStatus = append(logArgsWithStatus, slog.String("error", err.Error()))
			}
		} else if finalStatus == StatusSuccess {
			logLevel = slog.LevelInfo
		}
		p.logger.Log(ctx, logLevel, logMsg, logArgsWithStatus...)

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
		status = StatusFailed
		err = ctx.Err()
		return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
	default:
	}

	// 2. Get File Info (Stat)
	fileInfo, statErr := os.Stat(absFilePath)
	if statErr != nil {
		errMsg := fmt.Sprintf("Failed to stat file: %v", statErr)
		err = fmt.Errorf("%w: %w", ErrStatFailed, statErr)
		status = StatusFailed
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}
	modTime := fileInfo.ModTime()
	fileSize := fileInfo.Size()

	// 3. Calculate Config Hash
	configHash, cfgHashErr := p.CalculateConfigHash()
	if cfgHashErr != nil {
		errMsg := fmt.Sprintf("Failed to calculate config hash: %v", cfgHashErr)
		err = fmt.Errorf("%w: %w", ErrConfigHashCalculation, cfgHashErr)
		p.logger.Error(errMsg, logArgs...)
		status = StatusFailed
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err
	}

	var sourceContentBytes []byte

	// 4. Check Cache
	cacheStatus := CacheStatusDisabled
	var currentSourceHash string
	if p.opts.CacheEnabled && p.cacheManager != nil {
		cacheStatus = CacheStatusMiss
		if !p.opts.IgnoreCacheRead {
			sourceContentBytesForCache, readErrForCache := os.ReadFile(absFilePath)
			if readErrForCache != nil {
				p.logger.Warn("Failed to read file for cache check, treating as miss", append(logArgs, slog.String("error", readErrForCache.Error()))...)
			} else {
				currentSourceHash = fmt.Sprintf("%x", sha256.Sum256(sourceContentBytesForCache))
				isHit, _ := p.cacheManager.Check(relPath, modTime, currentSourceHash, configHash)
				if isHit {
					p.logger.Debug("Cache hit", logArgs...)
					cacheStatus = CacheStatusHit
					status = StatusCached
					result = FileInfo{
						Path:        relPath,
						SizeBytes:   fileSize,
						ModTime:     modTime,
						CacheStatus: cacheStatus,
					}
					return result, status, nil
				}
				p.logger.Debug("Cache miss", logArgs...)
				sourceContentBytes = sourceContentBytesForCache
			}
		} else {
			cacheStatus = CacheStatusDisabled
			p.logger.Debug("Cache read ignored", logArgs...)
		}
	}

	// 5. Read File Content (if not already read)
	if len(sourceContentBytes) == 0 {
		var readErr error
		sourceContentBytes, readErr = os.ReadFile(absFilePath)
		if readErr != nil {
			errMsg := fmt.Sprintf("Failed to read file: %v", readErr)
			err = fmt.Errorf("%w: %w", ErrReadFailed, readErr)
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		}
	}

	// Calculate source hash if needed
	if currentSourceHash == "" && p.opts.CacheEnabled {
		currentSourceHash = fmt.Sprintf("%x", sha256.Sum256(sourceContentBytes))
	}

	// 6. Handle Encoding
	var utf8ContentBytes []byte
	detectedEncoding := "unknown"
	certainty := false
	var encodingErr error
	if p.encodingHandler != nil {
		utf8ContentBytes, detectedEncoding, certainty, encodingErr = p.encodingHandler.DetectAndDecode(sourceContentBytes)
		if encodingErr != nil {
			p.logger.Warn("Encoding conversion failed", append(logArgs, slog.String("error", encodingErr.Error()))...)
		} else if !certainty {
			logMsg := "Encoding detection uncertain, using fallback/guess"
			if detectedEncoding != "" {
				logMsg = fmt.Sprintf("Encoding detection uncertain, using guessed encoding '%s'", detectedEncoding)
			}
			p.logger.Warn(logMsg, logArgs...)
		}
	} else {
		p.logger.Warn("Encoding handler not available, assuming UTF-8", logArgs...)
		utf8ContentBytes = sourceContentBytes
		detectedEncoding = "utf-8"
		certainty = false
	}
	p.logger.Debug("Encoding handled", append(logArgs, slog.String("finalEncoding", detectedEncoding), slog.Bool("certain", certainty))...)
	textContent := string(utf8ContentBytes)

	// Check context cancellation
	select {
	case <-ctx.Done():
		p.logger.Debug("Processing cancelled after encoding", logArgs...)
		status = StatusFailed
		err = ctx.Err()
		return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
	default:
	}

	// 7. Detect Binary File
	isBinary := false
	if p.encodingHandler != nil {
		isBinary = p.encodingHandler.IsBinary(utf8ContentBytes)
	} else {
		p.logger.Warn("Encoding handler not available, cannot perform binary detection", logArgs...)
	}
	if isBinary {
		switch p.opts.BinaryMode {
		case BinarySkip:
			p.logger.Debug("Skipping binary file", logArgs...)
			status = StatusSkipped
			result = SkippedInfo{Path: relPath, Reason: SkipReasonBinary, Details: "Binary file detected"}
			return result, status, nil
		case BinaryPlaceholder:
			p.logger.Debug("Generating placeholder for binary file", logArgs...)
			textContent = fmt.Sprintf("[Binary content skipped: %s]", filepath.Base(relPath))
		case BinaryError:
			err = ErrBinaryFile
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		default:
			p.logger.Error("Invalid binaryMode setting encountered", append(logArgs, slog.String("mode", string(p.opts.BinaryMode)))...)
			err = fmt.Errorf("%w: invalid binaryMode '%s'", ErrConfigValidation, p.opts.BinaryMode)
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
		}
	}

	// 8. Detect Large File
	isLarge := fileSize > p.opts.LargeFileThreshold
	truncated := false
	if isLarge && !isBinary {
		switch p.opts.LargeFileMode {
		case LargeFileSkip:
			details := fmt.Sprintf("Size %d bytes > threshold %d bytes", fileSize, p.opts.LargeFileThreshold)
			p.logger.Debug("Skipping large file", append(logArgs, slog.String("details", details))...)
			status = StatusSkipped
			result = SkippedInfo{Path: relPath, Reason: SkipReasonLarge, Details: details}
			return result, status, nil
		case LargeFileTruncate:
			p.logger.Debug("Truncating large file", append(logArgs, slog.Int64("size", fileSize), slog.String("config", p.opts.LargeFileTruncateCfg))...)
			truncatedBytes, errTrunc := truncateContent(utf8ContentBytes, p.opts.LargeFileTruncateCfg)
			if errTrunc != nil {
				errMsg := fmt.Sprintf("Failed to truncate file: %v", errTrunc)
				err = fmt.Errorf("%w: %w", ErrTruncationFailed, errTrunc)
				status = StatusFailed
				return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
			}
			textContent = string(truncatedBytes) + "\n\n[Content truncated due to file size limit]"
			truncated = true
		case LargeFileError:
			errMsg := fmt.Sprintf("Large file encountered (size %d bytes > threshold %d bytes)", fileSize, p.opts.LargeFileThreshold)
			err = ErrLargeFile
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		default:
			p.logger.Error("Invalid largeFileMode setting encountered", append(logArgs, slog.String("mode", string(p.opts.LargeFileMode)))...)
			err = fmt.Errorf("%w: invalid largeFileMode '%s'", ErrConfigValidation, p.opts.LargeFileMode)
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
		}
	}

	// Check cancellation
	select {
	case <-ctx.Done():
		p.logger.Debug("Processing cancelled before language detection", logArgs...)
		status = StatusFailed
		err = ctx.Err()
		return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
	default:
	}

	// 9. Detect Language
	languageName := "unknown"
	confidence := 0.0
	if p.langDetector != nil {
		var langErr error
		languageName, confidence, langErr = p.langDetector.Detect([]byte(textContent), relPath)
		if langErr != nil {
			p.logger.Warn("Language detection failed", append(logArgs, slog.String("error", langErr.Error()))...)
		}
	} else {
		p.logger.Warn("Language detector not available, defaulting to 'unknown'", logArgs...)
	}
	p.logger.Debug("Language detected", append(logArgs, slog.String("language", languageName), slog.Float64("confidence", confidence))...)

	// 10. Prepare Metadata Struct
	var extractedComments *string
	var gitInfo *template.GitInfo
	metadata := template.TemplateMetadata{
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
		ExtractedComments:  extractedComments,
		GitInfo:            gitInfo,
		FrontMatter:        make(map[string]interface{}),
	}

	// 11. Extract Comments (Optional)
	extractedCommentsValue := ""
	if p.opts.AnalysisOptions.ExtractComments && p.analysisEngine != nil && !isBinary {
		commentsResult, analysisErr := p.analysisEngine.ExtractDocComments([]byte(metadata.Content), metadata.DetectedLanguage, p.opts.AnalysisOptions.CommentStyles)
		if analysisErr != nil {
			p.logger.Warn("Comment extraction failed", append(logArgs, slog.String("error", analysisErr.Error()))...)
		}
		if commentsResult != "" {
			extractedCommentsValue = commentsResult
			metadata.ExtractedComments = &extractedCommentsValue
			p.logger.Debug("Comments extracted", logArgs...)
		}
	}

	// 12. Fetch Git Metadata (Optional)
	if p.opts.GitMetadataEnabled && p.gitClient != nil {
		gitMetaMap, gitErr := p.gitClient.GetFileMetadata(p.opts.InputPath, absFilePath)
		if gitErr != nil {
			p.logger.Warn("Git metadata fetch failed", append(logArgs, slog.String("error", gitErr.Error()))...)
		} else if len(gitMetaMap) > 0 {
			gitInfoData := template.GitInfo{
				Commit:      gitMetaMap["commit"],
				Author:      gitMetaMap["author"],
				AuthorEmail: gitMetaMap["authorEmail"],
				DateISO:     gitMetaMap["dateISO"],
			}
			metadata.GitInfo = &gitInfoData
			p.logger.Debug("Git metadata fetched", logArgs...)
		}
	}

	outputContent := metadata.Content
	var pluginsRun []string

	// 13. Run Preprocessor Plugins (Optional)
	if p.pluginRunner != nil {
		pluginMetadataMap := convertMetaToMap(&metadata)
		if pluginMetadataMap == nil {
			errMsg := "Internal error converting metadata for preprocessor plugin input"
			err = ErrMetadataConversion
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err
		}
		runPluginsOutput, pluginErr := p.runPluginsForStage(ctx, PluginStagePreprocessor, relPath, outputContent, pluginMetadataMap)
		if pluginErr != nil {
			err = pluginErr // Error already wrapped
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		}
		outputContent = runPluginsOutput.Content
		updateMetadataFromMap(&metadata, runPluginsOutput.Metadata)
		pluginsRun = append(pluginsRun, runPluginsOutput.PluginsRun...)
	}

	// 14. Generate Front Matter (Optional)
	var frontMatterBlock string
	if p.opts.FrontMatterConfig.Enabled {
		pluginMetadataMapForFM := convertMetaToMap(&metadata)
		if pluginMetadataMapForFM == nil {
			errMsg := "Internal error converting metadata for front matter generation"
			err = ErrMetadataConversion
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err
		}
		fmData := make(map[string]interface{})
		for k, v := range p.opts.FrontMatterConfig.Static {
			fmData[k] = v
		}
		for _, key := range p.opts.FrontMatterConfig.Include {
			if val, ok := pluginMetadataMapForFM[key]; ok {
				fmData[key] = val
			} else {
				p.logger.Warn("Front matter include key not found in metadata", append(logArgs, slog.String("key", key))...)
			}
		}
		metadata.FrontMatter = fmData

		fmBytes, fmErr := generateFrontMatter(fmData, p.opts.FrontMatterConfig.Format)
		if fmErr != nil {
			errMsg := fmt.Sprintf("Failed to generate front matter: %v", fmErr)
			err = fmt.Errorf("%w: %w", ErrFrontMatterGen, fmErr)
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		}
		frontMatterBlock = string(fmBytes)
		p.logger.Debug("Front matter generated", logArgs...)
	}

	// Update metadata Content field before templating
	metadata.Content = outputContent

	// Check cancellation
	select {
	case <-ctx.Done():
		p.logger.Debug("Processing cancelled before template execution", logArgs...)
		status = StatusFailed
		err = ctx.Err()
		return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
	default:
	}

	// 15. Execute Template
	var templateOutput bytes.Buffer
	if p.templateExecutor == nil {
		errMsg := "Internal error: TemplateExecutor is nil"
		err = ErrConfigValidation
		status = StatusFailed
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err
	}
	if templateErr := p.templateExecutor.Execute(&templateOutput, p.opts.Template, &metadata); templateErr != nil {
		errMsg := fmt.Sprintf("Template execution failed: %v", templateErr)
		err = fmt.Errorf("%w: %w", ErrTemplateExecution, templateErr)
		status = StatusFailed
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}
	outputContent = templateOutput.String()
	p.logger.Debug("Template executed", logArgs...)

	// 16. Run Postprocessor Plugins (Optional)
	if p.pluginRunner != nil {
		pluginMetadataMapAfterTpl := convertMetaToMap(&metadata)
		if pluginMetadataMapAfterTpl == nil {
			errMsg := "Internal error converting metadata for postprocessor plugin input"
			err = ErrMetadataConversion
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err
		}
		runPluginsOutput, pluginErr := p.runPluginsForStage(ctx, PluginStagePostprocessor, relPath, outputContent, pluginMetadataMapAfterTpl)
		if pluginErr != nil {
			err = pluginErr // Error already wrapped
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		}
		outputContent = runPluginsOutput.Content
		updateMetadataFromMap(&metadata, runPluginsOutput.Metadata)
		pluginsRun = append(pluginsRun, runPluginsOutput.PluginsRun...)
	}

	// 17. Run Formatter Plugins (Optional, Mutually Exclusive Output)
	formatterProvidedOutput := false
	if p.pluginRunner != nil {
		pluginMetadataMapForFormatter := convertMetaToMap(&metadata)
		if pluginMetadataMapForFormatter == nil {
			errMsg := "Internal error converting metadata for formatter plugin input"
			err = ErrMetadataConversion
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err
		}
		formatterOutput, formatterErr := p.runPluginsForStage(ctx, PluginStageFormatter, relPath, outputContent, pluginMetadataMapForFormatter)
		if formatterErr != nil {
			err = formatterErr // Error already wrapped
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		}
		pluginsRun = append(pluginsRun, formatterOutput.PluginsRun...)
		if formatterOutput.IsFinalOutput {
			outputContent = formatterOutput.Output
			formatterProvidedOutput = true
			p.logger.Debug("Using final output from formatter plugin", append(logArgs, slog.String("plugin", formatterOutput.FinalPlugin))...)
			frontMatterBlock = ""
		} else {
			outputContent = formatterOutput.Content
			updateMetadataFromMap(&metadata, formatterOutput.Metadata)
		}
	}

	// 18. Prepare Final Content & Output Path
	var finalContent string
	if formatterProvidedOutput {
		finalContent = outputContent
	} else {
		finalContent = frontMatterBlock + outputContent
	}

	outputRelPath := metadata.OutputPath
	if outputRelPath == "" {
		errMsg := fmt.Sprintf("Internal error: Generated empty output path for %s", relPath)
		err = fmt.Errorf("%w: %s", ErrWriteFailed, errMsg)
		status = StatusFailed
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}
	absOutputPath := filepath.Join(p.opts.OutputPath, outputRelPath)

	// 19. Ensure Output Directory Exists
	outputDir := filepath.Dir(absOutputPath)
	if mkdirErr := os.MkdirAll(outputDir, 0755); mkdirErr != nil {
		errMsg := fmt.Sprintf("Failed to create output directory %s: %v", outputDir, mkdirErr)
		err = fmt.Errorf("%w: %w", ErrMkdirFailed, mkdirErr)
		status = StatusFailed
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}

	// 20. Write Output File
	outputContentBytes := []byte(finalContent)
	if writeErr := os.WriteFile(absOutputPath, outputContentBytes, 0644); writeErr != nil {
		errMsg := fmt.Sprintf("Failed to write output file %s: %v", absOutputPath, writeErr)
		err = fmt.Errorf("%w: %w", ErrWriteFailed, writeErr)
		status = StatusFailed
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}
	outputContentHash := fmt.Sprintf("%x", sha256.Sum256(outputContentBytes))
	p.logger.Debug("Output file written", append(logArgs, slog.String("outputPath", outputRelPath))...)

	// 21. Update Cache (Success Case)
	if p.opts.CacheEnabled && p.cacheManager != nil {
		updateErr := p.cacheManager.Update(relPath, modTime, currentSourceHash, configHash, outputContentHash)
		if updateErr != nil {
			p.logger.Warn("Failed to update cache entry", append(logArgs, slog.String("error", updateErr.Error()))...)
		} else {
			p.logger.Debug("Cache updated", logArgs...)
		}
	}

	// 22. Success
	status = StatusSuccess
	result = FileInfo{
		Path:               relPath,
		OutputPath:         outputRelPath,
		Language:           metadata.DetectedLanguage,
		LanguageConfidence: metadata.LanguageConfidence,
		SizeBytes:          fileSize,
		ModTime:            modTime,
		CacheStatus:        cacheStatus,
		DurationMs:         time.Since(startTime).Milliseconds(),
		ExtractedComments:  metadata.ExtractedComments != nil,
		FrontMatter:        frontMatterBlock != "",
		PluginsRun:         pluginsRun,
	}
	return result, status, nil // Success: nil error
}

// CalculateConfigHash generates a stable hash representing relevant configuration.
// Note: This hash includes template content, enabled plugins (command, config),
// analysis settings, front matter settings, and key file handling modes.
// It deliberately excludes InputPath, OutputPath, Verbose, TuiEnabled, Concurrency,
// Cache flags, etc., as these don't affect the content generation for a given file.
func (p *FileProcessor) CalculateConfigHash() (string, error) { // minimal comment
	hasher := sha256.New()

	configSubset := make(map[string]interface{})

	// 1. Template Hash (Content or Name)
	templateHash := "default"
	// FIX: Replace undefined constant with string literal "default"
	if p.opts.Template != nil && p.opts.Template.Name() != "default" {
		if p.opts.TemplatePath != "" {
			tplBytes, err := os.ReadFile(p.opts.TemplatePath)
			if err != nil {
				// Log warning and return error: Cannot guarantee stable hash without template content
				p.logger.Error("Failed to read custom template file for hashing, cannot generate reliable config hash", slog.String("path", p.opts.TemplatePath), slog.String("error", err.Error()))
				return "", fmt.Errorf("%w: failed to read template file '%s' for hashing: %w", ErrConfigHashCalculation, p.opts.TemplatePath, err)
			}
			templateHash = fmt.Sprintf("sha256:%x", sha256.Sum256(tplBytes))
		} else {
			// Fallback if TemplatePath isn't set but custom template exists (less reliable)
			p.logger.Warn("Custom template provided without TemplatePath, config hash based only on template name (less reliable)", slog.String("templateName", p.opts.Template.Name()))
			templateHash = "custom_template_name:" + p.opts.Template.Name()
		}
	}
	configSubset["templateHash"] = templateHash

	// 2. FrontMatter Config (Stable serialization)
	sortedInclude := make([]string, len(p.opts.FrontMatterConfig.Include))
	copy(sortedInclude, p.opts.FrontMatterConfig.Include)
	sort.Strings(sortedInclude)
	staticFMBytes, errMarshalStatic := json.Marshal(p.opts.FrontMatterConfig.Static)
	if errMarshalStatic != nil {
		p.logger.Warn("Failed to marshal static front matter for hashing", slog.Any("error", errMarshalStatic))
		staticFMBytes = []byte("{}")
	}
	configSubset["frontMatter"] = map[string]interface{}{
		"enabled": p.opts.FrontMatterConfig.Enabled,
		"format":  p.opts.FrontMatterConfig.Format,
		"static":  string(staticFMBytes),
		"include": sortedInclude,
	}

	// 3. Analysis Config (Stable serialization)
	sortedStyles := make([]string, len(p.opts.AnalysisOptions.CommentStyles))
	copy(sortedStyles, p.opts.AnalysisOptions.CommentStyles)
	sort.Strings(sortedStyles)
	configSubset["analysis"] = map[string]interface{}{
		"extractComments": p.opts.AnalysisOptions.ExtractComments,
		"commentStyles":   sortedStyles,
	}

	// 4. Enabled Plugin Config (Stable serialization)
	sortedPlugins := make([]PluginConfig, 0, len(p.opts.PluginConfigs))
	for _, pcfg := range p.opts.PluginConfigs {
		if pcfg.Enabled {
			sortedPlugins = append(sortedPlugins, pcfg)
		}
	}
	sort.Slice(sortedPlugins, func(i, j int) bool {
		if sortedPlugins[i].Stage != sortedPlugins[j].Stage {
			return sortedPlugins[i].Stage < sortedPlugins[j].Stage
		}
		return sortedPlugins[i].Name < sortedPlugins[j].Name
	})
	var pluginHashes []string
	for _, pl := range sortedPlugins {
		sortedAppliesTo := make([]string, len(pl.AppliesTo))
		copy(sortedAppliesTo, pl.AppliesTo)
		sort.Strings(sortedAppliesTo)
		pluginConfigBytes, errMarshalPlugin := json.Marshal(map[string]interface{}{
			"name":      pl.Name,
			"stage":     pl.Stage,
			"command":   pl.Command,
			"appliesTo": sortedAppliesTo,
			"config":    pl.Config,
		})
		if errMarshalPlugin != nil {
			// Log warning and return error: Cannot guarantee stable hash without plugin config
			p.logger.Error("Failed to marshal plugin config for hashing, cannot generate reliable config hash", slog.String("plugin", pl.Name), slog.Any("error", errMarshalPlugin))
			return "", fmt.Errorf("%w: failed to marshal config for plugin '%s': %w", ErrConfigHashCalculation, pl.Name, errMarshalPlugin)
		}
		pluginHashes = append(pluginHashes, fmt.Sprintf("sha256:%x", sha256.Sum256(pluginConfigBytes)))
	}
	configSubset["enabledPlugins"] = pluginHashes

	// 5. Other Relevant Options affecting output
	configSubset["binaryMode"] = p.opts.BinaryMode
	configSubset["largeFileMode"] = p.opts.LargeFileMode
	configSubset["largeFileTruncateCfg"] = p.opts.LargeFileTruncateCfg
	configSubset["defaultEncoding"] = p.opts.DefaultEncoding
	langMapBytes, errMarshalLangMap := json.Marshal(p.opts.LanguageMappingsOverride)
	if errMarshalLangMap != nil {
		p.logger.Warn("Failed to marshal language mappings for hashing", slog.Any("error", errMarshalLangMap))
		langMapBytes = []byte("{}")
	}
	configSubset["languageMappingsOverride"] = string(langMapBytes)
	configSubset["languageDetectionConfidenceThreshold"] = p.opts.LanguageDetectionConfidenceThreshold

	// 6. Marshal the entire subset using JSON
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
	base := relPath
	if ext != "" {
		base = strings.TrimSuffix(relPath, ext)
	}
	// FIX: Check base again after trimming suffix
	if base == "" && ext != "" {
		// Handle cases like ".bashrc" -> ".bashrc.md"
		if strings.HasPrefix(relPath, ".") && len(relPath) > 1 {
			return relPath + ".md"
		}
		// Handle cases like "file." -> "file.md"
		// Or if base was truly empty, return original path + .md ? This seems less likely.
		// Fallback to avoid returning just ".md"
		if base == "" {
			return relPath + ".md"
		}
	}
	// Avoid adding .md if base already ends with .md (less likely for source files)
	if strings.HasSuffix(base, ".md") {
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

		// Skip unexported fields or the Options field
		if !fieldValue.CanInterface() || fieldName == "Options" {
			continue
		}

		fieldInterface := fieldValue.Interface()

		// Format time.Time as RFC3339 string for JSON compatibility
		if tm, ok := fieldInterface.(time.Time); ok {
			m[fieldName] = tm.Format(time.RFC3339)
			continue
		}

		// Handle pointers: assign nil or the underlying value
		if fieldValue.Kind() == reflect.Ptr {
			if fieldValue.IsNil() {
				m[fieldName] = nil
			} else {
				// Specific handling for *GitInfo needed if it's not automatically marshaled
				if gitInfoPtr, ok := fieldInterface.(*template.GitInfo); ok && gitInfoPtr != nil {
					// Manually convert GitInfo struct to map or rely on JSON marshal behavior
					// For simplicity, let's assume GitInfo is simple enough for direct interface{}
					m[fieldName] = fieldValue.Elem().Interface()
				} else {
					m[fieldName] = fieldValue.Elem().Interface()
				}
			}
			continue
		}

		// Ensure maps are non-nil
		if fieldValue.Kind() == reflect.Map {
			if fieldValue.IsNil() {
				m[fieldName] = make(map[string]interface{}) // Ensure non-nil map for JSON
			} else {
				m[fieldName] = fieldInterface
			}
			continue
		}

		// Assign other values directly
		m[fieldName] = fieldInterface
	}

	// Remove the potentially large Content field, plugins receive it separately
	delete(m, "Content")

	return m
}

// updateMetadataFromMap updates fields in the TemplateMetadata struct based on a map potentially modified by plugins.
func updateMetadataFromMap(meta *template.TemplateMetadata, data map[string]interface{}) { // minimal comment
	if data == nil || meta == nil {
		return
	}
	v := reflect.ValueOf(meta).Elem() // Ensure we are working with the underlying struct

	for key, value := range data {
		// Skip fields that shouldn't be updated from plugin metadata
		// (e.g., Content is handled separately, core path info shouldn't change post-plugin)
		if key == "Content" || key == "FilePath" || key == "FileName" ||
			key == "OutputPath" || key == "SizeBytes" || key == "ModTime" ||
			key == "ContentHash" || key == "IsBinary" || key == "IsLarge" || key == "Truncated" {
			continue
		}

		field := v.FieldByName(key)
		// Check if field exists and is settable
		if !field.IsValid() || !field.CanSet() {
			continue
		}

		valueReflect := reflect.ValueOf(value)
		fieldType := field.Type()

		// Direct assignment if types match
		if valueReflect.IsValid() && valueReflect.Type().AssignableTo(fieldType) {
			field.Set(valueReflect)
			continue
		}

		// Handle pointer fields (*string, *GitInfo)
		if fieldType.Kind() == reflect.Ptr {
			elemType := fieldType.Elem()
			if value == nil {
				// If plugin explicitly sets value to null, set pointer to nil
				field.Set(reflect.Zero(fieldType))
			} else if valueReflect.IsValid() {
				// Handle specific pointer types if needed (e.g., *GitInfo from map)
				if elemType == reflect.TypeOf(template.GitInfo{}) && valueReflect.Kind() == reflect.Map {
					// Attempt to convert map back to GitInfo struct
					if gitMap, ok := value.(map[string]interface{}); ok {
						newGitInfo := template.GitInfo{}
						if c, ok := gitMap["Commit"].(string); ok {
							newGitInfo.Commit = c
						}
						if a, ok := gitMap["Author"].(string); ok {
							newGitInfo.Author = a
						}
						if e, ok := gitMap["AuthorEmail"].(string); ok {
							newGitInfo.AuthorEmail = e
						}
						if d, ok := gitMap["DateISO"].(string); ok {
							newGitInfo.DateISO = d
						}
						// Set the pointer field to the address of the new struct
						if field.IsNil() { // Ensure pointer is allocated if nil
							field.Set(reflect.New(elemType))
						}
						field.Elem().Set(reflect.ValueOf(newGitInfo))
					}
				} else if elemType.Kind() == reflect.String && valueReflect.Kind() == reflect.String {
					// Set *string
					strVal := valueReflect.String()
					// field.Set(reflect.ValueOf(&strVal)) // This creates a pointer to a local var, which is bad
					if field.IsNil() {
						field.Set(reflect.New(elemType))
					}
					field.Elem().SetString(strVal)
				}
				// Add other pointer type conversions as needed
			}
			continue
		}

		// Handle map fields (like FrontMatter)
		if fieldType.Kind() == reflect.Map && valueReflect.Kind() == reflect.Map {
			// Ensure map types are compatible (e.g., map[string]interface{})
			if fieldType.Key().Kind() == reflect.String && fieldType.Elem().Kind() == reflect.Interface {
				// Attempt conversion if plugin returned map[interface{}]interface{}
				if mapValRaw, ok := value.(map[interface{}]interface{}); ok {
					convertedMap := make(map[string]interface{})
					canConvert := true
					for k, v_ := range mapValRaw {
						if strKey, okKey := k.(string); okKey {
							convertedMap[strKey] = v_
						} else {
							canConvert = false // Cannot convert non-string key
							break
						}
					}
					if canConvert {
						if field.IsNil() { // Ensure the map in the struct is initialized
							field.Set(reflect.MakeMap(fieldType))
						}
						// Merge instead of replace? Current logic replaces.
						// For merging: reflect.Copy(field, reflect.ValueOf(convertedMap)) might work or manual loop needed.
						// Replacing for now:
						field.Set(reflect.ValueOf(convertedMap))
					}
				} else if mapValStr, ok := value.(map[string]interface{}); ok {
					if field.IsNil() {
						field.Set(reflect.MakeMap(fieldType))
					}
					// Replace map content
					field.Set(reflect.ValueOf(mapValStr))
				}
			}
			continue
		}

		// Add more type conversion logic here if necessary
		// (e.g., converting float64 from JSON back to int64 if needed and possible)
	}
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

// runPluginsForStage executes applicable plugins for a given stage.
func (p *FileProcessor) runPluginsForStage(ctx context.Context, stage string, relPath string, currentContent string, currentMetadata map[string]interface{}) (PluginRunResult, error) { // minimal comment
	result := PluginRunResult{
		Content:       currentContent,
		Metadata:      currentMetadata,
		PluginsRun:    []string{},
		IsFinalOutput: false,
	}
	logArgs := []any{slog.String("path", relPath), slog.String("stage", stage)}

	if result.Metadata == nil {
		result.Metadata = make(map[string]interface{})
	}

	if p.pluginRunner == nil {
		p.logger.Debug("Plugin runner not configured, skipping stage", logArgs...)
		return result, nil
	}

	p.logger.Debug("Checking plugins for stage", logArgs...)
	pluginsExecuted := false

	for _, pluginCfg := range p.opts.PluginConfigs {
		pluginLogArgs := append(logArgs, slog.String("plugin", pluginCfg.Name))
		select {
		case <-ctx.Done():
			p.logger.Debug("Plugin execution cancelled during check", pluginLogArgs...)
			return result, ctx.Err()
		default:
		}

		// Assign stage to config based on map key used in Options (or however it's loaded)
		// This assumes the loader sets the stage correctly based on where the plugin was defined.
		// If not set, this logic might need adjustment.
		// Let's assume opts.PluginConfigs includes the Stage.
		if !pluginCfg.Enabled || pluginCfg.Stage != stage {
			continue
		}

		applies := false
		if len(pluginCfg.AppliesTo) == 0 {
			applies = true // Applies to all files if list is empty
		} else {
			for _, pattern := range pluginCfg.AppliesTo {
				// Use filepath.Match for glob patterns against the relative path
				match, _ := filepath.Match(pattern, relPath) // Ignore potential pattern error
				if match {
					applies = true
					break
				}
			}
		}

		if !applies {
			p.logger.Debug("Plugin skipped (appliesTo mismatch)", pluginLogArgs...)
			continue
		}

		p.logger.Debug("Running plugin", pluginLogArgs...)
		pluginsExecuted = true

		input := PluginInput{
			SchemaVersion: PluginSchemaVersion, // Use constant from this package
			Stage:         stage,
			FilePath:      relPath,
			Content:       result.Content,  // Pass current content
			Metadata:      result.Metadata, // Pass current metadata
			Config:        pluginCfg.Config,
		}

		pluginOutput, pluginErr := p.pluginRunner.Run(ctx, stage, pluginCfg, input)

		if pluginErr != nil {
			// Log the specific plugin error here for better context
			p.logger.Error("Plugin execution returned an error", append(pluginLogArgs, slog.String("error", pluginErr.Error()))...)
			// Return the error directly, ProcessFile will wrap it
			return result, pluginErr
		}

		// Append successfully run plugin name
		result.PluginsRun = append(result.PluginsRun, pluginCfg.Name)

		// Update content based on plugin output ONLY if it's not empty or nil.
		// PluginOutput uses `omitempty` for JSON, so check length.
		if pluginOutput.Content != "" {
			result.Content = pluginOutput.Content
			p.logger.Debug("Plugin updated content", pluginLogArgs...)
		}

		// Merge metadata updates from plugin
		if len(pluginOutput.Metadata) > 0 {
			for k, v := range pluginOutput.Metadata {
				result.Metadata[k] = v // Plugin metadata overwrites existing keys
			}
			p.logger.Debug("Plugin updated metadata", append(pluginLogArgs, slog.Any("updates", pluginOutput.Metadata))...)
		}

		// Handle formatter stage specific 'output' field
		if stage == PluginStageFormatter && pluginOutput.Output != "" {
			result.Output = pluginOutput.Output
			result.IsFinalOutput = true
			result.FinalPlugin = pluginCfg.Name
			p.logger.Debug("Formatter plugin provided final output, skipping further processing for this stage", pluginLogArgs...)
			// IMPORTANT: If a formatter provides final output, we break the loop for this stage
			// as only one formatter should determine the final output.
			break
		}
	}

	if pluginsExecuted {
		p.logger.Debug("Finished running plugins for stage", append(logArgs, slog.Int("executedCount", len(result.PluginsRun)))...)
	}

	return result, nil // Success
}

// generateFrontMatter marshals data into YAML or TOML format including delimiters.
func generateFrontMatter(data map[string]interface{}, format string) ([]byte, error) { // minimal comment
	var buf bytes.Buffer
	var marshaledBytes []byte
	var err error

	if data == nil {
		data = make(map[string]interface{})
	}

	switch strings.ToLower(format) {
	case "yaml":
		marshaledBytes, err = yaml.Marshal(data)
		if err == nil {
			buf.WriteString("---\n")
			buf.Write(marshaledBytes)
			// Ensure trailing newline before ending delimiter if content exists
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
			// Ensure trailing newline before ending delimiter if content exists
			if len(marshaledBytes) > 0 && !bytes.HasSuffix(marshaledBytes, []byte("\n")) {
				buf.WriteString("\n")
			}
			buf.WriteString("+++\n")
		}
	default:
		// Return specific validation error
		return nil, fmt.Errorf("%w: unsupported front matter format: %s", ErrConfigValidation, format)
	}

	if err != nil {
		// Wrap marshalling errors
		return nil, fmt.Errorf("%w: failed to marshal front matter to %s: %w", ErrFrontMatterGen, format, err)
	}

	return buf.Bytes(), nil
}

// truncateContent reduces content size based on config string (bytes or lines).
func truncateContent(content []byte, cfg string) ([]byte, error) { // minimal comment
	cfg = strings.TrimSpace(strings.ToLower(cfg))
	var limit int64 = -1
	isLineLimit := false

	if strings.HasSuffix(cfg, " lines") || strings.HasSuffix(cfg, " line") {
		lineStr := strings.Fields(cfg)[0]
		lineLimit, parseErr := strconv.ParseInt(lineStr, 10, 64)
		if parseErr == nil && lineLimit >= 0 {
			limit = lineLimit
			isLineLimit = true
		} else {
			return nil, fmt.Errorf("%w: invalid line limit format in truncate config '%s': %w", ErrConfigValidation, cfg, parseErr)
		}
	} else {
		sizeStr := cfg
		multiplier := int64(1)
		units := []struct {
			suffix string
			mult   int64
		}{
			{"gb", 1024 * 1024 * 1024}, {"g", 1024 * 1024 * 1024},
			{"mb", 1024 * 1024}, {"m", 1024 * 1024},
			{"kb", 1024}, {"k", 1024},
			{"b", 1}, // Add 'b' for bytes explicitly
		}
		numericPart := sizeStr
		unitFound := false
		// Iterate *backwards* from longest suffix to shortest (GB -> B)
		for i := len(units) - 1; i >= 0; i-- {
			unit := units[i]
			if strings.HasSuffix(sizeStr, unit.suffix) {
				numericPart = strings.TrimSuffix(sizeStr, unit.suffix)
				multiplier = unit.mult
				unitFound = true
				break
			}
		}

		// Handle plain number (assume bytes if no unit found after checking all)
		if !unitFound {
			// Allow plain numbers, assume bytes
			multiplier = 1
		}

		numericPart = strings.TrimSpace(numericPart)
		byteLimit, parseErr := strconv.ParseInt(numericPart, 10, 64)
		if parseErr != nil {
			return nil, fmt.Errorf("%w: invalid numeric value in truncate size config '%s': %w", ErrConfigValidation, cfg, parseErr)
		}
		if byteLimit < 0 {
			return nil, fmt.Errorf("%w: numeric value cannot be negative in truncate size config '%s'", ErrConfigValidation, cfg)
		}
		limit = byteLimit * multiplier

	}

	// Double check limit is non-negative (should be caught above, but defensive)
	if limit < 0 {
		return nil, fmt.Errorf("%w: calculated limit is negative, invalid config: %s", ErrConfigValidation, cfg)
	}

	if isLineLimit {
		if limit == 0 {
			return []byte{}, nil
		}
		lineCount := int64(0)
		endIndex := 0
		for i, b := range content {
			if b == '\n' {
				lineCount++
				if lineCount == limit {
					// Include the newline itself
					endIndex = i + 1
					break
				}
			}
			// If loop finishes without reaching limit, endIndex will be len(content)
			if i == len(content)-1 {
				endIndex = len(content)
			}
		}
		// Handle case where content has fewer lines than limit
		if lineCount < limit {
			endIndex = len(content)
		}
		// Handle potential empty content case
		if len(content) == 0 {
			return []byte{}, nil
		}
		return content[:endIndex], nil
	} else { // Byte limit
		byteLimit := limit
		contentLen := int64(len(content))
		if contentLen <= byteLimit {
			return content, nil // No truncation needed
		}
		// Ensure truncation happens at a valid UTF-8 boundary
		// Go backwards from the limit until we hit the start of a character
		finalLimit := byteLimit
		for finalLimit > 0 && (content[finalLimit]&0xC0) == 0x80 { // Check if it's a continuation byte (10xxxxxx)
			finalLimit--
		}
		// Safety check: If loop went too far back (e.g., all continuation bytes), start from 0.
		if finalLimit < 0 {
			finalLimit = 0
		}
		return content[:finalLimit], nil
	}
}

// --- END OF FINAL REVISED FILE pkg/converter/processor.go ---

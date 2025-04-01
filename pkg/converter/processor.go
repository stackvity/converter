// --- START OF FINAL REVISED FILE pkg/converter/processor.go ---
package converter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
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
	// Assume internal paths based on previous structure - THESE ARE OK
	"github.com/stackvity/stack-converter/pkg/converter/analysis"
	"github.com/stackvity/stack-converter/pkg/converter/encoding"
	"github.com/stackvity/stack-converter/pkg/converter/language"
	"github.com/stackvity/stack-converter/pkg/converter/template" // Imports the template package types like TemplateMetadata
	"gopkg.in/yaml.v3"
)

// --- Processor Specific Errors ---
// Assuming these are defined in pkg/converter/errors.go
// var (
// 	ErrReadFailed            = errors.New("failed to read file")
// 	ErrStatFailed            = errors.New("failed to get file stats")
// 	ErrConfigHashCalculation = errors.New("failed to calculate config hash")
// 	ErrBinaryFile            = errors.New("binary file encountered")
// 	ErrLargeFile             = errors.New("large file encountered")
// 	ErrTruncationFailed      = errors.New("failed to truncate content")
// 	ErrPluginExecution       = errors.New("plugin execution failed")
// 	ErrFrontMatterGen        = errors.New("failed to generate front matter")
// 	ErrTemplateExecution     = errors.New("template execution failed")
// 	ErrWriteFailed           = errors.New("failed to write output file")
// 	ErrMkdirFailed           = errors.New("failed to create output directory")
// 	ErrConfigValidation      = errors.New("invalid configuration options provided") // Used in helper functions
// )

// --- FileProcessor ---

// FileProcessor handles the processing pipeline for a single file.
type FileProcessor struct {
	opts             *Options
	logger           *slog.Logger
	cacheManager     CacheManager // Uses interface from options.go
	langDetector     language.LanguageDetector
	encodingHandler  encoding.EncodingHandler
	analysisEngine   analysis.AnalysisEngine
	gitClient        GitClient    // Uses interface from options.go
	pluginRunner     PluginRunner // Uses interface from options.go
	templateExecutor template.TemplateExecutor
}

// NewFileProcessor creates a new FileProcessor.
func NewFileProcessor(
	opts *Options,
	loggerHandler slog.Handler,
	cacheMgr CacheManager, // Use interface type from options.go
	langDet language.LanguageDetector,
	encHandler encoding.EncodingHandler,
	analysisEng analysis.AnalysisEngine,
	gitCl GitClient, // Use interface type from options.go
	pluginRun PluginRunner, // Use interface type from options.go
	tplExecutor template.TemplateExecutor,
) *FileProcessor {
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
func (p *FileProcessor) ProcessFile(ctx context.Context, absFilePath string) (result interface{}, status Status, err error) {
	startTime := time.Now()
	relPath, pathErr := filepath.Rel(p.opts.InputPath, absFilePath)
	if pathErr != nil {
		errMsg := fmt.Sprintf("Failed to calculate relative path for %s: %v", absFilePath, pathErr)
		p.logger.Error(errMsg, slog.String("absolutePath", absFilePath))
		// Use exported error variable from errors.go
		return nil, StatusFailed, fmt.Errorf("%w: %w", ErrReadFailed, pathErr) // Assuming ErrReadFailed is suitable
	}
	relPath = filepath.ToSlash(relPath)
	logArgs := []any{slog.String("path", relPath)}

	// Defer block to log final status and update hooks
	defer func() {
		duration := time.Since(startTime) // Use time.Since
		var message string
		finalStatus := status // Tentative final status
		if err != nil {
			finalStatus = StatusFailed // Override if an error occurred
			message = err.Error()
		} else {
			// Use switch on defined Status constants
			switch status {
			case StatusCached:
				message = "Retrieved from cache"
			case StatusSkipped:
				if si, ok := result.(SkippedInfo); ok {
					message = fmt.Sprintf("Skipped - %s: %s", si.Reason, si.Details)
				} else {
					message = "Skipped" // Fallback message if result type is unexpected
				}
			case StatusSuccess:
				message = "Successfully processed"
			}
		}

		logArgsWithStatus := append(logArgs, slog.String("status", string(finalStatus)), slog.Duration("duration", duration))
		if finalStatus == StatusFailed && err != nil {
			logArgsWithStatus = append(logArgsWithStatus, slog.String("error", err.Error()))
		}
		p.logger.Debug("File processing finished", logArgsWithStatus...)

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
		// Return ErrorInfo as result for failed status
		return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: true}, status, err
	default:
	}

	// 2. Get File Info (Stat)
	fileInfo, statErr := os.Stat(absFilePath)
	if statErr != nil {
		errMsg := fmt.Sprintf("Failed to stat file: %v", statErr)
		// Use exported error variable from errors.go
		err = fmt.Errorf("%w: %w", ErrStatFailed, statErr)
		status = StatusFailed
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}
	modTime := fileInfo.ModTime()
	fileSize := fileInfo.Size()

	// 3. Calculate Config Hash
	// FIX: Call the exported method name
	configHash, cfgHashErr := p.CalculateConfigHash()
	if cfgHashErr != nil {
		errMsg := fmt.Sprintf("Failed to calculate config hash: %v", cfgHashErr)
		// Use exported error variable from errors.go
		err = fmt.Errorf("%w: %w", ErrConfigHashCalculation, cfgHashErr)
		p.logger.Error(errMsg, logArgs...)
		status = StatusFailed
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: true}, status, err
	}

	var sourceContentBytes []byte // Declared outside for broader scope

	// 4. Check Cache
	cacheStatus := CacheStatusDisabled // Default if cache not enabled
	var currentSourceHash string
	if p.opts.CacheEnabled && p.cacheManager != nil {
		cacheStatus = CacheStatusMiss // Assume miss initially if enabled
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
					return result, status, nil // Return early on cache hit
				}
				p.logger.Debug("Cache miss", logArgs...)
				sourceContentBytes = sourceContentBytesForCache // Store content read for cache check
			}
		} else {
			cacheStatus = CacheStatusDisabled // Read ignored, effectively disabled
			p.logger.Debug("Cache read ignored", logArgs...)
		}
	}

	// 5. Read File Content (if not already read for cache check)
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

	// Calculate source hash if needed for cache update
	if currentSourceHash == "" && p.opts.CacheEnabled {
		currentSourceHash = fmt.Sprintf("%x", sha256.Sum256(sourceContentBytes))
	}

	// 6. Handle Encoding
	utf8ContentBytes, detectedEncoding, certainty, encodingErr := p.encodingHandler.DetectAndDecode(sourceContentBytes)
	if encodingErr != nil {
		p.logger.Warn("Encoding conversion failed", append(logArgs, slog.String("error", encodingErr.Error()))...)
	} else if !certainty {
		logMsg := "Encoding detection uncertain"
		// ... (logging logic remains the same) ...
		p.logger.Warn(logMsg, append(logArgs, slog.String("guessedEncoding", detectedEncoding))...)
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
	isBinary := p.encodingHandler.IsBinary(utf8ContentBytes)
	if isBinary {
		switch p.opts.BinaryMode {
		case BinarySkip:
			p.logger.Debug("Skipping binary file", logArgs...)
			status = StatusSkipped
			result = SkippedInfo{Path: relPath, Reason: SkipReasonBinary, Details: "Binary file detected"}
			return result, status, nil // Return early
		case BinaryPlaceholder:
			p.logger.Debug("Generating placeholder for binary file", logArgs...)
			textContent = fmt.Sprintf("[Binary content skipped: %s]", filepath.Base(relPath))
		case BinaryError:
			err = ErrBinaryFile
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
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
			return result, status, nil // Return early
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
	languageName, confidence, langErr := p.langDetector.Detect([]byte(textContent), relPath)
	if langErr != nil {
		p.logger.Warn("Language detection failed", append(logArgs, slog.String("error", langErr.Error()))...)
		languageName = "unknown"
		confidence = 0.0
	}
	p.logger.Debug("Language detected", append(logArgs, slog.String("language", languageName), slog.Float64("confidence", confidence))...)

	// 10. Prepare Metadata Struct
	var extractedComments *string
	var gitInfo *template.GitInfo
	metadata := template.TemplateMetadata{
		FilePath:           relPath,
		FileName:           filepath.Base(relPath),
		Content:            textContent, // Start with potentially modified content
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
		} else if commentsResult != "" {
			extractedCommentsValue = commentsResult
			metadata.ExtractedComments = &extractedCommentsValue
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
		}
	}

	outputContent := metadata.Content // Start with current content
	var pluginsRun []string

	// 13. Run Preprocessor Plugins (Optional)
	if p.pluginRunner != nil {
		pluginMetadataMap := convertMetaToMap(&metadata)
		runPluginsOutput, pluginErr := p.runPluginsForStage(ctx, PluginStagePreprocessor, relPath, outputContent, pluginMetadataMap)
		if pluginErr != nil {
			err = fmt.Errorf("%w: preprocessor stage failed: %w", ErrPluginExecution, pluginErr)
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
		fmData := make(map[string]interface{})
		for k, v := range p.opts.FrontMatterConfig.Static {
			fmData[k] = v
		}
		for _, key := range p.opts.FrontMatterConfig.Include {
			if val, ok := pluginMetadataMapForFM[key]; ok {
				fmData[key] = val
			}
		}
		metadata.FrontMatter = fmData // Store generated FM data

		fmBytes, fmErr := generateFrontMatter(fmData, p.opts.FrontMatterConfig.Format)
		if fmErr != nil {
			errMsg := fmt.Sprintf("Failed to generate front matter: %v", fmErr)
			err = fmt.Errorf("%w: %w", ErrFrontMatterGen, fmErr)
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		}
		frontMatterBlock = string(fmBytes)
	}

	// Update metadata Content before templating
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
	if templateErr := p.templateExecutor.Execute(&templateOutput, p.opts.Template, &metadata); templateErr != nil {
		errMsg := fmt.Sprintf("Template execution failed: %v", templateErr)
		err = fmt.Errorf("%w: %w", ErrTemplateExecution, templateErr)
		status = StatusFailed
		return ErrorInfo{Path: relPath, Error: errMsg, IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
	}
	outputContent = templateOutput.String() // Update content

	// 16. Run Postprocessor Plugins (Optional)
	if p.pluginRunner != nil {
		pluginMetadataMapAfterTpl := convertMetaToMap(&metadata)
		runPluginsOutput, pluginErr := p.runPluginsForStage(ctx, PluginStagePostprocessor, relPath, outputContent, pluginMetadataMapAfterTpl)
		if pluginErr != nil {
			err = fmt.Errorf("%w: postprocessor stage failed: %w", ErrPluginExecution, pluginErr)
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
		formatterOutput, formatterErr := p.runPluginsForStage(ctx, PluginStageFormatter, relPath, outputContent, pluginMetadataMapForFormatter)
		if formatterErr != nil {
			err = fmt.Errorf("%w: formatter stage failed: %w", ErrPluginExecution, formatterErr)
			status = StatusFailed
			return ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: p.opts.OnErrorMode == OnErrorStop}, status, err
		}
		pluginsRun = append(pluginsRun, formatterOutput.PluginsRun...)
		if formatterOutput.IsFinalOutput {
			outputContent = formatterOutput.Output // Use formatter output
			formatterProvidedOutput = true
			p.logger.Debug("Using final output from formatter plugin", append(logArgs, slog.String("plugin", formatterOutput.FinalPlugin))...)
			frontMatterBlock = "" // Typically formatters handle everything
		} else {
			outputContent = formatterOutput.Content // Reflect postprocessor results
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

	outputRelPath := generateOutputPath(relPath)
	if outputRelPath == "" {
		errMsg := fmt.Sprintf("Generated empty output path for %s", relPath)
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

	// 21. Update Cache (Success Case)
	if p.opts.CacheEnabled && p.cacheManager != nil {
		updateErr := p.cacheManager.Update(relPath, modTime, currentSourceHash, configHash, outputContentHash)
		if updateErr != nil {
			p.logger.Warn("Failed to update cache entry", append(logArgs, slog.String("error", updateErr.Error()))...)
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
		DurationMs:         time.Since(startTime).Milliseconds(), // Use time.Since
		ExtractedComments:  metadata.ExtractedComments != nil,
		FrontMatter:        frontMatterBlock != "",
		PluginsRun:         pluginsRun,
	}
	return result, status, nil // Return nil error on success
}

// CalculateConfigHash generates a stable hash representing relevant configuration.
// FIX: Renamed to be exported.
func (p *FileProcessor) CalculateConfigHash() (string, error) {
	hasher := sha256.New()

	configSubset := make(map[string]interface{})

	// Template Hash
	templateHash := "default"
	if p.opts.Template != nil && p.opts.Template.Name() != "default" {
		if p.opts.TemplatePath != "" {
			tplBytes, err := os.ReadFile(p.opts.TemplatePath)
			if err != nil {
				p.logger.Warn("Failed to read custom template file for hashing, config hash may be inaccurate", slog.String("path", p.opts.TemplatePath), slog.String("error", err.Error()))
				templateHash = "custom_template_read_error_" + p.opts.Template.Name()
			} else {
				templateHash = fmt.Sprintf("%x", sha256.Sum256(tplBytes))
			}
		} else {
			p.logger.Warn("Custom template provided without TemplatePath, config hash based on template name", slog.String("templateName", p.opts.Template.Name()))
			templateHash = "custom_template_" + p.opts.Template.Name()
		}
	}
	configSubset["templateHash"] = templateHash

	// FrontMatter Config (Ensure sorted Include slice)
	sortedInclude := make([]string, len(p.opts.FrontMatterConfig.Include))
	copy(sortedInclude, p.opts.FrontMatterConfig.Include)
	sort.Strings(sortedInclude)
	staticFMBytes, _ := json.Marshal(p.opts.FrontMatterConfig.Static) // Ignore error for hashing

	configSubset["frontMatter"] = map[string]interface{}{
		"enabled": p.opts.FrontMatterConfig.Enabled,
		"format":  p.opts.FrontMatterConfig.Format,
		"static":  string(staticFMBytes),
		"include": sortedInclude,
	}

	// Analysis Config (Sort styles)
	sortedStyles := make([]string, len(p.opts.AnalysisOptions.CommentStyles))
	copy(sortedStyles, p.opts.AnalysisOptions.CommentStyles)
	sort.Strings(sortedStyles)
	configSubset["analysis"] = map[string]interface{}{
		"extractComments": p.opts.AnalysisOptions.ExtractComments,
		"commentStyles":   sortedStyles,
	}

	// Plugin Config (Sort by Stage then Name for stability)
	sortedPlugins := make([]PluginConfig, len(p.opts.PluginConfigs))
	copy(sortedPlugins, p.opts.PluginConfigs)
	sort.Slice(sortedPlugins, func(i, j int) bool {
		if sortedPlugins[i].Stage != sortedPlugins[j].Stage {
			return sortedPlugins[i].Stage < sortedPlugins[j].Stage
		}
		return sortedPlugins[i].Name < sortedPlugins[j].Name
	})
	var pluginHashes []string
	for _, pl := range sortedPlugins {
		if !pl.Enabled {
			continue
		}
		sortedAppliesTo := make([]string, len(pl.AppliesTo))
		copy(sortedAppliesTo, pl.AppliesTo)
		sort.Strings(sortedAppliesTo)
		pluginConfigBytes, _ := json.Marshal(map[string]interface{}{
			"name":      pl.Name,
			"stage":     pl.Stage,
			"command":   pl.Command,
			"appliesTo": sortedAppliesTo,
			"config":    pl.Config,
		})
		pluginHashes = append(pluginHashes, fmt.Sprintf("%x", sha256.Sum256(pluginConfigBytes)))
	}
	configSubset["plugins"] = pluginHashes

	// Other Relevant Options
	configSubset["binaryMode"] = p.opts.BinaryMode
	configSubset["largeFileMode"] = p.opts.LargeFileMode
	configSubset["largeFileTruncateCfg"] = p.opts.LargeFileTruncateCfg
	configSubset["defaultEncoding"] = p.opts.DefaultEncoding
	configSubset["languageMappingsOverride"] = p.opts.LanguageMappingsOverride
	configSubset["languageDetectionConfidenceThreshold"] = p.opts.LanguageDetectionConfidenceThreshold

	// Use JSON marshalling with sorted keys for deterministic output
	configBytes, err := json.Marshal(configSubset)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrConfigHashCalculation, fmt.Errorf("failed to marshal config subset for hashing: %w", err))
	}

	hasher.Write(configBytes)
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// generateOutputPath determines the output path for a source file.
func generateOutputPath(relPath string) string {
	if relPath == "." || relPath == "" {
		return ""
	}
	ext := filepath.Ext(relPath)
	base := relPath
	if ext != "" {
		base = strings.TrimSuffix(relPath, ext)
	}
	if base == "" && ext != "" {
		return relPath + ".md"
	} else if base != "" {
		return base + ".md"
	}
	return relPath + ".md" // Fallback
}

// convertMetaToMap converts TemplateMetadata struct to map for plugins/frontmatter.
// ... (implementation remains the same) ...
func convertMetaToMap(meta *template.TemplateMetadata) map[string]interface{} {
	m := make(map[string]interface{})
	v := reflect.ValueOf(*meta)
	t := v.Type()

	for i := 0; i < v.NumField(); i++ {
		fieldName := t.Field(i).Name
		fieldValue := v.Field(i)

		if !fieldValue.CanInterface() || fieldName == "Content" { // Skip unexported or content
			continue
		}

		fieldInterface := fieldValue.Interface()

		if tm, ok := fieldInterface.(time.Time); ok {
			m[fieldName] = tm.Format(time.RFC3339) // Standard format
			continue
		}

		if fieldValue.Kind() == reflect.Ptr {
			if fieldValue.IsNil() {
				m[fieldName] = nil
			} else {
				m[fieldName] = fieldValue.Elem().Interface()
			}
			continue
		}

		if fieldValue.Kind() == reflect.Map {
			if fieldValue.IsNil() {
				m[fieldName] = make(map[string]interface{})
			} else {
				m[fieldName] = fieldInterface
			}
			continue
		}

		m[fieldName] = fieldInterface
	}
	return m
}

// updateMetadataFromMap updates fields in the TemplateMetadata struct based on a map potentially modified by plugins.
// ... (implementation remains the same) ...
func updateMetadataFromMap(meta *template.TemplateMetadata, data map[string]interface{}) {
	if data == nil {
		return
	}
	v := reflect.ValueOf(meta).Elem()

	for key, value := range data {
		// Skip fields not intended for plugin modification
		if key == "Content" || key == "FilePath" || key == "FileName" ||
			key == "OutputPath" || key == "SizeBytes" || key == "ModTime" ||
			key == "ContentHash" {
			continue
		}

		field := v.FieldByName(key)
		if !field.IsValid() || !field.CanSet() {
			continue
		}

		valueReflect := reflect.ValueOf(value)
		fieldType := field.Type()

		// Handle pointer fields
		if fieldType.Kind() == reflect.Ptr {
			elemType := fieldType.Elem()
			if value == nil {
				field.Set(reflect.Zero(fieldType)) // Set to nil pointer
			} else if valueReflect.IsValid() && valueReflect.Type().AssignableTo(elemType) {
				// If the value can be directly assigned to the element type
				newPtr := reflect.New(elemType)
				newPtr.Elem().Set(valueReflect)
				field.Set(newPtr)
			} else if valueReflect.Kind() == reflect.Map && elemType == reflect.TypeOf(template.GitInfo{}) {
				// Handle specific case: Reconstruct GitInfo struct from map
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
					field.Set(reflect.ValueOf(&newGitInfo))
				}
			} else if valueReflect.Kind() == reflect.String && elemType.Kind() == reflect.String {
				// Handle *string case like ExtractedComments
				strVal := valueReflect.String()
				field.Set(reflect.ValueOf(&strVal))
			}
			// Add more specific pointer type reconstructions if needed
		} else if valueReflect.IsValid() && valueReflect.Type().AssignableTo(fieldType) {
			field.Set(valueReflect)
		} else if valueReflect.Kind() == reflect.Map && fieldType.Kind() == reflect.Map {
			// Handle map fields like FrontMatter
			if fmMap, ok := value.(map[string]interface{}); ok && fieldType.Key().Kind() == reflect.String && fieldType.Elem().Kind() == reflect.Interface {
				field.Set(reflect.ValueOf(fmMap))
			}
		}
	}
}

// PluginRunResult holds results from running plugins for a stage.
type PluginRunResult struct {
	Content       string                 // Updated content after plugins ran
	Metadata      map[string]interface{} // Updated metadata after plugins ran
	PluginsRun    []string               // Names of plugins that actually ran
	IsFinalOutput bool                   // True if a formatter provided final 'output'
	Output        string                 // The final output from a formatter
	FinalPlugin   string                 // Name of the formatter plugin that provided final output
}

// runPluginsForStage executes applicable plugins for a given stage.
// ... (implementation remains the same) ...
func (p *FileProcessor) runPluginsForStage(ctx context.Context, stage string, relPath string, currentContent string, currentMetadata map[string]interface{}) (PluginRunResult, error) {
	result := PluginRunResult{
		Content:       currentContent, // Start with current state
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

	for _, pluginCfg := range p.opts.PluginConfigs {
		pluginLogArgs := append(logArgs, slog.String("plugin", pluginCfg.Name))
		select {
		case <-ctx.Done():
			p.logger.Debug("Plugin execution cancelled", pluginLogArgs...)
			return result, ctx.Err()
		default:
		}

		if !pluginCfg.Enabled || pluginCfg.Stage != stage {
			continue
		}

		applies := false
		if len(pluginCfg.AppliesTo) == 0 {
			applies = true
		} else {
			for _, pattern := range pluginCfg.AppliesTo {
				match, _ := filepath.Match(pattern, relPath)
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
		input := PluginInput{
			SchemaVersion: PluginSchemaVersion,
			Stage:         stage,
			FilePath:      relPath,
			Content:       result.Content,
			Metadata:      result.Metadata,
			Config:        pluginCfg.Config,
		}

		pluginOutput, pluginErr := p.pluginRunner.Run(ctx, stage, pluginCfg, input)

		if pluginErr != nil {
			return result, fmt.Errorf("plugin '%s' failed execution: %w", pluginCfg.Name, pluginErr)
		}
		if pluginOutput.Error != "" {
			pluginErr = fmt.Errorf("plugin '%s' reported error: %s", pluginCfg.Name, pluginOutput.Error)
			return result, pluginErr
		}

		result.PluginsRun = append(result.PluginsRun, pluginCfg.Name)
		result.Content = pluginOutput.Content

		if len(pluginOutput.Metadata) > 0 {
			for k, v := range pluginOutput.Metadata {
				result.Metadata[k] = v
			}
			p.logger.Debug("Plugin updated metadata", pluginLogArgs...)
		}

		if stage == PluginStageFormatter && pluginOutput.Output != "" {
			result.Output = pluginOutput.Output
			result.IsFinalOutput = true
			result.FinalPlugin = pluginCfg.Name
			p.logger.Debug("Formatter plugin provided final output", pluginLogArgs...)
			break
		}
	}

	return result, nil
}

// generateFrontMatter marshals data into YAML or TOML format including delimiters.
// ... (implementation remains the same) ...
func generateFrontMatter(data map[string]interface{}, format string) ([]byte, error) {
	var buf bytes.Buffer
	var marshaledBytes []byte
	var err error

	if data == nil {
		data = make(map[string]interface{})
	}

	switch format {
	case "yaml":
		marshaledBytes, err = yaml.Marshal(data)
		if err == nil {
			buf.WriteString("---\n")
			buf.Write(marshaledBytes)
			if !bytes.HasSuffix(marshaledBytes, []byte("\n")) {
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
			if !bytes.HasSuffix(marshaledBytes, []byte("\n")) {
				buf.WriteString("\n")
			}
			buf.WriteString("+++\n")
		}
	default:
		return nil, fmt.Errorf("%w: unsupported front matter format: %s", ErrConfigValidation, format)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: failed to marshal front matter to %s: %w", ErrFrontMatterGen, format, err)
	}

	return buf.Bytes(), nil
}

// truncateContent reduces content size based on config string (bytes or lines).
// ... (implementation remains the same) ...
func truncateContent(content []byte, cfg string) ([]byte, error) {
	cfg = strings.TrimSpace(strings.ToLower(cfg))
	var limit int64 = -1
	isLineLimit := false

	if strings.HasSuffix(cfg, " lines") || strings.HasSuffix(cfg, " line") {
		lineStr := strings.Fields(cfg)[0]
		lineLimit, err := strconv.ParseInt(lineStr, 10, 64)
		if err == nil && lineLimit >= 0 {
			limit = lineLimit
			isLineLimit = true
		} else {
			return nil, fmt.Errorf("%w: invalid line limit format in truncate config: %s", ErrConfigValidation, cfg)
		}
	} else {
		sizeStr := cfg
		multiplier := int64(1)
		units := []struct {
			suffix string
			mult   int64
		}{
			{"kb", 1024}, {"k", 1024},
			{"mb", 1024 * 1024}, {"m", 1024 * 1024},
			{"gb", 1024 * 1024 * 1024}, {"g", 1024 * 1024 * 1024},
			{"b", 1},
		}
		numericPart := sizeStr
		for _, unit := range units {
			if strings.HasSuffix(sizeStr, unit.suffix) {
				numericPart = strings.TrimSuffix(sizeStr, unit.suffix)
				multiplier = unit.mult
				break
			}
		}
		numericPart = strings.TrimSpace(numericPart)
		byteLimit, err := strconv.ParseInt(numericPart, 10, 64)
		if err == nil && byteLimit >= 0 {
			limit = byteLimit * multiplier
		} else {
			return nil, fmt.Errorf("%w: invalid size format in truncate config: %s", ErrConfigValidation, cfg)
		}
	}

	if limit < 0 {
		return nil, fmt.Errorf("%w: calculated limit is negative, invalid config: %s", ErrConfigValidation, cfg)
	}

	if isLineLimit {
		if limit == 0 {
			return []byte{}, nil
		}
		count := int64(0)
		endIndex := 0
		for i, b := range content {
			if b == '\n' {
				count++
				if count == limit {
					endIndex = i + 1
					break
				}
			}
			endIndex = i + 1
		}
		return content[:endIndex], nil
	} else { // Byte limit
		if int64(len(content)) <= limit {
			return content, nil
		}
		byteLimit := limit
		for byteLimit > 0 && (content[byteLimit]&0xC0) == 0x80 {
			byteLimit--
		}
		if byteLimit < 0 {
			byteLimit = 0
		}
		return content[:byteLimit], nil
	}
}

// --- END OF FINAL REVISED FILE pkg/converter/processor.go ---

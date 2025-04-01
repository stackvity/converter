// --- START OF FINAL REVISED FILE pkg/converter/engine.go ---
package converter

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	// Import necessary subpackages
	"github.com/stackvity/stack-converter/pkg/converter/analysis"
	"github.com/stackvity/stack-converter/pkg/converter/cache" // Correctly imported
	"github.com/stackvity/stack-converter/pkg/converter/encoding"
	"github.com/stackvity/stack-converter/pkg/converter/language"
	"github.com/stackvity/stack-converter/pkg/converter/template"
)

// ProcessorFactory defines a function type for creating FileProcessors.
type ProcessorFactory func(
	opts *Options,
	loggerHandler slog.Handler,
	cacheMgr CacheManager, // Use the interface type defined in this package
	langDet language.LanguageDetector,
	encHandler encoding.EncodingHandler,
	analysisEng analysis.AnalysisEngine,
	gitCl GitClient, // Use the interface type defined in this package
	pluginRun PluginRunner, // Use the interface type defined in this package
	tplExecutor template.TemplateExecutor,
) *FileProcessor

// WalkerFactory defines a function type for creating Walkers.
type WalkerFactory func(
	opts *Options,
	workerChan chan<- string,
	wg *sync.WaitGroup,
	loggerHandler slog.Handler,
) (*Walker, error)

// Engine orchestrates the documentation generation process.
type Engine struct {
	opts             *Options
	logger           *slog.Logger
	cacheManager     CacheManager // Use the interface type defined in this package
	processorFactory ProcessorFactory
	walkerFactory    WalkerFactory
	processor        *FileProcessor // Instance created using the factory in Run()
	walker           *Walker        // Instance created using the factory
	aggregator       *reportAggregator
	ctx              context.Context
	cancelFunc       context.CancelFunc
	concurrency      int
	totalScanned     atomic.Int64 // Counts files whose results were received by aggregator
	fatalOccurred    atomic.Bool
}

// NewEngine creates and initializes a new Engine instance, validating options and setting up dependencies.
func NewEngine(ctx context.Context, opts Options) (*Engine, error) { // minimal comment
	if opts.Logger == nil {
		return nil, fmt.Errorf("%w: Logger implementation (slog.Handler) cannot be nil", ErrConfigValidation)
	}
	if opts.EventHooks == nil {
		opts.EventHooks = &NoOpHooks{}
	}

	logger := slog.New(opts.Logger).With(slog.String("component", "engine"))

	// --- Dependency Initialization & Validation ---
	// Initialize cacheMgr with a default before checking CacheEnabled
	var cacheMgr CacheManager = &NoOpCacheManager{} // Default to NoOp

	if opts.CacheManager != nil {
		// Use injected manager if provided
		cacheMgr = opts.CacheManager
		logger.Debug("Using provided CacheManager implementation.")
	} else if opts.CacheEnabled {
		// Attempt to initialize FileCacheManager only if enabled and no manager injected
		if opts.CacheFilePath == "" && opts.OutputPath != "" {
			opts.CacheFilePath = filepath.Join(opts.OutputPath, cache.CacheFileName)
			logger.Debug("CacheFilePath not set, defaulting", "path", opts.CacheFilePath)
		}

		if opts.CacheFilePath != "" {
			// *** Use AppVersion from Options for cache compatibility checks ***
			appVersion := opts.AppVersion
			if appVersion == "" {
				appVersion = "dev" // Fallback if not provided by caller
				logger.Warn("AppVersion not set in Options, using 'dev' for cache compatibility. Cache may be invalid across builds.")
			}

			// --- Placeholder for FileCacheManager Initialization ---
			// NOTE: Assumes NewFileCacheManager constructor exists in the cache package.
			// This will cause a compile error until implemented there.
			// Using NoOpCacheManager here to allow engine.go to compile standalone.
			// Replace the next line with the actual call when ready:
			// defaultCacheMgr := cache.NewFileCacheManager(logger.Handler(), cache.CacheSchemaVersion, appVersion)
			logger.Warn("PLACEHOLDER: Attempting to initialize FileCacheManager, but constructor likely missing in cache package. Using NoOp manager.")
			defaultCacheMgr := &NoOpCacheManager{} // Using NoOp as placeholder
			// --- End Placeholder ---

			logger.Debug("Initializing FileCacheManager (placeholder used)", "path", opts.CacheFilePath, "schemaVersion", cache.CacheSchemaVersion, "appVersion", appVersion)

			// Attempt to load using the (potentially placeholder) manager
			cacheLoadErr := defaultCacheMgr.Load(opts.CacheFilePath) // Load might be no-op for placeholder
			if cacheLoadErr != nil {
				// Loading logic might differ slightly depending on placeholder vs real implementation
				if errors.Is(cacheLoadErr, os.ErrNotExist) {
					logger.Info("Cache file not found, creating new cache.", "path", opts.CacheFilePath)
				} else if errors.Is(cacheLoadErr, cache.ErrCacheLoad) {
					logger.Warn("Failed to load cache file (corruption or version mismatch?), treating as miss.", "path", opts.CacheFilePath, "error", cacheLoadErr.Error())
				} else {
					logger.Error("Critical error interacting with cache file, proceeding without cache.", "path", opts.CacheFilePath, "error", cacheLoadErr.Error())
					// Fallback to confirmed NoOp if loading had critical issues
					cacheMgr = &NoOpCacheManager{}
					opts.CacheEnabled = false // Force disable if critical load error
				}
				// Assign manager even if load failed (it might create a new one)
				// If placeholder was used, cacheMgr remains NoOp. If real manager used, it's assigned.
				if cacheMgr == nil { // Only assign if not already set to NoOp due to critical error
					cacheMgr = defaultCacheMgr
				}
			} else {
				logger.Info("Cache loaded successfully (or placeholder load completed)", "path", opts.CacheFilePath)
				cacheMgr = defaultCacheMgr // Assign the successfully loaded manager
			}
		} else {
			logger.Warn("Cache enabled but CacheFilePath could not be determined. Using NoOpCacheManager.")
			cacheMgr = &NoOpCacheManager{} // Ensure NoOp is assigned
		}
	} else {
		// Cache explicitly disabled, ensure NoOp manager is used
		logger.Debug("Cache explicitly disabled. Using NoOpCacheManager.")
		cacheMgr = &NoOpCacheManager{}
	}

	opts.CacheManager = cacheMgr // Assign the finally resolved cache manager back to options

	// Initialize defaults for other dependencies if not provided
	if opts.LanguageDetector == nil {
		opts.LanguageDetector = language.NewGoEnryDetector(opts.LanguageDetectionConfidenceThreshold, opts.LanguageMappingsOverride)
		logger.Debug("LanguageDetector not provided, using default GoEnryDetector.")
	}
	if opts.EncodingHandler == nil {
		opts.EncodingHandler = encoding.NewGoCharsetEncodingHandler(opts.DefaultEncoding)
		logger.Debug("EncodingHandler not provided, using default GoCharsetEncodingHandler.")
	}
	if opts.AnalysisEngine == nil {
		opts.AnalysisEngine = analysis.NewDefaultAnalysisEngine(opts.Logger)
		logger.Debug("AnalysisEngine not provided, using default.")
	}
	if opts.TemplateExecutor == nil {
		opts.TemplateExecutor = template.NewGoTemplateExecutor()
		logger.Debug("TemplateExecutor not provided, using default GoTemplateExecutor.")
	}
	// Check mandatory dependencies for certain modes
	if opts.GitClient == nil && opts.GitDiffMode != GitDiffModeNone {
		// Assume GitClient interface is defined in this package or pkg/converter/git
		return nil, fmt.Errorf("%w: GitClient required but not provided for git diff mode '%s'", ErrConfigValidation, opts.GitDiffMode)
	}
	anyPluginsEnabled := false
	for _, p := range opts.PluginConfigs {
		if p.Enabled {
			anyPluginsEnabled = true
			break
		}
	}
	if opts.PluginRunner == nil && anyPluginsEnabled {
		// Assume PluginRunner interface is defined in this package or pkg/converter/plugin
		return nil, fmt.Errorf("%w: PluginRunner required but not provided when plugins are enabled", ErrConfigValidation)
	}

	// Validate paths
	if _, err := os.Stat(opts.InputPath); err != nil {
		return nil, fmt.Errorf("%w: cannot access input path '%s': %w", ErrConfigValidation, opts.InputPath, err)
	}
	if err := os.MkdirAll(opts.OutputPath, 0755); err != nil {
		return nil, fmt.Errorf("%w: cannot create or access output directory '%s': %w", ErrConfigValidation, opts.OutputPath, err)
	}

	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = runtime.NumCPU()
		opts.Concurrency = concurrency
		logger.Debug("Concurrency auto-detected", "count", concurrency)
	}

	// Use provided factories or default constructors
	processorFactory := opts.ProcessorFactory
	if processorFactory == nil {
		processorFactory = NewFileProcessor
		logger.Debug("ProcessorFactory not provided, using default.")
	}
	walkerFactory := opts.WalkerFactory
	if walkerFactory == nil {
		walkerFactory = NewWalker
		logger.Debug("WalkerFactory not provided, using default.")
	}

	engineCtx, cancelFunc := context.WithCancel(ctx)

	engine := &Engine{
		opts:             &opts,
		logger:           logger,
		cacheManager:     cacheMgr, // Use the resolved cache manager
		processorFactory: processorFactory,
		walkerFactory:    walkerFactory,
		aggregator:       newReportAggregator(),
		ctx:              engineCtx,
		cancelFunc:       cancelFunc,
		concurrency:      concurrency,
	}

	return engine, nil
}

// Run starts the documentation generation process orchestrated by the Engine.
func (e *Engine) Run() (Report, error) { // minimal comment
	startTime := time.Now()
	e.logger.Info("Starting documentation generation run", "concurrency", e.concurrency, "cacheEnabled", e.opts.CacheEnabled)
	var finalErr error

	// Ensure context cancellation and cache persistence happen even on panic
	defer func() {
		// Recover from potential panics in workers or other logic
		if r := recover(); r != nil {
			e.logger.Error("Panic recovered during engine run", "panicValue", r)
			e.fatalOccurred.Store(true)
			if finalErr == nil {
				finalErr = fmt.Errorf("panic during execution: %v", r)
			}
			// Capture stack trace if possible
			// buf := make([]byte, 1<<16)
			// stackSize := runtime.Stack(buf, true)
			// e.logger.Error("Panic Stack Trace", "stack", string(buf[:stackSize]))
		}

		e.cancelFunc() // Ensure context is always cancelled on exit

		// Persist Cache
		if e.opts.CacheEnabled && e.cacheManager != nil {
			e.logger.Debug("Persisting cache index", "path", e.opts.CacheFilePath)
			if persistErr := e.cacheManager.Persist(e.opts.CacheFilePath); persistErr != nil {
				e.logger.Error("Failed to persist cache index", slog.String("path", e.opts.CacheFilePath), slog.String("error", persistErr.Error()))
				if finalErr == nil {
					// Report cache persist error only if no other fatal error occurred
					// Use the exported error variable from the cache package
					finalErr = fmt.Errorf("failed to persist cache: %w", cache.ErrCachePersist)
				}
			}
		}

		fatal := e.fatalOccurred.Load()
		finalReport := e.aggregator.getReport(e.opts, startTime, e.totalScanned.Load(), fatal)
		e.logger.Info("Documentation generation run finished",
			slog.Duration("duration", time.Since(startTime)),
			slog.Int("processed", finalReport.Summary.ProcessedCount),
			slog.Int("cached", finalReport.Summary.CachedCount),
			slog.Int("skipped", finalReport.Summary.SkippedCount),
			slog.Int("errors", finalReport.Summary.ErrorCount),
			slog.Bool("fatalErrorOccurred", finalReport.Summary.FatalErrorOccurred),
		)

		// Trigger final hook
		if e.opts.EventHooks != nil {
			if hookErr := e.opts.EventHooks.OnRunComplete(finalReport); hookErr != nil {
				e.logger.Warn("OnRunComplete hook returned an error", slog.String("error", hookErr.Error()))
			}
		}
	}()

	// Initialize Processor using the factory *before* starting workers
	// Ensure cacheManager (resolved above) is passed, not opts.CacheManager which might be nil initially
	e.processor = e.processorFactory(
		e.opts, e.logger.Handler(), e.cacheManager, e.opts.LanguageDetector,
		e.opts.EncodingHandler, e.opts.AnalysisEngine, e.opts.GitClient,
		e.opts.PluginRunner, e.opts.TemplateExecutor,
	)

	workerChan := make(chan string, e.concurrency)
	resultsChan := make(chan interface{}, e.concurrency)
	var wg sync.WaitGroup

	e.startWorkers(&wg, workerChan, resultsChan)

	// Initialize Walker using the factory
	walker, walkInitErr := e.walkerFactory(e.opts, workerChan, &wg, e.logger.Handler())
	if walkInitErr != nil {
		e.logger.Error("Failed to initialize directory walker", slog.String("error", walkInitErr.Error()))
		e.fatalOccurred.Store(true)
		close(workerChan) // Close channel before waiting
		wg.Wait()         // Wait for any potentially started workers to finish
		finalErr = fmt.Errorf("walker initialization failed: %w", walkInitErr)
		// Need to wait for aggregator too if resultsChan was created
		close(resultsChan) // Close results channel
		aggregatorDone := make(chan struct{})
		go e.aggregateResults(resultsChan, aggregatorDone) // Start aggregator to drain channel
		<-aggregatorDone                                   // Wait for aggregator to finish
		return e.aggregator.getReport(e.opts, startTime, 0, true), finalErr
	}
	e.walker = walker

	aggregatorDone := make(chan struct{})
	go e.aggregateResults(resultsChan, aggregatorDone)

	walkerDone := make(chan error, 1)
	go func() {
		defer close(walkerDone)
		walkerErr := e.walker.StartWalk(e.ctx) // Use engine's cancellable context
		if walkerErr != nil && !errors.Is(walkerErr, context.Canceled) && !errors.Is(walkerErr, context.DeadlineExceeded) {
			e.logger.Error("Directory walk failed critically", slog.String("error", walkerErr.Error()))
			walkerDone <- walkerErr
			if !e.fatalOccurred.Load() {
				e.fatalOccurred.Store(true)
				e.cancelFunc()
			}
		}
	}()

	finalWalkErr := <-walkerDone
	// Worker channel (`workerChan`) is closed by the walker upon completion or error.
	// Wait for workers to finish processing any remaining items in the channel.
	wg.Wait()
	// Close results channel only *after* all workers are done.
	close(resultsChan)
	// Wait for the aggregator to process all results.
	<-aggregatorDone

	// Determine final error, wrapping underlying cause
	if ctxErr := e.ctx.Err(); ctxErr != nil {
		if !e.fatalOccurred.Load() {
			e.logger.Info("Processing run cancelled", slog.String("reason", ctxErr.Error()))
		}
		e.fatalOccurred.Store(true)
		finalErr = ctxErr
	} else if finalWalkErr != nil {
		// fatalOccurred should already be set by walker goroutine
		finalErr = fmt.Errorf("directory walk failed: %w", finalWalkErr)
	} else if e.fatalOccurred.Load() {
		firstFatal := e.aggregator.getFirstFatalError()
		if firstFatal != nil {
			finalErr = fmt.Errorf("processing stopped due to fatal error: %w", firstFatal)
		} else {
			finalErr = errors.New("processing stopped due to fatal error")
		}
	}

	// Return the aggregated report and the determined final error (if any)
	return e.aggregator.getReport(e.opts, startTime, e.totalScanned.Load(), e.fatalOccurred.Load()), finalErr
}

// startWorkers launches the worker goroutines.
func (e *Engine) startWorkers(wg *sync.WaitGroup, workerChan <-chan string, resultsChan chan<- interface{}) { // minimal comment
	e.logger.Debug("Starting worker pool", "count", e.concurrency)
	for i := 0; i < e.concurrency; i++ {
		wg.Add(1)
		go e.processFilesWorker(wg, i, workerChan, resultsChan)
	}
}

// processFilesWorker is the main function executed by each worker goroutine.
func (e *Engine) processFilesWorker(wg *sync.WaitGroup, workerID int, workerChan <-chan string, resultsChan chan<- interface{}) { // minimal comment
	defer func() {
		// Recover from panics within a worker to prevent crashing the whole engine
		if r := recover(); r != nil {
			wLogger := e.logger.With(slog.Int("workerID", workerID))
			wLogger.Error("Panic recovered in worker", "panicValue", r)
			// Attempt to report a generic error for this worker's failure
			// Path might not be available if panic occurred early
			// Ensure resultsChan is not closed before sending
			func() {
				defer func() { recover() }() // Prevent panic if resultsChan is closed
				resultsChan <- ErrorInfo{Path: "unknown (panic)", Error: fmt.Sprintf("panic: %v", r), IsFatal: true}
			}()
			if !e.fatalOccurred.Load() {
				e.fatalOccurred.Store(true)
				e.cancelFunc() // Signal stop due to panic
			}
		}
		wg.Done() // Ensure WaitGroup is decremented even on panic
	}()

	wLogger := e.logger.With(slog.Int("workerID", workerID))
	wLogger.Debug("Worker started")

	for {
		select {
		case filePath, ok := <-workerChan:
			if !ok {
				wLogger.Debug("Worker shutting down (channel closed)")
				return
			}

			relPath, _ := filepath.Rel(e.opts.InputPath, filePath)
			if relPath == "" || relPath == "." {
				relPath = filepath.Base(filePath)
			}
			relPath = filepath.ToSlash(relPath)
			wLogger.Debug("Processing file", "path", relPath)

			if e.processor == nil {
				wLogger.Error("Internal error: FileProcessor not initialized for worker")
				errInfo := ErrorInfo{Path: relPath, Error: "internal error: processor not initialized", IsFatal: true}
				// Ensure resultsChan is not closed before sending
				func() {
					defer func() { recover() }()
					resultsChan <- errInfo
				}()
				if !e.fatalOccurred.Load() {
					e.fatalOccurred.Store(true)
					e.cancelFunc()
				}
				continue
			}

			// Pass the engine's cancellable context to the processor
			result, status, err := e.processor.ProcessFile(e.ctx, filePath)

			// Ensure resultsChan is not closed before sending
			func() {
				defer func() { recover() }() // Prevent panic if resultsChan is closed concurrently

				if err != nil {
					// Use OnErrorStop from opts, not defaultOnErrorStop
					isFatal := status == StatusFailed && e.opts.OnErrorMode == OnErrorStop
					errorInfo := ErrorInfo{Path: relPath, Error: err.Error(), IsFatal: isFatal}
					if ei, ok := result.(ErrorInfo); ok { // Check if result itself is already ErrorInfo
						errorInfo = ei              // Use the richer ErrorInfo from processor
						errorInfo.IsFatal = isFatal // Ensure IsFatal reflects the current OnErrorMode
					}
					resultsChan <- errorInfo
					if isFatal && !e.fatalOccurred.Load() {
						wLogger.Info("Worker detected fatal error condition, signalling stop", "path", relPath, "error", err)
						e.fatalOccurred.Store(true)
						e.cancelFunc()
					}
				} else if result != nil {
					resultsChan <- result
				} else {
					// Handle case where processor returns (nil, StatusSuccess, nil) - less likely but possible
					wLogger.Warn("Processor returned nil result and nil error", "path", relPath, "status", status)
					// Report as an internal error? Or assume success based on status?
					// Let's report it as an error for clarity.
					isFatal := e.opts.OnErrorMode == OnErrorStop
					errInfo := ErrorInfo{Path: relPath, Error: "internal error: processor returned nil result without error", IsFatal: isFatal}
					resultsChan <- errInfo
					if isFatal && !e.fatalOccurred.Load() {
						e.fatalOccurred.Store(true)
						e.cancelFunc()
					}
				}
			}()

		case <-e.ctx.Done():
			wLogger.Debug("Worker shutting down (context cancelled)")
			return
		}
	}
}

// aggregateResults reads results from the resultsChan and updates the reportAggregator.
func (e *Engine) aggregateResults(resultsChan <-chan interface{}, done chan<- struct{}) { // minimal comment
	defer close(done)
	e.logger.Debug("Result aggregator started")
	scanCount := int64(0)
	for result := range resultsChan {
		scanCount++
		switch r := result.(type) {
		case FileInfo:
			e.aggregator.addProcessed(r)
			if r.CacheStatus == CacheStatusHit {
				e.aggregator.addCached(r)
			}
		case SkippedInfo:
			e.aggregator.addSkipped(r)
		case ErrorInfo:
			e.aggregator.addError(r)
		default:
			// Log if an unexpected type is received, could indicate programming error
			e.logger.Warn("Aggregator received unknown result type", "type", fmt.Sprintf("%T", result))
		}
	}
	e.totalScanned.Store(scanCount)
	e.logger.Debug("Result aggregator finished", "resultsProcessed", scanCount)
}

// --- reportAggregator ---

// reportAggregator manages the collection of results during the run.
type reportAggregator struct {
	mu             sync.Mutex
	processedFiles []FileInfo
	skippedFiles   []SkippedInfo
	errors         []ErrorInfo
	cachedCount    int
	warningCount   int // Placeholder for actual warning tracking if needed
}

// newReportAggregator creates a new report aggregator.
func newReportAggregator() *reportAggregator { // minimal comment
	return &reportAggregator{
		processedFiles: make([]FileInfo, 0, 512), // Preallocate slightly
		skippedFiles:   make([]SkippedInfo, 0, 128),
		errors:         make([]ErrorInfo, 0, 32),
	}
}

// addProcessed appends a FileInfo to the list (thread-safe).
func (a *reportAggregator) addProcessed(info FileInfo) { // minimal comment
	a.mu.Lock()
	a.processedFiles = append(a.processedFiles, info)
	a.mu.Unlock()
}

// addCached increments the cached count (thread-safe).
func (a *reportAggregator) addCached(_ FileInfo) { // minimal comment
	a.mu.Lock()
	a.cachedCount++
	a.mu.Unlock()
}

// addSkipped appends a SkippedInfo to the list (thread-safe).
func (a *reportAggregator) addSkipped(info SkippedInfo) { // minimal comment
	a.mu.Lock()
	a.skippedFiles = append(a.skippedFiles, info)
	a.mu.Unlock()
}

// addError appends an ErrorInfo to the list (thread-safe).
func (a *reportAggregator) addError(info ErrorInfo) { // minimal comment
	a.mu.Lock()
	a.errors = append(a.errors, info)
	a.mu.Unlock()
}

// getFirstFatalError finds the first recorded error marked as fatal.
func (a *reportAggregator) getFirstFatalError() error { // minimal comment
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range a.errors {
		if e.IsFatal {
			// Return a standard error wrapping the original message for context
			return fmt.Errorf("fatal error processing file '%s': %s", e.Path, e.Error)
		}
	}
	return nil
}

// getReport compiles and returns the final Report struct.
func (a *reportAggregator) getReport(opts *Options, startTime time.Time, totalScanned int64, fatalOccurred bool) Report { // minimal comment
	a.mu.Lock()
	// Create copies of slices to avoid returning internal state directly
	processedCount := len(a.processedFiles)
	cachedCount := a.cachedCount // Already protected by addCached lock
	skippedCount := len(a.skippedFiles)
	errorCount := len(a.errors)
	warningCount := a.warningCount // Assuming this is managed thread-safely if used

	processed := make([]FileInfo, processedCount)
	copy(processed, a.processedFiles)
	skipped := make([]SkippedInfo, skippedCount)
	copy(skipped, a.skippedFiles)
	errorsList := make([]ErrorInfo, errorCount)
	copy(errorsList, a.errors)
	a.mu.Unlock() // Unlock after copying

	// Note: TotalFilesScanned reflects files *attempted* by workers (results received by aggregator).
	// This might be less than files *discovered* by the walker if cancellation occurs early.
	return Report{
		Summary: ReportSummary{
			InputPath:          opts.InputPath,
			OutputPath:         opts.OutputPath,
			ProfileUsed:        opts.ProfileName,
			ConfigFilePath:     opts.ConfigFilePath,
			TotalFilesScanned:  int(totalScanned), // Count of results aggregated
			ProcessedCount:     processedCount,
			CachedCount:        cachedCount,
			SkippedCount:       skippedCount,
			WarningCount:       warningCount,
			ErrorCount:         errorCount,
			FatalErrorOccurred: fatalOccurred,
			DurationSeconds:    time.Since(startTime).Seconds(),
			CacheEnabled:       opts.CacheEnabled,
			Concurrency:        opts.Concurrency,
			Timestamp:          time.Now().UTC(),
			SchemaVersion:      ReportSchemaVersion, // Use constant from this package
		},
		ProcessedFiles: processed,
		SkippedFiles:   skipped,
		Errors:         errorsList,
	}
}

// --- END OF FINAL REVISED FILE pkg/converter/engine.go ---

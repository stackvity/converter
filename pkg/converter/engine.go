// --- START OF FINAL REVISED FILE pkg/converter/engine.go ---
package converter

import (
	"context" // Import errors package for Is checks
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
	"github.com/stackvity/stack-converter/pkg/converter/cache" // Correct import path
	"github.com/stackvity/stack-converter/pkg/converter/encoding"
	"github.com/stackvity/stack-converter/pkg/converter/git" // Import git package for GitClient interface
	"github.com/stackvity/stack-converter/pkg/converter/language"
	"github.com/stackvity/stack-converter/pkg/converter/plugin" // Import plugin package for PluginRunner interface
	"github.com/stackvity/stack-converter/pkg/converter/template"
)

// ProcessorFactory defines a function type for creating FileProcessors.
// **MODIFIED:** Updated signature to use imported interface types explicitly.
type ProcessorFactory func(
	opts *Options,
	loggerHandler slog.Handler,
	cacheMgr cache.CacheManager, // Use type from cache package
	langDet language.LanguageDetector,
	encHandler encoding.EncodingHandler,
	analysisEng analysis.AnalysisEngine,
	gitCl git.GitClient, // Use type from git package
	pluginRun plugin.PluginRunner, // Use type from plugin package
	tplExecutor template.TemplateExecutor,
) *FileProcessor

// WalkerFactory defines a function type for creating Walkers.
// **MODIFIED:** Updated signature to match NewWalker.
type WalkerFactory func(
	opts *Options,
	workerChan chan<- string,
	wg *sync.WaitGroup,
	loggerHandler slog.Handler,
) (*Walker, error)

// Engine orchestrates the documentation generation process.
// **MODIFIED:** Added dependencies and state fields.
type Engine struct {
	opts             *Options
	logger           *slog.Logger
	cacheManager     cache.CacheManager // Use type from cache package
	processorFactory ProcessorFactory
	walkerFactory    WalkerFactory
	processor        *FileProcessor // Instance created using the factory in Run()
	walker           *Walker        // Instance created using the factory
	aggregator       *reportAggregator
	ctx              context.Context
	cancelFunc       context.CancelFunc
	concurrency      int
	totalScanned     atomic.Int64 // Counts files discovered by Walker
	totalResults     atomic.Int64 // Counts results received by Aggregator
	fatalOccurred    atomic.Bool
}

// NewEngine creates and initializes a new Engine instance, validating options and setting up dependencies.
// **MODIFIED:** Implemented dependency resolution and validation logic.
func NewEngine(ctx context.Context, opts Options) (*Engine, error) {
	// === Basic Validation ===
	if opts.Logger == nil {
		// Cannot log if logger is nil, return plain error
		return nil, fmt.Errorf("%w: Logger implementation (slog.Handler) cannot be nil", ErrConfigValidation)
	}
	logger := slog.New(opts.Logger).With(slog.String("component", "engine"))

	if opts.EventHooks == nil {
		// Default to NoOpHooks if none provided
		opts.EventHooks = &NoOpHooks{}
		logger.Debug("EventHooks not provided, defaulting to NoOpHooks.")
	}

	// === Dependency Initialization & Validation ===
	var cacheMgr cache.CacheManager = &NoOpCacheManager{} // Default before check

	if opts.CacheEnabled {
		logger.Debug("Cache enabled, attempting to initialize CacheManager.")
		if opts.CacheManager != nil {
			// Use injected manager if provided
			cacheMgr = opts.CacheManager
			logger.Debug("Using provided CacheManager implementation.")
		} else {
			// Attempt to initialize default FileCacheManager
			if opts.CacheFilePath == "" {
				if opts.OutputPath != "" {
					opts.CacheFilePath = filepath.Join(opts.OutputPath, cache.CacheFileName)
					logger.Debug("CacheFilePath not set, defaulting", "path", opts.CacheFilePath)
				} else {
					// Cannot determine cache path if OutputPath is also missing (should be caught earlier, but defensive)
					logger.Error("Cache enabled but cannot determine CacheFilePath (OutputPath is empty). Disabling cache.")
					opts.CacheEnabled = false
					// cacheMgr remains NoOpCacheManager
				}
			}

			if opts.CacheEnabled && opts.CacheFilePath != "" { // Check again if path was determined
				// Use AppVersion from Options for cache compatibility checks
				appVersion := opts.AppVersion
				if appVersion == "" {
					appVersion = "dev" // Fallback if not provided by caller
					logger.Warn("AppVersion not set in Options, using 'dev' for cache compatibility. Cache may be invalid across builds.")
				}

				// Use cache.CacheSchemaVersion from the cache package
				logger.Debug("Initializing FileCacheManager", "path", opts.CacheFilePath, "schemaVersion", cache.CacheSchemaVersion, "appVersion", appVersion)

				// Instantiate the default FileCacheManager from the cache package
				// Assuming NewFileCacheManager constructor exists and returns converter.CacheManager interface
				// Pass cache format from options, default handled inside constructor
				defaultCacheMgr := cache.NewFileCacheManager(logger.Handler(), cache.CacheSchemaVersion, appVersion, cache.DefaultCacheFormat) // Assuming format is passed or handled

				// Attempt to load using the default manager
				cacheLoadErr := defaultCacheMgr.Load(opts.CacheFilePath)
				if cacheLoadErr != nil {
					// Check specific cache load error from the cache package
					if errors.Is(cacheLoadErr, os.ErrNotExist) {
						logger.Info("Cache file not found, will create new cache.", "path", opts.CacheFilePath)
						// It's okay, manager will create it on Persist
						cacheMgr = defaultCacheMgr // Use the manager even if file didn't exist
					} else if errors.Is(cacheLoadErr, cache.ErrCacheLoad) {
						// Treat non-critical load errors (corruption, version mismatch handled by Load) as a miss
						logger.Warn("Failed to load cache file (corruption or version mismatch?), treating as miss.", "path", opts.CacheFilePath, "error", cacheLoadErr.Error())
						cacheMgr = defaultCacheMgr // Use the manager, but it will start with an empty index
					} else {
						// Treat other critical I/O errors (e.g., permissions) during load more seriously
						logger.Error("Critical error loading cache file, proceeding without cache.", "path", opts.CacheFilePath, "error", cacheLoadErr.Error())
						cacheMgr = &NoOpCacheManager{} // Fallback to confirmed NoOp
						opts.CacheEnabled = false      // Force disable cache
					}
				} else {
					logger.Info("Cache loaded successfully from file.", "path", opts.CacheFilePath)
					cacheMgr = defaultCacheMgr // Assign the successfully loaded manager
				}
			} else if opts.CacheEnabled { // If cache was enabled but path couldn't be set
				logger.Warn("Cache enabled but CacheFilePath could not be determined. Disabling cache.")
				opts.CacheEnabled = false
				// cacheMgr remains NoOpCacheManager
			}
		}
	} else {
		logger.Debug("Cache explicitly disabled. Using NoOpCacheManager.")
		// cacheMgr remains NoOpCacheManager
	}
	// Ensure the resolved manager is stored back into options for the processor/walker
	opts.CacheManager = cacheMgr

	// Initialize defaults for other dependencies if not provided in opts
	if opts.LanguageDetector == nil {
		opts.LanguageDetector = language.NewGoEnryDetector(opts.LanguageDetectionConfidenceThreshold, opts.LanguageMappingsOverride)
		logger.Debug("LanguageDetector not provided, using default GoEnryDetector.")
	}
	if opts.EncodingHandler == nil {
		opts.EncodingHandler = encoding.NewGoCharsetEncodingHandler(opts.DefaultEncoding)
		logger.Debug("EncodingHandler not provided, using default GoCharsetEncodingHandler.")
	}
	if opts.AnalysisEngine == nil {
		// Pass the configured logger handler
		opts.AnalysisEngine = analysis.NewDefaultAnalysisEngine(opts.Logger)
		logger.Debug("AnalysisEngine not provided, using default.")
	}
	if opts.TemplateExecutor == nil {
		opts.TemplateExecutor = template.NewGoTemplateExecutor()
		logger.Debug("TemplateExecutor not provided, using default GoTemplateExecutor.")
	}
	// Note: GitClient and PluginRunner defaults might be handled later or in CLI layer,
	// but check for requirement violations here based on config.
	if opts.GitClient == nil && opts.GitDiffMode != GitDiffModeNone {
		// Return fatal config error if Git features need a client but none provided/defaultable
		return nil, fmt.Errorf("%w: GitClient required but not provided for git diff mode '%s'", ErrConfigValidation, opts.GitDiffMode)
	}
	anyPluginsEnabled := false
	for _, p := range opts.PluginConfigs {
		if p.Enabled {
			anyPluginsEnabled = true
			break
		}
	}
	// Ensure opts.PluginRunner is checked against plugin.PluginRunner type if options.go uses it
	if opts.PluginRunner == nil && anyPluginsEnabled {
		// Return fatal config error if plugins enabled but no runner provided/defaultable
		return nil, fmt.Errorf("%w: PluginRunner required but not provided when plugins are enabled", ErrConfigValidation)
	}

	// === Path Validation ===
	if opts.InputPath == "" {
		// Should have been caught by caller (config loading), but double-check
		return nil, fmt.Errorf("%w: input path cannot be empty", ErrConfigValidation)
	}
	if _, err := os.Stat(opts.InputPath); err != nil {
		return nil, fmt.Errorf("%w: cannot access input path '%s': %w", ErrConfigValidation, opts.InputPath, err)
	}
	if opts.OutputPath == "" {
		// Should have been caught by caller (config loading), but double-check
		return nil, fmt.Errorf("%w: output path cannot be empty", ErrConfigValidation)
	}
	// Ensure output path exists (create if needed) - done by caller (config loading) usually, but good to ensure here too
	if err := os.MkdirAll(opts.OutputPath, 0755); err != nil {
		return nil, fmt.Errorf("%w: cannot create or access output directory '%s': %w", ErrConfigValidation, opts.OutputPath, err)
	}

	// === Concurrency Setup ===
	concurrency := opts.Concurrency
	if concurrency <= 0 {
		concurrency = runtime.NumCPU()
		opts.Concurrency = concurrency // Update opts if auto-detected
		logger.Debug("Concurrency auto-detected", "count", concurrency)
	} else {
		logger.Debug("Using configured concurrency", "count", concurrency)
	}

	// === Factory Resolution ===
	processorFactory := opts.ProcessorFactory
	if processorFactory == nil {
		processorFactory = NewFileProcessor // Use default constructor
		logger.Debug("ProcessorFactory not provided, using default NewFileProcessor.")
	}
	walkerFactory := opts.WalkerFactory
	if walkerFactory == nil {
		walkerFactory = NewWalker // Use default constructor
		logger.Debug("WalkerFactory not provided, using default NewWalker.")
	}

	// === Context Setup ===
	// Create a new cancellable context derived from the one passed in.
	// This allows the engine to manage its own cancellation scope if needed,
	// while still respecting cancellation from the parent context.
	engineCtx, cancelFunc := context.WithCancel(ctx)

	// === Engine Instantiation ===
	engine := &Engine{
		opts:             &opts, // Store pointer to potentially modified options
		logger:           logger,
		cacheManager:     opts.CacheManager, // Use the resolved cache manager
		processorFactory: processorFactory,
		walkerFactory:    walkerFactory,
		aggregator:       newReportAggregator(), // Initialize aggregator
		ctx:              engineCtx,             // Store the engine's cancellable context
		cancelFunc:       cancelFunc,            // Store the cancel function for cleanup
		concurrency:      concurrency,           // Store resolved concurrency
		// totalScanned and fatalOccurred initialized to zero by default
	}

	logger.Info("Engine initialized successfully.")
	return engine, nil
}

// Run starts the documentation generation process orchestrated by the Engine.
// **MODIFIED:** Implemented main orchestration logic, including real processor call.
func (e *Engine) Run() (Report, error) {
	startTime := time.Now()
	e.logger.Info("Starting documentation generation run", "concurrency", e.concurrency, "cacheEnabled", e.opts.CacheEnabled)
	var finalErr error

	// Defer block for cleanup, reporting, and panic recovery
	defer func() {
		// Recover from potential panics
		if r := recover(); r != nil {
			errMsg := fmt.Sprintf("panic during execution: %v", r)
			e.logger.Error("Panic recovered during engine run", "panicValue", r, "error", errMsg)
			// Attempt to capture stack trace for better debugging
			buf := make([]byte, 1<<16) // 64KB stack buffer
			stackSize := runtime.Stack(buf, true)
			e.logger.Error("Panic Stack Trace", "stack", string(buf[:stackSize]))

			e.fatalOccurred.Store(true)
			if finalErr == nil {
				finalErr = errors.New(errMsg) // Use a simple error for panic
			}
		}

		// Ensure context is always cancelled on exit
		e.cancelFunc()
		e.logger.Debug("Engine context cancelled.")

		// Persist Cache if enabled and manager exists
		// Use the resolved cacheManager from the engine struct
		if e.opts.CacheEnabled && e.cacheManager != nil && e.cacheManager != (*NoOpCacheManager)(nil) {
			persistPath := e.opts.CacheFilePath
			e.logger.Debug("Attempting to persist cache index", "path", persistPath)
			if persistErr := e.cacheManager.Persist(persistPath); persistErr != nil {
				// Log error but don't override existing fatal error from processing
				e.logger.Error("Failed to persist cache index", slog.String("path", persistPath), slog.String("error", persistErr.Error()))
				if finalErr == nil {
					// Only set cache persist error if no other fatal error occurred
					// Check if it wraps cache.ErrCachePersist
					if errors.Is(persistErr, cache.ErrCachePersist) {
						finalErr = persistErr // Propagate the wrapped error
					} else {
						finalErr = fmt.Errorf("failed to persist cache: %w", persistErr)
					}
				}
			} else {
				e.logger.Info("Cache persisted successfully.", "path", persistPath)
			}
		} else {
			// FIX: Remove undefined logArgs from this logging call (FIXED in previous step)
			e.logger.Debug("Cache persistence skipped (disabled or no manager).")
		}

		// Get final report state AFTER cleanup attempts
		fatal := e.fatalOccurred.Load()
		// Pass totalScanned for accurate reporting
		finalReport := e.aggregator.getReport(e.opts, startTime, e.totalScanned.Load(), fatal)

		logLevel := slog.LevelInfo
		logMsg := "Documentation generation run finished"
		if finalErr != nil && !errors.Is(finalErr, context.Canceled) && !errors.Is(finalErr, context.DeadlineExceeded) {
			// Log as error if a non-cancellation error occurred
			logLevel = slog.LevelError
			logMsg = "Documentation generation run finished with errors"
		}

		// Log final summary metrics
		e.logger.Log(e.ctx, logLevel, logMsg,
			slog.Duration("duration", time.Since(startTime)),
			slog.Int("processed", finalReport.Summary.ProcessedCount),
			slog.Int("cached", finalReport.Summary.CachedCount),
			slog.Int("skipped", finalReport.Summary.SkippedCount),
			slog.Int("errors", finalReport.Summary.ErrorCount),
			slog.Bool("fatalErrorOccurred", finalReport.Summary.FatalErrorOccurred),
		)

		// Trigger final hook (OnRunComplete)
		// Use the EventHooks from the options struct
		if e.opts.EventHooks != nil {
			if hookErr := e.opts.EventHooks.OnRunComplete(finalReport); hookErr != nil {
				// Log hook errors but don't let them affect the final return error
				e.logger.Warn("OnRunComplete hook returned an error", slog.String("error", hookErr.Error()))
			}
		}
	}() // End of defer block

	// === Main Orchestration ===

	// 1. Initialize Processor instance using the factory
	e.logger.Debug("Initializing FileProcessor instance.")
	// Create the actual FileProcessor instance using the resolved factory and dependencies.
	// FIX: Pass arguments matching the factory signature
	e.processor = e.processorFactory(
		e.opts,
		e.opts.Logger,
		e.opts.CacheManager,
		e.opts.LanguageDetector,
		e.opts.EncodingHandler,
		e.opts.AnalysisEngine,
		e.opts.GitClient,
		e.opts.PluginRunner, // This now correctly passes the plugin.PluginRunner from opts
		e.opts.TemplateExecutor,
	)
	if e.processor == nil {
		err := errors.New("internal error: processor factory returned nil")
		e.logger.Error(err.Error())
		e.fatalOccurred.Store(true)
		finalErr = err
		return Report{}, finalErr
	}

	// 2. Setup Channels and WaitGroup
	workerChan := make(chan string, e.concurrency)
	resultsChan := make(chan interface{}, e.concurrency)
	var wg sync.WaitGroup

	// 3. Start Worker Pool
	e.startWorkers(&wg, workerChan, resultsChan)

	// 4. Start Result Aggregator
	aggregatorDone := make(chan struct{})
	go e.aggregateResults(resultsChan, aggregatorDone)
	e.logger.Debug("Result aggregator started.")

	// 5. Initialize and Start Walker
	e.logger.Debug("Initializing directory walker.")
	// **Refinement:** Pass the engine's context to the Walker factory/start
	walker, walkInitErr := e.walkerFactory(e.opts, workerChan, &wg, e.opts.Logger)
	if walkInitErr != nil {
		errMsg := fmt.Sprintf("failed to initialize directory walker: %s", walkInitErr.Error())
		e.logger.Error(errMsg)
		e.fatalOccurred.Store(true)
		// Cleanup started goroutines/channels before returning
		close(workerChan) // Ensure workers can exit if started
		wg.Wait()         // Wait for any potentially started workers
		close(resultsChan)
		<-aggregatorDone
		finalErr = fmt.Errorf("%s", errMsg) // Wrap specific error if needed
		return Report{}, finalErr
	}
	e.walker = walker

	walkerDone := make(chan error, 1)
	go func() {
		defer close(walkerDone)
		e.logger.Info("Starting directory walk...")
		// Pass the engine's cancellable context to the walker
		walkerErr := e.walker.StartWalk(e.ctx)
		if walkerErr != nil {
			if errors.Is(walkerErr, context.Canceled) || errors.Is(walkerErr, context.DeadlineExceeded) {
				e.logger.Info("Directory walk cancelled.", slog.String("reason", walkerErr.Error()))
				// Don't store context errors as finalErr here, let the main routine check ctx.Err()
			} else {
				// Store critical walker error
				e.logger.Error("Directory walk failed critically", slog.String("error", walkerErr.Error()))
				walkerDone <- walkerErr // Send error back
				// Signal cancellation only if not already cancelled and no other fatal error recorded
				if !e.fatalOccurred.Load() && e.ctx.Err() == nil {
					e.fatalOccurred.Store(true)
					e.cancelFunc()
				}
			}
		} else {
			e.logger.Info("Directory walk completed.")
		}
	}()

	// 6. Wait for Completion
	e.logger.Debug("Waiting for walker to finish...")
	finalWalkErr := <-walkerDone // Receive potential critical walk error
	e.logger.Debug("Walker finished. Waiting for workers...")
	wg.Wait()
	e.logger.Debug("Workers finished. Closing results channel...")
	close(resultsChan)
	e.logger.Debug("Waiting for aggregator...")
	<-aggregatorDone
	e.logger.Debug("Aggregator finished.")

	// 7. Determine Final Error State
	// Check context cancellation *first*
	if ctxErr := e.ctx.Err(); ctxErr != nil {
		if finalErr == nil { // Only set if no other fatal error (like walker error) occurred
			finalErr = ctxErr
			// Ensure fatalOccurred is set if cancelled
			e.fatalOccurred.Store(true)
			e.logger.Info("Processing run cancelled.", slog.String("reason", ctxErr.Error()))
		}
	} else if finalWalkErr != nil {
		// Critical walker error occurred, already logged and fatalOccurred set
		if finalErr == nil {
			finalErr = fmt.Errorf("directory walk failed: %w", finalWalkErr)
		}
	} else if e.fatalOccurred.Load() {
		// A worker signaled a fatal error condition
		if finalErr == nil { // Only set if walker/context didn't already cause a fatal error
			firstFatal := e.aggregator.getFirstFatalError()
			if firstFatal != nil {
				finalErr = fmt.Errorf("processing stopped due to fatal error: %w", firstFatal)
			} else {
				// Should not happen if fatalOccurred is true, but fallback
				finalErr = errors.New("processing stopped due to an unspecified fatal error")
				e.logger.Error(finalErr.Error())
			}
		}
	}

	// 8. Return results (defer block handles final report generation/logging/hooks/cache)
	if finalErr != nil {
		e.logger.Debug("Engine run returning fatal error", slog.String("error", finalErr.Error()))
	} else {
		e.logger.Debug("Engine run returning success.")
	}
	// Placeholder report returned here, actual report generation happens in defer
	return Report{}, finalErr
}

// startWorkers launches the worker goroutines.
func (e *Engine) startWorkers(wg *sync.WaitGroup, workerChan <-chan string, resultsChan chan<- interface{}) {
	e.logger.Debug("Starting worker pool", "count", e.concurrency)
	for i := 0; i < e.concurrency; i++ {
		wg.Add(1)
		go e.processFilesWorker(wg, i, workerChan, resultsChan)
	}
	e.logger.Info("Worker pool started.")
}

// processFilesWorker is the main function executed by each worker goroutine.
// **MODIFIED:** Calls the actual FileProcessor.ProcessFile.
func (e *Engine) processFilesWorker(wg *sync.WaitGroup, workerID int, workerChan <-chan string, resultsChan chan<- interface{}) {
	defer func() {
		// Recover from potential panics within the worker itself
		if r := recover(); r != nil {
			wLogger := e.logger.With(slog.Int("workerID", workerID))
			errMsg := fmt.Sprintf("panic: %v", r)
			wLogger.Error("Panic recovered in worker", "panicValue", r, "error", errMsg)
			buf := make([]byte, 1<<16)
			stackSize := runtime.Stack(buf, true)
			wLogger.Error("Worker Panic Stack Trace", "stack", string(buf[:stackSize]))
			// Safely send error result
			func() {
				defer func() { recover() }() // Prevent panic if resultsChan closed
				resultsChan <- ErrorInfo{Path: "unknown (panic)", Error: errMsg, IsFatal: true}
			}()
			// Signal engine stop if panic occurs and not already stopping
			if !e.fatalOccurred.Load() && e.ctx.Err() == nil {
				e.fatalOccurred.Store(true)
				e.cancelFunc()
			}
		}
		wg.Done()
		e.logger.Debug("Worker finished", slog.Int("workerID", workerID))
	}()

	wLogger := e.logger.With(slog.Int("workerID", workerID))
	wLogger.Debug("Worker started.")

	for {
		select {
		case filePath, ok := <-workerChan:
			if !ok {
				wLogger.Debug("Worker shutting down (input channel closed).")
				return // Exit goroutine when channel is closed
			}

			// Check context *before* starting potentially long processing
			if e.ctx.Err() != nil {
				wLogger.Debug("Worker skipping file due to context cancellation before processing", "path", filepath.Base(filePath))
				// Do not send result, engine handles cancellation centrally
				continue
			}

			wLogger.Debug("Received file", "path", filepath.Base(filePath))

			if e.processor == nil {
				errMsg := "internal error: FileProcessor not initialized for worker"
				wLogger.Error(errMsg)
				// Safely send error result
				func() {
					defer func() { recover() }()
					resultsChan <- ErrorInfo{Path: filePath, Error: errMsg, IsFatal: true}
				}()
				// Signal engine stop
				if !e.fatalOccurred.Load() && e.ctx.Err() == nil {
					e.fatalOccurred.Store(true)
					e.cancelFunc()
				}
				continue // Continue loop in case other files can be processed? Or return? Let's continue.
			}

			// Trigger initial "Processing" hook call
			// Use relative path for hooks if possible
			relPath := filePath
			if rp, err := filepath.Rel(e.opts.InputPath, filePath); err == nil {
				relPath = filepath.ToSlash(rp)
			}
			if hookErr := e.opts.EventHooks.OnFileStatusUpdate(relPath, StatusProcessing, "", 0); hookErr != nil {
				wLogger.Warn("Event hook OnFileStatusUpdate (Processing) failed", slog.String("path", relPath), slog.String("error", hookErr.Error()))
			}

			// *** Call the actual processor implementation ***
			// Pass the engine's cancellable context down to the processor
			result, status, err := e.processor.ProcessFile(e.ctx, filePath)
			procDuration := time.Duration(0) // Get duration from FileInfo if available
			if fi, ok := result.(FileInfo); ok {
				procDuration = time.Duration(fi.DurationMs) * time.Millisecond
			} else if _, ok := result.(SkippedInfo); ok {
				// Skips happen fast, duration often negligible/not tracked by processor defer
				// FIX: Replace 'ei' with '_' as it's unused.
			} else if _, ok := result.(ErrorInfo); ok && err != nil { // Check err as well for ErrorInfo case
				// Error duration captured by processor defer? Need consistency.
				// For now, let the Engine's hook call use its overall duration.
			}

			// Process the result (thread-safe send to aggregator)
			func() {
				defer func() { recover() }() // Prevent panic if resultsChan closed concurrently
				sent := false
				if result != nil { // Check if processor returned a valid result struct
					resultsChan <- result
					sent = true
					pathStr := "unknown"
					// **Refinement:** Extract message for final hook call here
					message := ""
					switch r := result.(type) {
					case FileInfo:
						pathStr = r.Path
						if r.CacheStatus == CacheStatusHit {
							message = "Retrieved from cache"
						} else {
							message = "Successfully processed"
						}
						wLogger.Debug("Sent success/cache result to aggregator", "path", pathStr)
					case SkippedInfo:
						pathStr = r.Path
						// **REFINED:** Use the structured reason/details for the hook message
						message = fmt.Sprintf("%s: %s", r.Reason, r.Details)
						wLogger.Debug("Sent skipped result to aggregator", "path", pathStr)
					case ErrorInfo:
						pathStr = r.Path
						message = r.Error // Use error message from ErrorInfo
						wLogger.Debug("Sent error result to aggregator", "path", pathStr, "isFatalFileErr", r.IsFatal)
						// Trigger engine cancellation if this file error is considered fatal
						// The processor sets IsFatal based on OnErrorMode
						if r.IsFatal && !e.fatalOccurred.Load() && e.ctx.Err() == nil {
							wLogger.Warn("Worker detected fatal error condition, signalling stop", "path", r.Path, "error", r.Error)
							e.fatalOccurred.Store(true)
							e.cancelFunc()
						}
					}

					// Call final hook *after* sending result to aggregator
					// The Engine's main defer block now handles the *overall* run complete hook.
					// The worker is responsible for the *final* status update hook for the file it processed.
					// NOTE: The processor's internal defer block logs its completion but doesn't call hooks.
					if hookErr := e.opts.EventHooks.OnFileStatusUpdate(pathStr, status, message, procDuration); hookErr != nil {
						wLogger.Warn("Event hook OnFileStatusUpdate (Final) failed", slog.String("path", pathStr), slog.String("status", string(status)), slog.String("error", hookErr.Error()))
					}

				} else if err != nil {
					// If processor returned error but nil result (shouldn't happen ideally)
					errMsg := fmt.Sprintf("Processor error: %v", err)
					isFatalFileErr := e.opts.OnErrorMode == OnErrorStop
					errorInfo := ErrorInfo{Path: relPath, Error: errMsg, IsFatal: isFatalFileErr}
					resultsChan <- errorInfo
					sent = true
					wLogger.Error("Processor returned error but nil result", slog.String("path", relPath), slog.String("error", errMsg))
					// Call final hook
					if hookErr := e.opts.EventHooks.OnFileStatusUpdate(relPath, StatusFailed, errMsg, procDuration); hookErr != nil {
						wLogger.Warn("Event hook OnFileStatusUpdate (Final/Error) failed", slog.String("path", relPath), slog.String("error", hookErr.Error()))
					}
					// Trigger engine cancellation if fatal
					if isFatalFileErr && !e.fatalOccurred.Load() && e.ctx.Err() == nil {
						wLogger.Warn("Worker detected fatal error condition (nil result), signalling stop", "path", relPath, "error", errMsg)
						e.fatalOccurred.Store(true)
						e.cancelFunc()
					}
				}

				if !sent {
					wLogger.Warn("Processor returned nil result and nil error, nothing sent to aggregator.", "path", filePath)
					// Consider sending a generic success/unknown status? Or is this an internal error?
					// Let's assume this shouldn't happen and log it.
				}

			}() // End of safe send func

		case <-e.ctx.Done():
			wLogger.Debug("Worker shutting down (context cancelled).")
			return // Exit goroutine
		}
	}
}

// aggregateResults reads results from the resultsChan and updates the reportAggregator.
// **MODIFIED:** Ensures thread-safe updates to aggregator fields.
func (e *Engine) aggregateResults(resultsChan <-chan interface{}, done chan<- struct{}) {
	defer close(done)
	e.logger.Debug("Result aggregator started.")

	for result := range resultsChan {
		// Increment result count *before* locking mutex to avoid holding lock while potentially blocking on send
		e.totalResults.Add(1)

		e.aggregator.mu.Lock() // Lock for update
		updated := false
		switch r := result.(type) {
		case FileInfo:
			e.aggregator.processedFiles = append(e.aggregator.processedFiles, r)
			// Check cache status constant from the *converter* package
			if r.CacheStatus == CacheStatusHit {
				e.aggregator.cachedCount++
			}
			updated = true
			e.logger.Debug("Aggregator: Received Processed/Cached", "path", r.Path, "status", r.CacheStatus)
		case SkippedInfo:
			e.aggregator.skippedFiles = append(e.aggregator.skippedFiles, r)
			updated = true
			e.logger.Debug("Aggregator: Received Skipped", "path", r.Path, "reason", r.Reason)
		case ErrorInfo:
			e.aggregator.errors = append(e.aggregator.errors, r)
			updated = true
			e.logger.Debug("Aggregator: Received Error", "path", r.Path, "isFatal", r.IsFatal)
		default:
			e.logger.Warn("Aggregator received unknown result type", "type", fmt.Sprintf("%T", result))
		}
		e.aggregator.mu.Unlock() // Unlock after update

		if !updated {
			e.logger.Warn("Aggregator processed an item but didn't match known types", "type", fmt.Sprintf("%T", result))
		}
	}

	finalResultCount := e.totalResults.Load()
	e.logger.Info("Result aggregator finished.", "resultsProcessed", finalResultCount)
}

// --- reportAggregator ---

// reportAggregator manages the collection of results during the run.
// **MODIFIED:** Added thread-safety mutex.
type reportAggregator struct {
	mu             sync.Mutex // Mutex to protect concurrent updates
	processedFiles []FileInfo
	skippedFiles   []SkippedInfo
	errors         []ErrorInfo
	cachedCount    int
	warningCount   int // Placeholder for actual warning tracking if needed
}

// newReportAggregator creates a new report aggregator.
func newReportAggregator() *reportAggregator { // minimal comment
	return &reportAggregator{
		processedFiles: make([]FileInfo, 0, 512),
		skippedFiles:   make([]SkippedInfo, 0, 128),
		errors:         make([]ErrorInfo, 0, 32),
	}
}

// getFirstFatalError finds the first recorded error marked as fatal.
// **MODIFIED:** Uses mutex.
func (a *reportAggregator) getFirstFatalError() error {
	a.mu.Lock() // Lock for reading the errors slice
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
// **MODIFIED:** Uses mutex, uses atomic totalScanned passed from Engine.
func (a *reportAggregator) getReport(opts *Options, startTime time.Time, totalFilesScanned int64, fatalOccurred bool) Report {
	a.mu.Lock() // Lock for reading all aggregated data
	// Create copies of slices to avoid returning internal state directly
	processedCount := len(a.processedFiles)
	cachedCount := a.cachedCount // Access while locked
	skippedCount := len(a.skippedFiles)
	errorCount := len(a.errors)
	warningCount := a.warningCount // Assuming this is managed thread-safely if used

	// Create copies to ensure the returned report is immutable after unlock
	processed := make([]FileInfo, processedCount)
	copy(processed, a.processedFiles)
	skipped := make([]SkippedInfo, skippedCount)
	copy(skipped, a.skippedFiles)
	errorsList := make([]ErrorInfo, errorCount)
	copy(errorsList, a.errors)
	a.mu.Unlock() // Unlock after copying

	// Use the totalScanned value passed in (atomically read from engine)
	return Report{
		Summary: ReportSummary{
			InputPath:          opts.InputPath,
			OutputPath:         opts.OutputPath,
			ProfileUsed:        opts.ProfileName,
			ConfigFilePath:     opts.ConfigFilePath,
			TotalFilesScanned:  int(totalFilesScanned), // Use Walker's discovered count
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

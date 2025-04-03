// --- START OF FINAL REVISED FILE pkg/converter/cache/cache.go ---
package cache

import (
	"encoding/gob"
	"encoding/json" // Import encoding/json
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings" // Import strings
	"sync"
	"time"
	// FIX: Remove import cycle by removing unnecessary parent package import.
	// Errors specific to cache operations are now defined within this package.
	// _ "github.com/stackvity/stack-converter/pkg/converter"
)

// --- Constants ---

// CacheFileName is the standard name for the cache index file.
// Ref: proposal.md#8.5
const CacheFileName = ".stackconverter.cache"

// CacheSchemaVersion represents the current version of the cache file structure.
// Implementations MUST check this version during Load and invalidate if mismatched.
// Increment this if the CacheEntry struct or serialization format changes incompatibly.
// Ref: proposal.md#8.5
const CacheSchemaVersion = "1.0"

const (
	// DefaultCacheFormat specifies the default serialization format.
	DefaultCacheFormat = "gob"
	// CacheFormatGob represents the gob serialization format.
	CacheFormatGob = "gob"
	// CacheFormatJSON represents the JSON serialization format.
	CacheFormatJSON = "json"
)

// --- Error Variables ---

// FIX: Define cache-specific errors within this package.
// ErrCacheLoad indicates an error occurred while loading or decoding the cache index file.
// This is typically treated as a cache miss and logged as a warning, not returned as fatal
// unless the error is critical (e.g., permission denied accessing the file path).
// Ref: SC-STORY-020
var ErrCacheLoad = errors.New("failed to load cache index")

// ErrCachePersist indicates an error occurred while persisting the cache index file.
// This is typically logged as an error, but the main conversion run may still succeed.
// Ref: SC-STORY-020
var ErrCachePersist = errors.New("failed to persist cache index")

// --- Data Structures ---

// CacheEntry represents the stored state for a single cached file.
// It contains hashes and timestamps necessary for validation.
// Recommended serialization format: encoding/gob for performance.
// Ref: proposal.md#8.5
type CacheEntry struct {
	SourceModTime    time.Time `json:"sourceModTime" gob:"sourceModTime"`       // Modification time of the source file when cached.
	SourceHash       string    `json:"sourceHash" gob:"sourceHash"`             // Hash (e.g., SHA-256) of the source file content when cached.
	ConfigHash       string    `json:"configHash" gob:"configHash"`             // Hash of the relevant configuration affecting the output when cached. Ref: SC-STORY-028
	OutputHash       string    `json:"outputHash" gob:"outputHash"`             // Hash (e.g., SHA-256) of the generated output content when cached.
	SchemaVersion    string    `json:"schemaVersion" gob:"schemaVersion"`       // Schema version of this entry (must match CacheSchemaVersion). Ref: proposal.md#8.5
	ConverterVersion string    `json:"converterVersion" gob:"converterVersion"` // Version of the converter tool that created this entry. Ref: proposal.md#8.5
}

// CacheFileHeader contains metadata about the cache file itself.
// Written at the beginning of the cache file. Used for validation during Load.
type CacheFileHeader struct {
	SchemaVersion    string `json:"schemaVersion" gob:"schemaVersion"`       // Must match CacheSchemaVersion constant from this package
	ConverterVersion string `json:"converterVersion" gob:"converterVersion"` // Tool version (e.g., "v3.1.1", "dev")
	// Format field removed - format is determined by manager instance configuration
}

// --- Interfaces ---

// CacheManager defines the interface for interacting with the cache.
// Implementations are responsible for loading the cache index from persistence,
// checking if a file is considered fresh based on various hashes and timestamps,
// updating the in-memory index, and persisting the index back to storage.
// Ref: SC-STORY-014, SC-STORY-027
//
// Stability: Public Stable API - Implementations can be provided externally.
// Adherence to the method contracts, especially thread-safety for Update, is required.
type CacheManager interface {
	// Load attempts to read and decode the cache index from the specified path using the format
	// the CacheManager was configured with (e.g., Gob or JSON).
	//
	// Handling:
	//   - File Not Found: Handled gracefully, returns nil error, resulting in an empty in-memory index.
	//   - Decode/Corruption Errors: Logs a warning, returns nil error, results in an empty in-memory index (treated as miss).
	//   - Version Mismatches (Schema or Tool): Logs a warning, returns nil error, results in an empty in-memory index (treated as miss).
	//   - Critical I/O Errors (e.g., Permissions): Returns an error wrapping ErrCacheLoad.
	// Ref: LIB.10.1
	Load(cachePath string) error

	// Check determines if a cache entry for the given filePath is valid ("hit").
	// Returns true and the stored outputHash if all criteria match (path exists, modTime matches,
	// contentHash matches, configHash matches, schema/tool versions match), otherwise false and empty string.
	// This method MUST be safe for concurrent read access after Load completes.
	// Ref: LIB.10.2
	Check(filePath string, modTime time.Time, contentHash string, configHash string) (isHit bool, outputHash string)

	// Update adds or replaces an entry in the in-memory cache index.
	// This method MUST be thread-safe for concurrent calls from multiple workers.
	// Implementations MUST use appropriate Go concurrency primitives (e.g., sync.RWMutex)
	// to protect shared access to the internal index map. Tests using this implementation
	// MUST be run with the `-race` flag.
	// Ref: LIB.10.3
	Update(filePath string, modTime time.Time, sourceHash string, configHash string, outputHash string) error

	// Persist writes the current in-memory cache index to the specified path using the format
	// the CacheManager was configured with.
	// Implementations SHOULD perform an atomic write (e.g., write to temp file, then rename)
	// to prevent data loss if the process is interrupted during the write.
	// Returns an error wrapping ErrCachePersist if writing or renaming fails.
	// Ref: LIB.10.4
	Persist(cachePath string) error
}

// --- FileCacheManager Implementation ---

// fileCacheManager implements the CacheManager interface using a local file.
// It stores the cache index in memory (map) and uses a configured format (Gob or JSON) for persistence.
// Ensures thread-safety for concurrent updates using a sync.RWMutex.
type fileCacheManager struct {
	index            map[string]CacheEntry // In-memory index: map[relative_file_path] -> CacheEntry
	mu               sync.RWMutex          // Mutex to protect concurrent access to the index map
	logger           *slog.Logger
	schemaVersion    string // Expected schema version (usually CacheSchemaVersion constant)
	converterVersion string // Current tool version for compatibility check
	format           string // Serialization format ("gob" or "json")
}

// NewFileCacheManager creates a new file-based cache manager with a specific format.
// It requires the expected schemaVersion and the current converterVersion for validation during Load.
// The cacheFormat parameter dictates the serialization method ("gob" or "json", defaults to "gob").
func NewFileCacheManager(loggerHandler slog.Handler, schemaVersion string, converterVersion string, cacheFormat string) CacheManager { // minimal comment
	if loggerHandler == nil {
		loggerHandler = slog.NewTextHandler(io.Discard, nil)
	}
	// Determine and log the format being used
	format := strings.ToLower(cacheFormat)
	if format != CacheFormatJSON && format != CacheFormatGob {
		format = DefaultCacheFormat // Default to Gob if invalid or empty
	}
	logger := slog.New(loggerHandler).With(
		slog.String("component", "cacheManager"),
		slog.String("impl", "file"),
		slog.String("format", format), // Log the chosen format
	)

	if schemaVersion == "" {
		schemaVersion = CacheSchemaVersion
		logger.Warn("Schema version not provided, using package constant", "version", schemaVersion)
	}
	if converterVersion == "" {
		converterVersion = "dev"
		logger.Warn("Converter version not provided, using default", "version", converterVersion)
	}

	logger.Debug("File cache manager initialized")

	return &fileCacheManager{
		index:            make(map[string]CacheEntry),
		logger:           logger,
		schemaVersion:    schemaVersion,
		converterVersion: converterVersion,
		format:           format,
	}
}

// Load implements the CacheManager interface.
func (c *fileCacheManager) Load(cachePath string) error { // minimal comment
	c.mu.Lock()
	defer c.mu.Unlock()

	c.index = make(map[string]CacheEntry) // Reset index

	file, err := os.Open(cachePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			c.logger.Info("Cache file not found, initializing empty cache index.", "path", cachePath)
			return nil
		}
		// FIX: Wrap the locally defined ErrCacheLoad
		wrappedErr := fmt.Errorf("%w: failed to open cache file '%s': %w", ErrCacheLoad, cachePath, err)
		c.logger.Error("Critical cache load error", "path", cachePath, "error", err.Error())
		return wrappedErr
	}
	defer file.Close()

	var header CacheFileHeader
	var loadedIndex map[string]CacheEntry

	// Choose decoder based on configured format
	var headerDecodeErr, indexDecodeErr error
	if c.format == CacheFormatJSON {
		// Assume the *entire file* must match the configured format.
		// We reset the reader and decode the whole structure.
		_, seekErr := file.Seek(0, io.SeekStart) // Reset reader to beginning
		if seekErr != nil {
			// FIX: Wrap the locally defined ErrCacheLoad
			return fmt.Errorf("%w: failed to seek cache file '%s': %w", ErrCacheLoad, cachePath, seekErr)
		}
		// Define a struct for the combined JSON cache file
		type jsonCacheFile struct {
			Header CacheFileHeader       `json:"header"`
			Index  map[string]CacheEntry `json:"index"`
		}
		var cacheData jsonCacheFile
		jsonDecoder := json.NewDecoder(file)
		if decodeErr := jsonDecoder.Decode(&cacheData); decodeErr != nil {
			if errors.Is(decodeErr, io.EOF) { // Empty JSON file
				headerDecodeErr = io.EOF // Treat as empty file
			} else {
				headerDecodeErr = decodeErr // Treat as general decode error
			}
		} else {
			header = cacheData.Header
			loadedIndex = cacheData.Index
		}
	} else { // Assume Gob format
		decoder := gob.NewDecoder(file)
		headerDecodeErr = decoder.Decode(&header)
		if headerDecodeErr == nil {
			indexDecodeErr = decoder.Decode(&loadedIndex) // Try to decode index after header
		}
	}

	// Process header decode result
	if headerDecodeErr != nil {
		if errors.Is(headerDecodeErr, io.EOF) || errors.Is(headerDecodeErr, io.ErrUnexpectedEOF) {
			c.logger.Warn("Cache file appears empty or header is incomplete, treating as miss.", "path", cachePath)
			return nil
		}
		c.logger.Warn("Failed to decode cache file header/structure (corrupted or wrong format?), treating as miss.",
			"path", cachePath, "format_expected", c.format, "error", headerDecodeErr.Error())
		return nil
	}

	// Validate Header Versions
	if header.SchemaVersion != c.schemaVersion {
		c.logger.Warn("Cache file schema version mismatch, invalidating cache.",
			"path", cachePath, "file_schema", header.SchemaVersion, "expected_schema", c.schemaVersion)
		return nil
	}
	isDevTool := c.converterVersion == "dev"
	isDevCache := header.ConverterVersion == "dev"
	versionsMatch := header.ConverterVersion == c.converterVersion
	if !isDevTool && !isDevCache && !versionsMatch {
		c.logger.Warn("Cache file converter version mismatch, invalidating cache.",
			"path", cachePath, "file_converter", header.ConverterVersion, "expected_converter", c.converterVersion)
		return nil
	}

	// Process index decode result (only relevant for Gob path currently)
	if indexDecodeErr != nil {
		if errors.Is(indexDecodeErr, io.EOF) {
			c.logger.Info("Cache file contains header but no index data, loaded empty cache.", "path", cachePath)
			// Keep c.index as empty map, return success
			return nil
		}
		c.logger.Warn("Failed to decode cache file index data (corruption?), treating as miss.", "path", cachePath, "error", indexDecodeErr.Error())
		c.index = make(map[string]CacheEntry) // Ensure index is reset
		return nil
	}

	// Successfully loaded header and index (either from Gob or combined JSON).
	if loadedIndex == nil { // Safety check if JSON decoding path had issues finding index
		loadedIndex = make(map[string]CacheEntry)
	}
	c.index = loadedIndex
	c.logger.Info("Cache loaded successfully from file.", "path", cachePath, "format_loaded", c.format, "entries_loaded", len(c.index))
	return nil
}

// Check implements the CacheManager interface.
func (c *fileCacheManager) Check(filePath string, modTime time.Time, contentHash string, configHash string) (bool, string) { // minimal comment
	// RLock for checking the index map
	c.mu.RLock()
	entry, found := c.index[filePath]
	c.mu.RUnlock()

	logArgs := []any{
		slog.String("path", filePath),
		slog.Time("check_modTime", modTime),
		slog.String("check_contentHash", contentHash),
		slog.String("check_configHash", configHash),
	}

	if !found {
		c.logger.Debug("Cache check: Miss (entry not found)", logArgs...)
		return false, ""
	}

	// Add entry details for debugging misses
	logArgs = append(logArgs,
		slog.Time("entry_modTime", entry.SourceModTime),
		slog.String("entry_sourceHash", entry.SourceHash),
		slog.String("entry_configHash", entry.ConfigHash),
		slog.String("entry_schemaVersion", entry.SchemaVersion),
		slog.String("entry_converterVersion", entry.ConverterVersion),
	)

	// Version validation (defensive)
	if entry.SchemaVersion != c.schemaVersion {
		c.logger.Debug("Cache check: Miss (schema version mismatch in entry)", logArgs...)
		return false, ""
	}
	isDevTool := c.converterVersion == "dev"
	isDevEntry := entry.ConverterVersion == "dev"
	versionsMatch := entry.ConverterVersion == c.converterVersion
	if !isDevTool && !isDevEntry && !versionsMatch {
		c.logger.Debug("Cache check: Miss (converter version mismatch in entry)", logArgs...)
		return false, ""
	}

	// Core validation logic
	if !entry.SourceModTime.Equal(modTime) {
		c.logger.Debug("Cache check: Miss (modTime mismatch)", logArgs...)
		return false, ""
	}
	if entry.SourceHash != contentHash {
		c.logger.Debug("Cache check: Miss (contentHash mismatch)", logArgs...)
		return false, ""
	}
	if entry.ConfigHash != configHash {
		c.logger.Debug("Cache check: Miss (configHash mismatch)", logArgs...)
		return false, ""
	}

	// All checks passed
	c.logger.Debug("Cache check: Hit", append(logArgs, slog.String("outputHash", entry.OutputHash))...)
	return true, entry.OutputHash
}

// Update implements the CacheManager interface.
func (c *fileCacheManager) Update(filePath string, modTime time.Time, sourceHash string, configHash string, outputHash string) error { // minimal comment
	c.mu.Lock() // Exclusive lock for writing
	defer c.mu.Unlock()

	if c.index == nil {
		c.logger.Error("Internal cache error: index map is nil during Update.")
		c.index = make(map[string]CacheEntry)
		// Optionally return error here
	}

	entry := CacheEntry{
		SourceModTime:    modTime,
		SourceHash:       sourceHash,
		ConfigHash:       configHash,
		OutputHash:       outputHash,
		SchemaVersion:    c.schemaVersion,
		ConverterVersion: c.converterVersion,
	}
	c.index[filePath] = entry

	c.logger.Debug("Cache index updated in memory", slog.String("path", filePath))
	return nil
}

// Persist implements the CacheManager interface.
func (c *fileCacheManager) Persist(cachePath string) error { // minimal comment
	c.mu.RLock() // Read lock to copy index
	indexCopy := make(map[string]CacheEntry, len(c.index))
	for k, v := range c.index {
		indexCopy[k] = v
	}
	indexSize := len(indexCopy)
	c.mu.RUnlock()

	if indexSize == 0 {
		c.logger.Debug("Skipping cache persist, index is empty. Attempting removal.", "path", cachePath)
		err := os.Remove(cachePath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			c.logger.Warn("Failed to remove empty cache file", "path", cachePath, "error", err.Error())
		}
		return nil
	}

	cacheDir := filepath.Dir(cachePath)
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		// FIX: Wrap the locally defined ErrCachePersist
		wrappedErr := fmt.Errorf("%w: failed to ensure cache directory exists '%s': %w", ErrCachePersist, cacheDir, err)
		c.logger.Error("Cache persist error", "path", cachePath, "error", err.Error())
		return wrappedErr
	}

	tempFilePattern := filepath.Base(cachePath) + ".tmp-*"
	tempFile, err := os.CreateTemp(cacheDir, tempFilePattern)
	if err != nil {
		// FIX: Wrap the locally defined ErrCachePersist
		wrappedErr := fmt.Errorf("%w: failed to create temporary cache file in '%s': %w", ErrCachePersist, cacheDir, err)
		c.logger.Error("Cache persist error", "path", cachePath, "error", err.Error())
		return wrappedErr
	}
	tempFilePath := tempFile.Name()
	c.logger.Debug("Created temporary cache file", "temp_path", tempFilePath)

	closed := false
	defer func() {
		if !closed {
			_ = tempFile.Close() // Ignore error on cleanup close
		}
		if _, statErr := os.Stat(tempFilePath); statErr == nil {
			c.logger.Debug("Removing temporary cache file after error", "temp_path", tempFilePath)
			_ = os.Remove(tempFilePath)
		}
	}()

	// Encode based on configured format
	header := CacheFileHeader{
		SchemaVersion:    c.schemaVersion,
		ConverterVersion: c.converterVersion,
	}
	var encodeErr error
	if c.format == CacheFormatJSON {
		// Write as a single JSON object { "header": ..., "index": ... }
		type jsonCacheFile struct {
			Header CacheFileHeader       `json:"header"`
			Index  map[string]CacheEntry `json:"index"`
		}
		encoder := json.NewEncoder(tempFile)
		encoder.SetIndent("", "  ") // Make it somewhat readable
		encodeErr = encoder.Encode(jsonCacheFile{Header: header, Index: indexCopy})
	} else { // Default to Gob
		encoder := gob.NewEncoder(tempFile)
		// Encode Header then Index
		if errHeader := encoder.Encode(header); errHeader != nil {
			encodeErr = fmt.Errorf("failed to encode header: %w", errHeader)
		} else {
			encodeErr = encoder.Encode(indexCopy) // Encode index
		}
	}

	if encodeErr != nil {
		// FIX: Wrap the locally defined ErrCachePersist
		wrappedErr := fmt.Errorf("%w: failed to encode cache (%s) to temporary file '%s': %w", ErrCachePersist, c.format, tempFilePath, encodeErr)
		c.logger.Error("Cache persist encoding error", "path", cachePath, "error", encodeErr.Error())
		return wrappedErr
	}

	// Close file before renaming
	if err := tempFile.Close(); err != nil {
		closed = true
		// FIX: Wrap the locally defined ErrCachePersist
		wrappedErr := fmt.Errorf("%w: failed to close temporary cache file '%s' before rename: %w", ErrCachePersist, tempFilePath, err)
		c.logger.Error("Cache persist error", "path", cachePath, "error", err.Error())
		return wrappedErr
	}
	closed = true

	// Atomic Rename
	if err := os.Rename(tempFilePath, cachePath); err != nil {
		// FIX: Wrap the locally defined ErrCachePersist
		wrappedErr := fmt.Errorf("%w: failed to rename temporary cache file '%s' to final path '%s': %w", ErrCachePersist, tempFilePath, cachePath, err)
		c.logger.Error("Cache persist atomic rename error", "path", cachePath, "error", err.Error())
		return wrappedErr // Cleanup defer will try to remove tempFilePath if rename failed
	}

	c.logger.Info("Cache persisted successfully to file.", "path", cachePath, "format", c.format, "entries_saved", indexSize)
	return nil
}

// --- END OF FINAL REVISED FILE pkg/converter/cache/cache.go ---

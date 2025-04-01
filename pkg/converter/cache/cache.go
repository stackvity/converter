// --- START OF FINAL REVISED FILE pkg/converter/cache/cache.go ---
package cache

import (
	"errors"
	"time"
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

// --- Error Variables ---

// ErrCacheLoad indicates an error occurred while loading or decoding the cache index file.
// This is typically treated as a cache miss and logged as a warning, not returned as fatal.
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
	// Load attempts to read and decode the cache index from the specified path.
	// Implementations MUST handle file not found errors gracefully (returning an empty index)
	// and decoding/corruption errors (logging a warning, returning an empty index).
	// Implementations MUST validate both CacheSchemaVersion and ConverterVersion read from
	// the cache header/entries against current constants/versions, invalidating the cache
	// (returning empty index) if incompatible. Log reasons for invalidation clearly.
	// Returns an error wrapping ErrCacheLoad only for critical, unexpected load failures (e.g., permissions).
	// Ref: LIB.10.1
	Load(cachePath string) error

	// Check determines if a cache entry for the given filePath is valid ("hit").
	// Returns true and the stored outputHash if all criteria match, otherwise false and empty string.
	// This method MUST be safe for concurrent read access.
	// Ref: LIB.10.2
	Check(filePath string, modTime time.Time, contentHash string, configHash string) (isHit bool, outputHash string)

	// Update adds or replaces an entry in the in-memory cache index.
	// This method MUST be thread-safe for concurrent calls from multiple workers.
	// Implementations MUST use appropriate Go concurrency primitives (e.g., sync.RWMutex)
	// to protect shared access to the internal index map. Test with -race flag.
	// Ref: LIB.10.3
	Update(filePath string, modTime time.Time, sourceHash string, configHash string, outputHash string) error

	// Persist writes the current in-memory cache index to the specified path.
	// Implementations SHOULD perform an atomic write (e.g., write to temp file, then rename).
	// Returns an error wrapping ErrCachePersist if writing or renaming fails.
	// Ref: LIB.10.4
	Persist(cachePath string) error
}

// --- END OF FINAL REVISED FILE pkg/converter/cache/cache.go ---

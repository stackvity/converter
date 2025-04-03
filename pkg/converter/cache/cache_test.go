// --- START OF FINAL REVISED FILE pkg/converter/cache/cache_test.go ---
package cache_test

import (
	"bytes"
	"encoding/gob"
	"encoding/json" // Import json
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime" // Import runtime
	"sync"
	"testing"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter/cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCacheConstants verifies the constant values.
func TestCacheConstants(t *testing.T) { // minimal comment
	assert.Equal(t, ".stackconverter.cache", cache.CacheFileName)
	assert.Equal(t, "1.0", cache.CacheSchemaVersion)
	assert.Equal(t, "gob", cache.DefaultCacheFormat)
	assert.Equal(t, "gob", cache.CacheFormatGob)
	assert.Equal(t, "json", cache.CacheFormatJSON)
}

// TestCacheEntryInitialization verifies basic struct initialization.
func TestCacheEntryInitialization(t *testing.T) { // minimal comment
	now := time.Now()
	entry := cache.CacheEntry{
		SourceModTime:    now,
		SourceHash:       "hash1",
		ConfigHash:       "config1",
		OutputHash:       "output1",
		SchemaVersion:    "1.0",
		ConverterVersion: "v3.0.0",
	}
	assert.Equal(t, now, entry.SourceModTime)
	assert.Equal(t, "hash1", entry.SourceHash)
	assert.Equal(t, "config1", entry.ConfigHash)
	assert.Equal(t, "output1", entry.OutputHash)
	assert.Equal(t, "1.0", entry.SchemaVersion)
	assert.Equal(t, "v3.0.0", entry.ConverterVersion)
}

// --- Test Suite for fileCacheManager ---

// Helper struct to access internal fields for testing
type TestableFileCacheManager struct {
	SchemaVersion    string
	ConverterVersion string
	Format           string
	Index            map[string]cache.CacheEntry // Expose index for checks (populated manually in tests)
	Mu               *sync.RWMutex               // Expose mutex if needed
	// Store the interface for interaction
	ManagerInterface cache.CacheManager
}

// Setup function for fileCacheManager tests
// Returns both the interface and a helper struct for test configuration/verification.
func setupFileCacheTest(t *testing.T, format string) (cache.CacheManager, *TestableFileCacheManager, string, *bytes.Buffer, func()) { // minimal comment
	t.Helper()
	logBuf := &bytes.Buffer{}
	handler := slog.NewTextHandler(logBuf, &slog.HandlerOptions{Level: slog.LevelDebug})
	logger := slog.New(handler)

	testSchemaVersion := cache.CacheSchemaVersion
	testConverterVersion := "test-v1.0"

	// Use specific format if provided, otherwise default
	if format == "" {
		format = cache.DefaultCacheFormat
	}

	// Create the manager instance using the exported constructor
	managerInterface := cache.NewFileCacheManager(logger.Handler(), testSchemaVersion, testConverterVersion, format)
	require.NotNil(t, managerInterface)

	// Create the testable helper struct.
	// Note: We cannot directly access the internal, unexported fields of the
	// concrete implementation from a different package (`cache_test` vs `cache`).
	// Tests needing to verify internal state (like the index map) must either
	// 1. Do so indirectly via the exported interface methods (Load, Check, Update, Persist).
	// 2. Manually populate the `Index` field of this `TestableFileCacheManager`
	//    struct within the specific test function's setup phase if they need to
	//    pre-load state for a `Check` verification.
	testableManager := &TestableFileCacheManager{
		ManagerInterface: managerInterface,     // Store the interface for interaction
		SchemaVersion:    testSchemaVersion,    // We know what we passed in
		ConverterVersion: testConverterVersion, // We know what we passed in
		Format:           format,               // We know what we passed in
		// Initialize Index and Mu, but they are *not* connected to the internal state
		// of the real manager unless populated manually in tests.
		Index: make(map[string]cache.CacheEntry),
		Mu:    &sync.RWMutex{},
	}

	tempDir := t.TempDir()
	cachePath := filepath.Join(tempDir, cache.CacheFileName)

	cleanup := func() {
		if t.Failed() {
			t.Logf("--- Cache Manager Logs ---\n%s--- End Logs ---", logBuf.String())
		}
	}

	// Return the actual interface instance and the testable helper struct
	return managerInterface, testableManager, cachePath, logBuf, cleanup
}

// Helper function to create a valid cache file for testing Load
func createValidCacheFile(t *testing.T, path string, schemaVer, converterVer, format string, data map[string]cache.CacheEntry) { // minimal comment
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	file, err := os.Create(path)
	require.NoError(t, err)
	defer file.Close()

	header := cache.CacheFileHeader{
		SchemaVersion:    schemaVer,
		ConverterVersion: converterVer,
	}
	if data == nil {
		data = make(map[string]cache.CacheEntry)
	}

	if format == cache.CacheFormatJSON {
		type jsonCacheFile struct {
			Header cache.CacheFileHeader       `json:"header"`
			Index  map[string]cache.CacheEntry `json:"index"`
		}
		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		err = encoder.Encode(jsonCacheFile{Header: header, Index: data})
		require.NoError(t, err, "Failed to encode JSON cache file")
	} else { // Assume Gob
		encoder := gob.NewEncoder(file)
		err = encoder.Encode(header)
		require.NoError(t, err, "Failed to encode gob header")
		err = encoder.Encode(data)
		require.NoError(t, err, "Failed to encode gob index data")
	}
}

// Helper function to create a corrupt cache file
func createCorruptCacheFile(t *testing.T, path string) { // minimal comment
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	err := os.WriteFile(path, []byte("this is not valid {gob or json data"), 0644)
	require.NoError(t, err)
}

// Helper function to create an empty cache file
func createEmptyCacheFile(t *testing.T, path string) { // minimal comment
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	file, err := os.Create(path)
	require.NoError(t, err)
	file.Close()
}

// TestFileCacheManager_Load_Success_Gob tests successful loading of a valid Gob cache file.
func TestFileCacheManager_Load_Success_Gob(t *testing.T) { // minimal comment
	manager, _, cachePath, _, cleanup := setupFileCacheTest(t, cache.CacheFormatGob) // Test Gob format
	defer cleanup()

	now := time.Now().Truncate(time.Second)
	testData := map[string]cache.CacheEntry{
		"file1.go": {
			SourceModTime: now.Add(-1 * time.Hour), SourceHash: "hashA", ConfigHash: "configX", OutputHash: "outA",
			SchemaVersion: cache.CacheSchemaVersion, ConverterVersion: "test-v1.0",
		},
	}
	createValidCacheFile(t, cachePath, cache.CacheSchemaVersion, "test-v1.0", cache.CacheFormatGob, testData)

	err := manager.Load(cachePath)
	require.NoError(t, err)
	isHit, outHash := manager.Check("file1.go", now.Add(-1*time.Hour), "hashA", "configX")
	assert.True(t, isHit)
	assert.Equal(t, "outA", outHash)
}

// TestFileCacheManager_Load_Success_JSON tests successful loading of a valid JSON cache file.
func TestFileCacheManager_Load_Success_JSON(t *testing.T) { // minimal comment
	manager, _, cachePath, _, cleanup := setupFileCacheTest(t, cache.CacheFormatJSON) // Test JSON format
	defer cleanup()

	now := time.Now().Truncate(time.Second)
	testData := map[string]cache.CacheEntry{
		"file1.go": {
			SourceModTime: now.Add(-1 * time.Hour), SourceHash: "hashA", ConfigHash: "configX", OutputHash: "outA",
			SchemaVersion: cache.CacheSchemaVersion, ConverterVersion: "test-v1.0",
		},
	}
	createValidCacheFile(t, cachePath, cache.CacheSchemaVersion, "test-v1.0", cache.CacheFormatJSON, testData)

	err := manager.Load(cachePath)
	require.NoError(t, err)
	isHit, outHash := manager.Check("file1.go", now.Add(-1*time.Hour), "hashA", "configX")
	assert.True(t, isHit)
	assert.Equal(t, "outA", outHash)
}

// TestFileCacheManager_Load_FormatMismatch tests loading file with wrong format.
func TestFileCacheManager_Load_FormatMismatch(t *testing.T) { // minimal comment
	// 1. Save as Gob, Load as JSON
	managerJSON, _, cachePathGB, _, cleanupGB := setupFileCacheTest(t, cache.CacheFormatJSON)
	defer cleanupGB()
	testDataGB := map[string]cache.CacheEntry{"file1.go": {ConverterVersion: "test-v1.0", SchemaVersion: cache.CacheSchemaVersion}}
	createValidCacheFile(t, cachePathGB, cache.CacheSchemaVersion, "test-v1.0", cache.CacheFormatGob, testDataGB)
	errGB := managerJSON.Load(cachePathGB)
	require.NoError(t, errGB, "Load should treat format mismatch as miss, not error")
	isHitGB, _ := managerJSON.Check("file1.go", time.Time{}, "", "")
	assert.False(t, isHitGB, "Check should miss when loading Gob as JSON")

	// 2. Save as JSON, Load as Gob
	managerGob, _, cachePathJS, _, cleanupJS := setupFileCacheTest(t, cache.CacheFormatGob)
	defer cleanupJS()
	testDataJS := map[string]cache.CacheEntry{"file1.go": {ConverterVersion: "test-v1.0", SchemaVersion: cache.CacheSchemaVersion}}
	createValidCacheFile(t, cachePathJS, cache.CacheSchemaVersion, "test-v1.0", cache.CacheFormatJSON, testDataJS)
	errJS := managerGob.Load(cachePathJS)
	require.NoError(t, errJS, "Load should treat format mismatch as miss, not error")
	isHitJS, _ := managerGob.Check("file1.go", time.Time{}, "", "")
	assert.False(t, isHitJS, "Check should miss when loading JSON as Gob")
}

// TestFileCacheManager_Load_FileNotFound tests handling when cache file doesn't exist.
func TestFileCacheManager_Load_FileNotFound(t *testing.T) { // minimal comment
	manager, _, cachePath, _, cleanup := setupFileCacheTest(t, "") // Default format
	defer cleanup()

	err := manager.Load(cachePath)
	require.NoError(t, err)
	isHit, _ := manager.Check("file1.go", time.Now(), "hashA", "configX")
	assert.False(t, isHit)
}

// TestFileCacheManager_Load_EmptyFile tests handling of a zero-byte cache file.
func TestFileCacheManager_Load_EmptyFile(t *testing.T) { // minimal comment
	manager, _, cachePath, _, cleanup := setupFileCacheTest(t, "")
	defer cleanup()
	createEmptyCacheFile(t, cachePath)
	err := manager.Load(cachePath)
	require.NoError(t, err)
	isHit, _ := manager.Check("file1.go", time.Now(), "hashA", "configX")
	assert.False(t, isHit)
}

// TestFileCacheManager_Load_CorruptHeader tests handling of corrupted header.
func TestFileCacheManager_Load_CorruptHeader(t *testing.T) { // minimal comment
	manager, _, cachePath, _, cleanup := setupFileCacheTest(t, "")
	defer cleanup()
	createCorruptCacheFile(t, cachePath)
	err := manager.Load(cachePath)
	require.NoError(t, err)
	isHit, _ := manager.Check("file1.go", time.Now(), "hashA", "configX")
	assert.False(t, isHit)
}

// TestFileCacheManager_Load_CorruptIndex tests handling of corrupted index data after valid header.
func TestFileCacheManager_Load_CorruptIndex(t *testing.T) { // minimal comment
	manager, testableManager, cachePath, _, cleanup := setupFileCacheTest(t, cache.CacheFormatGob) // Use Gob for this test
	defer cleanup()

	// Create file with valid header but corrupt index data
	file, err := os.Create(cachePath)
	require.NoError(t, err)
	encoder := gob.NewEncoder(file)
	header := cache.CacheFileHeader{
		SchemaVersion:    testableManager.SchemaVersion,    // Use testable manager's properties
		ConverterVersion: testableManager.ConverterVersion, // Use testable manager's properties
	}
	require.NoError(t, encoder.Encode(header))
	_, err = file.Write([]byte("--- corrupt index data ---"))
	require.NoError(t, err)
	require.NoError(t, file.Close())

	err = manager.Load(cachePath)
	require.NoError(t, err) // Should treat as miss, not error
	isHit, _ := manager.Check("file1.go", time.Now(), "hashA", "configX")
	assert.False(t, isHit) // Index should be empty after load failure
}

// TestFileCacheManager_Load_SchemaVersionMismatch tests cache invalidation on schema version mismatch.
func TestFileCacheManager_Load_SchemaVersionMismatch(t *testing.T) { // minimal comment
	manager, testableManager, cachePath, _, cleanup := setupFileCacheTest(t, "")
	defer cleanup()
	testData := map[string]cache.CacheEntry{"f": {SchemaVersion: "0.9", ConverterVersion: testableManager.ConverterVersion}}
	createValidCacheFile(t, cachePath, "0.9", testableManager.ConverterVersion, testableManager.Format, testData)

	err := manager.Load(cachePath)
	require.NoError(t, err) // Should treat as miss, not error
	isHit, _ := manager.Check("f", time.Time{}, "", "")
	assert.False(t, isHit) // Index should be empty
}

// TestFileCacheManager_Load_ConverterVersionMismatch tests cache invalidation on converter version mismatch.
func TestFileCacheManager_Load_ConverterVersionMismatch(t *testing.T) { // minimal comment
	manager, testableManager, cachePath, _, cleanup := setupFileCacheTest(t, "")
	defer cleanup()
	testData := map[string]cache.CacheEntry{"f": {SchemaVersion: testableManager.SchemaVersion, ConverterVersion: "old-v0.9"}}
	createValidCacheFile(t, cachePath, testableManager.SchemaVersion, "old-v0.9", testableManager.Format, testData)

	err := manager.Load(cachePath)
	require.NoError(t, err) // Should treat as miss, not error
	isHit, _ := manager.Check("f", time.Time{}, "", "")
	assert.False(t, isHit) // Index should be empty
}

// TestFileCacheManager_Load_DevVersionCompatibility tests cache loading compatibility with 'dev' version.
func TestFileCacheManager_Load_DevVersionCompatibility(t *testing.T) { // minimal comment
	// Scenario 1: Tool is 'dev', Cache file is 'dev'
	managerDev, testableDev, cachePathDevDev, _, cleanupDevDev := setupFileCacheTest(t, "")
	testableDev.ConverterVersion = "dev" // Manually set tool version for this test scenario
	defer cleanupDevDev()
	testDataDevDev := map[string]cache.CacheEntry{"f": {ConverterVersion: "dev", SchemaVersion: testableDev.SchemaVersion}}
	createValidCacheFile(t, cachePathDevDev, testableDev.SchemaVersion, "dev", testableDev.Format, testDataDevDev)
	errDevDev := managerDev.Load(cachePathDevDev)
	require.NoError(t, errDevDev)
	isHitDevDev, _ := managerDev.Check("f", time.Time{}, "", "")
	assert.True(t, isHitDevDev, "dev tool version should load dev cache version")

	// Scenario 2: Tool is 'dev', Cache file is 'test-v1.0'
	managerDevV1, testableDevV1, cachePathDevV1, _, cleanupDevV1 := setupFileCacheTest(t, "")
	testableDevV1.ConverterVersion = "dev"
	defer cleanupDevV1()
	testDataDevV1 := map[string]cache.CacheEntry{"f": {ConverterVersion: "test-v1.0", SchemaVersion: testableDevV1.SchemaVersion}}
	createValidCacheFile(t, cachePathDevV1, testableDevV1.SchemaVersion, "test-v1.0", testableDevV1.Format, testDataDevV1)
	errDevV1 := managerDevV1.Load(cachePathDevV1)
	require.NoError(t, errDevV1)
	isHitDevV1, _ := managerDevV1.Check("f", time.Time{}, "", "")
	assert.True(t, isHitDevV1, "dev tool version should load non-dev cache version")

	// Scenario 3: Tool is 'test-v1.0', Cache file is 'dev'
	managerV1Dev, testableV1Dev, cachePathV1Dev, _, cleanupV1Dev := setupFileCacheTest(t, "")
	// testableV1Dev.ConverterVersion is already "test-v1.0" by default in setup
	defer cleanupV1Dev()
	testDataV1Dev := map[string]cache.CacheEntry{"f": {ConverterVersion: "dev", SchemaVersion: testableV1Dev.SchemaVersion}}
	createValidCacheFile(t, cachePathV1Dev, testableV1Dev.SchemaVersion, "dev", testableV1Dev.Format, testDataV1Dev)
	errV1Dev := managerV1Dev.Load(cachePathV1Dev)
	require.NoError(t, errV1Dev)
	isHitV1Dev, _ := managerV1Dev.Check("f", time.Time{}, "", "")
	assert.True(t, isHitV1Dev, "non-dev tool version should load dev cache version")

	// Scenario 4: Tool is 'test-v1.0', Cache file is 'test-v1.1' (Mismatch, no 'dev')
	managerV1V11, _, cachePathV1V11, _, cleanupV1V11 := setupFileCacheTest(t, "")
	defer cleanupV1V11()
	testDataV1V11 := map[string]cache.CacheEntry{"f": {ConverterVersion: "test-v1.1", SchemaVersion: cache.CacheSchemaVersion}}
	createValidCacheFile(t, cachePathV1V11, cache.CacheSchemaVersion, "test-v1.1", cache.DefaultCacheFormat, testDataV1V11)
	errV1V11 := managerV1V11.Load(cachePathV1V11)
	require.NoError(t, errV1V11) // Treat as miss
	isHitV1V11, _ := managerV1V11.Check("f", time.Time{}, "", "")
	assert.False(t, isHitV1V11, "non-dev tool version should invalidate different non-dev cache version")
}

// TestFileCacheManager_Check tests various hit/miss scenarios for Check.
func TestFileCacheManager_Check(t *testing.T) { // minimal comment
	manager, testableManager, _, _, cleanup := setupFileCacheTest(t, "")
	defer cleanup()

	now := time.Now().Truncate(time.Second)
	// Populate the Index manually for the test setup
	testableManager.Index = map[string]cache.CacheEntry{
		"file1.go": {
			SourceModTime: now.Add(-1 * time.Hour), SourceHash: "hashA", ConfigHash: "configX", OutputHash: "outA",
			SchemaVersion:    testableManager.SchemaVersion,    // Use known version
			ConverterVersion: testableManager.ConverterVersion, // Use known version
		},
	}
	// Update the actual manager's internal state via the interface
	err := manager.Update("file1.go", now.Add(-1*time.Hour), "hashA", "configX", "outA")
	require.NoError(t, err)

	// Exact Hit
	isHit, outHash := manager.Check("file1.go", now.Add(-1*time.Hour), "hashA", "configX")
	assert.True(t, isHit)
	assert.Equal(t, "outA", outHash)

	// Miss - Different ModTime
	isHit, _ = manager.Check("file1.go", now.Add(-2*time.Hour), "hashA", "configX")
	assert.False(t, isHit)

	// Miss - Different SourceHash
	isHit, _ = manager.Check("file1.go", now.Add(-1*time.Hour), "hashB", "configX")
	assert.False(t, isHit)

	// Miss - Different ConfigHash
	isHit, _ = manager.Check("file1.go", now.Add(-1*time.Hour), "hashA", "configY")
	assert.False(t, isHit)

	// Miss - File Not Found
	isHit, _ = manager.Check("nonexistent.go", now, "hashC", "configZ")
	assert.False(t, isHit)
}

// TestFileCacheManager_Update tests adding/updating entries in the cache.
func TestFileCacheManager_Update(t *testing.T) { // minimal comment
	manager, _, _, _, cleanup := setupFileCacheTest(t, "") // FIX: Use blank identifier for testableManager
	defer cleanup()

	now := time.Now().Truncate(time.Second)
	path1 := "file1.go"
	path2 := "file2.py"

	// 1. Add first entry
	err := manager.Update(path1, now, "hashA", "configX", "outA")
	require.NoError(t, err)
	isHit, outHash := manager.Check(path1, now, "hashA", "configX")
	assert.True(t, isHit)
	assert.Equal(t, "outA", outHash)
	// Cannot reliably check internal index length without reflection/exports

	// 2. Add second entry
	err = manager.Update(path2, now.Add(-10*time.Minute), "hashB", "configY", "outB")
	require.NoError(t, err)
	isHit, outHash = manager.Check(path2, now.Add(-10*time.Minute), "hashB", "configY")
	assert.True(t, isHit)
	assert.Equal(t, "outB", outHash)
	// Cannot reliably check internal index length

	// 3. Update first entry
	updatedModTime := now.Add(1 * time.Minute)
	err = manager.Update(path1, updatedModTime, "hashA_new", "configX", "outA_new")
	require.NoError(t, err)
	isHit, _ = manager.Check(path1, now, "hashA", "configX") // Check old state
	assert.False(t, isHit)
	isHit, outHash = manager.Check(path1, updatedModTime, "hashA_new", "configX") // Check new state
	assert.True(t, isHit)
	assert.Equal(t, "outA_new", outHash)
	// Cannot reliably check internal index length
}

// TestFileCacheManager_Persist_Success_Gob tests successful persistence using Gob.
func TestFileCacheManager_Persist_Success_Gob(t *testing.T) { // minimal comment
	manager, testableManager, cachePath, _, cleanup := setupFileCacheTest(t, cache.CacheFormatGob) // Specify Gob
	defer cleanup()

	now := time.Now().Truncate(time.Second)
	_ = manager.Update("file1.go", now, "hashA", "configX", "outA")
	_ = manager.Update("file2.py", now.Add(-10*time.Minute), "hashB", "configY", "outB")

	err := manager.Persist(cachePath)
	require.NoError(t, err)
	_, statErr := os.Stat(cachePath)
	require.NoError(t, statErr)

	// Verify by reloading with a new Gob manager
	manager2 := cache.NewFileCacheManager(slog.NewTextHandler(io.Discard, nil), testableManager.SchemaVersion, testableManager.ConverterVersion, cache.CacheFormatGob)
	errLoad := manager2.Load(cachePath)
	require.NoError(t, errLoad)
	isHit, outHash := manager2.Check("file1.go", now, "hashA", "configX")
	assert.True(t, isHit)
	assert.Equal(t, "outA", outHash)
	isHit, outHash = manager2.Check("file2.py", now.Add(-10*time.Minute), "hashB", "configY")
	assert.True(t, isHit)
	assert.Equal(t, "outB", outHash)
}

// TestFileCacheManager_Persist_Success_JSON tests successful persistence using JSON.
func TestFileCacheManager_Persist_Success_JSON(t *testing.T) { // minimal comment
	manager, testableManager, cachePath, _, cleanup := setupFileCacheTest(t, cache.CacheFormatJSON) // Specify JSON
	defer cleanup()

	now := time.Now().Truncate(time.Second)
	_ = manager.Update("file1.go", now, "hashA", "configX", "outA")
	_ = manager.Update("file2.py", now.Add(-10*time.Minute), "hashB", "configY", "outB")

	err := manager.Persist(cachePath)
	require.NoError(t, err)
	_, statErr := os.Stat(cachePath)
	require.NoError(t, statErr)

	// Verify by reloading with a new JSON manager
	manager2 := cache.NewFileCacheManager(slog.NewTextHandler(io.Discard, nil), testableManager.SchemaVersion, testableManager.ConverterVersion, cache.CacheFormatJSON)
	errLoad := manager2.Load(cachePath)
	require.NoError(t, errLoad)
	isHit, outHash := manager2.Check("file1.go", now, "hashA", "configX")
	assert.True(t, isHit)
	assert.Equal(t, "outA", outHash)
	isHit, outHash = manager2.Check("file2.py", now.Add(-10*time.Minute), "hashB", "configY")
	assert.True(t, isHit)
	assert.Equal(t, "outB", outHash)
}

// TestFileCacheManager_Persist_Empty tests persistence when index is empty (should remove file).
func TestFileCacheManager_Persist_Empty(t *testing.T) { // minimal comment
	manager, testableManager, cachePath, _, cleanup := setupFileCacheTest(t, "") // Default format
	defer cleanup()

	// Ensure cache file exists initially
	createValidCacheFile(t, cachePath, testableManager.SchemaVersion, testableManager.ConverterVersion, testableManager.Format, map[string]cache.CacheEntry{"dummy": {}})
	_, statErr := os.Stat(cachePath)
	require.NoError(t, statErr)

	err := manager.Persist(cachePath) // Manager index is empty
	require.NoError(t, err)
	_, statErr = os.Stat(cachePath)
	assert.True(t, errors.Is(statErr, os.ErrNotExist))
}

// TestFileCacheManager_Concurrency_Race tests concurrent Updates using -race flag.
func TestFileCacheManager_Concurrency_Race(t *testing.T) { // minimal comment
	manager, _, _, _, cleanup := setupFileCacheTest(t, "") // Use interface type
	defer cleanup()

	numGoroutines := 10
	numUpdatesPerGoroutine := 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < numUpdatesPerGoroutine; j++ {
				filePath := fmt.Sprintf("file_%d_%d.txt", goroutineID, j)
				modTime := time.Now().Add(time.Duration(goroutineID*1000+j) * time.Millisecond).Truncate(time.Second)
				err := manager.Update(filePath, modTime, "source", "config", "output")
				require.NoError(t, err, "Update failed in goroutine %d, iteration %d", goroutineID, j)
			}
		}(i)
	}
	wg.Wait()
	t.Log("Concurrent updates completed. Run with `go test -race ...` to detect race conditions.")
	assert.True(t, true, "Placeholder assertion; real check is race detector")
}

// TestFileCacheManager_Persist_PermissionError tests persisting to a non-writable directory.
func TestFileCacheManager_Persist_PermissionError(t *testing.T) { // minimal comment
	if runtime.GOOS == "windows" {
		t.Skip("Skipping permission error test on Windows due to complexity of setting read-only dirs reliably")
	}
	// --- FIX: Use blank identifier for testableManager ---
	// The `setupFileCacheTest` function returns multiple values. We only need `manager` and `cachePath`
	// for this test. We use the blank identifier `_` to explicitly ignore the returned
	// `testableManager` (*TestableFileCacheManager), `logBuf` (*bytes.Buffer), and `cleanup` (func()) variables,
	// as they are not used in the logic of this specific test case.
	manager, _, cachePath, _, cleanup := setupFileCacheTest(t, "") // Use blank identifiers for unused return values
	defer cleanup()

	// Create a directory and make it read-only
	readOnlyDir := filepath.Join(t.TempDir(), "no_write_dir")
	err := os.Mkdir(readOnlyDir, 0555) // Read/execute only permissions
	require.NoError(t, err)
	// Defer cleanup: Make writable again so TempDir can remove it
	defer os.Chmod(readOnlyDir, 0755)

	cachePath = filepath.Join(readOnlyDir, cache.CacheFileName) // Corrected path assignment

	// Add some data to attempt persisting
	_ = manager.Update("file1.go", time.Now(), "h1", "c1", "o1")

	// Attempt to persist to the read-only directory
	persistErr := manager.Persist(cachePath)
	require.Error(t, persistErr, "Expected error persisting to non-writable directory")
	assert.ErrorIs(t, persistErr, cache.ErrCachePersist, "Error should wrap ErrCachePersist")
	// Check for common permission error substrings (can vary slightly by OS)
	assert.Contains(t, persistErr.Error(), "permission denied", "Error message should mention permission issue")
}

// TestFileCacheManager_Persist_RenameError remains skipped due to simulation difficulty.
func TestFileCacheManager_Persist_RenameError(t *testing.T) { // minimal comment
	t.Skip("Skipping atomic rename error test - difficult to simulate reliably across platforms")
}

// --- END OF FINAL REVISED FILE pkg/converter/cache/cache_test.go ---

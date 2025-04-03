// --- START OF NEW FILE internal/testutil/helpers.go ---
package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// CreateDummyFile creates a dummy file with specified content at the given path,
// ensuring parent directories exist. It uses require assertions for test setup.
func CreateDummyFile(t *testing.T, path string, content string) {
	t.Helper()
	// Use absolute paths if needed, but often relative within temp dirs is fine
	// For consistency, let's stick to the provided path logic
	fullPath := filepath.Clean(path) // Clean the path
	dir := filepath.Dir(fullPath)
	// Ensure parent directory exists
	err := os.MkdirAll(dir, 0755)
	require.NoError(t, err, "Failed to create directory %s for dummy file", dir)
	// Write the file content
	err = os.WriteFile(fullPath, []byte(content), 0644)
	require.NoError(t, err, "Failed to write dummy file %s", fullPath)
}

// CreateDummyDir ensures a directory exists at the given path, creating parents if needed.
func CreateDummyDir(t *testing.T, path string) {
	t.Helper()
	fullPath := filepath.Clean(path)
	// MkdirAll handles creating the directory and its parents.
	// It returns nil if the directory already exists.
	err := os.MkdirAll(fullPath, 0755)
	require.NoError(t, err, "Failed to create dummy directory %s", fullPath)
}

// --- END OF NEW FILE internal/testutil/helpers.go ---

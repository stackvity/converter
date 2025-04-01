// --- START OF FINAL REVISED FILE pkg/converter/git/client.go ---
package git

import (
	"errors"
	"fmt"
)

// --- Error Variables ---

// ErrGitOperation indicates a failure during a Git operation performed via the GitClient.
// This might be due to the path not being a repository, an invalid reference, or errors
// running the underlying Git command or using the Git library. Implementations should
// wrap specific underlying errors with this variable (using Errorf or fmt.Errorf %w)
// for consistent checking using errors.Is(err, ErrGitOperation).
// Ref: SC-STORY-020
var ErrGitOperation = errors.New("git operation failed")

// --- Interfaces ---

// GitClient defines methods for interacting with Git repositories to retrieve
// metadata or information about changed files. Implementations might use the
// native `git` command via `os/exec` or a library like `go-git`.
// Ref: SC-STORY-014, SC-STORY-047
//
// Stability: Public Stable API - Implementations can be provided externally.
// Implementations should handle cases where a path is not within a Git repository
// gracefully, typically by returning an error wrapping ErrGitOperation.
// Implementations should ideally log which backend (e.g., "go-git", "exec") is active.
type GitClient interface {
	// GetFileMetadata retrieves metadata for a specific file within a repository.
	// Returns an error wrapping ErrGitOperation if metadata cannot be retrieved.
	// Ref: LIB.15.1
	GetFileMetadata(repoPath, filePath string) (map[string]string, error)

	// GetChangedFiles retrieves a list of files that have changed relative to a certain state.
	// Returns an error wrapping ErrGitOperation if the operation fails.
	// Ref: CLI.9.1
	GetChangedFiles(repoPath, mode string, ref string) ([]string, error)
}

// Errorf returns a formatted error that wraps ErrGitOperation.
// Helper intended for use by GitClient implementations.
func Errorf(format string, args ...interface{}) error {
	// minimal comment
	return fmt.Errorf("%w: "+format, append([]interface{}{ErrGitOperation}, args...)...)
}

// --- END OF FINAL REVISED FILE pkg/converter/git/client.go ---

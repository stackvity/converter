// --- START OF FINAL REVISED FILE internal/cli/git/git_gogit.go ---
//go:build gogit

package git

import (
	"context" // Keep context import for internal use (context.Background())
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing" // Import object package
	"github.com/go-git/go-git/v5/plumbing/storer"

	"github.com/stackvity/stack-converter/pkg/converter"
	libgit "github.com/stackvity/stack-converter/pkg/converter/git" // Use alias
)

// GoGitClient implements the GitClient interface using go-git.
type GoGitClient struct {
	logger *slog.Logger
}

// NewGoGitClient creates a new GoGitClient.
// Returns the interface type for consistency.
func NewGoGitClient(loggerHandler slog.Handler) converter.GitClient {
	if loggerHandler == nil {
		loggerHandler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
	}
	logger := slog.New(loggerHandler).With(slog.String("component", "gitClient"), slog.String("backend", "go-git"))
	logger.Debug("Using 'go-git' backend for Git operations.")
	return &GoGitClient{logger: logger}
}

// IsGitAvailable checks if the go-git library can open the repository.
// Note: This doesn't guarantee all operations will succeed, just that go-git is usable.
// For simplicity, we can return true as the check happens during operations.
// A more robust check might try a simple `openRepo`.
func (c *GoGitClient) IsGitAvailable() bool {
	// Assume go-git is available if this code is compiled.
	// The actual check for repo existence happens in the methods.
	return true
}

// openRepo helper function to open repository.
func (c *GoGitClient) openRepo(repoPath string) (*git.Repository, error) {
	// Ensure we are working with an absolute path for PlainOpenWithOptions
	absRepoPath, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, libgit.Errorf("failed to get absolute path for repository '%s': %w", repoPath, err)
	}

	repo, err := git.PlainOpenWithOptions(absRepoPath, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		if errors.Is(err, git.ErrRepositoryNotExists) {
			// Use the package-level Errorf helper for consistency
			specificErr := libgit.Errorf("repository not found at or above path '%s': %w", absRepoPath, err)
			return nil, specificErr
		}
		// Use the package-level Errorf helper for consistency
		return nil, libgit.Errorf("failed to open repository at '%s': %w", absRepoPath, err)
	}
	return repo, nil
}

// resolveRevision helper function.
func (c *GoGitClient) resolveRevision(repo *git.Repository, refName string) (*plumbing.Hash, error) {
	// ResolveRevision handles symbolic refs (like HEAD, branches, tags) and partial hashes.
	// It also supports relative refs like HEAD~1.
	hash, err := repo.ResolveRevision(plumbing.Revision(refName))
	if err != nil {
		logArgs := []any{slog.String("ref", refName), slog.Any("error", err)}
		c.logger.Error("Failed to resolve revision", logArgs...)
		// FIX: Replace ErrRevisionNotFound with ErrObjectNotFound
		if errors.Is(err, plumbing.ErrReferenceNotFound) || errors.Is(err, plumbing.ErrObjectNotFound) {
			// Use the package-level Errorf helper for consistency
			return nil, libgit.Errorf("invalid git reference '%s': %w", refName, err)
		}
		// Use the package-level Errorf helper for consistency
		return nil, libgit.Errorf("could not resolve git reference '%s': %w", refName, err)
	}
	return hash, nil
}

// GetChangedFiles implements the GitClient interface using go-git.
// Removed the ctx context.Context parameter to match the interface.
func (c *GoGitClient) GetChangedFiles(repoPath, mode string, ref string) ([]string, error) {
	logArgs := []any{slog.String("repo", repoPath), slog.String("mode", mode), slog.String("ref", ref)}
	c.logger.Debug("GoGitClient: Getting changed files", logArgs...)

	repo, err := c.openRepo(repoPath) // openRepo handles absolute path conversion
	if err != nil {
		// Return error from openRepo directly, as it's already wrapped
		c.logger.Error("Failed to open repository", append(logArgs, slog.Any("error", err))...)
		return nil, err
	}

	worktree, err := repo.Worktree()
	if err != nil {
		wrappedErr := libgit.Errorf("failed to get worktree for repository '%s': %w", repoPath, err)
		c.logger.Error("Failed to get worktree", append(logArgs, slog.Any("error", wrappedErr))...)
		return nil, wrappedErr
	}

	changedFilesMap := make(map[string]struct{})
	// Use context.Background() with timeout for internal operations needing a context.
	// Note: Status() below does not use context, but PatchContext() does.
	cmdCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Removed select check on parent ctx.Done()

	switch mode {
	case string(converter.GitDiffModeDiffOnly):
		// FIX: Call worktree.Status() instead of StatusContext(cmdCtx)
		status, err := worktree.Status()
		if err != nil {
			// FIX: Removed context error check as Status() doesn't use context
			wrappedErr := libgit.Errorf("failed to get git status for repository '%s': %w", repoPath, err)
			c.logger.Error("Failed to get git status", append(logArgs, slog.Any("error", wrappedErr))...)
			return nil, wrappedErr
		}
		c.logger.Debug("Git status obtained", append(logArgs, slog.Int("count", len(status)))...)
		for filePath, fileStatus := range status {
			// Removed select check on parent ctx.Done()

			// Logic for determining changed files remains the same
			// Includes staged (A, M, D, R, C) and unstaged (M, D)
			// Excludes untracked (??) and implicitly ignored files (which also appear as Untracked)
			isUntracked := fileStatus.Staging == git.Untracked && fileStatus.Worktree == git.Untracked
			// FIX: Remove incorrect check for git.Ignored constant
			// isIgnored := fileStatus.Staging == git.Ignored || fileStatus.Worktree == git.Ignored
			isChanged := !isUntracked && (fileStatus.Staging != git.Unmodified || fileStatus.Worktree != git.Unmodified)

			if isChanged {
				normalizedPath := filepath.ToSlash(filePath)
				changedFilesMap[normalizedPath] = struct{}{}
				// FIX: Replace fileStatus.String() with a constructed representation
				statusString := fmt.Sprintf("Staging: %c, Worktree: %c", fileStatus.Staging, fileStatus.Worktree)
				c.logger.Debug("DiffOnly: Found changed file", append(logArgs, slog.String("path", normalizedPath), slog.String("status", statusString))...)
			} else {
				// FIX: Replace fileStatus.String() with a constructed representation
				statusString := fmt.Sprintf("Staging: %c, Worktree: %c", fileStatus.Staging, fileStatus.Worktree)
				// This branch now includes Untracked files (which include ignored ones) and Unmodified files.
				c.logger.Debug("DiffOnly: Ignoring unmodified/untracked/ignored file", append(logArgs, slog.String("path", filePath), slog.String("status", statusString))...)
			}
		}

	case string(converter.GitDiffModeSince):
		if ref == "" {
			return nil, libgit.Errorf("git diff mode 'since' requires a non-empty reference")
		}

		headRef, err := repo.Head()
		if err != nil {
			// Handle case where HEAD doesn't exist (e.g., empty repo)
			if errors.Is(err, plumbing.ErrReferenceNotFound) {
				c.logger.Warn("HEAD reference not found, repository might be empty", logArgs...)
				return []string{}, nil // No changes since HEAD doesn't exist
			}
			return nil, libgit.Errorf("failed to get HEAD reference for repository '%s': %w", repoPath, err)
		}
		headCommit, err := repo.CommitObject(headRef.Hash())
		if err != nil {
			return nil, libgit.Errorf("failed to get HEAD commit object for repository '%s': %w", repoPath, err)
		}

		sinceHash, err := c.resolveRevision(repo, ref)
		if err != nil {
			return nil, err // resolveRevision already returns wrapped libgit.ErrGitOperation
		}
		sinceCommit, err := repo.CommitObject(*sinceHash)
		if err != nil {
			return nil, libgit.Errorf("failed to get commit object for 'since' reference '%s' in repository '%s': %w", ref, repoPath, err)
		}

		// Pass cmdCtx to PatchContext.
		patch, err := sinceCommit.PatchContext(cmdCtx, headCommit)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				wrappedErr := libgit.Errorf("context error during git patch generation in '%s': %w", repoPath, err)
				c.logger.Error("Internal context error during git patch generation", append(logArgs, slog.Any("error", wrappedErr))...)
				return nil, wrappedErr // Return wrapped error
			}
			wrappedErr := libgit.Errorf("failed to generate patch between '%s' and HEAD in repository '%s': %w", ref, repoPath, err)
			c.logger.Error("Failed to generate patch", append(logArgs, slog.Any("error", wrappedErr))...)
			return nil, wrappedErr
		}

		for _, filePatch := range patch.FilePatches() {
			// Removed select check on parent ctx.Done()

			// Logic for extracting paths from patch remains the same
			from, to := filePatch.Files()
			if to != nil { // File was added or modified
				normalizedPath := filepath.ToSlash(to.Path())
				changedFilesMap[normalizedPath] = struct{}{}
				c.logger.Debug("SinceRef: Found changed/added file", append(logArgs, slog.String("path", normalizedPath))...)
			} else if from != nil { // File was deleted ('to' is nil)
				normalizedPath := filepath.ToSlash(from.Path())
				changedFilesMap[normalizedPath] = struct{}{}
				c.logger.Debug("SinceRef: Found deleted file", append(logArgs, slog.String("path", normalizedPath))...)
			}
		}

	default:
		return nil, libgit.Errorf("unsupported git diff mode: %s", mode)
	}

	// Convert map keys to slice
	files := make([]string, 0, len(changedFilesMap))
	for k := range changedFilesMap {
		files = append(files, k)
	}
	c.logger.Debug("GoGitClient: Found changed files", append(logArgs, slog.Int("count", len(files)))...)
	return files, nil
}

// GetFileMetadata implements the GitClient interface using go-git.
// Removed the ctx context.Context parameter to match the interface.
func (c *GoGitClient) GetFileMetadata(repoPath, filePath string) (map[string]string, error) {
	logArgs := []any{slog.String("repo", repoPath), slog.String("file", filePath)}
	c.logger.Debug("GoGitClient: Getting file metadata", logArgs...)

	repo, err := c.openRepo(repoPath) // Handles absolute path logic
	if err != nil {
		// Treat repo not found as non-fatal for optional metadata
		if errors.Is(err, git.ErrRepositoryNotExists) {
			c.logger.Debug("Repository not found, skipping git metadata", logArgs...)
			return map[string]string{}, nil // Not an error
		}
		// Log other open errors as debug, return empty map as metadata is optional
		wrappedErr := fmt.Errorf("failed to open repository for metadata lookup: %w", err) // Keep original error type if possible
		c.logger.Debug(wrappedErr.Error(), logArgs...)
		return map[string]string{}, nil // Not an error
	}

	worktree, err := repo.Worktree()
	if err != nil {
		wrappedErr := fmt.Errorf("failed to get worktree for metadata lookup: %w", err)
		c.logger.Debug(wrappedErr.Error(), logArgs...)
		return map[string]string{}, nil // Not an error
	}
	absRepoRoot := worktree.Filesystem.Root()

	absFilePath, err := filepath.Abs(filePath)
	if err != nil {
		wrappedErr := fmt.Errorf("could not get absolute path for metadata lookup: %w", err)
		c.logger.Debug(wrappedErr.Error(), append(logArgs, slog.String("filePath", filePath))...)
		return map[string]string{}, nil // Not an error
	}

	relPath, err := filepath.Rel(absRepoRoot, absFilePath)
	if err != nil {
		wrappedErr := fmt.Errorf("could not calculate relative path for metadata lookup: %w", err)
		c.logger.Debug(wrappedErr.Error(), append(logArgs, slog.String("absRepoRoot", absRepoRoot), slog.String("absFilePath", absFilePath))...)
		return map[string]string{}, nil // Not an error
	}
	// Need to check if the relative path starts with '..' which indicates it's outside the repo root.
	// filepath.Clean resolves this properly.
	if strings.HasPrefix(filepath.Clean(relPath), "..") {
		c.logger.Debug("File path is outside the repository worktree, skipping git metadata", append(logArgs, slog.String("relPath", relPath))...)
		return map[string]string{}, nil // Not an error
	}
	relPath = filepath.ToSlash(relPath) // Ensure consistent separators for LogOptions

	// Use context.Background() with timeout for internal operations needing a context.
	// Note: repo.Log() does not directly use context, but keep for potential future use or consistency.
	cmdCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get iterator for commits affecting the file, ordered by committer time (most recent first).
	// FIX: Use repo.Log() instead of repo.LogContext()
	logIter, err := repo.Log(&git.LogOptions{
		FileName: &relPath,                  // Use pointer to the relative path string
		Order:    git.LogOrderCommitterTime, // Get history in reverse chronological order
		// No need for All: true, we only need the first commit from the iterator.
		// n=1 (implicit in Next())
	})
	if err != nil {
		// FIX: Removed context error check as repo.Log() doesn't use it
		// Log other errors as debug, metadata is optional
		wrappedErr := fmt.Errorf("failed to get git log iterator for file (may not exist in history): %w", err)
		c.logger.Debug(wrappedErr.Error(), logArgs...)
		return map[string]string{}, nil // Not an error
	}
	defer logIter.Close()

	// Get the first commit from the iterator (most recent one affecting the file)
	commit, err := logIter.Next()
	if err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, storer.ErrStop) { // io.EOF signals end of iterator
			c.logger.Debug("No commit history found for file (new file or untracked?)", logArgs...)
			return map[string]string{}, nil // Not an error
		}
		// Check for context errors *if* the iteration itself could be context-aware (less likely here)
		// We keep cmdCtx defined above, but Log/Next might not check it. Check just in case.
		if cmdCtx.Err() != nil {
			wrappedErr := fmt.Errorf("context error during git log iteration in '%s': %w", repoPath, cmdCtx.Err())
			c.logger.Warn(wrappedErr.Error(), logArgs...)
			return map[string]string{}, nil // Treat as non-fatal for metadata
		}
		// Log other iteration errors as debug
		wrappedErr := fmt.Errorf("error iterating git log for file: %w", err)
		c.logger.Debug(wrappedErr.Error(), logArgs...)
		return map[string]string{}, nil // Not an error
	}

	// Successfully found the last commit affecting the file
	metadata := map[string]string{
		"commit":      commit.Hash.String(),
		"author":      commit.Author.Name,
		"authorEmail": commit.Author.Email,
		"dateISO":     commit.Author.When.UTC().Format(time.RFC3339), // Use Author date, consistent with git log default
		"dateUnix":    fmt.Sprintf("%d", commit.Author.When.Unix()),
	}

	return metadata, nil // Success
}

// --- END OF FINAL REVISED FILE internal/cli/git/git_gogit.go ---

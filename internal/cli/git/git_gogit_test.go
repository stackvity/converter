// --- START OF FINAL REVISED FILE internal/cli/git/git_exec_test.go ---
//go:build !gogit

// ADD BUILD TAG to match git_exec.go
package git

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stackvity/stack-converter/pkg/converter"
	libgit "github.com/stackvity/stack-converter/pkg/converter/git" // Import aliased package
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// runCmdHelper runs a git command using os/exec, used within setupTestGitRepo.
func runCmdHelper(t *testing.T, repoPath string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = repoPath
	output, err := cmd.CombinedOutput()
	// Allow specific known "safe" errors during setup
	allowedErrors := []string{
		"No commits yet", "nothing to commit", "not a git repository",
		"reinitialized existing", "warning:", // Allow general warnings
	}
	isAllowedError := false
	if err != nil {
		outStr := string(output)
		for _, allowed := range allowedErrors {
			if strings.Contains(outStr, allowed) {
				isAllowedError = true
				break
			}
		}
		if errors.Is(err, context.DeadlineExceeded) {
			isAllowedError = true
			t.Logf("Warning: Git setup command timed out (allowed): git %s", strings.Join(args, " "))
		}
		if !isAllowedError {
			t.Logf("Git setup command failed unexpectedly: git %s\nOutput:\n%s\nError: %v", strings.Join(args, " "), outStr, err)
			// Use require.NoError to fail the test immediately on unexpected setup errors
			require.NoError(t, err, "Unexpected Git setup error")
		}
	}
}

// Helper to create a temporary Git repository for testing
func setupTestGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Skipping Git test: 'git' command not found in PATH")
	}
	repoPath := t.TempDir()
	// Use require.NoError for critical setup steps
	absRepoPath, err := filepath.Abs(repoPath)
	require.NoError(t, err)

	runCmd := func(args ...string) { runCmdHelper(t, absRepoPath, args...) }

	runCmd("init", "--initial-branch=main")
	runCmd("config", "user.email", "test@example.com")
	runCmd("config", "user.name", "Test User")
	runCmd("config", "commit.gpgsign", "false") // Ensure tests don't require GPG signing

	// C1
	err = os.WriteFile(filepath.Join(absRepoPath, "README.md"), []byte("# Initial commit\n"), 0644)
	require.NoError(t, err)
	runCmd("add", "README.md")
	runCmd("commit", "-m", "Initial commit")

	// C2
	err = os.WriteFile(filepath.Join(absRepoPath, "main.go"), []byte("package main\nfunc main(){}\n"), 0644)
	require.NoError(t, err)
	runCmd("add", "main.go")
	runCmd("commit", "-m", "Add main.go")

	return absRepoPath // Return absolute path
}

func TestExecGitClient_IsGitAvailable(t *testing.T) { // minimal comment
	// Create a dummy logger handler
	logHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	// Call the constructor which returns the interface
	clientInterface := NewExecGitClient(logHandler)
	require.NotNil(t, clientInterface, "Failed to create GitClient interface")

	// Perform type assertion to get the concrete type *ExecGitClient
	// because IsGitAvailable is defined on the concrete type, not the interface.
	client, ok := clientInterface.(*ExecGitClient) // FIX: Type assertion
	require.True(t, ok, "Failed to assert *ExecGitClient type")
	require.NotNil(t, client, "Failed to create concrete ExecGitClient after assertion")

	// Check if the underlying git command exists
	_, err := exec.LookPath("git")
	expected := (err == nil)

	// Assert the client's method reflects the system state
	// Call IsGitAvailable on the concrete type 'client'
	assert.Equal(t, expected, client.IsGitAvailable()) // FIX: Call on concrete type
}

// Recommendation 2: Use Table-Driven Tests
func TestExecGitClient_GetChangedFiles(t *testing.T) { // minimal comment
	logHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	client := NewExecGitClient(logHandler)                      // Returns converter.GitClient interface
	require.NotNil(t, client, "Failed to create ExecGitClient") // FIX: Check constructor was found
	// Remove ctx context.Background() as it's no longer passed to the method.

	testCases := []struct {
		name         string
		mode         string
		ref          string
		setupFunc    func(t *testing.T, repoPath string) // Use absolute repo path
		expected     []string
		expectError  bool
		errorIs      error  // Expected error type (e.g., libgit.ErrGitOperation)
		errorContain string // Expected substring in error message
	}{
		{
			name: "DiffOnly - Staged and Unstaged",
			mode: string(converter.GitDiffModeDiffOnly),
			ref:  "",
			setupFunc: func(t *testing.T, repoPath string) {
				require.NoError(t, os.WriteFile(filepath.Join(repoPath, "main.go"), []byte("// modified\n"), 0644)) // Modify existing tracked file
				require.NoError(t, os.WriteFile(filepath.Join(repoPath, "new_staged.txt"), []byte("new content"), 0644))
				runCmdHelper(t, repoPath, "add", "new_staged.txt")                                              // Stage the new file
				require.NoError(t, os.WriteFile(filepath.Join(repoPath, "untracked.tmp"), []byte("tmp"), 0644)) // Untracked file, should be ignored
			},
			expected:     []string{"main.go", "new_staged.txt"}, // Expect modified tracked file and new staged file
			expectError:  false,
			errorContain: "",
		},
		{
			name: "DiffOnly - Only Staged Delete",
			mode: string(converter.GitDiffModeDiffOnly),
			ref:  "",
			setupFunc: func(t *testing.T, repoPath string) {
				runCmdHelper(t, repoPath, "rm", "main.go") // Stages deletion
			},
			expected:     []string{"main.go"}, // Path is reported even though deleted
			expectError:  false,
			errorContain: "",
		},
		{
			name:         "DiffOnly - No Changes",
			mode:         string(converter.GitDiffModeDiffOnly),
			ref:          "",
			setupFunc:    func(t *testing.T, repoPath string) {},
			expected:     []string{},
			expectError:  false,
			errorContain: "",
		},
		{
			name:         "SinceRef - Valid Ref (HEAD~1)",
			mode:         string(converter.GitDiffModeSince),
			ref:          "HEAD~1", // Changes between C1 and C2 (HEAD)
			setupFunc:    func(t *testing.T, repoPath string) {},
			expected:     []string{"main.go"},
			expectError:  false,
			errorContain: "",
		},
		{
			name: "SinceRef - Valid Ref With Deletion (HEAD~1)",
			mode: string(converter.GitDiffModeSince),
			ref:  "HEAD~1", // Changes between C2 and C3 (HEAD)
			setupFunc: func(t *testing.T, repoPath string) {
				// Create commit C3
				runCmd := func(args ...string) { runCmdHelper(t, repoPath, args...) }
				runCmd("rm", "main.go")
				require.NoError(t, os.WriteFile(filepath.Join(repoPath, "deleted.txt"), []byte("added C3"), 0644))
				runCmd("add", "deleted.txt")
				runCmd("commit", "-m", "C3: Delete main.go, add deleted.txt")
			},
			expected:     []string{"main.go", "deleted.txt"}, // main.go (deleted), deleted.txt (added)
			expectError:  false,
			errorContain: "",
		},
		{
			name:         "SinceRef - Invalid Ref",
			mode:         string(converter.GitDiffModeSince),
			ref:          "invalid-ref-does-not-exist",
			setupFunc:    func(t *testing.T, repoPath string) {},
			expected:     nil,
			expectError:  true,
			errorIs:      libgit.ErrGitOperation,
			errorContain: "invalid git reference 'invalid-ref-does-not-exist'",
		},
		{
			name:         "SinceRef - Empty Ref",
			mode:         string(converter.GitDiffModeSince),
			ref:          "",
			setupFunc:    func(t *testing.T, repoPath string) {},
			expected:     nil,
			expectError:  true,
			errorIs:      libgit.ErrGitOperation,
			errorContain: "requires a non-empty reference",
		},
		{
			name:         "Error - Non-Git Repo",
			mode:         string(converter.GitDiffModeDiffOnly),
			ref:          "",
			setupFunc:    nil, // Special handling in loop
			expected:     nil,
			expectError:  true,
			errorIs:      libgit.ErrGitOperation,
			errorContain: "git status command failed",
		},
		{
			name:         "Error - Unsupported Mode",
			mode:         "bad-mode",
			ref:          "",
			setupFunc:    func(t *testing.T, repoPath string) {},
			expected:     nil,
			expectError:  true,
			errorIs:      libgit.ErrGitOperation,
			errorContain: "unsupported git diff mode",
		},
		{
			name:         "Error - Repo Path Not Directory",
			mode:         string(converter.GitDiffModeDiffOnly),
			ref:          "",
			setupFunc:    nil, // Special handling in loop
			expected:     nil,
			expectError:  true,
			errorIs:      libgit.ErrGitOperation,
			errorContain: "repository path is not a directory",
		},
		{
			name:         "Error - Repo Path Not Exist",
			mode:         string(converter.GitDiffModeDiffOnly),
			ref:          "",
			setupFunc:    nil, // Special handling in loop
			expected:     nil,
			expectError:  true,
			errorIs:      libgit.ErrGitOperation,
			errorContain: "repository path does not exist",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var targetPath string
			if tc.name == "Error - Non-Git Repo" {
				// Create a non-git directory for this specific test case
				targetPath = t.TempDir()
				_, err := filepath.Abs(targetPath) // Ensure absolute
				require.NoError(t, err)
			} else if tc.name == "Error - Repo Path Not Directory" {
				// Create a file instead of a directory
				targetPath = filepath.Join(t.TempDir(), "repo_is_a_file.txt")
				require.NoError(t, os.WriteFile(targetPath, []byte("i am a file"), 0644))
				_, err := filepath.Abs(targetPath) // Ensure absolute
				require.NoError(t, err)
			} else if tc.name == "Error - Repo Path Not Exist" {
				targetPath = filepath.Join(t.TempDir(), "non_existent_repo_path")
				// Ensure it doesn't exist
				_ = os.RemoveAll(targetPath)
				_, err := filepath.Abs(targetPath) // Ensure absolute path conceptually
				require.NoError(t, err)
			} else {
				// Setup a git repo for other cases
				targetPath = setupTestGitRepo(t) // Returns absolute path
				if tc.setupFunc != nil {
					tc.setupFunc(t, targetPath)
				}
			}

			// Remove the ctx argument from the call.
			changedFiles, err := client.GetChangedFiles(targetPath, tc.mode, tc.ref)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorIs != nil {
					assert.ErrorIs(t, err, tc.errorIs)
				}
				if tc.errorContain != "" {
					assert.Contains(t, err.Error(), tc.errorContain)
				}
			} else {
				require.NoError(t, err)
				// Use ElementsMatch for reliable comparison regardless of order
				if len(tc.expected) == 0 {
					assert.Empty(t, changedFiles)
				} else {
					assert.ElementsMatch(t, tc.expected, changedFiles)
				}
			}
		})
	}
}

// Recommendation 2: Use Table-Driven Tests
func TestExecGitClient_GetFileMetadata(t *testing.T) { // minimal comment
	logHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
	client := NewExecGitClient(logHandler)                      // Returns converter.GitClient interface
	require.NotNil(t, client, "Failed to create ExecGitClient") // FIX: Check constructor was found
	// Remove ctx context.Background() as it's no longer passed to the method.

	repoPath := setupTestGitRepo(t) // Shared repo for most cases (absolute path)

	// Add file with spaces
	spacedFileName := "file with spaces.txt"
	subDir := "subdir space"
	err := os.MkdirAll(filepath.Join(repoPath, subDir), 0755)
	require.NoError(t, err)
	spacedFilePathAbs := filepath.Join(repoPath, subDir, spacedFileName)
	err = os.WriteFile(spacedFilePathAbs, []byte("content space"), 0644)
	require.NoError(t, err)
	runCmdHelper(t, repoPath, "add", ".") // Add everything in the directory
	runCmdHelper(t, repoPath, "commit", "-m", "Add file with spaces")

	untrackedFilePath := filepath.Join(repoPath, "untracked.tmp")
	err = os.WriteFile(untrackedFilePath, []byte("tmp"), 0644)
	require.NoError(t, err)

	// Create a file outside the repo
	outsideRepoDir := t.TempDir()
	outsideFilePath := filepath.Join(outsideRepoDir, "outside.txt")
	err = os.WriteFile(outsideFilePath, []byte("outside"), 0644)
	require.NoError(t, err)
	defer os.Remove(outsideFilePath)

	testCases := []struct {
		name           string
		filePath       string // Path passed to the function (can be relative or absolute)
		targetRepoPath string // Repo path passed to the function (should be absolute)
		expectedAuthor string // Expected author to verify retrieval
		expectEmpty    bool   // Expect empty map (e.g., untracked, outside)
		expectError    bool   // Should generally be false as metadata is optional
		errorIs        error
		errorContain   string
	}{
		{
			name:           "Success - main.go (Absolute Path)",
			filePath:       filepath.Join(repoPath, "main.go"), // Absolute path to file
			targetRepoPath: repoPath,
			expectedAuthor: "Test User",
			expectEmpty:    false,
			expectError:    false,
		},
		{
			name:           "Success - README.md (Relative Path from Repo Root)",
			filePath:       "README.md", // Relative path
			targetRepoPath: repoPath,
			expectedAuthor: "Test User",
			expectEmpty:    false,
			expectError:    false,
		},
		{
			name:           "Success - File with Spaces",
			filePath:       spacedFilePathAbs, // Absolute path
			targetRepoPath: repoPath,
			expectedAuthor: "Test User",
			expectEmpty:    false,
			expectError:    false,
		},
		{
			name:           "Untracked File",
			filePath:       untrackedFilePath, // Absolute path
			targetRepoPath: repoPath,
			expectEmpty:    true, // Expect empty map for untracked files
			expectError:    false,
		},
		{
			name:           "Non-Existent File",
			filePath:       filepath.Join(repoPath, "nonexistent.file"),
			targetRepoPath: repoPath,
			expectEmpty:    true, // Expect empty map for non-existent files
			expectError:    false,
		},
		{
			name:           "Outside Repo File",
			filePath:       outsideFilePath, // Absolute path outside repoPath
			targetRepoPath: repoPath,        // Tell client where repo is
			expectEmpty:    true,            // Should detect file is outside
			expectError:    false,
		},
		{
			name:           "Non-Git Repo Path",
			filePath:       filepath.Join(outsideRepoDir, "somefile.txt"), // Absolute path in a non-git dir
			targetRepoPath: outsideRepoDir,                                // Pass the non-git dir as repo path
			expectEmpty:    true,                                          // Expect empty map
			expectError:    false,                                         // Should handle gracefully (logs warning/debug)
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Ensure file exists physically for tests needing it
			// This ensures absolute paths work even if the test setup changes cwd
			var absFilePath string
			if _, err := os.Stat(tc.filePath); os.IsNotExist(err) && !strings.Contains(tc.name, "Non-Existent") {
				// If file doesn't exist but should, create it (e.g., for Non-Git Repo Path)
				absTestFilePath, _ := filepath.Abs(tc.filePath)
				require.NoError(t, os.MkdirAll(filepath.Dir(absTestFilePath), 0755))
				require.NoError(t, os.WriteFile(absTestFilePath, []byte("dummy content"), 0644))
				absFilePath = absTestFilePath
				defer os.Remove(absFilePath) // Clean up dummy file
			} else {
				absFilePath, _ = filepath.Abs(tc.filePath)
			}

			// Use absolute file path in the call for consistency
			// The repoPath should already be absolute from setupTestGitRepo
			metadata, err := client.GetFileMetadata(tc.targetRepoPath, absFilePath)

			if tc.expectError {
				require.Error(t, err)
				if tc.errorIs != nil {
					assert.ErrorIs(t, err, tc.errorIs)
				}
				if tc.errorContain != "" {
					assert.Contains(t, err.Error(), tc.errorContain)
				}
			} else {
				require.NoError(t, err, "GetFileMetadata returned an unexpected error")
				if tc.expectEmpty {
					assert.Empty(t, metadata, "Metadata should be empty for this case")
				} else {
					require.NotEmpty(t, metadata, "Metadata should not be empty")
					assert.Equal(t, tc.expectedAuthor, metadata["author"])
					assert.NotEmpty(t, metadata["commit"])
					assert.NotEmpty(t, metadata["authorEmail"])
					assert.NotEmpty(t, metadata["dateISO"])
					// Optionally check for dateUnix if parsing is expected
					if _, ok := metadata["dateUnix"]; ok {
						assert.NotEmpty(t, metadata["dateUnix"])
					}
				}
			}
		})
	}

	// Separate test for Empty Repo case
	t.Run("Empty Repo", func(t *testing.T) {
		emptyRepoPath := t.TempDir()
		absEmptyRepoPath, err := filepath.Abs(emptyRepoPath)
		require.NoError(t, err)

		runCmdHelper(t, absEmptyRepoPath, "init", "--initial-branch=main")
		runCmdHelper(t, absEmptyRepoPath, "config", "user.email", "test@example.com")
		runCmdHelper(t, absEmptyRepoPath, "config", "user.name", "Test User")

		// Create a dummy file to attempt to get metadata for (it won't exist in git history)
		filePath := filepath.Join(absEmptyRepoPath, "newly_created.txt")
		require.NoError(t, os.WriteFile(filePath, []byte("dummy"), 0644))
		defer os.Remove(filePath)

		// Use absolute paths in the call
		metadata, err := client.GetFileMetadata(absEmptyRepoPath, filePath)
		require.NoError(t, err, "GetFileMetadata returned an unexpected error for empty repo")
		assert.Empty(t, metadata, "Expect empty metadata for a file in an empty repo")
	})
}

// --- END OF FINAL REVISED FILE internal/cli/git/git_gogit_test.go ---

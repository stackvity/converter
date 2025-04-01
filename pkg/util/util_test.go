// --- START OF NEW FILE pkg/util/util_test.go ---
package util_test // Test package name convention

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stackvity/stack-converter/pkg/util" // Import the new location
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchesGitignore(t *testing.T) {
	// Setup base paths - ensure they are absolute for realistic testing
	walkerBaseAbs, err := filepath.Abs("/home/user/project")
	require.NoError(t, err)
	subDirAbs := filepath.Join(walkerBaseAbs, "subdir")
	subSubDirAbs := filepath.Join(subDirAbs, "subsub")
	// Normalize paths for Windows compatibility in tests
	if runtime.GOOS == "windows" {
		walkerBaseAbs = filepath.ToSlash(walkerBaseAbs)
		subDirAbs = filepath.ToSlash(subDirAbs)
		subSubDirAbs = filepath.ToSlash(subSubDirAbs)
	}
	testCases := []struct {
		name               string
		pattern            string
		patternBaseAbsPath string // Where the ignore rule is defined
		walkerBaseAbsPath  string // The root directory being walked
		pathToMatchRel     string // Path relative to walkerBaseAbsPath
		isRooted           bool   // Pattern starts with '/' relative to its base
		expectedMatch      bool
	}{
		// --- Basic Matches (relative to walker base, like config patterns) ---
		{
			name:               "Exact file match (config)",
			pattern:            "file.log",
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "file.log",
			isRooted:           false,
			expectedMatch:      true,
		},
		{
			name:               "Glob file match (config)",
			pattern:            "*.log",
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "subdir/debug.log",
			isRooted:           false,
			expectedMatch:      true, // Matches "subdir/debug.log" relative to walkerBase
		},
		{
			name:               "Directory match (config)",
			pattern:            "build", // Pattern loader sets isDirOnly, pattern is just "build"
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "build", // Matching the directory itself
			isRooted:           false,
			expectedMatch:      true,
		},
		{
			name:               "Path component matches directory pattern (config)",
			pattern:            "build",
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "build/file.txt", // Path inside ignored dir
			isRooted:           false,
			expectedMatch:      true, // Pattern "build" matches the first component "build" relative to walkerBase
		},
		{
			name:               "Glob directory match (config)",
			pattern:            "target/build", // isDirOnly=true set by loader
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "target/build",
			isRooted:           false,
			expectedMatch:      true,
		},
		{
			name:               "No match (config)",
			pattern:            "*.tmp",
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "main.go",
			isRooted:           false,
			expectedMatch:      false,
		},
		// --- Rooted Matches (relative to pattern base) ---
		{
			name:               "Rooted file match (ignore file at root)",
			pattern:            "root.log", // Equivalent to /root.log in .gitignore at root
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "root.log",
			isRooted:           true,
			expectedMatch:      true, // Matches "root.log" relative to patternBaseAbsPath
		},
		{
			name:               "Rooted file mismatch (deep file)",
			pattern:            "root.log", // Equivalent to /root.log in .gitignore at root
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "subdir/root.log",
			isRooted:           true,
			expectedMatch:      false, // "subdir/root.log" doesn't match rooted "root.log"
		},
		{
			name:               "Rooted dir match (ignore file at root)",
			pattern:            "build", // Equivalent to /build/ in .gitignore at root
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "build",
			isRooted:           true,
			expectedMatch:      true, // Matches "build" relative to patternBaseAbsPath
		},
		{
			name:               "Rooted dir mismatch (deep dir)",
			pattern:            "build", // Equivalent to /build/ in .gitignore at root
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "subdir/build",
			isRooted:           true,
			expectedMatch:      false, // "subdir/build" doesn't match rooted "build"
		},
		{
			name:               "Rooted file inside root dir (ignore file at root)",
			pattern:            "build", // Equivalent to /build/ in .gitignore at root
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "build/file.txt",
			isRooted:           true,
			expectedMatch:      true, // Matches because "build/file.txt" relative to base starts with "build"
		},
		// --- Non-Rooted Matches (relative to pattern base, potentially deep) ---
		{
			name:               "Non-rooted file match (deep)",
			pattern:            "local.tmp",   // Defined in subdir ignore file
			patternBaseAbsPath: subDirAbs,     // Ignore file is in subdir
			walkerBaseAbsPath:  walkerBaseAbs, // Walking from project root
			pathToMatchRel:     "subdir/local.tmp",
			isRooted:           false,
			expectedMatch:      true, // Matches `local.tmp` relative to subDirAbs
		},
		{
			name:               "Non-rooted glob match (deep)",
			pattern:            "*.log",       // Defined in subdir ignore file
			patternBaseAbsPath: subDirAbs,     // Ignore file is in subdir
			walkerBaseAbsPath:  walkerBaseAbs, // Walking from project root
			pathToMatchRel:     "subdir/subsub/debug.log",
			isRooted:           false,
			expectedMatch:      true, // Matches `subsub/debug.log` relative to subDirAbs because `*.log` matches suffix
		},
		{
			name:               "Non-rooted mismatch (outside pattern base scope)",
			pattern:            "local.tmp",   // Defined in subdir ignore file
			patternBaseAbsPath: subDirAbs,     // Ignore file is in subdir
			walkerBaseAbsPath:  walkerBaseAbs, // Walking from project root
			pathToMatchRel:     "root/local.tmp",
			isRooted:           false,
			expectedMatch:      false, // Path relative to patternBase is "../root/local.tmp", pattern doesn't match
		},
		{
			name:               "Non-rooted match within root from subdir ignore",
			pattern:            "root.log",    // Non-rooted root.log in subdir ignore file
			patternBaseAbsPath: subDirAbs,     // Ignore file is in subdir
			walkerBaseAbsPath:  walkerBaseAbs, // Walking from project root
			pathToMatchRel:     "root.log",    // This file is outside subdir
			isRooted:           false,
			expectedMatch:      false, // Path relative to patternBase is "../root.log", pattern doesn't match
		},
		{
			name:               "Rooted match from subdir ignore",
			pattern:            "subfile.txt", // Like /subfile.txt in subdir ignore file
			patternBaseAbsPath: subDirAbs,     // Ignore file is in subdir
			walkerBaseAbsPath:  walkerBaseAbs, // Walking from project root
			pathToMatchRel:     "subdir/subfile.txt",
			isRooted:           true, // Rooted relative to subdir
			expectedMatch:      true, // Matches "subfile.txt" relative to patternBaseAbsPath
		},
		{
			name:               "Rooted mismatch from subdir ignore (too deep)",
			pattern:            "subfile.txt", // Like /subfile.txt in subdir ignore file
			patternBaseAbsPath: subDirAbs,     // Ignore file is in subdir
			walkerBaseAbsPath:  walkerBaseAbs, // Walking from project root
			pathToMatchRel:     "subdir/subsub/subfile.txt",
			isRooted:           true,  // Rooted relative to subdir
			expectedMatch:      false, // "subsub/subfile.txt" doesn't match rooted "subfile.txt"
		},
		// --- Edge Cases ---
		{
			name:               "Empty pattern",
			pattern:            "",
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "file.txt",
			isRooted:           false,
			expectedMatch:      false,
		},
		{
			name:               "Empty path",
			pattern:            "*.log",
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "",
			isRooted:           false,
			expectedMatch:      false,
		},
		{
			name:               "Dot path",
			pattern:            "*.log",
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     ".",
			isRooted:           false,
			expectedMatch:      false,
		},
		// --- Double Star Cases (Highlighting filepath.Match limitations vs gitignore) ---
		{
			name:               "Double star pattern (simple suffix)",
			pattern:            "**/temp.txt", // filepath.Match handles **/ like *
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "a/b/c/temp.txt",
			isRooted:           false,
			expectedMatch:      true, // Matches because filepath.Match treats **/ as *
		},
		{
			name:               "Double star pattern (middle)",
			pattern:            "a/**/c/*.log", // filepath.Match handles **/ like *
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "a/b/c/file.log",
			isRooted:           false,
			expectedMatch:      true, // Matches because filepath.Match treats **/ as *
		},
		{
			name:               "Double star pattern mismatch",
			pattern:            "a/**/c/*.log",
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "a/b/d/file.log",
			isRooted:           false,
			expectedMatch:      false, // 'd' doesn't match 'c'
		},
		{
			name:               "Double star as directory component",
			pattern:            "**/build", // Should match build anywhere using filepath.Match's * interpretation
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "src/target/build/output.o",
			isRooted:           false,
			expectedMatch:      true, // Matches the 'build' component relative to walker base
		},
		{
			name:               "Double star directory prefix (filepath.Match behavior)",
			pattern:            "**/temp/", // Match translates to */temp/* essentially
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "a/b/temp/file.txt",
			isRooted:           false,
			expectedMatch:      true, // Matches because "a/b/temp/file.txt" contains "temp/" as component prefix
		},
		{
			name:               "Complex Double Star (Gitignore specific, filepath.Match differs)",
			pattern:            "a/**/b/**/c.txt", // Gitignore: 'a', then any dirs, then 'b', then any dirs, then 'c.txt'
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "a/x/y/b/z/c.txt",
			isRooted:           false,
			expectedMatch:      true, // filepath.Match("a/*/b/*/c.txt", "a/x/y/b/z/c.txt") is true
		},
		{
			name:               "Complex Double Star Mismatch (filepath.Match differs)",
			pattern:            "a/**/b/**/c.txt",
			patternBaseAbsPath: walkerBaseAbs,
			walkerBaseAbsPath:  walkerBaseAbs,
			pathToMatchRel:     "a/x/y/d/z/c.txt", // 'd' instead of 'b'
			isRooted:           false,
			expectedMatch:      false, // filepath.Match("a/*/b/*/c.txt", "a/x/y/d/z/c.txt") is false
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Normalize paths for Windows compatibility before passing to function
			patternBaseAbsPathN := tc.patternBaseAbsPath
			walkerBaseAbsPathN := tc.walkerBaseAbsPath
			pathToMatchRelN := tc.pathToMatchRel
			if runtime.GOOS == "windows" {
				patternBaseAbsPathN = filepath.ToSlash(patternBaseAbsPathN)
				walkerBaseAbsPathN = filepath.ToSlash(walkerBaseAbsPathN)
				pathToMatchRelN = filepath.ToSlash(pathToMatchRelN)
			}
			match := util.MatchesGitignore(tc.pattern, patternBaseAbsPathN, walkerBaseAbsPathN, pathToMatchRelN, tc.isRooted)
			assert.Equal(t, tc.expectedMatch, match)
		})
	}
}

// --- END OF NEW FILE pkg/util/util_test.go ---

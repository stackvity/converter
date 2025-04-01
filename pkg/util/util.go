// --- START OF NEW FILE pkg/util/util.go ---
package util // Changed package name to reflect location within pkg

import (
	"path/filepath"
	"strings"
)

// MatchesGitignore checks if a relative path matches a gitignore-style pattern.
// Note: This is a simplified implementation using filepath.Match and does not cover all .gitignore edge cases (e.g., complex ** interactions differ from standard gitignore).
func MatchesGitignore(pattern, patternBaseAbsPath, walkerBaseAbsPath, pathToMatchRel string, isRooted bool) bool {
	// Clean the pattern and path
	pattern = filepath.ToSlash(pattern)
	pathToMatchRel = filepath.ToSlash(pathToMatchRel)
	// Handle empty patterns or paths immediately
	if pattern == "" || pathToMatchRel == "" || pathToMatchRel == "." {
		return false
	}
	// Absolute path of the item being checked
	pathToMatchAbs := filepath.Join(walkerBaseAbsPath, pathToMatchRel)
	// Path relative to the directory containing the ignore pattern's definition
	pathRelToPatternBase, err := filepath.Rel(patternBaseAbsPath, pathToMatchAbs)
	if err != nil {
		// Cannot determine relative path, assume no match
		return false
	}
	pathRelToPatternBase = filepath.ToSlash(pathRelToPatternBase)
	// Check for direct match from the pattern's base
	match, _ := filepath.Match(pattern, pathRelToPatternBase)
	if match {
		return true
	}
	// If pattern is not rooted, check if it matches any directory component deeper than the pattern's base
	if !isRooted {
		// Check if the pattern matches any suffix of the relative path segments
		parts := strings.Split(pathRelToPatternBase, "/")
		for i := range parts {
			subPath := strings.Join(parts[i:], "/")
			match, _ = filepath.Match(pattern, subPath)
			if match {
				return true
			}
		}
		// Also check against the path relative to the walker's root for non-rooted patterns from config
		matchWalkerBase, _ := filepath.Match(pattern, pathToMatchRel)
		if matchWalkerBase {
			return true
		}
	}
	return false
}

// --- END OF NEW FILE pkg/util/util.go ---

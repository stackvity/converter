// --- START OF FINAL REVISED FILE pkg/converter/language/detector.go ---
package language

import (
	"path/filepath"
	"strings"

	// Assume go-enry is the chosen library based on techstack.md and proposal.md
	"github.com/go-enry/go-enry/v2"
)

// LanguageDetector defines the interface for determining the programming language
// of a file based on its content and/or filename.
// Godoc implemented here defining contract (BE-TASK-015).
//
// Stability: Public Stable API - Implementations can be provided externally.
// Adherence to the method contracts, especially error handling and return value semantics, is required.
type LanguageDetector interface {
	// Detect attempts to identify the programming language of the given content.
	// It considers the filename/extension as a hint or fallback.
	//
	// Parameters:
	//   content: A byte slice representing the file's content (UTF-8 assumed).
	//   filePath: The relative path of the file, used for extension mapping/overrides.
	//
	// Returns:
	//   language: The detected language identifier (e.g., "go", "python", "plaintext"), always lowercase. Returns "plaintext" or "unknown" if detection fails completely.
	//   confidence: A score from 0.0 to 1.0 indicating the confidence in the detection.
	//               NOTE (Recommendation 4): For the default `goEnryDetector`, this score is currently indicative (e.g., 1.0 for overrides, ~0.8 for specific language detection by `enry.GetLanguage`, ~0.5 for extension/filename fallback, 0.0 for unknown/plaintext). The `confidenceThreshold` provided during initialization is *not* actively used by this default implementation to filter results due to the nature of the underlying `go-enry` functions utilized. This limitation should be understood by users relying on the confidence score. Future implementations or library updates might enable more granular confidence usage.
	//   err: Any error encountered during the detection process (typically nil, as detection failures usually result in a fallback language). Specific library errors might be wrapped. Callers should check this error and log appropriately if non-nil.
	Detect(content []byte, filePath string) (language string, confidence float64, err error)
}

// goEnryDetector implements the LanguageDetector interface using the go-enry library.
// It incorporates user overrides and a confidence threshold.
type goEnryDetector struct {
	confidenceThreshold float64           // See Detect Godoc NOTE regarding current usage limitations.
	overrides           map[string]string // Map[extension] -> languageID
}

// NewGoEnryDetector creates a new language detector using go-enry logic.
// Normalizes provided overrides (lowercase extension with leading dot, lowercase language ID).
// Unit tests should cover normalization edge cases (Recommendation 5).
func NewGoEnryDetector(confidenceThreshold float64, overrides map[string]string) LanguageDetector { // minimal comment
	normalizedOverrides := make(map[string]string)
	if overrides != nil {
		for ext, lang := range overrides {
			normalizedExt := strings.ToLower(strings.TrimSpace(ext))
			normalizedLang := strings.ToLower(strings.TrimSpace(lang))
			// Skip invalid entries
			if normalizedExt == "" || normalizedLang == "" || normalizedExt == "." {
				continue
			}
			// Ensure leading dot
			if !strings.HasPrefix(normalizedExt, ".") {
				normalizedExt = "." + normalizedExt
			}
			normalizedOverrides[normalizedExt] = normalizedLang // Store normalized lowercase lang
		}
	}

	return &goEnryDetector{
		confidenceThreshold: confidenceThreshold, // Store threshold even if not currently used by Detect logic.
		overrides:           normalizedOverrides,
	}
}

// Detect implements the LanguageDetector interface using go-enry.
// It prioritizes user overrides, then content/filename analysis, then extension, then filename-specific rules.
func (d *goEnryDetector) Detect(content []byte, filePath string) (string, float64, error) { // minimal comment
	// Return early for empty files to avoid unnecessary processing.
	if len(content) == 0 {
		return "unknown", 0.0, nil
	}

	ext := strings.ToLower(filepath.Ext(filePath))
	filename := filepath.Base(filePath)

	// 1. Check User Overrides First (Highest Confidence)
	if lang, ok := d.overrides[ext]; ok {
		return lang, 1.0, nil // Overrides are 100% confident
	}

	// 2. Try Combined Content/Filename Detection via GetLanguage (High Confidence)
	detectedLang := enry.GetLanguage(filename, content)
	if detectedLang != "" && detectedLang != "Text" {
		// GetLanguage determined a specific language. Assign reasonably high confidence.
		return strings.ToLower(detectedLang), 0.8, nil // Indicative score
	}

	// 3. Fallback to Extension if GetLanguage was inconclusive (Medium Confidence)
	langByExt, safeExt := enry.GetLanguageByExtension(filePath)
	if safeExt && langByExt != "" && langByExt != "Text" {
		// Use extension mapping result. Assign medium confidence.
		return strings.ToLower(langByExt), 0.5, nil // Indicative score
	}

	// 4. Handle special filenames if extension didn't match (Medium Confidence)
	langByFilename, safeFilename := enry.GetLanguageByFilename(filePath)
	if safeFilename && langByFilename != "" && langByFilename != "Text" {
		// Use filename mapping result. Assign medium confidence.
		return strings.ToLower(langByFilename), 0.5, nil // Indicative score
	}

	// 5. Absolute Fallback (Low/Zero Confidence)
	// If content exists but no other rule matched, classify as plain text.
	return "plaintext", 0.0, nil
}

// --- END OF FINAL REVISED FILE pkg/converter/language/detector.go ---

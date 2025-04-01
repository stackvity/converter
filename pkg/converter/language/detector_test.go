// --- START OF FINAL REVISED FILE pkg/converter/language/detector_test.go ---
package language_test

import (
	"testing"

	"github.com/stackvity/stack-converter/pkg/converter/language" // Use internal path for testing
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewGoEnryDetector verifies correct initialization and normalization of overrides.
func TestNewGoEnryDetector(t *testing.T) { // minimal comment
	overrides := map[string]string{
		".foo":     "foobar",
		"BAR":      "Barlang", // Test case insensitivity + missing dot - Value also gets lowercased
		".GZ":      "Gzip",    // Test case insensitivity - Value also gets lowercased
		"nodot":    "special",
		".case":    "lowercase",
		".Case":    "UPPERCASE", // Should be overwritten by lowercase key, value normalized
		"":         "emptykey",  // Should be skipped
		".empty":   "",          // Should be skipped
		".dotonly": "dotlang",
	}
	detector := language.NewGoEnryDetector(0.65, overrides)
	require.NotNil(t, detector)

	// Verify overrides were normalized and stored (checked via Detect tests)
	lang, conf, err := detector.Detect([]byte("content"), "myfile.foo")
	require.NoError(t, err)
	assert.Equal(t, "foobar", lang) // Already lowercase
	assert.Equal(t, 1.0, conf)      // Overrides are 100% confident

	lang, conf, err = detector.Detect([]byte("content"), "file.BAR")
	require.NoError(t, err)
	assert.Equal(t, "barlang", lang) // Normalized value
	assert.Equal(t, 1.0, conf)

	lang, conf, err = detector.Detect([]byte("content"), "archive.tar.GZ")
	require.NoError(t, err)
	assert.Equal(t, "gzip", lang) // Normalized value
	assert.Equal(t, 1.0, conf)

	// Test case normalization override
	lang, conf, err = detector.Detect([]byte("content"), "somefile.Case")
	require.NoError(t, err)
	assert.Equal(t, "lowercase", lang) // .case definition should win, value already lowercase
	assert.Equal(t, 1.0, conf)

	// Test empty key/value overrides were skipped
	lang, _, err = detector.Detect([]byte(""), "") // Empty path shouldn't match empty key
	require.NoError(t, err)
	assert.NotEqual(t, "emptykey", lang)

	lang, _, err = detector.Detect([]byte("content"), "file.empty")
	require.NoError(t, err)
	// Expected fallback, not an empty string or the skipped override value
	assert.Contains(t, []string{"plaintext", "unknown"}, lang)

	// Test nil overrides map
	detectorNil := language.NewGoEnryDetector(0.5, nil)
	require.NotNil(t, detectorNil)
	lang, _, err = detectorNil.Detect([]byte("package main"), "main.go")
	require.NoError(t, err)
	assert.Equal(t, "go", lang) // Normalized standard detection
}

// TestGoEnryDetector_Detect verifies various detection scenarios.
func TestGoEnryDetector_Detect(t *testing.T) { // minimal comment
	// Detector without overrides, standard threshold
	detector := language.NewGoEnryDetector(0.75, nil)

	testCases := []struct {
		name           string
		filePath       string
		content        []byte
		expectedLang   string
		minConfidence  float64 // Minimum expected confidence (0.0 if based on ext/fallback)
		overrideLang   string  // Language to use if overrides applied (expected lowercase)
		detector       language.LanguageDetector
		setupOverrides map[string]string // Overrides for specific tests (values will be lowercased by New)
	}{
		{
			name:          "Go Content",
			filePath:      "main.go",
			content:       []byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello\")\n}\n"),
			expectedLang:  "go", // lowercase
			minConfidence: 0.7,
			detector:      detector,
		},
		{
			name:          "Python Content",
			filePath:      "script.py",
			content:       []byte("#!/usr/bin/env python\n\ndef hello():\n    print(\"Hello\")\n\nhello()\n"),
			expectedLang:  "python", // lowercase
			minConfidence: 0.7,
			detector:      detector,
		},
		{
			name:          "Simple Text",
			filePath:      "notes.txt",
			content:       []byte("This is just plain text."),
			expectedLang:  "plaintext", // Standardized fallback
			minConfidence: 0.0,
			detector:      detector,
		},
		{
			name:          "YAML File",
			filePath:      "config.yaml",
			content:       []byte("key: value\nlist:\n  - item1\n  - item2\n"),
			expectedLang:  "yaml", // lowercase
			minConfidence: 0.7,
			detector:      detector,
		},
		{
			name:          "JSON File",
			filePath:      "data.json",
			content:       []byte("{\"key\": \"value\", \"number\": 123, \"array\": [1, 2, 3]}"),
			expectedLang:  "json", // lowercase
			minConfidence: 0.7,
			detector:      detector,
		},
		{
			name:          "Unknown Extension, Text Content",
			filePath:      "file.unknown",
			content:       []byte("Some text content here."),
			expectedLang:  "plaintext", // Standardized fallback
			minConfidence: 0.0,
			detector:      detector,
		},
		{
			name:          "Empty Content",
			filePath:      "empty.txt",
			content:       []byte(""),
			expectedLang:  "unknown", // Handle empty content
			minConfidence: 0.0,
			detector:      detector,
		},
		{
			name:           "Override Applied",
			filePath:       "code.mycpp",
			content:        []byte("int main() { return 0; }"),
			expectedLang:   "c++", // Normalized override value
			minConfidence:  1.0,
			overrideLang:   "c++", // lowercase
			setupOverrides: map[string]string{".mycpp": "C++"},
		},
		{
			name:           "Override Ignores Content",
			filePath:       "script.py", // Normally Python
			content:        []byte("#!/usr/bin/env python\nprint('hi')"),
			expectedLang:   "ruby", // Normalized override forces Ruby
			minConfidence:  1.0,
			overrideLang:   "ruby", // lowercase
			setupOverrides: map[string]string{".py": "Ruby"},
		},
		{
			name:           "Override Case Insensitivity",
			filePath:       "README.MD", // Uppercase extension
			content:        []byte("# Project"),
			expectedLang:   "documentation", // Normalized override value
			minConfidence:  1.0,
			overrideLang:   "documentation",                           // lowercase
			setupOverrides: map[string]string{".md": "Documentation"}, // Lowercase key in map
		},
		// --- New Test Cases ---
		{
			name:          "Extension vs Content Conflict (py file with go code)",
			filePath:      "conflict.py",
			content:       []byte("package main\nfunc main() {}"),
			expectedLang:  "go", // Normalized result
			minConfidence: 0.7,
			detector:      detector,
		},
		{
			name:          "Extensionless Makefile",
			filePath:      "Makefile",
			content:       []byte("build:\n\tgo build .\n"),
			expectedLang:  "makefile", // Normalized result
			minConfidence: 0.5,
			detector:      detector,
		},
		{
			name:          "Extensionless Dockerfile",
			filePath:      "Dockerfile",
			content:       []byte("FROM golang:1.21\nWORKDIR /app\nCOPY . .\nRUN go build -o main .\nCMD [\"./main\"]"),
			expectedLang:  "dockerfile", // Normalized result
			minConfidence: 0.5,
			detector:      detector,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testDetector := tc.detector
			if tc.setupOverrides != nil {
				// Create a detector specific to this test case with overrides
				testDetector = language.NewGoEnryDetector(0.75, tc.setupOverrides)
			}
			require.NotNil(t, testDetector, "Detector should not be nil")

			lang, confidence, err := testDetector.Detect(tc.content, tc.filePath)

			require.NoError(t, err)
			// Allow for variations in fallback/unknown reporting
			if (tc.expectedLang == "Text" || tc.expectedLang == "plaintext") && (lang == "Text" || lang == "plaintext") {
				// Consider it a match (ensure lang is lowercased if expected is)
				if tc.expectedLang == "plaintext" {
					assert.Equal(t, "plaintext", lang)
				} else {
					// If expectedLang is "Text", accept "Text" or "plaintext"
					assert.Contains(t, []string{"Text", "plaintext"}, lang)
				}
			} else if (tc.expectedLang == "unknown") && (lang == "unknown" || lang == "") {
				// Consider it a match
			} else {
				assert.Equal(t, tc.expectedLang, lang, "Detected language mismatch")
			}
			// Check minimum confidence - allows for higher scores but ensures basic level
			assert.GreaterOrEqual(t, confidence, tc.minConfidence, "Confidence score should meet minimum expectation")

			// If an override was expected, ensure the language matches the override explicitly (already lowercase)
			if tc.overrideLang != "" {
				assert.Equal(t, tc.overrideLang, lang, "Language should match the override")
				assert.Equal(t, 1.0, confidence, "Confidence should be 1.0 for overrides")
			}
		})
	}
}

// --- END OF FINAL REVISED FILE pkg/converter/language/detector_test.go ---

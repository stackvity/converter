// --- START OF FINAL REVISED FILE pkg/converter/encoding/handler.go ---
package encoding

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/net/html/charset" // Required for encoding.Encoding interface
	"golang.org/x/text/transform"
)

const (
	// sniffLen is the number of bytes used by http.DetectContentType
	sniffLen = 512
	// checkLen is a buffer size used for null byte checks.
	checkLen = 1024
	// Null byte threshold percentage to consider a file binary.
	nullThreshold = 0.15 // 15%
)

// Map of common text-based MIME type prefixes for quick lookup in IsBinary.
var knownTextMIMEPrefixes = map[string]bool{
	"text/":                     true, // Includes text/plain, text/html, text/css, etc.
	"application/json":          true,
	"application/xml":           true, // Includes application/xhtml+xml
	"application/javascript":    true,
	"application/ecmascript":    true,
	"application/yaml":          true,
	"application/toml":          true,
	"application/csv":           true,
	"application/sql":           true,
	"application/rtf":           true,
	"application/ld+json":       true,
	"application/manifest+json": true,
	"application/schema+json":   true,
	"application/typescript":    true, // Added based on recommendation
	"application/markdown":      true, // Added based on recommendation
	"image/svg+xml":             true, // Added based on recommendation (SVG is text)
	// Add more common text-based application/* types as needed
}

// Map of common text-based MIME type suffixes for quick lookup in IsBinary.
var knownTextMIMESuffixes = map[string]bool{
	"+xml":  true,
	"+json": true,
	// Add more suffixes as needed
}

// EncodingHandler defines the interface for detecting character encoding,
// converting content to UTF-8, and detecting binary files.
// Godoc implemented here defining contract (BE-TASK-015).
//
// Stability: Public Stable API - Implementations can be provided externally.
// Adherence to the method contracts, especially error handling and return value semantics, is required.
type EncodingHandler interface {
	// DetectAndDecode attempts to detect the encoding of the input content
	// and convert it to UTF-8. It returns the UTF-8 bytes, the detected
	// encoding name (IANA name), a boolean indicating if detection was certain,
	// and any error encountered during conversion. Fallback encoding is used if
	// detection is uncertain and a valid default is configured.
	DetectAndDecode(content []byte) (utf8Content []byte, detectedEncoding string, certainty bool, err error)

	// IsBinary checks if the content is likely binary data based on MIME type sniffing
	// (http.DetectContentType on first 512 bytes) and null byte percentage
	// (in first 1024 bytes).
	IsBinary(content []byte) bool
}

// goCharsetEncodingHandler implements EncodingHandler using Go's standard library
// and golang.org/x/net/html/charset.
// Godoc implemented here defining contract (BE-TASK-015).
// This is the default implementation used if none is injected via Options.
type goCharsetEncodingHandler struct {
	defaultEncoding string
}

// NewGoCharsetEncodingHandler creates a new encoding handler.
func NewGoCharsetEncodingHandler(defaultEncoding string) EncodingHandler { // minimal comment
	return &goCharsetEncodingHandler{
		defaultEncoding: defaultEncoding,
	}
}

// DetectAndDecode implements the EncodingHandler interface.
func (h *goCharsetEncodingHandler) DetectAndDecode(content []byte) ([]byte, string, bool, error) { // minimal comment
	// DetermineEncoding returns the detected encoding, its canonical name, and certainty.
	detectedEncodingImpl, name, certain := charset.DetermineEncoding(content, "")

	if !certain && h.defaultEncoding != "" {
		// charset.Lookup returns the encoding and its canonical name, or nil if not found.
		// It does NOT return an error directly.
		encodingLookup, lookupName := charset.Lookup(h.defaultEncoding)
		if encodingLookup == nil {
			// Default encoding specified was invalid, fall back to initial guess
			// **Caller should log a warning about the invalid defaultEncoding.**
			// Re-determination is redundant, just use the initial values.
			// detectedEncodingImpl, name, certain = charset.DetermineEncoding(content, "")
		} else {
			// Valid default encoding found, use it.
			detectedEncodingImpl = encodingLookup
			name = lookupName // Use the canonical name returned by Lookup
			certain = true    // Treat specified default as certain
		}
	}
	// If still uncertain here (no default, or default was invalid), proceed with the initial guess.
	// **Caller might want to log a warning if 'certain' is false.**

	// If detection completely failed (no guess, no default), assume UTF-8.
	if detectedEncodingImpl == nil {
		// If name is also empty (which DetermineEncoding might do for UTF-8/ASCII), set it explicitly.
		if name == "" {
			name = "utf-8"
		}
		return content, name, certain, nil // Return original content and certainty (likely false)
	}

	// Attempt conversion if needed
	decoder := detectedEncodingImpl.NewDecoder()
	transformer := transform.NewReader(bytes.NewReader(content), decoder)

	utf8Content, err := io.ReadAll(transformer)
	if err != nil {
		// Conversion error occurred
		finalName := name
		// If name is empty (could happen if original detection was uncertain and no valid default used),
		// try to find a reasonable name, or default to "unknown".
		if finalName == "" {
			// charset.IANAEncodingName is not public. We already have 'name' from DetermineEncoding or Lookup.
			// If 'name' is still empty, we can fallback.
			finalName = "unknown"
		}
		// Wrap error for better context
		return utf8Content, finalName, certain, fmt.Errorf("failed to convert from '%s': %w", finalName, err)
	}

	// If name is empty (could happen if original detection was uncertain and no valid default used),
	// and conversion succeeded, set name to "unknown".
	// The 'name' returned by DetermineEncoding or Lookup should be the best identifier we have.
	if name == "" {
		name = "unknown" // Fallback if canonical name couldn't be determined by charset funcs
	}
	// If detection was certain or default used, 'name' should already be set correctly.

	return utf8Content, name, certain, nil
}

// isMIMETextBased checks if a detected MIME type is likely text-based.
func isMIMETextBased(contentType string) bool { // minimal comment
	// Normalize the content type (remove parameters like ;charset=...)
	contentType = strings.SplitN(contentType, ";", 2)[0]
	contentType = strings.TrimSpace(contentType)

	// Handle common case explicitly
	if strings.HasPrefix(contentType, "text/") {
		return true
	}
	// Check against known application types that are text-based
	if _, ok := knownTextMIMEPrefixes[contentType]; ok {
		return true
	}
	// Check for common text-based suffixes like +xml or +json
	for suffix := range knownTextMIMESuffixes {
		if strings.HasSuffix(contentType, suffix) {
			return true
		}
	}
	// Treat octet-stream as potentially text, relying on null check
	if contentType == "application/octet-stream" {
		return true
	}

	return false
}

// IsBinary implements the EncodingHandler interface.
func (h *goCharsetEncodingHandler) IsBinary(content []byte) bool { // minimal comment
	// Prevent issues with empty content
	if len(content) == 0 {
		return false
	}

	// 1. Check MIME type using the beginning of the content
	checkLimitSniff := len(content)
	if checkLimitSniff > sniffLen {
		checkLimitSniff = sniffLen
	}
	contentType := http.DetectContentType(content[:checkLimitSniff])

	if !isMIMETextBased(contentType) {
		// If it's not explicitly recognized as likely text-based, consider it binary.
		return true
	}

	// 2. Check for excessive null bytes if MIME type was inconclusive or text-like
	checkLimitNull := len(content)
	if checkLimitNull > checkLen {
		checkLimitNull = checkLen
	}
	// Avoid division by zero
	if checkLimitNull == 0 {
		return false
	}
	nullCount := bytes.Count(content[:checkLimitNull], []byte{0x00})

	if float64(nullCount)/float64(checkLimitNull) > nullThreshold {
		return true // Null byte threshold overrides MIME detection if it suggested text/octet-stream
	}

	return false
}

// --- END OF FINAL REVISED FILE pkg/converter/encoding/handler.go ---

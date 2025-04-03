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
	detectedEncodingImpl, name, certain := charset.DetermineEncoding(content, "")

	// Apply fallback if detection was uncertain and a default is provided
	if !certain && h.defaultEncoding != "" {
		encodingLookup, lookupName := charset.Lookup(h.defaultEncoding)
		if encodingLookup != nil {
			// Valid default encoding found, use it
			detectedEncodingImpl = encodingLookup
			name = lookupName // Use canonical name from lookup
			certain = true    // Treat specified default as certain
		}
		// If lookup fails, proceed with the initial uncertain guess `detectedEncodingImpl`
	}

	// If no encoding was detected or assumed (even after fallback check), assume UTF-8
	if detectedEncodingImpl == nil {
		if name == "" {
			name = "utf-8"
		}
		// Return original content, maybe check if it's valid UTF-8? For now, assume valid.
		// Certainty remains the original value unless default was applied.
		return content, name, certain, nil
	}

	// Attempt conversion only if an encoding implementation was determined
	decoder := detectedEncodingImpl.NewDecoder()
	transformer := transform.NewReader(bytes.NewReader(content), decoder)

	utf8Content, err := io.ReadAll(transformer)
	if err != nil {
		finalName := name
		if finalName == "" {
			// Try to get a name from the encoding itself if possible, else unknown
			finalName = "unknown" // Fallback name
		}
		// On conversion error, return original content and the error
		return content, finalName, certain, fmt.Errorf("failed to convert from '%s': %w", finalName, err)
	}

	// Ensure a name is returned even if conversion succeeded but name was empty
	if name == "" {
		name = "unknown" // Fallback name if none determined
	}

	return utf8Content, name, certain, nil
}

// isMIMETextBased checks if a detected MIME type is likely text-based.
func isMIMETextBased(contentType string) bool { // minimal comment
	mimeType := strings.SplitN(contentType, ";", 2)[0]
	mimeType = strings.TrimSpace(mimeType)

	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	if _, ok := knownTextMIMEPrefixes[mimeType]; ok {
		return true
	}
	for suffix := range knownTextMIMESuffixes {
		if strings.HasSuffix(mimeType, suffix) {
			return true
		}
	}
	// Allow octet-stream to potentially be text, rely on null check
	if mimeType == "application/octet-stream" {
		return true
	}
	return false
}

// IsBinary implements the EncodingHandler interface.
func (h *goCharsetEncodingHandler) IsBinary(content []byte) bool { // minimal comment
	contentLen := len(content)
	if contentLen == 0 {
		return false
	}

	// 1. MIME type check
	checkLimitSniff := contentLen
	if checkLimitSniff > sniffLen {
		checkLimitSniff = sniffLen
	}
	contentType := http.DetectContentType(content[:checkLimitSniff])
	if !isMIMETextBased(contentType) {
		return true // Definitely not text-like based on MIME
	}

	// 2. Null byte check (only if MIME check passed or was inconclusive)
	checkLimitNull := contentLen
	if checkLimitNull > checkLen {
		checkLimitNull = checkLen
	}
	// CheckLimitNull should always be > 0 if contentLen > 0
	if checkLimitNull == 0 {
		return false // Should not happen, defensive
	}
	nullCount := bytes.Count(content[:checkLimitNull], []byte{0x00})

	return float64(nullCount)/float64(checkLimitNull) > nullThreshold
}

// --- END OF FINAL REVISED FILE pkg/converter/encoding/handler.go ---

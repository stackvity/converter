// --- START OF FINAL REVISED FILE pkg/converter/encoding/handler_test.go ---
package encoding_test

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stackvity/stack-converter/pkg/converter/encoding" // Use correct package path
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform" // Required for transform.Transformer interface
)

// --- Test Constants (mirroring internal values from handler.go for testing) ---
const (
	// testSniffLen mirrors the internal sniffLen constant (512 bytes).
	testSniffLen = 512
	// testCheckLen mirrors the internal checkLen constant (1024 bytes).
	testCheckLen = 1024
	// testNullThreshold mirrors the internal nullThreshold constant (0.15).
	testNullThreshold = 0.15
)

// Helper function to encode string to specified encoding bytes
func encodeBytes(t *testing.T, text string, enc transform.Transformer) []byte {
	t.Helper()
	encodedBytes, _, err := transform.Bytes(enc, []byte(text))
	require.NoError(t, err)
	return encodedBytes
}

// TestSC_LIB_PROC_ENC_001: Verify_LIB_Encoding_DetectUTF8_NoConversion
func TestDetectAndDecode_UTF8(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	input := []byte("Hello, UTF-8 world!")
	utf8Content, detectedEncoding, certainty, err := handler.DetectAndDecode(input)

	require.NoError(t, err)
	assert.Contains(t, []string{"utf-8", ""}, detectedEncoding, "Should detect UTF-8 or be empty for plain ASCII")
	assert.True(t, certainty, "Should be certain about UTF-8/ASCII")
	assert.Equal(t, input, utf8Content, "Content should remain unchanged")
}

// TestSC_LIB_PROC_ENC_002: Verify_LIB_Encoding_DetectConvertUTF16LE_Success (with BOM)
func TestDetectAndDecode_UTF16LE_WithBOM(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	originalText := "Hello, UTF-16LE!"
	bom := []byte{0xFF, 0xFE}
	utf16leEncoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder()
	encodedText := encodeBytes(t, originalText, utf16leEncoder)
	input := append(bom, encodedText...)

	utf8Content, detectedEncoding, certainty, err := handler.DetectAndDecode(input)

	require.NoError(t, err)
	assert.Contains(t, detectedEncoding, "utf-16le", "Should detect UTF-16 LE")
	assert.True(t, certainty, "Should be certain due to BOM")
	assert.Equal(t, originalText, string(utf8Content), "Content should be correctly converted")
}

// TestSC_LIB_PROC_ENC_002: Verify_LIB_Encoding_DetectConvertUTF16BE_Success (with BOM)
func TestDetectAndDecode_UTF16BE_WithBOM(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	originalText := "Hello, UTF-16BE!"
	bom := []byte{0xFE, 0xFF}
	utf16beEncoder := unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM).NewEncoder()
	encodedText := encodeBytes(t, originalText, utf16beEncoder)
	input := append(bom, encodedText...)

	utf8Content, detectedEncoding, certainty, err := handler.DetectAndDecode(input)

	require.NoError(t, err)
	assert.Contains(t, detectedEncoding, "utf-16be", "Should detect UTF-16 BE")
	assert.True(t, certainty, "Should be certain due to BOM")
	assert.Equal(t, originalText, string(utf8Content), "Content should be correctly converted")
}

// TestSC_LIB_PROC_ENC_003: Verify_LIB_Encoding_DetectConvertLatin1_Success
func TestDetectAndDecode_Latin1(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	originalText := "Héllo, Lätin-1!"
	latin1Encoder := charmap.ISO8859_1.NewEncoder()
	input := encodeBytes(t, originalText, latin1Encoder)

	utf8Content, detectedEncoding, certainty, err := handler.DetectAndDecode(input)

	require.NoError(t, err)
	possibleDetections := []string{"iso-8859-1", "windows-1252"}
	assert.Contains(t, possibleDetections, detectedEncoding, "Should detect ISO-8859-1 or compatible")
	assert.False(t, certainty, "Should likely be uncertain without BOM")
	assert.Equal(t, originalText, string(utf8Content), "Content should be correctly converted")
}

// TestSC_LIB_PROC_ENC_004: Verify_LIB_Encoding_UnknownEncoding_FallbackUsed
func TestDetectAndDecode_Fallback(t *testing.T) {
	originalText := "Héllo again"
	latin1Encoder := charmap.ISO8859_1.NewEncoder()
	input := encodeBytes(t, originalText, latin1Encoder)

	// Test *with* fallback
	handlerWithFallback := encoding.NewGoCharsetEncodingHandler("ISO-8859-1")
	utf8Content, detectedEncoding, certainty, err := handlerWithFallback.DetectAndDecode(input)

	require.NoError(t, err)
	assert.Equal(t, "iso-8859-1", detectedEncoding, "Should use fallback ISO-8859-1")
	assert.True(t, certainty, "Should be treated as certain when fallback is applied")
	assert.Equal(t, originalText, string(utf8Content), "Content should be correctly converted using fallback")

	// Test *without* fallback
	handlerWithoutFallback := encoding.NewGoCharsetEncodingHandler("")
	utf8ContentNoFallback, detectedEncodingNoFallback, certaintyNoFallback, errNoFallback := handlerWithoutFallback.DetectAndDecode(input)

	require.NoError(t, errNoFallback)
	assert.False(t, certaintyNoFallback, "Should be uncertain without fallback")
	t.Logf("Detected without fallback: %s", detectedEncodingNoFallback)
	assert.True(t, string(utf8ContentNoFallback) == originalText || bytes.Equal(utf8ContentNoFallback, input), "Should produce valid UTF-8 output without fallback")
}

// TestSC_LIB_PROC_ENC_006 (New): Verify handling of invalid default encoding setting
func TestDetectAndDecode_InvalidFallback(t *testing.T) {
	originalText := "Simple text"
	input := []byte(originalText)

	invalidDefault := "invalid-encoding-name"
	handlerWithInvalidFallback := encoding.NewGoCharsetEncodingHandler(invalidDefault)
	utf8Content, detectedEncoding, certainty, err := handlerWithInvalidFallback.DetectAndDecode(input)

	require.NoError(t, err)
	assert.NotEqual(t, invalidDefault, detectedEncoding, "Detected encoding should not be the invalid fallback name")
	assert.Contains(t, []string{"", "utf-8"}, detectedEncoding, "Should fallback to empty or utf-8 if default is invalid")
	assert.True(t, certainty, "Should still be certain about ASCII/UTF-8")
	assert.Equal(t, originalText, string(utf8Content), "Content should be unchanged ASCII")
}

// TestSC_LIB_PROC_ENC_005: Verify_LIB_Encoding_InvalidSequence_ErrorOrReplacement
func TestDetectAndDecode_InvalidUTF8(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	input := []byte("Valid start \xFF Invalid sequence \xFE end.")

	utf8Content, detectedEncoding, certainty, err := handler.DetectAndDecode(input)

	require.NoError(t, err, "Decoder should replace invalid sequences, not return conversion error")
	t.Logf("Detected encoding for invalid UTF-8: %s, Certainty: %v", detectedEncoding, certainty)

	replacementChar := string([]byte{0xEF, 0xBF, 0xBD})
	expected := "Valid start " + replacementChar + " Invalid sequence " + replacementChar + " end."
	assert.Equal(t, expected, string(utf8Content), "Invalid sequences should be replaced")
}

// --- IsBinary Tests ---

// TestSC_LIB_PROC_BIN-001 & -003: Verify_LIB_Binary_DetectPNG_IsBinary & HandleModeSkip
func TestIsBinary_PNG(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	input := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}
	isBinary := handler.IsBinary(input)
	assert.True(t, isBinary, "PNG signature should be detected as binary")
}

// TestSC_LIB_PROC_BIN-002 & -003: Verify_LIB_Binary_DetectNullBytes_IsBinary
func TestIsBinary_HighNulls(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	var buf bytes.Buffer
	// Use test constant testSniffLen
	buf.WriteString(strings.Repeat("a", testSniffLen))
	// Use test constant testCheckLen
	remaining := testCheckLen - buf.Len()
	for i := 0; i < remaining; i++ {
		if i%4 == 0 { // ~25% nulls in second part
			buf.WriteByte(0x00)
		} else {
			buf.WriteByte(byte('b' + (i % 26)))
		}
	}
	input := buf.Bytes()

	isBinary := handler.IsBinary(input)
	assert.True(t, isBinary, "High percentage of null bytes should be detected as binary")
}

// Test IsBinary: Text files should not be detected as binary
func TestIsBinary_TextFile(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	testCases := [][]byte{
		[]byte("This is a plain text file."),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"Hello\")\n}\n"),
		[]byte("{\n  \"key\": \"value\",\n  \"number\": 123\n}"),                    // JSON
		[]byte("<xml><tag>Value</tag></xml>"),                                       // XML
		[]byte("key = \"value\" # TOML"),                                            // TOML
		[]byte("field1,field2\nvalue1,value2"),                                      // CSV
		[]byte("SELECT * FROM users;"),                                              // SQL
		[]byte("function hello() { console.log('hi'); }"),                           // JavaScript
		[]byte("body { color: #333; }"),                                             // CSS
		[]byte(`{"type": "Feature", "properties": {}, "geometry": null}`),           // GeoJSON (application/json)
		[]byte(`<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`), // RSS (application/rss+xml)
		[]byte(`<!DOCTYPE html><html><body></body></html>`),                         // HTML (text/html)
	}

	for _, input := range testCases {
		// Manually determine expected MIME type for logging/debugging
		expectedMIME := http.DetectContentType(input)
		t.Logf("Testing content (starts '%s...'), detected MIME: %s", string(input[:min(len(input), 10)]), expectedMIME)

		isBinary := handler.IsBinary(input)
		assert.False(t, isBinary, "Text content should not be detected as binary: %q", string(input))
	}
}

// Test IsBinary: Vendor specific MIME types (+xml, +json) should be treated as text
func TestIsBinary_VendorMimeTypes(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	testCases := []struct {
		name    string
		content []byte // Content crafted to trigger specific MIME, minimal nulls
	}{
		{
			name: "Vendor XML",
			// Needs enough content to resemble XML for http.DetectContentType to potentially work
			content: []byte(`<?xml version="1.0"?><custom:resource xmlns:custom="http://example.com/ns"></custom:resource>`),
		},
		{
			name:    "Vendor JSON",
			content: []byte(`{"@context": "http://example.com/context.jsonld", "type": "CustomType"}`),
		},
		{
			name:    "Schema JSON",
			content: []byte(`{"$schema": "http://json-schema.org/draft-07/schema#", "title": "Test"}`),
		},
		// Add cases to test the knownTextMIMESuffixes and knownTextMIMEPrefixes logic in handler.go
		{
			name:    "Text Plain",
			content: []byte("This is plain text, should detect text/plain"),
		},
		{
			name:    "XML Suffix Example",
			content: []byte(`<root><item>value</item></root>`), // Should detect application/xml or text/xml
		},
		{
			name:    "JSON Suffix Example",
			content: []byte(`{"activity": "test"}`), // Should detect application/json
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Test with actual content detection
			isBinaryActual := handler.IsBinary(tc.content)
			// Use test constant testSniffLen
			detectedMIME := http.DetectContentType(tc.content[:min(len(tc.content), testSniffLen)])
			assert.False(t, isBinaryActual, "Content for '%s' (detected: %s) should not be binary", tc.name, detectedMIME)

			// Removed the monkey-patching logic as it's unreliable and tests internal implementation details.
			// The logic above (checking handler.IsBinary directly) sufficiently tests the outcome
			// which relies on both http.DetectContentType AND the null byte check.
		})
	}
}

// Test IsBinary: File with some nulls below threshold
func TestIsBinary_LowNulls(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	var buf bytes.Buffer
	// Use test constant testSniffLen
	buf.WriteString(strings.Repeat("a", testSniffLen))
	// Use test constant testCheckLen
	remaining := testCheckLen - buf.Len()
	for i := 0; i < remaining; i++ {
		if i%10 == 0 { // 10% null bytes in second part
			buf.WriteByte(0x00)
		} else {
			buf.WriteByte(byte('b' + (i % 26)))
		}
	}
	input := buf.Bytes()

	isBinary := handler.IsBinary(input)
	assert.False(t, isBinary, "Low percentage of null bytes should not be detected as binary")
}

// Test IsBinary: Empty file
func TestIsBinary_Empty(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	input := []byte{}
	isBinary := handler.IsBinary(input)
	assert.False(t, isBinary, "Empty content should not be detected as binary")
}

// Test IsBinary: Short file
func TestIsBinary_Short(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	input := []byte("short")
	isBinary := handler.IsBinary(input)
	assert.False(t, isBinary, "Short text content should not be detected as binary")
}

// Test IsBinary: Files that might be misidentified by MIME but pass null check
func TestIsBinary_TrickyText(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	// Starts with bytes that could be misinterpreted, but overall text content and low null count.
	// http.DetectContentType might return application/octet-stream.
	input := append([]byte{0x80, 0x81, 0x82, 0x83}, []byte(strings.Repeat("this is clearly text ", 50))...)

	isBinary := handler.IsBinary(input)
	assert.False(t, isBinary, "Text file with unusual start bytes should not be binary based on null check")
}

// Test IsBinary: Edge case where MIME indicates text but nulls exceed threshold
func TestIsBinary_TextMimeHighNulls(t *testing.T) {
	handler := encoding.NewGoCharsetEncodingHandler("")
	// Craft content that starts like text (forcing http.DetectContentType -> text/plain)
	// but contains >15% null bytes overall within the checkLen.
	var buf bytes.Buffer
	buf.WriteString("This looks like text initially.... ")
	// Ensure length is at least testSniffLen
	// Use test constant testSniffLen
	for buf.Len() < testSniffLen {
		buf.WriteString("padding ")
	}
	// Fill remaining up to testCheckLen with mostly nulls
	// Use test constant testCheckLen
	remaining := testCheckLen - buf.Len()
	// Use test constant testNullThreshold
	// FIX: Perform float calculation separately, then cast to int
	targetNullFloat := float64(testCheckLen) * testNullThreshold * 1.5 // Target >15% overall
	nullToAdd := int(targetNullFloat)                                  // Convert the result to int
	textToAdd := remaining - nullToAdd
	if textToAdd < 0 {
		textToAdd = 0
		nullToAdd = remaining
	}

	for i := 0; i < nullToAdd; i++ {
		buf.WriteByte(0x00)
	}
	for i := 0; i < textToAdd; i++ {
		buf.WriteByte('x')
	}
	// Use test constant testCheckLen
	input := buf.Bytes()[:testCheckLen] // Ensure we only check up to testCheckLen

	isBinary := handler.IsBinary(input)
	assert.True(t, isBinary, "File with text MIME but high nulls should be detected as binary")
}

// Helper for TestIsBinary_TextFile
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- END OF FINAL REVISED FILE pkg/converter/encoding/handler_test.go ---

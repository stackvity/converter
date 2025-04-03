// --- START OF FINAL REVISED FILE internal/cli/ui/view_test.go ---
package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list" // Required import for list.Item
	"github.com/charmbracelet/lipgloss"
	"github.com/stackvity/stack-converter/pkg/converter" // For Status enum
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newViewModel creates a model instance for view testing.
// Assumes HeaderStyle, FooterStyle, and StatusStyleFailed are defined and exported elsewhere in the ui package.
func newViewModel(width, height int, phase string, items []listItem, summary Summary, fatalErr string, quitting bool) *Model { // minimal comment
	m := NewModel()
	m.width = width
	m.height = height
	m.phaseMessage = phase
	m.fatalError = fatalErr
	m.quitting = quitting
	m.initialized = true
	m.summary = summary
	if m.summary.StartTime.IsZero() {
		m.summary.StartTime = time.Now().Add(-10 * time.Second)
	}

	// Prepare list items for the list model and internal tracking
	listItems := make([]list.Item, len(items)) // Use the correct list.Item interface type
	for i, item := range items {
		listItems[i] = item // ui.listItem implements list.Item
		m.itemMap[item.path] = i
	}
	m.fileItems = items

	// Calculate available list height accurately using lipgloss heights
	errorViewHeight := 0
	if fatalErr != "" {
		// Use the globally defined StatusStyleFailed (ensure it's exported) as used in model.go's View()
		// Use a copy to avoid modifying the original style if Width changes internal state
		// FIX: Use StatusStyleFailed instead of the undefined FatalErrorStyle
		tempStyle := StatusStyleFailed.Copy().Width(width)
		errorViewHeight = lipgloss.Height(tempStyle.Render("FATAL ERROR: "+fatalErr) + "\n")
	}
	// Use the globally defined HeaderStyle and FooterStyle (ensure they are exported)
	headerHeight := lipgloss.Height(HeaderStyle.Render(" "))
	footerHeight := lipgloss.Height(FooterStyle.Render(" "))
	listHeight := height - headerHeight - footerHeight - errorViewHeight
	if listHeight < 1 {
		listHeight = 1
	}
	m.list.SetSize(width, listHeight) // Set the calculated size accurately
	m.list.SetItems(listItems)        // Update items in the bubbletea list component

	return &m
}

// TestView_Initializing verifies the view output before initialization.
func TestView_Initializing(t *testing.T) { // minimal comment
	m := NewModel()
	view := m.View()
	assert.Equal(t, "Initializing...", view)
}

// TestView_Quitting verifies the view output when quitting.
func TestView_Quitting(t *testing.T) { // minimal comment
	m := newViewModel(80, 25, "Complete", nil, Summary{}, "", true) // quitting = true
	view := m.View()
	assert.Equal(t, "Exiting...\n", view)
}

// TestView_BasicLayout verifies the structure and content of the view in a typical state.
func TestView_BasicLayout(t *testing.T) { // minimal comment
	items := []listItem{
		{path: "file1.go", status: converter.StatusSuccess, duration: 50 * time.Millisecond},
		{path: "subdir/file2.txt", status: converter.StatusProcessing},
	}
	summary := Summary{
		TotalFilesScanned: 3, ProcessedCount: 1, CachedCount: 0, SkippedCount: 0, ErrorCount: 0,
		StartTime: time.Now().Add(-15 * time.Second),
	}
	m := newViewModel(80, 10, "Processing...", items, summary, "", false)
	view := m.View()

	// Check key elements
	assert.Contains(t, view, "Stack Converter vdev")
	assert.Contains(t, view, "Processing...")
	assert.Contains(t, view, m.spinner.View())
	assert.Contains(t, view, "file1.go")
	assert.Contains(t, view, "subdir/file2.txt")
	// Check correct summary key. Assuming TestView_Counts confirms the format.
	assert.Contains(t, view, "Total Scanned: 3") // Adjusted to match format in model.go
	assert.Contains(t, view, "Processed: 1")     // Adjusted to match format in model.go
	assert.Contains(t, view, "(Cached: 0)")      // Adjusted to match format in model.go
	assert.Contains(t, view, "Skipped: 0")       // Adjusted to match format in model.go
	assert.Contains(t, view, "Failed: 0")        // Adjusted to match format in model.go
	assert.Contains(t, view, "Elapsed:")
	assert.Contains(t, view, "q: quit")

	// Check status indicators
	assert.Contains(t, view, "[✓]")
	assert.Contains(t, view, "[…]")
	assert.Contains(t, view, "50ms")

	// Check layout structure
	lines := strings.Split(strings.TrimSpace(view), "\n")
	require.GreaterOrEqual(t, len(lines), 3)
	assert.Contains(t, lines[0], "Stack Converter")
	assert.Contains(t, lines[len(lines)-1], "Processed:") // Check for a key part of the summary line
}

// TestView_FatalError verifies the rendering when a fatal error occurs.
func TestView_FatalError(t *testing.T) { // minimal comment
	errMsg := "Critical failure during cache load"
	summary := Summary{ErrorCount: 1, StartTime: time.Now().Add(-5 * time.Second)}
	m := newViewModel(80, 10, "Complete", nil, summary, errMsg, false)
	view := m.View()

	// Use StatusStyleFailed for rendering comparison as done in model.go
	assert.Contains(t, view, StatusStyleFailed.Render(errMsg)) // Check the styled error message
	assert.Contains(t, view, "Complete")
	assert.NotContains(t, view, m.spinner.View())
	assert.Contains(t, view, "Processed:") // Check footer is present
	assert.Contains(t, view, "q: quit")

	// Check placement - ErrorView is rendered *before* the footer in model.go
	lines := strings.Split(strings.TrimSpace(view), "\n")
	require.GreaterOrEqual(t, len(lines), 3)
	assert.Contains(t, lines[0], "Stack Converter")       // Header
	assert.Contains(t, lines[len(lines)-2], errMsg)       // Error message line (before footer)
	assert.Contains(t, lines[len(lines)-1], "Processed:") // Footer
}

// TestView_WidthAndHeight verifies rendering respects dimensions.
func TestView_WidthAndHeight(t *testing.T) { // minimal comment
	width, height := 40, 20
	items := make([]listItem, 15)
	for i := range items {
		items[i] = listItem{path: fmt.Sprintf("file_%02d.txt", i), status: converter.StatusPending}
	}
	m := newViewModel(width, height, "Scanning...", items, Summary{TotalFilesScanned: 15}, "", false)
	view := m.View()

	lines := strings.Split(view, "\n")
	require.Greater(t, len(lines), 0)

	// Check width implicitly using presence of styled elements
	assert.Contains(t, view, HeaderStyle.Render(" "), "Header style expected")
	assert.Contains(t, view, FooterStyle.Render(" "), "Footer style expected")

	// Check list height
	listItemLines := 0
	listLinePrefix := " " // Assuming default delegate padding/prefix
	for _, line := range lines {
		// A simple check, might need refinement based on actual list item rendering format
		if strings.HasPrefix(line, listLinePrefix) && strings.Contains(line, ".txt") {
			listItemLines++
		}
	}
	headerHeight := lipgloss.Height(HeaderStyle.Render(" "))
	footerHeight := lipgloss.Height(FooterStyle.Render(" "))
	errorViewHeight := 0 // No error view in this test
	expectedListHeight := height - headerHeight - footerHeight - errorViewHeight
	if expectedListHeight < 1 {
		expectedListHeight = 1
	}
	require.Equal(t, expectedListHeight, m.list.Height(), "Internal list height mismatch")
	// Use LessOrEqual as the number of rendered lines depends on item height and might be less than calculated list height
	assert.LessOrEqual(t, listItemLines, m.list.Height(), "Rendered list items exceed calculated list height")
	assert.Greater(t, listItemLines, 0, "Expected some list items to be rendered")
}

// TestView_LongFilePathsTruncation verifies truncation of long file paths.
func TestView_LongFilePathsTruncation(t *testing.T) { // minimal comment
	width := 60
	longPath := "a/very/long/directory/structure/containing/a/similarly/long/filename_that_exceeds_normal_width.go"
	items := []listItem{
		{path: longPath, status: converter.StatusPending},
	}
	m := newViewModel(width, 10, "Scanning...", items, Summary{TotalFilesScanned: 1}, "", false)
	view := m.View()

	found := false
	renderedPathLine := ""
	listLinePrefix := " " // Assuming default delegate padding/prefix
	for _, line := range strings.Split(view, "\n") {
		// Check for the line rendering the list item, potentially truncated
		if strings.HasPrefix(line, listLinePrefix) && strings.Contains(line, "filename_that_exceeds") {
			found = true
			renderedPathLine = line
			break
		}
	}
	require.True(t, found, "Line containing the long path name not found")

	// Estimate available width for path display (This is tricky and depends on delegate rendering)
	// Let's check if the full path is present; if the path is longer than width, it shouldn't be fully present.
	// A more robust test might involve mocking lipgloss width calculation or analyzing rendered line length.
	availableWidth := width - lipgloss.Width(" [] ") - 4 // Estimate status + padding
	t.Logf("Testing long path: TotalWidth=%d, EstimatedAvailable=%d, PathLen=%d", width, availableWidth, len(longPath))

	if len(longPath) > availableWidth {
		// If the path is definitely longer than available space, it should be truncated.
		assert.NotContains(t, renderedPathLine, longPath, "Full long path rendered despite likely exceeding available width")
		assert.True(t, strings.Contains(renderedPathLine, "..."), "Truncated path should contain ellipsis (...)")
	} else {
		// If the path might fit, ensure the full path is there.
		t.Logf("Skipping truncation content check as path might fit within available width")
		assert.Contains(t, renderedPathLine, longPath, "Full path should be present if it fits estimated available width")
	}
}

// TestView_EmptyList verifies rendering with no files.
func TestView_EmptyList(t *testing.T) { // minimal comment
	summary := Summary{StartTime: time.Now().Add(-2 * time.Second)}
	m := newViewModel(80, 10, "Scanning...", []listItem{}, summary, "", false) // Empty item list
	view := m.View()

	// Check header and footer
	assert.Contains(t, view, "Stack Converter vdev")
	assert.Contains(t, view, "Scanning...")
	assert.Contains(t, view, "Total Scanned: 0") // Check summary count format
	assert.Contains(t, view, "q: quit")

	// Check no file paths rendered in list area
	foundPath := false
	lines := strings.Split(view, "\n")
	if len(lines) > 2 { // Check lines between header and footer
		for _, line := range lines[1 : len(lines)-1] {
			// Be more specific: check for lines likely representing list items
			// (e.g., starting with padding, containing typical path characters)
			// Exclude empty lines or lines that are just whitespace.
			trimmedLine := strings.TrimSpace(line)
			if len(trimmedLine) > 0 && !strings.Contains(line, "Scanning...") && !strings.Contains(line, "Processed:") {
				// If a non-empty line exists here that isn't header/footer/status, assume it's an unexpected path.
				foundPath = true
				t.Logf("Unexpected line found in empty list view: %q", line)
				break
			}
		}
	}
	assert.False(t, foundPath, "No file paths should be rendered when the list is empty")
	// The list view might render its own "empty" message, check for its presence
	// This depends on bubbletea/list behavior, might need adjustment.
	// For now, ensure the basic structure is there.
	assert.Contains(t, view, m.list.View(), "List view rendering missing")
}

// TestView_Counts verifies correct rendering of summary counts.
func TestView_Counts(t *testing.T) { // minimal comment
	summary := Summary{
		TotalFilesScanned: 105, ProcessedCount: 82, CachedCount: 51, SkippedCount: 15, ErrorCount: 8,
		StartTime: time.Now().Add(-30 * time.Second),
	}
	m := newViewModel(100, 10, "Complete", nil, summary, "", false) // Nil items, focus on footer
	view := m.View()

	// Check specific counts in the footer, matching the format in model.go's View()
	assert.Contains(t, view, "Processed: 82 (Cached: 51)")
	assert.Contains(t, view, "Skipped: 15")
	assert.Contains(t, view, "Failed: 8")
	assert.Contains(t, view, "Total Scanned: 105")
	assert.Contains(t, view, "Elapsed:") // Check presence of time
}

// --- END OF FINAL REVISED FILE internal/cli/ui/view_test.go ---

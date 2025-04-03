package ui

import (
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner" // Import spinner package
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stackvity/stack-converter/internal/cli/hooks" // Import hooks for message types
	"github.com/stackvity/stack-converter/pkg/converter"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper to create a model with specific dimensions for testing Update
// Returns a pointer to the model now, as methods operate on pointers.
func newTestModel(width, height int) *Model {
	m := NewModel() // NewModel still returns the value type
	m.width = width
	m.height = height
	listHeight := height - listHeightMargin
	if listHeight < 1 {
		listHeight = 1
	}
	m.list.SetSize(width, listHeight)
	m.initialized = true
	return &m // Return a pointer
}

func TestModel_Init(t *testing.T) {
	m := newTestModel(80, 25) // Get pointer
	cmd := m.Init()
	require.NotNil(t, cmd)
	// Check if it triggers a spinner tick
	msg := cmd()
	_, ok := msg.(spinner.TickMsg)
	assert.True(t, ok, "Init should return a command that produces spinner.TickMsg")
}

func TestModel_Update_Quit(t *testing.T) {
	_ = newTestModel(80, 25) // Discard the result if not needed

	testCases := []string{"q", "ctrl+c"}

	for _, key := range testCases {
		t.Run(key, func(t *testing.T) {
			// Use a pointer for the initial model
			testModel := newTestModel(80, 25)
			newModel, cmd := testModel.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
			require.NotNil(t, newModel)
			require.NotNil(t, cmd)

			// Assert model state changed
			// FIX: Assert to *Model
			updatedM, ok := newModel.(*Model)
			require.True(t, ok)
			assert.True(t, updatedM.quitting)

			// Assert returned command is tea.Quit
			msg := cmd()
			assert.Equal(t, tea.Quit(), msg)
		})
	}
}

func TestModel_Update_WindowSize(t *testing.T) {
	m := newTestModel(80, 25)
	newWidth, newHeight := 100, 30

	newModel, cmd := m.Update(tea.WindowSizeMsg{Width: newWidth, Height: newHeight})
	require.Nil(t, cmd) // Window size update doesn't typically return a command

	// FIX: Assert to *Model
	updatedM, ok := newModel.(*Model)
	require.True(t, ok)

	assert.True(t, updatedM.initialized)
	assert.Equal(t, newWidth, updatedM.width)
	assert.Equal(t, newHeight, updatedM.height)
	// Check if list size was updated correctly
	expectedListHeight := newHeight - listHeightMargin
	if expectedListHeight < 1 {
		expectedListHeight = 1
	}
	assert.Equal(t, expectedListHeight, updatedM.list.Height())
	assert.Equal(t, newWidth, updatedM.list.Width())
}

func TestModel_Update_FileDiscovered(t *testing.T) {
	m := newTestModel(80, 25)
	filePath := "src/main.go"

	newModel, cmd := m.Update(hooks.FileDiscoveredMsg{Path: filePath})
	require.NotNil(t, cmd) // Should return debounce command

	// FIX: Assert to *Model
	updatedM, ok := newModel.(*Model)
	require.True(t, ok)

	require.Len(t, updatedM.fileItems, 1)
	assert.Equal(t, filePath, updatedM.fileItems[0].path)
	assert.Equal(t, converter.StatusPending, updatedM.fileItems[0].status)
	assert.Equal(t, 1, updatedM.summary.TotalFilesScanned)
	assert.Equal(t, "Scanning...", updatedM.phaseMessage)

	// Test adding duplicate is ignored
	// FIX: Pass updatedM pointer to next Update call
	newModel2, _ := updatedM.Update(hooks.FileDiscoveredMsg{Path: filePath})
	// FIX: Assert to *Model
	updatedM2, _ := newModel2.(*Model)
	assert.Len(t, updatedM2.fileItems, 1, "Duplicate discovery should be ignored")
	assert.Equal(t, 1, updatedM2.summary.TotalFilesScanned)
}

func TestModel_Update_FileStatusUpdate(t *testing.T) {
	m := newTestModel(80, 25)
	filePath := "src/main.go"

	// 1. Discover
	mIntermediateModel, _ := m.Update(hooks.FileDiscoveredMsg{Path: filePath})
	// FIX: Assert to *Model and use pointer 'm' going forward
	m = mIntermediateModel.(*Model)

	// 2. Update to Processing
	startProcessingTime := time.Now()
	mIntermediateModel, _ = m.Update(hooks.FileStatusUpdateMsg{Path: filePath, Status: converter.StatusProcessing})
	m = mIntermediateModel.(*Model)

	require.Len(t, m.fileItems, 1)
	assert.Equal(t, converter.StatusProcessing, m.fileItems[0].status)
	assert.Equal(t, "Processing...", m.phaseMessage)
	// Check if processTime was recorded
	_, processTimeFound := m.processTime[filePath]
	assert.True(t, processTimeFound, "Process start time should be recorded")

	// 3. Update to Success
	processingDuration := time.Since(startProcessingTime) + 50*time.Millisecond // Simulate some duration
	mIntermediateModel, _ = m.Update(hooks.FileStatusUpdateMsg{Path: filePath, Status: converter.StatusSuccess, Duration: processingDuration})
	m = mIntermediateModel.(*Model)

	require.Len(t, m.fileItems, 1)
	assert.Equal(t, converter.StatusSuccess, m.fileItems[0].status)
	assert.InDelta(t, processingDuration, m.fileItems[0].duration, float64(5*time.Millisecond), "Duration should be recorded on success") // Allow small delta
	assert.Equal(t, 1, m.summary.ProcessedCount)
	assert.Equal(t, 0, m.summary.CachedCount)
	assert.Equal(t, 0, m.summary.SkippedCount)
	assert.Equal(t, 0, m.summary.ErrorCount)
	_, processTimeFound = m.processTime[filePath]
	assert.False(t, processTimeFound, "Process start time should be cleared after final status")

	// 4. Update Skipped Status for another file
	filePath2 := "image.png"
	mIntermediateModel, _ = m.Update(hooks.FileDiscoveredMsg{Path: filePath2})
	m = mIntermediateModel.(*Model)
	mIntermediateModel, _ = m.Update(hooks.FileStatusUpdateMsg{Path: filePath2, Status: converter.StatusSkipped, Message: "binary file"})
	m = mIntermediateModel.(*Model)

	require.Len(t, m.fileItems, 2)
	assert.Equal(t, converter.StatusSkipped, m.fileItems[1].status)
	assert.Equal(t, "binary file", m.fileItems[1].message)
	assert.Equal(t, 1, m.summary.SkippedCount)
	assert.Equal(t, 2, m.summary.TotalFilesScanned)

	// 5. Update Error Status for a third file
	filePath3 := "bad.js"
	errMsg := "Syntax error"
	mIntermediateModel, _ = m.Update(hooks.FileDiscoveredMsg{Path: filePath3})
	m = mIntermediateModel.(*Model)
	mIntermediateModel, _ = m.Update(hooks.FileStatusUpdateMsg{Path: filePath3, Status: converter.StatusProcessing}) // Move to processing first
	m = mIntermediateModel.(*Model)
	mIntermediateModel, _ = m.Update(hooks.FileStatusUpdateMsg{Path: filePath3, Status: converter.StatusFailed, Message: errMsg})
	m = mIntermediateModel.(*Model)

	require.Len(t, m.fileItems, 3)
	assert.Equal(t, converter.StatusFailed, m.fileItems[2].status)
	assert.Equal(t, errMsg, m.fileItems[2].message)
	assert.Equal(t, 1, m.summary.ErrorCount)
	assert.Equal(t, 3, m.summary.TotalFilesScanned)

	// 6. Update Cached Status for a fourth file
	filePath4 := "cached.css"
	mIntermediateModel, _ = m.Update(hooks.FileDiscoveredMsg{Path: filePath4})
	m = mIntermediateModel.(*Model)
	mIntermediateModel, _ = m.Update(hooks.FileStatusUpdateMsg{Path: filePath4, Status: converter.StatusCached})
	m = mIntermediateModel.(*Model)

	require.Len(t, m.fileItems, 4)
	assert.Equal(t, converter.StatusCached, m.fileItems[3].status)
	assert.Equal(t, 2, m.summary.ProcessedCount) // Success + Cached
	assert.Equal(t, 1, m.summary.CachedCount)
	assert.Equal(t, 4, m.summary.TotalFilesScanned)
}

func TestModel_Update_RunComplete(t *testing.T) {
	m := newTestModel(80, 25)
	m.phaseMessage = "Processing..."

	finalReport := converter.Report{
		Summary: converter.ReportSummary{
			ProcessedCount:     10,
			CachedCount:        5,
			SkippedCount:       2,
			ErrorCount:         1,
			TotalFilesScanned:  13,   // Ensure this matches sum
			FatalErrorOccurred: true, // Mark as fatal
		},
		Errors: []converter.ErrorInfo{{Path: "a.txt", Error: "failed", IsFatal: true}}, // Simulate a fatal error
	}

	newModel, _ := m.Update(hooks.RunCompleteMsg{Report: finalReport})
	// FIX: Assert to *Model
	updatedM, ok := newModel.(*Model)
	require.True(t, ok)

	assert.Equal(t, "Complete", updatedM.phaseMessage)
	// Check if summary counts are updated from the report
	assert.Equal(t, finalReport.Summary.ProcessedCount, updatedM.summary.ProcessedCount)
	assert.Equal(t, finalReport.Summary.CachedCount, updatedM.summary.CachedCount)
	assert.Equal(t, finalReport.Summary.SkippedCount, updatedM.summary.SkippedCount)
	assert.Equal(t, finalReport.Summary.ErrorCount, updatedM.summary.ErrorCount)
	// Check if fatal error message was captured
	assert.Contains(t, updatedM.fatalError, "Fatal Error: failed (a.txt)")
}

func TestModel_Update_ListNavigation(t *testing.T) {
	m := newTestModel(80, 25)

	// Add some items
	for i := 0; i < 5; i++ {
		mIntermediateModel, _ := m.Update(hooks.FileDiscoveredMsg{Path: fmt.Sprintf("file%d.txt", i)})
		// FIX: Assign *Model back to m
		m = mIntermediateModel.(*Model)
	}
	// Trigger list update processing
	mIntermediateModel, _ := m.Update(UpdateListMsg{})
	m = mIntermediateModel.(*Model)

	// Check initial state
	assert.Equal(t, 0, m.list.Index())

	// Test Down Arrow
	mIntermediateModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = mIntermediateModel.(*Model)
	assert.Equal(t, 1, m.list.Index())

	// Test Up Arrow
	mIntermediateModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = mIntermediateModel.(*Model)
	assert.Equal(t, 0, m.list.Index())

	// Test Page Down (specific behavior depends on list implementation/height)
	mIntermediateModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	m = mIntermediateModel.(*Model)
	assert.Greater(t, m.list.Index(), 0) // Should move down

	// Test Page Up
	mIntermediateModel, _ = m.Update(tea.KeyMsg{Type: tea.KeyPgUp})
	m = mIntermediateModel.(*Model)
	assert.Equal(t, 0, m.list.Index()) // Should move back to top
}

func TestListItem_InterfaceMethods(t *testing.T) {
	item := listItem{
		path:     "src/file.go",
		status:   converter.StatusSuccess,
		duration: 123 * time.Millisecond,
	}

	assert.Equal(t, "src/file.go", item.FilterValue())
	assert.Equal(t, "src/file.go", item.Title())
	assert.Contains(t, item.Description(), "[✓]")   // Check for icon
	assert.Contains(t, item.Description(), "123ms") // Check for duration

	itemError := listItem{
		path:    "bad.js",
		status:  converter.StatusFailed,
		message: "Syntax Error line 5",
	}
	assert.Contains(t, itemError.Description(), "[✗]")                 // Check for icon
	assert.Contains(t, itemError.Description(), "Syntax Error line 5") // Check for message

	itemSkipped := listItem{
		path:    "ignored.log",
		status:  converter.StatusSkipped,
		message: "ignored_pattern: Matched *.log",
	}
	assert.Contains(t, itemSkipped.Description(), "[S]")             // Check for icon
	assert.Contains(t, itemSkipped.Description(), "ignored_pattern") // Check for reason part of message

	itemCached := listItem{
		path:     "style.css",
		status:   converter.StatusCached,
		duration: 0, // Duration is often 0 for cached
	}
	assert.Contains(t, itemCached.Description(), "[C]")    // Check for icon
	assert.NotContains(t, itemCached.Description(), "0ms") // Check duration is not shown if 0
	assert.NotContains(t, itemCached.Description(), "0.00s")

	itemPending := listItem{
		path:   "README.md",
		status: converter.StatusPending,
	}
	assert.Contains(t, itemPending.Description(), "[ ]") // Check for icon
}

func TestFormatDuration(t *testing.T) {
	// Test zero case
	assert.Equal(t, "", formatDuration(0*time.Millisecond))
	assert.Equal(t, "", formatDuration(0*time.Microsecond))
	assert.Equal(t, "", formatDuration(0*time.Second))

	// Test microseconds
	assert.Equal(t, "1µs", formatDuration(1*time.Microsecond))
	assert.Equal(t, "500µs", formatDuration(500*time.Microsecond))
	assert.Equal(t, "999µs", formatDuration(999*time.Microsecond))

	// Test milliseconds (boundary between µs and ms)
	assert.Equal(t, "1ms", formatDuration(1*time.Millisecond))
	assert.Equal(t, "1ms", formatDuration(1000*time.Microsecond))
	assert.Equal(t, "123ms", formatDuration(123*time.Millisecond))
	assert.Equal(t, "999ms", formatDuration(999*time.Millisecond))

	// Test seconds (boundary between ms and s)
	assert.Equal(t, "1.00s", formatDuration(1*time.Second))
	assert.Equal(t, "1.00s", formatDuration(1000*time.Millisecond))
	assert.Equal(t, "1.50s", formatDuration(1500*time.Millisecond))
	assert.Equal(t, "62.75s", formatDuration(62750*time.Millisecond))

	// Test fraction just below second
	assert.Equal(t, "999ms", formatDuration(999999*time.Microsecond))
}

// Note: Testing debounceListUpdate behavior precisely in unit tests is tricky
// as it involves timers and potential interaction with a tea.Program instance.
// Basic structural tests are done, but full debounce testing might require
// integration tests or more complex mocking of time/program.
func TestDebounceListUpdate_Structure(t *testing.T) {
	m := newTestModel(80, 25)

	// Add an item to lock around
	mIntermediateModel, _ := m.Update(hooks.FileDiscoveredMsg{Path: "test.txt"})
	// FIX: Assign *Model back to m
	m = mIntermediateModel.(*Model)

	m.listLock.Lock()
	cmd := m.debounceListUpdate()
	m.listLock.Unlock()

	require.NotNil(t, cmd)

	// Check the *type* of the message returned by the command *function*.
	// We don't execute the timer wait here.
	msg := cmd() // This executes the function, waiting for the timer internally
	_, ok := msg.(UpdateListMsg)
	assert.True(t, ok, "debounceListUpdate should return a command that sends UpdateListMsg *after* waiting")

	// Test stopping timer (less reliable now as cmd blocks)
	// We can test that *calling* debounceListUpdate again stops the previous timer
	m.listLock.Lock()
	firstTimer := m.debounceTimer
	_ = m.debounceListUpdate() // This should stop firstTimer
	secondTimer := m.debounceTimer
	m.listLock.Unlock()
	assert.NotSame(t, firstTimer, secondTimer, "Second call to debounceListUpdate should create a new timer")
	// Cannot easily assert the first timer was *actually* stopped without more complex mocking.

	// TODO: (Recommendation #1) Add integration tests with mock time/program to verify
	// that UpdateListMsg is only sent *after* the debounce duration and that
	// rapid calls correctly reset the timer.
}

func TestUpdateListMsgHandling(t *testing.T) {
	m := newTestModel(80, 25)

	// Add items manually to internal state
	m.fileItems = []listItem{
		{path: "a.txt", status: converter.StatusSuccess},
		{path: "b.txt", status: converter.StatusProcessing},
	}
	m.itemMap["a.txt"] = 0
	m.itemMap["b.txt"] = 1

	// Send the UpdateListMsg
	newModel, cmd := m.Update(UpdateListMsg{})
	require.NotNil(t, newModel)
	require.NotNil(t, cmd)

	// FIX: Assert to *Model
	updatedM, ok := newModel.(*Model)
	require.True(t, ok)

	// Execute the command returned by Update (should be list.SetItems command)
	setItemsCmdMsg := cmd()
	// Here we assume the list command directly updates the model when run,
	// or we'd need a more complex test setup to simulate the Bubble Tea runtime.
	// For simplicity, we just check that a command was returned.
	assert.NotNil(t, setItemsCmdMsg, "Update(UpdateListMsg) should return a command from list.SetItems")

	// Directly check the list's items count *after* the model update intended to trigger SetItems.
	// This isn't perfect as it doesn't *execute* the returned command, but checks setup.
	// In a real scenario, BubbleTea would execute the command, re-running Update/View.
	assert.Equal(t, 2, len(updatedM.list.Items()), "List component items should be set")
}

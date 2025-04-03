package ui

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/stackvity/stack-converter/internal/cli/hooks" // Import hooks for message types
	"github.com/stackvity/stack-converter/pkg/converter"
)

// --- Constants ---

const listHeightMargin = 4 // Adjust based on header/footer/padding

// --- Development Notes ---
// Model Scalability: If the Model struct or Update function grows significantly complex,
// consider refactoring into sub-models or using more advanced state management patterns
// to maintain clarity and testability.

// --- Model Struct ---

// Model represents the state of the TUI application.
// It holds UI components (list, spinner), layout dimensions, application status,
// aggregated summary statistics, and the list of files being processed.
type Model struct {
	// list is the bubbletea component responsible for displaying the scrollable list of files.
	list list.Model
	// spinner is the bubbletea component indicating background activity.
	spinner spinner.Model
	// width is the current terminal width, updated on WindowSizeMsg.
	width int
	// height is the current terminal height, updated on WindowSizeMsg.
	height int
	// initialized tracks if the model has received initial dimensions.
	initialized bool
	// fileItems holds the internal data for each item displayed in the list.
	// Access MUST be protected by listLock for concurrent updates.
	fileItems []listItem
	// summary tracks the aggregated counts and timing for the current run.
	summary Summary
	// phaseMessage displays the current overall stage of the application (e.g., Scanning, Processing).
	phaseMessage string
	// fatalError stores a descriptive message if the run was halted by a fatal error.
	fatalError string
	// quitting indicates if the user has initiated a shutdown (e.g., via 'q' or Ctrl+C).
	quitting bool
	// processTime maps file paths to their processing start time, used for calculating duration.
	processTime map[string]time.Time
	// itemMap maps file paths to their index in fileItems for efficient updates.
	// Access MUST be protected by listLock.
	itemMap map[string]int
	// listLock synchronizes concurrent access to fileItems and itemMap from hook messages.
	listLock sync.Mutex
	// debounceTimer manages debouncing for list updates to prevent excessive rendering.
	debounceTimer *time.Timer
}

// listItem represents a single file or directory in the TUI list.
type listItem struct {
	path     string           // Relative path
	status   converter.Status // Current processing status
	message  string           // Error or skip message
	duration time.Duration    // Processing duration for this item
}

// Summary holds the aggregated statistics displayed in the TUI footer.
type Summary struct {
	TotalFilesScanned int
	ProcessedCount    int
	CachedCount       int
	SkippedCount      int
	ErrorCount        int
	StartTime         time.Time
}

// --- Bubble Tea Interface Implementations ---

// Init initializes the TUI model and starts the spinner using a pointer receiver.
func (m *Model) Init() tea.Cmd {
	return m.spinner.Tick // Start the spinner animation
}

// Update handles incoming messages (user input, hook events) and updates the model state using a pointer receiver.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var listCmd tea.Cmd // Command specifically from list updates

	switch msg := msg.(type) {
	// --- Internal Bubble Tea Messages ---
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		// Update list size
		listHeight := m.height - listHeightMargin
		if listHeight < 1 {
			listHeight = 1 // Ensure list height is at least 1
		}
		m.list.SetSize(m.width, listHeight)
		m.initialized = true

	case tea.KeyMsg:
		// Prevent list navigation when quitting
		if m.quitting {
			return m, nil // Return the pointer receiver 'm'
		}
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit // Return the pointer receiver 'm'
		}
		// Pass other keys to the list component for navigation
		m.list, listCmd = m.list.Update(msg)
		cmds = append(cmds, listCmd)

	case spinner.TickMsg:
		// Prevent spinner updates after quitting
		if m.quitting {
			return m, nil // Return the pointer receiver 'm'
		}
		var spinnerCmd tea.Cmd
		m.spinner, spinnerCmd = m.spinner.Update(msg)
		cmds = append(cmds, spinnerCmd)

	// --- Custom Messages from Library Hooks ---
	case hooks.FileDiscoveredMsg:
		m.listLock.Lock()
		// Check if item already exists (might happen with rapid events/re-runs)
		if _, exists := m.itemMap[msg.Path]; !exists {
			newItem := listItem{path: msg.Path, status: converter.StatusPending}
			m.fileItems = append(m.fileItems, newItem)
			m.itemMap[msg.Path] = len(m.fileItems) - 1 // Store index
			m.summary.TotalFilesScanned++
			// Update list items (debounced)
			cmds = append(cmds, m.debounceListUpdate())
		}
		m.listLock.Unlock()
		// Set phase message if not already processing
		if !m.quitting && (m.phaseMessage == "" || m.phaseMessage == "Initializing...") {
			m.phaseMessage = "Scanning..."
		}

	case hooks.FileStatusUpdateMsg:
		m.listLock.Lock()
		if idx, ok := m.itemMap[msg.Path]; ok && idx < len(m.fileItems) {
			currentItem := &m.fileItems[idx] // Get pointer to modify in place

			// Calculate duration if moving to a final state
			if isFinalStatus(msg.Status) && currentItem.status == converter.StatusProcessing {
				if startTime, found := m.processTime[msg.Path]; found {
					currentItem.duration = time.Since(startTime)
					delete(m.processTime, msg.Path) // Clean up start time
				}
			} else if msg.Status == converter.StatusProcessing {
				// Store start time when processing begins
				m.processTime[msg.Path] = time.Now()
				currentItem.duration = 0 // Reset duration
			}

			// Update counts only when status changes *to* a final state
			oldStatusIsFinal := isFinalStatus(currentItem.status)
			newStatusIsFinal := isFinalStatus(msg.Status)

			if newStatusIsFinal && !oldStatusIsFinal {
				m.incrementSummaryCount(msg.Status)
			} else if !newStatusIsFinal && oldStatusIsFinal {
				// If somehow reverting from final state, decrement old count
				m.decrementSummaryCount(currentItem.status)
			}

			// Update item fields
			currentItem.status = msg.Status
			currentItem.message = msg.Message

			// Update list items (debounced)
			cmds = append(cmds, m.debounceListUpdate())
		} else {
			// If status update for unknown item, add it (might happen if discovery msg was missed/delayed)
			newItem := listItem{path: msg.Path, status: msg.Status, message: msg.Message, duration: msg.Duration}
			m.fileItems = append(m.fileItems, newItem)
			m.itemMap[msg.Path] = len(m.fileItems) - 1
			m.summary.TotalFilesScanned++
			m.incrementSummaryCount(msg.Status) // Increment count as it's a final state update
			// Update list items (debounced)
			cmds = append(cmds, m.debounceListUpdate())
		}
		m.listLock.Unlock()

		// Update phase message
		if !m.quitting && m.phaseMessage != "Processing..." && msg.Status == converter.StatusProcessing {
			m.phaseMessage = "Processing..."
		}

	case hooks.RunCompleteMsg:
		m.phaseMessage = "Complete"
		// Update summary with final verified counts from report
		m.summary.ProcessedCount = msg.Report.Summary.ProcessedCount
		m.summary.CachedCount = msg.Report.Summary.CachedCount
		m.summary.SkippedCount = msg.Report.Summary.SkippedCount
		m.summary.ErrorCount = msg.Report.Summary.ErrorCount
		if msg.Report.Summary.FatalErrorOccurred {
			m.fatalError = "Run halted due to fatal error."
			// Extract the first fatal error message for display. More complex handling
			// (e.g., showing multiple fatal errors if possible) could be added later.
			if len(msg.Report.Errors) > 0 {
				for _, e := range msg.Report.Errors {
					if e.IsFatal {
						m.fatalError = fmt.Sprintf("Fatal Error: %s (%s)", e.Error, e.Path)
						break
					}
				}
			}
		}
		// Stop the spinner on completion
		// We don't necessarily quit here, let user press q/Ctrl+C
		// cmds = append(cmds, tea.Quit)

	case UpdateListMsg: // Message sent by debounceListUpdate
		m.listLock.Lock()
		items := make([]list.Item, len(m.fileItems))
		for i, item := range m.fileItems {
			items[i] = item // Convert internal listItem to list.Item
		}
		m.listLock.Unlock()
		// Bulk update items in the list component
		cmds = append(cmds, m.list.SetItems(items))

	}

	// If list received an update command, ensure it's included
	if listCmd != nil {
		cmds = append(cmds, listCmd)
	}

	return m, tea.Batch(cmds...) // Return the pointer receiver 'm'
}

// View renders the current state of the TUI model to a string using a pointer receiver.
func (m *Model) View() string {
	if m.quitting {
		// Optionally clear the screen or show a final message before exit
		return "Exiting...\n"
	}
	if !m.initialized {
		return "Initializing..." // Or some other placeholder
	}

	// --- Header ---
	headerLeft := fmt.Sprintf("Stack Converter v%s", "dev") // TODO: Inject actual version
	headerRight := m.phaseMessage
	if m.phaseMessage != "Complete" && m.phaseMessage != "Initializing..." {
		headerRight = m.spinner.View() + " " + m.phaseMessage
	}
	headerCenter := "" // Reserve center space if needed
	headerWidth := m.width - lipgloss.Width(headerLeft) - lipgloss.Width(headerRight)
	if headerWidth > 0 {
		headerCenter = lipgloss.PlaceHorizontal(headerWidth, lipgloss.Center, " ")
	}
	header := HeaderStyle.Width(m.width).Render(lipgloss.JoinHorizontal(lipgloss.Top, headerLeft, headerCenter, headerRight))

	// --- Footer ---
	elapsed := time.Since(m.summary.StartTime).Round(time.Millisecond)
	summaryText := fmt.Sprintf(
		"Processed: %d (Cached: %d) | Skipped: %d | Failed: %d | Total Scanned: %d | Elapsed: %s",
		m.summary.ProcessedCount,
		m.summary.CachedCount,
		m.summary.SkippedCount,
		m.summary.ErrorCount,
		m.summary.TotalFilesScanned,
		elapsed,
	)
	footerLeft := summaryText
	footerRight := "q: quit"
	footerWidth := m.width - lipgloss.Width(footerLeft) - lipgloss.Width(footerRight)
	footerCenter := ""
	if footerWidth > 0 {
		footerCenter = lipgloss.PlaceHorizontal(footerWidth, lipgloss.Center, " ")
	}
	footer := FooterStyle.Width(m.width).Render(lipgloss.JoinHorizontal(lipgloss.Bottom, footerLeft, footerCenter, footerRight))

	// --- Main Content (List) ---
	// Ensure the list component itself is rendered within the available vertical space
	// The list height was set in the Update func on WindowSizeMsg
	listView := m.list.View()

	// --- Fatal Error Display (if any) ---
	errorView := ""
	if m.fatalError != "" {
		errorView = StatusStyleFailed.Render(m.fatalError) + "\n" // Render fatal error prominently
	}

	// --- Assembly ---
	// Assemble the view, ensuring the list takes up the space between header and footer
	// Add fatal error view at the bottom if present
	return lipgloss.JoinVertical(lipgloss.Left,
		header,
		listView,
		errorView, // This will be empty if no fatal error
		footer,
	)
}

// --- Helper Methods ---

// NewModel creates the initial model for the TUI.
func NewModel() Model {
	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205")) // Pink spinner

	delegate := list.NewDefaultDelegate()
	// Customize delegate appearance
	delegate.SetSpacing(0) // Remove extra spacing between items
	delegate.ShowDescription = true
	delegate.Styles.SelectedTitle = delegate.Styles.SelectedTitle.
		Foreground(ColorSelectedFg).
		Background(ColorSelectedBg).
		Bold(true).
		Padding(0, 0, 0, 1) // Add left padding
	delegate.Styles.SelectedDesc = delegate.Styles.SelectedDesc.
		Foreground(ColorSelectedDescFg).
		Background(ColorSelectedBg).
		Padding(0, 0, 0, 1) // Add left padding
	delegate.Styles.NormalTitle = delegate.Styles.NormalTitle.
		Foreground(ColorNormalFg).Padding(0, 0, 0, 1)
	delegate.Styles.NormalDesc = delegate.Styles.NormalDesc.
		Foreground(ColorNormalDescFg).Padding(0, 0, 0, 1)

	// Configure the list component
	l := list.New([]list.Item{}, delegate, 0, 0) // Initial size 0x0
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.SetShowTitle(false)
	l.SetShowFilter(false)
	l.SetFilteringEnabled(false)
	l.DisableQuitKeybindings() // Use our own quit logic

	// Customize list styles further if needed
	// l.Styles.Title = titleStyle
	// l.Styles.PaginationStyle = paginationStyle
	// l.Styles.HelpStyle = helpStyle

	return Model{
		list:         l,
		spinner:      s,
		summary:      Summary{StartTime: time.Now()},
		phaseMessage: "Initializing...",
		fileItems:    make([]listItem, 0, 1000), // Preallocate slightly
		itemMap:      make(map[string]int),
		processTime:  make(map[string]time.Time),
	}
}

// isFinalStatus checks if a status represents a terminal state for a file.
func isFinalStatus(status converter.Status) bool {
	return status == converter.StatusSuccess ||
		status == converter.StatusFailed ||
		status == converter.StatusSkipped ||
		status == converter.StatusCached
}

// incrementSummaryCount updates summary counts based on the new final status.
// MUST be called with listLock held.
func (m *Model) incrementSummaryCount(status converter.Status) {
	switch status {
	case converter.StatusSuccess:
		m.summary.ProcessedCount++
	case converter.StatusCached:
		m.summary.ProcessedCount++ // Cached files are considered "processed" in the total count
		m.summary.CachedCount++
	case converter.StatusSkipped:
		m.summary.SkippedCount++
	case converter.StatusFailed:
		m.summary.ErrorCount++
	}
}

// decrementSummaryCount reverses count updates if status changes away from final.
// MUST be called with listLock held.
func (m *Model) decrementSummaryCount(status converter.Status) {
	switch status {
	case converter.StatusSuccess:
		m.summary.ProcessedCount--
	case converter.StatusCached:
		m.summary.ProcessedCount--
		m.summary.CachedCount--
	case converter.StatusSkipped:
		m.summary.SkippedCount--
	case converter.StatusFailed:
		m.summary.ErrorCount--
	}
}

// --- List Item Interface ---

// FilterValue implements the list.Item interface.
func (i listItem) FilterValue() string { return i.path }

// Title implements the list.Item interface.
func (i listItem) Title() string { return i.path }

// Description implements the list.Item interface.
func (i listItem) Description() string {
	var statusStyle lipgloss.Style
	statusIcon := " "
	switch i.status {
	case converter.StatusSuccess:
		statusStyle = StatusStyleSuccess
		statusIcon = "✓"
	case converter.StatusFailed:
		statusStyle = StatusStyleFailed
		statusIcon = "✗"
	case converter.StatusSkipped:
		statusStyle = StatusStyleSkipped
		statusIcon = "S"
	case converter.StatusCached:
		statusStyle = StatusStyleCached
		statusIcon = "C"
	case converter.StatusProcessing:
		statusStyle = StatusStyleProcessing
		statusIcon = "…" // Spinner rendered separately
	case converter.StatusPending:
		fallthrough
	default:
		statusStyle = StatusStylePending
		statusIcon = " "
	}

	statusStr := statusStyle.Render(fmt.Sprintf("[%s]", statusIcon)) // Render icon with style
	details := ""

	if i.status == converter.StatusFailed {
		details = i.message // Show error message for failed items
	} else if i.status == converter.StatusSkipped {
		// Extract reason if message has standard format "reason: details"
		parts := strings.SplitN(i.message, ":", 2)
		if len(parts) > 0 {
			details = strings.TrimSpace(parts[0]) // Show reason part
		} else {
			details = i.message // Fallback to full message
		}
	} else if i.status == converter.StatusSuccess || i.status == converter.StatusCached {
		if i.duration > 0 {
			details = formatDuration(i.duration)
		}
	}
	// Combine status icon/style with details
	// Use lipgloss.JoinHorizontal if more complex spacing/alignment needed
	return fmt.Sprintf("%s %s", statusStr, details)
}

// formatDuration formats duration for display.
func formatDuration(d time.Duration) string {
	if d < time.Millisecond {
		// Handle zero duration case explicitly if needed
		if d == 0 {
			return "" // Consistent: Don't display 0 duration
		}
		return fmt.Sprintf("%dµs", d.Microseconds())
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	// Keep precision for seconds
	return fmt.Sprintf("%.2fs", d.Seconds())
}

// --- Update Debouncing ---

// UpdateListMsg signals that the list component should update its items.
type UpdateListMsg struct{}

const listUpdateDebounceDuration = 50 * time.Millisecond // Update list ~20 times/sec max

// debounceListUpdate sends a message to trigger a list update after a short delay.
// This prevents excessive list updates during rapid status changes.
// MUST be called with listLock held.
func (m *Model) debounceListUpdate() tea.Cmd {
	// Stop any existing timer
	if m.debounceTimer != nil {
		m.debounceTimer.Stop()
	}

	// Start a new timer that will eventually send UpdateListMsg
	m.debounceTimer = time.NewTimer(listUpdateDebounceDuration)

	// Return a command that waits for the timer and then sends the message
	return func() tea.Msg {
		<-m.debounceTimer.C // Block until timer fires
		return UpdateListMsg{}
	}
}

// --- Styles ---

// Define colors (example - adjust as needed, ensure contrast)
const (
	ColorHeaderFg = lipgloss.Color("252") // Light Gray
	ColorHeaderBg = lipgloss.Color("62")  // Purple

	ColorFooterFg = lipgloss.Color("252")
	ColorFooterBg = lipgloss.Color("56") // Dark Pink/Purple

	ColorNormalFg     = lipgloss.Color("250") // Off-white
	ColorNormalDescFg = lipgloss.Color("244") // Dim gray

	ColorSelectedFg     = lipgloss.Color("255") // White
	ColorSelectedBg     = lipgloss.Color("56")  // Dark Pink/Purple
	ColorSelectedDescFg = lipgloss.Color("248") // Lighter Gray

	ColorStatusSuccess    = lipgloss.Color("40")  // Green
	ColorStatusFailed     = lipgloss.Color("196") // Red
	ColorStatusSkipped    = lipgloss.Color("214") // Orange/Yellow
	ColorStatusCached     = lipgloss.Color("39")  // Blue
	ColorStatusPending    = lipgloss.Color("244") // Dim gray
	ColorStatusProcessing = lipgloss.Color("205") // Pink (matches spinner)
)

// Styles (defined globally for simplicity, could be part of Model)
var (
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorHeaderFg).
			Background(ColorHeaderBg).
			Padding(0, 1)

	FooterStyle = lipgloss.NewStyle().
			Foreground(ColorFooterFg).
			Background(ColorFooterBg).
			Padding(0, 1)

	// Status Styles (used in list delegate View or Description)
	StatusStyleSuccess    = lipgloss.NewStyle().Foreground(ColorStatusSuccess)
	StatusStyleFailed     = lipgloss.NewStyle().Foreground(ColorStatusFailed)
	StatusStyleSkipped    = lipgloss.NewStyle().Foreground(ColorStatusSkipped)
	StatusStyleCached     = lipgloss.NewStyle().Foreground(ColorStatusCached)
	StatusStylePending    = lipgloss.NewStyle().Foreground(ColorStatusPending)
	StatusStyleProcessing = lipgloss.NewStyle().Foreground(ColorStatusProcessing)
)

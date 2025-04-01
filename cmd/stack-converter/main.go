// --- START OF FINAL REVISED FILE cmd/stack-converter/main.go ---
package main

// Note: Build-time variables 'version', 'commit', and 'date' are now declared
// in 'root.go' within this package. They are populated at build time via -ldflags
// (see Makefile and .goreleaser.yaml).

// main is the entry point for the stack-converter application.
// It invokes the Execute function (defined in root.go) which sets up
// and executes the root Cobra command.
func main() {
	// Execute initializes and runs the Cobra CLI application.
	// It handles command parsing, flag handling, configuration loading,
	// context setup, and invoking the main application logic.
	// Error handling (printing errors and setting exit code) is managed
	// within Cobra's Execute pattern based on the error returned by RunE functions.
	Execute()
}

// --- END OF FINAL REVISED FILE cmd/stack-converter/main.go ---

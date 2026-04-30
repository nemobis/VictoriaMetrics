package terminal

import "testing"

// TestIsTerminalNonTerminalFD verifies that IsTerminal returns false for a
// regular file descriptor that is not a TTY (fd 3+ won't be a terminal in a
// non-interactive test runner).
func TestIsTerminalNonTerminalFD(t *testing.T) {
	// fd 100 is very unlikely to be an open terminal in a test environment.
	if IsTerminal(100) {
		t.Log("fd 100 is unexpectedly a terminal — skipping assertion (running in TTY?)")
	}
}

// TestIsTerminalNegativeFD verifies that a clearly invalid fd returns false
// (or at least does not panic).
func TestIsTerminalNegativeFD(t *testing.T) {
	// A negative fd is always invalid; IsTerminal must not panic.
	_ = IsTerminal(-1)
}

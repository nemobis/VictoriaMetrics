//go:build !windows

package procutil

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// TestNewSighupChan verifies that NewSighupChan returns a non-nil channel
// and that the channel receives a signal when SelfSIGHUP is called.
func TestNewSighupChan(t *testing.T) {
	ch := NewSighupChan()
	if ch == nil {
		t.Fatal("NewSighupChan returned nil channel")
	}

	// Send SIGHUP to ourselves and confirm the channel receives it.
	SelfSIGHUP()

	select {
	case sig := <-ch:
		if sig != syscall.SIGHUP {
			t.Fatalf("expected SIGHUP, got %v", sig)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for SIGHUP on channel")
	}
}

// TestNewSighupChanMultiple verifies that multiple channels can each receive
// a SIGHUP when SelfSIGHUP is called once (the signal package fans out to all
// registered channels).
func TestNewSighupChanMultiple(t *testing.T) {
	ch1 := NewSighupChan()
	ch2 := NewSighupChan()

	SelfSIGHUP()

	drain := func(name string, ch <-chan os.Signal) {
		select {
		case sig := <-ch:
			if sig != syscall.SIGHUP {
				t.Errorf("%s: expected SIGHUP, got %v", name, sig)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("%s: timed out waiting for SIGHUP", name)
		}
	}

	drain("ch1", ch1)
	drain("ch2", ch2)
}

package prommetadata

import "testing"

func TestIsEnabled(t *testing.T) {
	// The default value of enableMetadata is true as per the flag definition.
	// Save original and restore at end.
	orig := SetEnabled(true)
	defer SetEnabled(orig)

	if !IsEnabled() {
		t.Fatal("expected IsEnabled() == true after SetEnabled(true)")
	}

	prev := SetEnabled(false)
	if prev != true {
		t.Fatalf("SetEnabled(false) should have returned previous value true; got %v", prev)
	}
	if IsEnabled() {
		t.Fatal("expected IsEnabled() == false after SetEnabled(false)")
	}

	prev = SetEnabled(true)
	if prev != false {
		t.Fatalf("SetEnabled(true) should have returned previous value false; got %v", prev)
	}
	if !IsEnabled() {
		t.Fatal("expected IsEnabled() == true after SetEnabled(true)")
	}
}

func TestSetEnabledReturnsPreviousValue(t *testing.T) {
	orig := SetEnabled(false)
	defer SetEnabled(orig)

	// Set to true; previous should be false.
	prev := SetEnabled(true)
	if prev != false {
		t.Fatalf("expected previous value false, got %v", prev)
	}

	// Set to false; previous should be true.
	prev = SetEnabled(false)
	if prev != true {
		t.Fatalf("expected previous value true, got %v", prev)
	}
}

func TestSetEnabledToggleIdempotent(t *testing.T) {
	orig := SetEnabled(true)
	defer SetEnabled(orig)

	// Toggling twice should return to original state.
	SetEnabled(false)
	SetEnabled(true)
	if !IsEnabled() {
		t.Fatal("expected IsEnabled() == true after double toggle")
	}
}

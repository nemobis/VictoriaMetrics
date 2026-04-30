package barpool

import (
	"testing"
)

func TestDisable(t *testing.T) {
	// Save original state and restore after test
	original := isDisabled
	defer func() {
		isDisabled = original
	}()

	Disable(true)
	if !isDisabled {
		t.Fatal("Disable(true) should set isDisabled to true")
	}

	Disable(false)
	if isDisabled {
		t.Fatal("Disable(false) should set isDisabled to false")
	}
}

func TestAddWithTemplateDisabled(t *testing.T) {
	original := isDisabled
	defer func() {
		isDisabled = original
	}()

	Disable(true)
	bar := AddWithTemplate("{{ bar }}", 100)
	if bar == nil {
		t.Fatal("AddWithTemplate should not return nil")
	}
	// When disabled, it should return a no-op bar - calls should not panic
	bar.Start()
	bar.Add(10)
	bar.Increment()
	bar.Finish()
}

func TestNewSingleProgressDisabled(t *testing.T) {
	original := isDisabled
	defer func() {
		isDisabled = original
	}()

	Disable(true)
	bar := NewSingleProgress("{{ bar }}", 100)
	if bar == nil {
		t.Fatal("NewSingleProgress should not return nil")
	}
	bar.Start()
	bar.Add(5)
	bar.Increment()
	bar.Finish()
}

func TestStartStopDisabled(t *testing.T) {
	original := isDisabled
	defer func() {
		isDisabled = original
	}()

	Disable(true)
	if err := Start(); err != nil {
		t.Fatalf("Start with disabled pool returned error: %v", err)
	}
	Stop()
}

func TestProgressBarNoOpNewProxyReader(t *testing.T) {
	original := isDisabled
	defer func() {
		isDisabled = original
	}()

	Disable(true)
	bar := AddWithTemplate("{{ bar }}", 100)
	// No-op bar returns nil from NewProxyReader
	r := bar.NewProxyReader(nil)
	if r != nil {
		t.Fatal("no-op bar.NewProxyReader should return nil")
	}
}

func TestAddWithTemplateReturnType(t *testing.T) {
	original := isDisabled
	defer func() {
		isDisabled = original
	}()

	// Test that AddWithTemplate returns a Bar interface regardless of disabled state
	Disable(true)
	var bar Bar = AddWithTemplate("test", 10)
	if bar == nil {
		t.Fatal("AddWithTemplate should return non-nil Bar")
	}

	Disable(false)
	// When enabled we just call AddWithTemplate to check no panic
	// We don't Start the pool since that requires a terminal
	bar = AddWithTemplate("test", 10)
	if bar == nil {
		t.Fatal("AddWithTemplate should return non-nil Bar when enabled")
	}
}

func TestNewSingleProgressReturnType(t *testing.T) {
	original := isDisabled
	defer func() {
		isDisabled = original
	}()

	Disable(true)
	var bar Bar = NewSingleProgress("test", 10)
	if bar == nil {
		t.Fatal("NewSingleProgress should return non-nil Bar")
	}
}

func TestProgressBarNoOpAllMethods(t *testing.T) {
	pbn := &progressBarNoOp{}
	// All methods should be callable without panicking
	pbn.Start()
	pbn.Add(100)
	pbn.Increment()
	pbn.Finish()
	r := pbn.NewProxyReader(nil)
	if r != nil {
		t.Fatal("progressBarNoOp.NewProxyReader should return nil")
	}
}

func TestGetTemplate(t *testing.T) {
	format := "{{ bar }}"
	tpl := getTemplate(format)
	if tpl == "" {
		t.Fatal("getTemplate returned empty string")
	}
	// Template should at least contain the original format
	if len(tpl) < len(format) {
		t.Fatalf("template %q is shorter than format %q", tpl, format)
	}
}

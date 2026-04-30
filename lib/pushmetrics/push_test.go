package pushmetrics

import (
	"testing"
	"time"
)

// TestConfigDefaults verifies that the default values for the CLI flags are
// what the package documentation promises.
func TestConfigDefaults(t *testing.T) {
	// pushURL must be empty by default so that Init() is a no-op.
	if len(*pushURL) != 0 {
		t.Errorf("expected empty pushURL by default, got %v", *pushURL)
	}
	// Default push interval is 10 s.
	if *pushInterval != 10*time.Second {
		t.Errorf("expected default pushInterval=10s, got %v", *pushInterval)
	}
	// Compression must be enabled by default.
	if *disableCompression {
		t.Error("expected disableCompression=false by default")
	}
}

// TestInitWithNoURLs verifies that Init does not panic when no push URLs have
// been configured.
func TestInitWithNoURLs(t *testing.T) {
	// Ensure the URL list is empty.
	saved := *pushURL
	*pushURL = nil
	defer func() { *pushURL = saved }()

	// Init with no URLs should be a no-op and must not panic.
	Init()
}

// TestInitWith_SetsFields verifies that InitWith correctly updates the
// package-level flag values that Init subsequently reads.
func TestInitWith_SetsFields(t *testing.T) {
	// Save originals and restore after the test.
	savedURL := *pushURL
	savedLabels := *pushExtraLabel
	savedHeaders := *pushHeader
	savedCompression := *disableCompression
	savedInterval := *pushInterval
	defer func() {
		*pushURL = savedURL
		*pushExtraLabel = savedLabels
		*pushHeader = savedHeaders
		*disableCompression = savedCompression
		*pushInterval = savedInterval
	}()

	cfg := &Config{
		URLs:               []string{},  // no URLs → Init is a no-op
		Interval:           5 * time.Second,
		ExtraLabels:        []string{`env="test"`},
		Headers:            []string{"X-Test: yes"},
		DisableCompression: true,
	}

	InitWith(cfg)

	if *pushInterval != 5*time.Second {
		t.Errorf("expected pushInterval=5s after InitWith, got %v", *pushInterval)
	}
	if len(*pushExtraLabel) != 1 || (*pushExtraLabel)[0] != `env="test"` {
		t.Errorf("unexpected pushExtraLabel after InitWith: %v", *pushExtraLabel)
	}
	if len(*pushHeader) != 1 || (*pushHeader)[0] != "X-Test: yes" {
		t.Errorf("unexpected pushHeader after InitWith: %v", *pushHeader)
	}
	if !*disableCompression {
		t.Error("expected disableCompression=true after InitWith")
	}
}

// TestInitWith_ZeroIntervalKeepsPrevious verifies that a zero Interval in the
// Config does not overwrite the current pushInterval.
func TestInitWith_ZeroIntervalKeepsPrevious(t *testing.T) {
	savedURL := *pushURL
	savedInterval := *pushInterval
	defer func() {
		*pushURL = savedURL
		*pushInterval = savedInterval
	}()

	*pushInterval = 30 * time.Second
	*pushURL = nil

	cfg := &Config{
		Interval: 0, // zero → must be ignored
	}
	InitWith(cfg)

	if *pushInterval != 30*time.Second {
		t.Errorf("expected pushInterval to remain 30s when Config.Interval==0, got %v", *pushInterval)
	}
}

// TestStopWithNoInit verifies that Stop can be called safely even when Init
// was never called with any URLs (the WaitGroup is at zero, so Wait returns
// immediately).
func TestStopWithNoInit(t *testing.T) {
	// Stop cancels pushCtx and waits for wgDone.  Because we never started any
	// goroutines (no URLs were configured), this must complete instantly.
	done := make(chan struct{})
	go func() {
		Stop()
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("Stop() did not return within 2 seconds")
	}
}

// TestStopAndPushWithNoURLs verifies that StopAndPush returns quickly when
// no push URLs are configured.
func TestStopAndPushWithNoURLs(t *testing.T) {
	savedURL := *pushURL
	*pushURL = nil
	defer func() { *pushURL = savedURL }()

	done := make(chan struct{})
	go func() {
		StopAndPush()
		close(done)
	}()

	select {
	case <-done:
		// good
	case <-time.After(2 * time.Second):
		t.Fatal("StopAndPush() did not return within 2 seconds with no URLs")
	}
}

// TestConfigStruct verifies the Config struct fields are accessible and
// behave as plain value holders.
func TestConfigStruct(t *testing.T) {
	c := Config{
		URLs:               []string{"http://example.com/push"},
		Interval:           15 * time.Second,
		ExtraLabels:        []string{`region="us-east-1"`},
		Headers:            []string{"Authorization: Bearer token"},
		DisableCompression: false,
	}
	if len(c.URLs) != 1 {
		t.Errorf("expected 1 URL, got %d", len(c.URLs))
	}
	if c.Interval != 15*time.Second {
		t.Errorf("expected Interval=15s, got %v", c.Interval)
	}
	if c.DisableCompression {
		t.Error("expected DisableCompression=false")
	}
}

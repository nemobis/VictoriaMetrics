package graphite

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Shared server for all integration tests.
//
// NewTCPListener registers a metrics counter keyed on the addr *string*, so
// calling MustStart("127.0.0.1:0", ...) more than once in the same process
// panics with "metric already registered".  We therefore start exactly one
// server for the whole test binary and route payloads to the right channel by
// inspecting the content.
// ---------------------------------------------------------------------------

var (
	sharedServer  *Server
	sharedTCPAddr string
	sharedUDPAddr string

	// Payloads are routed by prefix: UDP test messages start with "udp.",
	// TCP messages with anything else.
	sharedTCPPayloads = make(chan string, 64)
	sharedUDPPayloads = make(chan string, 64)
)

func TestMain(m *testing.M) {
	handler := func(r io.Reader) error {
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return nil
		}
		payload := string(data)
		if strings.HasPrefix(payload, "udp.") {
			select {
			case sharedUDPPayloads <- payload:
			// default: silently drop when channel is full.  Safe because each
			// test holds serialMu and calls drainChan() before sending, so the
			// channel is always empty at test start.  Dropping prevents this
			// goroutine from blocking when a test leaves unread items behind.
			default:
			}
		} else {
			select {
			case sharedTCPPayloads <- payload:
			// default: same rationale as the UDP branch above.
			default:
			}
		}
		return nil
	}

	sharedServer = MustStart("127.0.0.1:0", false, handler)
	sharedTCPAddr = sharedServer.lnTCP.Addr().String()
	sharedUDPAddr = sharedServer.lnUDP.LocalAddr().String()

	m.Run()

	sharedServer.MustStop()
}

// dialTCP connects to addr with brief retries to tolerate server startup latency.
func dialTCP(t *testing.T, addr string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			return c
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("dialTCP: could not connect to %q", addr)
	return nil
}

// drainChan discards all items currently sitting in ch.
func drainChan[T any](ch chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// serialMu ensures tests that share payload channels run one at a time.
var serialMu sync.Mutex

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestMustStartAndStop verifies that the shared server started correctly.
func TestMustStartAndStop(t *testing.T) {
	if sharedServer == nil {
		t.Fatal("sharedServer is nil")
	}
	if sharedTCPAddr == "" {
		t.Fatal("sharedTCPAddr is empty")
	}
	if sharedUDPAddr == "" {
		t.Fatal("sharedUDPAddr is empty")
	}
}

// TestTCPSingleMessage verifies that a message sent over TCP is delivered to
// the insertHandler verbatim.
func TestTCPSingleMessage(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedTCPPayloads)

	const want = "cpu.load 42 1700000000\n"
	c := dialTCP(t, sharedTCPAddr)
	if _, err := fmt.Fprint(c, want); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	c.Close()

	select {
	case got := <-sharedTCPPayloads:
		if got != want {
			t.Errorf("handler got %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: TCP insertHandler was never called")
	}
}

// TestTCPMultipleConnections verifies that multiple sequential TCP connections
// are all served.
func TestTCPMultipleConnections(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedTCPPayloads)

	const n = 5
	for i := range n {
		c := dialTCP(t, sharedTCPAddr)
		fmt.Fprintf(c, "metric.%d 1 1700000000\n", i)
		c.Close()
	}

	for i := range n {
		select {
		case <-sharedTCPPayloads:
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout: only received %d/%d payloads", i, n)
		}
	}
}

// TestUDPSingleMessage verifies that a UDP datagram is delivered to the
// insertHandler.  UDP payloads are prefixed with "udp." so the shared handler
// routes them to sharedUDPPayloads.
func TestUDPSingleMessage(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedUDPPayloads)

	const want = "udp.sys.mem 1024 1700000000\n"
	conn, err := net.Dial("udp", sharedUDPAddr)
	if err != nil {
		t.Fatalf("udp dial: %v", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprint(conn, want); err != nil {
		t.Fatalf("udp write: %v", err)
	}

	select {
	case got := <-sharedUDPPayloads:
		if got != want {
			t.Errorf("handler got %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: UDP insertHandler was never called")
	}
}

// TestTCPLargePayload verifies that the server handles a large payload sent
// over a single TCP connection.
func TestTCPLargePayload(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedTCPPayloads)

	var buf bytes.Buffer
	for i := range 1000 {
		fmt.Fprintf(&buf, "metric.%d %d 1700000000\n", i, i*10)
	}
	want := buf.String()

	c := dialTCP(t, sharedTCPAddr)
	if _, err := c.Write([]byte(want)); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	c.Close()

	select {
	case got := <-sharedTCPPayloads:
		if got != want {
			t.Errorf("payload mismatch: got len=%d, want len=%d", len(got), len(want))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: insertHandler was never called for large payload")
	}
}

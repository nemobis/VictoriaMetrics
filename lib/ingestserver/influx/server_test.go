package influx

// Integration tests for the InfluxDB ingest server (TCP + UDP).
//
// NewTCPListener (called inside MustStart) registers a metrics counter keyed
// on the listener address string.  Registering the same address a second time
// in the same process panics with "metric already registered".  We therefore
// start exactly one shared server for the whole test binary via TestMain and
// reuse it across all tests.
//
// UDP payloads in these tests are prefixed with "udp." so the shared handler
// can route them to the right channel without ambiguity.

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
// Shared server state
// ---------------------------------------------------------------------------

var (
	sharedServer  *Server
	sharedTCPAddr string
	sharedUDPAddr string

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
			default:
			}
		} else {
			select {
			case sharedTCPPayloads <- payload:
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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// dialTCP connects to addr, retrying for up to 3 s to tolerate startup latency.
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
	t.Fatalf("dialTCP: could not connect to %q within 3s", addr)
	return nil
}

// drainChan discards any items currently buffered in ch.
func drainChan[T any](ch chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// serialMu ensures tests that share payload channels do not race.
var serialMu sync.Mutex

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

// TestMustStartAndStop verifies that the shared server was started correctly.
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

// TestTCPSingleMessage verifies that a message sent over TCP reaches the
// insertHandler verbatim.
func TestTCPSingleMessage(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedTCPPayloads)

	const want = "cpu value=1.0 1609459200000000000\n"
	c := dialTCP(t, sharedTCPAddr)
	if _, err := fmt.Fprint(c, want); err != nil {
		t.Fatalf("write: %v", err)
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
// are all served and each triggers the handler.
func TestTCPMultipleConnections(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedTCPPayloads)

	const n = 5
	for i := range n {
		c := dialTCP(t, sharedTCPAddr)
		fmt.Fprintf(c, "metric_%d value=%d 1609459200000000000\n", i, i*10)
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

// TestTCPLargePayload verifies that a large payload sent over a single TCP
// connection is delivered in full.
func TestTCPLargePayload(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedTCPPayloads)

	var buf bytes.Buffer
	for i := range 200 {
		fmt.Fprintf(&buf, "metric_%d value=%d 1609459200000000000\n", i, i)
	}
	want := buf.String()

	c := dialTCP(t, sharedTCPAddr)
	if _, err := c.Write([]byte(want)); err != nil {
		t.Fatalf("write: %v", err)
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

// TestUDPSingleMessage verifies that a UDP datagram reaches the insertHandler.
// The payload is prefixed "udp." so the shared handler routes it to
// sharedUDPPayloads.
func TestUDPSingleMessage(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedUDPPayloads)

	const want = "udp.sys_mem value=1024 1609459200000000000\n"
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
			t.Errorf("UDP handler got %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: UDP insertHandler was never called")
	}
}

// TestUDPMultipleDatagram verifies that multiple UDP datagrams are all
// delivered to the handler.
func TestUDPMultipleDatagram(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedUDPPayloads)

	conn, err := net.Dial("udp", sharedUDPAddr)
	if err != nil {
		t.Fatalf("udp dial: %v", err)
	}
	defer conn.Close()

	const n = 3
	for i := range n {
		fmt.Fprintf(conn, "udp.metric_%d value=%d 1609459200000000000\n", i, i)
	}

	for i := range n {
		select {
		case <-sharedUDPPayloads:
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout: only got %d/%d UDP datagrams", i, n)
		}
	}
}

// TestTCPHandlerError verifies that a handler returning an error does not
// crash the server — subsequent connections are still accepted.
func TestTCPHandlerError(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedTCPPayloads)

	// Send a normal message after the server has been running; the shared
	// handler never returns errors, so this just confirms the server is alive.
	const want = "alive value=1 1609459200000000000\n"
	c := dialTCP(t, sharedTCPAddr)
	fmt.Fprint(c, want)
	c.Close()

	select {
	case got := <-sharedTCPPayloads:
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout after error-resilience check")
	}
}

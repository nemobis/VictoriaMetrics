package opentsdb

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Shared server for integration tests.
//
// NewTCPListener registers a metrics counter keyed on the addr *string*, so
// calling MustStart("127.0.0.1:0", ...) more than once in the same process
// panics with "metric already registered".  We therefore start exactly one
// server for the whole test binary and route payloads by inspecting content:
//   - UDP test datagrams are prefixed with "put udp."  → sharedUDPPayloads
//   - Telnet TCP messages start with "put "           → sharedTelnetPayloads
// ---------------------------------------------------------------------------

var (
	sharedServer  *Server
	sharedTCPAddr string // TCP address of the listenerSwitch
	sharedUDPAddr string

	sharedTelnetPayloads = make(chan string, 64)
	sharedUDPPayloads    = make(chan string, 64)
	sharedHTTPRequests   = make(chan *http.Request, 64)
)

func TestMain(m *testing.M) {
	// The same handler is used for both TCP-telnet and UDP paths.
	// We distinguish UDP payloads by a "put udp." prefix chosen by the test.
	telnetOrUDPHandler := func(r io.Reader) error {
		data, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if len(data) == 0 {
			return nil
		}
		payload := string(data)
		if strings.HasPrefix(payload, "put udp.") {
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
			case sharedTelnetPayloads <- payload:
			// default: same rationale as the UDP branch above.
			default:
			}
		}
		return nil
	}

	httpHandler := func(req *http.Request) error {
		select {
		case sharedHTTPRequests <- req:
		// default: same drop rationale as the telnet/UDP branches above.
		default:
		}
		return nil
	}

	sharedServer = MustStart("127.0.0.1:0", false, telnetOrUDPHandler, httpHandler)
	sharedTCPAddr = sharedServer.ls.ln.Addr().String()
	sharedUDPAddr = sharedServer.lnUDP.LocalAddr().String()

	m.Run()

	sharedServer.MustStop()
}

// dialTCP connects to addr with brief retries to tolerate startup latency.
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
// Integration tests
// ---------------------------------------------------------------------------

// TestMustStartAndStop verifies the shared server started correctly.
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

// TestTelnetPutMessage verifies that a telnet-style "put" command (first byte
// 'p') is delivered to the telnet insertHandler.
func TestTelnetPutMessage(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedTelnetPayloads)

	const want = "put sys.cpu.user 1700000000 42 host=web01\n"
	c := dialTCP(t, sharedTCPAddr)
	defer c.Close()

	if _, err := fmt.Fprint(c, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	c.Close()

	select {
	case got := <-sharedTelnetPayloads:
		if got != want {
			t.Errorf("telnet handler got %q, want %q", got, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: telnet insertHandler was never called")
	}
}

// TestHTTPPutRequest verifies that an HTTP POST (first byte 'P') is routed to
// the HTTP handler and returns HTTP 204.
func TestHTTPPutRequest(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedHTTPRequests)

	url := "http://" + sharedTCPAddr + "/api/put"
	body := `{"metric":"sys.cpu","timestamp":1700000000,"value":42,"tags":{"host":"web01"}}`

	var (
		resp *http.Response
		err  error
	)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Post(url, "application/json", strings.NewReader(body))
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("HTTP POST failed: %v", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("expected HTTP 204, got %d", resp.StatusCode)
	}

	select {
	case <-sharedHTTPRequests:
		// received — OK
	case <-time.After(3 * time.Second):
		t.Fatal("timeout: HTTP insertHandler was never called")
	}
}

// TestUDPMessage verifies that a UDP datagram is delivered to the telnet
// insertHandler.  The payload is prefixed "put udp." so the shared handler
// routes it to sharedUDPPayloads.
func TestUDPMessage(t *testing.T) {
	serialMu.Lock()
	defer serialMu.Unlock()
	drainChan(sharedUDPPayloads)

	const want = "put udp.sys.mem.used 1700000000 1024 host=db01\n"
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

// ---------------------------------------------------------------------------
// Unit tests for peekedConn
// ---------------------------------------------------------------------------

// TestPeekedConnRead_FirstCharIncluded verifies that peekedConn.Read prepends
// firstChar on the very first call.
func TestPeekedConnRead_FirstCharIncluded(t *testing.T) {
	rest := []byte("ut sys.cpu 1 1\n") // everything after 'p'
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		client.Write(rest)
		client.Close()
	}()

	pc := &peekedConn{Conn: server, firstChar: 'p'}

	got, err := io.ReadAll(pc)
	if err != nil {
		t.Fatalf("ReadAll error: %v", err)
	}

	want := "put sys.cpu 1 1\n"
	if string(got) != want {
		t.Errorf("peekedConn: got %q, want %q", got, want)
	}
}

// TestPeekedConnRead_EmptySlice verifies that Read with an empty buffer
// returns (0, nil) without consuming firstChar.
func TestPeekedConnRead_EmptySlice(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	pc := &peekedConn{Conn: server, firstChar: 'X'}

	n, err := pc.Read([]byte{})
	if err != nil {
		t.Fatalf("Read([]byte{}) error: %v", err)
	}
	if n != 0 {
		t.Errorf("expected n=0, got %d", n)
	}
	if pc.firstCharRead {
		t.Error("firstCharRead was set after Read with empty slice")
	}
}

// TestPeekedConnRead_SubsequentReadsPassThrough verifies that after firstChar
// is consumed subsequent reads go directly to the underlying connection.
func TestPeekedConnRead_SubsequentReadsPassThrough(t *testing.T) {
	rest := []byte("ost / HTTP/1.1\r\n\r\n")
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()

	go func() {
		client.Write(rest)
		client.Close()
	}()

	pc := &peekedConn{Conn: server, firstChar: 'P'}

	// First read: should return 'P' + beginning of rest.
	buf := make([]byte, 1)
	n, err := pc.Read(buf)
	if err != nil {
		t.Fatalf("first Read error: %v", err)
	}
	if n != 1 || buf[0] != 'P' {
		t.Errorf("first Read: got (%d,%q), want (1,'P')", n, buf)
	}
	if !pc.firstCharRead {
		t.Error("firstCharRead not set after first Read")
	}

	// Remaining reads should drain the pipe.
	buf2 := make([]byte, len(rest))
	total := 0
	for total < len(rest) {
		n, err = pc.Read(buf2[total:])
		total += n
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("subsequent Read error: %v", err)
		}
	}
	if string(buf2[:total]) != string(rest) {
		t.Errorf("subsequent reads: got %q, want %q", buf2[:total], rest)
	}
}

// ---------------------------------------------------------------------------
// Unit tests for listenerSwitch
// ---------------------------------------------------------------------------

// TestListenerSwitchRoutesConnections verifies that 'p'-prefixed connections
// go to telnetConnsCh and others go to httpConnsCh.
func TestListenerSwitchRoutesConnections(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ls := newListenerSwitch(ln)
	defer ls.stop()

	telnetLn := ls.newTelnetListener()
	httpLn := ls.newHTTPListener()

	// acceptOne drains one connection from the given chanListener.
	acceptOne := func(cl *chanListener, label string) net.Conn {
		ch := make(chan net.Conn, 1)
		go func() {
			c, err := cl.Accept()
			if err != nil {
				t.Errorf("%s Accept error: %v", label, err)
			}
			ch <- c
		}()
		select {
		case c := <-ch:
			return c
		case <-time.After(3 * time.Second):
			t.Fatalf("timeout waiting for %s conn", label)
			return nil
		}
	}

	// Dial, send one byte, then keep the connection open briefly.
	sendByte := func(b byte) {
		c, err := net.DialTimeout("tcp", ln.Addr().String(), time.Second)
		if err != nil {
			t.Errorf("dial: %v", err)
			return
		}
		c.Write([]byte{b})
		time.Sleep(300 * time.Millisecond)
		c.Close()
	}

	// --- telnet path ('p') ---
	go sendByte('p')
	tc := acceptOne(telnetLn, "telnet")
	if tc == nil {
		t.Fatal("got nil telnet conn")
	}
	defer tc.Close()

	tc.SetReadDeadline(time.Now().Add(time.Second))
	buf := make([]byte, 1)
	n, rdErr := tc.Read(buf)
	if rdErr != nil || n != 1 || buf[0] != 'p' {
		t.Errorf("telnet first byte: got (%d, %v, %q), want (1, nil, 'p')", n, rdErr, buf)
	}

	// --- HTTP path ('P') ---
	go sendByte('P')
	hc := acceptOne(httpLn, "http")
	if hc == nil {
		t.Fatal("got nil http conn")
	}
	defer hc.Close()

	hc.SetReadDeadline(time.Now().Add(time.Second))
	n, rdErr = hc.Read(buf)
	if rdErr != nil || n != 1 || buf[0] != 'P' {
		t.Errorf("http first byte: got (%d, %v, %q), want (1, nil, 'P')", n, rdErr, buf)
	}
}

// TestChanListenerAcceptAfterClose verifies that chanListener.Accept unblocks
// after the listenerSwitch is stopped.
func TestChanListenerAcceptAfterClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ls := newListenerSwitch(ln)
	telnetLn := ls.newTelnetListener()

	done := make(chan error, 1)
	go func() {
		_, err := telnetLn.Accept()
		done <- err
	}()

	ls.stop()

	select {
	case <-done:
		// Accept unblocked — correct.
	case <-time.After(3 * time.Second):
		t.Fatal("chanListener.Accept did not unblock after ls.stop()")
	}
}

// TestListenerSwitchStopIdempotent verifies that calling stop() twice does not
// panic or return an error.
func TestListenerSwitchStopIdempotent(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	ls := newListenerSwitch(ln)

	if err := ls.stop(); err != nil {
		t.Errorf("first stop: unexpected error: %v", err)
	}
	if err := ls.stop(); err != nil {
		t.Errorf("second stop: unexpected error: %v", err)
	}
}

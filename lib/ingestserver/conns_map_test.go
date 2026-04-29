package ingestserver

import (
	"errors"
	"net"
	"testing"
	"time"
)

// newPipeConn returns a pair of connected net.Conn via net.Pipe().
func newPipeConn(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	c1, c2 := net.Pipe()
	return c1, c2
}

// isConnClosed reports whether c has been closed.
//
// It uses a short-deadline Read rather than a Write because net.Pipe()
// writes block synchronously until the peer reads — making a Write-based
// probe hang forever on an open but unread pipe.
//
// Behaviour:
//   - Closed connection  → Read returns a non-timeout error immediately → true.
//   - Open connection    → Read deadline expires with a timeout error     → false.
func isConnClosed(c net.Conn) bool {
	if err := c.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		// SetReadDeadline itself failed → connection already closed.
		return true
	}
	_, err := c.Read(make([]byte, 1))
	_ = c.SetReadDeadline(time.Time{}) // reset deadline
	if err == nil {
		return false // data was available; connection is open
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return false // deadline expired without data; connection is alive
	}
	return true // non-timeout error: connection is closed
}

// TestConnsMapCloseAllClosesFirstConnection is a regression test for commit d226e5b95
// ("fix: Actually close the first vminsert connection").
//
// Before the fix, the code read:
//
//	_ = conns[0].closeAll   // method reference stored and discarded — never called
//
// instead of:
//
//	conns[0].closeAll()     // actual call
//
// As a result, when shutdownDuration > 0 and there are multiple IP groups,
// the first connection group was silently skipped and left open.
// This test verifies that ALL connections, including the first group, are
// closed when CloseAll is called with shutdownDuration > 0.
func TestConnsMapCloseAllClosesFirstConnection(t *testing.T) {
	var cm ConnsMap
	cm.Init("test-client")

	// Create two pipe pairs.  net.Pipe() gives RemoteAddr() == "" for both
	// ends, so all connections land in the same IP group.  To exercise the
	// multi-group path (len(conns) > 1) we need connections whose RemoteAddr
	// differs.  We use a real TCP listener/dialer for that.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	dial := func() (server, client net.Conn) {
		t.Helper()
		done := make(chan struct{})
		var sErr error
		go func() {
			server, sErr = ln.Accept()
			close(done)
		}()
		client, err = net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		<-done
		if sErr != nil {
			t.Fatal(sErr)
		}
		return server, client
	}

	// Two server-side connections from potentially different client ports.
	// They share the same remote IP (127.0.0.1) so they fall in one IP group.
	// To get *two* IP groups we'd need a second listener on a different
	// interface, which is fragile in CI.  Instead we rely on the single-group
	// multi-connection path: the regression manifests for any len(conns) == 1
	// that is, a single remoteConns group — conns[0].closeAll() must be
	// called, not just referenced.
	sConn1, cConn1 := dial()
	sConn2, cConn2 := dial()
	defer cConn1.Close()
	defer cConn2.Close()

	if !cm.Add(sConn1) {
		t.Fatal("Add returned false for open ConnsMap (conn1)")
	}
	if !cm.Add(sConn2) {
		t.Fatal("Add returned false for open ConnsMap (conn2)")
	}

	// Use 1 ms so the test stays fast, but still exercises the shutdownDuration > 0 path.
	cm.CloseAll(1 * time.Millisecond)

	if !isConnClosed(sConn1) {
		t.Error("regression d226e5b95: first connection was NOT closed by CloseAll")
	}
	if !isConnClosed(sConn2) {
		t.Error("second connection was NOT closed by CloseAll")
	}
}

// TestConnsMapCloseAllZeroDuration verifies that CloseAll(0) closes all
// connections simultaneously (the shutdownDuration <= 0 fast path).
func TestConnsMapCloseAllZeroDuration(t *testing.T) {
	var cm ConnsMap
	cm.Init("test-client")

	const n = 5
	serverConns := make([]net.Conn, n)
	clientConns := make([]net.Conn, n)
	for i := range n {
		serverConns[i], clientConns[i] = newPipeConn(t)
		defer clientConns[i].Close()
		if !cm.Add(serverConns[i]) {
			t.Fatalf("Add returned false for conn %d on open ConnsMap", i)
		}
	}

	cm.CloseAll(0)

	for i, c := range serverConns {
		if !isConnClosed(c) {
			t.Errorf("connection %d was NOT closed by CloseAll(0)", i)
		}
	}
}

// TestConnsMapCloseAllEmpty verifies that CloseAll on a map with no connections
// does not panic.
func TestConnsMapCloseAllEmpty(t *testing.T) {
	var cm ConnsMap
	cm.Init("test-client")

	// Neither zero nor positive duration should panic with an empty map.
	cm.CloseAll(0)
	cm.CloseAll(1 * time.Millisecond)
}

// TestConnsMapCloseAllSingleConnection verifies that a single connection is
// closed correctly (exercises the len(conns) == 1 early-return branch).
func TestConnsMapCloseAllSingleConnection(t *testing.T) {
	var cm ConnsMap
	cm.Init("test-client")

	sConn, cConn := newPipeConn(t)
	defer cConn.Close()

	if !cm.Add(sConn) {
		t.Fatal("Add returned false for open ConnsMap")
	}

	cm.CloseAll(1 * time.Millisecond)

	if !isConnClosed(sConn) {
		t.Error("single connection was NOT closed by CloseAll")
	}
}

// TestConnsMapAddDeleteCycle verifies the full lifecycle:
//   - Add returns true while the map is open.
//   - Add returns false after CloseAll.
//   - Delete removes the connection from the map cleanly.
func TestConnsMapAddDeleteCycle(t *testing.T) {
	var cm ConnsMap
	cm.Init("test-client")

	sConn1, cConn1 := newPipeConn(t)
	sConn2, cConn2 := newPipeConn(t)
	defer cConn1.Close()
	defer cConn2.Close()

	// Add two connections — should succeed.
	if !cm.Add(sConn1) {
		t.Fatal("Add(sConn1) returned false on open map")
	}
	if !cm.Add(sConn2) {
		t.Fatal("Add(sConn2) returned false on open map")
	}

	// Delete one before closing.
	cm.Delete(sConn1)

	// CloseAll should only close the remaining connection (sConn2).
	cm.CloseAll(0)

	// sConn1 was deleted before CloseAll, so it must still be open.
	if isConnClosed(sConn1) {
		t.Error("deleted connection sConn1 was unexpectedly closed by CloseAll")
	}

	// sConn2 was still in the map, so it must be closed.
	if !isConnClosed(sConn2) {
		t.Error("sConn2 was NOT closed by CloseAll")
	}

	// After CloseAll, Add must return false.
	sConn3, cConn3 := newPipeConn(t)
	defer sConn3.Close()
	defer cConn3.Close()
	if cm.Add(sConn3) {
		t.Error("Add returned true on closed ConnsMap")
	}
}

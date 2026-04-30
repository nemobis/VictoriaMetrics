package ioutil

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

// ---------- LimitedReader pool ----------

func TestGetLimitedReaderBasic(t *testing.T) {
	data := "hello world"
	r := strings.NewReader(data)
	lr := GetLimitedReader(r, 5)
	if lr == nil {
		t.Fatal("GetLimitedReader returned nil")
	}
	defer PutLimitedReader(lr)

	buf := make([]byte, 10)
	n, err := lr.Read(buf)
	if err != nil && err != io.EOF {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != 5 {
		t.Fatalf("expected 5 bytes read, got %d", n)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("expected 'hello', got %q", string(buf[:n]))
	}
}

func TestGetLimitedReaderLimitEnforced(t *testing.T) {
	data := strings.Repeat("x", 100)
	r := strings.NewReader(data)
	lr := GetLimitedReader(r, 10)
	defer PutLimitedReader(lr)

	got, err := io.ReadAll(lr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 10 {
		t.Fatalf("expected 10 bytes, got %d", len(got))
	}
}

func TestPutLimitedReaderClearsState(t *testing.T) {
	r := strings.NewReader("data")
	lr := GetLimitedReader(r, 42)
	PutLimitedReader(lr)

	if lr.R != nil {
		t.Fatal("PutLimitedReader should set R to nil")
	}
	if lr.N != 0 {
		t.Fatalf("PutLimitedReader should set N to 0, got %d", lr.N)
	}
}

func TestGetLimitedReaderPoolReuse(t *testing.T) {
	// Exercises the pool reuse path (second Get should hit the pool).
	r1 := strings.NewReader("aaa")
	lr1 := GetLimitedReader(r1, 3)
	PutLimitedReader(lr1)

	r2 := strings.NewReader("bbb")
	lr2 := GetLimitedReader(r2, 3)
	defer PutLimitedReader(lr2)
	if lr2 == nil {
		t.Fatal("second GetLimitedReader returned nil")
	}
}

// ---------- FirstByteReader pool ----------

func TestGetFirstByteReaderBasic(t *testing.T) {
	data := "hello"
	r := strings.NewReader(data)
	fbr := GetFirstByteReader(r)
	if fbr == nil {
		t.Fatal("GetFirstByteReader returned nil")
	}
	defer PutFirstByteReader(fbr)

	got, err := io.ReadAll(fbr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != data {
		t.Fatalf("expected %q, got %q", data, string(got))
	}
}

func TestFirstByteReaderWaitForData(t *testing.T) {
	data := "test data"
	r := strings.NewReader(data)
	fbr := GetFirstByteReader(r)
	defer PutFirstByteReader(fbr)

	// WaitForData should buffer the first chunk without consuming from fbr.
	fbr.WaitForData()

	got, err := io.ReadAll(fbr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != data {
		t.Fatalf("expected %q, got %q", data, string(got))
	}
}

func TestFirstByteReaderWaitForDataIdempotent(t *testing.T) {
	// Calling WaitForData twice must still return all data exactly once.
	data := "idempotent"
	r := strings.NewReader(data)
	fbr := GetFirstByteReader(r)
	defer PutFirstByteReader(fbr)

	fbr.WaitForData()
	fbr.WaitForData() // second call must be a no-op

	got, err := io.ReadAll(fbr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != data {
		t.Fatalf("expected %q, got %q", data, string(got))
	}
}

func TestFirstByteReaderEmptyReader(t *testing.T) {
	fbr := GetFirstByteReader(strings.NewReader(""))
	defer PutFirstByteReader(fbr)

	buf := make([]byte, 4)
	n, err := fbr.Read(buf)
	if n != 0 {
		t.Fatalf("expected 0 bytes from empty reader, got %d", n)
	}
	if err != io.EOF {
		t.Fatalf("expected io.EOF, got %v", err)
	}
}

func TestFirstByteReaderPartialReadFromFirstChunk(t *testing.T) {
	// Supply data longer than the 16-byte firstChunk buffer.
	data := strings.Repeat("a", 32)
	r := strings.NewReader(data)
	fbr := GetFirstByteReader(r)
	defer PutFirstByteReader(fbr)

	fbr.WaitForData() // buffers up to 16 bytes

	got, err := io.ReadAll(fbr)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != data {
		t.Fatalf("expected %q, got %q", data, string(got))
	}
}

func TestFirstByteReaderSmallReadBuffer(t *testing.T) {
	// Read with a buffer smaller than firstChunk to exercise the partial-copy path.
	data := "abcdefghij" // 10 bytes
	r := strings.NewReader(data)
	fbr := GetFirstByteReader(r)
	defer PutFirstByteReader(fbr)

	fbr.WaitForData()

	var out bytes.Buffer
	buf := make([]byte, 3)
	for {
		n, err := fbr.Read(buf)
		out.Write(buf[:n])
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if out.String() != data {
		t.Fatalf("expected %q, got %q", data, out.String())
	}
}

func TestFirstByteReaderWaitForDataPropagatesError(t *testing.T) {
	// A reader that immediately returns an error.
	sentinel := errors.New("read error")
	r := &errorReader{err: sentinel}
	fbr := GetFirstByteReader(r)
	defer PutFirstByteReader(fbr)

	fbr.WaitForData()

	buf := make([]byte, 4)
	_, err := fbr.Read(buf)
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
}

func TestPutFirstByteReaderClearsState(t *testing.T) {
	r := strings.NewReader("data")
	fbr := GetFirstByteReader(r)
	PutFirstByteReader(fbr)

	if fbr.r != nil {
		t.Fatal("PutFirstByteReader should set r to nil")
	}
	if fbr.firstChunkLen != 0 {
		t.Fatalf("PutFirstByteReader should reset firstChunkLen, got %d", fbr.firstChunkLen)
	}
	if fbr.firstChunkRead {
		t.Fatal("PutFirstByteReader should reset firstChunkRead to false")
	}
	if fbr.firstChunkErr != nil {
		t.Fatalf("PutFirstByteReader should reset firstChunkErr, got %v", fbr.firstChunkErr)
	}
}

// errorReader is an io.Reader that always returns an error.
type errorReader struct {
	err error
}

func (er *errorReader) Read(_ []byte) (int, error) {
	return 0, er.err
}

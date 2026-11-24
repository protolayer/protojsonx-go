package jsonwire

import (
	"bytes"
	"encoding/json/jsontext"
	"strings"
	"testing"

	testpb "github.com/protolayer/jsonwire/internal/gen/google/protobuf"
)

// TestReleaseBuffer checks the buffer policy: a large buffer stays while
// it is well used, and goes after 5 poorly used calls in a row.
func TestReleaseBuffer(t *testing.T) {
	// The state does not come from the pool, so that other tests cannot use
	// it at the same time.
	state := newTestEncodeState(t)
	use := func(n int) {
		t.Helper()
		state.buf.Reset()
		state.buf.Write(bytes.Repeat([]byte{'x'}, n))
		state.release()
		state.buf.Reset()
	}
	use(1 << 20)
	large := state.buf.Cap()
	if large < 1<<20 {
		t.Fatalf("buffer capacity %d after a 1 MB output", large)
	}
	// A well-used buffer stays.
	use(1 << 19)
	if state.buf.Cap() != large {
		t.Fatal("a buffer used to a half was dropped")
	}
	// 4 poorly used calls in a row keep the buffer. The 5th drops it.
	for i := range 4 {
		use(100)
		if state.buf.Cap() != large {
			t.Fatalf("buffer dropped after %d poorly used calls", i+1)
		}
	}
	use(100)
	if state.buf.Cap() != 0 {
		t.Fatalf("buffer capacity %d after 5 poorly used calls, want 0", state.buf.Cap())
	}
	// A well-used call resets the count of poorly used calls.
	use(1 << 20)
	large = state.buf.Cap()
	for range 3 {
		use(100)
	}
	use(1 << 19)
	for i := range 4 {
		use(100)
		if state.buf.Cap() != large {
			t.Fatalf("buffer dropped after a well-used call and %d poorly used calls", i+1)
		}
	}
	use(100)
	if state.buf.Cap() != 0 {
		t.Fatalf("buffer capacity %d after 5 poorly used calls, want 0", state.buf.Cap())
	}
	// A small buffer always stays.
	for range 10 {
		use(10)
	}
	if state.buf.Cap() == 0 {
		t.Fatal("small buffer was dropped")
	}
}

// TestReleaseBufferThresholds checks the limits of the buffer policy: a
// buffer of up to 4 KB always stays, and a call that uses exactly a quarter
// of the buffer is a well-used call.
func TestReleaseBufferThresholds(t *testing.T) {
	state := newTestEncodeState(t)
	// use writes n bytes into a buffer with the capacity size, or into the
	// current buffer if size is 0, and releases it.
	use := func(size, n int) {
		t.Helper()
		if size > 0 {
			state.buf = *bytes.NewBuffer(make([]byte, 0, size))
		}
		state.buf.Reset()
		state.buf.Write(bytes.Repeat([]byte{'x'}, n))
		state.release()
	}
	use(4<<10, 1)
	for range 10 {
		use(0, 1)
	}
	if state.buf.Cap() != 4<<10 {
		t.Errorf("a 4 KB buffer was dropped: capacity %d", state.buf.Cap())
	}
	const size = 64 << 10
	use(size, size/4)
	for range 4 {
		use(0, size/4-1)
	}
	// A call that uses exactly a quarter resets the count.
	use(0, size/4)
	for range 4 {
		use(0, size/4-1)
	}
	if state.buf.Cap() != size {
		t.Fatalf("buffer dropped after a call that used a quarter: capacity %d", state.buf.Cap())
	}
	use(0, size/4-1)
	if state.buf.Cap() != 0 {
		t.Errorf("buffer capacity %d after 5 calls that used less than a quarter, want 0", state.buf.Cap())
	}
	use(4<<10+1, 1)
	for range 4 {
		use(0, 1)
	}
	if state.buf.Cap() != 0 {
		t.Errorf("a buffer above 4 KB stayed after 5 poorly used calls: capacity %d", state.buf.Cap())
	}
}

// TestReleaseEncoder checks that the encoder, which keeps a scratch
// buffer as large as its largest value, is dropped after a large value.
func TestReleaseEncoder(t *testing.T) {
	// The state does not come from the pool, so that other tests cannot use
	// it at the same time.
	state := newTestEncodeState(t)
	state.scratch.maxValue = maxPooledValue
	state.release()
	if state.enc == nil {
		t.Fatal("encoder dropped after a small value")
	}
	state.scratch.maxValue = maxPooledValue + 1
	state.release()
	if state.enc != nil {
		t.Fatal("encoder kept after a large value")
	}
	if state.scratch.maxValue != 0 {
		t.Fatal("maxValue not reset")
	}
}

func newTestEncodeState(t *testing.T) *encodeState {
	t.Helper()
	opts, err := MarshalOptions{}.encoderOptions()
	if err != nil {
		t.Fatal(err)
	}
	state := &encodeState{scratch: newEncodeScratch()}
	state.enc = jsontext.NewEncoder(&state.buf, opts)
	return state
}

// TestMaxValue checks that a large bytes value drops the encoder, and a small
// one keeps it.
func TestMaxValue(t *testing.T) {
	for _, c := range []struct {
		size int
		keep bool
	}{
		{size: 1 << 10, keep: true},
		{size: maxPooledValue, keep: false},
	} {
		state := newTestEncodeState(t)
		// A small value after the large one checks that the largest value
		// counts, not the last one.
		m := &testpb.TestAllTypesProto3{RepeatedBytes: [][]byte{make([]byte, c.size), {1}}}
		if err := (MarshalOptions{}).marshalEncode(state.enc, m, state.scratch, false); err != nil {
			t.Fatal(err)
		}
		state.release()
		if (state.enc != nil) != c.keep {
			t.Errorf("bytes value of %d bytes: encoder kept is %v, want %v", c.size, state.enc != nil, c.keep)
		}
	}
}

// TestReleaseScratch checks that a large scratch stack does not go back into
// the pool.
func TestReleaseScratch(t *testing.T) {
	state := newTestEncodeState(t)
	state.scratch.fields = make([]fieldValue, 0, maxPooledEntries+1)
	state.release()
	if cap(state.scratch.fields) > maxPooledEntries {
		t.Error("encode state kept a large scratch stack")
	}

	// Not parallel: a Get right after a Put usually returns the same value.
	large := newEncodeScratch()
	large.entries = make([]mapEntry, 0, maxPooledEntries+1)
	putEncodeScratch(large)
	if got := encodeScratchPool.Get().(*encodeScratch); got == large {
		t.Error("putEncodeScratch pooled a large scratch stack")
	}
}

// TestIndentCache checks that only short indents go into the options cache.
func TestIndentCache(t *testing.T) {
	for _, n := range []int{maxCachedIndent, maxCachedIndent + 1} {
		indent := strings.Repeat("\t", n)
		if _, err := (MarshalOptions{Indent: indent}).encoderOptions(); err != nil {
			t.Fatal(err)
		}
		_, cached := multilineOptions.Load(indent)
		if want := n <= maxCachedIndent; cached != want {
			t.Errorf("indent of %d characters: cached is %v, want %v", n, cached, want)
		}
	}
}

package jsonwire

import (
	"bytes"
	"testing"
)

func TestEncodeStatePoolLimit(t *testing.T) {
	// Not parallel: a Get right after a Put checks whether the state was pooled.
	checkDropped := func(name string, s *encodeState, ok bool) {
		t.Helper()
		putEncodeState(s, ok)
		if got := getEncodeState(); got == s {
			t.Errorf("%s: state was returned to pool", name)
		}
	}

	largeBuf := getEncodeState()
	largeBuf.buf.Grow(maxPooledBuf + 1)
	checkDropped("large buffer", largeBuf, true)

	largeFields := getEncodeState()
	largeFields.fields = make([]fieldValue, 0, maxPooledEntries+1)
	checkDropped("large fields", largeFields, true)

	largeEntries := getEncodeState()
	largeEntries.entries = make([]mapEntry, 0, maxPooledEntries+1)
	checkDropped("large entries", largeEntries, true)

	onError := getEncodeState()
	checkDropped("on error", onError, false)

	// A normal state stays poolable and clears its buffer on release.
	normal := getEncodeState()
	normal.buf.Write(bytes.Repeat([]byte{'x'}, 128))
	if !normal.poolable() {
		t.Fatal("small state is not poolable")
	}
	putEncodeState(normal, true)
	if normal.buf.Len() != 0 {
		t.Fatalf("buffer length %d after putEncodeState, want 0", normal.buf.Len())
	}
}

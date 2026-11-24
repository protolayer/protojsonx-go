// Package testutil has helpers for the protojsonx tests: a random message
// generator, the test message types, and small helpers.
package testutil

import (
	"bytes"
	"encoding/json/jsontext"
	"testing"
)

// Compact removes the white space from a JSON value. protojson adds random
// white space to its output.
func Compact(t testing.TB, b []byte) []byte {
	t.Helper()
	v := jsontext.Value(bytes.Clone(b))
	if err := v.Compact(); err != nil {
		t.Fatalf("invalid JSON %q: %v", b, err)
	}
	return v
}

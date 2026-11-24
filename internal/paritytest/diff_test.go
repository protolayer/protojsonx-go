package paritytest

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"testing"
	"testing/iotest"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/protolayer/jsonwire-go"
	"github.com/protolayer/jsonwire-go/internal/testutil"
)

// The tests in this file compare jsonwire with protojson, which is the
// reference implementation. For each input, both must return an error, or both
// must return the same result.

type marshalCase struct {
	name      string
	protojson protojson.MarshalOptions
	jsonwire  jsonwire.MarshalOptions
}

var marshalCases = []marshalCase{
	{name: "default"},
	{
		name:      "UseProtoNames",
		protojson: protojson.MarshalOptions{UseProtoNames: true},
		jsonwire:  jsonwire.MarshalOptions{UseProtoNames: true},
	},
	{
		name:      "UseEnumNumbers",
		protojson: protojson.MarshalOptions{UseEnumNumbers: true},
		jsonwire:  jsonwire.MarshalOptions{UseEnumNumbers: true},
	},
	{
		name:      "EmitUnpopulated",
		protojson: protojson.MarshalOptions{EmitUnpopulated: true},
		jsonwire:  jsonwire.MarshalOptions{EmitUnpopulated: true},
	},
	{
		name:      "EmitDefaultValues",
		protojson: protojson.MarshalOptions{EmitDefaultValues: true},
		jsonwire:  jsonwire.MarshalOptions{EmitDefaultValues: true},
	},
	{
		name:      "EmitUnpopulatedAndDefaultValues",
		protojson: protojson.MarshalOptions{EmitUnpopulated: true, EmitDefaultValues: true},
		jsonwire:  jsonwire.MarshalOptions{EmitUnpopulated: true, EmitDefaultValues: true},
	},
	{
		name:      "AllowPartial",
		protojson: protojson.MarshalOptions{AllowPartial: true},
		jsonwire:  jsonwire.MarshalOptions{AllowPartial: true},
	},
	{
		name: "AllOptions",
		protojson: protojson.MarshalOptions{
			AllowPartial: true, UseProtoNames: true, UseEnumNumbers: true, EmitUnpopulated: true,
		},
		jsonwire: jsonwire.MarshalOptions{
			AllowPartial: true, UseProtoNames: true, UseEnumNumbers: true, EmitUnpopulated: true,
		},
	},
	{
		name:      "Multiline",
		protojson: protojson.MarshalOptions{Multiline: true},
		jsonwire:  jsonwire.MarshalOptions{Multiline: true},
	},
	{
		name:      "Indent",
		protojson: protojson.MarshalOptions{Indent: "\t"},
		jsonwire:  jsonwire.MarshalOptions{Indent: "\t"},
	},
}

type unmarshalCase struct {
	name      string
	protojson protojson.UnmarshalOptions
	jsonwire  jsonwire.UnmarshalOptions
}

var unmarshalCases = []unmarshalCase{
	{name: "default"},
	{
		name:      "DiscardUnknown",
		protojson: protojson.UnmarshalOptions{DiscardUnknown: true},
		jsonwire:  jsonwire.UnmarshalOptions{DiscardUnknown: true},
	},
	{
		name:      "AllowPartial",
		protojson: protojson.UnmarshalOptions{AllowPartial: true},
		jsonwire:  jsonwire.UnmarshalOptions{AllowPartial: true},
	},
	{
		name:      "RecursionLimit",
		protojson: protojson.UnmarshalOptions{RecursionLimit: 3, DiscardUnknown: true},
		jsonwire:  jsonwire.UnmarshalOptions{RecursionLimit: 3, DiscardUnknown: true},
	},
}

// checkMarshal compares the output of protojson and jsonwire for m. It
// returns the protojson output, or nil if both return an error.
func checkMarshal(t testing.TB, m proto.Message, c marshalCase) []byte {
	t.Helper()
	want, wantErr := c.protojson.Marshal(m)
	got, gotErr := c.jsonwire.Marshal(m)
	name := m.ProtoReflect().Descriptor().FullName()
	if (wantErr != nil) != (gotErr != nil) {
		t.Fatalf("%s: error mismatch for %v\nprotojson: %v\njsonwire:  %v\nprotojson output: %s\njsonwire output:  %s",
			c.name, name, wantErr, gotErr, want, got)
	}
	if gotErr != nil && !errors.Is(gotErr, proto.Error) {
		t.Fatalf("%s: error does not match proto.Error: %v", c.name, gotErr)
	}
	if wantErr == nil && !bytes.Equal(testutil.Compact(t, want), testutil.Compact(t, got)) {
		t.Fatalf("%s: output mismatch for %v\nprotojson: %s\njsonwire:  %s",
			c.name, name, testutil.Compact(t, want), testutil.Compact(t, got))
	}
	if c.jsonwire.Multiline || c.jsonwire.Indent != "" {
		if wantErr != nil {
			return nil
		}
		return want
	}
	if gotErr == nil && !bytes.Equal(testutil.Compact(t, got), got) {
		t.Fatalf("%s: default output is not compact: %s", c.name, got)
	}
	// The output is deterministic. Go visits map entries in a random order on
	// each Range, so a second call checks the sorting of map entries.
	// proto.Clone does not work for this check, because it drops -0 in
	// proto3 float fields.
	if gotErr == nil {
		again, err := c.jsonwire.Marshal(m)
		if err != nil || !bytes.Equal(again, got) {
			t.Fatalf("%s: output is not deterministic for %v\nfirst:  %s\nsecond: %s (%v)", c.name, name, got, again, err)
		}
	}
	// MarshalEncode must give the same result as Marshal with the default
	// jsontext options, and with the lenient options of encoding/json v1.
	// Strings must be valid UTF-8 in both cases.
	for _, opts := range []struct {
		name string
		opts []jsontext.Options
	}{
		{"default encoder", nil},
		{"lenient encoder", []jsontext.Options{jsontext.AllowInvalidUTF8(true), jsontext.AllowDuplicateNames(true)}},
	} {
		var buf bytes.Buffer
		err := c.jsonwire.MarshalEncode(jsontext.NewEncoder(&buf, opts.opts...), m)
		if (err != nil) != (gotErr != nil) {
			t.Fatalf("%s: MarshalEncode with %s: error mismatch for %v\nMarshal:       %v\nMarshalEncode: %v",
				c.name, opts.name, name, gotErr, err)
		}
		if err == nil && !bytes.Equal(bytes.TrimSuffix(buf.Bytes(), []byte("\n")), got) {
			t.Fatalf("%s: MarshalEncode with %s: output mismatch for %v\nMarshal:       %s\nMarshalEncode: %s",
				c.name, opts.name, name, got, buf.Bytes())
		}
	}
	if wantErr != nil {
		return nil
	}
	return want
}

// isValidJSON reports whether data is one valid JSON value. Duplicate object
// names are allowed, the same as in protojson.
func isValidJSON(data []byte) bool {
	return jsontext.Value(data).IsValid(jsontext.AllowDuplicateNames(true))
}

// checkUnmarshal compares the result of protojson and jsonwire for data.
func checkUnmarshal(t testing.TB, data []byte, newMessage func() proto.Message, c unmarshalCase) {
	t.Helper()
	checkUnmarshalWith(t, data, newMessage, c, true)
}

// checkUnmarshalWith is checkUnmarshal. If variants is false, it does not
// check the other ways to read data, which is faster.
func checkUnmarshalWith(t testing.TB, data []byte, newMessage func() proto.Message, c unmarshalCase, variants bool) {
	t.Helper()
	want := newMessage()
	wantErr := c.protojson.Unmarshal(data, want)
	got := newMessage()
	gotErr := c.jsonwire.Unmarshal(data, got)
	name := want.ProtoReflect().Descriptor().FullName()
	if gotErr != nil && !errors.Is(gotErr, proto.Error) {
		t.Fatalf("%s: error does not match proto.Error for input %q: %v", c.name, data, gotErr)
	}
	if variants {
		checkUnmarshalVariants(t, data, newMessage, c, got, gotErr)
	}
	if !isValidJSON(data) {
		// protojson accepts some input that is not valid JSON, for example
		// the number 0E. jsonwire follows RFC 8259 and must reject it.
		if gotErr == nil {
			t.Fatalf("%s: jsonwire accepted invalid JSON for %v: %q", c.name, name, data)
		}
		return
	}
	if (wantErr != nil) != (gotErr != nil) {
		t.Fatalf("%s: error mismatch for %v with input %q\nprotojson: %v\njsonwire:  %v",
			c.name, name, data, wantErr, gotErr)
	}
	if wantErr != nil {
		return
	}
	if !proto.Equal(want, got) {
		t.Fatalf("%s: result mismatch for %v with input %q\nprotojson: %v\njsonwire:  %v",
			c.name, name, data, want, got)
	}
}

// checkUnmarshalVariants checks other ways to read data against the result of
// Unmarshal (got and gotErr):
//
//   - The input is overwritten after Unmarshal returns. Unmarshal parses the
//     input in place, so a view into the input that stays in the message
//     would change.
//   - UnmarshalDecode with the lenient options of encoding/json v1. Strings
//     must be valid UTF-8 even then, so the result must be the same.
func checkUnmarshalVariants(t testing.TB, data []byte, newMessage func() proto.Message, c unmarshalCase, got proto.Message, gotErr error) {
	t.Helper()
	name := got.ProtoReflect().Descriptor().FullName()

	input := bytes.Clone(data)
	scribbled := newMessage()
	err := c.jsonwire.Unmarshal(input, scribbled)
	for i := range input {
		input[i] = 'x'
	}
	if (err != nil) != (gotErr != nil) || (err == nil && !proto.Equal(got, scribbled)) {
		t.Fatalf("%s: message changed after its input was overwritten for %v with input %q\nbefore: %v\nafter:  %v",
			c.name, name, data, got, scribbled)
	}

	// The lenient options of encoding/json v1 must give the same result.
	lenient, err := unmarshalDecodeAll(c, data, newMessage, jsontext.AllowInvalidUTF8(true), jsontext.AllowDuplicateNames(true))
	if (err != nil) != (gotErr != nil) {
		t.Fatalf("%s: lenient decoder: error mismatch for %v with input %q\nUnmarshal:       %v\nUnmarshalDecode: %v",
			c.name, name, data, gotErr, err)
	}
	if err == nil && !proto.Equal(got, lenient) {
		t.Fatalf("%s: lenient decoder: result mismatch for %v with input %q\nUnmarshal:       %v\nUnmarshalDecode: %v",
			c.name, name, data, got, lenient)
	}

	// The default options of encoding/json v2 reject duplicate object names.
	// Otherwise, they must give the same result.
	strict, err := unmarshalDecodeAll(c, data, newMessage)
	if !jsontext.Value(data).IsValid() {
		if err == nil {
			t.Fatalf("%s: default decoder accepted input with duplicate names for %v: %q", c.name, name, data)
		}
		return
	}
	if (err != nil) != (gotErr != nil) {
		t.Fatalf("%s: default decoder: error mismatch for %v with input %q\nUnmarshal:       %v\nUnmarshalDecode: %v",
			c.name, name, data, gotErr, err)
	}
	if err == nil && !proto.Equal(got, strict) {
		t.Fatalf("%s: default decoder: result mismatch for %v with input %q\nUnmarshal:       %v\nUnmarshalDecode: %v",
			c.name, name, data, got, strict)
	}
}

// unmarshalDecodeAll reads data with UnmarshalDecode and a decoder with the
// given options, and checks that nothing follows the value.
func unmarshalDecodeAll(c unmarshalCase, data []byte, newMessage func() proto.Message, opts ...jsontext.Options) (proto.Message, error) {
	m := newMessage()
	dec := jsontext.NewDecoder(bytes.NewReader(data), opts...)
	if err := c.jsonwire.UnmarshalDecode(dec, m); err != nil {
		return m, err
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return m, errors.New("unexpected data after top-level value")
	}
	return m, nil
}

// checkUnmarshalRead checks that UnmarshalRead gives the same result as
// Unmarshal when it reads the input one byte at a time. The decoder then
// refills its buffer often, which finds any use of decoder memory after the
// next read. It is slow, so the tests use it for a subset of the inputs.
func checkUnmarshalRead(t testing.TB, data []byte, newMessage func() proto.Message, c unmarshalCase) {
	t.Helper()
	got := newMessage()
	gotErr := c.jsonwire.Unmarshal(data, got)
	slow := newMessage()
	slowErr := c.jsonwire.UnmarshalRead(iotest.OneByteReader(bytes.NewReader(data)), slow)
	name := got.ProtoReflect().Descriptor().FullName()
	if (gotErr != nil) != (slowErr != nil) {
		t.Fatalf("%s: error mismatch for %v with input %q\nUnmarshal:     %v\nUnmarshalRead: %v",
			c.name, name, data, gotErr, slowErr)
	}
	if gotErr == nil && !proto.Equal(got, slow) {
		t.Fatalf("%s: UnmarshalRead mismatch for %v with input %q\nUnmarshal:     %v\nUnmarshalRead: %v",
			c.name, name, data, got, slow)
	}
}

// testIterations returns the number of random messages for each message type.
// The race detector makes the tests much slower, so it uses fewer.
func testIterations() int {
	// The race detector finds data races, not differences from protojson,
	// and it is about 10 times slower. TestConcurrentUse covers the shared
	// state, so a few messages are enough.
	if testutil.RaceEnabled {
		return 10
	}
	if testing.Short() {
		return 50
	}
	// The fuzz targets search further. This number keeps the test fast.
	return 200
}

func TestDifferentialMarshal(t *testing.T) {
	t.Parallel()
	iterations := testIterations()
	for i, newMessage := range testutil.Messages {
		t.Run(fmt.Sprint(newMessage().ProtoReflect().Descriptor().FullName()), func(t *testing.T) {
			t.Parallel()
			g := testutil.NewGenerator(uint64(i + 1))
			var ok, failed, size int
			for j := range iterations {
				// Most messages are valid. Every fourth message can contain
				// values that cannot be marshaled.
				g.Invalid = 0
				if j%4 == 0 {
					g.Invalid = 0.02
				}
				m := newMessage()
				g.Message(m.ProtoReflect(), 0)
				for _, c := range marshalCases {
					out := checkMarshal(t, m, c)
					if out == nil {
						failed++
						continue
					}
					ok++
					size += len(out)
					// Multiline output has the same content as compact
					// output, so it does not need its own decode checks.
					if c.jsonwire.Multiline || c.jsonwire.Indent != "" {
						continue
					}
					// The output of each marshal option must also unmarshal
					// to the same result. The other ways to read the data
					// are checked once, with the default options.
					for k, u := range unmarshalCases {
						checkUnmarshalWith(t, out, newMessage, u, k == 0)
					}
					// The one-byte reader check is slow. Use it for the first
					// messages only.
					if j < 25 && c.name == "default" {
						checkUnmarshalRead(t, out, newMessage, unmarshalCases[0])
					}
				}
			}
			// The generator must make both valid and invalid messages, or the
			// test does not check much.
			t.Logf("%d marshaled (%d bytes), %d errors", ok, size, failed)
			// With few iterations, by chance no invalid value can appear.
			if ok < iterations || (failed == 0 && iterations >= 50) {
				t.Errorf("generator is too narrow: %d marshaled, %d errors", ok, failed)
			}
		})
	}
}

// TestDifferentialRoundTrip checks that jsonwire reads its own output back to
// the same message.
func TestDifferentialRoundTrip(t *testing.T) {
	t.Parallel()
	iterations := testIterations()
	for i, newMessage := range testutil.Messages {
		t.Run(fmt.Sprint(newMessage().ProtoReflect().Descriptor().FullName()), func(t *testing.T) {
			t.Parallel()
			g := testutil.NewGenerator(uint64(i + 100))
			g.Invalid = 0
			for range iterations {
				m := newMessage()
				g.Message(m.ProtoReflect(), 0)
				b, err := jsonwire.MarshalOptions{AllowPartial: true}.Marshal(m)
				if err != nil {
					// Some random values cannot round trip, for example an
					// unknown number in a closed enum.
					continue
				}
				got := newMessage()
				if err := (jsonwire.UnmarshalOptions{AllowPartial: true}).Unmarshal(b, got); err != nil {
					t.Fatalf("unmarshal own output %s: %v", b, err)
				}
				// protojson is the reference for the round trip result. Some
				// values change, for example NaN in a google.protobuf.Value
				// becomes a string.
				want := newMessage()
				if err := (protojson.UnmarshalOptions{AllowPartial: true}).Unmarshal(b, want); err != nil {
					t.Fatalf("protojson cannot read jsonwire output %s: %v", b, err)
				}
				if !proto.Equal(want, got) {
					t.Fatalf("round trip mismatch for %s\nprotojson: %v\njsonwire:  %v", b, want, got)
				}
			}
		})
	}
}

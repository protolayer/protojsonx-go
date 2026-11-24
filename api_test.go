package jsonwire_test

import (
	"bytes"
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/protolayer/jsonwire"
	testpb "github.com/protolayer/jsonwire/internal/gen/google/protobuf"
	editionspb "github.com/protolayer/jsonwire/internal/gen/jsonwire/test"
	"github.com/protolayer/jsonwire/internal/testutil"
)

func newTestMessage() *testpb.TestAllTypesProto3 {
	return &testpb.TestAllTypesProto3{
		OptionalInt32:      42,
		OptionalInt64:      -7,
		OptionalString:     "hello",
		OptionalBytes:      []byte{1, 2, 3},
		OptionalNestedEnum: testpb.TestAllTypesProto3_BAR,
		RepeatedDouble:     []float64{1.5, -0.25},
		MapStringString:    map[string]string{"b": "2", "a": "1"},
		OneofField:         &testpb.TestAllTypesProto3_OneofString{OneofString: "choice"},
	}
}

func TestMarshalMatchesProtojson(t *testing.T) {
	t.Parallel()
	m := newTestMessage()
	want, err := protojson.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jsonwire.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(testutil.Compact(t, want), got) {
		t.Fatalf("got %s, want %s", got, testutil.Compact(t, want))
	}
}

func TestMarshalNil(t *testing.T) {
	t.Parallel()
	got, err := jsonwire.Marshal(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Fatalf("got %s, want {}", got)
	}
	got, err = jsonwire.Marshal((*testpb.TestAllTypesProto3)(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "{}" {
		t.Fatalf("got %s for typed nil, want {}", got)
	}
}

func TestMarshalWrite(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := jsonwire.MarshalWrite(&buf, newTestMessage()); err != nil {
		t.Fatal(err)
	}
	want, err := jsonwire.Marshal(newTestMessage())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got %q, want %q (no trailing newline)", buf.Bytes(), want)
	}

	// Errors before writing leave the writer untouched.
	buf.Reset()
	if err := (jsonwire.MarshalOptions{Indent: "x"}).MarshalWrite(&buf, newTestMessage()); err == nil || buf.Len() != 0 {
		t.Fatalf("invalid indent: err=%v, wrote %q", err, buf.String())
	}
	if err := jsonwire.MarshalWrite(&buf, new(editionspb.Editions)); err == nil || buf.Len() != 0 {
		t.Fatalf("missing required fields: err=%v, wrote %q", err, buf.String())
	}

	// Writer errors are returned directly.
	writeErr := errors.New("write failed")
	if err := jsonwire.MarshalWrite(failingWriter{err: writeErr}, newTestMessage()); !errors.Is(err, writeErr) {
		t.Fatalf("failing writer: got %v, want %v", err, writeErr)
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestMarshalIndent(t *testing.T) {
	t.Parallel()
	got, err := jsonwire.MarshalOptions{Indent: "\t"}.Marshal(newTestMessage())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "\n\t\"optionalInt32\": 42") {
		t.Fatalf("output is not indented with tabs:\n%s", got)
	}
	got, err = jsonwire.MarshalOptions{Multiline: true}.Marshal(newTestMessage())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "\n  \"optionalInt32\": 42") {
		t.Fatalf("output is not indented with two spaces:\n%s", got)
	}
	_, err = jsonwire.MarshalOptions{Indent: "x"}.Marshal(newTestMessage())
	if err == nil {
		t.Fatal("want error for invalid indent")
	}
}

// TestMarshalEncodeStream writes several messages as elements of one JSON
// array, and reads them back one at a time.
func TestMarshalEncodeStream(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := enc.WriteToken(jsontext.BeginArray); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		m := newTestMessage()
		m.OptionalInt32 = int32(i)
		if err := jsonwire.MarshalEncode(enc, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := enc.WriteToken(jsontext.EndArray); err != nil {
		t.Fatal(err)
	}

	dec := jsontext.NewDecoder(&buf)
	if _, err := dec.ReadToken(); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		var m testpb.TestAllTypesProto3
		if err := jsonwire.UnmarshalDecode(dec, &m); err != nil {
			t.Fatal(err)
		}
		want := newTestMessage()
		want.OptionalInt32 = int32(i)
		if !proto.Equal(&m, want) {
			t.Fatalf("message %d: got %v, want %v", i, &m, want)
		}
	}
	if tok, err := dec.ReadToken(); err != nil || tok.Kind() != ']' {
		t.Fatalf("got %v, %v, want end of array", tok, err)
	}
}

func TestMarshalEncodeUsesEncoderOptions(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf, jsontext.Multiline(true))
	if err := jsonwire.MarshalEncode(enc, newTestMessage()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\n") || strings.Count(buf.String(), "\n") < 5 {
		t.Fatalf("encoder options are not used:\n%s", buf.String())
	}
}

func TestMarshalRequiredFields(t *testing.T) {
	t.Parallel()
	m := new(editionspb.Editions)
	if _, err := jsonwire.Marshal(m); err == nil {
		t.Fatal("want error for missing required fields")
	}
	if _, err := (jsonwire.MarshalOptions{AllowPartial: true}).Marshal(m); err != nil {
		t.Fatalf("AllowPartial: %v", err)
	}
	// MarshalEncode checks required fields before it writes, so that enc
	// does not contain a partial value.
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := jsonwire.MarshalEncode(enc, m); err == nil {
		t.Fatal("want error for missing required fields")
	}
	if buf.Len() != 0 || enc.OutputOffset() != 0 {
		t.Fatalf("encoder has output after error: %q", buf.String())
	}
}

func TestUnmarshalTrailingData(t *testing.T) {
	t.Parallel()
	inputs := []string{`{} {}`, `{}x`, `{}]`, `{"optionalInt32":1} 1`}
	for _, input := range inputs {
		var m testpb.TestAllTypesProto3
		if err := jsonwire.Unmarshal([]byte(input), &m); err == nil {
			t.Errorf("Unmarshal(%q): want error", input)
		}
		if err := jsonwire.UnmarshalRead(strings.NewReader(input), &m); err == nil {
			t.Errorf("UnmarshalRead(%q): want error", input)
		}
	}
	// UnmarshalDecode reads only one value.
	dec := jsontext.NewDecoder(strings.NewReader(`{"optionalInt32":1} {"optionalInt32":2}`))
	for want := int32(1); want <= 2; want++ {
		var m testpb.TestAllTypesProto3
		if err := jsonwire.UnmarshalDecode(dec, &m); err != nil {
			t.Fatal(err)
		}
		if m.GetOptionalInt32() != want {
			t.Fatalf("got %d, want %d", m.GetOptionalInt32(), want)
		}
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		t.Fatalf("got %v, want EOF", err)
	}
}

func TestUnmarshalResetsMessage(t *testing.T) {
	t.Parallel()
	m := newTestMessage()
	if err := jsonwire.Unmarshal([]byte(`{"optionalBool":true}`), m); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(m, &testpb.TestAllTypesProto3{OptionalBool: true}) {
		t.Fatalf("message is not reset: %v", m)
	}
}

// TestUTF8IsAlwaysChecked checks that strings in a message must be valid
// UTF-8, even if the jsontext Encoder or Decoder allows invalid UTF-8. This is
// the case for encoding/json v1.
func TestUTF8IsAlwaysChecked(t *testing.T) {
	t.Parallel()
	lenient := jsontext.AllowInvalidUTF8(true)

	for _, m := range []*testpb.TestAllTypesProto3{
		{OptionalString: "bad \xff"},
		{MapStringString: map[string]string{"bad \xff": "x"}},
		{RepeatedString: []string{"ok", "\xed\xa0\x80"}},
	} {
		var buf bytes.Buffer
		if err := jsonwire.MarshalEncode(jsontext.NewEncoder(&buf, lenient), m); err == nil {
			t.Errorf("MarshalEncode(%v) with lenient encoder: want error, got %q", m, buf.String())
		}
		if _, err := jsonwire.Marshal(m); err == nil {
			t.Errorf("Marshal(%v): want error", m)
		}
	}

	for _, input := range []string{
		"{\"optionalString\":\"bad \xff\"}",
		"{\"mapStringString\":{\"bad \xff\":\"x\"}}",
		"{\"optional\xffString\":\"x\"}",
		"{\"optionalValue\":\"bad \xff\"}",
		"{\"optionalAny\":{\"@type\":\"type.googleapis.com/google.protobuf.StringValue\",\"value\":\"\xff\"}}",
	} {
		var m testpb.TestAllTypesProto3
		dec := jsontext.NewDecoder(strings.NewReader(input), lenient)
		if err := jsonwire.UnmarshalDecode(dec, &m); err == nil {
			t.Errorf("UnmarshalDecode(%q) with lenient decoder: want error, got %v", input, &m)
		}
		if err := jsonwire.Unmarshal([]byte(input), &m); err == nil {
			t.Errorf("Unmarshal(%q): want error", input)
		}
	}

	// Invalid UTF-8 in a skipped unknown field is also rejected under a lenient decoder.
	discardOpts := jsonwire.UnmarshalOptions{DiscardUnknown: true}
	for _, input := range []string{
		"{\"unknown\":\"bad \xff\"}",
		"{\"unknown\":[\"ok\",{\"nested\":\"\xff\"}]}",
	} {
		var m testpb.TestAllTypesProto3
		dec := jsontext.NewDecoder(strings.NewReader(input), lenient)
		if err := discardOpts.UnmarshalDecode(dec, &m); err == nil {
			t.Errorf("DiscardUnknown UnmarshalDecode(%q) with lenient decoder: want error", input)
		}
	}

	// Legitimate U+FFFD replacement characters remain valid under a lenient
	// encoder or decoder.
	const replacement = "\ufffd"
	validMsg := &testpb.TestAllTypesProto3{OptionalString: replacement}
	var buf bytes.Buffer
	if err := jsonwire.MarshalEncode(jsontext.NewEncoder(&buf, lenient), validMsg); err != nil {
		t.Fatalf("MarshalEncode(U+FFFD) with lenient encoder: %v", err)
	}
	for _, input := range []string{
		`{"optionalString":"\ufffd"}`,
		"{\"optionalString\":\"" + replacement + "\"}",
	} {
		var m testpb.TestAllTypesProto3
		dec := jsontext.NewDecoder(strings.NewReader(input), lenient)
		if err := jsonwire.UnmarshalDecode(dec, &m); err != nil {
			t.Fatalf("UnmarshalDecode(%q) with lenient decoder: %v", input, err)
		}
		if m.OptionalString != replacement {
			t.Fatalf("UnmarshalDecode(%q): got %q, want %q", input, m.OptionalString, replacement)
		}
	}
}

func TestRecursionLimit(t *testing.T) {
	t.Parallel()
	nested := func(depth int) []byte {
		return []byte(strings.Repeat(`{"recursiveMessage":`, depth) + "{}" + strings.Repeat("}", depth))
	}
	var m testpb.TestAllTypesProto3
	if err := (jsonwire.UnmarshalOptions{RecursionLimit: 5}).Unmarshal(nested(4), &m); err != nil {
		t.Fatalf("depth 4 with limit 5: %v", err)
	}
	if err := (jsonwire.UnmarshalOptions{RecursionLimit: 5}).Unmarshal(nested(5), &m); err == nil {
		t.Fatal("depth 5 with limit 5: want error")
	}
	if err := jsonwire.Unmarshal(nested(9000), &m); err != nil {
		t.Fatalf("depth 9000 with default limit: %v", err)
	}
}

// TestNestingLimit checks the differences that docs/compatibility.md gives for
// the 10000-level JSON nesting limit of jsontext.
func TestNestingLimit(t *testing.T) {
	t.Parallel()
	// Each cycle adds 2 message levels and 3 JSON levels (an object, an array,
	// and an object). 3334 cycles are 6669 message levels and 10003 JSON
	// levels.
	const cycles = 3334
	input := []byte(strings.Repeat(`{"repeatedNestedMessage":[{"corecursive":`, cycles) +
		"{}" + strings.Repeat("}]}", cycles))
	if err := protojson.Unmarshal(input, new(testpb.TestAllTypesProto3)); err != nil {
		t.Fatalf("protojson: %v", err)
	}
	if err := jsonwire.Unmarshal(input, new(testpb.TestAllTypesProto3)); err == nil {
		t.Error("Unmarshal through a list: want error at 10003 JSON levels")
	}
	var throughList testpb.TestAllTypesProto3
	if err := protojson.Unmarshal(input, &throughList); err != nil {
		t.Fatal(err)
	}
	if _, err := protojson.Marshal(&throughList); err != nil {
		t.Fatalf("protojson: %v", err)
	}
	if _, err := jsonwire.Marshal(&throughList); err == nil {
		t.Error("Marshal through a list: want error at 10003 JSON levels")
	}

	nested := func(depth int) *testpb.TestAllTypesProto3 {
		root := new(testpb.TestAllTypesProto3)
		for range depth - 1 {
			root = &testpb.TestAllTypesProto3{RecursiveMessage: root}
		}
		return root
	}
	if _, err := jsonwire.Marshal(nested(10000)); err != nil {
		t.Errorf("Marshal of 10000 levels: %v", err)
	}
	if _, err := protojson.Marshal(nested(10001)); err != nil {
		t.Fatalf("protojson: %v", err)
	}
	if _, err := jsonwire.Marshal(nested(10001)); err == nil {
		t.Error("Marshal of 10001 levels: want error")
	}
}

// streamMessage connects a message to encoding/json with the json/v2
// streaming interfaces, the same way generated code would.
type streamMessage struct {
	msg *testpb.TestAllTypesProto3
}

var streamCalls atomic.Int64

func (s *streamMessage) MarshalJSONTo(enc *jsontext.Encoder) error {
	streamCalls.Add(1)
	return jsonwire.MarshalEncode(enc, s.msg)
}

func (s *streamMessage) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	streamCalls.Add(1)
	s.msg = new(testpb.TestAllTypesProto3)
	return jsonwire.UnmarshalDecode(dec, s.msg)
}

type container struct {
	ID       string           `json:"id"`
	Message  *streamMessage   `json:"message"`
	Messages []*streamMessage `json:"messages"`
}

// TestEncodingJSON checks that json v1 and v2 call the streaming methods and
// that the result is the same as protojson.
func TestEncodingJSON(t *testing.T) {
	// Not parallel: the test counts calls of the streaming methods.
	want, err := protojson.Marshal(newTestMessage())
	if err != nil {
		t.Fatal(err)
	}
	wantJSON := `{"id":"x","message":` + string(testutil.Compact(t, want)) + `,"messages":[` +
		string(testutil.Compact(t, want)) + `]}`

	in := container{
		ID:       "x",
		Message:  &streamMessage{msg: newTestMessage()},
		Messages: []*streamMessage{{msg: newTestMessage()}},
	}
	for _, api := range []struct {
		name      string
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{
		{"v1", jsonv1.Marshal, jsonv1.Unmarshal},
		{
			"v2",
			func(v any) ([]byte, error) { return jsonv2.Marshal(v) },
			func(b []byte, v any) error { return jsonv2.Unmarshal(b, v) },
		},
	} {
		streamCalls.Store(0)
		got, err := api.marshal(in)
		if err != nil {
			t.Fatalf("%s marshal: %v", api.name, err)
		}
		if string(got) != wantJSON {
			t.Fatalf("%s marshal:\ngot  %s\nwant %s", api.name, got, wantJSON)
		}
		var out container
		if err := api.unmarshal(got, &out); err != nil {
			t.Fatalf("%s unmarshal: %v", api.name, err)
		}
		if !proto.Equal(out.Message.msg, newTestMessage()) || !proto.Equal(out.Messages[0].msg, newTestMessage()) {
			t.Fatalf("%s unmarshal: got %v", api.name, out.Message.msg)
		}
		if n := streamCalls.Load(); n != 4 {
			t.Fatalf("%s: streaming methods were called %d times, want 4", api.name, n)
		}

		// JSON null clears an existing pointer field and round-trips nil pointers.
		nullIn := container{ID: "empty"}
		nullJSON, err := api.marshal(nullIn)
		if err != nil {
			t.Fatalf("%s marshal nil field: %v", api.name, err)
		}
		pop := container{
			ID:       "x",
			Message:  &streamMessage{msg: newTestMessage()},
			Messages: []*streamMessage{{msg: newTestMessage()}},
		}
		if err := api.unmarshal(nullJSON, &pop); err != nil {
			t.Fatalf("%s unmarshal null field: %v", api.name, err)
		}
		if pop.Message != nil || len(pop.Messages) != 0 {
			t.Fatalf("%s unmarshal null field: got Message=%v, Messages=%v", api.name, pop.Message, pop.Messages)
		}
		pop = container{
			ID:       "x",
			Message:  &streamMessage{msg: newTestMessage()},
			Messages: []*streamMessage{{msg: newTestMessage()}},
		}
		if err := api.unmarshal([]byte(`{"id":"empty","message":null,"messages":[null]}`), &pop); err != nil {
			t.Fatalf("%s unmarshal explicit null: %v", api.name, err)
		}
		if pop.Message != nil || len(pop.Messages) != 1 || pop.Messages[0] != nil {
			t.Fatalf("%s unmarshal explicit null: got Message=%v, Messages=%v", api.name, pop.Message, pop.Messages)
		}
	}
}

// retainingResolver keeps every name that it is asked for, the way a caching
// resolver could.
type retainingResolver struct {
	*protoregistry.Types
	names []protoreflect.FullName
}

func (r *retainingResolver) FindExtensionByName(name protoreflect.FullName) (protoreflect.ExtensionType, error) {
	r.names = append(r.names, name)
	return nil, protoregistry.NotFound
}

// TestResolverGetsOwnedNames checks that a Resolver gets names that do not
// refer to decoder memory, which changes on the next read.
func TestResolverGetsOwnedNames(t *testing.T) {
	t.Parallel()
	input := `{"[a.b.c]":1,` +
		strings.Repeat(`"unknown":"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz",`, 1000) +
		`"optionalInt32":1}`
	for name, unmarshal := range map[string]func(jsonwire.UnmarshalOptions, proto.Message) error{
		"Unmarshal": func(o jsonwire.UnmarshalOptions, m proto.Message) error {
			b := []byte(input)
			err := o.Unmarshal(b, m)
			for i := range b {
				b[i] = 'x'
			}
			return err
		},
		"UnmarshalRead": func(o jsonwire.UnmarshalOptions, m proto.Message) error {
			return o.UnmarshalRead(iotest.OneByteReader(strings.NewReader(input)), m)
		},
	} {
		r := &retainingResolver{Types: new(protoregistry.Types)}
		opts := jsonwire.UnmarshalOptions{DiscardUnknown: true, AllowPartial: true, Resolver: r}
		if err := unmarshal(opts, new(editionspb.Editions)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(r.names) != 1 || r.names[0] != "a.b.c" {
			t.Fatalf("%s: resolver has %q, want [a.b.c]", name, r.names)
		}
	}
}

func TestUnmarshalEmptyInput(t *testing.T) {
	t.Parallel()
	for _, input := range []string{"", " ", "\n\t"} {
		var m testpb.TestAllTypesProto3
		err := jsonwire.Unmarshal([]byte(input), &m)
		if !errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			t.Errorf("Unmarshal(%q): got %v, want io.ErrUnexpectedEOF", input, err)
		}
		err = jsonwire.UnmarshalRead(strings.NewReader(input), &m)
		if !errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
			t.Errorf("UnmarshalRead(%q): got %v, want io.ErrUnexpectedEOF", input, err)
		}
		// At the end of a stream, UnmarshalDecode returns io.EOF, the same
		// as json.UnmarshalDecode.
		err = jsonwire.UnmarshalDecode(jsontext.NewDecoder(strings.NewReader(input)), &m)
		if !errors.Is(err, io.EOF) {
			t.Errorf("UnmarshalDecode(%q): got %v, want io.EOF", input, err)
		}
	}
}

// TestNestedAnyMemory checks that nested Any values do not copy their input
// at each level. Each level of Any holds the binary form of all inner levels,
// so the memory grows with the square of the depth in both packages. jsonwire
// must not use more than protojson. Before the fix, it used about 3 times more.
func TestNestedAnyMemory(t *testing.T) {
	if testutil.RaceEnabled {
		t.Skip("allocation counts are not stable with the race detector")
	}
	const depth = 1000
	input := []byte(`{"optionalAny":` +
		strings.Repeat(`{"@type":"type.googleapis.com/google.protobuf.Any","value":`, depth) +
		`{}` + strings.Repeat(`}`, depth) + `}`)
	allocated := func(unmarshal func([]byte, proto.Message) error) int64 {
		return testing.Benchmark(func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := unmarshal(input, new(testpb.TestAllTypesProto3)); err != nil {
					b.Fatal(err)
				}
			}
		}).AllocedBytesPerOp()
	}
	got := allocated(jsonwire.Unmarshal)
	want := allocated(protojson.Unmarshal)
	t.Logf("nested Any depth %d (%d KB input): jsonwire %d MB, protojson %d MB",
		depth, len(input)>>10, got>>20, want>>20)
	if got > want {
		t.Fatalf("jsonwire allocated %d MB, more than protojson (%d MB)", got>>20, want>>20)
	}
}

// TestEncodingJSONEscaping checks that json v1 escapes jsonwire output the
// same way as the output of MarshalJSON with protojson. v1 escapes <, >, &,
// U+2028, and U+2029.
func TestEncodingJSONEscaping(t *testing.T) {
	t.Parallel()
	m := &testpb.TestAllTypesProto3{
		OptionalString:  "<script>alert('x' & \"y\")</script>\u2028\u2029\x01",
		MapStringString: map[string]string{"<k>&": "\u2028"},
		OptionalBytes:   []byte("<>&"),
	}
	type oldContainer struct {
		Message *protojsonMessage `json:"message"`
	}
	type newContainer struct {
		Message *streamMessage `json:"message"`
	}
	want, err := jsonv1.Marshal(oldContainer{Message: &protojsonMessage{msg: m}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := jsonv1.Marshal(newContainer{Message: &streamMessage{msg: m}})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("v1 escaping differs:\nMarshalJSON:   %s\nMarshalJSONTo: %s", want, got)
	}
	var out newContainer
	if err := jsonv1.Unmarshal(got, &out); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(out.Message.msg, m) {
		t.Fatalf("round trip: got %v, want %v", out.Message.msg, m)
	}
}

// TestConcurrentUse runs Marshal and Unmarshal from many goroutines, which
// share the pooled encoder and decoder state. Run it with -race.
func TestConcurrentUse(t *testing.T) {
	t.Parallel()
	g := testutil.NewGenerator(99)
	g.Invalid = 0
	var messages, decoded []*testpb.TestAllTypesProto3
	var inputs [][]byte
	for range 20 {
		m := new(testpb.TestAllTypesProto3)
		g.Message(m.ProtoReflect(), 1)
		b, err := protojson.Marshal(m)
		if err != nil {
			continue
		}
		// Some values do not round trip, for example an unknown number of a
		// closed enum. protojson gives the expected result.
		want := new(testpb.TestAllTypesProto3)
		if err := protojson.Unmarshal(b, want); err != nil {
			t.Fatal(err)
		}
		messages = append(messages, m)
		inputs = append(inputs, testutil.Compact(t, b))
		decoded = append(decoded, want)
	}
	var wg sync.WaitGroup
	for w := range 8 {
		wg.Go(func() {
			for i := range 50 {
				k := (w + i) % len(messages)
				got, err := jsonwire.Marshal(messages[k])
				if err != nil || !bytes.Equal(got, inputs[k]) {
					t.Errorf("Marshal %d: %v\ngot  %s\nwant %s", k, err, got, inputs[k])
					return
				}
				var m testpb.TestAllTypesProto3
				if err := jsonwire.Unmarshal(inputs[k], &m); err != nil || !proto.Equal(&m, decoded[k]) {
					t.Errorf("Unmarshal %d: %v", k, err)
					return
				}
			}
		})
	}
	wg.Wait()
}

func TestMarshalAppendAndFormat(t *testing.T) {
	t.Parallel()
	m := newTestMessage()
	want, err := jsonwire.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := jsonwire.MarshalOptions{}.MarshalAppend([]byte("prefix:"), m)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "prefix:"+string(want) {
		t.Fatalf("MarshalAppend: got %s", got)
	}
	// On error, MarshalAppend returns nil. protojson does the same, except
	// for a missing required field, where it also returns the output.
	for _, c := range []struct {
		indent string
		m      proto.Message
	}{
		{"", wrapperspb.String("\xff")},
		{"", &durationpb.Duration{Nanos: -1, Seconds: 1}},
		{"", new(editionspb.Editions)},
		{"x", m},
	} {
		got, err := jsonwire.MarshalOptions{Indent: c.indent}.MarshalAppend([]byte("prefix:"), c.m)
		if err == nil || got != nil {
			t.Errorf("MarshalAppend(%v) with indent %q: got %q, %v; want nil and an error", c.m, c.indent, got, err)
		}
	}
	if got := (jsonwire.MarshalOptions{}).Format(nil); got != "<nil>" {
		t.Fatalf("Format(nil): got %q", got)
	}
	if got := (jsonwire.MarshalOptions{}).Format((*testpb.TestAllTypesProto3)(nil)); got != "<nil>" {
		t.Fatalf("Format(typed nil): got %q", got)
	}
	// Format ignores missing required fields.
	if got := (jsonwire.MarshalOptions{}).Format(new(editionspb.Editions)); got != "{}" {
		t.Fatalf("Format(missing required fields): got %q", got)
	}
	if got := (jsonwire.MarshalOptions{}).Format(m); got != string(want) {
		t.Fatalf("Format: got %s, want %s", got, want)
	}
}

// TestEncodingJSONAny checks Any inside encoding/json, where the decoder of
// encoding/json has internal options that sub-decoders must not copy.
func TestEncodingJSONAny(t *testing.T) {
	t.Parallel()
	inner, err := anypb.New(&testpb.TestAllTypesProto3{OptionalInt32: 1, OptionalString: "inner"})
	if err != nil {
		t.Fatal(err)
	}
	value, err := anypb.New(wrapperspb.String("<&>"))
	if err != nil {
		t.Fatal(err)
	}
	m := &testpb.TestAllTypesProto3{
		OptionalAny: inner,
		RepeatedAny: []*anypb.Any{value, inner},
	}
	type oldContainer struct {
		Messages []*protojsonMessage `json:"messages"`
	}
	type newContainer struct {
		Messages []*streamMessage `json:"messages"`
	}
	for name, api := range map[string]struct {
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{
		"v1": {jsonv1.Marshal, jsonv1.Unmarshal},
		"v2": {
			func(v any) ([]byte, error) { return jsonv2.Marshal(v) },
			func(b []byte, v any) error { return jsonv2.Unmarshal(b, v) },
		},
	} {
		want, err := api.marshal(oldContainer{Messages: []*protojsonMessage{{msg: m}, {msg: m}}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := api.marshal(newContainer{Messages: []*streamMessage{{msg: m}, {msg: m}}})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(testutil.Compact(t, got), testutil.Compact(t, want)) {
			t.Fatalf("%s: got %s, want %s", name, got, want)
		}
		var out newContainer
		if err := api.unmarshal(got, &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, s := range out.Messages {
			if !proto.Equal(s.msg, m) {
				t.Fatalf("%s: got %v, want %v", name, s.msg, m)
			}
		}
	}
}

// TestDescriptorNamesAreChecked checks that a JSON name from a dynamic
// descriptor must be valid UTF-8, also with an encoder that allows invalid
// UTF-8.
func TestDescriptorNamesAreChecked(t *testing.T) {
	t.Parallel()
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    proto.String("badname.proto"),
		Package: proto.String("badname"),
		Syntax:  proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("M"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name:     proto.String("x"),
				Number:   proto.Int32(1),
				Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
				Type:     descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
				JsonName: proto.String("x\xff"),
			}},
		}},
	}, nil)
	if err != nil {
		t.Skipf("protodesc rejects the descriptor: %v", err)
	}
	md := fd.Messages().Get(0)
	m := dynamicpb.NewMessage(md)
	m.Set(md.Fields().Get(0), protoreflect.ValueOfInt32(1))

	if _, err := protojson.Marshal(m); err == nil {
		t.Fatal("protojson accepted the name; update this test")
	}
	if _, err := jsonwire.Marshal(m); err == nil {
		t.Error("Marshal: want error")
	}
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf, jsontext.AllowInvalidUTF8(true))
	if err := jsonwire.MarshalEncode(enc, m); err == nil {
		t.Errorf("MarshalEncode with lenient encoder: want error, got %q", buf.String())
	}
}

// TestExtensionWithoutRange checks a dynamic message that holds an extension,
// but whose descriptor has no extension ranges. This occurs when the extension
// comes from a newer version of the file than the message descriptor:
// dynamicpb compares only the name of the extended message. The extension can
// even have the number of a declared field of the older message.
func TestExtensionWithoutRange(t *testing.T) {
	t.Parallel()
	newFile := func(msg *descriptorpb.DescriptorProto, ext *descriptorpb.FieldDescriptorProto) protoreflect.FileDescriptor {
		t.Helper()
		fdp := &descriptorpb.FileDescriptorProto{
			Name:        proto.String("skew.proto"),
			Package:     proto.String("skew"),
			Syntax:      proto.String("proto2"),
			MessageType: []*descriptorpb.DescriptorProto{msg},
		}
		if ext != nil {
			fdp.Extension = []*descriptorpb.FieldDescriptorProto{ext}
		}
		fd, err := protodesc.NewFile(fdp, nil)
		if err != nil {
			t.Fatal(err)
		}
		return fd
	}
	// The older message has the field a = 1 and no extension ranges.
	older := newFile(&descriptorpb.DescriptorProto{
		Name: proto.String("M"),
		Field: []*descriptorpb.FieldDescriptorProto{{
			Name:   proto.String("a"),
			Number: proto.Int32(1),
			Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
			Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		}},
	}, nil)

	for _, ext := range []struct {
		number int32
		typ    descriptorpb.FieldDescriptorProto_Type
		value  protoreflect.Value
	}{
		{100, descriptorpb.FieldDescriptorProto_TYPE_INT32, protoreflect.ValueOfInt32(7)},
		{1, descriptorpb.FieldDescriptorProto_TYPE_INT32, protoreflect.ValueOfInt32(7)},
		{1, descriptorpb.FieldDescriptorProto_TYPE_STRING, protoreflect.ValueOfString("x")},
	} {
		// The newer message has an extension range and no field a.
		newer := newFile(&descriptorpb.DescriptorProto{
			Name: proto.String("M"),
			ExtensionRange: []*descriptorpb.DescriptorProto_ExtensionRange{{
				Start: proto.Int32(1),
				End:   proto.Int32(200),
			}},
		}, &descriptorpb.FieldDescriptorProto{
			Name:     proto.String("e"),
			Number:   proto.Int32(ext.number),
			Extendee: proto.String(".skew.M"),
			Type:     ext.typ.Enum(),
			Label:    descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
		})
		m := dynamicpb.NewMessage(older.Messages().Get(0))
		xt := dynamicpb.NewExtensionType(newer.Extensions().Get(0))
		m.Set(xt.TypeDescriptor(), ext.value)

		for _, c := range []struct {
			name     string
			protoOpt protojson.MarshalOptions
			wireOpt  jsonwire.MarshalOptions
		}{
			{"default", protojson.MarshalOptions{}, jsonwire.MarshalOptions{}},
			{"EmitUnpopulated", protojson.MarshalOptions{EmitUnpopulated: true}, jsonwire.MarshalOptions{EmitUnpopulated: true}},
			{"EmitDefaultValues", protojson.MarshalOptions{EmitDefaultValues: true}, jsonwire.MarshalOptions{EmitDefaultValues: true}},
		} {
			want, err := c.protoOpt.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			got, err := c.wireOpt.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, testutil.Compact(t, want)) {
				t.Errorf("extension %d of type %v, %s: got %s, want %s",
					ext.number, ext.typ, c.name, got, testutil.Compact(t, want))
			}
		}
	}
}

// endlessArray is a reader that returns prefix and then the start of a JSON
// array that never ends.
type endlessArray struct{ prefix string }

func (r *endlessArray) Read(p []byte) (int, error) {
	n := copy(p, r.prefix)
	r.prefix = r.prefix[n:]
	if len(r.prefix) > 0 {
		return n, nil
	}
	for n+2 <= len(p) {
		p[n], p[n+1] = '1', ','
		n += 2
	}
	return n, nil
}

// rejectsEarly checks that UnmarshalRead returns an error for the input from
// r before it reads the whole array, which never ends.
func rejectsEarly(t *testing.T, r io.Reader, m proto.Message) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		done <- jsonwire.UnmarshalRead(r, m)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("want error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("UnmarshalRead read the whole array before it rejected the value")
	}
}

// TestNameRejectsContainerEarly checks that an object or array in the place of
// an object name is rejected at its first token.
func TestNameRejectsContainerEarly(t *testing.T) {
	t.Parallel()
	for _, prefix := range []string{
		`{[`, `{"optionalInt32":1,[`, `{"mapStringString":{[`,
		`{{"a":[`, `{"optionalInt32":1,{"a":[`, `{"mapStringString":{{"a":[`,
	} {
		rejectsEarly(t, &endlessArray{prefix: prefix}, new(testpb.TestAllTypesProto3))
	}
}

// TestScalarRejectsContainerEarly checks that an object or array for a scalar
// field is rejected at its first token. Before the fix, the decoder read the
// whole value first, which never ends here.
func TestScalarRejectsContainerEarly(t *testing.T) {
	t.Parallel()
	rejectsEarly(t, &endlessArray{prefix: "["}, new(wrapperspb.Int32Value))
}

func TestPackageFormat(t *testing.T) {
	t.Parallel()
	m := newTestMessage()
	want := jsonwire.MarshalOptions{Multiline: true}.Format(m)
	if got := jsonwire.Format(m); got != want || !strings.Contains(got, "\n") {
		t.Fatalf("Format: got %q, want %q", got, want)
	}
}

// wrappingResolver returns a wrapped protoregistry.NotFound, which a resolver
// must not do.
type wrappingResolver struct{ *protoregistry.Types }

func (wrappingResolver) FindExtensionByName(protoreflect.FullName) (protoreflect.ExtensionType, error) {
	return nil, fmt.Errorf("lookup: %w", protoregistry.NotFound)
}

// TestResolverNotFoundIsExact checks that only the exact NotFound value means
// an unknown extension, the same as in protojson.
func TestResolverNotFoundIsExact(t *testing.T) {
	t.Parallel()
	input := []byte(`{"[a.b]":1}`)
	r := wrappingResolver{Types: new(protoregistry.Types)}
	wantErr := protojson.UnmarshalOptions{DiscardUnknown: true, Resolver: r}.Unmarshal(input, new(editionspb.Editions))
	gotErr := jsonwire.UnmarshalOptions{DiscardUnknown: true, Resolver: r}.Unmarshal(input, new(editionspb.Editions))
	if wantErr == nil || gotErr == nil {
		t.Fatalf("want errors from both, got protojson %v, jsonwire %v", wantErr, gotErr)
	}
}

// TestMarshalOutputIsOwned checks that Marshal returns memory that the pooled
// state does not use again, for small and large outputs.
func TestMarshalOutputIsOwned(t *testing.T) {
	t.Parallel()
	for _, size := range []int{100, 100 << 10, 2 << 20} {
		first := &testpb.TestAllTypesProto3{OptionalBytes: bytes.Repeat([]byte{1}, size)}
		second := &testpb.TestAllTypesProto3{OptionalBytes: bytes.Repeat([]byte{2}, size)}
		a, err := jsonwire.Marshal(first)
		if err != nil {
			t.Fatal(err)
		}
		want := bytes.Clone(a)
		// Write into the spare capacity, which must not be shared either.
		_ = append(a[:len(a):cap(a)], "garbage"...)
		for range 10 {
			if _, err := jsonwire.Marshal(second); err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := jsonwire.MarshalWrite(&buf, second); err != nil {
				t.Fatal(err)
			}
			if _, err := (jsonwire.MarshalOptions{}).MarshalAppend(nil, second); err != nil {
				t.Fatal(err)
			}
		}
		if !bytes.Equal(a, want) {
			t.Fatalf("size %d: output changed after later Marshal calls", size)
		}
		if again, _ := jsonwire.Marshal(first); !bytes.Equal(again, want) {
			t.Fatalf("size %d: output of the same message changed", size)
		}
	}
}

// TestAnyValuesAreOwned checks that an Any does not refer to the input or to
// the memory of a pooled decoder after Unmarshal. Later calls use the same
// pooled decoders. The type URL has no escapes, so that the decoder could
// return a view of the input for it.
func TestAnyValuesAreOwned(t *testing.T) {
	t.Parallel()
	input := func(s string) []byte {
		return []byte(`{"optionalAny":{"optionalString":"` + s + `\n","@type":"type.googleapis.com/` +
			`protobuf_test_messages.proto3.TestAllTypesProto3","mapStringString":{"k` + s + `\t":"v` + s + `\""},` +
			`"repeatedString":["` + s + `\\","` + s + `"],"optionalAny":{"@type":"type.googleapis.com/google.protobuf.StringValue",` +
			`"value":"` + s + `é"}}}`)
	}
	var kept []*testpb.TestAllTypesProto3
	var want []*testpb.TestAllTypesProto3
	for i := range 50 {
		in := input(strings.Repeat(string(rune('a'+i%26)), 10+i))
		var w testpb.TestAllTypesProto3
		if err := protojson.Unmarshal(in, &w); err != nil {
			t.Fatal(err)
		}
		var read testpb.TestAllTypesProto3
		if err := jsonwire.UnmarshalRead(iotest.OneByteReader(bytes.NewReader(in)), &read); err != nil {
			t.Fatal(err)
		}
		var m testpb.TestAllTypesProto3
		if err := jsonwire.Unmarshal(in, &m); err != nil {
			t.Fatal(err)
		}
		// The message must not refer to the input either.
		for j := range in {
			in[j] = 'x'
		}
		kept = append(kept, &m, &read)
		want = append(want, &w, &w)
	}
	for i := range kept {
		if !proto.Equal(kept[i], want[i]) {
			t.Fatalf("message %d changed after later calls:\ngot  %v\nwant %v", i, kept[i], want[i])
		}
	}
}

// TestDuplicateNamesInAny checks that a decoder that checks for duplicate names
// rejects them at each depth of an Any value, and that a decoder that allows
// them gives the same result as protojson.
func TestDuplicateNamesInAny(t *testing.T) {
	t.Parallel()
	const prefix = `{"optionalAny":{"@type":"type.googleapis.com/`
	for _, input := range []string{
		prefix + `google.protobuf.Struct","value":{"a":1,"a":2}}}`,
		prefix + `google.protobuf.Struct","value":{"a":{"b":1,"b":2}}}}`,
		prefix + `google.protobuf.Value","value":[{"a":1,"a":2}]}}`,
		prefix + `protobuf_test_messages.proto3.TestAllTypesProto3","optionalStruct":{"a":1,"a":2}}}`,
		prefix + `google.protobuf.Any","value":{"@type":"type.googleapis.com/google.protobuf.Struct",` +
			`"value":{"a":1,"a":2}}}}`,
		prefix + `protobuf_test_messages.proto3.TestAllTypesProto3","unknown":1,"unknown":2}}`,
	} {
		opts := jsonwire.UnmarshalOptions{DiscardUnknown: true}
		strict := jsontext.NewDecoder(strings.NewReader(input))
		if err := opts.UnmarshalDecode(strict, new(testpb.TestAllTypesProto3)); err == nil {
			t.Errorf("%s: strict decoder: want error", input)
		}
		var want, got testpb.TestAllTypesProto3
		wantErr := protojson.UnmarshalOptions{DiscardUnknown: true}.Unmarshal([]byte(input), &want)
		lenient := jsontext.NewDecoder(strings.NewReader(input), jsontext.AllowDuplicateNames(true))
		gotErr := opts.UnmarshalDecode(lenient, &got)
		if (wantErr == nil) != (gotErr == nil) || (gotErr == nil && !proto.Equal(&got, &want)) {
			t.Errorf("%s: lenient decoder: got %v, %v; protojson: %v, %v", input, &got, gotErr, &want, wantErr)
		}
	}
}

// TestManyFields checks a message with more than 256 fields, so that field
// numbers and indexes above 255 do not collide with smaller ones.
func TestManyFields(t *testing.T) {
	t.Parallel()
	md := &descriptorpb.DescriptorProto{Name: proto.String("Large")}
	for i := range 300 {
		md.Field = append(md.Field, &descriptorpb.FieldDescriptorProto{
			Name:   proto.String(fmt.Sprintf("f%d", i)),
			Number: proto.Int32(int32(i + 1)),
			Label:  descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			Type:   descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum(),
		})
	}
	fd, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:        proto.String("large.proto"),
		Package:     proto.String("large"),
		Syntax:      proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{md},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{`{"f0":1,"f256":2}`, `{"f1":1,"f257":2}`, `{"f299":1,"f299":2}`} {
		want := dynamicpb.NewMessage(fd.Messages().Get(0))
		got := dynamicpb.NewMessage(fd.Messages().Get(0))
		wantErr := protojson.Unmarshal([]byte(input), want)
		gotErr := jsonwire.Unmarshal([]byte(input), got)
		if (gotErr == nil) != (wantErr == nil) || (gotErr == nil && !proto.Equal(got, want)) {
			t.Errorf("%s: got %v, %v; protojson: %v, %v", input, got, gotErr, want, wantErr)
		}
	}
}

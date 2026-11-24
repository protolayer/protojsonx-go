// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE-protobuf-go file.
//
// This file is a port of google.golang.org/protobuf/encoding/protojson to
// encoding/json/jsontext.

package jsonwire

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

const defaultIndent = "  "

// Marshal writes the given [proto.Message] in the ProtoJSON format using
// default options. The output is compact and deterministic: the same message
// gives the same bytes. Use [MarshalOptions] to change the behavior.
func Marshal(m proto.Message) ([]byte, error) {
	return MarshalOptions{}.Marshal(m)
}

// Format returns the ProtoJSON encoding of m as a multiline string, for human
// consumption.
func Format(m proto.Message) string {
	return MarshalOptions{Multiline: true}.Format(m)
}

// MarshalWrite writes the given [proto.Message] in the ProtoJSON format to w
// using default options. The output has no trailing newline.
func MarshalWrite(w io.Writer, m proto.Message) error {
	return MarshalOptions{}.MarshalWrite(w, m)
}

// MarshalEncode writes the given [proto.Message] as the next JSON value of enc
// using default options.
func MarshalEncode(enc *jsontext.Encoder, m proto.Message) error {
	return MarshalOptions{}.MarshalEncode(enc, m)
}

// MarshalOptions is a configurable JSON format marshaler. The fields have the
// same meaning as the fields of protojson.MarshalOptions.
type MarshalOptions struct {
	_ noUnkeyedLiterals

	// Multiline specifies whether the marshaler should format the output in
	// indented-form with every textual element on a new line. If Indent is an
	// empty string, then an arbitrary indent is chosen.
	//
	// Multiline and Indent apply only to Marshal and MarshalWrite. With
	// MarshalEncode, the options of the given jsontext.Encoder control the
	// format.
	Multiline bool

	// Indent specifies the set of indentation characters to use in a multiline
	// formatted output such that every entry is preceded by Indent and
	// terminated by a newline. If non-empty, then Multiline is treated as true.
	// Indent can only be composed of space or tab characters.
	Indent string

	// AllowPartial allows messages that have missing required fields to marshal
	// without returning an error. If AllowPartial is false (the default),
	// Marshal will return error if there are any missing required fields.
	AllowPartial bool

	// UseProtoNames uses proto field name instead of lowerCamelCase name in JSON
	// field names.
	UseProtoNames bool

	// UseEnumNumbers emits enum values as numbers.
	UseEnumNumbers bool

	// EmitUnpopulated specifies whether to emit unpopulated fields. It does not
	// emit unpopulated oneof fields or unpopulated extension fields. The JSON
	// value is null for fields that have presence and the zero value otherwise.
	EmitUnpopulated bool

	// EmitDefaultValues specifies whether to emit default-valued primitive
	// fields, empty lists, and empty maps. Unlike EmitUnpopulated, it does not
	// emit null for presence-sensing fields that are not set. EmitUnpopulated
	// takes precedence over EmitDefaultValues.
	EmitDefaultValues bool

	// Resolver is used for looking up types when expanding google.protobuf.Any
	// messages. If nil, this defaults to using protoregistry.GlobalTypes.
	Resolver interface {
		protoregistry.ExtensionTypeResolver
		protoregistry.MessageTypeResolver
	}
}

// noUnkeyedLiterals prevents unkeyed struct literals of the option types, so
// that new fields can be added later.
type noUnkeyedLiterals struct{}

// Marshal writes the given [proto.Message] in the ProtoJSON format using
// options in MarshalOptions. The output is deterministic: the same message and
// options give the same bytes.
func (o MarshalOptions) Marshal(m proto.Message) ([]byte, error) {
	return o.marshalToState(m, nil)
}

// MarshalAppend appends the ProtoJSON encoding of m to b and returns the
// result. On error, it returns nil. protojson does the same, except for a
// missing required field, where it also returns the output.
func (o MarshalOptions) MarshalAppend(b []byte, m proto.Message) ([]byte, error) {
	return o.marshalToState(m, b)
}

func (o MarshalOptions) marshalToState(m proto.Message, dst []byte) ([]byte, error) {
	opts, err := o.encoderOptions()
	if err != nil {
		return nil, err
	}
	s := getEncodeState()
	s.resetEncoder(opts)
	err = o.marshalEncode(s.enc, m, s, false)
	var out []byte
	if err == nil {
		raw := bytes.TrimSuffix(s.buf.Bytes(), []byte("\n"))
		switch {
		case dst != nil:
			out = append(dst, raw...)
		case !s.poolable():
			// s will not be pooled, so return its buffer directly without copying.
			out = raw
		default:
			out = bytes.Clone(raw)
		}
	}
	putEncodeState(s, err == nil)
	if err != nil {
		return nil, wrapError(err)
	}
	return out, nil
}

// Format returns the ProtoJSON encoding of m as a string, for human
// consumption. It ignores errors and missing required fields.
func (o MarshalOptions) Format(m proto.Message) string {
	if m == nil || !m.ProtoReflect().IsValid() {
		return "<nil>" // invalid syntax, but okay since this is for debugging
	}
	o.AllowPartial = true
	b, _ := o.Marshal(m)
	return string(b)
}

// MarshalWrite writes the given [proto.Message] in the ProtoJSON format to w
// using options in MarshalOptions. The output has no trailing newline, which
// is the same as json.MarshalWrite.
func (o MarshalOptions) MarshalWrite(w io.Writer, m proto.Message) error {
	opts, err := o.encoderOptions()
	if err != nil {
		return err
	}
	s := getEncodeState()
	s.resetEncoder(opts)
	err = o.marshalEncode(s.enc, m, s, false)
	var writeErr error
	if err == nil {
		_, writeErr = w.Write(bytes.TrimSuffix(s.buf.Bytes(), []byte("\n")))
	}
	putEncodeState(s, err == nil)
	if err != nil {
		return wrapError(err)
	}
	return writeErr
}

// MarshalEncode writes the given [proto.Message] as the next JSON value of enc
// using options in MarshalOptions.
//
// The options of enc control the format of the output, for example
// indentation. Multiline and Indent in MarshalOptions have no effect. The
// ProtoJSON rules for string values always apply: a string that is not valid
// UTF-8 causes an error even if enc allows invalid UTF-8.
//
// For the best performance, create enc with jsontext.AllowDuplicateNames(true).
// Field names in a message are unique, so the check for duplicate names only
// costs time.
//
// If MarshalEncode returns an error, enc can contain part of the value.
func (o MarshalOptions) MarshalEncode(enc *jsontext.Encoder, m proto.Message) error {
	allowInvalidUTF8, _ := jsonv2.GetOption(enc.Options(), jsontext.AllowInvalidUTF8)
	s := getEncodeState()
	err := o.marshalEncode(enc, m, s, allowInvalidUTF8)
	putEncodeState(s, true)
	return wrapError(err)
}

func (o MarshalOptions) marshalEncode(enc *jsontext.Encoder, m proto.Message, s *encodeState, checkUTF8 bool) error {
	if o.Resolver == nil {
		o.Resolver = protoregistry.GlobalTypes
	}
	// Treat a nil message interface as an empty message, in which case the
	// output is an empty JSON object.
	if m == nil {
		if err := enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		return enc.WriteToken(jsontext.EndObject)
	}
	// Check required fields before any output, so that a missing required
	// field does not leave a partial value in enc.
	if !o.AllowPartial {
		if err := proto.CheckInitialized(m); err != nil {
			return err
		}
	}
	e := encoder{enc: enc, opts: o, checkUTF8: checkUTF8, s: s}
	return e.marshalMessage(m.ProtoReflect(), "")
}

// Encoder options for Marshal and MarshalWrite. Field names are unique by
// construction, so the check for duplicate names is turned off. This is
// faster, and it gives the same output as protojson for messages with
// conflicting JSON names.
var (
	compactOptions   = jsontext.AllowDuplicateNames(true)
	multilineOptions = jsonv2.JoinOptions(compactOptions, jsontext.Multiline(true), jsontext.WithIndent(defaultIndent))
)

func (o MarshalOptions) encoderOptions() (jsontext.Options, error) {
	indent := o.Indent
	if o.Multiline && indent == "" {
		return multilineOptions, nil
	}
	if indent == "" {
		return compactOptions, nil
	}
	if strings.Trim(indent, " \t") != "" {
		return nil, errorf("indent may only be composed of space or tab characters")
	}
	return jsonv2.JoinOptions(compactOptions, jsontext.Multiline(true), jsontext.WithIndent(indent)), nil
}

// encodeState holds reusable buffers and scratch stacks for marshaling.
// The fields and entries slices are used as stacks: a nested message appends
// after its parent and pops its entries when done. The Range callbacks are
// created once per state to avoid closure allocations.
type encodeState struct {
	buf      bytes.Buffer
	enc      *jsontext.Encoder
	fields   []fieldValue
	entries  []mapEntry
	addField func(protoreflect.FieldDescriptor, protoreflect.Value) bool
	addEntry func(protoreflect.MapKey, protoreflect.Value) bool
}

type fieldValue struct {
	fd protoreflect.FieldDescriptor
	v  protoreflect.Value
}

type mapEntry struct {
	k protoreflect.MapKey
	v protoreflect.Value
}

const (
	maxPooledBuf     = 64 << 10
	maxPooledEntries = 1024
)

var encodeStatePool = sync.Pool{
	New: func() any {
		s := new(encodeState)
		s.addField = func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
			s.fields = append(s.fields, fieldValue{fd: fd, v: v})
			return true
		}
		s.addEntry = func(k protoreflect.MapKey, v protoreflect.Value) bool {
			s.entries = append(s.entries, mapEntry{k: k, v: v})
			return true
		}
		return s
	},
}

func getEncodeState() *encodeState {
	return encodeStatePool.Get().(*encodeState)
}

func (s *encodeState) resetEncoder(opts jsontext.Options) {
	s.buf.Reset()
	if s.enc == nil {
		s.enc = jsontext.NewEncoder(&s.buf, opts)
	} else {
		s.enc.Reset(&s.buf, opts)
	}
}

func (s *encodeState) poolable() bool {
	return s.buf.Cap() <= maxPooledBuf && cap(s.fields) <= maxPooledEntries && cap(s.entries) <= maxPooledEntries
}

func putEncodeState(s *encodeState, ok bool) {
	if !ok || !s.poolable() {
		return
	}
	s.buf.Reset()
	if s.enc != nil {
		s.enc.Reset(io.Discard)
	}
	clear(s.fields[:cap(s.fields)])
	s.fields = s.fields[:0]
	clear(s.entries[:cap(s.entries)])
	s.entries = s.entries[:0]
	encodeStatePool.Put(s)
}

type encoder struct {
	enc  *jsontext.Encoder
	opts MarshalOptions
	// checkUTF8 is true when the jsontext.Encoder allows invalid UTF-8. Then
	// the encoder must check strings itself to keep the ProtoJSON rules.
	checkUTF8 bool
	s         *encodeState
}

// marshalMessage marshals the fields in the given protoreflect.Message. If the
// typeURL is non-empty, then a synthetic "@type" field is injected containing
// the URL as the value.
func (e encoder) marshalMessage(m protoreflect.Message, typeURL string) error {
	md := m.Descriptor()
	if isMessageSet(md) {
		return errorf("no support for proto1 MessageSets")
	}
	if marshal := wellKnownTypeMarshaler(md.FullName()); marshal != nil {
		return marshal(e, m)
	}

	if err := e.enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	if typeURL != "" {
		if err := e.enc.WriteToken(jsontext.String("@type")); err != nil {
			return err
		}
		if err := e.writeString(typeURL, "@type"); err != nil {
			return err
		}
	}

	// Fields are written in declaration order, then extensions sorted by full
	// name. This is the same order as protojson.
	if err := e.marshalFields(m); err != nil {
		return err
	}
	return e.enc.WriteToken(jsontext.EndObject)
}

// marshalFields writes the fields of m in declaration order, followed by
// extensions sorted by full name.
func (e encoder) marshalFields(m protoreflect.Message) error {
	start := len(e.s.fields)
	m.Range(e.s.addField)
	if e.opts.EmitUnpopulated || e.opts.EmitDefaultValues {
		skipNull := !e.opts.EmitUnpopulated
		fds := m.Descriptor().Fields()
		for i := range fds.Len() {
			fd := fds.Get(i)
			if m.Has(fd) || fd.ContainingOneof() != nil {
				// Range gave the populated fields, including any dynamic
				// message extension that shares this field number. Unpopulated
				// oneof fields (including proto3 optional) are never written.
				continue
			}
			var v protoreflect.Value // an invalid value is written as null
			if !fd.HasPresence() {
				v = m.Get(fd)
			} else if skipNull {
				continue
			}
			e.s.fields = append(e.s.fields, fieldValue{fd: fd, v: v})
		}
	}
	end := len(e.s.fields)
	defer func() {
		clear(e.s.fields[start:end])
		e.s.fields = e.s.fields[:start]
	}()
	slices.SortFunc(e.s.fields[start:end], compareFieldOrder)
	for i := start; i < end; i++ {
		// Nested messages append to e.s.fields, which can reallocate it.
		f := e.s.fields[i]
		if err := e.marshalField(f.fd, f.v); err != nil {
			return err
		}
	}
	return nil
}

// compareFieldOrder puts fields before extensions. Fields are sorted by
// declaration index, and extensions by full name.
func compareFieldOrder(x, y fieldValue) int {
	xExt, yExt := x.fd.IsExtension(), y.fd.IsExtension()
	switch {
	case xExt != yExt:
		if xExt {
			return 1
		}
		return -1
	case xExt:
		return cmp.Compare(x.fd.FullName(), y.fd.FullName())
	default:
		return cmp.Compare(x.fd.Index(), y.fd.Index())
	}
}

func (e encoder) marshalField(fd protoreflect.FieldDescriptor, v protoreflect.Value) error {
	name := fd.JSONName()
	if e.opts.UseProtoNames {
		name = fd.TextName()
	}
	// Names from a descriptor are almost always valid UTF-8, but a dynamic
	// descriptor can have any json_name. Check it like message data.
	if err := e.writeString(name, string(fd.FullName())); err != nil {
		return err
	}
	return e.marshalValue(v, fd)
}

// marshalValue marshals the given protoreflect.Value.
func (e encoder) marshalValue(val protoreflect.Value, fd protoreflect.FieldDescriptor) error {
	switch {
	case fd.IsList():
		return e.marshalList(val.List(), fd)
	case fd.IsMap():
		return e.marshalMap(val.Map(), fd)
	default:
		return e.marshalSingular(val, fd)
	}
}

// marshalSingular marshals the given non-repeated field value. This includes
// all scalar types, enums, messages, and groups.
func (e encoder) marshalSingular(val protoreflect.Value, fd protoreflect.FieldDescriptor) error {
	if !val.IsValid() {
		return e.enc.WriteToken(jsontext.Null)
	}

	switch kind := fd.Kind(); kind {
	case protoreflect.BoolKind:
		return e.enc.WriteToken(jsontext.Bool(val.Bool()))

	case protoreflect.StringKind:
		return e.writeString(val.String(), string(fd.FullName()))

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return e.enc.WriteToken(jsontext.Int(val.Int()))

	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return e.enc.WriteToken(jsontext.Uint(val.Uint()))

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		// 64-bit integers are written out as JSON strings.
		return e.writeQuotedInt(val.Int())

	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return e.writeQuotedUint(val.Uint())

	case protoreflect.FloatKind:
		// jsontext writes NaN and infinities as the JSON strings "NaN",
		// "Infinity", and "-Infinity", which is the ProtoJSON format.
		return e.enc.WriteToken(jsontext.Float32(float32(val.Float())))

	case protoreflect.DoubleKind:
		return e.enc.WriteToken(jsontext.Float(val.Float()))

	case protoreflect.BytesKind:
		b := e.enc.AvailableBuffer()
		b = append(b, '"')
		b = base64.StdEncoding.AppendEncode(b, val.Bytes())
		b = append(b, '"')
		return e.enc.WriteValue(b)

	case protoreflect.EnumKind:
		if fd.Enum().FullName() == nullValueFullName {
			return e.enc.WriteToken(jsontext.Null)
		}
		desc := fd.Enum().Values().ByNumber(val.Enum())
		if e.opts.UseEnumNumbers || desc == nil {
			return e.enc.WriteToken(jsontext.Int(int64(val.Enum())))
		}
		return e.enc.WriteToken(jsontext.String(string(desc.Name())))

	case protoreflect.MessageKind, protoreflect.GroupKind:
		return e.marshalMessage(val.Message(), "")

	default:
		return errorf("%v has unknown kind: %v", fd.FullName(), kind)
	}
}

// marshalList marshals the given protoreflect.List.
func (e encoder) marshalList(list protoreflect.List, fd protoreflect.FieldDescriptor) error {
	if err := e.enc.WriteToken(jsontext.BeginArray); err != nil {
		return err
	}
	for i := range list.Len() {
		if err := e.marshalSingular(list.Get(i), fd); err != nil {
			return err
		}
	}
	return e.enc.WriteToken(jsontext.EndArray)
}

// marshalMap marshals the given protoreflect.Map. Entries are sorted: false
// before true, numbers in ascending order, and strings by UTF-8 bytes. This is
// the same order as protojson.
func (e encoder) marshalMap(mmap protoreflect.Map, fd protoreflect.FieldDescriptor) error {
	if err := e.enc.WriteToken(jsontext.BeginObject); err != nil {
		return err
	}
	start := len(e.s.entries)
	mmap.Range(e.s.addEntry)
	end := len(e.s.entries)
	defer func() {
		clear(e.s.entries[start:end])
		e.s.entries = e.s.entries[:start]
	}()
	keyKind := fd.MapKey().Kind()
	slices.SortFunc(e.s.entries[start:end], func(x, y mapEntry) int {
		return compareMapKeys(keyKind, x.k, y.k)
	})
	valueFd := fd.MapValue()
	for i := start; i < end; i++ {
		// Nested messages append to e.s.entries, which can reallocate it.
		entry := e.s.entries[i]
		if err := e.writeMapKey(entry.k, keyKind, fd); err != nil {
			return err
		}
		if err := e.marshalSingular(entry.v, valueFd); err != nil {
			return err
		}
	}
	return e.enc.WriteToken(jsontext.EndObject)
}

// writeMapKey writes a map key as an object name. Numbers and booleans are
// formatted without an allocation.
func (e encoder) writeMapKey(k protoreflect.MapKey, kind protoreflect.Kind, fd protoreflect.FieldDescriptor) error {
	switch kind {
	case protoreflect.StringKind:
		return e.writeString(k.String(), string(fd.FullName()))
	case protoreflect.BoolKind:
		if k.Bool() {
			return e.enc.WriteToken(jsontext.String("true"))
		}
		return e.enc.WriteToken(jsontext.String("false"))
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return e.writeQuotedInt(k.Int())
	default:
		return e.writeQuotedUint(k.Uint())
	}
}

// writeQuotedInt writes n as a JSON string. It formats n into the encoder
// buffer, so it does not allocate.
func (e encoder) writeQuotedInt(n int64) error {
	b := e.enc.AvailableBuffer()
	b = append(b, '"')
	b = strconv.AppendInt(b, n, 10)
	b = append(b, '"')
	return e.enc.WriteValue(b)
}

// writeQuotedUint writes n as a JSON string without an allocation.
func (e encoder) writeQuotedUint(n uint64) error {
	b := e.enc.AvailableBuffer()
	b = append(b, '"')
	b = strconv.AppendUint(b, n, 10)
	b = append(b, '"')
	return e.enc.WriteValue(b)
}

func compareMapKeys(kind protoreflect.Kind, x, y protoreflect.MapKey) int {
	switch kind {
	case protoreflect.BoolKind:
		switch {
		case x.Bool() == y.Bool():
			return 0
		case !x.Bool():
			return -1
		default:
			return 1
		}
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return cmp.Compare(x.Int(), y.Int())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return cmp.Compare(x.Uint(), y.Uint())
	default:
		return cmp.Compare(x.String(), y.String())
	}
}

// writeString writes a string value or an object name and checks UTF-8.
func (e encoder) writeString(s, field string) error {
	if e.checkUTF8 && !utf8.ValidString(s) {
		return invalidUTF8Error(field)
	}
	if err := e.enc.WriteToken(jsontext.String(s)); err != nil {
		return wrapInvalidUTF8(err, s, field)
	}
	return nil
}

func invalidUTF8Error(field string) error {
	return errorf("field %v contains invalid UTF-8", field)
}

// wrapInvalidUTF8 gives a jsontext error for the string s the same meaning as
// the protojson error, if s is not valid UTF-8. It checks s only after an
// error, so the common path does not scan s twice.
func wrapInvalidUTF8(err error, s, field string) error {
	if !utf8.ValidString(s) {
		return fmt.Errorf("%w: %w", invalidUTF8Error(field), err)
	}
	return err
}

// isMessageSet reports whether the message uses the proto1 MessageSet wire
// format, which jsonwire does not support.
func isMessageSet(md protoreflect.MessageDescriptor) bool {
	xmd, ok := md.(interface{ IsMessageSet() bool })
	return ok && xmd.IsMessageSet()
}

// errorf returns an error with the package prefix.
func errorf(format string, args ...any) error {
	return &wireError{err: fmt.Errorf("jsonwire: "+format, args...)}
}

// wireError is the error type of jsonwire. Like protojson errors, it matches
// proto.Error with errors.Is.
type wireError struct {
	err error
}

func (e *wireError) Error() string   { return e.err.Error() }
func (e *wireError) Unwrap() []error { return []error{proto.Error, e.err} }

// wrapError makes err match proto.Error, the same as protojson errors. io.EOF
// at the end of a stream stays the same, because callers compare it with ==.
func wrapError(err error) error {
	if err == nil || err == io.EOF || errors.Is(err, proto.Error) {
		return err
	}
	return &wireError{err: err}
}

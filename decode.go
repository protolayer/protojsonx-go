// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE-protobuf-go file.
//
// This file is a port of google.golang.org/protobuf/encoding/protojson to
// encoding/json/jsontext.

package jsonwire

import (
	"bytes"
	"encoding/base64"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// JSON token kinds, as returned by jsontext.Token.Kind and
// jsontext.Decoder.PeekKind.
const (
	kindNull        jsontext.Kind = 'n'
	kindFalse       jsontext.Kind = 'f'
	kindTrue        jsontext.Kind = 't'
	kindString      jsontext.Kind = '"'
	kindNumber      jsontext.Kind = '0'
	kindObjectOpen  jsontext.Kind = '{'
	kindObjectClose jsontext.Kind = '}'
	kindArrayOpen   jsontext.Kind = '['
	kindArrayClose  jsontext.Kind = ']'
)

// Unmarshal reads the given []byte into the given [proto.Message]. The
// provided message must be mutable (for example, a non-nil pointer to a
// message).
func Unmarshal(b []byte, m proto.Message) error {
	return UnmarshalOptions{}.Unmarshal(b, m)
}

// UnmarshalRead reads one JSON value from r into the given [proto.Message].
// It reads r until EOF, and it is an error if r contains more than one JSON
// value.
func UnmarshalRead(r io.Reader, m proto.Message) error {
	return UnmarshalOptions{}.UnmarshalRead(r, m)
}

// UnmarshalDecode reads the next JSON value from dec into the given
// [proto.Message].
func UnmarshalDecode(dec *jsontext.Decoder, m proto.Message) error {
	return UnmarshalOptions{}.UnmarshalDecode(dec, m)
}

// UnmarshalOptions is a configurable JSON format parser. The fields have the
// same meaning as the fields of protojson.UnmarshalOptions.
type UnmarshalOptions struct {
	_ noUnkeyedLiterals

	// AllowPartial accepts input for messages that will result in missing
	// required fields. If AllowPartial is false (the default), Unmarshal will
	// return error if there are any missing required fields.
	AllowPartial bool

	// DiscardUnknown specifies whether to ignore unknown fields when parsing.
	// An unknown field is any field whose field name or field number does not
	// resolve to any known or extension field in the message. By default,
	// unmarshal rejects unknown fields as an error.
	DiscardUnknown bool

	// Resolver is used for looking up types when unmarshaling
	// google.protobuf.Any messages or extension fields. If nil, this defaults
	// to using protoregistry.GlobalTypes.
	Resolver interface {
		protoregistry.MessageTypeResolver
		protoregistry.ExtensionTypeResolver
	}

	// RecursionLimit limits how deeply messages may be nested. If zero, a
	// default limit is applied.
	RecursionLimit int
}

// Unmarshal reads the given []byte and populates the given [proto.Message]
// using options in the UnmarshalOptions object. If Unmarshal returns an error,
// the given message may be partially set.
func (o UnmarshalOptions) Unmarshal(b []byte, m proto.Message) error {
	dec := jsontext.NewDecoder(bytes.NewBuffer(b), jsontext.AllowDuplicateNames(true))
	return o.unmarshalAll(dec, m)
}

// UnmarshalRead reads one JSON value from r and populates the given
// [proto.Message] using options in the UnmarshalOptions object. It reads r
// until EOF, and it is an error if r contains more than one JSON value.
//
// UnmarshalRead does not limit the size of the input. For input from an
// untrusted source, limit r, for example with [io.LimitReader]. Memory use
// grows with the input: each google.protobuf.Any value is read completely
// before it is decoded, because its "@type" field can come last.
func (o UnmarshalOptions) UnmarshalRead(r io.Reader, m proto.Message) error {
	return o.unmarshalAll(jsontext.NewDecoder(r, jsontext.AllowDuplicateNames(true)), m)
}

// unmarshalAll reads exactly one JSON value from dec, followed by EOF.
func (o UnmarshalOptions) unmarshalAll(dec *jsontext.Decoder, m proto.Message) error {
	if err := o.UnmarshalDecode(dec, m); err != nil {
		if errors.Is(err, io.EOF) {
			// Empty input. Do not return io.EOF, which callers can take as a
			// clean end of input.
			return errorf("%w", io.ErrUnexpectedEOF)
		}
		return err
	}
	switch _, err := dec.ReadToken(); {
	case err == io.EOF:
		return nil
	case err != nil:
		return wrapError(err)
	default:
		return errorf("unexpected data after top-level value at offset %d", dec.InputOffset())
	}
}

// UnmarshalDecode reads the next JSON value from dec and populates the given
// [proto.Message] using options in the UnmarshalOptions object. It does not
// read past the end of that value, so the caller can read more values from
// dec.
//
// The syntax options of dec apply, for example whether duplicate JSON object
// names are allowed. The ProtoJSON rules for string values always apply: a
// string that is not valid UTF-8 causes an error even if dec allows invalid
// UTF-8. Unmarshal and UnmarshalRead allow duplicate object names, which is
// the same as protojson. Duplicate fields and duplicate map keys are always an
// error. For the best performance, create dec with
// jsontext.AllowDuplicateNames(true).
//
// Like UnmarshalRead, UnmarshalDecode does not limit the size of the input.
func (o UnmarshalOptions) UnmarshalDecode(dec *jsontext.Decoder, m proto.Message) error {
	proto.Reset(m)

	if o.Resolver == nil {
		o.Resolver = protoregistry.GlobalTypes
	}
	if o.RecursionLimit == 0 {
		o.RecursionLimit = protowire.DefaultRecursionLimit
	}

	allowInvalidUTF8, _ := jsonv2.GetOption(dec.Options(), jsontext.AllowInvalidUTF8)
	d := decoder{
		dec:       dec,
		opts:      o,
		checkUTF8: allowInvalidUTF8,
	}
	if err := d.unmarshalMessage(m.ProtoReflect(), false); err != nil {
		return wrapError(err)
	}
	if o.AllowPartial {
		return nil
	}
	return proto.CheckInitialized(m)
}

// decoder holds the state for one call. Methods use a value receiver, so that
// opts.RecursionLimit counts down for each nested message, the same as in
// protojson.
type decoder struct {
	dec  *jsontext.Decoder
	opts UnmarshalOptions
	// checkUTF8 is true when the jsontext.Decoder allows invalid UTF-8. Then
	// the decoder must check strings itself to keep the ProtoJSON rules.
	checkUTF8 bool
}

// newError returns an error with the location of the decoder.
func (d decoder) newError(format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	if ptr := d.dec.StackPointer(); ptr != "" {
		msg += " (at " + string(ptr) + ")"
	}
	return &wireError{err: errors.New("jsonwire: " + msg)}
}

// unexpectedTokenError returns a syntax error for the given unexpected token.
func (d decoder) unexpectedTokenError(tok jsontext.Token) error {
	return d.newError("unexpected token %s", tokenString(tok))
}

// tokenString formats a token for an error message.
func tokenString(tok jsontext.Token) string {
	if tok.Kind() == kindString {
		return strconv.Quote(tok.String())
	}
	return tok.String()
}

// readToken reads the next JSON token. When the underlying decoder allows
// invalid UTF-8, string tokens are checked against the strict ProtoJSON UTF-8
// rules before they are returned.
func (d decoder) readToken() (jsontext.Token, error) {
	if d.checkUTF8 && d.dec.PeekKind() == kindString {
		raw, err := d.dec.ReadValue()
		if err != nil {
			return jsontext.Token{}, err
		}
		b, err := jsontext.AppendUnquote(nil, raw)
		if err != nil {
			return jsontext.Token{}, d.newError("invalid string: %v", err)
		}
		return jsontext.String(string(b)), nil
	}
	return d.dec.ReadToken()
}

// readString reads the next JSON token, which must be a string, and returns
// its unquoted value.
func (d decoder) readString() (string, error) {
	tok, err := d.readToken()
	if err != nil {
		return "", err
	}
	if tok.Kind() != kindString {
		return "", d.unexpectedTokenError(tok)
	}
	return tok.String(), nil
}

// subDecoder returns a decoder that reads the given JSON value with the same
// options and recursion limit as d.
//
// The sub-decoder parses raw in place, without a copy. The caller must not
// read from d.dec while it uses the sub-decoder, because raw can refer to the
// memory of d.dec.
func (d decoder) subDecoder(raw []byte) decoder {
	d.dec = jsontext.NewDecoder(bytes.NewBuffer(raw), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(d.checkUTF8))
	return d
}

// reset makes the sub-decoder read raw again from the start.
func (d decoder) reset(raw []byte) {
	d.dec.Reset(bytes.NewBuffer(raw), jsontext.AllowDuplicateNames(true), jsontext.AllowInvalidUTF8(d.checkUTF8))
}

// unmarshalMessage unmarshals a message into the given protoreflect.Message.
func (d decoder) unmarshalMessage(m protoreflect.Message, skipTypeURL bool) error {
	d.opts.RecursionLimit--
	if d.opts.RecursionLimit < 0 {
		return errorf("exceeded max recursion depth")
	}
	if unmarshal := wellKnownTypeUnmarshaler(m.Descriptor().FullName()); unmarshal != nil {
		return unmarshal(d, m)
	}

	tok, err := d.dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != kindObjectOpen {
		return d.unexpectedTokenError(tok)
	}

	messageDesc := m.Descriptor()
	if isMessageSet(messageDesc) {
		return errorf("no support for proto1 MessageSets")
	}

	var seenFields, seenOneofs fieldSet
	fieldDescs := messageDesc.Fields()
	for {
		// Read field name.
		if d.dec.PeekKind() == kindObjectClose {
			_, err := d.dec.ReadToken()
			return err
		}
		name, err := d.readString()
		if err != nil {
			return err
		}

		// Unmarshaling a non-custom embedded message in Any will contain the
		// JSON field "@type" which should be skipped because it is not a field
		// of the embedded message, but simply an artifact of the Any format.
		if skipTypeURL && name == "@type" {
			if err := d.dec.SkipValue(); err != nil {
				return err
			}
			continue
		}

		// Get the FieldDescriptor.
		var fd protoreflect.FieldDescriptor
		if strings.HasPrefix(name, "[") && strings.HasSuffix(name, "]") {
			// Only extension names are in [name] format.
			extName := protoreflect.FullName(name[1 : len(name)-1])
			extType, err := d.opts.Resolver.FindExtensionByName(extName)
			// protoregistry requires resolvers to return exactly NotFound,
			// not a wrapped error. Compare the same way as protojson.
			if err != nil && err != protoregistry.NotFound {
				return d.newError("unable to resolve %q: %v", extName, err)
			}
			if extType != nil {
				fd = extType.TypeDescriptor()
				if !messageDesc.ExtensionRanges().Has(fd.Number()) || fd.ContainingMessage().FullName() != messageDesc.FullName() {
					return d.newError("message %v cannot be extended by %v", messageDesc.FullName(), fd.FullName())
				}
			}
		} else {
			// The name can either be the JSON name or the proto field name.
			fd = fieldDescs.ByJSONName(name)
			if fd == nil {
				fd = fieldDescs.ByTextName(name)
			}
		}

		if fd == nil {
			// Field is unknown.
			if d.opts.DiscardUnknown {
				if err := d.skipJSONValue(); err != nil {
					return err
				}
				continue
			}
			return d.newError("unknown field %q", name)
		}

		// Do not allow duplicate fields. Fields are identified by index, and
		// extensions by number.
		key := uint64(fd.Index())
		if fd.IsExtension() {
			key = 1<<32 | uint64(fd.Number())
		}
		if seenFields.has(key) {
			return d.newError("duplicate field %q", name)
		}
		seenFields.set(key)

		// No need to set values for JSON null unless the field type is
		// google.protobuf.Value or google.protobuf.NullValue.
		if d.dec.PeekKind() == kindNull && !isKnownValue(fd) && !isNullValue(fd) {
			if _, err := d.dec.ReadToken(); err != nil {
				return err
			}
			continue
		}

		switch {
		case fd.IsList():
			list := m.Mutable(fd).List()
			if err := d.unmarshalList(list, fd); err != nil {
				return err
			}
		case fd.IsMap():
			mmap := m.Mutable(fd).Map()
			if err := d.unmarshalMap(mmap, fd); err != nil {
				return err
			}
		default:
			// If field is a oneof, check if it has already been set.
			if od := fd.ContainingOneof(); od != nil {
				idx := uint64(od.Index())
				if seenOneofs.has(idx) {
					return d.newError("error parsing %q, oneof %v is already set", fd.TextName(), od.FullName())
				}
				seenOneofs.set(idx)
			}

			// Required or optional fields.
			if err := d.unmarshalSingular(m, fd); err != nil {
				return err
			}
		}
	}
}

func isKnownValue(fd protoreflect.FieldDescriptor) bool {
	md := fd.Message()
	return md != nil && md.FullName() == valueFullName
}

func isNullValue(fd protoreflect.FieldDescriptor) bool {
	ed := fd.Enum()
	return ed != nil && ed.FullName() == nullValueFullName
}

// unmarshalSingular unmarshals to the non-repeated field specified by the
// given FieldDescriptor.
func (d decoder) unmarshalSingular(m protoreflect.Message, fd protoreflect.FieldDescriptor) error {
	var val protoreflect.Value
	var err error
	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		val = m.NewField(fd)
		err = d.unmarshalMessage(val.Message(), false)
	default:
		val, err = d.unmarshalScalar(fd)
	}

	if err != nil {
		return err
	}
	if val.IsValid() {
		m.Set(fd, val)
	}
	return nil
}

// unmarshalScalar unmarshals to a scalar or enum protoreflect.Value specified
// by the given FieldDescriptor.
func (d decoder) unmarshalScalar(fd protoreflect.FieldDescriptor) (protoreflect.Value, error) {
	const b32 int = 32
	const b64 int = 64

	tok, err := d.readToken()
	if err != nil {
		return protoreflect.Value{}, err
	}

	kind := fd.Kind()
	switch kind {
	case protoreflect.BoolKind:
		switch tok.Kind() {
		case kindTrue:
			return protoreflect.ValueOfBool(true), nil
		case kindFalse:
			return protoreflect.ValueOfBool(false), nil
		}

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		if v, ok := unmarshalInt(tok, b32); ok {
			return v, nil
		}

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		if v, ok := unmarshalInt(tok, b64); ok {
			return v, nil
		}

	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		if v, ok := unmarshalUint(tok, b32); ok {
			return v, nil
		}

	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		if v, ok := unmarshalUint(tok, b64); ok {
			return v, nil
		}

	case protoreflect.FloatKind:
		if v, ok := unmarshalFloat(tok, b32); ok {
			return v, nil
		}

	case protoreflect.DoubleKind:
		if v, ok := unmarshalFloat(tok, b64); ok {
			return v, nil
		}

	case protoreflect.StringKind:
		if tok.Kind() == kindString {
			return protoreflect.ValueOfString(tok.String()), nil
		}

	case protoreflect.BytesKind:
		if v, ok := unmarshalBytes(tok); ok {
			return v, nil
		}

	case protoreflect.EnumKind:
		if v, ok := unmarshalEnum(tok, fd, d.opts.DiscardUnknown); ok {
			return v, nil
		}

	default:
		return protoreflect.Value{}, d.newError("invalid scalar kind %v", kind)
	}

	return protoreflect.Value{}, d.newError("invalid value for %v field %v: %v", kind, fd.JSONName(), tokenString(tok))
}

func unmarshalInt(tok jsontext.Token, bitSize int) (protoreflect.Value, bool) {
	var n int64
	switch tok.Kind() {
	case kindNumber:
		var err error
		if n, err = tok.Int(); err == nil {
			if bitSize == 32 && (n < math.MinInt32 || n > math.MaxInt32) {
				return protoreflect.Value{}, false
			}
			break
		}
		var ok bool
		if n, ok = parseInt(tok.String(), bitSize); !ok {
			return protoreflect.Value{}, false
		}
	case kindString:
		var ok bool
		if n, ok = parseInt(tok.String(), bitSize); !ok {
			return protoreflect.Value{}, false
		}
	default:
		return protoreflect.Value{}, false
	}
	if bitSize == 32 {
		return protoreflect.ValueOfInt32(int32(n)), true
	}
	return protoreflect.ValueOfInt64(n), true
}

func unmarshalUint(tok jsontext.Token, bitSize int) (protoreflect.Value, bool) {
	var n uint64
	switch tok.Kind() {
	case kindNumber:
		var err error
		if n, err = tok.Uint(); err == nil {
			if bitSize == 32 && n > math.MaxUint32 {
				return protoreflect.Value{}, false
			}
			break
		}
		var ok bool
		if n, ok = parseUint(tok.String(), bitSize); !ok {
			return protoreflect.Value{}, false
		}
	case kindString:
		var ok bool
		if n, ok = parseUint(tok.String(), bitSize); !ok {
			return protoreflect.Value{}, false
		}
	default:
		return protoreflect.Value{}, false
	}
	if bitSize == 32 {
		return protoreflect.ValueOfUint32(uint32(n)), true
	}
	return protoreflect.ValueOfUint64(n), true
}

func unmarshalFloat(tok jsontext.Token, bitSize int) (protoreflect.Value, bool) {
	switch tok.Kind() {
	case kindNumber:
		if bitSize == 32 {
			n, err := tok.Float32()
			if err != nil {
				return protoreflect.Value{}, false
			}
			return protoreflect.ValueOfFloat32(n), true
		}
		n, err := tok.Float()
		if err != nil {
			return protoreflect.Value{}, false
		}
		return protoreflect.ValueOfFloat64(n), true

	case kindString:
		s := tok.String()
		switch s {
		case "NaN":
			return floatValue(math.NaN(), bitSize), true
		case "Infinity":
			return floatValue(math.Inf(+1), bitSize), true
		case "-Infinity":
			return floatValue(math.Inf(-1), bitSize), true
		}
		if _, ok := parseNumberParts(s); !ok {
			return protoreflect.Value{}, false
		}
		n, err := strconv.ParseFloat(s, bitSize)
		if err != nil {
			return protoreflect.Value{}, false
		}
		return floatValue(n, bitSize), true

	default:
		return protoreflect.Value{}, false
	}
}

func floatValue(n float64, bitSize int) protoreflect.Value {
	if bitSize == 32 {
		return protoreflect.ValueOfFloat32(float32(n))
	}
	return protoreflect.ValueOfFloat64(n)
}

func unmarshalBytes(tok jsontext.Token) (protoreflect.Value, bool) {
	if tok.Kind() != kindString {
		return protoreflect.Value{}, false
	}

	s := tok.String()
	enc := base64.StdEncoding
	if strings.ContainsAny(s, "-_") {
		enc = base64.URLEncoding
	}
	if len(s)%4 != 0 {
		enc = enc.WithPadding(base64.NoPadding)
	}
	b, err := enc.DecodeString(s)
	if err != nil {
		return protoreflect.Value{}, false
	}
	return protoreflect.ValueOfBytes(b), true
}

func unmarshalEnum(tok jsontext.Token, fd protoreflect.FieldDescriptor, discardUnknown bool) (protoreflect.Value, bool) {
	switch tok.Kind() {
	case kindString:
		// Lookup EnumNumber based on name.
		if enumVal := fd.Enum().Values().ByName(protoreflect.Name(tok.String())); enumVal != nil {
			return protoreflect.ValueOfEnum(enumVal.Number()), true
		}
		if discardUnknown {
			return protoreflect.Value{}, true
		}

	case kindNumber:
		if v, ok := unmarshalInt(tok, 32); ok {
			return protoreflect.ValueOfEnum(protoreflect.EnumNumber(v.Int())), true
		}

	case kindNull:
		// This is only valid for google.protobuf.NullValue.
		if isNullValue(fd) {
			return protoreflect.ValueOfEnum(0), true
		}
	}

	return protoreflect.Value{}, false
}

func (d decoder) unmarshalList(list protoreflect.List, fd protoreflect.FieldDescriptor) error {
	tok, err := d.dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != kindArrayOpen {
		return d.unexpectedTokenError(tok)
	}

	switch fd.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		for {
			if d.dec.PeekKind() == kindArrayClose {
				_, err := d.dec.ReadToken()
				return err
			}
			val := list.NewElement()
			if err := d.unmarshalMessage(val.Message(), false); err != nil {
				return err
			}
			list.Append(val)
		}
	default:
		for {
			if d.dec.PeekKind() == kindArrayClose {
				_, err := d.dec.ReadToken()
				return err
			}
			val, err := d.unmarshalScalar(fd)
			if err != nil {
				return err
			}
			if val.IsValid() {
				list.Append(val)
			}
		}
	}
}

func (d decoder) unmarshalMap(mmap protoreflect.Map, fd protoreflect.FieldDescriptor) error {
	tok, err := d.dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != kindObjectOpen {
		return d.unexpectedTokenError(tok)
	}

	// Determine ahead whether map entry is a scalar type or a message type in
	// order to call the appropriate unmarshalMapValue func inside the for loop
	// below.
	var unmarshalMapValue func() (protoreflect.Value, error)
	switch fd.MapValue().Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		unmarshalMapValue = func() (protoreflect.Value, error) {
			val := mmap.NewValue()
			if err := d.unmarshalMessage(val.Message(), false); err != nil {
				return protoreflect.Value{}, err
			}
			return val, nil
		}
	default:
		unmarshalMapValue = func() (protoreflect.Value, error) {
			return d.unmarshalScalar(fd.MapValue())
		}
	}

	for {
		// Read field name.
		if d.dec.PeekKind() == kindObjectClose {
			_, err := d.dec.ReadToken()
			return err
		}
		name, err := d.readString()
		if err != nil {
			return err
		}

		// Unmarshal field name.
		pkey, err := d.unmarshalMapKey(name, fd.MapKey())
		if err != nil {
			return err
		}

		// Check for duplicate field name.
		if mmap.Has(pkey) {
			return d.newError("duplicate map key %q", name)
		}

		// Read and unmarshal field value.
		pval, err := unmarshalMapValue()
		if err != nil {
			return err
		}
		if pval.IsValid() {
			mmap.Set(pkey, pval)
		}
	}
}

// unmarshalMapKey converts the given JSON object name into a
// protoreflect.MapKey. A map key type is any integral or string type.
func (d decoder) unmarshalMapKey(name string, fd protoreflect.FieldDescriptor) (protoreflect.MapKey, error) {
	const b32 = 32
	const b64 = 64
	const base10 = 10

	kind := fd.Kind()
	switch kind {
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(name).MapKey(), nil

	case protoreflect.BoolKind:
		switch name {
		case "true":
			return protoreflect.ValueOfBool(true).MapKey(), nil
		case "false":
			return protoreflect.ValueOfBool(false).MapKey(), nil
		}

	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		if n, err := strconv.ParseInt(name, base10, b32); err == nil {
			return protoreflect.ValueOfInt32(int32(n)).MapKey(), nil
		}

	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		if n, err := strconv.ParseInt(name, base10, b64); err == nil {
			return protoreflect.ValueOfInt64(n).MapKey(), nil
		}

	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		if n, err := strconv.ParseUint(name, base10, b32); err == nil {
			return protoreflect.ValueOfUint32(uint32(n)).MapKey(), nil
		}

	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		if n, err := strconv.ParseUint(name, base10, b64); err == nil {
			return protoreflect.ValueOfUint64(n).MapKey(), nil
		}

	default:
		return protoreflect.MapKey{}, d.newError("invalid kind for map key: %v", kind)
	}

	return protoreflect.MapKey{}, d.newError("invalid value for %v key: %q", kind, name)
}

// skipJSONValue reads one JSON value (null, boolean, string, number, object,
// or array) and drops it. Like protojson, it limits the nesting depth to the
// remaining recursion limit.
func (d decoder) skipJSONValue() error {
	var open int
	for {
		tok, err := d.readToken()
		if err != nil {
			return err
		}
		switch tok.Kind() {
		case kindObjectClose, kindArrayClose:
			open--
		case kindObjectOpen, kindArrayOpen:
			open++
			if open > d.opts.RecursionLimit {
				return errorf("exceeded max recursion depth")
			}
		}
		if open == 0 {
			return nil
		}
	}
}

// fieldSet is a set of field keys. Keys below 256 use a bitset with no
// allocation, which covers the fields of almost all messages.
type fieldSet struct {
	lo [4]uint64
	hi map[uint64]struct{}
}

func (s *fieldSet) has(n uint64) bool {
	if n < 256 {
		return s.lo[n/64]&(1<<(n%64)) != 0
	}
	_, ok := s.hi[n]
	return ok
}

func (s *fieldSet) set(n uint64) {
	if n < 256 {
		s.lo[n/64] |= 1 << (n % 64)
		return
	}
	if s.hi == nil {
		s.hi = make(map[uint64]struct{})
	}
	s.hi[n] = struct{}{}
}

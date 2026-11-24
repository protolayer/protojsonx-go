// Copyright 2019 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE-protobuf-go file.
//
// This file is a port of google.golang.org/protobuf/encoding/protojson to
// encoding/json/jsontext.

package jsonwire

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Names and field numbers of the well-known types.
const (
	googleProtobufPackage protoreflect.FullName = "google.protobuf"

	anyFullName       protoreflect.FullName = "google.protobuf.Any"
	emptyFullName     protoreflect.FullName = "google.protobuf.Empty"
	valueFullName     protoreflect.FullName = "google.protobuf.Value"
	nullValueFullName protoreflect.FullName = "google.protobuf.NullValue"
	durationFullName  protoreflect.FullName = "google.protobuf.Duration"
	timestampFullName protoreflect.FullName = "google.protobuf.Timestamp"

	anyTypeURLFieldNumber   protoreflect.FieldNumber = 1
	anyValueFieldNumber     protoreflect.FieldNumber = 2
	wrapperValueFieldNumber protoreflect.FieldNumber = 1
	structFieldsFieldNumber protoreflect.FieldNumber = 1
	listValuesFieldNumber   protoreflect.FieldNumber = 1
	fieldMaskPathsNumber    protoreflect.FieldNumber = 1
	secondsFieldNumber      protoreflect.FieldNumber = 1
	nanosFieldNumber        protoreflect.FieldNumber = 2

	valueNullValueFieldNumber   protoreflect.FieldNumber = 1
	valueNumberValueFieldNumber protoreflect.FieldNumber = 2
	valueStringValueFieldNumber protoreflect.FieldNumber = 3
	valueBoolValueFieldNumber   protoreflect.FieldNumber = 4
	valueStructValueFieldNumber protoreflect.FieldNumber = 5
	valueListValueFieldNumber   protoreflect.FieldNumber = 6
)

type marshalFunc func(encoder, protoreflect.Message) error

// wellKnownTypeMarshaler returns a marshal function if the message type has
// specialized serialization behavior. It returns nil otherwise.
func wellKnownTypeMarshaler(name protoreflect.FullName) marshalFunc {
	if name.Parent() != googleProtobufPackage {
		return nil
	}
	switch name.Name() {
	case "Any":
		return encoder.marshalAny
	case "Timestamp":
		return encoder.marshalTimestamp
	case "Duration":
		return encoder.marshalDuration
	case "BoolValue", "Int32Value", "Int64Value", "UInt32Value", "UInt64Value",
		"FloatValue", "DoubleValue", "StringValue", "BytesValue":
		return encoder.marshalWrapperType
	case "Struct":
		return encoder.marshalStruct
	case "ListValue":
		return encoder.marshalListValue
	case "Value":
		return encoder.marshalKnownValue
	case "FieldMask":
		return encoder.marshalFieldMask
	}
	// The spec explicitly specifies that the Empty message is not considered
	// to have any special JSON mapping.
	return nil
}

type unmarshalFunc func(decoder, protoreflect.Message) error

// wellKnownTypeUnmarshaler returns a unmarshal function if the message type
// has specialized serialization behavior. It returns nil otherwise.
func wellKnownTypeUnmarshaler(name protoreflect.FullName) unmarshalFunc {
	if name.Parent() != googleProtobufPackage {
		return nil
	}
	switch name.Name() {
	case "Any":
		return decoder.unmarshalAny
	case "Timestamp":
		return decoder.unmarshalTimestamp
	case "Duration":
		return decoder.unmarshalDuration
	case "BoolValue", "Int32Value", "Int64Value", "UInt32Value", "UInt64Value",
		"FloatValue", "DoubleValue", "StringValue", "BytesValue":
		return decoder.unmarshalWrapperType
	case "Struct":
		return decoder.unmarshalStruct
	case "ListValue":
		return decoder.unmarshalListValue
	case "Value":
		return decoder.unmarshalKnownValue
	case "FieldMask":
		return decoder.unmarshalFieldMask
	case "Empty":
		return decoder.unmarshalEmpty
	}
	return nil
}

// The JSON representation of an Any message uses the regular representation
// of the deserialized, embedded message, with an additional field `@type`
// which contains the type URL. If the embedded message type is well-known and
// has a custom JSON representation, that representation will be embedded
// adding a field `value` which holds the custom JSON in addition to the
// `@type` field.

func (e encoder) marshalAny(m protoreflect.Message) error {
	fds := m.Descriptor().Fields()
	fdType := fds.ByNumber(anyTypeURLFieldNumber)
	fdValue := fds.ByNumber(anyValueFieldNumber)

	if !m.Has(fdType) {
		if !m.Has(fdValue) {
			// If message is empty, marshal out empty JSON object.
			if err := e.enc.WriteToken(jsontext.BeginObject); err != nil {
				return err
			}
			return e.enc.WriteToken(jsontext.EndObject)
		}
		// Return error if type_url field is not set, but value is set.
		return errorf("%s: type_url is not set", anyFullName)
	}

	typeVal := m.Get(fdType)
	valueVal := m.Get(fdValue)

	// Resolve the type in order to unmarshal value field.
	typeURL := typeVal.String()
	emt, err := e.opts.Resolver.FindMessageByURL(typeURL)
	if err != nil {
		return errorf("%s: unable to resolve %q: %v", anyFullName, typeURL, err)
	}

	em := emt.New()
	err = proto.UnmarshalOptions{
		AllowPartial: true, // never check required fields inside an Any
		Resolver:     e.opts.Resolver,
	}.Unmarshal(valueVal.Bytes(), em.Interface())
	if err != nil {
		return errorf("%s: unable to unmarshal %q: %v", anyFullName, typeURL, err)
	}

	// If type of value has custom JSON encoding, marshal out a field "value"
	// with corresponding custom JSON encoding of the embedded message as a
	// field.
	if marshal := wellKnownTypeMarshaler(emt.Descriptor().FullName()); marshal != nil {
		if err := e.enc.WriteToken(jsontext.BeginObject); err != nil {
			return err
		}
		if err := e.enc.WriteToken(jsontext.String("@type")); err != nil {
			return err
		}
		if err := e.writeString(typeURL, string(anyFullName)+".type_url"); err != nil {
			return err
		}
		if err := e.enc.WriteToken(jsontext.String("value")); err != nil {
			return err
		}
		if err := marshal(e, em); err != nil {
			return err
		}
		return e.enc.WriteToken(jsontext.EndObject)
	}

	// Else, marshal out the embedded message's fields in this Any object.
	return e.marshalMessage(em, typeURL)
}

func (d decoder) unmarshalAny(m protoreflect.Message) error {
	// Check for an object before reading the value, so that the error is the
	// same as for other messages.
	if d.dec.PeekKind() != kindObjectOpen {
		tok, err := d.dec.ReadToken()
		if err != nil {
			return err
		}
		return d.unexpectedTokenError(tok)
	}

	// Read the whole object. The "@type" field can come after the fields of
	// the embedded message, so the object is read two times: first to find
	// the type, then to decode the fields.
	// raw refers to decoder memory. It stays valid because this function does
	// not read from d.dec again, and the sub-decoders parse raw in place, so
	// nested Any values do not copy their input.
	raw, err := d.dec.ReadValue()
	if err != nil {
		return err
	}

	// One sub-decoder reads raw two times: first to find the type, then to
	// decode the fields.
	sub := d.subDecoder(raw)
	typeURL, err := findTypeURL(sub)
	switch {
	case errors.Is(err, errEmptyObject):
		// An empty JSON object translates to an empty Any message.
		return nil

	case errors.Is(err, errMissingType):
		if d.opts.DiscardUnknown {
			// Treat all fields as unknowns, similar to an empty object. Skip
			// the value again to apply the same depth check as protojson.
			sub.reset(raw)
			return sub.skipJSONValue()
		}
		return d.newError("%v", err)

	case err != nil:
		return err
	}

	emt, err := d.opts.Resolver.FindMessageByURL(typeURL)
	if err != nil {
		return d.newError("unable to resolve %q: %v", typeURL, err)
	}

	// Create new message for the embedded message type and unmarshal into it.
	em := emt.New()
	sub.reset(raw)
	if unmarshal := wellKnownTypeUnmarshaler(emt.Descriptor().FullName()); unmarshal != nil {
		// If embedded message is a custom type, unmarshal the JSON "value"
		// field into it.
		if err := sub.unmarshalAnyValue(unmarshal, em); err != nil {
			return err
		}
	} else {
		// Else unmarshal the current JSON object into it.
		if err := sub.unmarshalMessage(em, true); err != nil {
			return err
		}
	}
	// Serialize the embedded message and assign the resulting bytes to the
	// proto value field.
	b, err := proto.MarshalOptions{
		AllowPartial:  true, // No need to check required fields inside an Any.
		Deterministic: true,
	}.Marshal(em.Interface())
	if err != nil {
		return d.newError("error in marshaling Any.value field: %v", err)
	}

	fds := m.Descriptor().Fields()
	fdType := fds.ByNumber(anyTypeURLFieldNumber)
	fdValue := fds.ByNumber(anyValueFieldNumber)

	m.Set(fdType, protoreflect.ValueOfString(typeURL))
	m.Set(fdValue, protoreflect.ValueOfBytes(b))
	return nil
}

var (
	errEmptyObject = errors.New(`empty object`)
	errMissingType = errors.New(`missing "@type" field`)
)

// findTypeURL returns the "@type" field value from the JSON object that d
// reads. It returns errEmptyObject if the JSON object is empty or
// errMissingType if the @type field does not exist. It returns other errors if
// the @type field is not valid or for other decoding issues.
func findTypeURL(d decoder) (string, error) {
	var typeURL string
	numFields := 0
	// Skip start object.
	if _, err := d.dec.ReadToken(); err != nil {
		return "", err
	}

	for {
		if d.dec.PeekKind() == kindObjectClose {
			if typeURL == "" {
				// Did not find @type field.
				if numFields > 0 {
					return "", errMissingType
				}
				return "", errEmptyObject
			}
			return typeURL, nil
		}
		name, err := d.readString()
		if err != nil {
			return "", err
		}
		numFields++
		if name != "@type" {
			// Skip value.
			if err := d.skipJSONValue(); err != nil {
				return "", err
			}
			continue
		}

		// Return error if this was previously set already.
		if typeURL != "" {
			return "", d.newError(`duplicate "@type" field`)
		}
		// Read field value.
		tok, err := d.readToken()
		if err != nil {
			return "", err
		}
		if tok.Kind() != kindString {
			return "", d.newError(`@type field value is not a string: %v`, tokenString(tok))
		}
		typeURL = tok.String()
		if typeURL == "" {
			return "", d.newError(`@type field contains empty value`)
		}
	}
}

// unmarshalAnyValue unmarshals the given custom-type message from the JSON
// object's "value" field.
func (d decoder) unmarshalAnyValue(unmarshal unmarshalFunc, m protoreflect.Message) error {
	// Skip ObjectOpen, and start reading the fields.
	if _, err := d.dec.ReadToken(); err != nil {
		return err
	}

	var found bool // Used for detecting duplicate "value".
	for {
		if d.dec.PeekKind() == kindObjectClose {
			if _, err := d.dec.ReadToken(); err != nil {
				return err
			}
			if !found {
				// We tolerate an omitted `value` field with the
				// google.protobuf.Empty Well-Known-Type, for compatibility
				// with other proto runtimes that have interpreted the spec
				// differently.
				if m.Descriptor().FullName() != emptyFullName {
					return d.newError(`missing "value" field`)
				}
			}
			return nil
		}
		name, err := d.readString()
		if err != nil {
			return err
		}
		switch name {
		case "@type":
			// Skip the value as this was previously parsed already.
			if err := d.dec.SkipValue(); err != nil {
				return err
			}

		case "value":
			if found {
				return d.newError(`duplicate "value" field`)
			}
			// Unmarshal the field value into the given message.
			if err := unmarshal(d, m); err != nil {
				return err
			}
			found = true

		default:
			if d.opts.DiscardUnknown {
				if err := d.skipJSONValue(); err != nil {
					return err
				}
				continue
			}
			return d.newError("unknown field %q", name)
		}
	}
}

// Wrapper types are encoded as JSON primitives like string, number or boolean.

func (e encoder) marshalWrapperType(m protoreflect.Message) error {
	fd := m.Descriptor().Fields().ByNumber(wrapperValueFieldNumber)
	val := m.Get(fd)
	return e.marshalSingular(val, fd)
}

func (d decoder) unmarshalWrapperType(m protoreflect.Message) error {
	fd := m.Descriptor().Fields().ByNumber(wrapperValueFieldNumber)
	val, err := d.unmarshalScalar(fd)
	if err != nil {
		return err
	}
	m.Set(fd, val)
	return nil
}

// The JSON representation for Empty is an empty JSON object.

func (d decoder) unmarshalEmpty(protoreflect.Message) error {
	tok, err := d.dec.ReadToken()
	if err != nil {
		return err
	}
	if tok.Kind() != kindObjectOpen {
		return d.unexpectedTokenError(tok)
	}

	for {
		if d.dec.PeekKind() == kindObjectClose {
			_, err := d.dec.ReadToken()
			return err
		}
		name, err := d.readString()
		if err != nil {
			return err
		}
		if d.opts.DiscardUnknown {
			if err := d.skipJSONValue(); err != nil {
				return err
			}
			continue
		}
		return d.newError("unknown field %q", name)
	}
}

// The JSON representation for Struct is a JSON object that contains the
// encoded Struct.fields map and follows the serialization rules for a map.

func (e encoder) marshalStruct(m protoreflect.Message) error {
	fd := m.Descriptor().Fields().ByNumber(structFieldsFieldNumber)
	return e.marshalMap(m.Get(fd).Map(), fd)
}

func (d decoder) unmarshalStruct(m protoreflect.Message) error {
	fd := m.Descriptor().Fields().ByNumber(structFieldsFieldNumber)
	return d.unmarshalMap(m.Mutable(fd).Map(), fd)
}

// The JSON representation for ListValue is JSON array that contains the
// encoded ListValue.values repeated field and follows the serialization rules
// for a repeated field.

func (e encoder) marshalListValue(m protoreflect.Message) error {
	fd := m.Descriptor().Fields().ByNumber(listValuesFieldNumber)
	return e.marshalList(m.Get(fd).List(), fd)
}

func (d decoder) unmarshalListValue(m protoreflect.Message) error {
	fd := m.Descriptor().Fields().ByNumber(listValuesFieldNumber)
	return d.unmarshalList(m.Mutable(fd).List(), fd)
}

// The JSON representation for a Value is dependent on the oneof field that is
// set. Each of the field in the oneof has its own custom serialization rule. A
// Value message needs to be a oneof field set, else it is an error.

func (e encoder) marshalKnownValue(m protoreflect.Message) error {
	od := m.Descriptor().Oneofs().ByName("kind")
	fd := m.WhichOneof(od)
	if fd == nil {
		return errorf("%s: none of the oneof fields is set", valueFullName)
	}
	if fd.Number() == valueNumberValueFieldNumber {
		if v := m.Get(fd).Float(); math.IsNaN(v) || math.IsInf(v, 0) {
			return errorf("%s.number_value: invalid %v value", valueFullName, v)
		}
	}
	return e.marshalSingular(m.Get(fd), fd)
}

func (d decoder) unmarshalKnownValue(m protoreflect.Message) error {
	var fd protoreflect.FieldDescriptor
	var val protoreflect.Value
	fields := m.Descriptor().Fields()
	switch kind := d.dec.PeekKind(); kind {
	case kindNull:
		if _, err := d.dec.ReadToken(); err != nil {
			return err
		}
		fd = fields.ByNumber(valueNullValueFieldNumber)
		val = protoreflect.ValueOfEnum(0)

	case kindTrue, kindFalse:
		if _, err := d.dec.ReadToken(); err != nil {
			return err
		}
		fd = fields.ByNumber(valueBoolValueFieldNumber)
		val = protoreflect.ValueOfBool(kind == kindTrue)

	case kindNumber:
		tok, err := d.readToken()
		if err != nil {
			return err
		}
		fd = fields.ByNumber(valueNumberValueFieldNumber)
		var ok bool
		val, ok = unmarshalFloat(tok, 64)
		if !ok {
			return d.newError("invalid %v: %v", valueFullName, tokenString(tok))
		}

	case kindString:
		// A JSON string may have been encoded from the number_value field,
		// e.g. "NaN", "Infinity", etc. Parsing a proto double type also
		// allows for it to be in JSON string form. Given this custom encoding
		// spec, however, there is no way to identify that and hence a JSON
		// string is always assigned to the string_value field, which means
		// that certain encoding cannot be parsed back to the same field.
		s, err := d.readString()
		if err != nil {
			return err
		}
		fd = fields.ByNumber(valueStringValueFieldNumber)
		val = protoreflect.ValueOfString(s)

	case kindObjectOpen:
		fd = fields.ByNumber(valueStructValueFieldNumber)
		val = m.NewField(fd)
		if err := d.unmarshalStruct(val.Message()); err != nil {
			return err
		}

	case kindArrayOpen:
		fd = fields.ByNumber(valueListValueFieldNumber)
		val = m.NewField(fd)
		if err := d.unmarshalListValue(val.Message()); err != nil {
			return err
		}

	default:
		tok, err := d.dec.ReadToken()
		if err != nil {
			return err
		}
		return d.newError("invalid %v: %v", valueFullName, tokenString(tok))
	}

	m.Set(fd, val)
	return nil
}

// The JSON representation for a Duration is a JSON string that ends in the
// suffix "s" (indicating seconds) and is preceded by the number of seconds,
// with nanoseconds expressed as fractional seconds.
//
// Durations less than one second are represented with a 0 seconds field and a
// positive or negative nanos field. For durations of one second or more, a
// non-zero value for the nanos field must be of the same sign as the seconds
// field.
//
// Duration.seconds must be from -315,576,000,000 to +315,576,000,000
// inclusive. Duration.nanos must be from -999,999,999 to +999,999,999
// inclusive.

const (
	secondsInNanos       = 999999999
	maxSecondsInDuration = 315576000000
)

func (e encoder) marshalDuration(m protoreflect.Message) error {
	fds := m.Descriptor().Fields()
	fdSeconds := fds.ByNumber(secondsFieldNumber)
	fdNanos := fds.ByNumber(nanosFieldNumber)

	secs := m.Get(fdSeconds).Int()
	nanos := m.Get(fdNanos).Int()
	if secs < -maxSecondsInDuration || secs > maxSecondsInDuration {
		return errorf("%s: seconds out of range %v", durationFullName, secs)
	}
	if nanos < -secondsInNanos || nanos > secondsInNanos {
		return errorf("%s: nanos out of range %v", durationFullName, nanos)
	}
	if (secs > 0 && nanos < 0) || (secs < 0 && nanos > 0) {
		return errorf("%s: signs of seconds and nanos do not match", durationFullName)
	}
	// Generated output always contains 0, 3, 6, or 9 fractional digits,
	// depending on required precision, followed by the suffix "s". The value
	// is formatted into the encoder buffer, so it does not allocate.
	b := e.enc.AvailableBuffer()
	b = append(b, '"')
	if secs < 0 || nanos < 0 {
		b = append(b, '-')
		secs, nanos = -1*secs, -1*nanos
	}
	b = strconv.AppendInt(b, secs, 10)
	b = append(b, '.')
	b = appendNanos(b, nanos)
	b = trimFraction(b)
	b = append(b, 's', '"')
	return e.enc.WriteValue(b)
}

// appendNanos appends n as exactly 9 decimal digits.
func appendNanos(b []byte, n int64) []byte {
	var digits [9]byte
	for i := len(digits) - 1; i >= 0; i-- {
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return append(b, digits[:]...)
}

// trimFraction removes trailing zeros from a 9-digit fraction in groups of 3,
// and removes the fraction if it is zero. This gives 0, 3, 6, or 9 digits.
func trimFraction(b []byte) []byte {
	b = bytes.TrimSuffix(b, []byte("000"))
	b = bytes.TrimSuffix(b, []byte("000"))
	return bytes.TrimSuffix(b, []byte(".000"))
}

func (d decoder) unmarshalDuration(m protoreflect.Message) error {
	s, err := d.readString()
	if err != nil {
		return err
	}

	secs, nanos, ok := parseDuration(s)
	if !ok {
		return d.newError("invalid %v value %q", durationFullName, s)
	}
	// Validate seconds. No need to validate nanos because parseDuration would
	// have covered that already.
	if secs < -maxSecondsInDuration || secs > maxSecondsInDuration {
		return d.newError("%v value out of range: %q", durationFullName, s)
	}

	fds := m.Descriptor().Fields()
	m.Set(fds.ByNumber(secondsFieldNumber), protoreflect.ValueOfInt64(secs))
	m.Set(fds.ByNumber(nanosFieldNumber), protoreflect.ValueOfInt32(nanos))
	return nil
}

// parseDuration parses the given input string for seconds and nanoseconds
// value for the Duration JSON format. The format is a decimal number with a
// suffix 's'. It can have optional plus/minus sign. There needs to be at least
// an integer or fractional part. Fractional part is limited to 9 digits only
// for nanoseconds precision, regardless of whether there are trailing zero
// digits. Example values are 1s, 0.1s, 1.s, .1s, +1s, -1s, -.1s.
func parseDuration(input string) (int64, int32, bool) {
	b := []byte(input)
	size := len(b)
	if size < 2 {
		return 0, 0, false
	}
	if b[size-1] != 's' {
		return 0, 0, false
	}
	b = b[:size-1]

	// Read optional plus/minus symbol.
	var neg bool
	switch b[0] {
	case '-':
		neg = true
		b = b[1:]
	case '+':
		b = b[1:]
	}
	if len(b) == 0 {
		return 0, 0, false
	}

	// Read the integer part.
	var intp []byte
	switch {
	case b[0] == '0':
		b = b[1:]

	case '1' <= b[0] && b[0] <= '9':
		intp = b[0:]
		b = b[1:]
		n := 1
		for len(b) > 0 && isDigit(b[0]) {
			n++
			b = b[1:]
		}
		intp = intp[:n]

	case b[0] == '.':
		// Continue below.

	default:
		return 0, 0, false
	}

	var nanos int32
	if len(b) > 0 {
		if b[0] != '.' {
			return 0, 0, false
		}
		// Read the fractional part.
		b = b[1:]
		n := 0
		for len(b) > 0 && n < 9 && isDigit(b[0]) {
			nanos = nanos*10 + int32(b[0]-'0')
			n++
			b = b[1:]
		}
		// It is not valid if there are more bytes left.
		if len(b) > 0 {
			return 0, 0, false
		}
		// Pad fractional part with 0s.
		for i := n; i < 9; i++ {
			nanos *= 10
		}
	}

	var secs int64
	if len(intp) > 0 {
		var err error
		secs, err = strconv.ParseInt(string(intp), 10, 64)
		if err != nil {
			return 0, 0, false
		}
	}

	if neg {
		secs = -secs
		nanos = -nanos
	}
	return secs, nanos, true
}

// The JSON representation for a Timestamp is a JSON string in the RFC 3339
// format, i.e. "{year}-{month}-{day}T{hour}:{min}:{sec}[.{frac_sec}]Z" where
// {year} is always expressed using four digits while {month}, {day}, {hour},
// {min}, and {sec} are zero-padded to two digits each. The fractional seconds,
// which can go up to 9 digits, up to 1 nanosecond resolution, is optional. The
// "Z" suffix indicates the timezone ("UTC"); the timezone is required.
// Encoding should always use UTC (as indicated by "Z") and a decoder should be
// able to accept both UTC and other timezones (as indicated by an offset).
//
// Timestamp.seconds must be from 0001-01-01T00:00:00Z to 9999-12-31T23:59:59Z
// inclusive. Timestamp.nanos must be from 0 to 999,999,999 inclusive.

const (
	maxTimestampSeconds = 253402300799
	minTimestampSeconds = -62135596800
)

func (e encoder) marshalTimestamp(m protoreflect.Message) error {
	fds := m.Descriptor().Fields()
	secs := m.Get(fds.ByNumber(secondsFieldNumber)).Int()
	nanos := m.Get(fds.ByNumber(nanosFieldNumber)).Int()
	if secs < minTimestampSeconds || secs > maxTimestampSeconds {
		return errorf("%s: seconds out of range %v", timestampFullName, secs)
	}
	if nanos < 0 || nanos > secondsInNanos {
		return errorf("%s: nanos out of range %v", timestampFullName, nanos)
	}
	// Uses RFC 3339, where generated output will be Z-normalized and uses 0,
	// 3, 6 or 9 fractional digits.
	t := time.Unix(secs, nanos).UTC()
	b := e.enc.AvailableBuffer()
	b = append(b, '"')
	b = t.AppendFormat(b, "2006-01-02T15:04:05.000000000")
	b = trimFraction(b)
	b = append(b, 'Z', '"')
	return e.enc.WriteValue(b)
}

func (d decoder) unmarshalTimestamp(m protoreflect.Message) error {
	s, err := d.readString()
	if err != nil {
		return err
	}

	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return d.newError("invalid %v value %q", timestampFullName, s)
	}
	// Validate seconds.
	secs := t.Unix()
	if secs < minTimestampSeconds || secs > maxTimestampSeconds {
		return d.newError("%v value out of range: %q", timestampFullName, s)
	}
	// Validate subseconds.
	i := strings.LastIndexByte(s, '.')  // start of subsecond field
	j := strings.LastIndexAny(s, "Z-+") // start of timezone field
	if i >= 0 && j >= i && j-i > len(".999999999") {
		return d.newError("invalid %v value %q", timestampFullName, s)
	}

	fds := m.Descriptor().Fields()
	m.Set(fds.ByNumber(secondsFieldNumber), protoreflect.ValueOfInt64(secs))
	m.Set(fds.ByNumber(nanosFieldNumber), protoreflect.ValueOfInt32(int32(t.Nanosecond())))
	return nil
}

// The JSON representation for a FieldMask is a JSON string where paths are
// separated by a comma. Fields name in each path are converted to/from
// lower-camel naming conventions. Encoding should fail if the path name would
// end up differently after a round-trip.

func (e encoder) marshalFieldMask(m protoreflect.Message) error {
	fd := m.Descriptor().Fields().ByNumber(fieldMaskPathsNumber)
	list := m.Get(fd).List()
	paths := make([]string, 0, list.Len())

	for i := range list.Len() {
		s := list.Get(i).String()
		if !protoreflect.FullName(s).IsValid() {
			return errorf("%s contains invalid path: %q", fd.FullName(), s)
		}
		// Return error if conversion to camelCase is not reversible.
		cc := jsonCamelCase(s)
		if s != jsonSnakeCase(cc) {
			return errorf("%s contains irreversible value %q", fd.FullName(), s)
		}
		paths = append(paths, cc)
	}

	return e.enc.WriteToken(jsontext.String(strings.Join(paths, ",")))
}

func (d decoder) unmarshalFieldMask(m protoreflect.Message) error {
	s, err := d.readString()
	if err != nil {
		return err
	}
	str := strings.TrimSpace(s)
	if str == "" {
		return nil
	}
	paths := strings.Split(str, ",")

	fd := m.Descriptor().Fields().ByNumber(fieldMaskPathsNumber)
	list := m.Mutable(fd).List()

	for _, s0 := range paths {
		s := jsonSnakeCase(s0)
		if strings.Contains(s0, "_") || !protoreflect.FullName(s).IsValid() {
			return d.newError("%v contains invalid path: %q", fd.FullName(), s0)
		}
		list.Append(protoreflect.ValueOfString(s))
	}
	return nil
}

// jsonCamelCase converts a snake_case identifier to a camelCase identifier,
// according to the protobuf JSON specification.
func jsonCamelCase(s string) string {
	var b []byte
	var wasUnderscore bool
	for i := range len(s) { // proto identifiers are always ASCII
		c := s[i]
		if c != '_' {
			if wasUnderscore && 'a' <= c && c <= 'z' {
				c -= 'a' - 'A' // convert to uppercase
			}
			b = append(b, c)
		}
		wasUnderscore = c == '_'
	}
	return string(b)
}

// jsonSnakeCase converts a camelCase identifier to a snake_case identifier,
// according to the protobuf JSON specification.
func jsonSnakeCase(s string) string {
	var b []byte
	for i := range len(s) { // proto identifiers are always ASCII
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			b = append(b, '_')
			c += 'a' - 'A' // convert to lowercase
		}
		b = append(b, c)
	}
	return string(b)
}

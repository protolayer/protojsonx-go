package testutil

import (
	"math"
	"math/rand/v2"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/protolayer/jsonwire-go/internal/gen/conformance/test_protos"
	"github.com/protolayer/jsonwire-go/internal/gen/editions/golden"
	testpb "github.com/protolayer/jsonwire-go/internal/gen/google/protobuf"
	editionspb "github.com/protolayer/jsonwire-go/internal/gen/jsonwire/test"
)

// Messages are the message types that the tests use. They cover proto2,
// proto3, and editions, with all field kinds, well-known types, extensions,
// groups and delimited fields, closed enums, and required fields.
var Messages = []func() proto.Message{
	func() proto.Message { return new(testpb.TestAllTypesProto3) },
	func() proto.Message { return new(testpb.TestAllTypesProto2) },
	func() proto.Message { return new(testpb.TestAllRequiredTypesProto2) },
	func() proto.Message { return new(golden.TestAllTypesProto3) },
	func() proto.Message { return new(golden.TestAllTypesProto2) },
	func() proto.Message { return new(test_protos.TestAllTypesEdition2023) },
	func() proto.Message { return new(editionspb.Editions) },
	// Dynamic messages use a different implementation of protoreflect.
	func() proto.Message {
		return dynamicpb.NewMessage((*testpb.TestAllTypesProto3)(nil).ProtoReflect().Descriptor())
	},
	func() proto.Message {
		return dynamicpb.NewMessage((*testpb.TestAllTypesProto2)(nil).ProtoReflect().Descriptor())
	},
	func() proto.Message {
		return dynamicpb.NewMessage((*editionspb.Editions)(nil).ProtoReflect().Descriptor())
	},
}

// Generator fills messages with random values. Most values are valid, but some
// are not, for example strings with invalid UTF-8 or timestamps out of range.
// For those values, both protojson and jsonwire must return an error.
type Generator struct {
	r *rand.Rand
	// invalid is the chance (0 to 1) of a value that cannot be marshaled.
	Invalid float64
}

// NewGenerator returns a Generator with a fixed seed, so that a test can
// repeat a run.
func NewGenerator(seed uint64) *Generator {
	return &Generator{r: rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15)), Invalid: 0.01}
}

func (g *Generator) chance(p float64) bool { return g.r.Float64() < p }

// MaxDepth is the depth at which the Generator stops making nested messages.
const MaxDepth = 3

// Message fills m with random fields. depth is the nesting depth of m.
func (g *Generator) Message(m protoreflect.Message, depth int) {
	if g.wellKnown(m, depth) {
		return
	}
	md := m.Descriptor()
	fill := 0.5
	if depth >= MaxDepth {
		fill = 0.05
	}
	fields := md.Fields()
	setOneofs := map[protoreflect.FullName]bool{}
	for i := range fields.Len() {
		fd := fields.Get(i)
		if od := fd.ContainingOneof(); od != nil && !od.IsSynthetic() {
			if setOneofs[od.FullName()] {
				continue
			}
			if !g.chance(fill / 2) {
				continue
			}
			setOneofs[od.FullName()] = true
		} else if fd.Cardinality() == protoreflect.Required {
			// Required fields are almost always set, so that most messages
			// marshal without AllowPartial.
			if g.chance(g.Invalid) {
				continue
			}
		} else if !g.chance(fill) {
			continue
		}
		if depth >= MaxDepth && fd.Message() != nil {
			// Stop the recursion, also for required fields. A message type
			// with a required field of its own type can never be complete.
			continue
		}
		g.field(m, fd, depth)
	}
	if md.ExtensionRanges().Len() > 0 {
		protoregistry.GlobalTypes.RangeExtensionsByMessage(md.FullName(), func(xt protoreflect.ExtensionType) bool {
			xd := xt.TypeDescriptor()
			if g.chance(fill/2) && (depth < MaxDepth || xd.Message() == nil) {
				g.field(m, xd, depth)
			}
			return true
		})
	}
}

func (g *Generator) field(m protoreflect.Message, fd protoreflect.FieldDescriptor, depth int) {
	switch {
	case fd.IsList():
		list := m.Mutable(fd).List()
		for range g.r.IntN(4) {
			if fd.Message() != nil {
				v := list.NewElement()
				g.Message(v.Message(), depth+1)
				list.Append(v)
			} else {
				list.Append(g.scalar(fd))
			}
		}
	case fd.IsMap():
		mmap := m.Mutable(fd).Map()
		for range g.r.IntN(4) {
			k := g.scalar(fd.MapKey()).MapKey()
			if fd.MapValue().Message() != nil {
				v := mmap.NewValue()
				g.Message(v.Message(), depth+1)
				mmap.Set(k, v)
			} else {
				mmap.Set(k, g.scalar(fd.MapValue()))
			}
		}
	case fd.Message() != nil:
		v := m.NewField(fd)
		g.Message(v.Message(), depth+1)
		m.Set(fd, v)
	default:
		m.Set(fd, g.scalar(fd))
	}
}

var (
	testStrings = []string{
		"", "a", "hello world", "fooBar", "foo_bar", "with \"quotes\" and \\slash/",
		"tab\tnew\nline\rret", "\x00\x01\x1f", "<script>&amp;</script>", "\u2028\u2029",
		"\u65e5\u672c\u8a9e", "emoji \U0001F642", "\u00e9\u0301", "NaN", "Infinity", "-Infinity", "1", "-1.5e3",
		"true", "null", "{}", "[]", "0001-01-01T00:00:00Z", "1.5s",
	}
	invalidStrings = []string{"\xff", "a\xc3", "\xed\xa0\x80"}
	testFloats     = []float64{
		0, math.Copysign(0, -1), 1, -1, 0.1, 1.5, -2.25, 1e21, 1e-7, 1e20, 1e-6, 123456789,
		math.MaxFloat64, math.SmallestNonzeroFloat64, math.MaxFloat32, math.SmallestNonzeroFloat32,
		math.Inf(1), math.Inf(-1), math.NaN(), 9007199254740993, 1.0 / 3,
	}
	testInts = []int64{
		0, 1, -1, 42, math.MaxInt32, math.MinInt32, math.MaxInt64, math.MinInt64,
		1 << 53, 1<<53 + 1, -(1 << 53) - 1,
	}
)

func (g *Generator) scalar(fd protoreflect.FieldDescriptor) protoreflect.Value {
	switch fd.Kind() {
	case protoreflect.BoolKind:
		return protoreflect.ValueOfBool(g.chance(0.5))
	case protoreflect.EnumKind:
		values := fd.Enum().Values()
		if g.chance(0.1) || values.Len() == 0 {
			// An enum number that has no name.
			return protoreflect.ValueOfEnum(protoreflect.EnumNumber(g.r.Int32N(1000) - 500))
		}
		return protoreflect.ValueOfEnum(values.Get(g.r.IntN(values.Len())).Number())
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind:
		return protoreflect.ValueOfInt32(int32(g.int()))
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		return protoreflect.ValueOfInt64(g.int())
	case protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return protoreflect.ValueOfUint32(uint32(g.int()))
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return protoreflect.ValueOfUint64(uint64(g.int()))
	case protoreflect.FloatKind:
		return protoreflect.ValueOfFloat32(float32(g.float()))
	case protoreflect.DoubleKind:
		return protoreflect.ValueOfFloat64(g.float())
	case protoreflect.StringKind:
		return protoreflect.ValueOfString(g.string())
	case protoreflect.BytesKind:
		b := make([]byte, g.r.IntN(12))
		for i := range b {
			b[i] = byte(g.r.UintN(256))
		}
		return protoreflect.ValueOfBytes(b)
	default:
		panic("unexpected kind " + fd.Kind().String())
	}
}

func (g *Generator) int() int64 {
	if g.chance(0.5) {
		return testInts[g.r.IntN(len(testInts))]
	}
	return g.r.Int64() >> g.r.UintN(64)
}

func (g *Generator) float() float64 {
	if g.chance(0.6) {
		return testFloats[g.r.IntN(len(testFloats))]
	}
	return (g.r.Float64() - 0.5) * math.Pow(10, float64(g.r.IntN(80)-40))
}

func (g *Generator) string() string {
	if g.chance(g.Invalid) {
		return invalidStrings[g.r.IntN(len(invalidStrings))]
	}
	if g.chance(0.8) {
		return testStrings[g.r.IntN(len(testStrings))]
	}
	b := make([]rune, g.r.IntN(10))
	for i := range b {
		switch {
		case g.chance(0.6):
			b[i] = rune(' ' + g.r.IntN(95))
		case g.chance(0.5):
			b[i] = rune(g.r.IntN(0x20))
		default:
			b[i] = rune(0x80 + g.r.IntN(0x10000-0x80))
			if b[i] >= 0xd800 && b[i] < 0xe000 {
				b[i] = 'x'
			}
		}
	}
	return string(b)
}

// wellKnown fills a well-known type with a value that fits its JSON format.
// It reports false if m is not a well-known type.
func (g *Generator) wellKnown(m protoreflect.Message, depth int) bool {
	var v proto.Message
	switch m.Descriptor().FullName() {
	case "google.protobuf.Timestamp":
		ts := &timestamppb.Timestamp{
			Seconds: g.r.Int64N(253402300799+62135596800) - 62135596800,
			Nanos:   g.nanos(),
		}
		if g.chance(g.Invalid) {
			ts.Seconds = math.MaxInt64
		}
		v = ts
	case "google.protobuf.Duration":
		d := &durationpb.Duration{Seconds: g.r.Int64N(2*315576000000) - 315576000000}
		d.Nanos = g.nanos()
		if d.Seconds < 0 || (d.Seconds == 0 && g.chance(0.5)) {
			d.Nanos = -d.Nanos
		}
		if g.chance(g.Invalid) {
			d.Nanos = -d.Nanos // signs do not match
		}
		v = d
	case "google.protobuf.FieldMask":
		fm := new(fieldmaskpb.FieldMask)
		paths := []string{"foo", "foo_bar", "foo.bar_baz", "a1.b2", "x"}
		for range g.r.IntN(3) {
			fm.Paths = append(fm.Paths, paths[g.r.IntN(len(paths))])
		}
		if g.chance(g.Invalid) {
			fm.Paths = append(fm.Paths, "foo__bar")
		}
		v = fm
	case "google.protobuf.Struct":
		v = g.structValue(depth)
	case "google.protobuf.ListValue":
		v = g.listValue(depth)
	case "google.protobuf.Value":
		v = g.value(depth)
	case "google.protobuf.Empty":
		v = new(emptypb.Empty)
	case "google.protobuf.Any":
		v = g.any(depth)
	case "google.protobuf.BoolValue", "google.protobuf.Int32Value", "google.protobuf.Int64Value",
		"google.protobuf.UInt32Value", "google.protobuf.UInt64Value", "google.protobuf.FloatValue",
		"google.protobuf.DoubleValue", "google.protobuf.StringValue", "google.protobuf.BytesValue":
		fd := m.Descriptor().Fields().ByNumber(1)
		if g.chance(0.8) {
			m.Set(fd, g.scalar(fd))
		}
		return true
	default:
		return false
	}
	proto.Merge(m.Interface(), v)
	return true
}

func (g *Generator) nanos() int32 {
	switch g.r.IntN(4) {
	case 0:
		return 0
	case 1:
		return g.r.Int32N(1000) * 1e6
	case 2:
		return g.r.Int32N(1e6) * 1e3
	default:
		return g.r.Int32N(1e9)
	}
}

func (g *Generator) structValue(depth int) *structpb.Struct {
	s := &structpb.Struct{Fields: map[string]*structpb.Value{}}
	if depth >= MaxDepth {
		return s
	}
	for range g.r.IntN(4) {
		s.Fields[g.string()] = g.value(depth + 1)
	}
	return s
}

func (g *Generator) listValue(depth int) *structpb.ListValue {
	l := new(structpb.ListValue)
	if depth >= MaxDepth {
		return l
	}
	for range g.r.IntN(4) {
		l.Values = append(l.Values, g.value(depth+1))
	}
	return l
}

func (g *Generator) value(depth int) *structpb.Value {
	if g.chance(g.Invalid) {
		return new(structpb.Value) // no kind is set
	}
	kinds := 4
	if depth < MaxDepth {
		kinds = 6
	}
	switch g.r.IntN(kinds) {
	case 0:
		return structpb.NewNullValue()
	case 1:
		return structpb.NewBoolValue(g.chance(0.5))
	case 2:
		f := g.float()
		if (math.IsNaN(f) || math.IsInf(f, 0)) && !g.chance(g.Invalid*10) {
			f = 0.5
		}
		return structpb.NewNumberValue(f)
	case 3:
		return structpb.NewStringValue(g.string())
	case 4:
		return structpb.NewStructValue(g.structValue(depth + 1))
	default:
		return structpb.NewListValue(g.listValue(depth + 1))
	}
}

func (g *Generator) any(depth int) *anypb.Any {
	if g.chance(0.1) {
		return new(anypb.Any)
	}
	if g.chance(g.Invalid) {
		return &anypb.Any{TypeUrl: "type.googleapis.com/does.not.Exist", Value: []byte{0x08, 0x01}}
	}
	var inner proto.Message
	switch g.r.IntN(8) {
	case 0:
		inner = wrapperspb.Int32(int32(g.int()))
	case 1:
		inner = wrapperspb.String(g.string())
	case 2:
		inner = new(emptypb.Empty)
	case 3:
		inner = new(timestamppb.Timestamp)
	case 4:
		inner = new(durationpb.Duration)
	case 5:
		inner = g.value(depth + 1)
	default:
		msg := new(testpb.TestAllTypesProto3)
		if depth < MaxDepth {
			g.Message(msg.ProtoReflect(), depth+1)
		}
		inner = msg
	}
	if depth < MaxDepth {
		g.Message(inner.ProtoReflect(), depth+1)
	}
	a, err := anypb.New(inner)
	if err != nil {
		// For example, a string with invalid UTF-8 in a proto3 message.
		return new(anypb.Any)
	}
	return a
}

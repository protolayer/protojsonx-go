package paritytest

import (
	"testing"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/protolayer/protojsonx-go/internal/testutil"
)

// FuzzUnmarshal checks that protojsonx and protojson agree on any input.
func FuzzUnmarshal(f *testing.F) {
	for _, input := range edgeInputs {
		f.Add([]byte(input), uint8(0))
	}
	for _, input := range edgeInputsProto2 {
		f.Add([]byte(input), uint8(1))
	}
	for _, input := range edgeInputsEditions {
		f.Add([]byte(input), uint8(6))
	}
	g := testutil.NewGenerator(42)
	g.Invalid = 0
	for i, newMessage := range testutil.Messages {
		for range 3 {
			m := newMessage()
			g.Message(m.ProtoReflect(), testutil.MaxDepth)
			b, err := protojson.MarshalOptions{AllowPartial: true}.Marshal(m)
			if err == nil {
				f.Add(b, uint8(i))
			}
		}
	}
	f.Fuzz(func(t *testing.T, data []byte, kind uint8) {
		newMessage := testutil.Messages[int(kind)%len(testutil.Messages)]
		for _, c := range unmarshalCases {
			checkUnmarshal(t, data, newMessage, c)
			checkUnmarshalRead(t, data, newMessage, c)
		}
	})
}

// FuzzMarshal checks that protojsonx and protojson write the same JSON for any
// message that the binary format can describe.
func FuzzMarshal(f *testing.F) {
	g := testutil.NewGenerator(43)
	for i, newMessage := range testutil.Messages {
		for range 3 {
			m := newMessage()
			g.Message(m.ProtoReflect(), testutil.MaxDepth)
			b, err := proto.MarshalOptions{AllowPartial: true}.Marshal(m)
			if err == nil {
				f.Add(b, uint8(i))
			}
		}
	}
	f.Fuzz(func(t *testing.T, data []byte, kind uint8) {
		m := testutil.Messages[int(kind)%len(testutil.Messages)]()
		if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(data, m); err != nil {
			return
		}
		for _, c := range marshalCases {
			checkMarshal(t, m, c)
		}
	})
}

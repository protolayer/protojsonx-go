// Package jsonwire marshals and unmarshals protocol buffer messages in the
// ProtoJSON format using encoding/json/jsontext.
//
// It produces the same format as google.golang.org/protobuf/encoding/protojson,
// but reads from a [jsontext.Decoder] and writes to a [jsontext.Encoder]. This
// lets protobuf messages stream inside larger JSON values handled by
// encoding/json/v2 without an intermediate []byte.
//
// The API follows the proposal in https://github.com/golang/protobuf/issues/1673:
//
//	MarshalEncode(*jsontext.Encoder, proto.Message) error
//	MarshalWrite(io.Writer, proto.Message) error
//	UnmarshalDecode(*jsontext.Decoder, proto.Message) error
//	UnmarshalRead(io.Reader, proto.Message) error
//
// Marshal, MarshalAppend, Format, and Unmarshal work on []byte and string,
// matching protojson.
//
// # Use with encoding/json
//
// Implement json.MarshalerTo and json.UnmarshalerFrom on a message type to
// stream it through json.Marshal and json.Unmarshal (both v1 and v2):
//
//	func (x *MyMessage) MarshalJSONTo(enc *jsontext.Encoder) error {
//		return jsonwire.MarshalEncode(enc, x)
//	}
//
//	func (x *MyMessage) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
//		return jsonwire.UnmarshalDecode(dec, x)
//	}
//
// # Differences from protojson
//
// jsonwire passes the protobuf conformance suite. The main differences are:
//
//   - MarshalEncode and UnmarshalDecode follow the syntax options of the given
//     jsontext Encoder or Decoder (such as indentation and duplicate names).
//     Message strings must always be valid UTF-8, even when AllowInvalidUTF8
//     is enabled.
//   - Only valid JSON is accepted; malformed numbers like 1E are rejected.
//   - Output is compact and deterministic, with no random white space.
//   - Missing required fields return an error without writing partial output.
//   - JSON nesting is capped at 10,000 levels for both Marshal and Unmarshal,
//     including lists and maps.
//
// For details, see
// https://github.com/protolayer/jsonwire/blob/main/docs/compatibility.md.
//
// Requires Go 1.27 or newer.
package jsonwire

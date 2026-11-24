// Command conformance is a testee for the protobuf conformance test runner.
//
// It reads length-prefixed ConformanceRequest messages from stdin and writes
// length-prefixed ConformanceResponse messages to stdout until stdin is closed.
// The JSONWIRE_CONFORMANCE_IMPL environment variable selects the ProtoJSON
// implementation: jsonwire (this module, the default) or protojson (upstream).
package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/protolayer/jsonwire-go"
	conformancepb "github.com/protolayer/jsonwire-go/internal/gen/conformance"
	edition2023pb "github.com/protolayer/jsonwire-go/internal/gen/conformance/test_protos"
	editionspb "github.com/protolayer/jsonwire-go/internal/gen/editions/golden"
	testmessagespb "github.com/protolayer/jsonwire-go/internal/gen/google/protobuf"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const (
	implJSONWire  = "jsonwire"
	implProtoJSON = "protojson"
)

func main() {
	// The runner starts the testee without arguments, so the implementation
	// comes from the environment.
	impl := os.Getenv("JSONWIRE_CONFORMANCE_IMPL")
	if impl == "" {
		impl = implJSONWire
	}
	codec, err := newCodec(impl)
	if err != nil {
		slog.Error("invalid JSONWIRE_CONFORMANCE_IMPL", "error", err)
		os.Exit(2)
	}
	if err := serve(os.Stdin, os.Stdout, codec); err != nil {
		slog.Error("conformance testee failed", "error", err)
		os.Exit(1)
	}
}

// codec is the ProtoJSON implementation under test.
type codec struct {
	marshal   func(proto.Message) ([]byte, error)
	unmarshal func(data []byte, message proto.Message, discardUnknown bool) error
}

func newCodec(impl string) (codec, error) {
	switch impl {
	case implJSONWire:
		return codec{
			marshal: jsonwire.Marshal,
			unmarshal: func(data []byte, message proto.Message, discardUnknown bool) error {
				return jsonwire.UnmarshalOptions{DiscardUnknown: discardUnknown}.Unmarshal(data, message)
			},
		}, nil
	case implProtoJSON:
		return codec{
			marshal: protojson.Marshal,
			unmarshal: func(data []byte, message proto.Message, discardUnknown bool) error {
				return protojson.UnmarshalOptions{DiscardUnknown: discardUnknown}.Unmarshal(data, message)
			},
		}, nil
	default:
		return codec{}, fmt.Errorf("unknown implementation %q", impl)
	}
}

// serve runs the request and response loop until reader returns io.EOF at a
// message boundary.
func serve(reader io.Reader, writer io.Writer, codec codec) error {
	for {
		var size uint32
		if err := binary.Read(reader, binary.LittleEndian, &size); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read request size: %w", err)
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(reader, data); err != nil {
			return fmt.Errorf("read request: %w", err)
		}
		request := new(conformancepb.ConformanceRequest)
		if err := proto.Unmarshal(data, request); err != nil {
			return fmt.Errorf("unmarshal request: %w", err)
		}
		output, err := proto.Marshal(handle(request, codec))
		if err != nil {
			return fmt.Errorf("marshal response: %w", err)
		}
		if err := binary.Write(writer, binary.LittleEndian, uint32(len(output))); err != nil {
			return fmt.Errorf("write response size: %w", err)
		}
		if _, err := writer.Write(output); err != nil {
			return fmt.Errorf("write response: %w", err)
		}
	}
}

func newMessage(messageType string) (proto.Message, bool) {
	switch messageType {
	case "protobuf_test_messages.proto3.TestAllTypesProto3":
		return new(testmessagespb.TestAllTypesProto3), true
	case "protobuf_test_messages.proto2.TestAllTypesProto2":
		return new(testmessagespb.TestAllTypesProto2), true
	case "protobuf_test_messages.editions.TestAllTypesEdition2023":
		return new(edition2023pb.TestAllTypesEdition2023), true
	case "protobuf_test_messages.editions.proto3.TestAllTypesProto3":
		return new(editionspb.TestAllTypesProto3), true
	case "protobuf_test_messages.editions.proto2.TestAllTypesProto2":
		return new(editionspb.TestAllTypesProto2), true
	default:
		return nil, false
	}
}

func handle(request *conformancepb.ConformanceRequest, codec codec) *conformancepb.ConformanceResponse {
	message, ok := newMessage(request.GetMessageType())
	if !ok {
		return runtimeError("unknown message type: " + request.GetMessageType())
	}

	var err error
	switch payload := request.GetPayload().(type) {
	case *conformancepb.ConformanceRequest_ProtobufPayload:
		err = proto.Unmarshal(payload.ProtobufPayload, message)
	case *conformancepb.ConformanceRequest_JsonPayload:
		discardUnknown := request.GetTestCategory() == conformancepb.TestCategory_JSON_IGNORE_UNKNOWN_PARSING_TEST
		err = codec.unmarshal([]byte(payload.JsonPayload), message, discardUnknown)
	case *conformancepb.ConformanceRequest_TextPayload, *conformancepb.ConformanceRequest_JspbPayload:
		return skipped("only protobuf and JSON input are supported")
	default:
		return runtimeError("unknown request payload type")
	}
	if err != nil {
		return &conformancepb.ConformanceResponse{
			Result: &conformancepb.ConformanceResponse_ParseError{ParseError: err.Error()},
		}
	}

	switch request.GetRequestedOutputFormat() {
	case conformancepb.WireFormat_PROTOBUF:
		output, err := proto.Marshal(message)
		if err != nil {
			return serializeError(err)
		}
		return &conformancepb.ConformanceResponse{
			Result: &conformancepb.ConformanceResponse_ProtobufPayload{ProtobufPayload: output},
		}
	case conformancepb.WireFormat_JSON:
		output, err := codec.marshal(message)
		if err != nil {
			return serializeError(err)
		}
		return &conformancepb.ConformanceResponse{
			Result: &conformancepb.ConformanceResponse_JsonPayload{JsonPayload: string(output)},
		}
	case conformancepb.WireFormat_TEXT_FORMAT, conformancepb.WireFormat_JSPB:
		return skipped("only protobuf and JSON output are supported")
	default:
		return runtimeError("unknown output format")
	}
}

func skipped(reason string) *conformancepb.ConformanceResponse {
	return &conformancepb.ConformanceResponse{
		Result: &conformancepb.ConformanceResponse_Skipped{Skipped: reason},
	}
}

func runtimeError(message string) *conformancepb.ConformanceResponse {
	return &conformancepb.ConformanceResponse{
		Result: &conformancepb.ConformanceResponse_RuntimeError{RuntimeError: message},
	}
}

func serializeError(err error) *conformancepb.ConformanceResponse {
	return &conformancepb.ConformanceResponse{
		Result: &conformancepb.ConformanceResponse_SerializeError{SerializeError: err.Error()},
	}
}

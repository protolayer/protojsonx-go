# jsonwire

ProtoJSON for `encoding/json/v2`. Same output as `protojson`, but it streams through `jsontext` and is faster.

## Why

`protojson` has no streaming API for `encoding/json/v2` ([golang/protobuf#1673](https://github.com/golang/protobuf/issues/1673)). A message inside a struct is first encoded to a separate `[]byte`, and then `encoding/json` copies and parses it again. 

This package adds the streaming API, so messages go straight through the encoder and decoder.

## Performance

Compared with `protojson`:

- **Unmarshal:** 25% to 36% faster
- **Marshal:** 12% to 23% faster, with 2x to 4x fewer allocations

Why it is faster:

- **One pass.** No extra `[]byte` to copy and parse again inside `encoding/json`.
- **Faster JSON engine.** Built on `jsontext` from `encoding/json/v2`.
- **Fewer allocations.** Formats values into the encoder buffer and reuses encoder state.

[More about performance](docs/performance.md)

## Install

```sh
go get github.com/protolayer/jsonwire
```

Requires Go 1.27 or newer.

## Use

```go
msg, _ := structpb.NewStruct(map[string]any{"user": "alice", "count": 3})

b, err := jsonwire.Marshal(msg) // {"count":3,"user":"alice"}

var out structpb.Struct
err = jsonwire.Unmarshal(b, &out)
```

[Run a complete example on the Go Playground](https://go.dev/play/p/vqkHbPGN3So).

To use a message inside `encoding/json`:

```go
func (x *MyMessage) MarshalJSONTo(enc *jsontext.Encoder) error {
	return jsonwire.MarshalEncode(enc, x)
}

func (x *MyMessage) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	return jsonwire.UnmarshalDecode(dec, x)
}
```

To generate these methods for every message, use [protoc-gen-go-json](https://github.com/mfridman/protoc-gen-go-json) with `emit_json_v2=true`.

## More

- [Compatibility with protojson](docs/compatibility.md)
- [Performance and tuning](docs/performance.md)

## License

MIT. Parts are ported from [protobuf-go](https://github.com/protocolbuffers/protobuf-go) ([BSD-3-Clause](LICENSE-protobuf-go)). The test protos in `proto/` are from [protobuf](https://github.com/protocolbuffers/protobuf) ([BSD-3-Clause](proto/LICENSE)).

# protojsonx

[![CI](https://github.com/protolayer/protojsonx-go/actions/workflows/ci.yaml/badge.svg)](https://github.com/protolayer/protojsonx-go/actions/workflows/ci.yaml)
[![Go Reference](https://pkg.go.dev/badge/github.com/protolayer/protojsonx-go.svg)](https://pkg.go.dev/github.com/protolayer/protojsonx-go)

ProtoJSON for `encoding/json/v2`. Produces the same output as `protojson`, streams directly through `jsontext`, and runs faster.

## Why

`protojson` does not yet support the streaming API in `encoding/json/v2` ([golang/protobuf#1673](https://github.com/golang/protobuf/issues/1673)). When a protobuf message sits inside a Go struct, `protojson` encodes it to a separate `[]byte` that `encoding/json` must validate and copy. On unmarshal, the JSON is parsed twice.

`protojsonx` streams messages directly through `jsontext.Encoder` and `jsontext.Decoder`.

## Performance

Compared with `protojson`:

- **Unmarshal:** 25% to 36% faster
- **Marshal:** 12% to 23% faster, with 2x to 4x fewer allocations

Why it is faster:

- **One pass.** Streams directly inside `encoding/json` with no intermediate `[]byte`.
- **Faster JSON engine.** Built on `jsontext` from `encoding/json/v2`.
- **Fewer allocations.** Writes formatted values straight into the encoder buffer and reuses encoder state.

See [Performance and tuning](docs/performance.md) for benchmarks and tuning tips.

## Install

```sh
go get github.com/protolayer/protojsonx-go
```

Requires Go 1.27 or newer.

## Use

```go
msg, _ := structpb.NewStruct(map[string]any{"user": "alice", "count": 3})

b, err := protojsonx.Marshal(msg) // {"count":3,"user":"alice"}

var out structpb.Struct
err = protojsonx.Unmarshal(b, &out)
```

To stream any `proto.Message` inside a Go struct with `encoding/json/v2`, pass `MarshalEncode` and `UnmarshalDecode` directly with no wrapper types or code generation:

```go
opts := json.JoinOptions(
	json.WithMarshalers(json.MarshalToFunc(protojsonx.MarshalEncode)),
	json.WithUnmarshalers(json.UnmarshalFromFunc(protojsonx.UnmarshalDecode)),
)

b, err := json.Marshal(v, opts)
err = json.Unmarshal(b, &v, opts)
```

[Run a complete example on the Go Playground](https://go.dev/play/p/NS6YM8QWi5P).

For `encoding/json` v1 compatibility, or to avoid passing options on each call, implement `MarshalJSONTo` and `UnmarshalJSONFrom` on your message types:

```go
func (x *MyMessage) MarshalJSONTo(enc *jsontext.Encoder) error {
	return protojsonx.MarshalEncode(enc, x)
}

func (x *MyMessage) UnmarshalJSONFrom(dec *jsontext.Decoder) error {
	return protojsonx.UnmarshalDecode(dec, x)
}
```

To generate these methods automatically for every message, use [protoc-gen-go-json](https://github.com/mfridman/protoc-gen-go-json) with `emit_json_v2=true`.

## Docs

- [Compatibility with protojson](docs/compatibility.md)
- [Performance and tuning](docs/performance.md)

## License

MIT. Parts are ported from [protobuf-go](https://github.com/protocolbuffers/protobuf-go) ([BSD-3-Clause](LICENSE-protobuf-go)). Test protos in `proto/` are from [protobuf](https://github.com/protocolbuffers/protobuf) ([BSD-3-Clause](proto/LICENSE)).

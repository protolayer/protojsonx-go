# Performance and tuning

Compared with `protojson`:

- **Unmarshal:** 25% to 36% faster, with fewer allocations.
- **Marshal:** 12% to 23% faster, with 2x to 4x fewer allocations.
- **With `AllowDuplicateNames(true)`:** another 25% to 36% faster for large messages inside Go structs.

## Why it is faster

- **One pass.** Inside `encoding/json`, `protojson` encodes to a separate `[]byte` that `encoding/json` then validates and copies. On unmarshal, the JSON is parsed twice. `protojsonx` streams directly through `jsontext` and parses once.
- **Faster JSON engine.** `protojsonx` uses `jsontext` from `encoding/json/v2` instead of `protojson`'s older JSON parser and formatter.
- **Fewer allocations.** Numbers, bytes, durations, and timestamps write directly into the encoder buffer, and small encoder states are reused between calls.

## Benchmarks

Benchmarks use `TestAllTypesProto3` from the protobuf conformance suite ([proto](../proto/google/protobuf/test_messages_proto3.proto)):

- **small:** 8 fields set, 266 bytes of JSON ([small.json](../testdata/benchmark/small.json))
- **large:** 73 fields set with nested messages and well-known types, 3.7 KB of JSON ([large.json](../testdata/benchmark/large.json))

"In a struct" wraps the message as a field of a Go struct handled by `encoding/json/v2`.

| Benchmark | `protojson` | `protojsonx` | Change | Allocations |
| --- | ---: | ---: | ---: | ---: |
| Marshal, small | 3.10 us | 2.74 us | -12% | 32 -> 8 |
| Marshal, large | 22.0 us | 16.9 us | -23% | 269 -> 92 |
| Marshal in a struct, small | 3.55 us | 2.91 us | -18% | 35 -> 10 |
| Marshal in a struct, large | 34.2 us | 27.5 us | -20% | 371 -> 192 |
| Unmarshal, small | 3.08 us | 2.31 us | -25% | 56 -> 53 |
| Unmarshal, large | 42.9 us | 27.6 us | -36% | 707 -> 604 |
| Unmarshal in a struct, small | 3.59 us | 2.45 us | -32% | 58 -> 49 |
| Unmarshal in a struct, large | 54.7 us | 37.0 us | -32% | 807 -> 696 |

Medians of 10 runs on an Apple M4 Pro, Go 1.27.4, `-cpu=1`. Run `make bench` to reproduce, or `make bench-parallel` for multi-CPU shapes. Most of the remaining time is protobuf reflection.

### Other shapes

`BenchmarkShapes` also tests lists, packed numbers, dynamic messages, `Any`, large `bytes` fields, and 1 MB strings. `protojsonx` is faster for typical structured messages. For messages dominated by a single large string (especially non-ASCII text or heavy escaping) or unmarshaling a large `bytes` field, `protojson` can be faster on one CPU. Benchmark with your own payloads if your traffic looks like that.

## Tuning

`encoding/json/v2` checks every JSON object for duplicate names by default, which adds noticeable overhead on large messages. `protojsonx` already rejects duplicate protobuf fields and map keys, so you can disable the outer check:

```go
b, err := json.Marshal(v, jsontext.AllowDuplicateNames(true))
```

For the large message in a struct, this makes marshal 36% faster and unmarshal 25% faster (5% and 3% for the small message). Note that the option applies to the entire `json.Marshal` or `json.Unmarshal` call, including non-protobuf types in the same value.

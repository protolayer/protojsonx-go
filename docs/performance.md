# Performance and tuning

## Summary

Compared with `protojson`:

- **Unmarshal:** 25% to 36% faster, with fewer allocations.
- **Marshal:** 12% to 23% faster, with 2x to 4x fewer allocations.
- **With one option, a further 25% to 36% for large messages in structs.** See [Tuning](#tuning).

## Why it is faster

- **One pass.** Inside `encoding/json`, `protojson` builds a separate `[]byte` that `encoding/json` then validates and copies. On unmarshal, the JSON is parsed twice. `jsonwire` streams directly through `jsontext` and parses once.
- **Faster JSON engine.** `jsonwire` uses `jsontext` from `encoding/json/v2` instead of `protojson`'s older JSON parser and formatter.
- **Fewer allocations.** Numbers, bytes, durations, and timestamps are written directly into the encoder buffer, and small encoder states are reused between calls.

## Benchmarks

The benchmarks use `TestAllTypesProto3` from the protobuf conformance suite ([proto](../proto/google/protobuf/test_messages_proto3.proto)):

- **small**: 8 fields set, 266 bytes of JSON ([small.json](../testdata/benchmark/small.json))
- **large**: 73 fields set, with nested messages and well-known types, 3.7 KB of JSON ([large.json](../testdata/benchmark/large.json))

"In a struct" means the message is a field of a struct that `encoding/json/v2` handles.

| Benchmark | `protojson` | `jsonwire` | Change | Allocations |
| --- | ---: | ---: | ---: | ---: |
| Marshal, small | 3.10 us | 2.74 us | -12% | 32 -> 8 |
| Marshal, large | 22.0 us | 16.9 us | -23% | 269 -> 92 |
| Marshal in a struct, small | 3.55 us | 2.91 us | -18% | 35 -> 10 |
| Marshal in a struct, large | 34.2 us | 27.5 us | -20% | 371 -> 192 |
| Unmarshal, small | 3.08 us | 2.31 us | -25% | 56 -> 53 |
| Unmarshal, large | 42.9 us | 27.6 us | -36% | 707 -> 604 |
| Unmarshal in a struct, small | 3.59 us | 2.45 us | -32% | 58 -> 49 |
| Unmarshal in a struct, large | 54.7 us | 37.0 us | -32% | 807 -> 696 |

Medians of 10 runs on an Apple M4 Pro, Go 1.27.4, `-cpu=1`. To reproduce, run `make bench` (and `make bench-parallel` for multi-CPU shapes). Most of the remaining cost is protobuf reflection.

### Edge cases

`BenchmarkShapes` also tests lists, packed numbers, dynamic messages, `Any`, large `bytes` fields, and 1 MB strings. `jsonwire` is faster for typical structured messages. For messages dominated by a single large string (especially non-ASCII text or heavy escaping) or unmarshaling a large `bytes` field, `protojson` can be faster on one CPU. Measure both packages with your data if your messages have that shape.

## Tuning

### Turn off the duplicate name check

`encoding/json/v2` checks every JSON object for duplicate names by default. For large messages, this check is expensive. `jsonwire` already rejects duplicate fields and map keys, so you can disable the check:

```go
b, err := json.Marshal(v, jsontext.AllowDuplicateNames(true))
```

For the large message in a struct, marshal is 36% faster and unmarshal is 25% faster. For the small message, marshal is 5% faster and unmarshal is 3% faster. The option applies to the whole call, so other types in the same value skip the check too.

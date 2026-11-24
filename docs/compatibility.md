# Compatibility with protojson

`protojsonx` is a port of `protojson` to `encoding/json/jsontext`. It passes the same protobuf conformance suite and matches `protojson` on well-formed messages and JSON.

The five differences below only show up in edge cases or when you pass a custom `jsontext` encoder or decoder.

| Difference | Summary |
| --- | --- |
| [Encoder and decoder options apply](#encoder-and-decoder-options-apply) | `MarshalEncode` and `UnmarshalDecode` follow the caller's `jsontext` options |
| [Only valid JSON](#only-valid-json) | Invalid JSON numbers like `1E` are rejected |
| [Deterministic output](#deterministic-output) | Output is compact and stable, with no random white space |
| [No partial output on missing required fields](#no-partial-output-on-missing-required-fields) | Failed marshals return only the error, not partial bytes |
| [Nesting limit](#nesting-limit) | `jsontext` caps total JSON depth at 10,000 levels |

## Encoder and decoder options apply

`MarshalEncode` and `UnmarshalDecode` respect the options of the `jsontext.Encoder` or `jsontext.Decoder` you pass in, including options from an outer `encoding/json/v2` call. That is why `jsontext.Multiline(true)` on `json.Marshal` indents embedded protobuf messages.

One visible effect is duplicate object names. `encoding/json/v2` rejects duplicate names by default, while `protojson` allows them in unknown fields and in schemas with conflicting JSON field names (such as `legacy_json_field_conflicts`):

```go
input := `{"unknown":1,"unknown":2}`
opts := protojsonx.UnmarshalOptions{DiscardUnknown: true}

opts.Unmarshal([]byte(input), msg)                                       // ok, like protojson
opts.UnmarshalDecode(jsontext.NewDecoder(strings.NewReader(input)), msg) // error: duplicate name
```

`Marshal`, `MarshalAppend`, `MarshalWrite`, `Unmarshal`, `UnmarshalRead`, and `encoding/json` v1 allow duplicate object names, matching `protojson`. To match that behavior with `MarshalEncode`, `UnmarshalDecode`, or `encoding/json/v2`, pass `jsontext.AllowDuplicateNames(true)`. This also speeds up large messages (see [Tuning](performance.md#tuning)).

UTF-8 is the one exception: message strings must always be valid UTF-8, even when the encoder or decoder sets `jsontext.AllowInvalidUTF8(true)` (as `encoding/json` v1 does).

## Only valid JSON

`jsontext` accepts only valid JSON. Some malformed numbers that `protojson` accepts are rejected:

```
{"count":1E}    protojson: count = 1    protojsonx: error
```

## Deterministic output

`protojson` inserts random white space so callers cannot rely on exact byte output. `protojsonx` emits compact, deterministic JSON instead: fields appear in declaration order, extensions are sorted by full name, and map entries are sorted by key.

```
protojson:  {"a":1,  "b":2}
protojsonx:   {"a":1,"b":2}
```

## No partial output on missing required fields

When a required field is missing, `protojson.Marshal` and `MarshalAppend` return both the encoded bytes and an error. `protojsonx` checks required fields before writing, so `Marshal` and `MarshalAppend` return `nil` with the error, and `MarshalEncode` and `MarshalWrite` write nothing.

This avoids leaving a broken partial value inside a larger JSON stream. Set `AllowPartial: true` if you want output with missing required fields, or use `Format` for debugging.

## Nesting limit

`jsontext` enforces a fixed maximum JSON nesting depth of 10,000 levels for both `Marshal` and `Unmarshal`. `protojson` limits only message nesting on `Unmarshal` via `RecursionLimit` (default 10,000) and has no limit on `Marshal`.

Because JSON arrays and map objects count toward the `jsontext` depth:

- Setting `RecursionLimit` above 10,000 cannot raise the JSON depth limit.
- Messages nested through lists or maps hit the limit around 5,000 message levels instead of 10,000.
- `Marshal` also fails if the output exceeds 10,000 JSON levels.

Typical messages are well under 100 levels deep, so this only affects deeply linked recursive structures.

## Testing

- **Conformance:** passes the protobuf conformance suite (`make conformance`).
- **Differential tests:** compares random proto2, proto3, and editions messages against `protojson` across all option combinations.
- **Edge cases:** tests numbers, strings, enums, maps, oneofs, `null`, extensions, and well-known types against `protojson`.
- **Fuzzing:** fuzzes both `Marshal` and `Unmarshal` against `protojson` (`make fuzz`).

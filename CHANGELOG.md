# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v0.1.0] - 2026-11-24

Initial release of `protojsonx`, a ProtoJSON implementation built on `encoding/json/jsontext` for
Go 1.27 and newer.

### Added

- Streaming APIs following [golang/protobuf#1673](https://github.com/golang/protobuf/issues/1673):
  `MarshalEncode`, `MarshalWrite`, `UnmarshalDecode`, and `UnmarshalRead`.
- Drop-in `[]byte` and formatting APIs matching `protojson`: `Marshal`, `MarshalAppend`, `Format`,
  and `Unmarshal`, with `MarshalOptions` and `UnmarshalOptions`.
- Full support for proto2, proto3, Protobuf Editions (2023), extensions, dynamic messages, and all
  well-known types (`Any`, `Timestamp`, `Duration`, `Struct`, `Value`, `ListValue`, `FieldMask`,
  `Empty`, and wrapper types).
- Compact, deterministic JSON output and full compatibility with the protobuf conformance test
  suite.

[Unreleased]: https://github.com/protolayer/protojsonx-go/compare/v0.1.0...HEAD
[v0.1.0]: https://github.com/protolayer/protojsonx-go/releases/tag/v0.1.0

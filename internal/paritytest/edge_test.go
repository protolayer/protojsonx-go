package paritytest

import (
	"math"
	"strconv"
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/protolayer/protojsonx-go"
	"github.com/protolayer/protojsonx-go/internal/gen/conformance/test_protos"
	testpb "github.com/protolayer/protojsonx-go/internal/gen/google/protobuf"
	editionspb "github.com/protolayer/protojsonx-go/internal/gen/protojsonx/test"
)

// edgeInputs are JSON inputs that test the parts of the ProtoJSON format that
// random messages do not reach. protojsonx and protojson must agree on each of
// them: both return an error, or both return the same message.
var edgeInputs = []string{
	// Syntax.
	``, ` `, `{`, `}`, `[]`, `null`, `1`, `"a"`, `{}`, ` {} `, "\t{}\n", `{} {}`, `{}x`, `{},`,
	`{"optionalInt32":1,}`, `{"optionalInt32" 1}`, `{"optionalInt32":}`, `{optionalInt32:1}`,
	`{'optionalInt32':1}`, "\ufeff{}", `{"optionalInt32":1}}`,

	// Field names.
	`{"optionalInt32":1}`, `{"optional_int32":1}`, `{"OptionalInt32":1}`, `{"optionalint32":1}`,
	`{"unknown":1}`, `{"unknown":{"a":[1,{"b":null}]}}`, `{"unknown":[[[[[[]]]]]]}`,
	`{"optionalInt32":1,"optionalInt32":2}`, `{"optionalInt32":1,"optional_int32":2}`,
	`{"fieldname1":1}`, `{"fieldName1":1}`, `{"field_name2":1,"fieldName2":2}`,
	`{"FIELD_NAME11":1}`, `{"FieldName12":1}`, `{"Field_Name14":1}`, `{"field__Name16":1}`,
	`{"@type":"x"}`, `{"[protobuf_test_messages.proto2.extension_int32]":1}`,
	`{"[does.not.exist]":1}`, `{"[]":1}`, `{"[":1}`, `{"optionalInt32\u0000":1}`,

	// Null.
	`{"optionalInt32":null}`, `{"optionalNestedMessage":null}`, `{"repeatedInt32":null}`,
	`{"mapStringString":null}`, `{"optionalValue":null}`, `{"optionalNullValue":null}`,
	`{"repeatedInt32":[null]}`, `{"repeatedNestedMessage":[null]}`, `{"mapStringString":{"a":null}}`,
	`{"mapStringNestedMessage":{"a":null}}`, `{"repeatedValue":[null]}`, `{"repeatedValue":null}`,
	`{"optionalInt32":null,"optionalInt32":1}`, `{"oneofUint32":null,"oneofString":"a"}`,
	`{"oneofNullValue":null}`, `{"oneofNullValue":"NULL_VALUE"}`, `{"oneofNullValue":0}`,

	// Integers.
	`{"optionalInt32":0}`, `{"optionalInt32":-0}`, `{"optionalInt32":1.0}`, `{"optionalInt32":1.5}`,
	`{"optionalInt32":1e2}`, `{"optionalInt32":1E+2}`, `{"optionalInt32":1e-2}`, `{"optionalInt32":100e-2}`,
	`{"optionalInt32":1.000000e3}`, `{"optionalInt32":0.1e1}`, `{"optionalInt32":2147483647}`,
	`{"optionalInt32":2147483648}`, `{"optionalInt32":-2147483648}`, `{"optionalInt32":-2147483649}`,
	`{"optionalInt32":"1"}`, `{"optionalInt32":" 1"}`, `{"optionalInt32":"1 "}`, `{"optionalInt32":"01"}`,
	`{"optionalInt32":"1e2"}`, `{"optionalInt32":"0x10"}`, `{"optionalInt32":""}`, `{"optionalInt32":"-"}`,
	`{"optionalInt32":"+1"}`, `{"optionalInt32":"1,2"}`, `{"optionalInt32":true}`, `{"optionalInt32":[1]}`,
	`{"optionalInt32":{}}`, `{"optionalInt32":"\u00311"}`, `{"optionalInt32":1e100000000000}`,
	`{"optionalInt32":0.0000000000000000000000000000001e31}`, `{"optionalInt32":"NaN"}`,
	`{"optionalInt64":"9223372036854775807"}`, `{"optionalInt64":"9223372036854775808"}`,
	`{"optionalInt64":9223372036854775807}`, `{"optionalInt64":-9223372036854775808}`,
	`{"optionalInt64":1e18}`, `{"optionalInt64":1e19}`, `{"optionalInt64":"-1e18"}`,
	`{"optionalUint32":4294967295}`, `{"optionalUint32":4294967296}`, `{"optionalUint32":-1}`,
	`{"optionalUint32":-0}`, `{"optionalUint32":"-0"}`, `{"optionalUint64":"18446744073709551615"}`,
	`{"optionalUint64":"18446744073709551616"}`, `{"optionalUint64":1.8446744073709551615e19}`,
	`{"optionalSint32":-5}`, `{"optionalFixed32":5}`, `{"optionalSfixed64":"-5"}`,
	`{"optionalInt32":00}`, `{"optionalInt32":-}`, `{"optionalInt32":.5}`, `{"optionalInt32":5.}`,

	// Floats.
	`{"optionalFloat":1.5}`, `{"optionalFloat":"1.5"}`, `{"optionalFloat":"NaN"}`, `{"optionalFloat":"nan"}`,
	`{"optionalFloat":"Infinity"}`, `{"optionalFloat":"-Infinity"}`, `{"optionalFloat":"+Infinity"}`,
	`{"optionalFloat":"inf"}`, `{"optionalFloat":3.4028235e38}`, `{"optionalFloat":3.5e38}`,
	`{"optionalFloat":-3.5e38}`, `{"optionalFloat":1e-50}`, `{"optionalDouble":1.7976931348623157e308}`,
	`{"optionalDouble":1.8e308}`, `{"optionalDouble":-1.8e308}`, `{"optionalDouble":4.9e-324}`,
	`{"optionalDouble":1e-400}`, `{"optionalDouble":-0}`, `{"optionalDouble":-0.0}`, `{"optionalDouble":" 1"}`,
	`{"optionalDouble":"1e5"}`, `{"optionalDouble":"1e500"}`, `{"optionalDouble":true}`,
	`{"optionalDouble":0.1234567890123456789}`,

	// Booleans and strings.
	`{"optionalBool":true}`, `{"optionalBool":false}`, `{"optionalBool":"true"}`, `{"optionalBool":1}`,
	`{"optionalBool":null}`, `{"optionalString":"a"}`, `{"optionalString":1}`, `{"optionalString":null}`,
	`{"optionalString":"\u0000"}`, `{"optionalString":"\ud83d\ude42"}`, `{"optionalString":"\ud83d"}`,
	`{"optionalString":"\ude42"}`, `{"optionalString":"\ud83d\u0041"}`, `{"optionalString":"\uD83D\uDE42"}`,
	`{"optionalString":"\x"}`, `{"optionalString":"\/"}`, `{"optionalString":"\u00e9"}`,
	"{\"optionalString\":\"\x01\"}", "{\"optionalString\":\"\x7f\"}", "{\"optionalString\":\"\xff\"}",
	"{\"optionalString\":\"\xed\xa0\x80\"}", "{\"opt\xffionalString\":\"a\"}",
	`{"optionalString":"\u12"}`, `{"optionalString":"\uZZZZ"}`,
	// Invalid UTF-8 and bad surrogates in values that DiscardUnknown skips.
	"{\"unknown\":\"\xff\"}", "{\"unknown\":[\"ok\",\"\xed\xa0\x80\"]}", "{\"unknown\":{\"k\xff\":1}}",
	`{"unknown":"\ud800"}`, `{"unknown":{"a":["\udc00x"]}}`, "{\"unk\xffnown\":1}",
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Int32Value","value":1,"unknown":"\ud800"}}`,

	// Bytes.
	`{"optionalBytes":""}`, `{"optionalBytes":"AQID"}`, `{"optionalBytes":"AQI="}`, `{"optionalBytes":"AQI"}`,
	`{"optionalBytes":"-_8="}`, `{"optionalBytes":"-_8"}`, `{"optionalBytes":"+/8="}`, `{"optionalBytes":"+_8="}`,
	`{"optionalBytes":"A"}`, `{"optionalBytes":"AQ=="}`, `{"optionalBytes":"AQ="}`, `{"optionalBytes":"A Q=="}`,
	`{"optionalBytes":"AQ\n=="}`, `{"optionalBytes":1}`, `{"optionalBytes":"!!!!"}`,

	// Enums.
	`{"optionalNestedEnum":"FOO"}`, `{"optionalNestedEnum":"foo"}`, `{"optionalNestedEnum":"UNKNOWN"}`,
	`{"optionalNestedEnum":1}`, `{"optionalNestedEnum":12345}`, `{"optionalNestedEnum":-1}`,
	`{"optionalNestedEnum":1.0}`, `{"optionalNestedEnum":1.5}`, `{"optionalNestedEnum":"1"}`,
	`{"optionalNestedEnum":2147483648}`, `{"optionalNestedEnum":true}`, `{"repeatedNestedEnum":["FOO","X"]}`,
	`{"mapStringNestedEnum":{"a":"X","b":"FOO"}}`, `{"optionalAliasedEnum":"ALIAS_BAZ"}`,
	`{"optionalAliasedEnum":"MOO"}`, `{"optionalAliasedEnum":"moo"}`, `{"optionalAliasedEnum":"bAz"}`,

	// Repeated fields and maps.
	`{"repeatedInt32":[]}`, `{"repeatedInt32":[1,2,"3"]}`, `{"repeatedInt32":1}`, `{"repeatedInt32":{}}`,
	`{"repeatedInt32":[[1]]}`, `{"repeatedNestedMessage":[{},{"a":1}]}`, `{"repeatedNestedMessage":[1]}`,
	`{"mapInt32Int32":{}}`, `{"mapInt32Int32":{"1":2}}`, `{"mapInt32Int32":{"01":2}}`, `{"mapInt32Int32":{" 1":2}}`,
	`{"mapInt32Int32":{"1.0":2}}`, `{"mapInt32Int32":{"1e2":2}}`, `{"mapInt32Int32":{"-0":2}}`,
	`{"mapInt32Int32":{"+1":2}}`, `{"mapInt32Int32":{"a":2}}`, `{"mapInt32Int32":{"1":2,"1":3}}`,
	`{"mapInt32Int32":{"1":2,"01":3}}`, `{"mapInt32Int32":[]}`, `{"mapInt64Int64":{"9223372036854775807":1}}`,
	`{"mapUint32Uint32":{"-1":1}}`, `{"mapBoolBool":{"true":true,"false":false}}`, `{"mapBoolBool":{"True":true}}`,
	`{"mapBoolBool":{"1":true}}`, `{"mapStringString":{"":""}}`, `{"mapStringString":{"a":"b","a":"c"}}`,
	`{"mapStringBytes":{"a":"AQ=="}}`, `{"mapStringNestedMessage":{"a":{"a":1}}}`,

	// Oneofs.
	`{"oneofUint32":1}`, `{"oneofUint32":1,"oneofString":"a"}`, `{"oneofUint32":1,"oneof_uint32":2}`,
	`{"oneofNestedMessage":{}}`, `{"oneofNestedMessage":{},"oneofBool":true}`,

	// Nested messages.
	`{"optionalNestedMessage":{}}`, `{"optionalNestedMessage":{"a":1}}`, `{"optionalNestedMessage":1}`,
	`{"optionalNestedMessage":[]}`, `{"recursiveMessage":{"recursiveMessage":{"optionalInt32":1}}}`,
	`{"optionalNestedMessage":{"corecursive":{"optionalInt32":1}}}`,

	// Timestamp.
	`{"optionalTimestamp":"1970-01-01T00:00:00Z"}`, `{"optionalTimestamp":"1970-01-01T00:00:00.1Z"}`,
	`{"optionalTimestamp":"1970-01-01T00:00:00.123456789Z"}`, `{"optionalTimestamp":"1970-01-01T00:00:00.1234567890Z"}`,
	`{"optionalTimestamp":"1970-01-01T00:00:00+08:00"}`, `{"optionalTimestamp":"1970-01-01T00:00:00-08:00"}`,
	`{"optionalTimestamp":"1970-01-01T00:00:00.5+08:00"}`, `{"optionalTimestamp":"1970-01-01t00:00:00z"}`,
	`{"optionalTimestamp":"1970-01-01 00:00:00Z"}`, `{"optionalTimestamp":"0001-01-01T00:00:00Z"}`,
	`{"optionalTimestamp":"0000-12-31T23:59:59Z"}`, `{"optionalTimestamp":"9999-12-31T23:59:59.999999999Z"}`,
	`{"optionalTimestamp":"10000-01-01T00:00:00Z"}`, `{"optionalTimestamp":"0001-01-01T00:00:00+00:01"}`,
	`{"optionalTimestamp":"9999-12-31T23:59:59-00:01"}`, `{"optionalTimestamp":"1970-01-01T00:00:00"}`,
	`{"optionalTimestamp":"1970-01-01T00:00:60Z"}`, `{"optionalTimestamp":0}`, `{"optionalTimestamp":{}}`,
	`{"optionalTimestamp":"1970-01-01T00:00:00.Z"}`, `{"optionalTimestamp":"1970-02-30T00:00:00Z"}`,

	// Duration.
	`{"optionalDuration":"0s"}`, `{"optionalDuration":"1s"}`, `{"optionalDuration":"-1s"}`, `{"optionalDuration":"+1s"}`,
	`{"optionalDuration":"1.5s"}`, `{"optionalDuration":"-0.5s"}`, `{"optionalDuration":".5s"}`, `{"optionalDuration":"1.s"}`,
	`{"optionalDuration":"-.5s"}`, `{"optionalDuration":"1.123456789s"}`, `{"optionalDuration":"1.1234567890s"}`,
	`{"optionalDuration":"315576000000s"}`, `{"optionalDuration":"315576000001s"}`, `{"optionalDuration":"-315576000000.999999999s"}`,
	`{"optionalDuration":"1"}`, `{"optionalDuration":"s"}`, `{"optionalDuration":"1m"}`, `{"optionalDuration":"01s"}`,
	`{"optionalDuration":"1e2s"}`, `{"optionalDuration":" 1s"}`, `{"optionalDuration":"--1s"}`, `{"optionalDuration":1}`,
	`{"optionalDuration":"9223372036854775808s"}`, `{"optionalDuration":"0.s"}`, `{"optionalDuration":"-s"}`,

	// FieldMask.
	`{"optionalFieldMask":""}`, `{"optionalFieldMask":"foo"}`, `{"optionalFieldMask":"fooBar,baz.quxQuux"}`,
	`{"optionalFieldMask":"foo_bar"}`, `{"optionalFieldMask":" foo "}`, `{"optionalFieldMask":"foo, bar"}`,
	`{"optionalFieldMask":"foo,,bar"}`, `{"optionalFieldMask":"FooBar"}`, `{"optionalFieldMask":"foo.bar."}`,
	`{"optionalFieldMask":"1foo"}`, `{"optionalFieldMask":"fooBAR"}`, `{"optionalFieldMask":","}`,
	`{"optionalFieldMask":1}`,

	// Wrappers.
	`{"optionalBoolWrapper":true}`, `{"optionalBoolWrapper":null}`, `{"optionalInt32Wrapper":"1"}`,
	`{"optionalInt64Wrapper":"-5"}`, `{"optionalUint64Wrapper":1}`, `{"optionalFloatWrapper":"NaN"}`,
	`{"optionalDoubleWrapper":1e400}`, `{"optionalStringWrapper":""}`, `{"optionalBytesWrapper":"AQ=="}`,
	`{"optionalStringWrapper":{"value":"a"}}`, `{"repeatedInt32Wrapper":[1,null]}`, `{"repeatedBoolWrapper":[true]}`,

	// Struct, Value, and ListValue.
	`{"optionalStruct":{}}`, `{"optionalStruct":{"a":1,"b":"c","d":null,"e":[true,{}]}}`, `{"optionalStruct":[]}`,
	`{"optionalStruct":null}`, `{"optionalStruct":{"a":1,"a":2}}`, `{"optionalStruct":{"a":{"b":{"c":{}}}}}`,
	`{"optionalValue":1}`, `{"optionalValue":"a"}`, `{"optionalValue":true}`, `{"optionalValue":[]}`,
	`{"optionalValue":{}}`, `{"optionalValue":1e400}`, `{"optionalValue":"NaN"}`, `{"optionalValue":-0}`,
	`{"optionalValue":12345678901234567890}`, `{"repeatedValue":[1,null,"a",[],{}]}`, `{"repeatedListValue":[[1],[]]}`,
	`{"optionalValue":[[[[[[[[[[1]]]]]]]]]]}`, `{"repeatedListValue":[1]}`, `{"repeatedStruct":[{}]}`,
	`{"repeatedStruct":[null]}`,

	// Any.
	`{"optionalAny":{}}`, `{"optionalAny":null}`, `{"optionalAny":[]}`, `{"optionalAny":1}`,
	`{"optionalAny":{"optionalInt32":1}}`, `{"optionalAny":{"@type":""}}`, `{"optionalAny":{"@type":1}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/does.not.Exist"}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/protobuf_test_messages.proto3.TestAllTypesProto3"}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/protobuf_test_messages.proto3.TestAllTypesProto3","optionalInt32":1}}`,
	`{"optionalAny":{"optionalInt32":1,"@type":"type.googleapis.com/protobuf_test_messages.proto3.TestAllTypesProto3"}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/protobuf_test_messages.proto3.TestAllTypesProto3","@type":"x"}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/protobuf_test_messages.proto3.TestAllTypesProto3","unknown":1}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/protobuf_test_messages.proto3.TestAllTypesProto3","optionalAny":{"@type":"type.googleapis.com/google.protobuf.Int32Value","value":5}}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Int32Value","value":5}}`,
	`{"optionalAny":{"value":5,"@type":"type.googleapis.com/google.protobuf.Int32Value"}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Int32Value"}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Int32Value","value":5,"value":6}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Int32Value","value":5,"extra":1}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Empty"}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Empty","value":{}}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Empty","value":{"a":1}}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Value","value":null}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Struct","value":{"a":[1]}}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Timestamp","value":"1970-01-01T00:00:00Z"}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Any","value":{"@type":"type.googleapis.com/google.protobuf.Int32Value","value":1}}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Any","value":{}}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/google.protobuf.Duration","value":"1s"}}`,
	`{"optionalAny":{"@type":"google.protobuf.Duration","value":"1s"}}`,
	`{"optionalAny":{"@type":"/google.protobuf.Duration","value":"1s"}}`,
	`{"optionalAny":{"unknown":{"deep":[[[]]]}}}`,
	// The depth check of an Any without @type, which DiscardUnknown discards.
	`{"optionalAny":{"a":[]}}`, `{"optionalAny":{"a":[[]]}}`, `{"optionalAny":{"a":[[[]]]}}`,
	`{"optionalAny":{"a":[[[[]]]]}}`,
	// Quoted numbers with an exponent sign or white space.
	`{"optionalInt32":"1e+2"}`, `{"optionalInt32":"1e-0"}`, `{"optionalInt32":" 1"}`, `{"optionalInt32":"1 "}`,
	// Numbers with fractions and exponents for integer and float fields.
	`{"optionalDouble":"1.25"}`, `{"optionalInt32":"1.00"}`, `{"optionalInt32":10.5e-1}`,
	`{"optionalInt32":15e-1}`, `{"optionalInt32":150e-1}`, `{"optionalInt64":"1.50e1"}`,
	// Duplicate unknown names, which DiscardUnknown accepts.
	`{"unknown":1,"unknown":2}`, `{"unknown":{"a":1,"a":2}}`,
	`{"optionalAny":{"@type":"type.googleapis.com/protobuf_test_messages.proto3.TestAllTypesProto3","optionalInt32":"x"}}`,
}

// edgeInputsEditions test editions features: required fields, closed enums,
// presence, default values, delimited and group-like fields, json_name, and
// extensions.
var edgeInputsEditions = []string{
	`{}`, `{"requiredInt32":1,"requiredMessage":{}}`, `{"requiredInt32":1}`, `{"required_int32":1,"requiredMessage":null}`,
	`{"closedEnum":"CLOSED_A"}`, `{"closedEnum":1}`, `{"closedEnum":99}`, `{"closedEnum":0}`, `{"closedEnum":"UNKNOWN"}`,
	`{"repeatedClosed":["CLOSED_A",99,"CLOSED_B"]}`, `{"repeatedClosed":["X"]}`, `{"openEnum":99}`, `{"openEnum":"X"}`,
	`{"explicitInt32":0}`, `{"implicitInt32":0}`, `{"explicitInt32":null}`, `{"defaultString":""}`, `{"defaultString":"hello"}`,
	`{"delimited":{"value":1}}`, `{"Delimited":{"value":1}}`, `{"repeatedDelimited":[{"name":"a"},{}]}`,
	`{"group":{"value":1}}`, `{"Group":{"value":1}}`, `{"group":{"value":1},"Group":{"value":2}}`,
	`{"mapGroup":{"1":{"value":1},"-1":{}}}`, `{"mapGroup":{"x":{}}}`,
	`{"customJSON":"x"}`, `{"custom_json":"x"}`, `{"customJson":"x"}`, `{"customJSON":"x","custom_json":"y"}`,
	`{"choiceInt":1,"choiceGroup":{}}`, `{"choiceGroup":{"value":1}}`, `{"choiceInt":null,"choiceGroup":{}}`,
	`{"[protojsonx.test.ext_int32]":1}`, `{"[protojsonx.test.ext_group]":{"value":1}}`, `{"[protojsonx.test.ext_strings]":["a"]}`,
	`{"[protojsonx.test.ext_int32]":1,"[protojsonx.test.ext_int32]":2}`, `{"[protojsonx.test.unknown_ext]":1}`,
	`{"[protojsonx.test.Editions.ext_int32]":1}`, `{"[protobuf_test_messages.editions.extension_int32]":1}`,
	`{"recursive":{"requiredInt32":1,"requiredMessage":{}}}`, `{"recursive":{"recursive":{}}}`,
}

// edgeInputsProto2 test proto2 features: groups, extensions, required fields,
// and closed enums.
var edgeInputsProto2 = []string{
	`{}`, `{"data":{"groupInt32":1}}`, `{"Data":{"groupInt32":1}}`, `{"data":{"group_int32":1}}`,
	`{"[protobuf_test_messages.proto2.extension_int32]":1}`,
	`{"[protobuf_test_messages.proto2.extension_int32]":1,"[protobuf_test_messages.proto2.extension_int32]":2}`,
	`{"[protobuf_test_messages.proto2.groupfield]":{"groupInt32":1}}`,
	`{"[protobuf_test_messages.proto2.TestAllTypesProto2.MessageSetCorrectExtension1.message_set_extension]":{}}`,
	`{"[protobuf_test_messages.proto3.TestAllTypesProto3]":1}`, `{"optionalNestedEnum":12345}`,
	`{"optionalNestedEnum":"FOO"}`, `{"repeatedNestedEnum":[1,2,99]}`, `{"defaultInt32":0}`,
	`{"messageSetCorrect":{}}`, `{"optionalInt32":1,"optional_int32":1}`, `{"fieldname1":1}`,
	`{"multiwordgroupfield":{"groupInt32":1}}`, `{"MultiWordGroupField":{"groupInt32":1}}`,
	`{"optionalGroup":{}}`,
}

var edgeInputsRequired = []string{
	`{}`, `{"requiredInt32":1}`, `{"requiredNestedMessage":{}}`,
}

var edgeInputsEdition2023 = []string{
	`{}`, `{"groupliketype":{"c":1}}`, `{"GroupLikeType":{"c":1}}`, `{"delimitedField":{"c":1}}`,
	`{"[protobuf_test_messages.editions.groupliketype]":{"c":1}}`,
	`{"[protobuf_test_messages.editions.delimited_ext]":{"c":1}}`,
}

func TestEdgeInputs(t *testing.T) {
	t.Parallel()
	groups := []struct {
		inputs     []string
		newMessage func() proto.Message
	}{
		{edgeInputs, func() proto.Message { return new(testpb.TestAllTypesProto3) }},
		{edgeInputsProto2, func() proto.Message { return new(testpb.TestAllTypesProto2) }},
		{edgeInputsRequired, func() proto.Message { return new(testpb.TestAllRequiredTypesProto2) }},
		{edgeInputsEditions, func() proto.Message { return new(editionspb.Editions) }},
		{edgeInputsEdition2023, func() proto.Message { return new(test_protos.TestAllTypesEdition2023) }},
	}
	for _, group := range groups {
		for _, input := range group.inputs {
			for _, c := range unmarshalCases {
				checkUnmarshal(t, []byte(input), group.newMessage, c)
				checkUnmarshalRead(t, []byte(input), group.newMessage, c)
			}
		}
	}
}

// hugeExponentInputs have exponents at the limits of int32. On 32-bit
// platforms, the size check in protojson overflows, and protojson tries to
// allocate about 2 GB. The value says whether the input is valid.
var hugeExponentInputs = map[string]bool{
	`{"optionalInt64":1e2147483647}`:    false,
	`{"optionalInt64":"1e2147483647"}`:  false,
	`{"optionalUint64":1e2147483647}`:   false,
	`{"optionalInt64":1e2147483648}`:    false,
	`{"optionalInt64":1e-2147483648}`:   false,
	`{"optionalInt64":10e-2147483648}`:  false,
	`{"optionalInt32":1.5e2147483647}`:  false,
	`{"optionalInt64":-1e2147483647}`:   false,
	`{"optionalInt64":0e2147483647}`:    true,
	`{"optionalInt64":"0e-2147483648"}`: true,
}

// TestHugeExponents checks huge exponents with protojsonx on all platforms. It
// compares with protojson only on 64-bit platforms, where protojson does not
// run out of memory.
func TestHugeExponents(t *testing.T) {
	t.Parallel()
	newMessage := func() proto.Message { return new(testpb.TestAllTypesProto3) }
	for input, valid := range hugeExponentInputs {
		err := protojsonx.Unmarshal([]byte(input), newMessage())
		if (err == nil) != valid {
			t.Errorf("Unmarshal(%s): got error %v, want valid=%v", input, err, valid)
		}
		if strconv.IntSize == 64 {
			for _, c := range unmarshalCases {
				checkUnmarshal(t, []byte(input), newMessage, c)
			}
		}
	}
}

// invalidMessages are messages that ProtoJSON cannot represent. Marshal must
// return an error for each of them, the same as protojson.
func invalidMessages() []proto.Message {
	return []proto.Message{
		&durationpb.Duration{Seconds: 315576000001},
		&durationpb.Duration{Seconds: -315576000001},
		&durationpb.Duration{Nanos: 1000000000},
		&durationpb.Duration{Seconds: 1, Nanos: -1},
		&durationpb.Duration{Seconds: 1, Nanos: -1000000000},
		&durationpb.Duration{Nanos: -1000000000},
		&durationpb.Duration{Seconds: -1, Nanos: -1000000000},
		&timestamppb.Timestamp{Nanos: -1},
		&timestamppb.Timestamp{Nanos: 1000000000},
		&timestamppb.Timestamp{Seconds: 253402300800},
		&timestamppb.Timestamp{Seconds: -62135596801},
		&fieldmaskpb.FieldMask{Paths: []string{"a b"}},
		&fieldmaskpb.FieldMask{Paths: []string{""}},
		&fieldmaskpb.FieldMask{Paths: []string{"aB"}},
		&anypb.Any{Value: []byte{1}},
		&anypb.Any{TypeUrl: "type.googleapis.com/google.protobuf.Duration", Value: []byte{0xff}},
		&testpb.TestAllTypesProto3{OptionalDuration: &durationpb.Duration{Nanos: 1000000000}},
		&testpb.TestAllTypesProto3{RepeatedTimestamp: []*timestamppb.Timestamp{{Nanos: -1}}},
		structpb.NewNumberValue(math.NaN()),
		structpb.NewNumberValue(math.Inf(1)),
		structpb.NewNumberValue(math.Inf(-1)),
	}
}

// limitMessages are valid messages at the limits of the well-known types.
// Marshal must accept them, the same as protojson.
func limitMessages() []proto.Message {
	return []proto.Message{
		&durationpb.Duration{Seconds: 315576000000},
		&durationpb.Duration{Seconds: -315576000000},
		&durationpb.Duration{Seconds: 315576000000, Nanos: 999999999},
		&durationpb.Duration{Seconds: -315576000000, Nanos: -999999999},
		&durationpb.Duration{Nanos: 999999999},
		&durationpb.Duration{Nanos: -999999999},
		&timestamppb.Timestamp{Seconds: 253402300799, Nanos: 999999999},
		&timestamppb.Timestamp{Seconds: -62135596800},
		&timestamppb.Timestamp{Nanos: 999999999},
		structpb.NewNumberValue(math.MaxFloat64),
		structpb.NewNumberValue(-math.MaxFloat64),
		structpb.NewNumberValue(math.SmallestNonzeroFloat64),
	}
}

func TestInvalidMessages(t *testing.T) {
	t.Parallel()
	for _, m := range limitMessages() {
		for _, c := range marshalCases {
			checkMarshal(t, m, c)
		}
		if _, err := protojsonx.Marshal(m); err != nil {
			t.Errorf("Marshal(%v): %v", m, err)
		}
	}
	for _, m := range invalidMessages() {
		for _, c := range marshalCases {
			checkMarshal(t, m, c)
		}
		if _, err := protojsonx.Marshal(m); err == nil {
			t.Errorf("Marshal(%v): want error", m)
		}
	}
}

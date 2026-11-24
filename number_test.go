package protojsonx

import (
	"math"
	"testing"
)

func TestParseIntAndUint(t *testing.T) {
	t.Parallel()

	intCases := []struct {
		raw     string
		bitSize int
		want    int64
		ok      bool
	}{
		{"0", 32, 0, true},
		{"-0", 32, 0, true},
		{"-0.0", 32, 0, true},
		{"1.0", 32, 1, true},
		{"1e2", 32, 100, true},
		{"100e-2", 32, 1, true},
		{"-2147483648", 32, math.MinInt32, true},
		{"2147483647", 32, math.MaxInt32, true},
		{"2147483648", 32, 0, false},
		{"-2147483649", 32, 0, false},
		{"-9223372036854775808", 64, math.MinInt64, true},
		{"9223372036854775807", 64, math.MaxInt64, true},
		{"9223372036854775808", 64, 0, false},
		{"01", 32, 0, false},
		{"-00", 32, 0, false},
		{"+1", 32, 0, false},
		{" 1", 32, 0, false},
		{"1 ", 32, 0, false},
		{"1.", 32, 0, false},
		{".1", 32, 0, false},
		{"1.5", 32, 0, false},
		{"1e", 32, 0, false},
		{"1e+", 32, 0, false},
		{"1e-", 32, 0, false},
		{"1e21", 64, 0, false},
		{"0e999999999999999999999999", 64, 0, true},
	}
	for _, tc := range intCases {
		got, ok := parseInt(tc.raw, tc.bitSize)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseInt(%q, %d) = %d, %v; want %d, %v", tc.raw, tc.bitSize, got, ok, tc.want, tc.ok)
		}
	}

	uintCases := []struct {
		raw     string
		bitSize int
		want    uint64
		ok      bool
	}{
		{"0", 32, 0, true},
		{"-0", 32, 0, true},
		{"-0.0", 64, 0, true},
		{"4294967295", 32, math.MaxUint32, true},
		{"4294967296", 32, 0, false},
		{"18446744073709551615", 64, math.MaxUint64, true},
		{"18446744073709551616", 64, 0, false},
		{"-1", 64, 0, false},
		{"1e2", 32, 100, true},
		{"100e-2", 32, 1, true},
		{"1.5", 64, 0, false},
		{"01", 64, 0, false},
	}
	for _, tc := range uintCases {
		got, ok := parseUint(tc.raw, tc.bitSize)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseUint(%q, %d) = %d, %v; want %d, %v", tc.raw, tc.bitSize, got, ok, tc.want, tc.ok)
		}
	}
}

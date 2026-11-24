// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE-protobuf-go file.
//
// This file is a port of google.golang.org/protobuf/internal/encoding/json.

package protojsonx

import (
	"strconv"
	"strings"
)

func isDigit(c byte) bool {
	return '0' <= c && c <= '9'
}

// numberParts is the result of parsing a valid JSON number (RFC 8259).
// The fields are substrings of the input.
type numberParts struct {
	neg  bool
	intp string
	frac string
	exp  string
}

// parseNumberParts validates that s is a JSON number and returns its parts.
func parseNumberParts(s string) (numberParts, bool) {
	var neg bool
	var intp, frac, exp string

	if len(s) == 0 {
		return numberParts{}, false
	}

	// Optional -
	if s[0] == '-' {
		neg = true
		s = s[1:]
		if len(s) == 0 {
			return numberParts{}, false
		}
	}

	// Digits: either a single 0, or 1-9 followed by any digits.
	switch {
	case s[0] == '0':
		s = s[1:]
	case '1' <= s[0] && s[0] <= '9':
		n := 1
		for n < len(s) && isDigit(s[n]) {
			n++
		}
		intp, s = s[:n], s[n:]
	default:
		return numberParts{}, false
	}

	// Optional fraction: '.' followed by 1 or more digits.
	if len(s) > 0 && s[0] == '.' {
		s = s[1:]
		n := 0
		for n < len(s) && isDigit(s[n]) {
			n++
		}
		if n == 0 {
			return numberParts{}, false
		}
		frac, s = s[:n], s[n:]
	}

	// Optional exponent: 'e'/'E', optional '+'/'-', and 1 or more digits.
	if len(s) > 0 && (s[0] == 'e' || s[0] == 'E') {
		s = s[1:]
		n := 0
		if len(s) > 0 && (s[0] == '+' || s[0] == '-') {
			n++
		}
		digitsStart := n
		for n < len(s) && isDigit(s[n]) {
			n++
		}
		if n == digitsStart {
			return numberParts{}, false
		}
		exp, s = s[:n], s[n:]
	}

	if len(s) != 0 {
		return numberParts{}, false
	}

	return numberParts{
		neg:  neg,
		intp: intp,
		frac: strings.TrimRight(frac, "0"), // Remove unnecessary 0s to the right.
		exp:  exp,
	}, true
}

// intString returns the given JSON number as an integer string without
// E-notation, or false if it is not a valid JSON number or not an integer.
func intString(raw string) (string, bool) {
	n, ok := parseNumberParts(raw)
	if !ok {
		return "", false
	}

	intpSize := len(n.intp)
	fracSize := len(n.frac)

	if intpSize == 0 && fracSize == 0 {
		return "0", true
	}

	var exp int
	if len(n.exp) > 0 {
		i, err := strconv.ParseInt(n.exp, 10, 32)
		if err != nil {
			return "", false
		}
		exp = int(i)
	}

	const maxDigits = 20 // Max uint64 value has 20 decimal digits.
	var num string
	switch {
	case exp == 0:
		if fracSize > 0 || intpSize > maxDigits {
			return "", false
		}
		if n.neg && len(raw) == 1+intpSize {
			return raw, true
		}
		num = n.intp

	case exp > 0:
		// For positive E, shift fraction digits into integer part and pad with
		// zeroes as needed.
		if fracSize > exp {
			return "", false
		}
		// Compare without an addition, which can overflow on 32-bit platforms.
		if exp > maxDigits-intpSize {
			return "", false
		}
		num = n.intp + n.frac + strings.Repeat("0", exp-fracSize)

	default:
		// For negative E, shift digits in integer part out.
		if fracSize > 0 {
			return "", false
		}
		index := intpSize + exp
		if index < 0 {
			return "", false
		}
		for i := index; i < intpSize; i++ {
			if n.intp[i] != '0' {
				return "", false
			}
		}
		num = n.intp[:index]
	}

	if n.neg {
		return "-" + num, true
	}
	return num, true
}

// parseInt returns the signed integer value of the given JSON number. It
// returns false if the number is not an integer or does not fit in bitSize.
func parseInt(raw string, bitSize int) (int64, bool) {
	s, ok := intString(raw)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, bitSize)
	if err != nil {
		return 0, false
	}
	return n, true
}

// parseUint returns the unsigned integer value of the given JSON number. It
// returns false if the number is not an unsigned integer or does not fit in
// bitSize.
func parseUint(raw string, bitSize int) (uint64, bool) {
	s, ok := intString(raw)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 10, bitSize)
	if err != nil {
		return 0, false
	}
	return n, true
}

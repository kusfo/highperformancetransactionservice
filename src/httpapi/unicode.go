package httpapi

import "unicode/utf8"

// validJSONUnicode checks syntactically valid JSON before encoding/json can
// replace invalid UTF-8 or unpaired UTF-16 escapes with U+FFFD. Escaped
// backslashes are skipped, so a literal sequence like \\ud800 remains valid.
func validJSONUnicode(data []byte) bool {
	if !utf8.Valid(data) {
		return false
	}
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		if i+4 >= len(data) {
			return false
		}
		code := hexCodeUnit(data[i+1 : i+5])
		i += 4
		if code >= 0xdc00 && code <= 0xdfff {
			return false
		}
		if code < 0xd800 || code > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		low := hexCodeUnit(data[i+3 : i+7])
		if low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

// JSON syntax validation has already established four hexadecimal digits.
func hexCodeUnit(digits []byte) uint16 {
	var value uint16
	for _, c := range digits {
		value <<= 4
		switch {
		case c >= '0' && c <= '9':
			value += uint16(c - '0')
		case c >= 'a' && c <= 'f':
			value += uint16(c - 'a' + 10)
		default:
			value += uint16(c - 'A' + 10)
		}
	}
	return value
}

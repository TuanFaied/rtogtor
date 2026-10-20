package ir

import (
	"encoding/base64"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TuanFaied/rtogtor/engine/rtgerr"
)

// Canonicalize validates the text form of a scalar value and returns its
// canonical text form as defined in specs/values.md §2.
//
// For float64, the result is either the RFC 8785 number text or one of the
// special strings "NaN", "Infinity", "-Infinity", "-0".
//
// json and array values are not scalar and return RTG-VAL-002.
func Canonicalize(t LogicalType, input string) (string, error) {
	switch t {
	case TypeBool:
		return canonicalBool(input)
	case TypeInt64:
		return canonicalInt64(input)
	case TypeDecimal:
		return canonicalDecimal(input)
	case TypeFloat64:
		return canonicalFloat64(input)
	case TypeString:
		return canonicalString(input)
	case TypeUUID:
		return canonicalUUID(input)
	case TypeDate:
		return canonicalDate(input)
	case TypeTime:
		return canonicalTime(input)
	case TypeTimeTZ:
		return canonicalTimeTZ(input)
	case TypeTimestamp:
		return canonicalTimestamp(input)
	case TypeTimestampTZ:
		return canonicalTimestampTZ(input)
	case TypeDuration:
		return canonicalDuration(input)
	case TypeBytes:
		return canonicalBytes(input)
	case TypeJSON, TypeArray:
		return "", rtgerr.New(rtgerr.ValUnsupportedType, "%s is not a scalar type and has no canonical text form", t)
	default:
		return "", rtgerr.New(rtgerr.ValUnsupportedType, "unknown logical type %q", string(t))
	}
}

func invalid(t LogicalType, input, reason string) error {
	// The value may be user data; quote it so control characters are visible.
	return rtgerr.New(rtgerr.ValInvalid, "invalid %s value %s: %s", t, strconv.Quote(truncate(input, 64)), reason)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	// Do not split a UTF-8 sequence.
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}

func canonicalBool(s string) (string, error) {
	if s == "true" || s == "false" {
		return s, nil
	}
	return "", invalid(TypeBool, s, `expected "true" or "false"`)
}

var intPattern = regexp.MustCompile(`^[+-]?[0-9]+$`)

func canonicalInt64(s string) (string, error) {
	if !intPattern.MatchString(s) {
		return "", invalid(TypeInt64, s, "expected an optionally signed decimal integer")
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return "", invalid(TypeInt64, s, "out of the 64-bit signed range")
	}
	return strconv.FormatInt(n, 10), nil
}

var decimalPattern = regexp.MustCompile(`^([+-]?)([0-9]+)(?:\.([0-9]+))?$`)

func canonicalDecimal(s string) (string, error) {
	switch s {
	case "NaN", "Infinity", "-Infinity":
		return s, nil
	}
	m := decimalPattern.FindStringSubmatch(s)
	if m == nil {
		return "", invalid(TypeDecimal, s, "expected digits with an optional fraction (no exponent)")
	}
	return canonicalDecimalParts(m[1] == "-", m[2], m[3]), nil
}

// canonicalDecimalParts normalizes sign, integer digits and fraction digits.
func canonicalDecimalParts(negative bool, intDigits, fracDigits string) string {
	intDigits = strings.TrimLeft(intDigits, "0")
	if intDigits == "" {
		intDigits = "0"
	}
	fracDigits = strings.TrimRight(fracDigits, "0")
	out := intDigits
	if fracDigits != "" {
		out += "." + fracDigits
	}
	if negative && out != "0" {
		out = "-" + out
	}
	return out
}

var floatPattern = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)

func canonicalFloat64(s string) (string, error) {
	switch s {
	case "NaN", "Infinity", "-Infinity", "-0":
		return s, nil
	}
	if !floatPattern.MatchString(s) {
		return "", invalid(TypeFloat64, s, "expected a decimal number, NaN, Infinity or -Infinity")
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) {
		return "", invalid(TypeFloat64, s, "out of the float64 range")
	}
	return FormatFloat64(f), nil
}

// FormatFloat64 returns the canonical text of f: RFC 8785 number formatting,
// with the special values NaN, Infinity, -Infinity and -0 spelled as strings.
func FormatFloat64(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0 && math.Signbit(f):
		return "-0"
	case f == 0:
		return "0"
	}
	return formatECMAScript(f)
}

// formatECMAScript implements ECMAScript Number::toString for finite non-zero
// values, as required by RFC 8785 §3.2.2.3.
func formatECMAScript(f float64) string {
	// Shortest round-trip digits in scientific form, e.g. "1.2345e+20".
	sci := strconv.FormatFloat(f, 'e', -1, 64)
	negative := sci[0] == '-'
	if negative {
		sci = sci[1:]
	}
	mantissa, expText, _ := strings.Cut(sci, "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	exp, _ := strconv.Atoi(expText)

	k := len(digits) // number of significant digits
	n := exp + 1     // value = 0.digits × 10^n
	var out string
	switch {
	case k <= n && n <= 21:
		out = digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		out = digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		out = "0." + strings.Repeat("0", -n) + digits
	default:
		e := n - 1
		sign := "+"
		if e < 0 {
			sign = "-"
			e = -e
		}
		if k == 1 {
			out = digits + "e" + sign + strconv.Itoa(e)
		} else {
			out = digits[:1] + "." + digits[1:] + "e" + sign + strconv.Itoa(e)
		}
	}
	if negative {
		out = "-" + out
	}
	return out
}

func canonicalString(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", rtgerr.New(rtgerr.ValInvalid, "invalid string value: not valid UTF-8")
	}
	return s, nil
}

func canonicalUUID(s string) (string, error) {
	if len(s) != 36 {
		return "", invalid(TypeUUID, s, "expected the 36-character hyphenated form")
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return "", invalid(TypeUUID, s, "expected hyphens at positions 9, 14, 19 and 24")
			}
			continue
		}
		if !isHex(c) {
			return "", invalid(TypeUUID, s, "expected hexadecimal digits")
		}
	}
	return strings.ToLower(s), nil
}

func isHex(c byte) bool {
	return ('0' <= c && c <= '9') || ('a' <= c && c <= 'f') || ('A' <= c && c <= 'F')
}

const (
	datePart = `([0-9]{4})-([0-9]{2})-([0-9]{2})`
	timePart = `([0-9]{2}):([0-9]{2}):([0-9]{2})(?:\.([0-9]{1,9}))?`
	zonePart = `(Z|[+-][0-9]{2}:[0-9]{2})`
)

var (
	datePattern        = regexp.MustCompile(`^` + datePart + `$`)
	timePattern        = regexp.MustCompile(`^` + timePart + `$`)
	timeTZPattern      = regexp.MustCompile(`^` + timePart + zonePart + `$`)
	timestampPattern   = regexp.MustCompile(`^` + datePart + `T` + timePart + `$`)
	timestampTZPattern = regexp.MustCompile(`^` + datePart + `T` + timePart + zonePart + `$`)
)

// atoi converts a regexp-validated digit string.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func validDate(y, m, d int) bool {
	if y < 1 || y > 9999 || m < 1 || m > 12 || d < 1 {
		return false
	}
	t := time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
	return t.Day() == d && int(t.Month()) == m
}

func validClock(h, m, s int) bool {
	return h < 24 && m < 60 && s < 60
}

// fraction returns ".digits" with trailing zeros removed, or "".
func fraction(digits string) string {
	digits = strings.TrimRight(digits, "0")
	if digits == "" {
		return ""
	}
	return "." + digits
}

// nanos converts 1–9 fractional digits to nanoseconds.
func nanos(digits string) int {
	if digits == "" {
		return 0
	}
	return atoi(digits + strings.Repeat("0", 9-len(digits)))
}

func canonicalDate(s string) (string, error) {
	m := datePattern.FindStringSubmatch(s)
	if m == nil {
		return "", invalid(TypeDate, s, "expected YYYY-MM-DD")
	}
	if !validDate(atoi(m[1]), atoi(m[2]), atoi(m[3])) {
		return "", invalid(TypeDate, s, "not a calendar date between 0001-01-01 and 9999-12-31")
	}
	return s, nil
}

func clockText(h, mi, sec string, frac string) string {
	return h + ":" + mi + ":" + sec + fraction(frac)
}

func canonicalTime(s string) (string, error) {
	m := timePattern.FindStringSubmatch(s)
	if m == nil {
		return "", invalid(TypeTime, s, "expected HH:MM:SS with up to 9 fractional digits")
	}
	if !validClock(atoi(m[1]), atoi(m[2]), atoi(m[3])) {
		return "", invalid(TypeTime, s, "time of day out of range")
	}
	return clockText(m[1], m[2], m[3], m[4]), nil
}

// offsetSeconds parses "Z" or "±HH:MM" and validates the range ±18:00.
func offsetSeconds(zone string) (int, bool) {
	if zone == "Z" {
		return 0, true
	}
	h, mi := atoi(zone[1:3]), atoi(zone[4:6])
	if mi >= 60 || h*60+mi > 18*60 {
		return 0, false
	}
	secs := (h*60 + mi) * 60
	if zone[0] == '-' {
		secs = -secs
	}
	return secs, true
}

func canonicalTimeTZ(s string) (string, error) {
	m := timeTZPattern.FindStringSubmatch(s)
	if m == nil {
		return "", invalid(TypeTimeTZ, s, "expected HH:MM:SS with up to 9 fractional digits and an offset (Z or ±HH:MM)")
	}
	if !validClock(atoi(m[1]), atoi(m[2]), atoi(m[3])) {
		return "", invalid(TypeTimeTZ, s, "time of day out of range")
	}
	off, ok := offsetSeconds(m[5])
	if !ok {
		return "", invalid(TypeTimeTZ, s, "offset out of range")
	}
	zone := m[5]
	if off == 0 {
		zone = "Z"
	}
	return clockText(m[1], m[2], m[3], m[4]) + zone, nil
}

func canonicalTimestamp(s string) (string, error) {
	m := timestampPattern.FindStringSubmatch(s)
	if m == nil {
		return "", invalid(TypeTimestamp, s, "expected YYYY-MM-DDTHH:MM:SS with up to 9 fractional digits and no offset")
	}
	if !validDate(atoi(m[1]), atoi(m[2]), atoi(m[3])) || !validClock(atoi(m[4]), atoi(m[5]), atoi(m[6])) {
		return "", invalid(TypeTimestamp, s, "date or time out of range")
	}
	return m[1] + "-" + m[2] + "-" + m[3] + "T" + clockText(m[4], m[5], m[6], m[7]), nil
}

func canonicalTimestampTZ(s string) (string, error) {
	m := timestampTZPattern.FindStringSubmatch(s)
	if m == nil {
		return "", invalid(TypeTimestampTZ, s, "expected an RFC 3339 timestamp with an offset (Z or ±HH:MM)")
	}
	y, mo, d := atoi(m[1]), atoi(m[2]), atoi(m[3])
	h, mi, sec := atoi(m[4]), atoi(m[5]), atoi(m[6])
	if !validDate(y, mo, d) || !validClock(h, mi, sec) {
		return "", invalid(TypeTimestampTZ, s, "date or time out of range")
	}
	off, ok := offsetSeconds(m[8])
	if !ok {
		return "", invalid(TypeTimestampTZ, s, "offset out of range")
	}
	utc := time.Date(y, time.Month(mo), d, h, mi, sec, nanos(m[7]), time.FixedZone("", off)).UTC()
	if utc.Year() < 1 || utc.Year() > 9999 {
		return "", invalid(TypeTimestampTZ, s, "out of range after conversion to UTC")
	}
	frac := ""
	if utc.Nanosecond() != 0 {
		frac = fraction(strconv.Itoa(1_000_000_000 + utc.Nanosecond())[1:])
	}
	return utc.Format("2006-01-02T15:04:05") + frac + "Z", nil
}

var durationPattern = regexp.MustCompile(`^P([+-]?[0-9]+)M([+-]?[0-9]+)DT([+-]?)([0-9]+)(?:\.([0-9]{1,9}))?S$`)

func canonicalDuration(s string) (string, error) {
	m := durationPattern.FindStringSubmatch(s)
	if m == nil {
		return "", invalid(TypeDuration, s, "expected P<months>M<days>DT<seconds>S")
	}
	months, err1 := strconv.ParseInt(m[1], 10, 64)
	days, err2 := strconv.ParseInt(m[2], 10, 64)
	_, err3 := strconv.ParseInt(m[4], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil {
		return "", invalid(TypeDuration, s, "component out of the 64-bit signed range")
	}
	seconds := canonicalDecimalParts(m[3] == "-", m[4], m[5])
	return "P" + strconv.FormatInt(months, 10) + "M" + strconv.FormatInt(days, 10) + "DT" + seconds + "S", nil
}

func canonicalBytes(s string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(s)
	// The decoder ignores CR/LF and tolerates non-zero padding bits;
	// re-encoding and comparing rejects every non-canonical form.
	if err != nil || base64.StdEncoding.EncodeToString(b) != s {
		return "", invalid(TypeBytes, s, "expected canonical padded standard Base64")
	}
	return s, nil
}

// Package esnum formats float64 values exactly as ECMAScript
// Number.prototype.toString does, as required by RFC 8785 §3.2.2.3.
package esnum

import (
	"strconv"
	"strings"
)

// Format returns the ECMAScript string form of a finite float64. Zero (of
// either sign) is "0". The caller must handle NaN and ±Inf.
func Format(f float64) string {
	if f == 0 {
		return "0"
	}
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

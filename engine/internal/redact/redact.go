// Package redact removes credentials from text before it reaches logs,
// errors, reports or the terminal.
package redact

import (
	"net/url"
	"strings"
)

// Placeholder replaces redacted text.
const Placeholder = "[REDACTED]"

// Secrets replaces every occurrence of each non-empty secret in msg. Even a
// short secret is redacted: garbling a message is better than leaking.
// Longer secrets are replaced first so a secret containing another is fully
// removed.
func Secrets(msg string, secrets ...string) string {
	sorted := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if s != "" {
			sorted = append(sorted, s)
		}
	}
	// Insertion sort by descending length; the list is tiny.
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && len(sorted[j]) > len(sorted[j-1]); j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	for _, s := range sorted {
		msg = strings.ReplaceAll(msg, s, Placeholder)
	}
	return msg
}

// URLSecrets returns the strings derived from a connection URL that must never
// be shown: the URL itself, its password (raw and percent-decoded), and the
// query string, which may carry passwords or key material.
func URLSecrets(raw string) []string {
	out := []string{raw}
	u, err := url.Parse(raw)
	if err != nil {
		// Unparseable: also hide everything after the scheme separator.
		if i := strings.Index(raw, "://"); i >= 0 {
			out = append(out, raw[i+3:])
		}
		return out
	}
	if pw, ok := u.User.Password(); ok {
		out = append(out, pw, url.QueryEscape(pw), url.PathEscape(pw))
	}
	if u.RawQuery != "" {
		out = append(out, u.RawQuery)
		// Parameters such as password or sslpassword can carry secrets, and
		// drivers may echo them individually.
		for _, values := range u.Query() {
			for _, v := range values {
				if v != "" {
					out = append(out, v)
				}
			}
		}
	}
	return out
}

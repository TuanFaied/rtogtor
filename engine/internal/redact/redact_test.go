package redact

import (
	"strings"
	"testing"
)

func TestURLSecretsRemovePassword(t *testing.T) {
	raw := "postgres://app:s3cr%2Ft!pw@db.internal:5432/shop?sslmode=require&sslpassword=keypass"
	msg := "failed to connect using " + raw + "; password s3cr/t!pw rejected; sslpassword=keypass"
	got := Secrets(msg, URLSecrets(raw)...)
	for _, leak := range []string{"s3cr/t!pw", "s3cr%2Ft!pw", "keypass", raw} {
		if strings.Contains(got, leak) {
			t.Errorf("redacted message still contains %q: %s", leak, got)
		}
	}
}

func TestUnparseableURL(t *testing.T) {
	raw := "postgres://user:hunter22@host:notaport/db\x7f"
	got := Secrets("cannot parse "+raw, URLSecrets(raw)...)
	if strings.Contains(got, "hunter22") {
		t.Errorf("password leaked: %s", got)
	}
}

func TestShortSecretsAreRedacted(t *testing.T) {
	if got := Secrets("user=app password=pw1", "pw1", ""); strings.Contains(got, "pw1") {
		t.Errorf("short password leaked: %q", got)
	}
}

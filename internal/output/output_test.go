package output

import (
	"strings"
	"testing"
)

func TestRedactRemovesTokenLikeValues(t *testing.T) {
	input := `request failed: xi-api-key: abc123 authorization BearerSomething api_key="secret"`
	got := Redact(input)
	if strings.Contains(got, "abc123") || strings.Contains(got, "BearerSomething") || strings.Contains(got, "secret") {
		t.Fatalf("Redact() leaked secret-like value: %q", got)
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatalf("Redact() = %q, want redaction marker", got)
	}
}

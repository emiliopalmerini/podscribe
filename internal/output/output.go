package output

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
)

type Envelope struct {
	OK    bool       `json:"ok"`
	Data  any        `json:"data,omitempty"`
	Error *ErrorInfo `json:"error,omitempty"`
}

type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

var tokenLike = regexp.MustCompile(`(?i)(xi-api-key|api[_-]?key|authorization|token)(["':=\s]+)([^"',\s]+)`)

func JSONSuccess(w io.Writer, data any) error {
	return writeJSON(w, Envelope{OK: true, Data: data})
}

func JSONError(w io.Writer, err error) error {
	return writeJSON(w, Envelope{
		OK: false,
		Error: &ErrorInfo{
			Code:    apperr.Code(err),
			Message: Redact(apperr.Message(err)),
		},
	})
}

func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func HumanError(w io.Writer, err error) {
	fmt.Fprintf(w, "Error: %s\n", Redact(apperr.Message(err)))
}

func Redact(s string) string {
	if s == "" {
		return s
	}
	redacted := tokenLike.ReplaceAllString(s, `$1$2[REDACTED]`)
	if len(redacted) > 4096 {
		return strings.TrimSpace(redacted[:4096]) + "..."
	}
	return redacted
}

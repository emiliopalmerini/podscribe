package elevenlabs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	el "github.com/emiliopalmerini/elevenlabs-go/elevenlabs"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
	"github.com/emiliopalmerini/podscribe/internal/output"
)

type APIError struct {
	StatusCode      int
	Status          string
	ProviderType    string
	ProviderCode    string
	ProviderStatus  string
	ProviderMessage string
	RequestID       string
	TraceID         string
	RetryAfter      string
	Body            string
	Validation      []string
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	return e.message()
}

func newAPIError(res *http.Response, body []byte) error {
	apiErr := parseAPIError(res, body)
	apiErr.redact()
	return apperr.Wrap(apiErr.code(), apiErr.message(), apiErr)
}

func newAPIErrorFromSDK(err *el.APIError) error {
	apiErr := &APIError{
		StatusCode:      err.StatusCode,
		Status:          err.Status,
		ProviderType:    err.ProviderType,
		ProviderCode:    err.ProviderCode,
		ProviderStatus:  err.ProviderStatus,
		ProviderMessage: firstNonEmpty(err.ProviderMessage, err.Message),
		RequestID:       err.RequestID,
		TraceID:         err.TraceID,
		RetryAfter:      err.RetryAfter,
		Body:            output.Redact(compactBody(err.Body)),
		Validation:      summarizeSDKValidation(err.Validation),
	}
	apiErr.redact()
	return apperr.Wrap(apiErr.code(), apiErr.message(), apiErr)
}

func parseAPIError(res *http.Response, body []byte) *APIError {
	apiErr := &APIError{
		StatusCode: res.StatusCode,
		Status:     res.Status,
		RequestID:  res.Header.Get("request-id"),
		TraceID:    res.Header.Get("x-trace-id"),
		RetryAfter: res.Header.Get("retry-after"),
		Body:       output.Redact(compactBody(body)),
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return apiErr
	}

	var parsed providerErrorBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return apiErr
	}
	apiErr.ProviderType = parsed.Type
	apiErr.ProviderCode = parsed.Code
	apiErr.ProviderStatus = parsed.Status
	apiErr.ProviderMessage = firstNonEmpty(parsed.Message, parsed.Error)
	apiErr.RequestID = firstNonEmpty(apiErr.RequestID, parsed.RequestID)

	if len(parsed.Detail) == 0 {
		return apiErr
	}
	var detail providerErrorDetail
	if err := json.Unmarshal(parsed.Detail, &detail); err == nil {
		apiErr.ProviderType = firstNonEmpty(apiErr.ProviderType, detail.Type)
		apiErr.ProviderCode = firstNonEmpty(apiErr.ProviderCode, detail.Code)
		apiErr.ProviderStatus = firstNonEmpty(apiErr.ProviderStatus, detail.Status)
		apiErr.ProviderMessage = firstNonEmpty(apiErr.ProviderMessage, detail.Message, detail.Error)
		apiErr.RequestID = firstNonEmpty(apiErr.RequestID, detail.RequestID)
		return apiErr
	}

	var detailText string
	if err := json.Unmarshal(parsed.Detail, &detailText); err == nil {
		apiErr.ProviderMessage = firstNonEmpty(apiErr.ProviderMessage, detailText)
		return apiErr
	}

	var validation []validationError
	if err := json.Unmarshal(parsed.Detail, &validation); err == nil {
		apiErr.Validation = summarizeValidation(validation)
		if len(apiErr.Validation) > 0 {
			apiErr.ProviderMessage = strings.Join(apiErr.Validation, "; ")
		}
	}
	return apiErr
}

func (e *APIError) redact() {
	e.ProviderMessage = output.Redact(e.ProviderMessage)
	for i, item := range e.Validation {
		e.Validation[i] = output.Redact(item)
	}
}

type providerErrorBody struct {
	Detail    json.RawMessage `json:"detail"`
	Type      string          `json:"type"`
	Code      string          `json:"code"`
	Status    string          `json:"status"`
	Message   string          `json:"message"`
	Error     string          `json:"error"`
	RequestID string          `json:"request_id"`
}

type providerErrorDetail struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	Error     string `json:"error"`
	RequestID string `json:"request_id"`
}

type validationError struct {
	Loc  []any  `json:"loc"`
	Msg  string `json:"msg"`
	Type string `json:"type"`
}

func (e *APIError) code() string {
	if e.ProviderCode == "quota_exceeded" {
		return apperr.CodeQuota
	}
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return apperr.CodeAuth
	case http.StatusForbidden:
		return apperr.CodeForbidden
	case http.StatusNotFound:
		return apperr.CodeNotFound
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return apperr.CodeInvalidInput
	case http.StatusTooManyRequests:
		return apperr.CodeRateLimited
	default:
		return apperr.CodeAPI
	}
}

func (e *APIError) message() string {
	message := firstNonEmpty(e.ProviderMessage, e.Body)
	switch e.code() {
	case apperr.CodeQuota:
		return withRequestMeta("ElevenLabs quota exceeded", message, e)
	case apperr.CodeAuth:
		return withRequestMeta("ElevenLabs authentication failed", message, e)
	case apperr.CodeForbidden:
		return withRequestMeta("ElevenLabs request forbidden", message, e)
	case apperr.CodeNotFound:
		return withRequestMeta("ElevenLabs resource not found", message, e)
	case apperr.CodeRateLimited:
		return withRequestMeta("ElevenLabs rate limit exceeded", message, e)
	case apperr.CodeInvalidInput:
		if len(e.Validation) > 0 {
			return withRequestMeta("ElevenLabs request validation failed", strings.Join(e.Validation, "; "), e)
		}
		return withRequestMeta(fmt.Sprintf("ElevenLabs API returned %s", e.Status), message, e)
	default:
		return withRequestMeta(fmt.Sprintf("ElevenLabs API returned %s", e.Status), message, e)
	}
}

func withRequestMeta(prefix, message string, apiErr *APIError) string {
	var b strings.Builder
	b.WriteString(prefix)
	if strings.TrimSpace(message) != "" {
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(message))
	}
	var meta []string
	if apiErr.RequestID != "" {
		meta = append(meta, "request_id: "+apiErr.RequestID)
	}
	if apiErr.TraceID != "" {
		meta = append(meta, "trace_id: "+apiErr.TraceID)
	}
	if apiErr.RetryAfter != "" {
		meta = append(meta, "retry_after: "+apiErr.RetryAfter)
	}
	if len(meta) > 0 {
		b.WriteString(" (")
		b.WriteString(strings.Join(meta, ", "))
		b.WriteString(")")
	}
	return b.String()
}

func summarizeValidation(items []validationError) []string {
	const maxValidationItems = 3
	summaries := make([]string, 0, len(items))
	for _, item := range items {
		msg := firstNonEmpty(item.Msg, item.Type)
		if msg == "" {
			continue
		}
		loc := formatValidationLoc(item.Loc)
		if loc != "" {
			msg = loc + ": " + msg
		}
		summaries = append(summaries, msg)
		if len(summaries) == maxValidationItems {
			break
		}
	}
	if len(items) > maxValidationItems {
		summaries = append(summaries, fmt.Sprintf("%d more validation errors", len(items)-maxValidationItems))
	}
	return summaries
}

func summarizeSDKValidation(items []el.ValidationError) []string {
	validation := make([]validationError, 0, len(items))
	for _, item := range items {
		validation = append(validation, validationError{
			Loc:  item.Loc,
			Msg:  item.Msg,
			Type: item.Type,
		})
	}
	return summarizeValidation(validation)
}

func formatValidationLoc(parts []any) string {
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		switch v := part.(type) {
		case string:
			if v != "" {
				labels = append(labels, v)
			}
		case float64:
			labels = append(labels, fmt.Sprintf("%.0f", v))
		default:
			labels = append(labels, fmt.Sprint(v))
		}
	}
	return strings.Join(labels, ".")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

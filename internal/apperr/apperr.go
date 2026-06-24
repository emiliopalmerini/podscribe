package apperr

import "errors"

const (
	CodeAPI          = "api_error"
	CodeAuth         = "auth_error"
	CodeConfig       = "config_error"
	CodeFilesystem   = "filesystem_error"
	CodeForbidden    = "forbidden_error"
	CodeInvalidInput = "invalid_input"
	CodeNotFound     = "not_found"
	CodeQuota        = "quota_error"
	CodeRateLimited  = "rate_limited"
	CodeNetwork      = "network_error"
	CodeUnexpected   = "unexpected_error"
)

type Error struct {
	Code    string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Code
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func New(code, message string) error {
	return &Error{Code: code, Message: message}
}

func Wrap(code, message string, err error) error {
	return &Error{Code: code, Message: message, Err: err}
}

func Code(err error) string {
	var appErr *Error
	if errors.As(err, &appErr) && appErr.Code != "" {
		return appErr.Code
	}
	return CodeUnexpected
}

func Message(err error) string {
	var appErr *Error
	if errors.As(err, &appErr) && appErr.Message != "" {
		return appErr.Message
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

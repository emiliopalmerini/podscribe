package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	el "github.com/emiliopalmerini/elevenlabs-go"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
	"github.com/emiliopalmerini/podscribe/internal/transcription"
)

const (
	defaultRetryAttempts  = 3
	defaultRetryBaseDelay = 200 * time.Millisecond
	defaultRetryMaxDelay  = 2 * time.Second
)

type Client struct {
	BaseURL        string
	APIKey         string
	HTTPClient     *http.Client
	retryAttempts  int
	retryBaseDelay time.Duration
}

var (
	_ transcription.Provider  = (*Client)(nil)
	_ transcription.RawGetter = (*Client)(nil)
)

func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Minute,
		},
		retryAttempts:  defaultRetryAttempts,
		retryBaseDelay: defaultRetryBaseDelay,
	}
}

func (c *Client) Check(ctx context.Context) error {
	client, err := c.sdkClient()
	if err != nil {
		return err
	}
	if _, err := client.ListModels(ctx); err != nil {
		return wrapSDKError(err, "")
	}
	return nil
}

func (c *Client) UserID(ctx context.Context) (string, error) {
	client, err := c.sdkClient()
	if err != nil {
		return "", err
	}
	user, err := client.GetUser(ctx)
	if err != nil {
		return "", wrapSDKError(err, "")
	}
	if user == nil {
		return "", apperr.New(apperr.CodeAPI, "ElevenLabs user response was empty")
	}
	if strings.TrimSpace(user.UserID) == "" {
		return "", apperr.New(apperr.CodeAPI, "ElevenLabs user response did not include user_id")
	}
	return user.UserID, nil
}

func (c *Client) Transcribe(ctx context.Context, req transcription.Request) (transcription.Transcript, error) {
	client, err := c.sdkClient()
	if err != nil {
		return transcription.Transcript{}, err
	}
	file, size, err := openAudioFile(req.FilePath)
	if err != nil {
		return transcription.Transcript{}, err
	}
	defer file.Close()

	transcript, err := client.CreateTranscript(ctx, sdkTranscriptRequest(req, file, size))
	if err != nil {
		return transcription.Transcript{}, wrapSDKError(err, "could not parse ElevenLabs transcript response")
	}
	return convertTranscript(transcript)
}

func (c *Client) SubmitWebhook(ctx context.Context, req transcription.Request) (transcription.WebhookResponse, error) {
	client, err := c.sdkClient()
	if err != nil {
		return transcription.WebhookResponse{}, err
	}
	file, size, err := openAudioFile(req.FilePath)
	if err != nil {
		return transcription.WebhookResponse{}, err
	}
	defer file.Close()

	response, err := client.SubmitTranscriptWebhook(ctx, sdkTranscriptRequest(req, file, size))
	if err != nil {
		return transcription.WebhookResponse{}, wrapSDKError(err, "could not parse ElevenLabs webhook response")
	}
	if response == nil {
		return transcription.WebhookResponse{}, apperr.New(apperr.CodeAPI, "ElevenLabs webhook response was empty")
	}
	return transcription.WebhookResponse{
		Message:         response.Message,
		RequestID:       response.RequestID,
		TranscriptionID: response.TranscriptionID,
	}, nil
}

func (c *Client) GetTranscript(ctx context.Context, id string) (transcription.Transcript, error) {
	client, err := c.sdkClient()
	if err != nil {
		return transcription.Transcript{}, err
	}
	transcript, err := client.GetTranscript(ctx, id)
	if err != nil {
		return transcription.Transcript{}, wrapSDKError(err, "could not parse ElevenLabs transcript response")
	}
	return convertTranscript(transcript)
}

func (c *Client) DeleteTranscript(ctx context.Context, id string) (any, error) {
	client, err := c.sdkClient()
	if err != nil {
		return nil, err
	}
	resp, err := client.DeleteTranscriptWithResponse(ctx, id)
	if err != nil {
		return nil, wrapSDKError(err, "")
	}
	if resp == nil {
		return nil, nil
	}
	return resp.Data, nil
}

func (c *Client) sdkClient() (*el.Client, error) {
	if strings.TrimSpace(c.APIKey) == "" {
		return nil, apperr.New(apperr.CodeAuth, "missing ElevenLabs API key")
	}
	if err := validateBaseURL(c.BaseURL); err != nil {
		return nil, err
	}
	return el.NewClient(
		c.APIKey,
		el.WithBaseURL(strings.TrimRight(c.BaseURL, "/")),
		el.WithHTTPClient(c.httpClient()),
		el.WithRetryConfig(c.sdkRetryConfig()),
	), nil
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Client) sdkRetryConfig() el.RetryConfig {
	baseDelay := c.retryBaseDelay
	maxDelay := defaultRetryMaxDelay
	if baseDelay <= 0 {
		baseDelay = time.Nanosecond
		maxDelay = time.Nanosecond
	}
	return el.RetryConfig{
		MaxAttempts: c.attempts(),
		BaseDelay:   baseDelay,
		MaxDelay:    maxDelay,
	}
}

func sdkTranscriptRequest(req transcription.Request, file *os.File, size int64) el.CreateTranscriptRequest {
	out := el.CreateTranscriptRequest{
		ModelID:                 req.Model,
		File:                    &el.File{Name: filepath.Base(req.FilePath), Reader: file, SizeBytes: size},
		LanguageCode:            req.Language,
		TimestampsGranularity:   req.TimestampsGranularity,
		NumSpeakers:             req.Speakers,
		WebhookID:               req.WebhookID,
		WebhookMetadata:         req.WebhookMetadata,
		MultichannelOutputStyle: req.MultichannelOutputStyle,
		Keyterms:                req.Keyterms,
	}
	if req.OnUploadProgress != nil {
		out.OnUploadProgress = func(update el.UploadProgress) {
			req.OnUploadProgress(transcription.UploadProgress{
				SentBytes:  update.SentBytes,
				TotalBytes: update.TotalBytes,
				Done:       update.Done,
				Attempt:    update.Attempt,
			})
		}
	}
	if req.Diarize {
		out.Diarize = boolPtr(true)
	}
	out.TagAudioEvents = boolPtr(req.TagAudioEvents)
	if req.Clean {
		out.NoVerbatim = boolPtr(true)
	}
	if req.UseMultiChannel {
		out.UseMultiChannel = boolPtr(true)
	}
	return out
}

func openAudioFile(path string) (*os.File, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not open audio file %s", path), err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, 0, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not inspect audio file %s", path), err)
	}
	return file, info.Size(), nil
}

func boolPtr(v bool) *bool {
	return &v
}

func convertTranscript(in *el.Transcript) (transcription.Transcript, error) {
	if in == nil {
		return transcription.Transcript{}, apperr.New(apperr.CodeAPI, "ElevenLabs transcript response was empty")
	}
	var out transcription.Transcript
	if err := convertJSON(in, &out); err != nil {
		return transcription.Transcript{}, apperr.Wrap(apperr.CodeAPI, "could not convert ElevenLabs transcript response", err)
	}
	return out, nil
}

func convertJSON(in, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func RawGetClient(baseURL, apiKey string) *Client {
	return NewClient(baseURL, apiKey)
}

func (c *Client) RawGet(ctx context.Context, path string) ([]byte, error) {
	if c.APIKey == "" {
		return nil, apperr.New(apperr.CodeAuth, "missing ElevenLabs API key")
	}
	if !strings.HasPrefix(path, "/v1/") {
		return nil, apperr.New(apperr.CodeInvalidInput, "raw GET path must start with /v1/")
	}
	if strings.Contains(path, "://") || strings.HasPrefix(path, "//") {
		return nil, apperr.New(apperr.CodeInvalidInput, "raw GET path must be a relative API path")
	}
	endpoint, err := c.endpoint(path)
	if err != nil {
		return nil, err
	}
	return c.do(ctx, func(ctx context.Context) (*http.Request, error) {
		return c.newAPIRequest(ctx, http.MethodGet, endpoint, "could not build raw GET request")
	})
}

func (c *Client) newAPIRequest(ctx context.Context, method, endpoint, buildError string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInvalidInput, buildError, err)
	}
	req.Header.Set("xi-api-key", c.APIKey)
	return req, nil
}

func (c *Client) endpoint(path string) (string, error) {
	if err := validateBaseURL(c.BaseURL); err != nil {
		return "", err
	}
	return strings.TrimRight(c.BaseURL, "/") + path, nil
}

func validateBaseURL(baseURL string) error {
	if strings.TrimSpace(baseURL) == "" {
		return apperr.New(apperr.CodeConfig, "missing ElevenLabs base URL")
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return apperr.New(apperr.CodeConfig, "invalid ElevenLabs base URL")
	}
	return nil
}

func (c *Client) do(ctx context.Context, buildRequest func(context.Context) (*http.Request, error)) ([]byte, error) {
	httpClient := c.httpClient()
	attempts := c.attempts()
	for attempt := 1; attempt <= attempts; attempt++ {
		req, err := buildRequest(ctx)
		if err != nil {
			return nil, err
		}
		res, err := httpClient.Do(req)
		if err != nil {
			if !isRetryableConnectionError(err) || ctx.Err() != nil || attempt == attempts {
				return nil, apperr.Wrap(apperr.CodeNetwork, "request to ElevenLabs failed", err)
			}
			if err := sleepContext(ctx, c.retryDelay(attempt)); err != nil {
				return nil, apperr.Wrap(apperr.CodeNetwork, "request to ElevenLabs failed", err)
			}
			continue
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			return nil, apperr.Wrap(apperr.CodeNetwork, "could not read ElevenLabs response", err)
		}
		if shouldRetryRateLimit(res.StatusCode, attempt, attempts) {
			if err := sleepContext(ctx, c.rateLimitRetryDelay(res.Header.Get("retry-after"), attempt)); err != nil {
				return nil, apperr.Wrap(apperr.CodeNetwork, "request to ElevenLabs failed", err)
			}
			continue
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return nil, newAPIError(res, body)
		}
		return body, nil
	}
	return nil, apperr.New(apperr.CodeNetwork, "request to ElevenLabs failed")
}

func wrapSDKError(err error, parseMessage string) error {
	if err == nil {
		return nil
	}
	var apiErr *el.APIError
	if errors.As(err, &apiErr) {
		return newAPIErrorFromSDK(apiErr)
	}
	if parseMessage != "" && strings.Contains(err.Error(), "decode response") {
		return apperr.Wrap(apperr.CodeAPI, parseMessage, err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || isNetworkError(err) {
		return apperr.Wrap(apperr.CodeNetwork, "request to ElevenLabs failed", err)
	}
	if strings.Contains(err.Error(), "base url") {
		return apperr.Wrap(apperr.CodeConfig, "invalid ElevenLabs base URL", err)
	}
	if strings.Contains(err.Error(), "required") || strings.Contains(err.Error(), "audio source") {
		return apperr.Wrap(apperr.CodeInvalidInput, err.Error(), err)
	}
	return apperr.Wrap(apperr.CodeUnexpected, "ElevenLabs SDK request failed", err)
}

func shouldRetryRateLimit(statusCode, attempt, attempts int) bool {
	return statusCode == http.StatusTooManyRequests && attempt < attempts
}

func (c *Client) attempts() int {
	if c.retryAttempts > 0 {
		return c.retryAttempts
	}
	return defaultRetryAttempts
}

func (c *Client) retryDelay(attempt int) time.Duration {
	if c.retryBaseDelay <= 0 {
		return 0
	}
	delay := c.retryBaseDelay
	for i := 1; i < attempt; i++ {
		if delay >= defaultRetryMaxDelay/2 {
			return defaultRetryMaxDelay
		}
		delay *= 2
	}
	if delay > defaultRetryMaxDelay {
		return defaultRetryMaxDelay
	}
	return delay
}

func (c *Client) rateLimitRetryDelay(retryAfter string, attempt int) time.Duration {
	if delay, ok := parseRetryAfter(retryAfter, time.Now()); ok {
		return delay
	}
	return c.retryDelay(attempt)
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	when, err := http.ParseTime(value)
	if err != nil {
		return 0, false
	}
	if !when.After(now) {
		return 0, true
	}
	return when.Sub(now), true
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func isNetworkError(err error) bool {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func isRetryableConnectionError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		err = urlErr.Err
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}

func compactBody(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) == 0 {
		return ""
	}
	if len(body) > 4096 {
		body = body[:4096]
	}
	return string(body)
}

package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
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

type TranscribeOptions struct {
	FilePath                string
	Model                   string
	Language                string
	Diarize                 bool
	Speakers                int
	Keyterms                []string
	Clean                   bool
	TagAudioEvents          bool
	TimestampsGranularity   string
	UseMultiChannel         bool
	MultichannelOutputStyle string
	Webhook                 bool
	WebhookID               string
	WebhookMetadata         map[string]any
	OnUploadProgress        func(UploadProgress)
}

type formField struct {
	name  string
	value string
}

type UploadProgress struct {
	SentBytes  int64
	TotalBytes int64
}

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

func (c *Client) TranscribeFile(ctx context.Context, opts TranscribeOptions) (TranscriptResponse, []byte, error) {
	if c.APIKey == "" {
		return TranscriptResponse{}, nil, apperr.New(apperr.CodeAuth, "missing ElevenLabs API key")
	}
	endpoint, err := c.endpoint("/v1/speech-to-text")
	if err != nil {
		return TranscriptResponse{}, nil, err
	}

	raw, err := c.do(ctx, func(ctx context.Context) (*http.Request, error) {
		return c.newTranscribeRequest(ctx, endpoint, opts)
	})
	if err != nil {
		return TranscriptResponse{}, nil, err
	}
	var transcript TranscriptResponse
	if err := json.Unmarshal(raw, &transcript); err != nil {
		return TranscriptResponse{}, raw, apperr.Wrap(apperr.CodeAPI, "could not parse ElevenLabs transcript response", err)
	}
	return transcript, raw, nil
}

func (c *Client) SubmitTranscriptionWebhook(ctx context.Context, opts TranscribeOptions) (WebhookResponse, []byte, error) {
	if c.APIKey == "" {
		return WebhookResponse{}, nil, apperr.New(apperr.CodeAuth, "missing ElevenLabs API key")
	}
	endpoint, err := c.endpoint("/v1/speech-to-text")
	if err != nil {
		return WebhookResponse{}, nil, err
	}
	opts.Webhook = true

	raw, err := c.do(ctx, func(ctx context.Context) (*http.Request, error) {
		return c.newTranscribeRequest(ctx, endpoint, opts)
	})
	if err != nil {
		return WebhookResponse{}, nil, err
	}
	var response WebhookResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return WebhookResponse{}, raw, apperr.Wrap(apperr.CodeAPI, "could not parse ElevenLabs webhook response", err)
	}
	return response, raw, nil
}

func (c *Client) newTranscribeRequest(ctx context.Context, endpoint string, opts TranscribeOptions) (*http.Request, error) {
	file, err := os.Open(opts.FilePath)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not open audio file %s", opts.FilePath), err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not inspect audio file %s", opts.FilePath), err)
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		file.Close()
		pw.CloseWithError(err)
		return nil, apperr.Wrap(apperr.CodeInvalidInput, "could not build transcription request", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("xi-api-key", c.APIKey)

	go func() {
		defer file.Close()
		if err := writeTranscribeMultipart(writer, opts, file, info.Size()); err != nil {
			pw.CloseWithError(err)
			return
		}
		if err := writer.Close(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()

	return req, nil
}

func (c *Client) GetTranscript(ctx context.Context, id string) (TranscriptResponse, []byte, error) {
	if c.APIKey == "" {
		return TranscriptResponse{}, nil, apperr.New(apperr.CodeAuth, "missing ElevenLabs API key")
	}
	endpoint, err := c.endpoint("/v1/speech-to-text/transcripts/" + url.PathEscape(id))
	if err != nil {
		return TranscriptResponse{}, nil, err
	}
	raw, err := c.do(ctx, func(ctx context.Context) (*http.Request, error) {
		return c.newAPIRequest(ctx, http.MethodGet, endpoint, "could not build get transcript request")
	})
	if err != nil {
		return TranscriptResponse{}, nil, err
	}
	var transcript TranscriptResponse
	if err := json.Unmarshal(raw, &transcript); err != nil {
		return TranscriptResponse{}, raw, apperr.Wrap(apperr.CodeAPI, "could not parse ElevenLabs transcript response", err)
	}
	return transcript, raw, nil
}

func (c *Client) DeleteTranscript(ctx context.Context, id string) ([]byte, error) {
	if c.APIKey == "" {
		return nil, apperr.New(apperr.CodeAuth, "missing ElevenLabs API key")
	}
	endpoint, err := c.endpoint("/v1/speech-to-text/transcripts/" + url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	return c.do(ctx, func(ctx context.Context) (*http.Request, error) {
		return c.newAPIRequest(ctx, http.MethodDelete, endpoint, "could not build delete transcript request")
	})
}

func (c *Client) GetUser(ctx context.Context) (UserResponse, []byte, error) {
	if c.APIKey == "" {
		return UserResponse{}, nil, apperr.New(apperr.CodeAuth, "missing ElevenLabs API key")
	}
	endpoint, err := c.endpoint("/v1/user")
	if err != nil {
		return UserResponse{}, nil, err
	}
	raw, err := c.do(ctx, func(ctx context.Context) (*http.Request, error) {
		return c.newAPIRequest(ctx, http.MethodGet, endpoint, "could not build get user request")
	})
	if err != nil {
		return UserResponse{}, nil, err
	}
	var user UserResponse
	if err := json.Unmarshal(raw, &user); err != nil {
		return UserResponse{}, raw, apperr.Wrap(apperr.CodeAPI, "could not parse ElevenLabs user response", err)
	}
	if strings.TrimSpace(user.UserID) == "" {
		return UserResponse{}, raw, apperr.New(apperr.CodeAPI, "ElevenLabs user response did not include user_id")
	}
	return user, raw, nil
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

func writeTranscribeMultipart(writer *multipart.Writer, opts TranscribeOptions, file *os.File, totalBytes int64) error {
	fields := []formField{
		{name: "model_id", value: opts.Model},
		{name: "timestamps_granularity", value: opts.TimestampsGranularity},
	}
	if opts.UseMultiChannel {
		fields = append(fields, formField{name: "use_multi_channel", value: "true"})
		if opts.MultichannelOutputStyle != "" {
			fields = append(fields, formField{name: "multichannel_output_style", value: opts.MultichannelOutputStyle})
		}
	}
	if opts.Language != "" {
		fields = append(fields, formField{name: "language_code", value: opts.Language})
	}
	if opts.Diarize {
		fields = append(fields, formField{name: "diarize", value: "true"})
	}
	if opts.Speakers > 0 {
		fields = append(fields, formField{name: "num_speakers", value: strconv.Itoa(opts.Speakers)})
	}
	if !opts.TagAudioEvents {
		fields = append(fields, formField{name: "tag_audio_events", value: "false"})
	}
	if opts.Clean {
		fields = append(fields, formField{name: "no_verbatim", value: "true"})
	}
	if opts.Webhook {
		fields = append(fields, formField{name: "webhook", value: "true"})
	}
	if opts.WebhookID != "" {
		fields = append(fields, formField{name: "webhook_id", value: opts.WebhookID})
	}
	if len(opts.WebhookMetadata) > 0 {
		metadata, err := json.Marshal(opts.WebhookMetadata)
		if err != nil {
			return apperr.Wrap(apperr.CodeInvalidInput, "could not encode webhook metadata", err)
		}
		fields = append(fields, formField{name: "webhook_metadata", value: string(metadata)})
	}
	for _, field := range fields {
		if err := writer.WriteField(field.name, field.value); err != nil {
			return apperr.Wrap(apperr.CodeFilesystem, "could not write multipart field", err)
		}
	}
	for _, term := range opts.Keyterms {
		if err := writer.WriteField("keyterms", term); err != nil {
			return apperr.Wrap(apperr.CodeFilesystem, "could not write keyterm field", err)
		}
	}

	partHeader := make(textproto.MIMEHeader)
	filename := filepath.Base(opts.FilePath)
	partHeader.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, escapeQuotes(filename)))
	partHeader.Set("Content-Type", contentTypeFor(filename))
	part, err := writer.CreatePart(partHeader)
	if err != nil {
		return apperr.Wrap(apperr.CodeFilesystem, "could not create multipart file part", err)
	}
	var reader io.Reader = file
	if opts.OnUploadProgress != nil {
		opts.OnUploadProgress(UploadProgress{SentBytes: 0, TotalBytes: totalBytes})
		reader = &progressReader{
			reader: file,
			total:  totalBytes,
			report: opts.OnUploadProgress,
		}
	}
	if _, err := io.Copy(part, reader); err != nil {
		return apperr.Wrap(apperr.CodeFilesystem, "could not stream audio file into multipart request", err)
	}
	return nil
}

type progressReader struct {
	reader io.Reader
	sent   int64
	total  int64
	report func(UploadProgress)
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.sent += int64(n)
		r.report(UploadProgress{SentBytes: r.sent, TotalBytes: r.total})
	}
	return n, err
}

func escapeQuotes(s string) string {
	return strings.NewReplacer("\\", "\\\\", `"`, "\\\"").Replace(s)
}

func contentTypeFor(filename string) string {
	if typ := mime.TypeByExtension(filepath.Ext(filename)); typ != "" {
		return typ
	}
	return "application/octet-stream"
}

func (c *Client) endpoint(path string) (string, error) {
	if strings.TrimSpace(c.BaseURL) == "" {
		return "", apperr.New(apperr.CodeConfig, "missing ElevenLabs base URL")
	}
	base, err := url.Parse(c.BaseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", apperr.New(apperr.CodeConfig, "invalid ElevenLabs base URL")
	}
	return strings.TrimRight(c.BaseURL, "/") + path, nil
}

func (c *Client) do(ctx context.Context, buildRequest func(context.Context) (*http.Request, error)) ([]byte, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
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

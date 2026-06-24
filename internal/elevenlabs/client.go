package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
	"github.com/emiliopalmerini/podscribe/internal/output"
)

type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type TranscribeOptions struct {
	FilePath              string
	Model                 string
	Language              string
	Diarize               bool
	Speakers              int
	Keyterms              []string
	Clean                 bool
	TagAudioEvents        bool
	TimestampsGranularity string
	OnUploadProgress      func(UploadProgress)
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

	file, err := os.Open(opts.FilePath)
	if err != nil {
		return TranscriptResponse{}, nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not open audio file %s", opts.FilePath), err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return TranscriptResponse{}, nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not inspect audio file %s", opts.FilePath), err)
	}

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		_ = file.Close()
		_ = pw.CloseWithError(err)
		return TranscriptResponse{}, nil, apperr.Wrap(apperr.CodeInvalidInput, "could not build transcription request", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("xi-api-key", c.APIKey)

	go func() {
		defer file.Close()
		if err := writeTranscribeMultipart(writer, opts, file, info.Size()); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		if err := writer.Close(); err != nil {
			_ = pw.CloseWithError(err)
			return
		}
		_ = pw.Close()
	}()

	raw, err := c.do(req)
	if err != nil {
		return TranscriptResponse{}, nil, err
	}
	var transcript TranscriptResponse
	if err := json.Unmarshal(raw, &transcript); err != nil {
		return TranscriptResponse{}, raw, apperr.Wrap(apperr.CodeAPI, "could not parse ElevenLabs transcript response", err)
	}
	return transcript, raw, nil
}

func (c *Client) GetTranscript(ctx context.Context, id string) (TranscriptResponse, []byte, error) {
	if c.APIKey == "" {
		return TranscriptResponse{}, nil, apperr.New(apperr.CodeAuth, "missing ElevenLabs API key")
	}
	endpoint, err := c.endpoint("/v1/speech-to-text/transcripts/" + url.PathEscape(id))
	if err != nil {
		return TranscriptResponse{}, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return TranscriptResponse{}, nil, apperr.Wrap(apperr.CodeInvalidInput, "could not build get transcript request", err)
	}
	req.Header.Set("xi-api-key", c.APIKey)

	raw, err := c.do(req)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInvalidInput, "could not build delete transcript request", err)
	}
	req.Header.Set("xi-api-key", c.APIKey)
	return c.do(req)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeInvalidInput, "could not build raw GET request", err)
	}
	req.Header.Set("xi-api-key", c.APIKey)
	return c.do(req)
}

func writeTranscribeMultipart(writer *multipart.Writer, opts TranscribeOptions, file *os.File, totalBytes int64) error {
	fields := []formField{
		{name: "model_id", value: opts.Model},
		{name: "timestamps_granularity", value: opts.TimestampsGranularity},
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

func (c *Client) do(req *http.Request) ([]byte, error) {
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeNetwork, "request to ElevenLabs failed", err)
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeNetwork, "could not read ElevenLabs response", err)
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, apperr.New(apperr.CodeAPI, fmt.Sprintf("ElevenLabs API returned %s: %s", res.Status, output.Redact(compactBody(body))))
	}
	return body, nil
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

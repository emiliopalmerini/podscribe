package elevenlabs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
)

func TestTranscribeFileStreamsMultipartRequest(t *testing.T) {
	audio := t.TempDir() + "/episode.mp3"
	if err := osWriteFile(audio, []byte("fake audio")); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var sawRequest bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawRequest = true
		if r.Method != http.MethodPost || r.URL.Path != "/v1/speech-to-text" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("xi-api-key"); got != "test-key" {
			t.Fatalf("xi-api-key = %q", got)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader() error = %v", err)
		}
		fields := map[string][]string{}
		var fileContent string
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("NextPart() error = %v", err)
			}
			b, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("ReadAll(part) error = %v", err)
			}
			if part.FormName() == "file" {
				fileContent = string(b)
				if part.FileName() != "episode.mp3" {
					t.Fatalf("file name = %q", part.FileName())
				}
				continue
			}
			fields[part.FormName()] = append(fields[part.FormName()], string(b))
		}
		assertField(t, fields, "model_id", "scribe_v2")
		assertField(t, fields, "timestamps_granularity", "word")
		assertField(t, fields, "language_code", "en")
		assertField(t, fields, "diarize", "true")
		assertField(t, fields, "num_speakers", "2")
		assertField(t, fields, "tag_audio_events", "false")
		assertField(t, fields, "no_verbatim", "true")
		if got := strings.Join(fields["keyterms"], ","); got != "Emilio,Podscribe" {
			t.Fatalf("keyterms = %q", got)
		}
		if fileContent != "fake audio" {
			t.Fatalf("file content = %q", fileContent)
		}
		_, _ = w.Write([]byte(`{"language_code":"en","text":"Hello","words":[],"transcription_id":"tx_123"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	client.HTTPClient = server.Client()

	var progress []UploadProgress
	resp, raw, err := client.TranscribeFile(context.Background(), TranscribeOptions{
		FilePath:              audio,
		Model:                 "scribe_v2",
		Language:              "en",
		Diarize:               true,
		Speakers:              2,
		Keyterms:              []string{"Emilio", "Podscribe"},
		Clean:                 true,
		TagAudioEvents:        false,
		TimestampsGranularity: "word",
		OnUploadProgress: func(update UploadProgress) {
			progress = append(progress, update)
		},
	})
	if err != nil {
		t.Fatalf("TranscribeFile() error = %v", err)
	}
	if !sawRequest {
		t.Fatal("server did not receive request")
	}
	if resp.TranscriptionID != "tx_123" {
		t.Fatalf("transcription ID = %q", resp.TranscriptionID)
	}
	if !json.Valid(raw) {
		t.Fatalf("raw response is not JSON: %q", string(raw))
	}
	if len(progress) == 0 {
		t.Fatal("upload progress was not reported")
	}
	lastProgress := progress[len(progress)-1]
	if lastProgress.SentBytes != int64(len("fake audio")) || lastProgress.TotalBytes != int64(len("fake audio")) {
		t.Fatalf("last progress = %+v, want sent and total %d", lastProgress, len("fake audio"))
	}
}

func TestTranscribeFileSendsMultichannelFields(t *testing.T) {
	audio := t.TempDir() + "/episode.flac"
	if err := osWriteFile(audio, []byte("fake multichannel audio")); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var fields map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader() error = %v", err)
		}
		fields = map[string][]string{}
		var sawFile bool
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("NextPart() error = %v", err)
			}
			b, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("ReadAll(part) error = %v", err)
			}
			if part.FormName() == "file" {
				sawFile = true
				continue
			}
			fields[part.FormName()] = append(fields[part.FormName()], string(b))
		}
		if !sawFile {
			t.Fatal("multipart request did not include file")
		}
		_, _ = w.Write([]byte(`{"language_code":"en","text":"Hello","words":[],"transcription_id":"tx_123"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	client.HTTPClient = server.Client()

	_, _, err := client.TranscribeFile(context.Background(), TranscribeOptions{
		FilePath:                audio,
		Model:                   "scribe_v2",
		TagAudioEvents:          true,
		TimestampsGranularity:   "word",
		UseMultiChannel:         true,
		MultichannelOutputStyle: "combined",
	})
	if err != nil {
		t.Fatalf("TranscribeFile() error = %v", err)
	}
	assertField(t, fields, "use_multi_channel", "true")
	assertField(t, fields, "multichannel_output_style", "combined")
}

func TestGetDeleteAndRawGET(t *testing.T) {
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		if r.Header.Get("xi-api-key") != "test-key" {
			t.Fatalf("missing API key")
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/speech-to-text/transcripts/tx_123":
			_, _ = w.Write([]byte(`{"language_code":"en","text":"Stored","words":[]}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/speech-to-text/transcripts/tx_123":
			_, _ = w.Write([]byte(`{"deleted":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			_, _ = w.Write([]byte(`{"models":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	client.HTTPClient = server.Client()

	if _, _, err := client.GetTranscript(context.Background(), "tx_123"); err != nil {
		t.Fatalf("GetTranscript() error = %v", err)
	}
	if _, err := client.DeleteTranscript(context.Background(), "tx_123"); err != nil {
		t.Fatalf("DeleteTranscript() error = %v", err)
	}
	if _, err := client.RawGet(context.Background(), "/v1/models?limit=1"); err != nil {
		t.Fatalf("RawGet() error = %v", err)
	}
	want := []string{
		"GET /v1/speech-to-text/transcripts/tx_123",
		"DELETE /v1/speech-to-text/transcripts/tx_123",
		"GET /v1/models?limit=1",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Fatalf("requests = %#v, want %#v", requests, want)
	}
}

func TestGetUserParsesStableIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/user" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("xi-api-key") != "test-key" {
			t.Fatalf("missing API key")
		}
		_, _ = w.Write([]byte(`{"user_id":"user_123","seat_type":"workspace_admin","created_at":1689761411,"xi_api_key":"secret"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	client.HTTPClient = server.Client()

	user, raw, err := client.GetUser(context.Background())
	if err != nil {
		t.Fatalf("GetUser() error = %v", err)
	}
	if user.UserID != "user_123" || user.SeatType != "workspace_admin" || user.CreatedAt != 1689761411 {
		t.Fatalf("user = %+v", user)
	}
	if !json.Valid(raw) {
		t.Fatalf("raw response is not JSON: %q", string(raw))
	}
}

func TestSubmitTranscriptionWebhookSendsMetadata(t *testing.T) {
	audio := t.TempDir() + "/episode.mp3"
	if err := osWriteFile(audio, []byte("fake audio")); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var fields map[string][]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/speech-to-text" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader() error = %v", err)
		}
		fields = map[string][]string{}
		var sawFile bool
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("NextPart() error = %v", err)
			}
			b, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("ReadAll(part) error = %v", err)
			}
			if part.FormName() == "file" {
				sawFile = true
				continue
			}
			fields[part.FormName()] = append(fields[part.FormName()], string(b))
		}
		if !sawFile {
			t.Fatal("multipart request did not include file")
		}
		_, _ = w.Write([]byte(`{"message":"Request accepted. Transcription result will be sent to the webhook.","request_id":"req_123","transcription_id":"tx_123"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	client.HTTPClient = server.Client()

	resp, _, err := client.SubmitTranscriptionWebhook(context.Background(), TranscribeOptions{
		FilePath:              audio,
		Model:                 "scribe_v2",
		TagAudioEvents:        true,
		TimestampsGranularity: "word",
		WebhookID:             "wh_123",
		WebhookMetadata: map[string]any{
			"podscribe_job_key": "job_123",
		},
	})
	if err != nil {
		t.Fatalf("SubmitTranscriptionWebhook() error = %v", err)
	}
	if resp.RequestID != "req_123" || resp.TranscriptionID == nil || *resp.TranscriptionID != "tx_123" {
		t.Fatalf("webhook response = %+v", resp)
	}
	assertField(t, fields, "webhook", "true")
	assertField(t, fields, "webhook_id", "wh_123")
	var metadata map[string]string
	if err := json.Unmarshal([]byte(fields["webhook_metadata"][0]), &metadata); err != nil {
		t.Fatalf("webhook_metadata is not JSON: %v", err)
	}
	if metadata["podscribe_job_key"] != "job_123" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestRawGetRejectsAbsoluteURL(t *testing.T) {
	client := NewClient("https://api.example", "test-key")
	if _, err := client.RawGet(context.Background(), "https://evil.example/v1/models"); err == nil {
		t.Fatal("RawGet() error = nil, want invalid input")
	}
}

func TestRawGetRetriesConnectionErrors(t *testing.T) {
	var attempts int
	client := NewClient("https://api.example", "test-key")
	client.retryBaseDelay = 0
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			return nil, retryableDialError()
		}
		if req.Method != http.MethodGet || req.URL.Path != "/v1/models" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		return jsonResponse(req, http.StatusOK, `{"models":[]}`), nil
	})}

	raw, err := client.RawGet(context.Background(), "/v1/models")
	if err != nil {
		t.Fatalf("RawGet() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if !json.Valid(raw) {
		t.Fatalf("raw response is not JSON: %q", string(raw))
	}
}

func TestRawGetDoesNotRetryAPIErrors(t *testing.T) {
	var attempts int
	client := NewClient("https://api.example", "test-key")
	client.retryBaseDelay = 0
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		return jsonResponse(req, http.StatusInternalServerError, `{"detail":"temporary upstream failure"}`), nil
	})}

	_, err := client.RawGet(context.Background(), "/v1/models")
	if err == nil {
		t.Fatal("RawGet() error = nil, want API error")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if got := apperr.Code(err); got != apperr.CodeAPI {
		t.Fatalf("error code = %q, want %q", got, apperr.CodeAPI)
	}
}

func TestRawGetRetriesRateLimit(t *testing.T) {
	var attempts int
	client := NewClient("https://api.example", "test-key")
	client.retryBaseDelay = 0
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			res := jsonResponse(req, http.StatusTooManyRequests, `{"detail":{"message":"Too many requests"}}`)
			res.Header.Set("retry-after", "0")
			return res, nil
		}
		return jsonResponse(req, http.StatusOK, `{"models":[]}`), nil
	})}

	raw, err := client.RawGet(context.Background(), "/v1/models")
	if err != nil {
		t.Fatalf("RawGet() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if !json.Valid(raw) {
		t.Fatalf("raw response is not JSON: %q", string(raw))
	}
}

func TestTranscribeFileRetriesConnectionErrorsWithFreshMultipartRequest(t *testing.T) {
	audio := t.TempDir() + "/episode.mp3"
	if err := osWriteFile(audio, []byte("fake audio")); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var attempts int
	client := NewClient("https://api.example", "test-key")
	client.retryBaseDelay = 0
	client.HTTPClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts++
		if attempts == 1 {
			_ = req.Body.Close()
			return nil, retryableDialError()
		}
		if req.Method != http.MethodPost || req.URL.Path != "/v1/speech-to-text" {
			t.Fatalf("request = %s %s", req.Method, req.URL.Path)
		}
		reader, err := req.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader() error = %v", err)
		}
		var fileContent string
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("NextPart() error = %v", err)
			}
			b, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("ReadAll(part) error = %v", err)
			}
			if part.FormName() == "file" {
				fileContent = string(b)
			}
		}
		if fileContent != "fake audio" {
			t.Fatalf("file content = %q", fileContent)
		}
		return jsonResponse(req, http.StatusOK, `{"language_code":"en","text":"Hello","words":[],"transcription_id":"tx_123"}`), nil
	})}

	resp, raw, err := client.TranscribeFile(context.Background(), TranscribeOptions{
		FilePath:              audio,
		Model:                 "scribe_v2",
		TagAudioEvents:        true,
		TimestampsGranularity: "word",
	})
	if err != nil {
		t.Fatalf("TranscribeFile() error = %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if resp.TranscriptionID != "tx_123" {
		t.Fatalf("transcription ID = %q", resp.TranscriptionID)
	}
	if !json.Valid(raw) {
		t.Fatalf("raw response is not JSON: %q", string(raw))
	}
}

func TestAPIErrorIncludesStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"detail":"bad request"}`, http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	client.HTTPClient = server.Client()

	_, err := client.RawGet(context.Background(), "/v1/models")
	if err == nil {
		t.Fatal("RawGet() error = nil, want API error")
	}
	if !strings.Contains(err.Error(), "400 Bad Request") {
		t.Fatalf("error = %q, want status", err.Error())
	}
}

func TestAPIErrorParsesProviderFailures(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     string
		headers  map[string]string
		wantCode string
		wantText []string
	}{
		{
			name:   "quota exceeded overrides unauthorized status",
			status: http.StatusUnauthorized,
			body:   `{"detail":{"type":"invalid_request","code":"quota_exceeded","message":"This request exceeds your quota of 10000. You have 315 credits remaining, while 4366 credits are required for this request.","status":"quota_exceeded","request_id":"req_quota"}}`,
			headers: map[string]string{
				"x-trace-id": "trace_123",
			},
			wantCode: apperr.CodeQuota,
			wantText: []string{
				"ElevenLabs quota exceeded",
				"315 credits remaining",
				"request_id: req_quota",
				"trace_id: trace_123",
			},
		},
		{
			name:     "auth failure",
			status:   http.StatusUnauthorized,
			body:     `{"detail":{"message":"Invalid API key"}}`,
			wantCode: apperr.CodeAuth,
			wantText: []string{"ElevenLabs authentication failed", "Invalid API key"},
		},
		{
			name:     "forbidden",
			status:   http.StatusForbidden,
			body:     `{"detail":"IP address is not allowed"}`,
			wantCode: apperr.CodeForbidden,
			wantText: []string{"ElevenLabs request forbidden", "IP address is not allowed"},
		},
		{
			name:     "not found",
			status:   http.StatusNotFound,
			body:     `{"detail":{"message":"Transcript not found"}}`,
			wantCode: apperr.CodeNotFound,
			wantText: []string{"ElevenLabs resource not found", "Transcript not found"},
		},
		{
			name:   "validation array",
			status: http.StatusUnprocessableEntity,
			body: `{"detail":[
				{"loc":["body","file"],"msg":"Field required","type":"missing"},
				{"loc":["body","model_id"],"msg":"Input should be 'scribe_v2' or 'scribe_v1'","type":"enum"}
			]}`,
			wantCode: apperr.CodeInvalidInput,
			wantText: []string{
				"ElevenLabs request validation failed",
				"body.file: Field required",
				"body.model_id: Input should be",
			},
		},
		{
			name:   "rate limited",
			status: http.StatusTooManyRequests,
			body:   `{"detail":{"message":"Too many requests"}}`,
			headers: map[string]string{
				"retry-after": "30",
			},
			wantCode: apperr.CodeRateLimited,
			wantText: []string{
				"ElevenLabs rate limit exceeded",
				"Too many requests",
				"retry_after: 30",
			},
		},
		{
			name:     "invalid json fallback",
			status:   http.StatusInternalServerError,
			body:     "temporary upstream failure",
			wantCode: apperr.CodeAPI,
			wantText: []string{"ElevenLabs API returned 500 Internal Server Error", "temporary upstream failure"},
		},
		{
			name:     "oversized body fallback is compacted",
			status:   http.StatusBadGateway,
			body:     strings.Repeat("x", 5000) + "tail-marker",
			wantCode: apperr.CodeAPI,
			wantText: []string{"ElevenLabs API returned 502 Bad Gateway", strings.Repeat("x", 100)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for name, value := range tt.headers {
					w.Header().Set(name, value)
				}
				http.Error(w, tt.body, tt.status)
			}))
			defer server.Close()

			client := NewClient(server.URL, "test-key")
			client.HTTPClient = server.Client()
			client.retryAttempts = 1

			_, err := client.RawGet(context.Background(), "/v1/models")
			if err == nil {
				t.Fatal("RawGet() error = nil, want API error")
			}
			if got := apperr.Code(err); got != tt.wantCode {
				t.Fatalf("error code = %q, want %q; error = %v", got, tt.wantCode, err)
			}
			for _, want := range tt.wantText {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %q, want substring %q", err.Error(), want)
				}
			}
			if strings.Contains(err.Error(), "tail-marker") {
				t.Fatalf("error = %q, want compacted body without tail marker", err.Error())
			}
		})
	}
}

func assertField(t *testing.T, fields map[string][]string, name, want string) {
	t.Helper()
	got := fields[name]
	if len(got) != 1 || got[0] != want {
		t.Fatalf("%s = %#v, want %q", name, got, want)
	}
}

func osWriteFile(name string, data []byte) error {
	return os.WriteFile(name, data, 0o644)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func retryableDialError() error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
}

func jsonResponse(req *http.Request, statusCode int, body string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Status:     fmt.Sprintf("%d %s", statusCode, http.StatusText(statusCode)),
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
}

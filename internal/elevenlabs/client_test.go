package elevenlabs

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
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

func TestRawGetRejectsAbsoluteURL(t *testing.T) {
	client := NewClient("https://api.example", "test-key")
	if _, err := client.RawGet(context.Background(), "https://evil.example/v1/models"); err == nil {
		t.Fatal("RawGet() error = nil, want invalid input")
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

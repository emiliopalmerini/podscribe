package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorJSONWithoutAuth(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{"--json", "doctor"}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr=%s", err, stderr.String())
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			AuthAvailable bool   `json:"auth_available"`
			AuthSource    string `json:"auth_source"`
			RemoteCheck   string `json:"remote_check"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if !env.OK || env.Data.AuthAvailable || env.Data.AuthSource != "missing" || env.Data.RemoteCheck != "skipped_missing_auth" {
		t.Fatalf("doctor JSON = %+v", env)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestDefaultTranscriptPath(t *testing.T) {
	got := defaultTranscriptPath("/tmp/audio/episode.final.mp3")
	want := "/tmp/audio/episode.final.transcript.md"
	if got != want {
		t.Fatalf("defaultTranscriptPath() = %q, want %q", got, want)
	}
}

func TestCollectKeyterms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "terms.txt")
	if err := os.WriteFile(path, []byte("# names\nEmilio Palmerini\n\nPodscribe\n"), 0o644); err != nil {
		t.Fatalf("write terms: %v", err)
	}
	got, err := collectKeyterms([]string{" ElevenLabs "}, path)
	if err != nil {
		t.Fatalf("collectKeyterms() error = %v", err)
	}
	want := []string{"ElevenLabs", "Emilio Palmerini", "Podscribe"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("collectKeyterms() = %#v, want %#v", got, want)
	}
}

func TestValidateKeytermsRejectsUnsupportedCharacters(t *testing.T) {
	if err := validateKeyterms([]string{"bad <term>"}); err == nil {
		t.Fatal("validateKeyterms() error = nil, want unsupported character error")
	}
}

func TestEnsureWritableTargetRequiresForce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "episode.transcript.md")
	if err := os.WriteFile(path, []byte("edited"), 0o644); err != nil {
		t.Fatalf("write existing: %v", err)
	}
	if err := ensureWritableTarget(path, false); err == nil {
		t.Fatal("ensureWritableTarget() error = nil, want overwrite protection")
	}
	if err := ensureWritableTarget(path, true); err != nil {
		t.Fatalf("ensureWritableTarget(force) error = %v", err)
	}
}

func TestTranscribeReportsUploadProgress(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	audio := filepath.Join(t.TempDir(), "episode.mp3")
	if err := os.WriteFile(audio, []byte(strings.Repeat("audio", 4096)), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	server := newTranscribeTestServer(t)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	stderrText := stderr.String()
	if !strings.Contains(stderrText, "Uploading "+audio) {
		t.Fatalf("stderr = %q, want upload start", stderrText)
	}
	if !strings.Contains(stderrText, "Uploaded ") {
		t.Fatalf("stderr = %q, want upload progress", stderrText)
	}
	if !strings.Contains(stderrText, "Upload complete; waiting for ElevenLabs to transcribe") {
		t.Fatalf("stderr = %q, want upload completion", stderrText)
	}
	if !strings.Contains(stdout.String(), "Wrote "+defaultTranscriptPath(audio)) {
		t.Fatalf("stdout = %q, want output path", stdout.String())
	}
}

func TestTranscribeJSONSuppressesUploadProgress(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	audio := filepath.Join(t.TempDir(), "episode.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	server := newTranscribeTestServer(t)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--json",
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			OutputPath string `json:"output_path"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if !env.OK || env.Data.OutputPath != defaultTranscriptPath(audio) {
		t.Fatalf("transcribe JSON = %+v", env)
	}
}

func TestTranscribeTimestampFlagControlsMarkdownTimestamps(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	dir := t.TempDir()
	audio := filepath.Join(dir, "episode.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	server := newTranscribeTestServer(t)
	defer server.Close()

	noTimestamps := filepath.Join(dir, "no-timestamps.md")
	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
		"--out", noTimestamps,
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() without --timestamps error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	md, err := os.ReadFile(noTimestamps)
	if err != nil {
		t.Fatalf("read transcript without timestamps: %v", err)
	}
	if strings.Contains(string(md), "[00:00:01]") {
		t.Fatalf("transcript included timestamps by default:\n%s", string(md))
	}
	if !strings.Contains(string(md), "Hello world.") {
		t.Fatalf("transcript missing text:\n%s", string(md))
	}

	withTimestamps := filepath.Join(dir, "with-timestamps.md")
	stdout.Reset()
	stderr.Reset()
	err = Execute(context.Background(), []string{
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
		"--out", withTimestamps,
		"--timestamps",
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() with --timestamps error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	md, err = os.ReadFile(withTimestamps)
	if err != nil {
		t.Fatalf("read transcript with timestamps: %v", err)
	}
	if !strings.Contains(string(md), "[00:00:01] Hello world.") {
		t.Fatalf("transcript missing timestamped text:\n%s", string(md))
	}
}

func newTranscribeTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/speech-to-text" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader() error = %v", err)
		}
		var sawFile bool
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("NextPart() error = %v", err)
			}
			if part.FormName() == "file" {
				sawFile = true
			}
			if _, err := io.Copy(io.Discard, part); err != nil {
				t.Fatalf("read multipart part: %v", err)
			}
		}
		if !sawFile {
			t.Fatal("multipart request did not include file")
		}
		_, _ = w.Write([]byte(`{"language_code":"en","text":"Hello world.","words":[{"text":"Hello","start":1.2,"end":1.4,"type":"word"},{"text":"world.","start":1.5,"end":1.8,"type":"word"}],"transcription_id":"tx_123"}`))
	}))
}

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/emiliopalmerini/podscribe/internal/jobstore"
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

func TestVersionFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--version"},
		{"-v"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := Execute(context.Background(), args, strings.NewReader(""), &stdout, &stderr, "test")
			if err != nil {
				t.Fatalf("Execute() error = %v\nstderr=%s", err, stderr.String())
			}
			if got, want := stdout.String(), "podscribe test\n"; got != want {
				t.Fatalf("stdout = %q, want %q", got, want)
			}
			if stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
		})
	}
}

func TestVersionFlagHonorsJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{"--json", "--version"}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr=%s", err, stderr.String())
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if !env.OK || env.Data.Version != "test" {
		t.Fatalf("version JSON = %+v", env)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestHelpIncludesVersionFlags(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{"--help"}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "-v, --version") {
		t.Fatalf("stdout = %q, want version flags", stdout.String())
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

func TestTranscribeQuotaErrorIsActionable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	dir := t.TempDir()
	audio := filepath.Join(dir, "episode.mp3")
	if err := os.WriteFile(audio, []byte(strings.Repeat("audio", 4096)), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	outPath := defaultTranscriptPath(audio)
	server := newTranscribeQuotaTestServer(t)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err == nil {
		t.Fatal("Execute() error = nil, want quota error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	stderrText := stderr.String()
	for _, want := range []string{
		"Upload complete; waiting for ElevenLabs to transcribe",
		"Error: ElevenLabs quota exceeded",
		"315 credits remaining",
		"request_id: req_quota",
	} {
		if !strings.Contains(stderrText, want) {
			t.Fatalf("stderr = %q, want substring %q", stderrText, want)
		}
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Fatalf("transcript output exists or stat failed unexpectedly: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	err = Execute(context.Background(), []string{
		"--json",
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err == nil {
		t.Fatal("Execute(--json) error = nil, want quota error")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	var env struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if env.OK || env.Error.Code != "quota_error" {
		t.Fatalf("quota JSON = %+v", env)
	}
	if !strings.Contains(env.Error.Message, "ElevenLabs quota exceeded") || !strings.Contains(env.Error.Message, "315 credits remaining") {
		t.Fatalf("quota JSON message = %q", env.Error.Message)
	}
	if _, err := os.Stat(outPath); !os.IsNotExist(err) {
		t.Fatalf("transcript output exists or stat failed unexpectedly: %v", err)
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

func TestTranscribeSpeakerNamesImplyDiarizationAndRenderNames(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	dir := t.TempDir()
	audio := filepath.Join(dir, "episode.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	outPath := filepath.Join(dir, "named.md")
	var fields map[string][]string
	server := newTranscribeFieldsTestServer(t, &fields, `{"language_code":"en","text":"Hello. Thanks!","words":[{"text":"Hello.","start":1.2,"end":1.4,"type":"word","speaker_id":"speaker_0"},{"text":"Thanks!","start":2.5,"end":2.8,"type":"word","speaker_id":"speaker_1"}],"transcription_id":"tx_123"}`)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
		"--out", outPath,
		"--speaker-name", "Emilio Palmerini",
		"--speaker-name", "Guest",
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	md, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	for _, want := range []string{
		"Emilio Palmerini: Hello.",
		"Guest: Thanks!",
	} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("transcript missing %q:\n%s", want, string(md))
		}
	}
	if got := strings.Join(fields["diarize"], "|"); got != "true" {
		t.Fatalf("diarize fields = %q, want true", got)
	}
	if got := strings.Join(fields["num_speakers"], "|"); got != "2" {
		t.Fatalf("num_speakers fields = %q, want 2", got)
	}
}

func TestTranscribeSpeakerNamesFileAndFlagsAppendInOrder(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	dir := t.TempDir()
	audio := filepath.Join(dir, "episode.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	namesPath := filepath.Join(dir, "speakers.txt")
	if err := os.WriteFile(namesPath, []byte("# regular speakers\nEmilio\n\nGuest\n"), 0o644); err != nil {
		t.Fatalf("write speaker names: %v", err)
	}
	outPath := filepath.Join(dir, "named.md")
	var fields map[string][]string
	server := newTranscribeFieldsTestServer(t, &fields, `{"language_code":"en","text":"One. Two. Three.","words":[{"text":"One.","start":1.2,"end":1.4,"type":"word","speaker_id":"speaker_0"},{"text":"Two.","start":2.5,"end":2.8,"type":"word","speaker_id":"speaker_1"},{"text":"Three.","start":3.5,"end":3.8,"type":"word","speaker_id":"speaker_2"}],"transcription_id":"tx_123"}`)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
		"--out", outPath,
		"--speaker-names-file", namesPath,
		"--speaker-name", "Producer",
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	md, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	for _, want := range []string{
		"Emilio: One.",
		"Guest: Two.",
		"Producer: Three.",
	} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("transcript missing %q:\n%s", want, string(md))
		}
	}
	if got := strings.Join(fields["num_speakers"], "|"); got != "3" {
		t.Fatalf("num_speakers fields = %q, want 3", got)
	}
}

func TestTranscribeTracksMergesAndUploadsMultichannelAudio(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "ffmpeg-args.txt")
	installFakeMergeTools(t, dir, argsPath)
	emilio := filepath.Join(dir, "emilio.wav")
	guest := filepath.Join(dir, "guest.wav")
	for _, path := range []string{emilio, guest} {
		if err := os.WriteFile(path, []byte("track audio"), 0o644); err != nil {
			t.Fatalf("write track: %v", err)
		}
	}

	var fields map[string][]string
	var uploaded string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/user" {
			_, _ = w.Write([]byte(`{"user_id":"user_test","seat_type":"workspace_admin","created_at":1}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/speech-to-text" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader() error = %v", err)
		}
		fields = map[string][]string{}
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
				t.Fatalf("read multipart part: %v", err)
			}
			if part.FormName() == "file" {
				uploaded = string(b)
				continue
			}
			fields[part.FormName()] = append(fields[part.FormName()], string(b))
		}
		_, _ = w.Write([]byte(`{"language_code":"en","text":"Hello. Thanks!","words":[{"text":"Hello.","start":1.0,"end":1.2,"type":"word","channel_index":0},{"text":"Thanks!","start":1.5,"end":1.8,"type":"word","channel_index":1}],"transcription_id":"tx_123"}`))
	}))
	defer server.Close()

	outPath := filepath.Join(dir, "episode.md")
	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe",
		"--track", "Emilio=" + emilio,
		"--track", "Guest=" + guest,
		"--track-offset", "Guest=1.5s",
		"--out", outPath,
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	if uploaded != "merged audio" {
		t.Fatalf("uploaded file content = %q, want merged audio", uploaded)
	}
	if got := strings.Join(fields["use_multi_channel"], "|"); got != "true" {
		t.Fatalf("use_multi_channel fields = %q, want true", got)
	}
	if got := strings.Join(fields["multichannel_output_style"], "|"); got != "combined" {
		t.Fatalf("multichannel_output_style fields = %q, want combined", got)
	}
	if got := strings.Join(fields["diarize"], "|"); got != "" {
		t.Fatalf("diarize fields = %q, want unset for multichannel tracks", got)
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read ffmpeg args: %v", err)
	}
	if !strings.Contains(string(argsBytes), "adelay=1500:all=1") {
		t.Fatalf("ffmpeg args missing offset delay:\n%s", string(argsBytes))
	}

	md, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	for _, want := range []string{
		"Emilio: Hello.",
		"Guest: Thanks!",
	} {
		if !strings.Contains(string(md), want) {
			t.Fatalf("transcript missing %q:\n%s", want, string(md))
		}
	}
	for _, want := range []string{
		"Channel 0: Emilio",
		"Channel 1: Guest",
		"offset 1.5s",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr missing %q:\n%s", want, stderr.String())
		}
	}

	entries, err := jobstore.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("job cache entries = %d, want 1", len(entries))
	}
	record := entries[0].Record
	if record.SourcePath != "" {
		t.Fatalf("multichannel SourcePath = %q, want empty because merged temp audio is not durable", record.SourcePath)
	}
	if len(record.InputTracks) != 2 || record.InputTracks[1].Name != "Guest" || record.InputTracks[1].OffsetNanos != int64(1500*time.Millisecond) {
		t.Fatalf("input tracks = %+v, want stored track metadata", record.InputTracks)
	}
}

func TestCollectTracksParsesOffsets(t *testing.T) {
	dir := t.TempDir()
	host := filepath.Join(dir, "host.wav")
	guest := filepath.Join(dir, "guest.wav")
	for _, path := range []string{host, guest} {
		if err := os.WriteFile(path, []byte("track audio"), 0o644); err != nil {
			t.Fatalf("write track: %v", err)
		}
	}

	tracks, err := collectTracks(
		[]string{"Host=" + host, "Guest=" + guest},
		[]string{"Guest=1.25", "Host=-500ms"},
	)
	if err != nil {
		t.Fatalf("collectTracks() error = %v", err)
	}
	if tracks[0].Offset.String() != "-500ms" || tracks[1].Offset.String() != "1.25s" {
		t.Fatalf("offsets = %s, %s; want -500ms and 1.25s", tracks[0].Offset, tracks[1].Offset)
	}

	if _, err := collectTracks([]string{"Host=" + host}, []string{"Guest=1s"}); err == nil {
		t.Fatal("collectTracks() error = nil, want unknown track offset error")
	}
}

func TestTranscribeReusesCompletedJobCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	dir := t.TempDir()
	audio := filepath.Join(dir, "episode.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	server, postCount := newCacheTestServer(t)
	defer server.Close()

	firstOut := filepath.Join(dir, "first.md")
	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--json",
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
		"--out", firstOut,
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("first Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	secondOut := filepath.Join(dir, "second.md")
	stdout.Reset()
	stderr.Reset()
	err = Execute(context.Background(), []string{
		"--json",
		"--api-key", "rotated-test-key",
		"--base-url", server.URL,
		"transcribe", audio,
		"--out", secondOut,
		"--timestamps",
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("second Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if *postCount != 1 {
		t.Fatalf("transcribe POST count = %d, want 1", *postCount)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			CacheStatus string `json:"cache_status"`
			ReusedCache bool   `json:"reused_cache"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if !env.OK || env.Data.CacheStatus != "hit" || !env.Data.ReusedCache {
		t.Fatalf("cache JSON = %+v", env)
	}
	md, err := os.ReadFile(secondOut)
	if err != nil {
		t.Fatalf("read cached transcript: %v", err)
	}
	if !strings.Contains(string(md), "[00:00:01] Hello world.") {
		t.Fatalf("cached transcript was not re-rendered with current timestamp flag:\n%s", string(md))
	}
}

func TestTranscribeSubmittedJobBlocksAutomaticRetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	dir := t.TempDir()
	audio := filepath.Join(dir, "episode.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	server, postCount := newCacheTestServer(t)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
		"--webhook",
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("webhook Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = Execute(context.Background(), []string{
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err == nil {
		t.Fatal("Execute() error = nil, want submitted job retry block")
	}
	if *postCount != 1 {
		t.Fatalf("transcribe POST count = %d, want 1", *postCount)
	}
	if !strings.Contains(stderr.String(), "is submitted") || !strings.Contains(stderr.String(), "--force") {
		t.Fatalf("stderr = %q, want submitted retry guidance", stderr.String())
	}
}

func TestImportWebhookCompletesJobCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	dir := t.TempDir()
	audio := filepath.Join(dir, "episode.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	server, postCount := newCacheTestServer(t)
	defer server.Close()

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--json",
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
		"--webhook",
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("webhook Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	var submitted struct {
		Data struct {
			JobKey string `json:"job_key"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &submitted); err != nil {
		t.Fatalf("webhook stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if submitted.Data.JobKey == "" {
		t.Fatalf("missing job key in webhook output: %s", stdout.String())
	}

	importOut := filepath.Join(dir, "imported.md")
	importRaw := filepath.Join(dir, "imported.json")
	payload := `{
		"webhook_metadata": {"podscribe_job_key": "` + submitted.Data.JobKey + `"},
		"data": {
			"language_code": "en",
			"text": "Hello world.",
			"words": [
				{"text":"Hello","start":1.2,"end":1.4,"type":"word"},
				{"text":"world.","start":1.5,"end":1.8,"type":"word"}
			],
			"transcription_id": "tx_webhook"
		}
	}`
	stdout.Reset()
	stderr.Reset()
	err = Execute(context.Background(), []string{
		"--json",
		"transcripts", "import-webhook", "-",
		"--out", importOut,
		"--raw-out", importRaw,
	}, strings.NewReader(payload), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("import webhook Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	md, err := os.ReadFile(importOut)
	if err != nil {
		t.Fatalf("read imported markdown: %v", err)
	}
	if !strings.Contains(string(md), "Hello world.") {
		t.Fatalf("imported markdown missing transcript:\n%s", string(md))
	}
	if _, err := os.Stat(importRaw); err != nil {
		t.Fatalf("raw import was not written: %v", err)
	}

	rerunOut := filepath.Join(dir, "rerun.md")
	stdout.Reset()
	stderr.Reset()
	err = Execute(context.Background(), []string{
		"--json",
		"--api-key", "test-key",
		"--base-url", server.URL,
		"transcribe", audio,
		"--out", rerunOut,
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("rerun Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if *postCount != 1 {
		t.Fatalf("transcribe POST count = %d, want only webhook submit POST", *postCount)
	}
}

func TestTranscriptLocateFindsSelectedTextFromCache(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	audio := filepath.Join(dir, "episode.mp3")
	outPath := filepath.Join(dir, "episode.transcript.md")
	saveLocateRecord(t, jobstore.Record{
		JobKey:     "locate-json",
		Status:     jobstore.StatusCompleted,
		SourcePath: audio,
		OutputPath: outPath,
		RawResponse: json.RawMessage(`{
			"language_code":"en",
			"text":"Before Hello world after.",
			"words":[
				{"text":"Before","start":1.0,"end":1.1,"type":"word"},
				{"text":"Hello","start":1.2,"end":1.4,"type":"word"},
				{"text":"world.","start":1.5,"end":1.8,"type":"word"},
				{"text":"after.","start":2.0,"end":2.2,"type":"word"}
			],
			"transcription_id":"tx_locate"
		}`),
	})

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--json",
		"transcripts", "locate", outPath,
		"--text", "[00:00:01] Speaker 1: hello world",
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
			JobKey     string `json:"job_key"`
			SourcePath string `json:"source_path"`
			Matches    []struct {
				StartSeconds float64 `json:"start_seconds"`
				EndSeconds   float64 `json:"end_seconds"`
				Timestamp    string  `json:"timestamp"`
				Text         string  `json:"text"`
				Context      string  `json:"context"`
			} `json:"matches"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if !env.OK || env.Data.JobKey != "locate-json" || env.Data.SourcePath != audio {
		t.Fatalf("locate JSON = %+v", env)
	}
	if len(env.Data.Matches) != 1 {
		t.Fatalf("matches = %+v, want 1", env.Data.Matches)
	}
	match := env.Data.Matches[0]
	if match.Timestamp != "00:00:01.200" || match.StartSeconds != 1.2 || match.EndSeconds != 1.8 {
		t.Fatalf("match timing = %+v, want 1.2-1.8", match)
	}
	if match.Text != "Hello world." || !strings.Contains(match.Context, "Before Hello world. after.") {
		t.Fatalf("match text/context = %+v", match)
	}
}

func TestTranscriptLocateReadsSelectedTextFromStdin(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	audio := filepath.Join(dir, "episode.mp3")
	saveLocateRecord(t, jobstore.Record{
		JobKey:     "locate-stdin",
		Status:     jobstore.StatusCompleted,
		SourcePath: audio,
		RawResponse: json.RawMessage(`{
			"language_code":"en",
			"text":"Hello world.",
			"words":[
				{"text":"Hello","start":1.2,"end":1.4,"type":"word"},
				{"text":"world.","start":1.5,"end":1.8,"type":"word"}
			],
			"transcription_id":"tx_locate"
		}`),
	})

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"transcripts", "locate", audio,
		"--text", "-",
	}, strings.NewReader("Speaker 1: Hello world"), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	for _, want := range []string{
		"Timestamp: 00:00:01.200 (1.200s)",
		"Audio: " + audio,
		"Match: Hello world.",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout = %q, want %q", stdout.String(), want)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestTranscriptLocateRequiresJobKeyForAmbiguousCacheMatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	sharedOut := filepath.Join(dir, "episode.transcript.md")
	saveLocateRecord(t, jobstore.Record{
		JobKey:     "locate-a",
		Status:     jobstore.StatusCompleted,
		OutputPath: sharedOut,
		RawResponse: json.RawMessage(`{
			"language_code":"en",
			"text":"First phrase.",
			"words":[{"text":"First","start":1.0,"end":1.1,"type":"word"}],
			"transcription_id":"tx_a"
		}`),
	})
	saveLocateRecord(t, jobstore.Record{
		JobKey:     "locate-b",
		Status:     jobstore.StatusCompleted,
		OutputPath: sharedOut,
		RawResponse: json.RawMessage(`{
			"language_code":"en",
			"text":"Second phrase.",
			"words":[{"text":"Second","start":2.0,"end":2.1,"type":"word"}],
			"transcription_id":"tx_b"
		}`),
	})

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"transcripts", "locate", sharedOut,
		"--text", "Second",
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err == nil {
		t.Fatal("Execute() error = nil, want ambiguous cache error")
	}
	if !strings.Contains(stderr.String(), "matches multiple completed jobs (locate-a, locate-b)") {
		t.Fatalf("stderr = %q, want ambiguous cache guidance", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = Execute(context.Background(), []string{
		"transcripts", "locate", sharedOut,
		"--job-key", "locate-b",
		"--text", "Second",
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute(--job-key) error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "Timestamp: 00:00:02.000 (2.000s)") {
		t.Fatalf("stdout = %q, want locate-b timestamp", stdout.String())
	}
}

func TestTranscriptLocateWritesClipOut(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	installFakeFFmpegForCLITest(t, dir)
	audio := filepath.Join(dir, "episode.mp3")
	clipOut := filepath.Join(dir, "clip.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	saveLocateRecord(t, jobstore.Record{
		JobKey:     "locate-clip",
		Status:     jobstore.StatusCompleted,
		SourcePath: audio,
		RawResponse: json.RawMessage(`{
			"language_code":"en",
			"text":"Hello world.",
			"words":[
				{"text":"Hello","start":1.2,"end":1.4,"type":"word"},
				{"text":"world.","start":1.5,"end":1.8,"type":"word"}
			],
			"transcription_id":"tx_locate"
		}`),
	})

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"--json",
		"transcripts", "locate", audio,
		"--text", "Hello world",
		"--clip-out", clipOut,
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err != nil {
		t.Fatalf("Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	if b, err := os.ReadFile(clipOut); err != nil || string(b) != "clip" {
		t.Fatalf("clip content = %q, err=%v", string(b), err)
	}

	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Clip *struct {
				SourcePath      string  `json:"source_path"`
				OutputPath      string  `json:"output_path"`
				StartSeconds    float64 `json:"start_seconds"`
				EndSeconds      float64 `json:"end_seconds"`
				DurationSeconds float64 `json:"duration_seconds"`
				StreamCopy      bool    `json:"stream_copy"`
			} `json:"clip"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout.String())
	}
	if !env.OK || env.Data.Clip == nil {
		t.Fatalf("locate JSON = %+v, want clip metadata", env)
	}
	if env.Data.Clip.SourcePath != audio || env.Data.Clip.OutputPath != clipOut {
		t.Fatalf("clip paths = %+v, want source/output paths", env.Data.Clip)
	}
	if env.Data.Clip.StartSeconds != 1.2 || env.Data.Clip.EndSeconds != 1.8 || !env.Data.Clip.StreamCopy {
		t.Fatalf("clip metadata = %+v", env.Data.Clip)
	}
}

func TestTranscriptLocateClipOutRequiresSingleReturnedMatch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	audio := filepath.Join(dir, "episode.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	saveLocateRecord(t, jobstore.Record{
		JobKey:     "locate-many",
		Status:     jobstore.StatusCompleted,
		SourcePath: audio,
		RawResponse: json.RawMessage(`{
			"language_code":"en",
			"text":"again again.",
			"words":[
				{"text":"again","start":1.0,"end":1.1,"type":"word"},
				{"text":"again","start":2.0,"end":2.1,"type":"word"}
			],
			"transcription_id":"tx_locate"
		}`),
	})

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"transcripts", "locate", audio,
		"--text", "again",
		"--clip-out", filepath.Join(dir, "clip.mp3"),
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err == nil {
		t.Fatal("Execute() error = nil, want multiple-match clip error")
	}
	if !strings.Contains(stderr.String(), "--clip-out requires exactly one located match") {
		t.Fatalf("stderr = %q, want single-match guidance", stderr.String())
	}
}

func TestTranscriptLocateClipOutRequiresForceToOverwrite(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	installFakeFFmpegForCLITest(t, dir)
	audio := filepath.Join(dir, "episode.mp3")
	clipOut := filepath.Join(dir, "clip.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}
	if err := os.WriteFile(clipOut, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write existing clip: %v", err)
	}
	saveLocateRecord(t, jobstore.Record{
		JobKey:     "locate-overwrite",
		Status:     jobstore.StatusCompleted,
		SourcePath: audio,
		RawResponse: json.RawMessage(`{
			"language_code":"en",
			"text":"Hello.",
			"words":[{"text":"Hello","start":1.0,"end":1.1,"type":"word"}],
			"transcription_id":"tx_locate"
		}`),
	})

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"transcripts", "locate", audio,
		"--text", "Hello",
		"--clip-out", clipOut,
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err == nil {
		t.Fatal("Execute() error = nil, want overwrite error")
	}
	if !strings.Contains(stderr.String(), "use --force to overwrite") {
		t.Fatalf("stderr = %q, want overwrite guidance", stderr.String())
	}
}

func TestTranscriptLocateClipOutRequiresExistingSourceAudio(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	audio := filepath.Join(dir, "missing.mp3")
	saveLocateRecord(t, jobstore.Record{
		JobKey:     "locate-missing-audio",
		Status:     jobstore.StatusCompleted,
		SourcePath: audio,
		RawResponse: json.RawMessage(`{
			"language_code":"en",
			"text":"Hello.",
			"words":[{"text":"Hello","start":1.0,"end":1.1,"type":"word"}],
			"transcription_id":"tx_locate"
		}`),
	})

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"transcripts", "locate", audio,
		"--text", "Hello",
		"--clip-out", filepath.Join(dir, "clip.mp3"),
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err == nil {
		t.Fatal("Execute() error = nil, want missing source audio error")
	}
	if !strings.Contains(stderr.String(), "could not read audio source "+audio) {
		t.Fatalf("stderr = %q, want source audio error", stderr.String())
	}
}

func TestTranscribeRejectsMoreSpeakerNamesThanExplicitSpeakers(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ELEVENLABS_API_KEY", "")

	audio := filepath.Join(t.TempDir(), "episode.mp3")
	if err := os.WriteFile(audio, []byte("fake audio"), 0o644); err != nil {
		t.Fatalf("write audio: %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), []string{
		"transcribe", audio,
		"--speakers", "1",
		"--speaker-name", "Emilio",
		"--speaker-name", "Guest",
	}, strings.NewReader(""), &stdout, &stderr, "test")
	if err == nil {
		t.Fatal("Execute() error = nil, want speaker count validation error")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "--speakers must be at least the number of speaker names") {
		t.Fatalf("stderr = %q, want speaker count validation error", stderr.String())
	}
}

func saveLocateRecord(t *testing.T, record jobstore.Record) {
	t.Helper()
	if _, err := jobstore.Save(record); err != nil {
		t.Fatalf("Save(%s) error = %v", record.JobKey, err)
	}
}

func installFakeFFmpegForCLITest(t *testing.T, dir string) {
	t.Helper()
	path := filepath.Join(dir, "ffmpeg")
	script := `#!/bin/sh
last=
for arg do
  last="$arg"
done
printf clip > "$last"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	t.Setenv("PATH", dir)
}

func installFakeMergeTools(t *testing.T, dir, argsPath string) {
	t.Helper()
	ffprobe := filepath.Join(dir, "ffprobe")
	ffprobeScript := `#!/bin/sh
last=
for arg do
  last="$arg"
done
case "$last" in
  *emilio.wav) printf '10.0\n' ;;
  *guest.wav) printf '10.0\n' ;;
  *) printf '1.0\n' ;;
esac
`
	if err := os.WriteFile(ffprobe, []byte(ffprobeScript), 0o755); err != nil {
		t.Fatalf("write fake ffprobe: %v", err)
	}

	ffmpeg := filepath.Join(dir, "ffmpeg")
	ffmpegScript := fmt.Sprintf(`#!/bin/sh
: > %q
last=
for arg do
  printf '%%s\n' "$arg" >> %q
  last="$arg"
done
printf 'merged audio' > "$last"
`, argsPath, argsPath)
	if err := os.WriteFile(ffmpeg, []byte(ffmpegScript), 0o755); err != nil {
		t.Fatalf("write fake ffmpeg: %v", err)
	}
	t.Setenv("PATH", dir)
}

func newTranscribeTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/user" {
			_, _ = w.Write([]byte(`{"user_id":"user_test","seat_type":"workspace_admin","created_at":1,"xi_api_key":"should-not-be-used"}`))
			return
		}
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

func newTranscribeFieldsTestServer(t *testing.T, fields *map[string][]string, response string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/user" {
			_, _ = w.Write([]byte(`{"user_id":"user_test","seat_type":"workspace_admin","created_at":1,"xi_api_key":"should-not-be-used"}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/speech-to-text" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader() error = %v", err)
		}
		gotFields := make(map[string][]string)
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
				if _, err := io.Copy(io.Discard, part); err != nil {
					t.Fatalf("read multipart file part: %v", err)
				}
				continue
			}
			b, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("read multipart field: %v", err)
			}
			gotFields[part.FormName()] = append(gotFields[part.FormName()], string(b))
		}
		if !sawFile {
			t.Fatal("multipart request did not include file")
		}
		*fields = gotFields
		_, _ = w.Write([]byte(response))
	}))
}

func newCacheTestServer(t *testing.T) (*httptest.Server, *int) {
	t.Helper()
	postCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/user" {
			_, _ = w.Write([]byte(`{"user_id":"user_test","seat_type":"workspace_admin","created_at":1,"xi_api_key":"should-not-be-used"}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/speech-to-text" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		postCount++
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatalf("MultipartReader() error = %v", err)
		}
		fields := make(map[string][]string)
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
				if _, err := io.Copy(io.Discard, part); err != nil {
					t.Fatalf("read multipart file part: %v", err)
				}
				continue
			}
			b, err := io.ReadAll(part)
			if err != nil {
				t.Fatalf("read multipart field: %v", err)
			}
			fields[part.FormName()] = append(fields[part.FormName()], string(b))
		}
		if !sawFile {
			t.Fatal("multipart request did not include file")
		}
		if strings.Join(fields["webhook"], "|") == "true" {
			_, _ = w.Write([]byte(`{"message":"Request accepted. Transcription result will be sent to the webhook.","request_id":"req_webhook","transcription_id":"tx_webhook_ack"}`))
			return
		}
		_, _ = w.Write([]byte(`{"language_code":"en","text":"Hello world.","words":[{"text":"Hello","start":1.2,"end":1.4,"type":"word"},{"text":"world.","start":1.5,"end":1.8,"type":"word"}],"transcription_id":"tx_123"}`))
	}))
	return server, &postCount
}

func newTranscribeQuotaTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/user" {
			_, _ = w.Write([]byte(`{"user_id":"user_test","seat_type":"workspace_admin","created_at":1,"xi_api_key":"should-not-be-used"}`))
			return
		}
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
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":{"type":"invalid_request","code":"quota_exceeded","message":"This request exceeds your quota of 10000. You have 315 credits remaining, while 4366 credits are required for this request.","status":"quota_exceeded","request_id":"req_quota"}}`))
	}))
}

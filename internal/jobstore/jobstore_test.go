package jobstore

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJobKeyUsesUserNamespaceNotAPIKey(t *testing.T) {
	req := RemoteRequest{
		Model:                 "scribe_v2",
		Diarize:               true,
		Speakers:              2,
		Keyterms:              []string{"Podscribe", "Emilio"},
		TagAudioEvents:        true,
		TimestampsGranularity: "word",
	}
	requestHash, err := RequestHash(req)
	if err != nil {
		t.Fatalf("RequestHash() error = %v", err)
	}
	audioHash := "audio-sha"

	got1, err := JobKey("https://api.elevenlabs.io/", UserNamespace("user_123"), audioHash, requestHash)
	if err != nil {
		t.Fatalf("JobKey() error = %v", err)
	}
	got2, err := JobKey("https://api.elevenlabs.io", UserNamespace("user_123"), audioHash, requestHash)
	if err != nil {
		t.Fatalf("JobKey() error = %v", err)
	}
	if got1 != got2 {
		t.Fatalf("job key changed for equivalent base URL: %q != %q", got1, got2)
	}

	otherUser, err := JobKey("https://api.elevenlabs.io", UserNamespace("user_456"), audioHash, requestHash)
	if err != nil {
		t.Fatalf("JobKey(other user) error = %v", err)
	}
	if otherUser == got1 {
		t.Fatal("job key did not change for different user namespace")
	}
}

func TestRequestHashNormalizesKeytermOrder(t *testing.T) {
	base := RemoteRequest{
		Model:                 "scribe_v2",
		Diarize:               true,
		Speakers:              2,
		Keyterms:              []string{"Emilio", "Podscribe"},
		TagAudioEvents:        true,
		TimestampsGranularity: "word",
	}
	reordered := base
	reordered.Keyterms = []string{" Podscribe ", "Emilio"}

	got, err := RequestHash(base)
	if err != nil {
		t.Fatalf("RequestHash() error = %v", err)
	}
	want, err := RequestHash(reordered)
	if err != nil {
		t.Fatalf("RequestHash(reordered) error = %v", err)
	}
	if got != want {
		t.Fatalf("request hash changed for reordered keyterms: %q != %q", got, want)
	}

	changed := base
	changed.Speakers = 3
	changedHash, err := RequestHash(changed)
	if err != nil {
		t.Fatalf("RequestHash(changed) error = %v", err)
	}
	if changedHash == got {
		t.Fatal("request hash did not change for remote parameter change")
	}
}

func TestSaveWritesPrivateCacheFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	path, err := Save(Record{
		JobKey:           "abc123",
		Status:           StatusPending,
		AccountNamespace: UserNamespace("user_123"),
		BaseURL:          "https://api.elevenlabs.io",
		AudioSHA256:      "audio",
		RequestHash:      "request",
		RemoteRequest: RemoteRequest{
			Model:                 "scribe_v2",
			TagAudioEvents:        true,
			TimestampsGranularity: "word",
		},
	})
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if path != filepath.Join(os.Getenv("HOME"), ".podscribe", "jobs", "v1", "abc123.json") {
		t.Fatalf("path = %q", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat cache file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("cache file permissions = %o, want 0600", got)
	}
	dirInfo, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatalf("stat cache dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Fatalf("cache dir permissions = %o, want 0700", got)
	}
}

func TestFindCompletedByPathMatchesStoredPaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	audio := filepath.Join(dir, "episode.mp3")
	out := filepath.Join(dir, "episode.transcript.md")
	raw := filepath.Join(dir, "episode.json")

	if _, err := Save(Record{
		JobKey:        "completed",
		Status:        StatusCompleted,
		SourcePath:    audio,
		OutputPath:    out,
		RawOutputPath: raw,
	}); err != nil {
		t.Fatalf("Save(completed) error = %v", err)
	}
	if _, err := Save(Record{
		JobKey:     "pending",
		Status:     StatusPending,
		SourcePath: audio,
	}); err != nil {
		t.Fatalf("Save(pending) error = %v", err)
	}

	for _, path := range []string{audio, out, raw} {
		matches, err := FindCompletedByPath(path)
		if err != nil {
			t.Fatalf("FindCompletedByPath(%q) error = %v", path, err)
		}
		if len(matches) != 1 || matches[0].Record.JobKey != "completed" {
			t.Fatalf("FindCompletedByPath(%q) = %+v, want completed record", path, matches)
		}
	}
}

func TestFindCompletedByPathMatchesRelativeAndAbsolutePaths(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	dir := t.TempDir()
	previousDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("Chdir(%q) error = %v", dir, err)
	}
	defer func() {
		if err := os.Chdir(previousDir); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	if _, err := Save(Record{
		JobKey:     "relative",
		Status:     StatusCompleted,
		OutputPath: "episode.transcript.md",
	}); err != nil {
		t.Fatalf("Save(relative) error = %v", err)
	}

	matches, err := FindCompletedByPath(filepath.Join(dir, "episode.transcript.md"))
	if err != nil {
		t.Fatalf("FindCompletedByPath() error = %v", err)
	}
	if len(matches) != 1 || matches[0].Record.JobKey != "relative" {
		t.Fatalf("FindCompletedByPath() = %+v, want relative record", matches)
	}
}

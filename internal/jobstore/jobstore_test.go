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

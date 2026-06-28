package jobstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
)

const (
	SchemaVersion = 1

	StatusPending   = "pending"
	StatusSubmitted = "submitted"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

type RemoteRequest struct {
	Model                   string   `json:"model"`
	Language                string   `json:"language,omitempty"`
	Diarize                 bool     `json:"diarize"`
	Speakers                int      `json:"speakers,omitempty"`
	Keyterms                []string `json:"keyterms,omitempty"`
	Clean                   bool     `json:"clean"`
	TagAudioEvents          bool     `json:"tag_audio_events"`
	TimestampsGranularity   string   `json:"timestamps_granularity"`
	UseMultiChannel         bool     `json:"use_multi_channel,omitempty"`
	MultichannelOutputStyle string   `json:"multichannel_output_style,omitempty"`
}

type RenderRequest struct {
	Title        string   `json:"title,omitempty"`
	SourceFile   string   `json:"source_file,omitempty"`
	Model        string   `json:"model,omitempty"`
	Diarized     bool     `json:"diarized"`
	Timestamps   bool     `json:"timestamps"`
	SpeakerNames []string `json:"speaker_names,omitempty"`
}

type Record struct {
	SchemaVersion    int             `json:"schema_version"`
	JobKey           string          `json:"job_key"`
	Status           string          `json:"status"`
	AccountNamespace string          `json:"account_namespace"`
	BaseURL          string          `json:"base_url"`
	AudioSHA256      string          `json:"audio_sha256"`
	RequestHash      string          `json:"request_hash"`
	RemoteRequest    RemoteRequest   `json:"remote_request"`
	RenderRequest    RenderRequest   `json:"render_request"`
	SourcePath       string          `json:"source_path,omitempty"`
	OutputPath       string          `json:"output_path,omitempty"`
	RawOutputPath    string          `json:"raw_output_path,omitempty"`
	InputTracks      []InputTrack    `json:"input_tracks,omitempty"`
	TranscriptionID  string          `json:"transcription_id,omitempty"`
	WebhookRequestID string          `json:"webhook_request_id,omitempty"`
	RawResponse      json.RawMessage `json:"raw_response,omitempty"`
	LastErrorCode    string          `json:"last_error_code,omitempty"`
	LastErrorMessage string          `json:"last_error_message,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
	CompletedAt      *time.Time      `json:"completed_at,omitempty"`
}

type InputTrack struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	OffsetNanos int64  `json:"offset_nanos,omitempty"`
}

type Entry struct {
	Path   string
	Record Record
}

func Root() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", apperr.Wrap(apperr.CodeConfig, "could not determine home directory", err)
	}
	return filepath.Join(home, ".podscribe", "jobs", "v1"), nil
}

func Path(jobKey string) (string, error) {
	if strings.TrimSpace(jobKey) == "" {
		return "", apperr.New(apperr.CodeInvalidInput, "job key cannot be empty")
	}
	root, err := Root()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, jobKey+".json"), nil
}

func Load(jobKey string) (Record, bool, error) {
	path, err := Path(jobKey)
	if err != nil {
		return Record{}, false, err
	}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not read job cache at %s", path), err)
	}
	var record Record
	if err := json.Unmarshal(b, &record); err != nil {
		return Record{}, true, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not parse job cache at %s", path), err)
	}
	return record, true, nil
}

func List() ([]Entry, error) {
	root, err := Root()
	if err != nil {
		return nil, err
	}
	dirEntries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not read job cache directory %s", root), err)
	}

	entries := make([]Entry, 0, len(dirEntries))
	for _, dirEntry := range dirEntries {
		if dirEntry.IsDir() || !strings.HasSuffix(dirEntry.Name(), ".json") {
			continue
		}
		path := filepath.Join(root, dirEntry.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not read job cache at %s", path), err)
		}
		var record Record
		if err := json.Unmarshal(b, &record); err != nil {
			return nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not parse job cache at %s", path), err)
		}
		entries = append(entries, Entry{Path: path, Record: record})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Record.JobKey < entries[j].Record.JobKey
	})
	return entries, nil
}

func FindCompletedByPath(path string) ([]Entry, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, apperr.New(apperr.CodeInvalidInput, "path cannot be empty")
	}
	entries, err := List()
	if err != nil {
		return nil, err
	}
	matches := make([]Entry, 0)
	for _, entry := range entries {
		if entry.Record.Status != StatusCompleted {
			continue
		}
		if recordMatchesPath(entry.Record, path) {
			matches = append(matches, entry)
		}
	}
	return matches, nil
}

func recordMatchesPath(record Record, path string) bool {
	for _, candidate := range []string{record.SourcePath, record.OutputPath, record.RawOutputPath} {
		if samePath(candidate, path) {
			return true
		}
	}
	return false
}

func samePath(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	absA, errA := canonicalAbs(a)
	absB, errB := canonicalAbs(b)
	if errA == nil && errB == nil && absA == absB {
		return true
	}
	return false
}

func canonicalAbs(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	dir, file := filepath.Split(absPath)
	if realDir, err := filepath.EvalSymlinks(dir); err == nil {
		absPath = filepath.Join(realDir, file)
	}
	return filepath.Clean(absPath), nil
}

func Save(record Record) (string, error) {
	if strings.TrimSpace(record.JobKey) == "" {
		return "", apperr.New(apperr.CodeInvalidInput, "job key cannot be empty")
	}
	root, err := Root()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not create job cache directory %s", root), err)
	}
	record.SchemaVersion = SchemaVersion
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	if record.Status == StatusCompleted && record.CompletedAt == nil {
		completedAt := now
		record.CompletedAt = &completedAt
	}

	b, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", apperr.Wrap(apperr.CodeUnexpected, "could not encode job cache", err)
	}
	b = append(b, '\n')

	path := filepath.Join(root, record.JobKey+".json")
	tmp, err := os.CreateTemp(root, "."+record.JobKey+".*.tmp")
	if err != nil {
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not create temp job cache in %s", root), err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not set permissions on %s", tmpPath), err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not write job cache to %s", tmpPath), err)
	}
	if err := tmp.Close(); err != nil {
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not close job cache temp file %s", tmpPath), err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not save job cache to %s", path), err)
	}
	return path, nil
}

func FileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not open audio file %s for hashing", path), err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not hash audio file %s", path), err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func RequestHash(req RemoteRequest) (string, error) {
	req.Keyterms = normalizedStrings(req.Keyterms)
	return HashJSON(req)
}

func JobKey(baseURL, accountNamespace, audioSHA256, requestHash string) (string, error) {
	return HashJSON(struct {
		Version          string `json:"version"`
		BaseURL          string `json:"base_url"`
		AccountNamespace string `json:"account_namespace"`
		AudioSHA256      string `json:"audio_sha256"`
		RequestHash      string `json:"request_hash"`
	}{
		Version:          "podscribe:v1",
		BaseURL:          strings.TrimRight(baseURL, "/"),
		AccountNamespace: accountNamespace,
		AudioSHA256:      audioSHA256,
		RequestHash:      requestHash,
	})
}

func HashJSON(value any) (string, error) {
	b, err := json.Marshal(value)
	if err != nil {
		return "", apperr.Wrap(apperr.CodeUnexpected, "could not encode hash input", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func UserNamespace(userID string) string {
	return "elevenlabs_user:" + strings.TrimSpace(userID)
}

func APIKeyNamespace(apiKey string) string {
	sum := sha256.Sum256([]byte(apiKey))
	return "api_key_sha256:" + hex.EncodeToString(sum[:])
}

func normalizedStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

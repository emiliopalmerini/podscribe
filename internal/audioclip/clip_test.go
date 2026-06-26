package audioclip

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
)

func TestCopyWritesClipWithFFmpegStreamCopy(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args.txt")
	installFakeFFmpeg(t, dir, argsPath)

	source := filepath.Join(dir, "episode.mp3")
	output := filepath.Join(dir, "clip.mp3")
	if err := os.WriteFile(source, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	result, err := Copy(Request{
		SourcePath:   source,
		OutputPath:   output,
		StartSeconds: 1.2,
		EndSeconds:   1.8,
	})
	if err != nil {
		t.Fatalf("Copy() error = %v", err)
	}
	if result.OutputPath != output || result.SourcePath != source || result.DurationSeconds != 0.6000000000000001 {
		t.Fatalf("Copy() result = %+v", result)
	}
	if !result.StreamCopy || result.Command != "ffmpeg" || result.RequiresExternal != "ffmpeg" {
		t.Fatalf("Copy() result metadata = %+v", result)
	}
	if b, err := os.ReadFile(output); err != nil || string(b) != "clip" {
		t.Fatalf("output content = %q, err=%v", string(b), err)
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read fake ffmpeg args: %v", err)
	}
	args := "\n" + string(argsBytes)
	for _, want := range []string{
		"\n-hide_banner\n",
		"\n-loglevel\nerror\n",
		"\n-ss\n1.200\n",
		"\n-i\n" + source + "\n",
		"\n-t\n0.600\n",
		"\n-vn\n",
		"\n-c\ncopy\n",
		"\n-n\n",
		"\n" + output + "\n",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("ffmpeg args = %q, want %q", string(argsBytes), want)
		}
	}
}

func TestCopyRequiresForceToOverwrite(t *testing.T) {
	dir := t.TempDir()
	installFakeFFmpeg(t, dir, filepath.Join(dir, "args.txt"))

	source := filepath.Join(dir, "episode.mp3")
	output := filepath.Join(dir, "clip.mp3")
	if err := os.WriteFile(source, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(output, []byte("existing"), 0o644); err != nil {
		t.Fatalf("write output: %v", err)
	}

	_, err := Copy(Request{
		SourcePath:   source,
		OutputPath:   output,
		StartSeconds: 1.2,
		EndSeconds:   1.8,
	})
	if err == nil {
		t.Fatal("Copy() error = nil, want overwrite error")
	}
	if apperr.Code(err) != apperr.CodeFilesystem || !strings.Contains(apperr.Message(err), "use --force to overwrite") {
		t.Fatalf("Copy() error = %v, code=%s", err, apperr.Code(err))
	}
}

func TestCopyReportsMissingFFmpeg(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)

	source := filepath.Join(dir, "episode.mp3")
	output := filepath.Join(dir, "clip.mp3")
	if err := os.WriteFile(source, []byte("audio"), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	_, err := Copy(Request{
		SourcePath:   source,
		OutputPath:   output,
		StartSeconds: 1.2,
		EndSeconds:   1.8,
	})
	if err == nil {
		t.Fatal("Copy() error = nil, want missing ffmpeg error")
	}
	if apperr.Code(err) != apperr.CodeConfig || !strings.Contains(apperr.Message(err), "ffmpeg not found in PATH") {
		t.Fatalf("Copy() error = %v, code=%s", err, apperr.Code(err))
	}
}

func TestCopyRejectsInvalidDuration(t *testing.T) {
	_, err := Copy(Request{
		SourcePath:   "episode.mp3",
		OutputPath:   "clip.mp3",
		StartSeconds: 2,
		EndSeconds:   2,
	})
	if err == nil {
		t.Fatal("Copy() error = nil, want duration error")
	}
	if apperr.Code(err) != apperr.CodeInvalidInput {
		t.Fatalf("Copy() code = %s, want invalid_input", apperr.Code(err))
	}
}

func installFakeFFmpeg(t *testing.T, dir, argsPath string) {
	t.Helper()
	path := filepath.Join(dir, "ffmpeg")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$FAKE_FFMPEG_ARGS"
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
	t.Setenv("FAKE_FFMPEG_ARGS", argsPath)
}

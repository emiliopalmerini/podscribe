package audiomerge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMergeBuildsMultichannelFileWithOffsets(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "ffmpeg-args.txt")
	installFakeAudioTools(t, dir, argsPath, map[string]string{
		"emilio.wav": "10.0",
		"guest.wav":  "10.0",
	})

	emilio := filepath.Join(dir, "emilio.wav")
	guest := filepath.Join(dir, "guest.wav")
	for _, path := range []string{emilio, guest} {
		if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
			t.Fatalf("write track: %v", err)
		}
	}

	output := filepath.Join(dir, "merged.flac")
	result, err := Merge(context.Background(), Request{
		Tracks: []Track{
			{Name: "Emilio", Path: emilio},
			{Name: "Guest", Path: guest, Offset: 1500 * time.Millisecond},
		},
		OutputPath: output,
	})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}
	if result.Path != output || result.Size == 0 {
		t.Fatalf("Merge() result = %+v, want output path and nonzero size", result)
	}
	merged, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(merged) != "merged audio" {
		t.Fatalf("output = %q, want fake merged audio", string(merged))
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read ffmpeg args: %v", err)
	}
	args := string(argsBytes)
	for _, want := range []string{
		"-i\n" + emilio,
		"-i\n" + guest,
		"aformat=channel_layouts=mono,apad",
		"aformat=channel_layouts=mono,adelay=1500:all=1,apad",
		"amerge=inputs=2",
		"-map\n[aout]",
		"-t\n11.5",
		"-c:a\nflac",
		output,
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("ffmpeg args missing %q\n%s", want, args)
		}
	}
}

func TestMixdownBuildsSingleChannelFileWithOffsets(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "ffmpeg-args.txt")
	installFakeAudioTools(t, dir, argsPath, map[string]string{
		"emilio.wav": "10.0",
		"guest.wav":  "10.0",
	})

	emilio := filepath.Join(dir, "emilio.wav")
	guest := filepath.Join(dir, "guest.wav")
	for _, path := range []string{emilio, guest} {
		if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
			t.Fatalf("write track: %v", err)
		}
	}

	output := filepath.Join(dir, "mixed.flac")
	result, err := Mixdown(context.Background(), Request{
		Tracks: []Track{
			{Name: "Emilio", Path: emilio},
			{Name: "Guest", Path: guest, Offset: 1500 * time.Millisecond},
		},
		OutputPath: output,
	})
	if err != nil {
		t.Fatalf("Mixdown() error = %v", err)
	}
	if result.Path != output || result.Size == 0 {
		t.Fatalf("Mixdown() result = %+v, want output path and nonzero size", result)
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read ffmpeg args: %v", err)
	}
	args := string(argsBytes)
	for _, want := range []string{
		"-i\n" + emilio,
		"-i\n" + guest,
		"aformat=channel_layouts=mono,apad",
		"aformat=channel_layouts=mono,adelay=1500:all=1,apad",
		"amix=inputs=2:duration=longest",
		"-map\n[aout]",
		"-t\n11.5",
		"-c:a\nflac",
		output,
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("ffmpeg args missing %q\n%s", want, args)
		}
	}
	if strings.Contains(args, "amerge=inputs=2") {
		t.Fatalf("ffmpeg args used multichannel merge for mixdown:\n%s", args)
	}
}

func TestMergeTrimsNegativeOffsets(t *testing.T) {
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "ffmpeg-args.txt")
	installFakeAudioTools(t, dir, argsPath, map[string]string{
		"host.wav":  "12.0",
		"guest.wav": "12.0",
	})

	host := filepath.Join(dir, "host.wav")
	guest := filepath.Join(dir, "guest.wav")
	for _, path := range []string{host, guest} {
		if err := os.WriteFile(path, []byte("audio"), 0o644); err != nil {
			t.Fatalf("write track: %v", err)
		}
	}

	_, err := Merge(context.Background(), Request{
		Tracks: []Track{
			{Name: "Host", Path: host},
			{Name: "Guest", Path: guest, Offset: -500 * time.Millisecond},
		},
		OutputPath: filepath.Join(dir, "merged.flac"),
	})
	if err != nil {
		t.Fatalf("Merge() error = %v", err)
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read ffmpeg args: %v", err)
	}
	args := string(argsBytes)
	for _, want := range []string{
		"atrim=start=0.5,asetpts=PTS-STARTPTS",
		"-t\n12",
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("ffmpeg args missing %q\n%s", want, args)
		}
	}
}

func installFakeAudioTools(t *testing.T, dir, argsPath string, durations map[string]string) {
	t.Helper()
	ffprobe := filepath.Join(dir, "ffprobe")
	var cases strings.Builder
	for base, duration := range durations {
		fmt.Fprintf(&cases, "*%s) printf '%s\\n' ;;\n", base, duration)
	}
	ffprobeScript := fmt.Sprintf(`#!/bin/sh
last=
for arg do
  last="$arg"
done
case "$last" in
%s
*) printf '1.0\n' ;;
esac
`, cases.String())
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

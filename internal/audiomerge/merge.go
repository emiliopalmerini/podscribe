package audiomerge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
)

const (
	MinTracks = 2
	MaxTracks = 5
)

type Track struct {
	Name   string
	Path   string
	Offset time.Duration
}

type Request struct {
	Tracks     []Track
	OutputPath string
}

type Result struct {
	Path     string
	Size     int64
	Duration time.Duration
}

func Merge(ctx context.Context, req Request) (Result, error) {
	tracks, err := ValidateTracks(req.Tracks)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(req.OutputPath) == "" {
		return Result{}, apperr.New(apperr.CodeInvalidInput, "merged audio output path cannot be empty")
	}

	var outputDuration time.Duration
	for _, track := range tracks {
		duration, err := probeDuration(ctx, track.Path)
		if err != nil {
			return Result{}, err
		}
		if track.Offset < 0 && -track.Offset >= duration {
			return Result{}, apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("track %q negative offset trims the entire file", track.Name))
		}
		end := duration + track.Offset
		if end > outputDuration {
			outputDuration = end
		}
	}
	if outputDuration <= 0 {
		return Result{}, apperr.New(apperr.CodeInvalidInput, "merged audio duration must be greater than 0")
	}

	args := ffmpegArgs(tracks, req.OutputPath, outputDuration)
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return Result{}, apperr.Wrap(apperr.CodeFilesystem, strings.TrimSpace("could not merge audio tracks with ffmpeg: "+stderr.String()), err)
	}
	info, err := os.Stat(req.OutputPath)
	if err != nil {
		return Result{}, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("ffmpeg completed but merged audio was not written to %s", req.OutputPath), err)
	}
	if info.IsDir() {
		return Result{}, apperr.New(apperr.CodeInvalidInput, "merged audio output path must be a file")
	}
	return Result{Path: req.OutputPath, Size: info.Size(), Duration: outputDuration}, nil
}

func ValidateTracks(tracks []Track) ([]Track, error) {
	if len(tracks) < MinTracks {
		return nil, apperr.New(apperr.CodeInvalidInput, "provide at least two --track values")
	}
	if len(tracks) > MaxTracks {
		return nil, apperr.New(apperr.CodeInvalidInput, "ElevenLabs multichannel transcription supports at most 5 tracks")
	}

	out := make([]Track, 0, len(tracks))
	seenNames := map[string]struct{}{}
	for _, track := range tracks {
		track.Name = strings.TrimSpace(track.Name)
		track.Path = strings.TrimSpace(track.Path)
		if track.Name == "" {
			return nil, apperr.New(apperr.CodeInvalidInput, "--track speaker name cannot be empty")
		}
		if strings.ContainsAny(track.Name, "\r\n") {
			return nil, apperr.New(apperr.CodeInvalidInput, "--track speaker name cannot contain newlines")
		}
		if _, ok := seenNames[track.Name]; ok {
			return nil, apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("duplicate --track speaker name %q", track.Name))
		}
		seenNames[track.Name] = struct{}{}
		if track.Path == "" {
			return nil, apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("--track %q path cannot be empty", track.Name))
		}
		info, err := os.Stat(track.Path)
		if err != nil {
			return nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not read track file %s", track.Path), err)
		}
		if info.IsDir() {
			return nil, apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("track path %s must be a file", track.Path))
		}
		out = append(out, track)
	}
	return out, nil
}

func ContentHash(tracks []Track) (string, error) {
	tracks, err := ValidateTracks(tracks)
	if err != nil {
		return "", err
	}
	type hashedTrack struct {
		Index       int    `json:"index"`
		ContentHash string `json:"content_hash"`
		OffsetNanos int64  `json:"offset_nanos"`
	}
	payload := struct {
		Version string        `json:"version"`
		Tracks  []hashedTrack `json:"tracks"`
	}{
		Version: "podscribe:multichannel:v1",
	}
	for i, track := range tracks {
		hash, err := fileSHA256(track.Path)
		if err != nil {
			return "", err
		}
		payload.Tracks = append(payload.Tracks, hashedTrack{
			Index:       i,
			ContentHash: hash,
			OffsetNanos: int64(track.Offset),
		})
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", apperr.Wrap(apperr.CodeUnexpected, "could not encode multichannel hash input", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func ffmpegArgs(tracks []Track, outputPath string, outputDuration time.Duration) []string {
	args := []string{"-hide_banner", "-loglevel", "error", "-y", "-vn"}
	for _, track := range tracks {
		args = append(args, "-i", track.Path)
	}

	var parts []string
	for i, track := range tracks {
		filters := []string{"aformat=channel_layouts=mono"}
		if track.Offset < 0 {
			filters = append(filters, "atrim=start="+formatSecondsDuration(-track.Offset), "asetpts=PTS-STARTPTS")
		} else if track.Offset > 0 {
			filters = append(filters, fmt.Sprintf("adelay=%d:all=1", delayMilliseconds(track.Offset)))
		}
		filters = append(filters, "apad")
		parts = append(parts, fmt.Sprintf("[%d:a]%s[a%d]", i, strings.Join(filters, ","), i))
	}

	var inputs strings.Builder
	for i := range tracks {
		fmt.Fprintf(&inputs, "[a%d]", i)
	}
	parts = append(parts, fmt.Sprintf("%samerge=inputs=%d[aout]", inputs.String(), len(tracks)))

	args = append(args,
		"-filter_complex", strings.Join(parts, ";"),
		"-map", "[aout]",
		"-t", formatSecondsDuration(outputDuration),
		"-c:a", "flac",
		outputPath,
	)
	return args
}

func probeDuration(ctx context.Context, path string) (time.Duration, error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "ffprobe",
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, apperr.Wrap(apperr.CodeFilesystem, strings.TrimSpace("could not inspect track duration with ffprobe: "+stderr.String()), err)
	}
	seconds, err := strconv.ParseFloat(strings.TrimSpace(stdout.String()), 64)
	if err != nil || seconds <= 0 {
		return 0, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not parse ffprobe duration for %s", path), err)
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not open track file %s for hashing", path), err)
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not hash track file %s", path), err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func delayMilliseconds(d time.Duration) int64 {
	return int64(math.Round(float64(d) / float64(time.Millisecond)))
}

func formatSecondsDuration(d time.Duration) string {
	seconds := float64(d) / float64(time.Second)
	return strconv.FormatFloat(seconds, 'f', -1, 64)
}

func TempOutputPath() (string, error) {
	file, err := os.CreateTemp("", "podscribe-multichannel-*.flac")
	if err != nil {
		return "", apperr.Wrap(apperr.CodeFilesystem, "could not create temporary multichannel audio file", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not close temporary audio file %s", path), err)
	}
	return filepath.Clean(path), nil
}

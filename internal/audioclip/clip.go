package audioclip

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
)

type Request struct {
	SourcePath   string
	OutputPath   string
	StartSeconds float64
	EndSeconds   float64
	Force        bool
}

type Result struct {
	SourcePath       string  `json:"source_path"`
	OutputPath       string  `json:"output_path"`
	StartSeconds     float64 `json:"start_seconds"`
	EndSeconds       float64 `json:"end_seconds"`
	DurationSeconds  float64 `json:"duration_seconds"`
	Command          string  `json:"command"`
	StreamCopy       bool    `json:"stream_copy"`
	RequiresExternal string  `json:"requires_external"`
}

func Copy(req Request) (Result, error) {
	sourcePath := strings.TrimSpace(req.SourcePath)
	outputPath := strings.TrimSpace(req.OutputPath)
	if sourcePath == "" {
		return Result{}, apperr.New(apperr.CodeInvalidInput, "audio source path cannot be empty")
	}
	if outputPath == "" {
		return Result{}, apperr.New(apperr.CodeInvalidInput, "audio clip output path cannot be empty")
	}
	if req.EndSeconds <= req.StartSeconds {
		return Result{}, apperr.New(apperr.CodeInvalidInput, "audio clip duration must be greater than 0")
	}
	if samePath(sourcePath, outputPath) {
		return Result{}, apperr.New(apperr.CodeInvalidInput, "audio clip output path must differ from the source audio path")
	}

	info, err := os.Stat(sourcePath)
	if err != nil {
		return Result{}, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not read audio source %s", sourcePath), err)
	}
	if info.IsDir() {
		return Result{}, apperr.New(apperr.CodeInvalidInput, "audio source path must be a file")
	}
	if err := ensureWritableOutput(outputPath, req.Force); err != nil {
		return Result{}, err
	}

	ffmpegPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		return Result{}, apperr.Wrap(apperr.CodeConfig, "ffmpeg not found in PATH; install ffmpeg to use --clip-out", err)
	}

	duration := req.EndSeconds - req.StartSeconds
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-ss", formatSeconds(req.StartSeconds),
		"-i", sourcePath,
		"-t", formatSeconds(duration),
		"-vn",
		"-c", "copy",
	}
	if req.Force {
		args = append(args, "-y")
	} else {
		args = append(args, "-n")
	}
	args = append(args, outputPath)

	var stderr bytes.Buffer
	cmd := exec.Command(ffmpegPath, args...)
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		message := fmt.Sprintf("could not write audio clip to %s", outputPath)
		if detail != "" {
			message += ": " + detail
		}
		return Result{}, apperr.Wrap(apperr.CodeFilesystem, message, err)
	}
	if _, err := os.Stat(outputPath); err != nil {
		return Result{}, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("ffmpeg completed but audio clip was not written to %s", outputPath), err)
	}

	return Result{
		SourcePath:       sourcePath,
		OutputPath:       outputPath,
		StartSeconds:     req.StartSeconds,
		EndSeconds:       req.EndSeconds,
		DurationSeconds:  duration,
		Command:          "ffmpeg",
		StreamCopy:       true,
		RequiresExternal: "ffmpeg",
	}, nil
}

func ensureWritableOutput(path string, force bool) error {
	info, err := os.Stat(path)
	if err == nil {
		if info.IsDir() {
			return apperr.New(apperr.CodeInvalidInput, "audio clip output path must be a file")
		}
		if !force {
			return apperr.New(apperr.CodeFilesystem, fmt.Sprintf("%s already exists; use --force to overwrite", path))
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not inspect %s", path), err)
	}
	return nil
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	if errA == nil && errB == nil {
		return filepath.Clean(absA) == filepath.Clean(absB)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

func formatSeconds(seconds float64) string {
	return strconv.FormatFloat(seconds, 'f', 3, 64)
}

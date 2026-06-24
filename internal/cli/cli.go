package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
	"github.com/emiliopalmerini/podscribe/internal/config"
	"github.com/emiliopalmerini/podscribe/internal/elevenlabs"
	"github.com/emiliopalmerini/podscribe/internal/output"
	"github.com/emiliopalmerini/podscribe/internal/render"
)

type rootOptions struct {
	json    bool
	apiKey  string
	baseURL string
	version string
	in      io.Reader
	out     io.Writer
	errOut  io.Writer
}

func Execute(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer, version string) error {
	opts := &rootOptions{version: version, in: in, out: out, errOut: errOut}
	cmd := newRootCommand(ctx, opts)
	cmd.SetArgs(args)
	cmd.SetIn(in)
	cmd.SetOut(out)
	cmd.SetErr(errOut)

	err := cmd.Execute()
	if err == nil {
		return nil
	}
	if opts.json {
		_ = output.JSONError(out, err)
	} else {
		output.HumanError(errOut, err)
	}
	return err
}

func newRootCommand(ctx context.Context, opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "podscribe",
		Short:         "Transcribe podcast audio with ElevenLabs",
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.PersistentFlags().BoolVar(&opts.json, "json", false, "emit machine-readable JSON on stdout")
	cmd.PersistentFlags().StringVar(&opts.apiKey, "api-key", "", "ElevenLabs API key for this invocation")
	cmd.PersistentFlags().StringVar(&opts.baseURL, "base-url", "", "ElevenLabs API base URL")

	cmd.AddCommand(newInitCommand(opts))
	cmd.AddCommand(newDoctorCommand(ctx, opts))
	cmd.AddCommand(newTranscribeCommand(ctx, opts))
	cmd.AddCommand(newTranscriptsCommand(ctx, opts))
	cmd.AddCommand(newRequestCommand(ctx, opts))
	return cmd
}

func newInitCommand(opts *rootOptions) *cobra.Command {
	var apiKey string
	var baseURL string
	cmd := &cobra.Command{
		Use:   "init --api-key <key>",
		Short: "Store local ElevenLabs configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(apiKey) == "" {
				return apperr.New(apperr.CodeInvalidInput, "provide --api-key")
			}
			if strings.TrimSpace(baseURL) == "" {
				baseURL = config.DefaultBaseURL
			}
			path, err := config.Save(config.Config{APIKey: strings.TrimSpace(apiKey), BaseURL: strings.TrimSpace(baseURL)})
			if err != nil {
				return err
			}
			data := map[string]any{
				"config_path": path,
				"base_url":    strings.TrimSpace(baseURL),
				"api_key":     "[REDACTED]",
			}
			if opts.json {
				return output.JSONSuccess(opts.out, data)
			}
			fmt.Fprintf(opts.out, "Wrote config to %s\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&apiKey, "api-key", "", "ElevenLabs API key to store")
	cmd.Flags().StringVar(&baseURL, "base-url", config.DefaultBaseURL, "ElevenLabs API base URL to store")
	return cmd
}

func newDoctorCommand(ctx context.Context, opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check config, auth, and API reachability",
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := config.Resolve(opts.apiKey, opts.baseURL)
			if err != nil {
				return err
			}
			data := map[string]any{
				"version":         opts.version,
				"config_path":     rt.ConfigPath,
				"config_found":    rt.ConfigFound,
				"auth_available":  rt.APIKey != "",
				"auth_source":     rt.APIKeySource,
				"base_url":        rt.BaseURL,
				"base_url_source": rt.BaseURLSource,
			}
			if rt.APIKey == "" {
				data["remote_check"] = "skipped_missing_auth"
				data["setup"] = "Set ELEVENLABS_API_KEY or run podscribe init --api-key <key>."
			} else {
				client := elevenlabs.NewClient(rt.BaseURL, rt.APIKey)
				_, err := client.RawGet(ctx, "/v1/models")
				if err != nil {
					data["remote_check"] = "failed"
					data["api_reachable"] = false
					data["api_error"] = output.Redact(apperr.Message(err))
				} else {
					data["remote_check"] = "ok"
					data["api_reachable"] = true
				}
			}
			if opts.json {
				return output.JSONSuccess(opts.out, data)
			}
			printDoctor(opts.out, data)
			return nil
		},
	}
	return cmd
}

func printDoctor(w io.Writer, data map[string]any) {
	fmt.Fprintf(w, "podscribe %v\n", data["version"])
	fmt.Fprintf(w, "Config: %v (found: %v)\n", data["config_path"], data["config_found"])
	fmt.Fprintf(w, "Auth: %v via %v\n", data["auth_available"], data["auth_source"])
	fmt.Fprintf(w, "Base URL: %v via %v\n", data["base_url"], data["base_url_source"])
	fmt.Fprintf(w, "Remote check: %v\n", data["remote_check"])
	if setup, ok := data["setup"]; ok {
		fmt.Fprintf(w, "Setup: %v\n", setup)
	}
	if apiErr, ok := data["api_error"]; ok {
		fmt.Fprintf(w, "API error: %v\n", apiErr)
	}
}

func newTranscribeCommand(ctx context.Context, opts *rootOptions) *cobra.Command {
	var flags transcribeFlags
	cmd := &cobra.Command{
		Use:   "transcribe <audio-file>",
		Short: "Transcribe a local podcast audio file",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return apperr.New(apperr.CodeInvalidInput, "provide exactly one audio file")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTranscribe(ctx, opts, flags, args[0])
		},
	}
	cmd.Flags().StringVar(&flags.model, "model", "scribe_v2", "ElevenLabs speech-to-text model")
	cmd.Flags().StringVar(&flags.language, "language", "", "optional ISO-639 language code")
	cmd.Flags().BoolVar(&flags.diarize, "diarize", false, "annotate speaker turns")
	cmd.Flags().IntVar(&flags.speakers, "speakers", 0, "maximum number of speakers; implies --diarize")
	cmd.Flags().StringArrayVar(&flags.keyterms, "keyterm", nil, "custom vocabulary term; repeatable")
	cmd.Flags().StringVar(&flags.keytermsFile, "keyterms-file", "", "file with one keyterm per line")
	cmd.Flags().BoolVar(&flags.clean, "clean", false, "remove fillers and non-speech artifacts where supported")
	cmd.Flags().BoolVar(&flags.noAudioEvents, "no-audio-events", false, "disable audio event tags such as laughter")
	cmd.Flags().StringVar(&flags.out, "out", "", "Markdown output path")
	cmd.Flags().StringVar(&flags.rawOut, "raw-out", "", "optional raw ElevenLabs JSON output path")
	cmd.Flags().BoolVar(&flags.force, "force", false, "overwrite existing output files")
	return cmd
}

type transcribeFlags struct {
	model         string
	language      string
	diarize       bool
	speakers      int
	keyterms      []string
	keytermsFile  string
	clean         bool
	noAudioEvents bool
	out           string
	rawOut        string
	force         bool
}

func runTranscribe(ctx context.Context, opts *rootOptions, flags transcribeFlags, audioPath string) error {
	audioSize, err := validateAudioPath(audioPath)
	if err != nil {
		return err
	}
	if err := validateTranscribeFlags(flags); err != nil {
		return err
	}
	keyterms, err := collectKeyterms(flags.keyterms, flags.keytermsFile)
	if err != nil {
		return err
	}
	if err := validateKeyterms(keyterms); err != nil {
		return err
	}

	outPath := flags.out
	if outPath == "" {
		outPath = defaultTranscriptPath(audioPath)
	}
	if err := ensureWritableTarget(outPath, flags.force); err != nil {
		return err
	}
	if flags.rawOut != "" {
		if err := ensureWritableTarget(flags.rawOut, flags.force); err != nil {
			return err
		}
	}

	rt, err := config.Resolve(opts.apiKey, opts.baseURL)
	if err != nil {
		return err
	}
	client := elevenlabs.NewClient(rt.BaseURL, rt.APIKey)
	var progress *transcribeProgressPrinter
	if !opts.json {
		fmt.Fprintf(opts.errOut, "Uploading %s (%s) to ElevenLabs...\n", audioPath, formatBytes(audioSize))
		progress = newTranscribeProgressPrinter(opts.errOut)
	}
	diarize := flags.diarize || flags.speakers > 0
	transcript, raw, err := client.TranscribeFile(ctx, elevenlabs.TranscribeOptions{
		FilePath:              audioPath,
		Model:                 flags.model,
		Language:              flags.language,
		Diarize:               diarize,
		Speakers:              flags.speakers,
		Keyterms:              keyterms,
		Clean:                 flags.clean,
		TagAudioEvents:        !flags.noAudioEvents,
		TimestampsGranularity: "word",
		OnUploadProgress:      progressCallback(progress),
	})
	if progress != nil {
		progress.Stop()
	}
	if err != nil {
		return err
	}

	if !opts.json {
		fmt.Fprintln(opts.errOut, "Rendering Markdown transcript...")
	}
	md := render.Markdown(transcript, render.MarkdownOptions{
		Title:       strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath)),
		SourceFile:  filepath.Base(audioPath),
		Model:       flags.model,
		GeneratedAt: time.Now().UTC(),
		Diarized:    diarize,
	})
	if err := os.WriteFile(outPath, []byte(md), 0o644); err != nil {
		return apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not write transcript to %s", outPath), err)
	}
	if flags.rawOut != "" {
		if err := os.WriteFile(flags.rawOut, append(raw, '\n'), 0o644); err != nil {
			return apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not write raw transcript to %s", flags.rawOut), err)
		}
	}

	data := map[string]any{
		"output_path":      outPath,
		"raw_output_path":  optionalString(flags.rawOut),
		"source_file":      audioPath,
		"model":            flags.model,
		"language_code":    transcript.LanguageCode,
		"transcription_id": transcript.TranscriptionID,
	}
	if transcript.AudioDurationSecs != nil {
		data["audio_duration_secs"] = *transcript.AudioDurationSecs
	}
	if opts.json {
		return output.JSONSuccess(opts.out, data)
	}
	fmt.Fprintf(opts.out, "Wrote %s\n", outPath)
	if flags.rawOut != "" {
		fmt.Fprintf(opts.out, "Wrote %s\n", flags.rawOut)
	}
	return nil
}

func newTranscriptsCommand(ctx context.Context, opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transcripts",
		Short: "Read or delete stored ElevenLabs transcripts",
	}
	cmd.AddCommand(newTranscriptGetCommand(ctx, opts))
	cmd.AddCommand(newTranscriptDeleteCommand(ctx, opts))
	return cmd
}

func newTranscriptGetCommand(ctx context.Context, opts *rootOptions) *cobra.Command {
	var outPath string
	cmd := &cobra.Command{
		Use:   "get <transcription-id>",
		Short: "Fetch a stored transcript by ID",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
				return apperr.New(apperr.CodeInvalidInput, "provide a transcription ID")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := config.Resolve(opts.apiKey, opts.baseURL)
			if err != nil {
				return err
			}
			client := elevenlabs.NewClient(rt.BaseURL, rt.APIKey)
			transcript, raw, err := client.GetTranscript(ctx, args[0])
			if err != nil {
				return err
			}
			if outPath != "" {
				if err := os.WriteFile(outPath, append(raw, '\n'), 0o644); err != nil {
					return apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not write transcript JSON to %s", outPath), err)
				}
			}
			if opts.json {
				data := map[string]any{
					"transcription_id": args[0],
					"output_path":      optionalString(outPath),
					"transcript":       transcript,
				}
				return output.JSONSuccess(opts.out, data)
			}
			if outPath != "" {
				fmt.Fprintf(opts.out, "Wrote %s\n", outPath)
				return nil
			}
			_, err = opts.out.Write(append(raw, '\n'))
			return err
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "write raw transcript JSON to this path")
	return cmd
}

func newTranscriptDeleteCommand(ctx context.Context, opts *rootOptions) *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <transcription-id>",
		Short: "Delete a stored transcript by ID",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
				return apperr.New(apperr.CodeInvalidInput, "provide a transcription ID")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if !yes {
				return apperr.New(apperr.CodeInvalidInput, "deleting a transcript requires --yes")
			}
			rt, err := config.Resolve(opts.apiKey, opts.baseURL)
			if err != nil {
				return err
			}
			client := elevenlabs.NewClient(rt.BaseURL, rt.APIKey)
			raw, err := client.DeleteTranscript(ctx, args[0])
			if err != nil {
				return err
			}
			data := map[string]any{
				"transcription_id": args[0],
				"deleted":          true,
			}
			if len(raw) > 0 && json.Valid(raw) {
				var body any
				if err := json.Unmarshal(raw, &body); err == nil {
					data["response"] = body
				}
			}
			if opts.json {
				return output.JSONSuccess(opts.out, data)
			}
			fmt.Fprintf(opts.out, "Deleted transcript %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "confirm deletion")
	return cmd
}

func newRequestCommand(ctx context.Context, opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "request",
		Short: "Raw read-only ElevenLabs API requests",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "get <path>",
		Short: "Run a raw GET request against a /v1/... path",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
				return apperr.New(apperr.CodeInvalidInput, "provide a /v1/... path")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			rt, err := config.Resolve(opts.apiKey, opts.baseURL)
			if err != nil {
				return err
			}
			client := elevenlabs.NewClient(rt.BaseURL, rt.APIKey)
			raw, err := client.RawGet(ctx, args[0])
			if err != nil {
				return err
			}
			if opts.json {
				var body any
				if err := json.Unmarshal(raw, &body); err != nil {
					body = string(raw)
				}
				return output.JSONSuccess(opts.out, body)
			}
			_, err = opts.out.Write(append(raw, '\n'))
			return err
		},
	})
	return cmd
}

func validateAudioPath(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not read audio file %s", path), err)
	}
	if info.IsDir() {
		return 0, apperr.New(apperr.CodeInvalidInput, "audio path must be a file")
	}
	return info.Size(), nil
}

func validateTranscribeFlags(flags transcribeFlags) error {
	switch flags.model {
	case "scribe_v2", "scribe_v1":
	default:
		return apperr.New(apperr.CodeInvalidInput, "--model must be scribe_v2 or scribe_v1")
	}
	if flags.speakers < 0 || flags.speakers > 32 {
		return apperr.New(apperr.CodeInvalidInput, "--speakers must be between 1 and 32")
	}
	if flags.clean && flags.model != "scribe_v2" {
		return apperr.New(apperr.CodeInvalidInput, "--clean is only supported with scribe_v2")
	}
	return nil
}

func collectKeyterms(flagTerms []string, filePath string) ([]string, error) {
	terms := append([]string{}, flagTerms...)
	if filePath == "" {
		return normalizeTerms(terms), nil
	}
	b, err := os.ReadFile(filePath)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not read keyterms file %s", filePath), err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		terms = append(terms, line)
	}
	return normalizeTerms(terms), nil
}

func normalizeTerms(terms []string) []string {
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term != "" {
			out = append(out, term)
		}
	}
	return out
}

func validateKeyterms(terms []string) error {
	if len(terms) > 1000 {
		return apperr.New(apperr.CodeInvalidInput, "keyterms cannot exceed 1000 entries")
	}
	for _, term := range terms {
		if len([]rune(term)) >= 50 {
			return apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("keyterm %q must be shorter than 50 characters", term))
		}
		if len(strings.Fields(term)) > 5 {
			return apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("keyterm %q must contain at most 5 words", term))
		}
		if strings.ContainsAny(term, `<>[]{}\`) {
			return apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("keyterm %q contains unsupported characters", term))
		}
	}
	return nil
}

func defaultTranscriptPath(audioPath string) string {
	dir := filepath.Dir(audioPath)
	base := filepath.Base(audioPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	return filepath.Join(dir, stem+".transcript.md")
}

func ensureWritableTarget(path string, force bool) error {
	if path == "" {
		return nil
	}
	if _, err := os.Stat(path); err == nil && !force {
		return apperr.New(apperr.CodeFilesystem, fmt.Sprintf("%s already exists; use --force to overwrite", path))
	} else if err != nil && !os.IsNotExist(err) {
		return apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not inspect %s", path), err)
	}
	return nil
}

func optionalString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

type transcribeProgressPrinter struct {
	mu             sync.Mutex
	w              io.Writer
	lastUploadLine time.Time
	uploadDone     bool
	stopped        bool
	waitStop       chan struct{}
}

const (
	uploadProgressInterval = time.Second
	transcribeWaitInterval = 30 * time.Second
)

func newTranscribeProgressPrinter(w io.Writer) *transcribeProgressPrinter {
	return &transcribeProgressPrinter{
		w:              w,
		lastUploadLine: time.Now().Add(-uploadProgressInterval),
		waitStop:       make(chan struct{}),
	}
}

func progressCallback(progress *transcribeProgressPrinter) func(elevenlabs.UploadProgress) {
	if progress == nil {
		return nil
	}
	return progress.ReportUpload
}

func (p *transcribeProgressPrinter) ReportUpload(progress elevenlabs.UploadProgress) {
	if progress.SentBytes == 0 {
		return
	}

	now := time.Now()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}

	uploadComplete := progress.TotalBytes >= 0 && progress.SentBytes >= progress.TotalBytes
	if !uploadComplete && now.Sub(p.lastUploadLine) < uploadProgressInterval {
		return
	}

	fmt.Fprintf(p.w, "Uploaded %s / %s (%d%%)\n", formatBytes(progress.SentBytes), formatBytes(progress.TotalBytes), uploadPercent(progress.SentBytes, progress.TotalBytes))
	p.lastUploadLine = now

	if uploadComplete && !p.uploadDone {
		p.uploadDone = true
		p.startWaitingLocked(now)
	}
}

func (p *transcribeProgressPrinter) startWaitingLocked(startedAt time.Time) {
	if p.stopped {
		return
	}
	fmt.Fprintln(p.w, "Upload complete; waiting for ElevenLabs to transcribe...")
	go p.waitLoop(startedAt)
}

func (p *transcribeProgressPrinter) waitLoop(startedAt time.Time) {
	ticker := time.NewTicker(transcribeWaitInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.mu.Lock()
			if !p.stopped {
				fmt.Fprintf(p.w, "Still waiting for ElevenLabs transcript response (elapsed %s)...\n", formatElapsed(time.Since(startedAt)))
			}
			p.mu.Unlock()
		case <-p.waitStop:
			return
		}
	}
}

func (p *transcribeProgressPrinter) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	p.stopped = true
	close(p.waitStop)
}

func uploadPercent(sent, total int64) int {
	if total <= 0 {
		return 100
	}
	if sent <= 0 {
		return 0
	}
	percent := int(float64(sent) / float64(total) * 100)
	if percent > 100 {
		return 100
	}
	return percent
}

func formatBytes(bytes int64) string {
	if bytes < 0 {
		bytes = 0
	}
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB"} {
		value /= 1024
		if value < 1024 {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%.1f PiB", value/1024)
}

func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Minute {
		return d.String()
	}
	minutes := int(d / time.Minute)
	seconds := int((d % time.Minute) / time.Second)
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

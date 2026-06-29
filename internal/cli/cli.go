package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	sdk "github.com/emiliopalmerini/elevenlabs-go/elevenlabs"
	"github.com/spf13/cobra"

	"github.com/emiliopalmerini/podscribe/internal/apperr"
	"github.com/emiliopalmerini/podscribe/internal/audioclip"
	"github.com/emiliopalmerini/podscribe/internal/audiomerge"
	"github.com/emiliopalmerini/podscribe/internal/config"
	elclient "github.com/emiliopalmerini/podscribe/internal/elevenlabs"
	"github.com/emiliopalmerini/podscribe/internal/jobstore"
	"github.com/emiliopalmerini/podscribe/internal/locate"
	"github.com/emiliopalmerini/podscribe/internal/output"
	"github.com/emiliopalmerini/podscribe/internal/render"
	"github.com/emiliopalmerini/podscribe/internal/transcription"
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
	if rootBoolFlag(args, "version", "v") && rootBoolFlag(args, "json", "") {
		return output.JSONSuccess(out, map[string]any{"version": version})
	}
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
		Version:       opts.version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	cmd.SetVersionTemplate("podscribe {{.Version}}\n")
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

func rootBoolFlag(args []string, name, shorthand string) bool {
	longFlag := "--" + name
	longPrefix := longFlag + "="
	shortFlag := ""
	if shorthand != "" {
		shortFlag = "-" + shorthand
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return false
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			return false
		}
		if arg == longFlag || (shortFlag != "" && arg == shortFlag) {
			return true
		}
		if after, ok := strings.CutPrefix(arg, longPrefix); ok {
			return after == "true"
		}
		switch arg {
		case "--api-key", "--base-url":
			i++
		}
	}
	return false
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
				client := elclient.NewClient(rt.BaseURL, rt.APIKey)
				err := client.Check(ctx)
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
		Use:   "transcribe <audio-file>|--track <name=audio-file>",
		Short: "Transcribe local podcast audio",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(flags.tracks) > 0 {
				if len(args) != 0 {
					return apperr.New(apperr.CodeInvalidInput, "do not provide an audio file when using --track")
				}
				return nil
			}
			if len(args) != 1 {
				return apperr.New(apperr.CodeInvalidInput, "provide exactly one audio file")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			audioPath := ""
			if len(args) > 0 {
				audioPath = args[0]
			}
			return runTranscribe(ctx, opts, flags, audioPath)
		},
	}
	cmd.Flags().StringVar(&flags.model, "model", "scribe_v2", "ElevenLabs speech-to-text model")
	cmd.Flags().StringVar(&flags.language, "language", "", "optional ISO-639 language code")
	cmd.Flags().BoolVar(&flags.diarize, "diarize", false, "annotate speaker turns")
	cmd.Flags().IntVar(&flags.speakers, "speakers", 0, "maximum number of speakers; implies --diarize")
	cmd.Flags().StringArrayVar(&flags.speakerNames, "speaker-name", nil, "speaker display name; repeatable and ordered by detected speaker")
	cmd.Flags().StringVar(&flags.speakerNamesFile, "speaker-names-file", "", "file with one speaker display name per line")
	cmd.Flags().StringArrayVar(&flags.keyterms, "keyterm", nil, "custom vocabulary term; repeatable")
	cmd.Flags().StringVar(&flags.keytermsFile, "keyterms-file", "", "file with one keyterm per line")
	cmd.Flags().StringArrayVar(&flags.tracks, "track", nil, "speaker track as name=audio-file; repeatable for multichannel upload")
	cmd.Flags().StringArrayVar(&flags.trackOffsets, "track-offset", nil, "speaker track offset as name=duration, for example Guest=1.42s or Guest=-500ms")
	cmd.Flags().BoolVar(&flags.trackMixdown, "track-mixdown", false, "mix --track inputs into one audio file instead of preserving separate channels")
	cmd.Flags().BoolVar(&flags.clean, "clean", false, "remove fillers and non-speech artifacts where supported")
	cmd.Flags().BoolVar(&flags.noAudioEvents, "no-audio-events", false, "disable audio event tags such as laughter")
	cmd.Flags().BoolVar(&flags.timestamps, "timestamps", false, "include timestamps in Markdown transcript blocks")
	cmd.Flags().StringVar(&flags.out, "out", "", "Markdown output path")
	cmd.Flags().StringVar(&flags.rawOut, "raw-out", "", "optional raw ElevenLabs JSON output path")
	cmd.Flags().BoolVar(&flags.force, "force", false, "overwrite existing output files")
	cmd.Flags().BoolVar(&flags.webhook, "webhook", false, "submit transcription asynchronously and wait for webhook import")
	cmd.Flags().StringVar(&flags.webhookID, "webhook-id", "", "specific ElevenLabs webhook ID to use with --webhook")
	return cmd
}

type transcribeFlags struct {
	model            string
	language         string
	diarize          bool
	speakers         int
	speakerNames     []string
	speakerNamesFile string
	keyterms         []string
	keytermsFile     string
	tracks           []string
	trackOffsets     []string
	trackMixdown     bool
	clean            bool
	noAudioEvents    bool
	timestamps       bool
	out              string
	rawOut           string
	force            bool
	webhook          bool
	webhookID        string
}

type transcribeInput struct {
	SourcePath        string
	SourceLabel       string
	RenderSourceFile  string
	TitleSourcePath   string
	DefaultOutputPath string
	UploadPath        string
	UploadSize        int64
	AudioHash         string
	MultiChannel      bool
	TrackMixdown      bool
	Tracks            []audiomerge.Track
	SpeakerNames      []string
}

func runTranscribe(ctx context.Context, opts *rootOptions, flags transcribeFlags, audioPath string) error {
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
	input, err := resolveTranscribeInput(flags, audioPath)
	if err != nil {
		return err
	}
	speakerNames := input.SpeakerNames
	speakers := flags.speakers
	diarize := flags.diarize || speakers > 0 || len(speakerNames) > 0
	if input.MultiChannel {
		speakers = 0
		diarize = false
	} else if speakers == 0 && len(speakerNames) > 0 {
		speakers = len(speakerNames)
	}

	outPath := flags.out
	if outPath == "" {
		outPath = input.DefaultOutputPath
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
	client := elclient.NewClient(rt.BaseURL, rt.APIKey)
	accountNamespace, namespaceSource, err := resolveAccountNamespace(ctx, client, rt.APIKey)
	if err != nil {
		return err
	}
	if namespaceSource == "api_key_hash" && !opts.json {
		fmt.Fprintln(opts.errOut, "Warning: could not resolve ElevenLabs user_id; using API-key hash namespace for cache.")
	}

	audioHash := input.AudioHash
	remoteReq := buildRemoteRequest(flags, keyterms, diarize, speakers, input.MultiChannel)
	requestHash, err := jobstore.RequestHash(remoteReq)
	if err != nil {
		return err
	}
	jobKey, err := jobstore.JobKey(rt.BaseURL, accountNamespace, audioHash, requestHash)
	if err != nil {
		return err
	}
	cachePath, err := jobstore.Path(jobKey)
	if err != nil {
		return err
	}
	renderReq := buildRenderRequest(input.TitleSourcePath, input.RenderSourceFile, flags, diarize || input.MultiChannel, speakerNames)

	if record, found, err := jobstore.Load(jobKey); err != nil {
		return err
	} else if found && !flags.force {
		switch record.Status {
		case jobstore.StatusCompleted:
			transcript, raw, err := transcriptFromRaw(record.RawResponse, cachePath)
			if err != nil {
				return err
			}
			return writeTranscriptOutputs(opts, flags, input.SourceLabel, outPath, transcript, raw, renderReq, transcribeOutputMeta{
				JobKey:                 jobKey,
				AccountNamespace:       accountNamespace,
				AccountNamespaceSource: namespaceSource,
				CacheStatus:            "hit",
				CachePath:              cachePath,
				ReusedCache:            true,
			})
		case jobstore.StatusPending, jobstore.StatusSubmitted:
			return apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("transcription job %s is %s; use --force to submit a new request or import the webhook result", jobKey, record.Status))
		}
	}

	uploadPath := input.UploadPath
	audioSize := input.UploadSize
	if len(input.Tracks) > 0 {
		var tempPath string
		var mergeResult audiomerge.Result
		if input.TrackMixdown {
			if !opts.json {
				writeTrackMixdownMap(opts.errOut, input.Tracks)
				fmt.Fprintln(opts.errOut, "Mixing tracks down into temporary audio...")
			}
			tempPath, err = audiomerge.TempMixdownOutputPath()
			if err != nil {
				return err
			}
			mergeResult, err = audiomerge.Mixdown(ctx, audiomerge.Request{Tracks: input.Tracks, OutputPath: tempPath})
		} else {
			if !opts.json {
				writeTrackMap(opts.errOut, input.Tracks)
				fmt.Fprintln(opts.errOut, "Merging tracks into temporary multichannel audio...")
			}
			tempPath, err = audiomerge.TempOutputPath()
			if err != nil {
				return err
			}
			mergeResult, err = audiomerge.Merge(ctx, audiomerge.Request{Tracks: input.Tracks, OutputPath: tempPath})
		}
		if err != nil {
			return err
		}
		defer os.Remove(tempPath)
		uploadPath = mergeResult.Path
		audioSize = mergeResult.Size
		if !opts.json {
			if input.TrackMixdown {
				fmt.Fprintf(opts.errOut, "Mixed down %d tracks into %s (%s).\n", len(input.Tracks), uploadPath, formatBytes(audioSize))
			} else {
				fmt.Fprintf(opts.errOut, "Merged %d tracks into %s (%s).\n", len(input.Tracks), uploadPath, formatBytes(audioSize))
			}
		}
	}

	record := newJobRecord(jobKey, accountNamespace, rt.BaseURL, audioHash, requestHash, remoteReq, renderReq, input.SourcePath, outPath, flags.rawOut, jobstoreInputTracks(input.Tracks))
	cachePath, err = jobstore.Save(record)
	if err != nil {
		return err
	}

	transcriptFile, closeTranscriptFile, _, err := elclient.OpenTranscriptFile(uploadPath)
	if err != nil {
		if shouldRecordTranscribeFailure(err) {
			_, _ = saveFailedJob(record, err)
		}
		return err
	}
	defer closeTranscriptFile()

	transcribeReq := buildTranscriptRequest(transcriptFile, flags, keyterms, diarize, speakers, input.MultiChannel)
	if input.MultiChannel {
		transcribeReq.MultichannelOutputStyle = "combined"
	}

	if flags.webhook {
		transcribeReq.WebhookID = flags.webhookID
		transcribeReq.WebhookMetadata = map[string]any{
			"podscribe_job_key":           jobKey,
			"podscribe_account_namespace": accountNamespace,
		}
		return submitWebhookTranscription(ctx, opts, client, uploadPath, transcribeReq, audioSize, record, transcribeOutputMeta{
			JobKey:                 jobKey,
			AccountNamespace:       accountNamespace,
			AccountNamespaceSource: namespaceSource,
			CacheStatus:            "submitted",
			CachePath:              cachePath,
			ReusedCache:            false,
		})
	}

	var progress *transcribeProgressPrinter
	if !opts.json {
		fmt.Fprintf(opts.errOut, "Uploading %s (%s) to ElevenLabs...\n", uploadPath, formatBytes(audioSize))
		progress = newTranscribeProgressPrinter(opts.errOut)
	}
	transcribeReq.OnUploadProgress = progressCallback(progress)
	transcript, err := client.CreateTranscript(ctx, transcribeReq)
	if progress != nil {
		progress.Stop()
	}
	if err != nil {
		if shouldRecordTranscribeFailure(err) {
			_, _ = saveFailedJob(record, err)
		}
		return err
	}
	raw, err := transcriptJSON(transcript)
	if err != nil {
		return err
	}

	record.Status = jobstore.StatusCompleted
	record.TranscriptionID = transcript.TranscriptionID
	record.RawResponse = append([]byte(nil), raw...)
	record.LastErrorCode = ""
	record.LastErrorMessage = ""
	cachePath, err = jobstore.Save(record)
	if err != nil {
		return err
	}
	return writeTranscriptOutputs(opts, flags, input.SourceLabel, outPath, transcript, raw, renderReq, transcribeOutputMeta{
		JobKey:                 jobKey,
		AccountNamespace:       accountNamespace,
		AccountNamespaceSource: namespaceSource,
		CacheStatus:            "stored",
		CachePath:              cachePath,
		ReusedCache:            false,
	})
}

type transcribeOutputMeta struct {
	JobKey                 string
	AccountNamespace       string
	AccountNamespaceSource string
	CacheStatus            string
	CachePath              string
	ReusedCache            bool
}

func resolveAccountNamespace(ctx context.Context, provider transcription.Provider, apiKey string) (string, string, error) {
	if strings.TrimSpace(apiKey) == "" {
		return "", "", apperr.New(apperr.CodeAuth, "missing ElevenLabs API key")
	}
	userID, err := provider.UserID(ctx)
	if err == nil {
		return jobstore.UserNamespace(userID), "user", nil
	}
	if apperr.Code(err) == apperr.CodeConfig {
		return "", "", err
	}
	return jobstore.APIKeyNamespace(apiKey), "api_key_hash", nil
}

func buildTranscriptRequest(file *sdk.TranscriptFile, flags transcribeFlags, keyterms []string, diarize bool, speakers int, multiChannel bool) sdk.CreateTranscriptRequest {
	req := sdk.CreateTranscriptRequest{
		File:                  file,
		ModelID:               flags.model,
		LanguageCode:          flags.language,
		NumSpeakers:           speakers,
		Keyterms:              append([]string(nil), keyterms...),
		TagAudioEvents:        boolPtr(!flags.noAudioEvents),
		TimestampsGranularity: "word",
	}
	if diarize {
		req.Diarize = boolPtr(true)
	}
	if flags.clean {
		req.NoVerbatim = boolPtr(true)
	}
	if multiChannel {
		req.UseMultiChannel = boolPtr(true)
	}
	return req
}

func resolveTranscribeInput(flags transcribeFlags, audioPath string) (transcribeInput, error) {
	if len(flags.tracks) == 0 {
		audioSize, err := validateAudioPath(audioPath)
		if err != nil {
			return transcribeInput{}, err
		}
		speakerNames, err := collectSpeakerNames(flags.speakerNames, flags.speakerNamesFile)
		if err != nil {
			return transcribeInput{}, err
		}
		if err := validateSpeakerNames(speakerNames, flags.speakers); err != nil {
			return transcribeInput{}, err
		}
		audioHash, err := jobstore.FileSHA256(audioPath)
		if err != nil {
			return transcribeInput{}, err
		}
		return transcribeInput{
			SourcePath:        audioPath,
			SourceLabel:       audioPath,
			RenderSourceFile:  filepath.Base(audioPath),
			TitleSourcePath:   audioPath,
			DefaultOutputPath: defaultTranscriptPath(audioPath),
			UploadPath:        audioPath,
			UploadSize:        audioSize,
			AudioHash:         audioHash,
			SpeakerNames:      speakerNames,
		}, nil
	}

	tracks, err := collectTracks(flags.tracks, flags.trackOffsets, flags.trackMixdown)
	if err != nil {
		return transcribeInput{}, err
	}
	var audioHash string
	if flags.trackMixdown {
		audioHash, err = audiomerge.MixdownContentHash(tracks)
	} else {
		audioHash, err = audiomerge.ContentHash(tracks)
	}
	if err != nil {
		return transcribeInput{}, err
	}
	speakerNames := trackNames(tracks)
	if flags.trackMixdown && (len(flags.speakerNames) > 0 || flags.speakerNamesFile != "") {
		speakerNames, err = collectSpeakerNames(flags.speakerNames, flags.speakerNamesFile)
		if err != nil {
			return transcribeInput{}, err
		}
	}
	if flags.trackMixdown {
		if err := validateSpeakerNames(speakerNames, flags.speakers); err != nil {
			return transcribeInput{}, err
		}
	}
	return transcribeInput{
		SourceLabel:       trackSourceLabel(tracks),
		RenderSourceFile:  trackSourceLabel(tracks),
		TitleSourcePath:   tracks[0].Path,
		DefaultOutputPath: defaultTranscriptPath(tracks[0].Path),
		AudioHash:         audioHash,
		MultiChannel:      !flags.trackMixdown,
		TrackMixdown:      flags.trackMixdown,
		Tracks:            tracks,
		SpeakerNames:      speakerNames,
	}, nil
}

func buildRemoteRequest(flags transcribeFlags, keyterms []string, diarize bool, speakers int, multiChannel bool) jobstore.RemoteRequest {
	style := ""
	if multiChannel {
		style = "combined"
	}
	return jobstore.RemoteRequest{
		Model:                   flags.model,
		Language:                strings.TrimSpace(flags.language),
		Diarize:                 diarize,
		Speakers:                speakers,
		Keyterms:                append([]string(nil), keyterms...),
		Clean:                   flags.clean,
		TagAudioEvents:          !flags.noAudioEvents,
		TimestampsGranularity:   "word",
		UseMultiChannel:         multiChannel,
		MultichannelOutputStyle: style,
	}
}

func buildRenderRequest(titleSourcePath, sourceFile string, flags transcribeFlags, diarize bool, speakerNames []string) jobstore.RenderRequest {
	title := strings.TrimSuffix(filepath.Base(titleSourcePath), filepath.Ext(titleSourcePath))
	return jobstore.RenderRequest{
		Title:        title,
		SourceFile:   sourceFile,
		Model:        flags.model,
		Diarized:     diarize,
		Timestamps:   flags.timestamps,
		SpeakerNames: append([]string(nil), speakerNames...),
	}
}

func newJobRecord(jobKey, accountNamespace, baseURL, audioHash, requestHash string, remoteReq jobstore.RemoteRequest, renderReq jobstore.RenderRequest, sourcePath, outPath, rawOutPath string, inputTracks []jobstore.InputTrack) jobstore.Record {
	now := time.Now().UTC()
	return jobstore.Record{
		SchemaVersion:    jobstore.SchemaVersion,
		JobKey:           jobKey,
		Status:           jobstore.StatusPending,
		AccountNamespace: accountNamespace,
		BaseURL:          strings.TrimRight(baseURL, "/"),
		AudioSHA256:      audioHash,
		RequestHash:      requestHash,
		RemoteRequest:    remoteReq,
		RenderRequest:    renderReq,
		SourcePath:       sourcePath,
		OutputPath:       outPath,
		RawOutputPath:    rawOutPath,
		InputTracks:      inputTracks,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func collectTracks(trackSpecs, offsetSpecs []string, mixdown bool) ([]audiomerge.Track, error) {
	tracks := make([]audiomerge.Track, 0, len(trackSpecs))
	for _, spec := range trackSpecs {
		name, path, ok := strings.Cut(spec, "=")
		if !ok {
			return nil, apperr.New(apperr.CodeInvalidInput, "--track must use name=audio-file")
		}
		tracks = append(tracks, audiomerge.Track{Name: strings.TrimSpace(name), Path: strings.TrimSpace(path)})
	}

	offsets := map[string]time.Duration{}
	for _, spec := range offsetSpecs {
		name, raw, ok := strings.Cut(spec, "=")
		if !ok {
			return nil, apperr.New(apperr.CodeInvalidInput, "--track-offset must use name=duration")
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, apperr.New(apperr.CodeInvalidInput, "--track-offset speaker name cannot be empty")
		}
		if _, exists := offsets[name]; exists {
			return nil, apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("duplicate --track-offset for %q", name))
		}
		offset, err := parseTrackOffset(raw)
		if err != nil {
			return nil, err
		}
		offsets[name] = offset
	}

	seen := map[string]struct{}{}
	for i := range tracks {
		if _, ok := seen[tracks[i].Name]; ok {
			return nil, apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("duplicate --track speaker name %q", tracks[i].Name))
		}
		seen[tracks[i].Name] = struct{}{}
		if offset, ok := offsets[tracks[i].Name]; ok {
			tracks[i].Offset = offset
			delete(offsets, tracks[i].Name)
		}
	}
	for name := range offsets {
		return nil, apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("--track-offset references unknown track %q", name))
	}
	if mixdown {
		return audiomerge.ValidateMixdownTracks(tracks)
	}
	return audiomerge.ValidateTracks(tracks)
}

func parseTrackOffset(raw string) (time.Duration, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return 0, apperr.New(apperr.CodeInvalidInput, "--track-offset duration cannot be empty")
	}
	if d, err := time.ParseDuration(value); err == nil {
		return d, nil
	}
	seconds, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("invalid --track-offset duration %q", raw))
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func trackNames(tracks []audiomerge.Track) []string {
	names := make([]string, 0, len(tracks))
	for _, track := range tracks {
		names = append(names, track.Name)
	}
	return names
}

func trackSourceLabel(tracks []audiomerge.Track) string {
	parts := make([]string, 0, len(tracks))
	for _, track := range tracks {
		parts = append(parts, track.Name+"="+track.Path)
	}
	return strings.Join(parts, ", ")
}

func jobstoreInputTracks(tracks []audiomerge.Track) []jobstore.InputTrack {
	if len(tracks) == 0 {
		return nil
	}
	out := make([]jobstore.InputTrack, 0, len(tracks))
	for _, track := range tracks {
		out = append(out, jobstore.InputTrack{
			Name:        track.Name,
			Path:        track.Path,
			OffsetNanos: int64(track.Offset),
		})
	}
	return out
}

func writeTrackMap(w io.Writer, tracks []audiomerge.Track) {
	fmt.Fprintln(w, "Multichannel track map:")
	for i, track := range tracks {
		fmt.Fprintf(w, "Channel %d: %s  %s  offset %s\n", i, track.Name, track.Path, track.Offset.String())
	}
}

func writeTrackMixdownMap(w io.Writer, tracks []audiomerge.Track) {
	fmt.Fprintln(w, "Track mixdown map:")
	for i, track := range tracks {
		fmt.Fprintf(w, "Track %d: %s  %s  offset %s\n", i+1, track.Name, track.Path, track.Offset.String())
	}
}

func submitWebhookTranscription(ctx context.Context, opts *rootOptions, provider transcription.Provider, uploadPath string, transcribeReq sdk.CreateTranscriptRequest, audioSize int64, record jobstore.Record, meta transcribeOutputMeta) error {
	var progress *transcribeProgressPrinter
	if !opts.json {
		fmt.Fprintf(opts.errOut, "Uploading %s (%s) to ElevenLabs webhook...\n", uploadPath, formatBytes(audioSize))
		progress = newTranscribeProgressPrinter(opts.errOut)
	}
	transcribeReq.OnUploadProgress = progressCallback(progress)
	response, err := provider.SubmitTranscriptWebhook(ctx, transcribeReq)
	if progress != nil {
		progress.Stop()
	}
	if err != nil {
		if shouldRecordTranscribeFailure(err) {
			_, _ = saveFailedJob(record, err)
		}
		return err
	}
	if response == nil {
		return apperr.New(apperr.CodeAPI, "ElevenLabs webhook response was empty")
	}

	record.Status = jobstore.StatusSubmitted
	record.WebhookRequestID = response.RequestID
	if response.TranscriptionID != nil {
		record.TranscriptionID = *response.TranscriptionID
	}
	record.LastErrorCode = ""
	record.LastErrorMessage = ""
	cachePath, err := jobstore.Save(record)
	if err != nil {
		return err
	}
	meta.CachePath = cachePath

	data := map[string]any{
		"job_key":                  meta.JobKey,
		"account_namespace":        meta.AccountNamespace,
		"account_namespace_source": meta.AccountNamespaceSource,
		"cache_status":             meta.CacheStatus,
		"cache_path":               meta.CachePath,
		"reused_cache":             meta.ReusedCache,
		"webhook_submitted":        true,
		"request_id":               response.RequestID,
		"transcription_id":         optionalString(record.TranscriptionID),
		"output_path":              optionalString(record.OutputPath),
		"raw_output_path":          optionalString(record.RawOutputPath),
	}
	if opts.json {
		return output.JSONSuccess(opts.out, data)
	}
	fmt.Fprintf(opts.out, "Submitted transcription job %s\n", meta.JobKey)
	if record.TranscriptionID != "" {
		fmt.Fprintf(opts.out, "Transcription ID: %s\n", record.TranscriptionID)
	}
	fmt.Fprintf(opts.out, "Cache: %s\n", meta.CachePath)
	return nil
}

func shouldRecordTranscribeFailure(err error) bool {
	switch apperr.Code(err) {
	case apperr.CodeNetwork, apperr.CodeUnexpected:
		return false
	default:
		return true
	}
}

func saveFailedJob(record jobstore.Record, err error) (string, error) {
	record.Status = jobstore.StatusFailed
	record.LastErrorCode = apperr.Code(err)
	record.LastErrorMessage = output.Redact(apperr.Message(err))
	return jobstore.Save(record)
}

func transcriptFromRaw(raw []byte, source string) (*sdk.Transcript, []byte, error) {
	if len(raw) == 0 {
		return nil, nil, apperr.New(apperr.CodeFilesystem, fmt.Sprintf("cached transcript at %s is missing raw response; use --force to submit a new request", source))
	}
	var transcript sdk.Transcript
	if err := json.Unmarshal(raw, &transcript); err != nil {
		return nil, nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("cached transcript at %s is not valid transcript JSON; use --force to submit a new request", source), err)
	}
	return &transcript, append([]byte(nil), raw...), nil
}

func transcriptJSON(transcript *sdk.Transcript) ([]byte, error) {
	if transcript == nil {
		return nil, apperr.New(apperr.CodeAPI, "ElevenLabs transcript response was empty")
	}
	raw, err := json.Marshal(transcript)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeAPI, "could not encode transcript JSON", err)
	}
	return raw, nil
}

func writeTranscriptOutputs(opts *rootOptions, flags transcribeFlags, audioPath, outPath string, transcript *sdk.Transcript, raw []byte, renderReq jobstore.RenderRequest, meta transcribeOutputMeta) error {
	if !opts.json {
		if meta.ReusedCache {
			fmt.Fprintf(opts.errOut, "Reusing cached ElevenLabs transcript from %s\n", meta.CachePath)
		}
		fmt.Fprintln(opts.errOut, "Rendering Markdown transcript...")
	}
	md := render.Markdown(transcript, markdownOptionsFromRenderRequest(renderReq))
	if err := os.WriteFile(outPath, []byte(md), 0o644); err != nil {
		return apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not write transcript to %s", outPath), err)
	}
	if flags.rawOut != "" {
		rawWithNewline := append(append([]byte(nil), raw...), '\n')
		if err := os.WriteFile(flags.rawOut, rawWithNewline, 0o644); err != nil {
			return apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not write raw transcript to %s", flags.rawOut), err)
		}
	}

	data := map[string]any{
		"output_path":              outPath,
		"raw_output_path":          optionalString(flags.rawOut),
		"source_file":              audioPath,
		"model":                    renderReq.Model,
		"language_code":            transcript.LanguageCode,
		"transcription_id":         transcript.TranscriptionID,
		"job_key":                  meta.JobKey,
		"account_namespace":        meta.AccountNamespace,
		"account_namespace_source": meta.AccountNamespaceSource,
		"cache_status":             meta.CacheStatus,
		"cache_path":               meta.CachePath,
		"reused_cache":             meta.ReusedCache,
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

func markdownOptionsFromRenderRequest(req jobstore.RenderRequest) render.MarkdownOptions {
	return render.MarkdownOptions{
		Title:        req.Title,
		SourceFile:   req.SourceFile,
		Model:        req.Model,
		GeneratedAt:  time.Now().UTC(),
		Diarized:     req.Diarized,
		Timestamps:   req.Timestamps,
		SpeakerNames: append([]string(nil), req.SpeakerNames...),
	}
}

func newTranscriptsCommand(ctx context.Context, opts *rootOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "transcripts",
		Short: "Read or delete stored ElevenLabs transcripts",
	}
	cmd.AddCommand(newTranscriptGetCommand(ctx, opts))
	cmd.AddCommand(newTranscriptDeleteCommand(ctx, opts))
	cmd.AddCommand(newTranscriptLocateCommand(opts))
	cmd.AddCommand(newTranscriptImportWebhookCommand(opts))
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
			provider := elclient.NewClient(rt.BaseURL, rt.APIKey)
			transcript, err := provider.GetTranscript(ctx, args[0])
			if err != nil {
				return err
			}
			raw, err := transcriptJSON(transcript)
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
			provider := elclient.NewClient(rt.BaseURL, rt.APIKey)
			response, err := provider.DeleteTranscriptWithResponse(ctx, args[0])
			if err != nil {
				return err
			}
			data := map[string]any{
				"transcription_id": args[0],
				"deleted":          true,
			}
			if response != nil && response.Data != nil {
				data["response"] = response.Data
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

func newTranscriptLocateCommand(opts *rootOptions) *cobra.Command {
	var text string
	var jobKey string
	var clipOut string
	var force bool
	var limit int
	cmd := &cobra.Command{
		Use:   "locate <audio-or-transcript-file>",
		Short: "Find the audio timestamp for selected transcript text",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
				return apperr.New(apperr.CodeInvalidInput, "provide an audio or transcript path")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit <= 0 {
				return apperr.New(apperr.CodeInvalidInput, "--limit must be greater than 0")
			}
			query, err := readLocateText(opts.in, text)
			if err != nil {
				return err
			}
			entry, err := resolveLocateRecord(args[0], jobKey)
			if err != nil {
				return err
			}
			transcript, _, err := transcriptFromRaw(entry.Record.RawResponse, entry.Path)
			if err != nil {
				return err
			}
			result := locate.Find(transcript, query, limit)
			if !result.HasTimedWords {
				return apperr.New(apperr.CodeNotFound, fmt.Sprintf("cached transcript for job %s does not include word-level timing", entry.Record.JobKey))
			}
			if len(result.Matches) == 0 {
				return apperr.New(apperr.CodeNotFound, "could not locate selected text in cached transcript")
			}
			clip, err := writeLocateClip(entry.Record, result.Matches, clipOut, force)
			if err != nil {
				return err
			}

			data := map[string]any{
				"job_key":         entry.Record.JobKey,
				"cache_path":      entry.Path,
				"source_path":     optionalString(entry.Record.SourcePath),
				"output_path":     optionalString(entry.Record.OutputPath),
				"raw_output_path": optionalString(entry.Record.RawOutputPath),
				"query":           query,
				"matches":         result.Matches,
			}
			if clip != nil {
				data["clip"] = clip
			}
			if opts.json {
				return output.JSONSuccess(opts.out, data)
			}
			writeLocateHuman(opts.out, entry.Record, result.Matches, clip)
			return nil
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "selected transcript text to locate; use - to read stdin")
	cmd.Flags().StringVar(&jobKey, "job-key", "", "explicit local job key when path matching is ambiguous")
	cmd.Flags().StringVar(&clipOut, "clip-out", "", "write the matched audio segment to this path using ffmpeg stream copy")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing --clip-out file")
	cmd.Flags().IntVar(&limit, "limit", locate.DefaultLimit, "maximum number of matches to return")
	return cmd
}

func readLocateText(in io.Reader, text string) (string, error) {
	if strings.TrimSpace(text) == "" {
		return "", apperr.New(apperr.CodeInvalidInput, "provide --text with selected transcript text or --text - to read stdin")
	}
	if text == "-" {
		b, err := io.ReadAll(in)
		if err != nil {
			return "", apperr.Wrap(apperr.CodeFilesystem, "could not read selected transcript text from stdin", err)
		}
		text = string(b)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "", apperr.New(apperr.CodeInvalidInput, "selected transcript text cannot be empty")
	}
	return text, nil
}

func resolveLocateRecord(path, jobKey string) (jobstore.Entry, error) {
	jobKey = strings.TrimSpace(jobKey)
	if jobKey != "" {
		record, found, err := jobstore.Load(jobKey)
		if err != nil {
			return jobstore.Entry{}, err
		}
		if !found {
			return jobstore.Entry{}, apperr.New(apperr.CodeNotFound, fmt.Sprintf("no local job cache found for job %s", jobKey))
		}
		cachePath, err := jobstore.Path(jobKey)
		if err != nil {
			return jobstore.Entry{}, err
		}
		if record.Status != jobstore.StatusCompleted {
			return jobstore.Entry{}, apperr.New(apperr.CodeNotFound, fmt.Sprintf("job %s is %s, not completed", jobKey, record.Status))
		}
		return jobstore.Entry{Path: cachePath, Record: record}, nil
	}

	matches, err := jobstore.FindCompletedByPath(path)
	if err != nil {
		return jobstore.Entry{}, err
	}
	if len(matches) == 0 {
		return jobstore.Entry{}, apperr.New(apperr.CodeNotFound, fmt.Sprintf("no completed cached transcript found for %s; run transcribe first or pass --job-key", path))
	}
	if len(matches) > 1 {
		keys := make([]string, 0, len(matches))
		for _, match := range matches {
			keys = append(keys, match.Record.JobKey)
		}
		return jobstore.Entry{}, apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("%s matches multiple completed jobs (%s); pass --job-key", path, strings.Join(keys, ", ")))
	}
	return matches[0], nil
}

func writeLocateClip(record jobstore.Record, matches []locate.Match, clipOut string, force bool) (*audioclip.Result, error) {
	clipOut = strings.TrimSpace(clipOut)
	if clipOut == "" {
		return nil, nil
	}
	if len(matches) != 1 {
		return nil, apperr.New(apperr.CodeInvalidInput, "--clip-out requires exactly one located match; narrow the selection or pass --limit 1")
	}
	if strings.TrimSpace(record.SourcePath) == "" {
		return nil, apperr.New(apperr.CodeInvalidInput, fmt.Sprintf("cached job %s does not include a source audio path; cannot use --clip-out", record.JobKey))
	}
	match := matches[0]
	clip, err := audioclip.Copy(audioclip.Request{
		SourcePath:   record.SourcePath,
		OutputPath:   clipOut,
		StartSeconds: match.StartSeconds,
		EndSeconds:   match.EndSeconds,
		Force:        force,
	})
	if err != nil {
		return nil, err
	}
	return &clip, nil
}

func writeLocateHuman(w io.Writer, record jobstore.Record, matches []locate.Match, clip *audioclip.Result) {
	audioPath := firstNonEmptyString(record.SourcePath, record.OutputPath, record.RawOutputPath)
	if len(matches) == 1 {
		match := matches[0]
		fmt.Fprintf(w, "Timestamp: %s (%.3fs)\n", match.Timestamp, match.StartSeconds)
		fmt.Fprintf(w, "Audio: %s\n", audioPath)
		if clip != nil {
			fmt.Fprintf(w, "Clip: %s\n", clip.OutputPath)
		}
		fmt.Fprintf(w, "Match: %s\n", match.Text)
		if match.Context != "" {
			fmt.Fprintf(w, "Context: %s\n", match.Context)
		}
		return
	}

	fmt.Fprintf(w, "Audio: %s\n", audioPath)
	fmt.Fprintf(w, "Matches: %d\n", len(matches))
	for i, match := range matches {
		fmt.Fprintf(w, "%d. %s (%.3fs) %s\n", i+1, match.Timestamp, match.StartSeconds, match.Text)
		if match.Context != "" {
			fmt.Fprintf(w, "   Context: %s\n", match.Context)
		}
	}
}

func newTranscriptImportWebhookCommand(opts *rootOptions) *cobra.Command {
	var outPath string
	var rawOutPath string
	var force bool
	cmd := &cobra.Command{
		Use:   "import-webhook <file|->",
		Short: "Import an ElevenLabs speech-to-text webhook payload into the local job cache",
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 || strings.TrimSpace(args[0]) == "" {
				return apperr.New(apperr.CodeInvalidInput, "provide a webhook payload file or - for stdin")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, err := readPayloadArg(opts.in, args[0])
			if err != nil {
				return err
			}
			jobKey, transcriptRaw, err := parseWebhookPayload(payload)
			if err != nil {
				return err
			}
			record, found, err := jobstore.Load(jobKey)
			if err != nil {
				return err
			}
			if !found {
				return apperr.New(apperr.CodeNotFound, fmt.Sprintf("no local job cache found for webhook job %s", jobKey))
			}
			cachePath, err := jobstore.Path(jobKey)
			if err != nil {
				return err
			}
			transcript, raw, err := transcriptFromRaw(transcriptRaw, "webhook payload")
			if err != nil {
				return err
			}

			targetOut := firstNonEmptyString(outPath, record.OutputPath)
			targetRawOut := firstNonEmptyString(rawOutPath, record.RawOutputPath)
			if err := ensureWritableTarget(targetOut, force); err != nil {
				return err
			}
			if err := ensureWritableTarget(targetRawOut, force); err != nil {
				return err
			}

			record.Status = jobstore.StatusCompleted
			record.RawResponse = append([]byte(nil), raw...)
			if transcript.TranscriptionID != "" {
				record.TranscriptionID = transcript.TranscriptionID
			}
			record.LastErrorCode = ""
			record.LastErrorMessage = ""
			cachePath, err = jobstore.Save(record)
			if err != nil {
				return err
			}

			if targetOut != "" {
				renderReq := record.RenderRequest
				if renderReq.Model == "" {
					renderReq.Model = record.RemoteRequest.Model
				}
				md := render.Markdown(transcript, markdownOptionsFromRenderRequest(renderReq))
				if err := os.WriteFile(targetOut, []byte(md), 0o644); err != nil {
					return apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not write transcript to %s", targetOut), err)
				}
			}
			if targetRawOut != "" {
				rawWithNewline := append(append([]byte(nil), raw...), '\n')
				if err := os.WriteFile(targetRawOut, rawWithNewline, 0o644); err != nil {
					return apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not write raw transcript to %s", targetRawOut), err)
				}
			}

			data := map[string]any{
				"job_key":          jobKey,
				"cache_path":       cachePath,
				"cache_status":     jobstore.StatusCompleted,
				"output_path":      optionalString(targetOut),
				"raw_output_path":  optionalString(targetRawOut),
				"transcription_id": record.TranscriptionID,
			}
			if opts.json {
				return output.JSONSuccess(opts.out, data)
			}
			fmt.Fprintf(opts.out, "Imported webhook transcript for job %s\n", jobKey)
			if targetOut != "" {
				fmt.Fprintf(opts.out, "Wrote %s\n", targetOut)
			}
			if targetRawOut != "" {
				fmt.Fprintf(opts.out, "Wrote %s\n", targetRawOut)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&outPath, "out", "", "write Markdown transcript to this path")
	cmd.Flags().StringVar(&rawOutPath, "raw-out", "", "write raw transcript JSON to this path")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite existing output files")
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
			client := elclient.NewClient(rt.BaseURL, rt.APIKey)
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
	if len(flags.trackOffsets) > 0 && len(flags.tracks) == 0 {
		return apperr.New(apperr.CodeInvalidInput, "--track-offset requires --track")
	}
	if flags.trackMixdown && len(flags.tracks) == 0 {
		return apperr.New(apperr.CodeInvalidInput, "--track-mixdown requires --track")
	}
	if len(flags.tracks) > 0 {
		if !flags.trackMixdown && flags.diarize {
			return apperr.New(apperr.CodeInvalidInput, "--diarize cannot be used with --track")
		}
		if !flags.trackMixdown && flags.speakers > 0 {
			return apperr.New(apperr.CodeInvalidInput, "--speakers cannot be used with --track")
		}
		if !flags.trackMixdown && (len(flags.speakerNames) > 0 || flags.speakerNamesFile != "") {
			return apperr.New(apperr.CodeInvalidInput, "--speaker-name and --speaker-names-file cannot be used with --track; use the --track names instead")
		}
	}
	if flags.speakers < 0 || flags.speakers > 32 {
		return apperr.New(apperr.CodeInvalidInput, "--speakers must be between 1 and 32")
	}
	if flags.clean && flags.model != "scribe_v2" {
		return apperr.New(apperr.CodeInvalidInput, "--clean is only supported with scribe_v2")
	}
	if flags.webhookID != "" && !flags.webhook {
		return apperr.New(apperr.CodeInvalidInput, "--webhook-id requires --webhook")
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

func collectSpeakerNames(flagNames []string, filePath string) ([]string, error) {
	names := make([]string, 0, len(flagNames))
	if filePath != "" {
		b, err := os.ReadFile(filePath)
		if err != nil {
			return nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not read speaker names file %s", filePath), err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			names = append(names, line)
		}
	}
	for _, name := range flagNames {
		if strings.ContainsAny(name, "\r\n") {
			return nil, apperr.New(apperr.CodeInvalidInput, "--speaker-name cannot contain newlines")
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, apperr.New(apperr.CodeInvalidInput, "--speaker-name cannot be empty")
		}
		names = append(names, name)
	}
	return names, nil
}

func validateSpeakerNames(names []string, speakers int) error {
	if len(names) > 32 {
		return apperr.New(apperr.CodeInvalidInput, "speaker names cannot exceed 32 entries")
	}
	if speakers > 0 && len(names) > speakers {
		return apperr.New(apperr.CodeInvalidInput, "--speakers must be at least the number of speaker names")
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

func boolPtr(value bool) *bool {
	return &value
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func readPayloadArg(in io.Reader, arg string) ([]byte, error) {
	if arg == "-" {
		b, err := io.ReadAll(in)
		if err != nil {
			return nil, apperr.Wrap(apperr.CodeFilesystem, "could not read webhook payload from stdin", err)
		}
		return b, nil
	}
	b, err := os.ReadFile(arg)
	if err != nil {
		return nil, apperr.Wrap(apperr.CodeFilesystem, fmt.Sprintf("could not read webhook payload file %s", arg), err)
	}
	return b, nil
}

func parseWebhookPayload(payload []byte) (string, []byte, error) {
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		return "", nil, apperr.Wrap(apperr.CodeInvalidInput, "webhook payload is not valid JSON", err)
	}
	jobKey := findStringField(value, "podscribe_job_key")
	if jobKey == "" {
		return "", nil, apperr.New(apperr.CodeInvalidInput, "webhook payload does not include podscribe_job_key metadata")
	}
	raw, ok := findTranscriptObject(value)
	if !ok {
		return "", nil, apperr.New(apperr.CodeInvalidInput, "webhook payload does not include a transcript object")
	}
	return jobKey, raw, nil
}

func findStringField(value any, key string) string {
	switch v := value.(type) {
	case map[string]any:
		for name, item := range v {
			if name == key {
				if s, ok := item.(string); ok {
					return strings.TrimSpace(s)
				}
			}
			if found := findStringField(item, key); found != "" {
				return found
			}
		}
	case []any:
		for _, item := range v {
			if found := findStringField(item, key); found != "" {
				return found
			}
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if strings.HasPrefix(trimmed, "{") {
			var nested any
			if err := json.Unmarshal([]byte(trimmed), &nested); err == nil {
				return findStringField(nested, key)
			}
		}
	}
	return ""
}

func findTranscriptObject(value any) ([]byte, bool) {
	if looksLikeTranscript(value) {
		raw, err := json.Marshal(value)
		return raw, err == nil
	}
	switch v := value.(type) {
	case map[string]any:
		for _, key := range []string{"transcript", "transcription", "data", "result"} {
			if item, ok := v[key]; ok {
				if raw, ok := findTranscriptObject(item); ok {
					return raw, true
				}
			}
		}
		for _, item := range v {
			if raw, ok := findTranscriptObject(item); ok {
				return raw, true
			}
		}
	case []any:
		for _, item := range v {
			if raw, ok := findTranscriptObject(item); ok {
				return raw, true
			}
		}
	}
	return nil, false
}

func looksLikeTranscript(value any) bool {
	v, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for _, key := range []string{"language_code", "text", "words", "transcripts"} {
		if _, ok := v[key]; ok {
			return true
		}
	}
	return false
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

func progressCallback(progress *transcribeProgressPrinter) func(sdk.TranscriptUploadProgress) {
	if progress == nil {
		return nil
	}
	return progress.ReportUpload
}

func (p *transcribeProgressPrinter) ReportUpload(progress sdk.TranscriptUploadProgress) {
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

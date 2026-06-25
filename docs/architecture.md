# Architecture

`podscribe` keeps the command surface small and the implementation testable.

- `cmd/podscribe` is only process entrypoint glue.
- `internal/cli` owns Cobra commands, flag validation, progress, and file writes.
- `internal/config` owns config paths, auth precedence, and base URL resolution.
- `internal/elevenlabs` owns request construction, streamed multipart upload, API errors, and typed transcript responses.
- `internal/jobstore` owns local resume/idempotency records, audio/request hashing, and atomic cache writes.
- `internal/render` turns ElevenLabs word timing into editable Markdown transcript blocks.
- `internal/output` owns the JSON envelope and secret redaction policy.

The transcription upload uses `io.Pipe` with `multipart.Writer` so long podcast files are streamed to the HTTP request instead of buffered in memory. The ElevenLabs client reports file-byte progress through a callback, and the CLI renders that progress only on stderr so JSON stdout remains parseable.

Resume and idempotency are client-side. Before a transcription upload, the CLI resolves the ElevenLabs `user_id`, hashes the audio file and remote request options, and looks up `~/.podscribe/jobs/v1/<job-key>.json`. Completed jobs are rendered from cached raw JSON. Pending or submitted jobs block automatic resubmission unless `--force` is set, because ElevenLabs does not expose historical partial batch transcripts and a previous upload may already have been accepted.

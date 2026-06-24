# Architecture

`podscribe` keeps the command surface small and the implementation testable.

- `cmd/podscribe` is only process entrypoint glue.
- `internal/cli` owns Cobra commands, flag validation, progress, and file writes.
- `internal/config` owns config paths, auth precedence, and base URL resolution.
- `internal/elevenlabs` owns request construction, streamed multipart upload, API errors, and typed transcript responses.
- `internal/render` turns ElevenLabs word timing into editable Markdown transcript blocks.
- `internal/output` owns the JSON envelope and secret redaction policy.

The transcription upload uses `io.Pipe` with `multipart.Writer` so long podcast files are streamed to the HTTP request instead of buffered in memory.

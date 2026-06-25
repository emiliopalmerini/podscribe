# Repository Guidelines

## Project Structure & Module Organization

`podscribe` is a Go CLI for transcribing podcast audio with ElevenLabs. The executable entrypoint is `cmd/podscribe/main.go`; most behavior lives under `internal/`. Key packages are `internal/cli` for Cobra commands and file output, `internal/config` for auth and local config, `internal/elevenlabs` for API requests and response types, `internal/jobstore` for transcript cache/resume records, `internal/render` for Markdown transcript output, and `internal/output` for JSON envelopes. Architecture notes live in `docs/architecture.md`. Generated binaries and release artifacts belong in `bin/` or `dist/` and are ignored.

## Build, Test, and Development Commands

- `make build`: builds `bin/podscribe` from `./cmd/podscribe`.
- `make run ARGS='doctor'`: runs the CLI locally with optional arguments.
- `make test`: runs `go test ./...`.
- `make vet`: runs `go vet ./...`.
- `make fmt`: applies `gofmt` to all non-vendor Go files.
- `make check`: runs formatting, vetting, and tests.
- `nix flake check`: validates the Nix package/check output.
- `goreleaser release --snapshot --clean`: checks release packaging locally.

## Coding Style & Naming Conventions

Use standard Go formatting with tabs via `gofmt`; do not hand-format alignment. Keep package names short and lowercase. Prefer small functions with explicit errors over hidden process exits, especially inside `internal/` packages. CLI-facing errors should preserve machine-readable JSON behavior and redact secrets. Keep stdout parseable for `--json`; progress and diagnostics belong on stderr.

## Testing Guidelines

Tests use Go's standard `testing` package and live beside source files as `*_test.go`. Name tests by behavior, for example `TestDoctorJSONWithoutAuth`. Use `httptest`, `t.TempDir`, and `t.Setenv` for isolated CLI and API scenarios. CI does not make live ElevenLabs calls, so cover API interactions with local test servers and fixtures. Run `make check` before opening a PR.

## Commit & Pull Request Guidelines

Recent history uses short, imperative, sentence-case subjects such as `Add transcription resume cache` and `Retry ElevenLabs rate limits`. Keep commits focused and include tests or docs with behavior changes. Pull requests should describe user-visible changes, validation commands run, related issues, and any release or config impact. Include CLI output examples when changing command behavior or JSON contracts.

## Security & Configuration Tips

Never commit `.env`, API keys, transcripts with sensitive content, or local cache data. Use `.env.example` for documenting variables and prefer `ELEVENLABS_API_KEY` or `--api-key` for local testing. Check new errors and logs for accidental secret exposure.

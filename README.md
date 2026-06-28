# podscribe

`podscribe` is a small Go CLI for transcribing podcast audio with the ElevenLabs Speech to Text API.

## Install

With Go:

```bash
go install github.com/emiliopalmerini/podscribe/cmd/podscribe@latest
```

With Nix:

```bash
nix run github:emiliopalmerini/podscribe -- doctor
nix profile install github:emiliopalmerini/podscribe
```

Prebuilt release archives for macOS, Linux, and Windows are published from tagged GitHub releases. Download the archive for your platform from the releases page and verify it with the published SHA-256 checksum file.

## Configure

The CLI reads auth in this order:

1. `--api-key`
2. `ELEVENLABS_API_KEY`
3. `~/.podscribe/config.json`

Environment variables are usually the safest day-to-day option:

```bash
export ELEVENLABS_API_KEY=...
podscribe doctor
```

Or store local config:

```bash
podscribe init --api-key ...
```

`--base-url` can point at ElevenLabs residency endpoints when needed. The default is `https://api.elevenlabs.io`.

## Usage

Transcribe a local audio file:

```bash
podscribe transcribe episode.mp3
```

By default, `podscribe` writes `episode.transcript.md` next to the audio file and refuses to overwrite existing files unless `--force` is set.

Human-readable runs print upload progress to stderr, then keep reporting that the command is waiting for the ElevenLabs transcript response if server-side processing takes a while.

Completed transcription results are cached in `~/.podscribe/jobs/v1` and automatically reused for identical audio, ElevenLabs user, base URL, and remote transcription options. Local-only options such as `--out`, `--raw-out`, `--timestamps`, and `--speaker-name` can re-render from the cached raw transcript without a new ElevenLabs request. Pass `--force` to bypass the cache and submit a new request.

Pass `--timestamps` to prefix transcript blocks with `[hh:mm:ss]` timestamps.

Useful podcast flags:

```bash
podscribe transcribe episode.mp3 \
  --diarize \
  --speakers 2 \
  --speaker-name "Emilio Palmerini" \
  --speaker-name "Guest" \
  --keyterm "Emilio Palmerini" \
  --keyterms-file terms.txt \
  --clean
```

Speaker names are assigned by first detected speaker order and rendered in Markdown instead of generic labels. You can also keep recurring names in a file:

```bash
podscribe transcribe episode.mp3 --speaker-names-file speakers.txt
```

`speakers.txt` uses one name per line. Blank lines and lines starting with `#` are ignored. Speaker names imply diarization; when `--speakers` is omitted, podscribe sends the number of supplied names as the speaker count.

If each podcast speaker is recorded to a separate file that starts from the same timeline, merge those files into one ElevenLabs multichannel upload with repeated `--track` flags:

```bash
podscribe transcribe \
  --track "Emilio=emilio.wav" \
  --track "Guest=guest.wav" \
  --out episode.transcript.md
```

By default, track names become channel labels in the transcript. Track mode works best with isolated speaker tracks; Combo tracks that include the full call or heavy bleed can produce duplicated text across channels. Use `--track-offset` when a file starts late or early:

```bash
podscribe transcribe \
  --track "Emilio=emilio.wav" \
  --track "Guest=guest.wav" \
  --track-offset "Guest=1.42s"
```

Positive offsets add leading silence; negative offsets trim from the start. Default track mode requires `ffmpeg` and `ffprobe`, supports two to five tracks, and uploads a temporary multichannel FLAC with ElevenLabs `use_multi_channel=true` and `multichannel_output_style=combined`.

If the source files are Combo tracks or otherwise not isolated, mix them into one temporary FLAC instead of preserving separate channels:

```bash
podscribe transcribe \
  --track "Emilio=emilio.wav" \
  --track "Guest=guest.wav" \
  --track-mixdown \
  --out episode.transcript.md
```

In mixdown mode, track names are used as default diarization speaker names, and you can override them with `--speaker-name` or `--speaker-names-file`. Mixdown mode uploads normal single-audio transcription without ElevenLabs multichannel fields and supports two to thirty-two tracks.

Save the raw ElevenLabs JSON alongside the Markdown:

```bash
podscribe transcribe episode.mp3 --raw-out episode.elevenlabs.json
```

Submit asynchronously to a configured ElevenLabs speech-to-text webhook:

```bash
podscribe transcribe episode.mp3 --webhook --webhook-id <webhook-id>
```

When the webhook payload arrives, import it into the local cache and optionally render outputs:

```bash
podscribe transcripts import-webhook payload.json \
  --out episode.transcript.md \
  --raw-out episode.elevenlabs.json
```

Fetch or delete stored transcripts:

```bash
podscribe transcripts get <transcription-id> --out transcript.json
podscribe transcripts delete <transcription-id> --yes
```

Locate selected transcript text in the cached word timings:

```bash
podscribe transcripts locate episode.transcript.md --text "Welcome back"
cat selected.txt | podscribe transcripts locate episode.mp3 --text -
podscribe transcripts locate episode.mp3 --text "Welcome back" --clip-out welcome.mp3
```

The lookup is local and uses completed jobs in `~/.podscribe/jobs/v1`; pass `--job-key` if a path matches more than one cached job.
`--clip-out` writes a single matched audio segment with `ffmpeg -c copy`; install `ffmpeg` and pass `--limit 1` or narrow the selected text if the lookup returns multiple matches. Existing clip files are not overwritten unless `--force` is set.

Run a read-only raw API request:

```bash
podscribe request get /v1/models
```

## JSON Contract

Pass `--json` to keep stdout machine-readable. Progress and diagnostics go to stderr.

Success:

```json
{
  "ok": true,
  "data": {
    "output_path": "episode.transcript.md"
  }
}
```

Error:

```json
{
  "ok": false,
  "error": {
    "code": "invalid_input",
    "message": "provide exactly one audio file"
  }
}
```

Secrets are redacted from error messages.

## Markdown Output

Markdown transcripts use YAML front matter followed by editable transcript blocks:

```markdown
---
title: "episode"
source_file: "episode.mp3"
provider: "elevenlabs"
model: "scribe_v2"
language_code: "en"
diarized: true
generated_at: "2026-06-24T10:00:00Z"
---

# episode

## Transcript

Speaker 1: Welcome back.
```

Speaker labels are emitted when diarization is requested or speaker IDs are present in the response; provided names replace the generated labels when speaker IDs are available. Timestamps are emitted only when `--timestamps` is set.

## Development

Use the Nix development shell for pinned local tooling:

```bash
nix develop
```

Or use the local Go toolchain directly:

```bash
make build
make test
make vet
make check
```

`make check` formats Go files, runs `go vet`, and runs the full test suite. `nix flake check` validates the Nix package/check output.

No live ElevenLabs calls run in CI. Use `podscribe doctor` and a small local audio file for manual smoke testing.

## Contributing

Read [AGENTS.md](AGENTS.md) for the repository layout, local workflow, testing expectations, JSON/stdout rules, and security guidance before opening a PR.

## License

podscribe is released under the [MIT License](LICENSE).

## Release

Releases are tag-driven. To publish a release:

```bash
git tag v0.1.0
git push origin v0.1.0
```

The release workflow runs tests, `go vet`, builds cross-platform archives with GoReleaser, stamps the CLI version from the tag, and publishes SHA-256 checksums.

Before pushing a tag, validate release packaging locally:

```bash
goreleaser check
goreleaser release --snapshot --clean
nix flake check
```

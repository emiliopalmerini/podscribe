# podscribe

`podscribe` is a small Go CLI for transcribing podcast audio with the ElevenLabs Speech to Text API.

## Install

```bash
go install github.com/emiliopalmerini/podscribe/cmd/podscribe@latest
```

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

```bash
go test ./...
go vet ./...
go build ./cmd/podscribe
```

No live ElevenLabs calls run in CI. Use `podscribe doctor` and a small local audio file for manual smoke testing.

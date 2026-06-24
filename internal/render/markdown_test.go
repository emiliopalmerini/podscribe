package render

import (
	"strings"
	"testing"
	"time"

	"github.com/emiliopalmerini/podscribe/internal/elevenlabs"
)

func TestMarkdownRendersDiarizedTurns(t *testing.T) {
	start0, end0 := 1.2, 1.7
	start1, end1 := 2.0, 2.4
	start2, end2 := 6.0, 6.3
	duration := 10.0

	got := Markdown(elevenlabs.TranscriptResponse{
		LanguageCode:        "en",
		LanguageProbability: 0.98,
		TranscriptionID:     "tx_123",
		AudioDurationSecs:   &duration,
		Words: []elevenlabs.Word{
			{Text: "Hello", Type: "word", Start: &start0, End: &end0, SpeakerID: "speaker_0"},
			{Text: "world.", Type: "word", Start: &start1, End: &end1, SpeakerID: "speaker_0"},
			{Text: "Thanks!", Type: "word", Start: &start2, End: &end2, SpeakerID: "speaker_1"},
		},
	}, MarkdownOptions{
		Title:       "Episode 1",
		SourceFile:  "episode.mp3",
		Model:       "scribe_v2",
		GeneratedAt: time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC),
		Diarized:    true,
	})

	for _, want := range []string{
		`title: "Episode 1"`,
		`source_file: "episode.mp3"`,
		`transcription_id: "tx_123"`,
		"diarized: true",
		"# Episode 1",
		"[00:00:01] Speaker 1: Hello world.",
		"[00:00:06] Speaker 2: Thanks!",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Markdown() missing %q\n%s", want, got)
		}
	}
}

func TestMarkdownFallsBackToPlainText(t *testing.T) {
	got := Markdown(elevenlabs.TranscriptResponse{
		LanguageCode: "en",
		Text:         "A simple transcript.",
	}, MarkdownOptions{Title: "Plain", SourceFile: "plain.mp3", Model: "scribe_v2"})

	if !strings.Contains(got, "A simple transcript.") {
		t.Fatalf("Markdown() did not include plain text:\n%s", got)
	}
	if strings.Contains(got, "Speaker 1") {
		t.Fatalf("Markdown() unexpectedly included speaker labels:\n%s", got)
	}
}

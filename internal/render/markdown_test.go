package render

import (
	"strings"
	"testing"
	"time"

	"github.com/emiliopalmerini/podscribe/internal/transcription"
)

func TestMarkdownRendersDiarizedTurns(t *testing.T) {
	start0, end0 := 1.2, 1.7
	start1, end1 := 2.0, 2.4
	start2, end2 := 6.0, 6.3
	duration := 10.0

	got := Markdown(transcription.Transcript{
		LanguageCode:        "en",
		LanguageProbability: 0.98,
		TranscriptionID:     "tx_123",
		AudioDurationSecs:   &duration,
		Words: []transcription.Word{
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
		"Speaker 1: Hello world.",
		"Speaker 2: Thanks!",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Markdown() missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "[00:00:") {
		t.Fatalf("Markdown() included timestamps by default:\n%s", got)
	}
}

func TestMarkdownRendersTimestampsWhenRequested(t *testing.T) {
	start, end := 61.2, 61.7

	got := Markdown(transcription.Transcript{
		LanguageCode:    "en",
		TranscriptionID: "tx_123",
		Words: []transcription.Word{
			{Text: "Hello.", Type: "word", Start: &start, End: &end, SpeakerID: "speaker_0"},
		},
	}, MarkdownOptions{
		Title:       "Episode 1",
		SourceFile:  "episode.mp3",
		Model:       "scribe_v2",
		GeneratedAt: time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC),
		Diarized:    true,
		Timestamps:  true,
	})

	want := "[00:01:01] Speaker 1: Hello."
	if !strings.Contains(got, want) {
		t.Fatalf("Markdown() missing %q\n%s", want, got)
	}
}

func TestMarkdownRendersNamedSpeakers(t *testing.T) {
	start0, end0 := 1.2, 1.7
	start1, end1 := 2.0, 2.4

	got := Markdown(transcription.Transcript{
		LanguageCode:    "en",
		TranscriptionID: "tx_123",
		Words: []transcription.Word{
			{Text: "Hello.", Type: "word", Start: &start0, End: &end0, SpeakerID: "speaker_0"},
			{Text: "Thanks!", Type: "word", Start: &start1, End: &end1, SpeakerID: "speaker_1"},
		},
	}, MarkdownOptions{
		Title:        "Episode 1",
		SourceFile:   "episode.mp3",
		Model:        "scribe_v2",
		GeneratedAt:  time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC),
		Diarized:     true,
		SpeakerNames: []string{"Emilio Palmerini", "Guest"},
	})

	for _, want := range []string{
		"Emilio Palmerini: Hello.",
		"Guest: Thanks!",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Markdown() missing %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "Speaker 1") || strings.Contains(got, "Speaker 2") {
		t.Fatalf("Markdown() included fallback speaker labels:\n%s", got)
	}
}

func TestMarkdownFallsBackForUnnamedSpeakers(t *testing.T) {
	start0, end0 := 1.2, 1.7
	start1, end1 := 2.0, 2.4

	got := Markdown(transcription.Transcript{
		LanguageCode:    "en",
		TranscriptionID: "tx_123",
		Words: []transcription.Word{
			{Text: "Hello.", Type: "word", Start: &start0, End: &end0, SpeakerID: "speaker_0"},
			{Text: "Thanks!", Type: "word", Start: &start1, End: &end1, SpeakerID: "speaker_1"},
		},
	}, MarkdownOptions{
		Title:        "Episode 1",
		SourceFile:   "episode.mp3",
		Model:        "scribe_v2",
		GeneratedAt:  time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC),
		Diarized:     true,
		SpeakerNames: []string{"Emilio"},
	})

	for _, want := range []string{
		"Emilio: Hello.",
		"Speaker 2: Thanks!",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Markdown() missing %q\n%s", want, got)
		}
	}
}

func TestMarkdownRendersNamedSpeakerWithTimestamps(t *testing.T) {
	start, end := 61.2, 61.7

	got := Markdown(transcription.Transcript{
		LanguageCode:    "en",
		TranscriptionID: "tx_123",
		Words: []transcription.Word{
			{Text: "Hello.", Type: "word", Start: &start, End: &end, SpeakerID: "speaker_0"},
		},
	}, MarkdownOptions{
		Title:        "Episode 1",
		SourceFile:   "episode.mp3",
		Model:        "scribe_v2",
		GeneratedAt:  time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC),
		Diarized:     true,
		Timestamps:   true,
		SpeakerNames: []string{"Emilio"},
	})

	want := "[00:01:01] Emilio: Hello."
	if !strings.Contains(got, want) {
		t.Fatalf("Markdown() missing %q\n%s", want, got)
	}
}

func TestMarkdownKeepsSpeakerLabelsStableAcrossChunks(t *testing.T) {
	start0, end0 := 1.2, 1.7
	start1, end1 := 2.0, 2.4
	channel0, channel1 := 0, 1

	got := Markdown(transcription.Transcript{
		Transcripts: []transcription.Transcript{
			{
				LanguageCode: "en",
				ChannelIndex: &channel0,
				Words: []transcription.Word{
					{Text: "Hello.", Type: "word", Start: &start0, End: &end0, SpeakerID: "speaker_0"},
				},
			},
			{
				LanguageCode: "en",
				ChannelIndex: &channel1,
				Words: []transcription.Word{
					{Text: "Thanks!", Type: "word", Start: &start1, End: &end1, SpeakerID: "speaker_1"},
				},
			},
		},
	}, MarkdownOptions{
		Title:        "Episode 1",
		SourceFile:   "episode.mp3",
		Model:        "scribe_v2",
		GeneratedAt:  time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC),
		Diarized:     true,
		SpeakerNames: []string{"Emilio", "Guest"},
	})

	for _, want := range []string{
		"### Channel 0",
		"Emilio: Hello.",
		"### Channel 1",
		"Guest: Thanks!",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Markdown() missing %q\n%s", want, got)
		}
	}
}

func TestMarkdownRendersCombinedMultichannelWordsWithTrackNames(t *testing.T) {
	start0, end0 := 1.0, 1.2
	start1, end1 := 1.5, 1.8
	start2, end2 := 3.0, 3.2
	channel0, channel1 := 0, 1

	got := Markdown(transcription.Transcript{
		LanguageCode:    "en",
		TranscriptionID: "tx_123",
		Words: []transcription.Word{
			{Text: "Hello.", Type: "word", Start: &start0, End: &end0, ChannelIndex: &channel0},
			{Text: "Thanks!", Type: "word", Start: &start1, End: &end1, ChannelIndex: &channel1},
			{Text: "Again.", Type: "word", Start: &start2, End: &end2, ChannelIndex: &channel0},
		},
	}, MarkdownOptions{
		Title:        "Episode 1",
		SourceFile:   "episode.flac",
		Model:        "scribe_v2",
		GeneratedAt:  time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC),
		SpeakerNames: []string{"Emilio", "Guest"},
	})

	for _, want := range []string{
		"diarized: true",
		"Emilio: Hello.",
		"Guest: Thanks!",
		"Emilio: Again.",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Markdown() missing %q\n%s", want, got)
		}
	}
}

func TestMarkdownFallsBackToPlainText(t *testing.T) {
	got := Markdown(transcription.Transcript{
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

package locate

import (
	"strings"
	"testing"

	"github.com/emiliopalmerini/podscribe/internal/elevenlabs"
)

func TestFindMatchesSelectedTextWithCopiedPrefixes(t *testing.T) {
	start0, end0 := 1.2, 1.4
	start1, end1 := 1.5, 1.8
	start2, end2 := 2.0, 2.3
	start3, end3 := 2.4, 2.7

	resp := elevenlabs.TranscriptResponse{
		Words: []elevenlabs.Word{
			{Text: "Before", Type: "word", Start: &start0, End: &end0},
			{Text: "Hello,", Type: "word", Start: &start1, End: &end1},
			{Text: "world.", Type: "word", Start: &start2, End: &end2},
			{Text: "After", Type: "word", Start: &start3, End: &end3},
		},
	}

	result := Find(resp, "[00:00:01] Speaker 1: hello world", DefaultLimit)
	if !result.HasTimedWords {
		t.Fatal("Find() HasTimedWords = false, want true")
	}
	if len(result.Matches) != 1 {
		t.Fatalf("Find() matches = %d, want 1: %+v", len(result.Matches), result.Matches)
	}
	match := result.Matches[0]
	if match.Timestamp != "00:00:01.500" || match.StartSeconds != 1.5 || match.EndSeconds != 2.3 {
		t.Fatalf("match timing = %+v, want 1.5-2.3", match)
	}
	if match.Text != "Hello, world." {
		t.Fatalf("match text = %q, want formatted phrase", match.Text)
	}
	if !strings.Contains(match.Context, "Before Hello, world. After") {
		t.Fatalf("match context = %q, want surrounding words", match.Context)
	}
}

func TestFindMatchesMultilineSelection(t *testing.T) {
	start0, end0 := 61.2, 61.5
	start1, end1 := 61.6, 61.9
	start2, end2 := 62.0, 62.3

	resp := elevenlabs.TranscriptResponse{
		Words: []elevenlabs.Word{
			{Text: "First", Type: "word", Start: &start0, End: &end0},
			{Text: "line.", Type: "word", Start: &start1, End: &end1},
			{Text: "Second!", Type: "word", Start: &start2, End: &end2},
		},
	}

	result := Find(resp, "[00:01:01] Emilio: first line.\n[00:01:02] Guest: second", DefaultLimit)
	if len(result.Matches) != 1 {
		t.Fatalf("Find() matches = %d, want 1: %+v", len(result.Matches), result.Matches)
	}
	if got := result.Matches[0].Timestamp; got != "00:01:01.200" {
		t.Fatalf("timestamp = %q, want 00:01:01.200", got)
	}
}

func TestFindReturnsRepeatedMatchesInTimeOrderWithLimit(t *testing.T) {
	start0, end0 := 1.0, 1.2
	start1, end1 := 2.0, 2.2
	start2, end2 := 3.0, 3.2
	channel0, channel1 := 0, 1

	resp := elevenlabs.TranscriptResponse{
		Transcripts: []elevenlabs.TranscriptChunk{
			{
				ChannelIndex: &channel1,
				Words: []elevenlabs.Word{
					{Text: "again", Type: "word", Start: &start2, End: &end2},
				},
			},
			{
				ChannelIndex: &channel0,
				Words: []elevenlabs.Word{
					{Text: "again", Type: "word", Start: &start0, End: &end0},
					{Text: "again", Type: "word", Start: &start1, End: &end1},
				},
			},
		},
	}

	result := Find(resp, "again", 2)
	if len(result.Matches) != 2 {
		t.Fatalf("Find() matches = %d, want limited 2: %+v", len(result.Matches), result.Matches)
	}
	if result.Matches[0].StartSeconds != 1.0 || result.Matches[1].StartSeconds != 2.0 {
		t.Fatalf("matches are not sorted by time: %+v", result.Matches)
	}
	if result.Matches[0].ChannelIndex == nil || *result.Matches[0].ChannelIndex != 0 {
		t.Fatalf("channel index = %+v, want 0", result.Matches[0].ChannelIndex)
	}
}

func TestFindReportsMissingWordTiming(t *testing.T) {
	resp := elevenlabs.TranscriptResponse{
		Words: []elevenlabs.Word{
			{Text: "Hello", Type: "word"},
			{Text: "world", Type: "word"},
		},
	}

	result := Find(resp, "hello world", DefaultLimit)
	if result.HasTimedWords {
		t.Fatal("Find() HasTimedWords = true, want false")
	}
	if len(result.Matches) != 0 {
		t.Fatalf("Find() matches = %+v, want none", result.Matches)
	}
}

func TestFormatTimestampRoundsMilliseconds(t *testing.T) {
	if got := FormatTimestamp(3661.9996); got != "01:01:02.000" {
		t.Fatalf("FormatTimestamp() = %q", got)
	}
}

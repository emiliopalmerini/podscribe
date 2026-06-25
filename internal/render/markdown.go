package render

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/emiliopalmerini/podscribe/internal/elevenlabs"
)

type MarkdownOptions struct {
	Title      string
	SourceFile string
	Model      string

	GeneratedAt  time.Time
	Diarized     bool
	Timestamps   bool
	SpeakerNames []string
}

type block struct {
	Start   *float64
	Speaker string
	Text    string
}

func Markdown(resp elevenlabs.TranscriptResponse, opts MarkdownOptions) string {
	if opts.Title == "" {
		opts.Title = strings.TrimSuffix(filepath.Base(opts.SourceFile), filepath.Ext(opts.SourceFile))
	}
	if opts.GeneratedAt.IsZero() {
		opts.GeneratedAt = time.Now().UTC()
	}

	var b strings.Builder
	writeFrontMatter(&b, resp, opts)
	fmt.Fprintf(&b, "# %s\n\n", opts.Title)
	fmt.Fprintf(&b, "## Transcript\n\n")

	chunks := resp.Chunks()
	diarized := opts.Diarized || hasSpeakers(chunks)
	labels := speakerLabels(chunks, opts.SpeakerNames)
	for i, chunk := range chunks {
		if len(chunks) > 1 {
			if chunk.ChannelIndex != nil {
				fmt.Fprintf(&b, "### Channel %d\n\n", *chunk.ChannelIndex)
			} else {
				fmt.Fprintf(&b, "### Channel %d\n\n", i+1)
			}
		}
		writeChunk(&b, chunk, diarized, opts.Timestamps, labels)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

func writeFrontMatter(b *strings.Builder, resp elevenlabs.TranscriptResponse, opts MarkdownOptions) {
	chunks := resp.Chunks()
	first := chunks[0]
	duration := resp.AudioDurationSecs
	if duration == nil {
		duration = first.AudioDurationSecs
	}
	transcriptionID := resp.TranscriptionID
	if transcriptionID == "" {
		transcriptionID = first.TranscriptionID
	}

	fmt.Fprintln(b, "---")
	yamlString(b, "title", opts.Title)
	yamlString(b, "source_file", opts.SourceFile)
	yamlString(b, "provider", "elevenlabs")
	yamlString(b, "model", opts.Model)
	yamlString(b, "language_code", first.LanguageCode)
	if first.LanguageProbability != 0 {
		fmt.Fprintf(b, "language_probability: %s\n", strconv.FormatFloat(first.LanguageProbability, 'f', -1, 64))
	}
	if duration != nil {
		fmt.Fprintf(b, "audio_duration_secs: %s\n", strconv.FormatFloat(*duration, 'f', -1, 64))
	}
	yamlString(b, "transcription_id", transcriptionID)
	fmt.Fprintf(b, "diarized: %t\n", opts.Diarized || hasSpeakers(chunks))
	yamlString(b, "generated_at", opts.GeneratedAt.UTC().Format(time.RFC3339))
	fmt.Fprintln(b, "---")
	fmt.Fprintln(b)
}

func yamlString(b *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(b, "%s: %s\n", key, strconv.Quote(value))
}

func writeChunk(b *strings.Builder, chunk elevenlabs.TranscriptChunk, diarized, timestamps bool, labels map[string]string) {
	if len(chunk.Words) == 0 {
		text := strings.TrimSpace(chunk.Text)
		if text != "" {
			fmt.Fprintln(b, text)
			fmt.Fprintln(b)
		}
		return
	}

	blocks := groupWords(chunk.Words)
	for _, block := range blocks {
		line := strings.TrimSpace(block.Text)
		if diarized && block.Speaker != "" {
			line = labels[block.Speaker] + ": " + line
		}
		if timestamps {
			line = formatTimestamp(block.Start) + " " + line
		}
		fmt.Fprintf(b, "%s\n\n", line)
	}
}

func groupWords(words []elevenlabs.Word) []block {
	const (
		silenceGapSeconds = 1.5
		maxBlockSeconds   = 45.0
		maxBlockChars     = 650
	)

	var blocks []block
	var current block
	var text strings.Builder
	var lastEnd *float64

	flush := func() {
		content := strings.TrimSpace(text.String())
		if content == "" {
			return
		}
		current.Text = content
		blocks = append(blocks, current)
		current = block{}
		text.Reset()
	}

	for _, word := range words {
		if word.Text == "" {
			continue
		}

		startsNew := text.Len() == 0
		if !startsNew && word.Type != "spacing" {
			if current.Speaker != "" && word.SpeakerID != "" && current.Speaker != word.SpeakerID {
				startsNew = true
			}
			if lastEnd != nil && word.Start != nil && *word.Start-*lastEnd > silenceGapSeconds {
				startsNew = true
			}
			if current.Start != nil && word.Start != nil && *word.Start-*current.Start > maxBlockSeconds {
				startsNew = true
			}
			if text.Len() > maxBlockChars && endsSentence(text.String()) {
				startsNew = true
			}
		}

		if startsNew && text.Len() > 0 {
			flush()
		}
		if text.Len() == 0 {
			current.Start = word.Start
			current.Speaker = word.SpeakerID
		}
		appendToken(&text, word.Text, word.Type)
		if word.End != nil {
			lastEnd = word.End
		}
	}
	flush()

	sort.SliceStable(blocks, func(i, j int) bool {
		if blocks[i].Start == nil {
			return false
		}
		if blocks[j].Start == nil {
			return true
		}
		return *blocks[i].Start < *blocks[j].Start
	})
	return blocks
}

func appendToken(b *strings.Builder, token, typ string) {
	if typ == "spacing" {
		b.WriteString(token)
		return
	}
	current := b.String()
	last, _ := utf8.DecodeLastRuneInString(current)
	if current != "" && !unicode.IsSpace(last) && !startsWithPunctuation(token) {
		b.WriteByte(' ')
	}
	b.WriteString(token)
}

func startsWithPunctuation(s string) bool {
	if s == "" {
		return false
	}
	switch []rune(s)[0] {
	case '.', ',', '!', '?', ';', ':', ')', ']', '}', '%':
		return true
	default:
		return false
	}
}

func endsSentence(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasSuffix(s, ".") || strings.HasSuffix(s, "!") || strings.HasSuffix(s, "?")
}

func speakerLabels(chunks []elevenlabs.TranscriptChunk, names []string) map[string]string {
	labels := make(map[string]string)
	for _, chunk := range chunks {
		for _, word := range chunk.Words {
			if word.SpeakerID == "" {
				continue
			}
			if _, ok := labels[word.SpeakerID]; ok {
				continue
			}
			if len(labels) < len(names) {
				labels[word.SpeakerID] = names[len(labels)]
				continue
			}
			switch strings.ToLower(word.SpeakerID) {
			case "agent":
				labels[word.SpeakerID] = "Agent"
			case "customer":
				labels[word.SpeakerID] = "Customer"
			default:
				labels[word.SpeakerID] = fmt.Sprintf("Speaker %d", len(labels)+1)
			}
		}
	}
	return labels
}

func hasSpeakers(chunks []elevenlabs.TranscriptChunk) bool {
	for _, chunk := range chunks {
		for _, word := range chunk.Words {
			if word.SpeakerID != "" {
				return true
			}
		}
	}
	return false
}

func formatTimestamp(seconds *float64) string {
	if seconds == nil {
		return "[--:--:--]"
	}
	total := int(*seconds)
	if total < 0 {
		total = 0
	}
	h := total / 3600
	m := (total % 3600) / 60
	s := total % 60
	return fmt.Sprintf("[%02d:%02d:%02d]", h, m, s)
}

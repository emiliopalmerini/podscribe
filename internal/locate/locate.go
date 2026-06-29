package locate

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/emiliopalmerini/elevenlabs-go/elevenlabs"
)

const DefaultLimit = 5

type Result struct {
	HasTimedWords bool
	Matches       []Match
}

type Match struct {
	StartSeconds float64 `json:"start_seconds"`
	EndSeconds   float64 `json:"end_seconds"`
	Timestamp    string  `json:"timestamp"`
	Text         string  `json:"text"`
	Context      string  `json:"context"`
	ChannelIndex *int    `json:"channel_index,omitempty"`
}

type token struct {
	Value     string
	WordIndex int
}

var (
	leadingTimestamp = regexp.MustCompile(`^\s*\[(?:--:--:--|\d{1,2}:\d{2}:\d{2}(?:[.,]\d{1,3})?)\]\s*`)
	leadingSpeaker   = regexp.MustCompile(`^\s*[\p{L}\p{N}][\p{L}\p{N} ._'&-]{0,79}:\s+`)
)

func Find(resp *elevenlabs.Transcript, query string, limit int) Result {
	if resp == nil {
		return Result{}
	}
	if limit <= 0 {
		limit = DefaultLimit
	}
	queryTokens := normalizeTokens(stripSelectionPrefixes(query))
	if len(queryTokens) == 0 {
		return Result{}
	}

	var result Result
	for _, chunk := range resp.Chunks() {
		tokens, hasTimedWords := timedTokens(chunk.Words)
		result.HasTimedWords = result.HasTimedWords || hasTimedWords
		if len(tokens) < len(queryTokens) {
			continue
		}
		result.Matches = append(result.Matches, findChunkMatches(chunk, tokens, queryTokens)...)
	}

	sort.SliceStable(result.Matches, func(i, j int) bool {
		return result.Matches[i].StartSeconds < result.Matches[j].StartSeconds
	})
	if len(result.Matches) > limit {
		result.Matches = result.Matches[:limit]
	}
	return result
}

func findChunkMatches(chunk elevenlabs.Transcript, tokens []token, queryTokens []string) []Match {
	matches := make([]Match, 0)
	for i := 0; i <= len(tokens)-len(queryTokens); i++ {
		if !tokensMatch(tokens[i:i+len(queryTokens)], queryTokens) {
			continue
		}
		startIndex := tokens[i].WordIndex
		endIndex := tokens[i+len(queryTokens)-1].WordIndex
		start := chunk.Words[startIndex].Start
		if start == nil {
			continue
		}
		end := chunk.Words[endIndex].End
		if end == nil {
			end = chunk.Words[endIndex].Start
		}
		endSeconds := *start
		if end != nil {
			endSeconds = *end
		}
		contextStart := startIndex - 5
		if contextStart < 0 {
			contextStart = 0
		}
		contextEnd := endIndex + 5
		if contextEnd >= len(chunk.Words) {
			contextEnd = len(chunk.Words) - 1
		}
		matches = append(matches, Match{
			StartSeconds: *start,
			EndSeconds:   endSeconds,
			Timestamp:    FormatTimestamp(*start),
			Text:         phrase(chunk.Words, startIndex, endIndex),
			Context:      phrase(chunk.Words, contextStart, contextEnd),
			ChannelIndex: chunk.ChannelIndex,
		})
	}
	return matches
}

func tokensMatch(tokens []token, queryTokens []string) bool {
	if len(tokens) != len(queryTokens) {
		return false
	}
	for i := range tokens {
		if tokens[i].Value != queryTokens[i] {
			return false
		}
	}
	return true
}

func timedTokens(words []elevenlabs.TranscriptWord) ([]token, bool) {
	tokens := make([]token, 0, len(words))
	var hasTimedWords bool
	for i, word := range words {
		values := normalizeTokens(word.Text)
		if len(values) == 0 {
			continue
		}
		if word.Start == nil {
			continue
		}
		hasTimedWords = true
		for _, value := range values {
			tokens = append(tokens, token{Value: value, WordIndex: i})
		}
	}
	return tokens, hasTimedWords
}

func stripSelectionPrefixes(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i, line := range lines {
		line = leadingTimestamp.ReplaceAllString(line, "")
		line = leadingSpeaker.ReplaceAllString(line, "")
		lines[i] = line
	}
	return strings.Join(lines, " ")
}

func normalizeTokens(s string) []string {
	var tokens []string
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, b.String())
		b.Reset()
	}
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func phrase(words []elevenlabs.TranscriptWord, start, end int) string {
	if len(words) == 0 || start > end {
		return ""
	}
	if start < 0 {
		start = 0
	}
	if end >= len(words) {
		end = len(words) - 1
	}
	var b strings.Builder
	for i := start; i <= end; i++ {
		appendToken(&b, words[i].Text, words[i].Type)
	}
	return strings.TrimSpace(b.String())
}

func appendToken(b *strings.Builder, text, typ string) {
	if text == "" {
		return
	}
	if typ == "spacing" {
		b.WriteString(text)
		return
	}
	current := b.String()
	last, _ := utf8.DecodeLastRuneInString(current)
	if current != "" && !unicode.IsSpace(last) && !startsWithPunctuation(text) {
		b.WriteByte(' ')
	}
	b.WriteString(text)
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

func FormatTimestamp(seconds float64) string {
	if seconds < 0 {
		seconds = 0
	}
	totalMillis := int64(math.Round(seconds * 1000))
	millis := totalMillis % 1000
	totalSeconds := totalMillis / 1000
	hours := totalSeconds / 3600
	minutes := (totalSeconds % 3600) / 60
	secs := totalSeconds % 60
	return fmt.Sprintf("%02d:%02d:%02d.%03d", hours, minutes, secs, millis)
}

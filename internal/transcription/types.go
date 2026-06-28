package transcription

import "context"

// Provider transcribes audio and manages stored transcripts.
type Provider interface {
	Check(ctx context.Context) error
	UserID(ctx context.Context) (string, error)
	Transcribe(ctx context.Context, req Request) (Transcript, error)
	SubmitWebhook(ctx context.Context, req Request) (WebhookResponse, error)
	GetTranscript(ctx context.Context, id string) (Transcript, error)
	DeleteTranscript(ctx context.Context, id string) (any, error)
}

// RawGetter supports provider-specific read-only debugging requests.
type RawGetter interface {
	RawGet(ctx context.Context, path string) ([]byte, error)
}

type Request struct {
	FilePath                string
	Model                   string
	Language                string
	Diarize                 bool
	Speakers                int
	Keyterms                []string
	Clean                   bool
	TagAudioEvents          bool
	TimestampsGranularity   string
	UseMultiChannel         bool
	MultichannelOutputStyle string
	WebhookID               string
	WebhookMetadata         map[string]any
	OnUploadProgress        func(UploadProgress)
}

type UploadProgress struct {
	SentBytes int64
	// TotalBytes is -1 when the upload size cannot be determined.
	TotalBytes int64
	Done       bool
	Attempt    int
}

type Transcript struct {
	Text                string             `json:"text,omitempty"`
	LanguageCode        string             `json:"language_code,omitempty"`
	LanguageProbability float64            `json:"language_probability,omitempty"`
	Words               []Word             `json:"words,omitempty"`
	ChannelIndex        *int               `json:"channel_index,omitempty"`
	AdditionalFormats   []AdditionalFormat `json:"additional_formats,omitempty"`
	TranscriptionID     string             `json:"transcription_id,omitempty"`
	AudioDurationSecs   *float64           `json:"audio_duration_secs,omitempty"`
	Entities            []DetectedEntity   `json:"entities,omitempty"`
	Transcripts         []Transcript       `json:"transcripts,omitempty"`
}

func (t Transcript) Chunks() []Transcript {
	if len(t.Transcripts) > 0 {
		return t.Transcripts
	}
	return []Transcript{t}
}

type Word struct {
	Text         string      `json:"text"`
	Type         string      `json:"type,omitempty"`
	Start        *float64    `json:"start,omitempty"`
	End          *float64    `json:"end,omitempty"`
	SpeakerID    string      `json:"speaker_id,omitempty"`
	Logprob      float64     `json:"logprob,omitempty"`
	Characters   []Character `json:"characters,omitempty"`
	ChannelIndex *int        `json:"channel_index,omitempty"`
}

type Character struct {
	Text  string   `json:"text"`
	Start *float64 `json:"start,omitempty"`
	End   *float64 `json:"end,omitempty"`
}

type DetectedEntity struct {
	Text       string `json:"text"`
	EntityType string `json:"entity_type"`
	StartChar  int    `json:"start_char"`
	EndChar    int    `json:"end_char"`
}

type AdditionalFormat struct {
	RequestedFormat string `json:"requested_format"`
	FileExtension   string `json:"file_extension"`
	ContentType     string `json:"content_type"`
	IsBase64Encoded bool   `json:"is_base64_encoded"`
	Content         string `json:"content"`
}

type WebhookResponse struct {
	Message         string  `json:"message"`
	RequestID       string  `json:"request_id"`
	TranscriptionID *string `json:"transcription_id,omitempty"`
}

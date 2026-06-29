package transcription

import (
	"context"

	el "github.com/emiliopalmerini/elevenlabs-go/elevenlabs"
)

// Provider is the slim ElevenLabs boundary used by CLI workflows.
type Provider interface {
	Check(ctx context.Context) error
	UserID(ctx context.Context) (string, error)
	CreateTranscript(ctx context.Context, req el.CreateTranscriptRequest) (*el.Transcript, error)
	SubmitTranscriptWebhook(ctx context.Context, req el.CreateTranscriptRequest) (*el.TranscriptWebhookResponse, error)
	GetTranscript(ctx context.Context, id string) (*el.Transcript, error)
	DeleteTranscriptWithResponse(ctx context.Context, id string) (*el.Response[any], error)
}

// RawGetter supports provider-specific read-only debugging requests.
type RawGetter interface {
	RawGet(ctx context.Context, path string) ([]byte, error)
}

package ai

import "context"

// Message represents a single message in a conversation with an AI provider.
type Message struct {
	Role    string // "system" or "user"
	Content string
}

// Provider is the interface that AI backends must implement.
type Provider interface {
	Complete(ctx context.Context, messages []Message) (string, error)
}

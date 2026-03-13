package ai

import (
	"context"
	"fmt"
)

const (
	ProviderOpenAI = "openai"
	ProviderGemini = "gemini"
)

// NewProvider creates the appropriate AI provider based on the provider name.
// Accepted values for provider are "openai" and "gemini". apiKey is the
// provider-specific API key. model is an optional model override; pass "" for
// the provider default.
func NewProvider(ctx context.Context, provider, apiKey, model string) (Provider, error) {
	switch provider {
	case ProviderOpenAI:
		return NewOpenAIProvider(apiKey, model), nil
	case ProviderGemini:
		return NewGeminiProvider(ctx, apiKey, model)
	default:
		return nil, fmt.Errorf("unknown AI provider %q; expected %q or %q", provider, ProviderOpenAI, ProviderGemini)
	}
}

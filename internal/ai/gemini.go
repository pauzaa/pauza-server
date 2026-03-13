package ai

import (
	"context"
	"fmt"

	"google.golang.org/genai"
)

const defaultGeminiModel = "gemini-2.5-flash"

// GeminiProvider implements Provider using the Google Gemini API.
type GeminiProvider struct {
	client *genai.Client
	model  string
}

// NewGeminiProvider creates a Gemini-backed provider. If model is empty the
// default (gemini-2.5-flash) is used. The client is created once and reused.
func NewGeminiProvider(ctx context.Context, apiKey, model string) (*GeminiProvider, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return nil, fmt.Errorf("creating gemini client: %w", err)
	}
	if model == "" {
		model = defaultGeminiModel
	}
	return &GeminiProvider{client: client, model: model}, nil
}

func (p *GeminiProvider) Complete(ctx context.Context, messages []Message) (string, error) {
	var systemText string
	var userParts []*genai.Part

	for _, m := range messages {
		switch m.Role {
		case "system":
			systemText = m.Content
		case "user":
			userParts = append(userParts, &genai.Part{Text: m.Content})
		}
	}

	config := &genai.GenerateContentConfig{}
	if systemText != "" {
		config.SystemInstruction = &genai.Content{
			Parts: []*genai.Part{{Text: systemText}},
		}
	}

	contents := []*genai.Content{{
		Role:  "user",
		Parts: userParts,
	}}

	resp, err := p.client.Models.GenerateContent(ctx, p.model, contents, config)
	if err != nil {
		return "", fmt.Errorf("gemini generate content: %w", err)
	}
	text := resp.Text()
	if text == "" {
		return "", fmt.Errorf("gemini: empty response")
	}
	return text, nil
}

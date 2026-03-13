package ai

import (
	"context"
	"fmt"

	openai "github.com/sashabaranov/go-openai"
)

const defaultOpenAIModel = "gpt-4o"

// OpenAIProvider implements Provider using the OpenAI chat completions API.
type OpenAIProvider struct {
	client *openai.Client
	model  string
}

// NewOpenAIProvider creates an OpenAI-backed provider. If model is empty the
// default (gpt-4o) is used.
func NewOpenAIProvider(apiKey, model string) *OpenAIProvider {
	if model == "" {
		model = defaultOpenAIModel
	}
	return &OpenAIProvider{
		client: openai.NewClient(apiKey),
		model:  model,
	}
}

func (p *OpenAIProvider) Complete(ctx context.Context, messages []Message) (string, error) {
	msgs := make([]openai.ChatCompletionMessage, len(messages))
	for i, m := range messages {
		msgs[i] = openai.ChatCompletionMessage{
			Role:    m.Role,
			Content: m.Content,
		}
	}

	resp, err := p.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:    p.model,
		Messages: msgs,
	})
	if err != nil {
		return "", fmt.Errorf("openai chat completion: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("openai: empty response")
	}
	return resp.Choices[0].Message.Content, nil
}

package llm

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/sashabaranov/go-openai"
)

// cleanupTemperature keeps the rewrite deterministic. Cleanup is a mechanical
// edit, so any sampling spread just invites the model to reword the dictation.
const cleanupTemperature = 0.3

// ChatAdapter talks to any service exposing OpenAI's chat completions API.
// Providers differ only in base URL, so OpenAI, Groq, OpenRouter and a local
// Ollama or vLLM server all run through this one adapter.
type ChatAdapter struct {
	client *openai.Client
	config Config
}

// NewChatAdapter creates an adapter pointed at cfg.BaseURL.
func NewChatAdapter(cfg Config) *ChatAdapter {
	clientConfig := openai.DefaultConfig(cfg.APIKey)
	if cfg.BaseURL != "" {
		clientConfig.BaseURL = cfg.BaseURL
	}
	return &ChatAdapter{
		client: openai.NewClientWithConfig(clientConfig),
		config: cfg,
	}
}

func (a *ChatAdapter) Process(ctx context.Context, text string) (string, error) {
	if text == "" {
		return "", nil
	}

	opts := PostProcessingOptions{
		RemoveStutters:    a.config.RemoveStutters,
		AddPunctuation:    a.config.AddPunctuation,
		FixGrammar:        a.config.FixGrammar,
		RemoveFillerWords: a.config.RemoveFillerWords,
	}

	systemPrompt := BuildSystemPrompt(opts, a.config.Keywords)
	userPrompt := BuildUserPrompt(text, a.config.CustomPrompt)

	label := a.config.Provider
	if label == "" {
		label = "llm"
	}

	req := openai.ChatCompletionRequest{
		Model: a.config.Model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: systemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: userPrompt},
		},
		Temperature: cleanupTemperature,
	}

	start := time.Now()
	resp, err := a.client.CreateChatCompletion(ctx, req)
	duration := time.Since(start)

	if err != nil {
		log.Printf("%s-llm-adapter: API call failed after %v: %v", label, duration, err)
		return "", fmt.Errorf("%s chat completion: %w", label, err)
	}

	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("%s chat completion: no response choices", label)
	}

	result := StripPreamble(resp.Choices[0].Message.Content)
	log.Printf("%s-llm-adapter: processed in %v: %q -> %q", label, duration, text, result)
	return result, nil
}

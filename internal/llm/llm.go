package llm

import (
	"context"
	"fmt"
)

// Adapter interface for LLM text processing
type Adapter interface {
	Process(ctx context.Context, text string) (string, error)
}

// Config holds LLM adapter configuration
type Config struct {
	// Provider names the service, and only labels logs and errors. What the
	// adapter actually talks to is BaseURL.
	Provider string
	// BaseURL is an OpenAI-style API root, ending at the version segment
	// (e.g. "https://api.groq.com/openai/v1"). The client appends the
	// "/chat/completions" path itself.
	BaseURL           string
	APIKey            string
	Model             string
	RemoveStutters    bool
	AddPunctuation    bool
	FixGrammar        bool
	RemoveFillerWords bool
	CustomPrompt      string
	Keywords          []string
	// RequiresAPIKey is false for endpoints that serve without credentials,
	// such as a local Ollama or llama.cpp server.
	RequiresAPIKey bool
}

// NewAdapter creates an LLM adapter for the configured endpoint.
func NewAdapter(cfg Config) (Adapter, error) {
	if cfg.BaseURL == "" {
		return nil, fmt.Errorf("llm base URL required for provider %q", cfg.Provider)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("llm model required for provider %q", cfg.Provider)
	}
	if cfg.RequiresAPIKey && cfg.APIKey == "" {
		return nil, fmt.Errorf("%s API key required", cfg.Provider)
	}
	return NewChatAdapter(cfg), nil
}

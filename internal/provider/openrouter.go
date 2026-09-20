package provider

import "strings"

// openRouterBaseURL is OpenRouter's OpenAI-compatible API root.
const openRouterBaseURL = "https://openrouter.ai/api"

// OpenRouterProvider implements Provider for OpenRouter, a single API key and
// bill in front of models from many vendors. It serves LLM models only;
// transcription goes through the dedicated speech providers.
type OpenRouterProvider struct{}

func (p *OpenRouterProvider) Name() string {
	return ProviderOpenRouter
}

func (p *OpenRouterProvider) RequiresAPIKey() bool {
	return true
}

func (p *OpenRouterProvider) ValidateAPIKey(key string) bool {
	return strings.HasPrefix(key, "sk-or-")
}

func (p *OpenRouterProvider) APIKeyURL() string {
	return "https://openrouter.ai/keys"
}

func (p *OpenRouterProvider) IsLocal() bool {
	return false
}

// Models lists a few models suited to transcript cleanup. Cleanup is a
// mechanical edit that any competent model handles, and the result is typed
// out while the user waits, so these are picked for latency and price rather
// than reasoning ability. Any other OpenRouter model id works too: set
// llm.model by hand.
func (p *OpenRouterProvider) Models() []Model {
	endpoint := &EndpointConfig{BaseURL: openRouterBaseURL, Path: "/v1/chat/completions"}

	llmModel := func(id, name, description string) Model {
		return Model{
			ID:                id,
			Name:              name,
			Description:       description,
			Type:              LLM,
			SupportsBatch:     true,
			SupportsStreaming: false,
			Local:             false,
			AdapterType:       AdapterOpenAI,
			Endpoint:          endpoint,
			DocsURL:           "https://openrouter.ai/models",
		}
	}

	return []Model{
		llmModel("google/gemini-2.5-flash-lite", "Gemini 2.5 Flash Lite", "Fastest and cheapest; good default for cleanup"),
		llmModel("openai/gpt-4o-mini", "GPT-4o Mini", "Fast and cheap; reliable instruction following"),
		llmModel("anthropic/claude-haiku-4.5", "Claude Haiku 4.5", "Fast; strongest at leaving wording alone"),
		llmModel("meta-llama/llama-3.3-70b-instruct", "Llama 3.3 70B Instruct", "Open weights; priced low across providers"),
	}
}

func (p *OpenRouterProvider) DefaultModel(t ModelType) string {
	if t == LLM {
		return "google/gemini-2.5-flash-lite"
	}
	return ""
}

package provider

import "strings"

// GroqProvider implements Provider for Groq services
type GroqProvider struct{}

func (p *GroqProvider) Name() string {
	return ProviderGroq
}

func (p *GroqProvider) RequiresAPIKey() bool {
	return true
}

func (p *GroqProvider) ValidateAPIKey(key string) bool {
	return strings.HasPrefix(key, "gsk_")
}

func (p *GroqProvider) APIKeyURL() string {
	return "https://console.groq.com/keys"
}

func (p *GroqProvider) IsLocal() bool {
	return false
}

func (p *GroqProvider) Models() []Model {
	// https://console.groq.com/docs/speech-to-text#supported-languages
	allLangs := groqTranscriptionLanguages
	docsURL := "https://console.groq.com/docs/speech-to-text#supported-languages"

	return []Model{
		// transcription models
		{
			ID:                 "whisper-large-v3",
			Name:               "Whisper Large v3",
			Description:        "Best accuracy; generous free tier makes this great default",
			Type:               Transcription,
			SupportsBatch:      true,
			SupportsStreaming:  false,
			Local:              false,
			AdapterType:        AdapterOpenAI,
			SupportedLanguages: allLangs,
			Endpoint:           &EndpointConfig{BaseURL: "https://api.groq.com/openai", Path: "/v1/audio/transcriptions"},
			DocsURL:            docsURL,
		},
		{
			ID:                 "whisper-large-v3-turbo",
			Name:               "Whisper Large v3 Turbo",
			Description:        "Faster with slight accuracy tradeoff; still very good",
			Type:               Transcription,
			SupportsBatch:      true,
			SupportsStreaming:  false,
			Local:              false,
			AdapterType:        AdapterOpenAI,
			SupportedLanguages: allLangs,
			Endpoint:           &EndpointConfig{BaseURL: "https://api.groq.com/openai", Path: "/v1/audio/transcriptions"},
			DocsURL:            docsURL,
		},
		// LLM models. Groq rotates its catalog: check
		// https://console.groq.com/docs/models before adding one here.
		{
			ID:                "openai/gpt-oss-20b",
			Name:              "GPT-OSS 20B",
			Description:       "Fastest good cleanup; best default",
			Type:              LLM,
			SupportsBatch:     true,
			SupportsStreaming: false,
			Local:             false,
			AdapterType:       AdapterOpenAI,
			Endpoint:          &EndpointConfig{BaseURL: "https://api.groq.com/openai", Path: "/v1/chat/completions"},
		},
		{
			ID:                "openai/gpt-oss-120b",
			Name:              "GPT-OSS 120B",
			Description:       "Smarter rewrites; slower than the 20B",
			Type:              LLM,
			SupportsBatch:     true,
			SupportsStreaming: false,
			Local:             false,
			AdapterType:       AdapterOpenAI,
			Endpoint:          &EndpointConfig{BaseURL: "https://api.groq.com/openai", Path: "/v1/chat/completions"},
		},
		{
			ID:                "qwen/qwen3.8-27b",
			Name:              "Qwen3.8 27B",
			Description:       "Strong multilingual cleanup",
			Type:              LLM,
			SupportsBatch:     true,
			SupportsStreaming: false,
			Local:             false,
			AdapterType:       AdapterOpenAI,
			Endpoint:          &EndpointConfig{BaseURL: "https://api.groq.com/openai", Path: "/v1/chat/completions"},
		},
	}
}

func (p *GroqProvider) DefaultModel(t ModelType) string {
	switch t {
	case Transcription:
		return "whisper-large-v3-turbo"
	case LLM:
		return "openai/gpt-oss-20b"
	}
	return ""
}

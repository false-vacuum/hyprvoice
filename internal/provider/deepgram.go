package provider

// DeepgramProvider implements Provider for Deepgram transcription services
type DeepgramProvider struct{}

func (p *DeepgramProvider) Name() string {
	return ProviderDeepgram
}

func (p *DeepgramProvider) RequiresAPIKey() bool {
	return true
}

func (p *DeepgramProvider) ValidateAPIKey(key string) bool {
	// Deepgram API keys are alphanumeric, just check non-empty
	return len(key) > 0
}

func (p *DeepgramProvider) APIKeyURL() string {
	return "https://console.deepgram.com/project/keys"
}

func (p *DeepgramProvider) IsLocal() bool {
	return false
}

func (p *DeepgramProvider) Models() []Model {
	// https://developers.deepgram.com/docs/models-languages-overview
	nova3Langs := deepgramNova3Languages
	nova2Langs := deepgramNova2Languages

	docsURL := "https://developers.deepgram.com/docs/language"

	return []Model{
		{
			ID:                 "nova-3",
			Name:               "Nova-3",
			Description:        "Best accuracy; streaming available for faster response",
			Type:               Transcription,
			SupportsBatch:      true,
			SupportsStreaming:  true,
			Local:              false,
			AdapterType:        AdapterDeepgram,
			SupportedLanguages: nova3Langs,
			Endpoint:           &EndpointConfig{BaseURL: "https://api.deepgram.com", Path: "/v1/listen"},
			StreamingEndpoint:  &EndpointConfig{BaseURL: "wss://api.deepgram.com", Path: "/v1/listen"},
			DocsURL:            docsURL,
		},
		{
			ID:                "flux-general-en",
			Name:              "Flux (English)",
			Description:       "Turn-based streaming; live drafts and lowest latency (English only)",
			Type:              Transcription,
			SupportsBatch:     false,
			SupportsStreaming: true,
			Local:             false,
			AdapterType:       AdapterDeepgramFlux,
			// English-only. Both codes are listed so a Deepgram user already
			// set to en-US can switch to Flux without a config error; the API
			// takes no language parameter either way.
			SupportedLanguages: []string{"en", "en-US"},
			// Flux is a separate API from the v1 models, on its own path.
			Endpoint: &EndpointConfig{BaseURL: "wss://api.deepgram.com", Path: "/v2/listen"},
			DocsURL:  "https://developers.deepgram.com/docs/flux/",
		},
		{
			ID:                 "nova-2",
			Name:               "Nova-2",
			Description:        "Cheaper legacy model; still solid accuracy",
			Type:               Transcription,
			SupportsBatch:      true,
			SupportsStreaming:  true,
			Local:              false,
			AdapterType:        AdapterDeepgram,
			SupportedLanguages: nova2Langs,
			Endpoint:           &EndpointConfig{BaseURL: "https://api.deepgram.com", Path: "/v1/listen"},
			StreamingEndpoint:  &EndpointConfig{BaseURL: "wss://api.deepgram.com", Path: "/v1/listen"},
			DocsURL:            docsURL,
		},
	}
}

func (p *DeepgramProvider) DefaultModel(t ModelType) string {
	switch t {
	case Transcription:
		return "nova-3"
	}
	return ""
}

package provider

// CustomProvider implements Provider for any OpenAI-compatible chat endpoint:
// Ollama, LM Studio, llama.cpp, vLLM, LiteLLM, a company gateway.
//
// It is the one provider whose models are not known ahead of time, so Models()
// is empty and the base URL and model id both come from config. Callers that
// enumerate models must special-case it via IsCustom.
type CustomProvider struct{}

func (p *CustomProvider) Name() string {
	return ProviderCustom
}

// RequiresAPIKey is false because local servers usually serve without one. A
// key is still sent when config supplies it, for gateways that want it.
func (p *CustomProvider) RequiresAPIKey() bool {
	return false
}

// ValidateAPIKey accepts anything: the endpoint defines the key format, and an
// empty key is valid for a local server.
func (p *CustomProvider) ValidateAPIKey(key string) bool {
	return true
}

func (p *CustomProvider) APIKeyURL() string {
	return ""
}

// IsLocal is false because the endpoint may be either. Local-only handling
// keys off the configured URL, not this flag.
func (p *CustomProvider) IsLocal() bool {
	return false
}

func (p *CustomProvider) Models() []Model {
	return nil
}

func (p *CustomProvider) DefaultModel(t ModelType) string {
	return ""
}

// IsCustom reports whether a provider name is the user-supplied endpoint,
// whose model list and base URL come from config rather than the registry.
func IsCustom(providerName string) bool {
	return providerName == ProviderCustom
}

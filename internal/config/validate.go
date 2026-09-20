package config

import (
	"fmt"
	"slices"
	"strings"

	"github.com/leonardotrapani/hyprvoice/internal/injection"
	"github.com/leonardotrapani/hyprvoice/internal/provider"
)

// mapConfigProviderToRegistryName maps config provider names to provider registry names
// Config uses names like "groq-transcription", "mistral-transcription"
// Registry uses base names like "groq", "mistral"
func mapConfigProviderToRegistryName(configProvider string) string {
	switch configProvider {
	case "groq-transcription":
		return "groq"
	case "mistral-transcription":
		return "mistral"
	default:
		return configProvider
	}
}

// envVarForProvider returns the environment variable name for a provider's API key
func envVarForProvider(registryName string) string {
	switch registryName {
	case "openai":
		return "OPENAI_API_KEY"
	case "groq":
		return "GROQ_API_KEY"
	case "mistral":
		return "MISTRAL_API_KEY"
	case "elevenlabs":
		return "ELEVENLABS_API_KEY"
	case "deepgram":
		return "DEEPGRAM_API_KEY"
	default:
		return ""
	}
}

func (c *Config) Validate() error {
	if c.Recording.SampleRate <= 0 {
		return fmt.Errorf("invalid recording.sample_rate: %d", c.Recording.SampleRate)
	}
	if c.Recording.Channels <= 0 {
		return fmt.Errorf("invalid recording.channels: %d", c.Recording.Channels)
	}
	if c.Recording.BufferSize <= 0 {
		return fmt.Errorf("invalid recording.buffer_size: %d", c.Recording.BufferSize)
	}
	if c.Recording.ChannelBufferSize <= 0 {
		return fmt.Errorf("invalid recording.channel_buffer_size: %d", c.Recording.ChannelBufferSize)
	}
	if c.Recording.Format == "" {
		return fmt.Errorf("invalid recording.format: empty")
	}
	if c.Recording.Timeout <= 0 {
		return fmt.Errorf("invalid recording.timeout: %v", c.Recording.Timeout)
	}

	if c.Transcription.Provider == "" {
		return fmt.Errorf("invalid transcription.provider: empty")
	}

	// map config provider name to registry provider name
	registryName := mapConfigProviderToRegistryName(c.Transcription.Provider)

	// validate provider exists in registry
	p := provider.GetProvider(registryName)
	if p == nil {
		providers := provider.ListProvidersWithTranscription()
		return fmt.Errorf("unknown transcription.provider: %s (available: %s)", c.Transcription.Provider, strings.Join(providers, ", "))
	}

	// validate API key requirement using registry
	if p.RequiresAPIKey() {
		apiKey := c.resolveAPIKeyForProvider(c.Transcription.Provider)
		if apiKey == "" {
			envVar := envVarForProvider(registryName)
			return fmt.Errorf("%s API key required: not found in config (providers.%s.api_key) or environment variable (%s)",
				strings.Title(registryName), registryName, envVar)
		}
	}

	// validate model exists
	if c.Transcription.Model == "" {
		return fmt.Errorf("invalid transcription.model: empty")
	}

	// validate model exists in provider
	_, err := provider.GetModel(registryName, c.Transcription.Model)
	if err != nil {
		models := provider.ModelsOfType(p, provider.Transcription)
		modelIDs := make([]string, len(models))
		for i, m := range models {
			modelIDs[i] = m.ID
		}
		return fmt.Errorf("invalid model for %s: %s (available: %s)", c.Transcription.Provider, c.Transcription.Model, strings.Join(modelIDs, ", "))
	}

	// validate language-model compatibility using effective language (transcription overrides general)
	effectiveLanguage := c.resolveEffectiveLanguage()
	if err := ValidateModelLanguageCompatibility(registryName, c.Transcription.Model, effectiveLanguage); err != nil {
		return err
	}

	// LLM validation
	if c.LLM.Enabled {
		if c.LLM.Provider == "" {
			return fmt.Errorf("llm.provider required when llm.enabled = true")
		}
		if c.LLM.Model == "" {
			return fmt.Errorf("llm.model required when llm.enabled = true")
		}

		// validate LLM provider exists
		llmProvider := provider.GetProvider(c.LLM.Provider)
		if llmProvider == nil {
			providers := provider.ListProvidersWithLLM()
			return fmt.Errorf("invalid llm.provider: %s (available: %s)", c.LLM.Provider, strings.Join(providers, ", "))
		}

		// validate LLM model exists
		llmModel, err := provider.GetModel(c.LLM.Provider, c.LLM.Model)
		if err != nil {
			models := provider.ModelsOfType(llmProvider, provider.LLM)
			modelIDs := make([]string, len(models))
			for i, m := range models {
				modelIDs[i] = m.ID
			}
			return fmt.Errorf("invalid llm.model: %s (available for %s: %s)", c.LLM.Model, c.LLM.Provider, strings.Join(modelIDs, ", "))
		}

		// verify model is actually an LLM
		if llmModel.Type != provider.LLM {
			return fmt.Errorf("invalid llm.model: %s is not an LLM model", c.LLM.Model)
		}

		// validate LLM API key
		if llmProvider.RequiresAPIKey() {
			llmAPIKey := c.resolveAPIKeyForLLMProvider(c.LLM.Provider)
			if llmAPIKey == "" {
				envVar := envVarForProvider(c.LLM.Provider)
				return fmt.Errorf("%s API key required for LLM: not found in config (providers.%s.api_key) or environment variable (%s)",
					strings.Title(c.LLM.Provider), c.LLM.Provider, envVar)
			}
		}
	}

	if len(c.Injection.Backends) == 0 {
		return fmt.Errorf("invalid injection.backends: empty (must have at least one backend)")
	}
	validBackends := map[string]bool{"ydotool": true, "wtype": true, "clipboard": true, "paste": true}
	for _, backend := range c.Injection.Backends {
		if !validBackends[backend] {
			return fmt.Errorf("invalid injection.backends: unknown backend %q (must be paste, ydotool, wtype, or clipboard)", backend)
		}
	}
	if c.Injection.YdotoolTimeout <= 0 {
		return fmt.Errorf("invalid injection.ydotool_timeout: %v", c.Injection.YdotoolTimeout)
	}
	if c.Injection.WtypeTimeout <= 0 {
		return fmt.Errorf("invalid injection.wtype_timeout: %v", c.Injection.WtypeTimeout)
	}
	if c.Injection.ClipboardTimeout <= 0 {
		return fmt.Errorf("invalid injection.clipboard_timeout: %v", c.Injection.ClipboardTimeout)
	}
	if err := validatePaste(c.Injection); err != nil {
		return err
	}

	validTypes := map[string]bool{"desktop": true, "overlay": true, "log": true, "none": true}
	if !validTypes[c.Notifications.Type] {
		return fmt.Errorf("invalid notifications.type: %s (must be desktop, overlay, log, or none)", c.Notifications.Type)
	}

	return nil
}

// ValidateModelLanguageCompatibility validates that a model supports the given language.
// Returns error if the language is not supported, nil if supported or if langCode is empty (auto).
func ValidateModelLanguageCompatibility(registryProvider, modelID, langCode string) error {
	// empty language code means auto-detect, always supported
	if langCode == "" {
		return nil
	}

	model, err := provider.GetModel(registryProvider, modelID)
	if err != nil {
		return err // model not found errors handled elsewhere
	}

	if model.SupportsLanguage(langCode) {
		return nil
	}

	// language not supported - build helpful error message
	// truncate supported languages for error message
	supported := model.SupportedLanguages
	suffix := ""
	if len(supported) > 5 {
		supported = supported[:5]
		suffix = "..."
	}

	// build error with docs URL if available
	docsHint := ""
	if model.DocsURL != "" {
		docsHint = fmt.Sprintf(" See %s for full list.", model.DocsURL)
	}

	langLabel := provider.LanguageLabel(langCode)
	if langLabel == "" {
		langLabel = fmt.Sprintf("language '%s'", langCode)
	}

	return fmt.Errorf(
		"model %s does not support %s.%s Supported: %s%s",
		model.Name,
		langLabel,
		docsHint,
		strings.Join(supported, ", "),
		suffix,
	)
}

// validatePaste checks the paste backend's settings, and only when it is
// actually in the chain: an unused section is not worth failing a load over.
func validatePaste(cfg InjectionConfig) error {
	if !slices.Contains(cfg.Backends, "paste") {
		return nil
	}

	if cfg.PasteTimeout <= 0 {
		return fmt.Errorf("invalid injection.paste_timeout: %v", cfg.PasteTimeout)
	}
	if err := injection.ValidatePasteKey(cfg.Paste.Key); err != nil {
		return fmt.Errorf("invalid injection.paste.key: %w", err)
	}
	if err := validateSelection(cfg.Paste.Selection); err != nil {
		return fmt.Errorf("invalid injection.paste.selection: %w", err)
	}
	for app, entry := range cfg.Paste.Apps {
		if err := injection.ValidatePasteKey(entry.Key); err != nil {
			return fmt.Errorf("invalid injection.paste.apps.%s.key: %w", app, err)
		}
		if err := validateSelection(entry.Selection); err != nil {
			return fmt.Errorf("invalid injection.paste.apps.%s.selection: %w", app, err)
		}
	}
	return nil
}

func validateSelection(selection string) error {
	switch selection {
	case "", "clipboard", injection.SelectionPrimary:
		return nil
	default:
		return fmt.Errorf("%q (must be clipboard or primary)", selection)
	}
}

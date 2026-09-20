package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonardotrapani/hyprvoice/internal/injection"
	"github.com/leonardotrapani/hyprvoice/internal/provider"
)

// validTestConfig is a config that passes Validate, so each test can change
// the one thing it is about and trust the rest.
func validTestConfig(t *testing.T) *Config {
	t.Helper()

	cfg := DefaultConfig()
	cfg.Transcription.Provider = "openai"
	cfg.Transcription.Model = "whisper-1"
	cfg.Providers = map[string]ProviderConfig{"openai": {APIKey: "test-key"}}
	cfg.Notifications.Type = "none"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("fixture is not valid to begin with: %v", err)
	}
	return cfg
}

func TestValidate_RejectsABadPasteKey(t *testing.T) {
	// Arrange: a typo in the config should fail the load, not the dictation.
	cfg := validTestConfig(t)
	cfg.Injection.Backends = []string{"paste", "wtype"}
	cfg.Injection.Paste.Key = "meta+v"

	// Act
	err := cfg.Validate()

	// Assert
	if err == nil {
		t.Fatal("an unknown modifier was accepted")
	}
	if !strings.Contains(err.Error(), "injection.paste.key") {
		t.Errorf("error does not name the setting: %v", err)
	}
}

func TestValidate_RejectsABadPerAppPasteKey(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Injection.Backends = []string{"paste"}
	cfg.Injection.Paste.Apps = map[string]PasteAppConfig{"kitty": {Key: "ctrl+"}}

	err := cfg.Validate()

	if err == nil {
		t.Fatal("a malformed per-app key was accepted")
	}
	if !strings.Contains(err.Error(), "kitty") {
		t.Errorf("error does not name the app: %v", err)
	}
}

func TestValidate_RejectsAnUnusablePasteTimeout(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Injection.Backends = []string{"paste"}
	cfg.Injection.PasteTimeout = 0

	if err := cfg.Validate(); err == nil {
		t.Error("a zero paste timeout was accepted")
	}
}

func TestValidate_IgnoresPasteSettingsWhenTheBackendIsUnused(t *testing.T) {
	// An unused section is not worth failing a load over.
	cfg := validTestConfig(t)
	cfg.Injection.Backends = []string{"wtype", "clipboard"}
	cfg.Injection.Paste.Key = "nonsense+"
	cfg.Injection.PasteTimeout = 0

	if err := cfg.Validate(); err != nil {
		t.Errorf("unused paste settings failed the load: %v", err)
	}
}

func TestValidate_AcceptsPasteInTheBackendChain(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Injection.Backends = []string{"paste", "wtype", "clipboard"}

	if err := cfg.Validate(); err != nil {
		t.Errorf("the shipped paste defaults do not validate: %v", err)
	}
}

func TestDefaultPasteApps_AreAllSendable(t *testing.T) {
	// Every shipped default must parse, or it breaks on first use.
	for app, entry := range DefaultPasteApps() {
		if err := injection.ValidatePasteKey(entry.Key); err != nil {
			t.Errorf("shipped default for %s is unusable: %v", app, err)
		}
	}
}

func TestToInjectionConfig_LowercasesAppIds(t *testing.T) {
	// The lookup is case-insensitive, which only works if the map is folded
	// on the way in.
	cfg := validTestConfig(t)
	cfg.Injection.Paste.Apps = map[string]PasteAppConfig{"Alacritty": {Key: "ctrl+shift+v"}}

	got := cfg.ToInjectionConfig()

	if got.Paste.Apps["alacritty"].Key != "ctrl+shift+v" {
		t.Errorf("app ids were not folded: %v", got.Paste.Apps)
	}
}

func TestLoad_FillsPasteDefaultsForAConfigWithoutTheSection(t *testing.T) {
	// Arrange: a config file written before the paste backend existed, with
	// only "paste" added to the chain.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "hyprvoice")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := `[recording]
  sample_rate = 16000
  channels = 1
  format = "s16"
  buffer_size = 8192
  channel_buffer_size = 30
  timeout = "5m"

[transcription]
  provider = "openai"
  model = "whisper-1"

[providers.openai]
  api_key = "test-key"

[injection]
  backends = ["paste", "wtype", "clipboard"]
  ydotool_timeout = "5s"
  wtype_timeout = "5s"
  clipboard_timeout = "3s"

[notifications]
  type = "none"
`
	if err := os.WriteFile(filepath.Join(path, "config.toml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	// Act
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed on a config with no paste section: %v", err)
	}

	// Assert
	if err := cfg.Validate(); err != nil {
		t.Errorf("config with no paste section does not validate: %v", err)
	}
	if cfg.Injection.PasteTimeout != DefaultPasteTimeout {
		t.Errorf("paste_timeout = %v, want %v", cfg.Injection.PasteTimeout, DefaultPasteTimeout)
	}
	if cfg.Injection.Paste.Key != DefaultPasteKey {
		t.Errorf("paste key = %q, want %q", cfg.Injection.Paste.Key, DefaultPasteKey)
	}
	if cfg.Injection.Paste.Apps["kitty"].Key == "" {
		t.Error("shipped per-app defaults were not filled in")
	}
	if !cfg.Injection.Paste.RestoreClipboard {
		t.Error("restore_clipboard defaulted to off")
	}
}

func TestLoad_KeepsRestoreClipboardOffWhenSetExplicitly(t *testing.T) {
	// A bool cannot say "unset", so an explicit false must survive.
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "hyprvoice")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := `[recording]
  sample_rate = 16000
  channels = 1
  format = "s16"
  buffer_size = 8192
  channel_buffer_size = 30
  timeout = "5m"

[transcription]
  provider = "openai"
  model = "whisper-1"

[providers.openai]
  api_key = "test-key"

[injection]
  backends = ["paste"]

  [injection.paste]
    restore_clipboard = false

[notifications]
  type = "none"
`
	if err := os.WriteFile(filepath.Join(path, "config.toml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() failed: %v", err)
	}

	if cfg.Injection.Paste.RestoreClipboard {
		t.Error("an explicit restore_clipboard = false was overridden")
	}
}

func TestValidateLLMBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		baseURL  string
		wantErr  bool
	}{
		{
			name:     "custom provider requires a URL",
			provider: provider.ProviderCustom,
			baseURL:  "",
			wantErr:  true,
		},
		{
			name:     "custom provider accepts a local URL",
			provider: provider.ProviderCustom,
			baseURL:  "http://localhost:11434/v1",
			wantErr:  false,
		},
		{
			name:     "known provider needs no URL",
			provider: provider.ProviderGroq,
			baseURL:  "",
			wantErr:  false,
		},
		{
			name:     "known provider accepts an override",
			provider: provider.ProviderGroq,
			baseURL:  "https://proxy.internal/openai/v1",
			wantErr:  false,
		},
		{
			name:     "rejects a missing scheme",
			provider: provider.ProviderCustom,
			baseURL:  "localhost:11434/v1",
			wantErr:  true,
		},
		{
			name:     "rejects a missing host",
			provider: provider.ProviderCustom,
			baseURL:  "http:///v1",
			wantErr:  true,
		},
		{
			name:     "rejects a URL carrying the chat completions path",
			provider: provider.ProviderCustom,
			baseURL:  "http://localhost:11434/v1/chat/completions",
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateLLMBaseURL(tc.provider, tc.baseURL)

			if tc.wantErr && err == nil {
				t.Fatal("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestResolveLLMBaseURL(t *testing.T) {
	tests := []struct {
		name     string
		llm      LLMConfig
		expected string
	}{
		{
			name:     "uses the registry URL for a known provider",
			llm:      LLMConfig{Provider: provider.ProviderGroq},
			expected: "https://api.groq.com/openai/v1",
		},
		{
			name:     "uses the registry URL for openrouter",
			llm:      LLMConfig{Provider: provider.ProviderOpenRouter},
			expected: "https://openrouter.ai/api/v1",
		},
		{
			name:     "an explicit base URL overrides the registry",
			llm:      LLMConfig{Provider: provider.ProviderGroq, BaseURL: "https://proxy.internal/v1"},
			expected: "https://proxy.internal/v1",
		},
		{
			name:     "trims a trailing slash",
			llm:      LLMConfig{Provider: provider.ProviderCustom, BaseURL: "http://localhost:11434/v1/"},
			expected: "http://localhost:11434/v1",
		},
		{
			name:     "empty for a custom provider with no URL",
			llm:      LLMConfig{Provider: provider.ProviderCustom},
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{LLM: tc.llm}

			result := cfg.resolveLLMBaseURL()

			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
		})
	}
}

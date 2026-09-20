package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonardotrapani/hyprvoice/internal/injection"
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

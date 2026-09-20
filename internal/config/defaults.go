package config

import "time"

// DefaultConfig returns the initial configuration used for onboarding.
// Defaults for the paste backend, shared by a fresh config and by filling in
// a config file written before the backend existed.
const (
	DefaultPasteKey       = "ctrl+v"
	DefaultPasteSelection = "clipboard"
	DefaultPasteTimeout   = 3 * time.Second
)

func DefaultConfig() *Config {
	return &Config{
		Recording: RecordingConfig{
			SampleRate:        16000,
			Channels:          1,
			Format:            "s16",
			BufferSize:        8192,
			Device:            "",
			ChannelBufferSize: 30,
			Timeout:           5 * time.Minute,
		},
		Transcription: TranscriptionConfig{
			Language:  "",
			Streaming: false,
			Threads:   0,
		},
		Injection: InjectionConfig{
			Backends:         []string{"ydotool", "wtype", "clipboard"},
			YdotoolTimeout:   5 * time.Second,
			WtypeTimeout:     5 * time.Second,
			ClipboardTimeout: 3 * time.Second,
			PasteTimeout:     DefaultPasteTimeout,
			Paste: PasteConfig{
				Key:              DefaultPasteKey,
				Selection:        DefaultPasteSelection,
				Apps:             DefaultPasteApps(),
				RestoreClipboard: true,
			},
		},
		Notifications: NotificationsConfig{
			Enabled: false,
			Type:    "",
		},
		Providers: make(map[string]ProviderConfig),
		Keywords:  nil,
		LLM: LLMConfig{
			Enabled: false,
		},
	}
}

// DefaultPasteApps is how known applications take a paste.
//
// Terminals moved paste onto ctrl+shift+v because ctrl+v predates their use
// of ctrl+c for interrupt. kitty also pastes the primary selection on
// shift+Insert, which is preferred: clipboard managers generally do not watch
// primary, so a dictation pasted that way leaves no history entry and the
// clipboard is never touched.
func DefaultPasteApps() map[string]PasteAppConfig {
	clipboard := func(key string) PasteAppConfig {
		return PasteAppConfig{Key: key, Selection: "clipboard"}
	}

	return map[string]PasteAppConfig{
		"kitty":                  {Key: "shift+Insert", Selection: "primary"},
		"foot":                   clipboard("ctrl+shift+v"),
		"footclient":             clipboard("ctrl+shift+v"),
		"alacritty":              clipboard("ctrl+shift+v"),
		"org.wezfurlong.wezterm": clipboard("ctrl+shift+v"),
		"com.mitchellh.ghostty":  clipboard("ctrl+shift+v"),
		"wezterm":                clipboard("ctrl+shift+v"),
		"ghostty":                clipboard("ctrl+shift+v"),
		"gnome-terminal":         clipboard("ctrl+shift+v"),
		"org.gnome.console":      clipboard("ctrl+shift+v"),
		"konsole":                clipboard("ctrl+shift+v"),
		"xterm":                  clipboard("ctrl+shift+v"),
		"rio":                    clipboard("ctrl+shift+v"),
		"st":                     clipboard("ctrl+shift+v"),
	}
}

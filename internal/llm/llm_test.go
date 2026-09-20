package llm

import (
	"strings"
	"testing"
)

func TestBuildSystemPrompt(t *testing.T) {
	tests := []struct {
		name     string
		opts     PostProcessingOptions
		keywords []string
		contains []string
	}{
		{
			name: "all options enabled",
			opts: PostProcessingOptions{
				RemoveStutters:    true,
				AddPunctuation:    true,
				FixGrammar:        true,
				RemoveFillerWords: true,
			},
			keywords: nil,
			contains: []string{
				"Remove stutters",
				"Add proper punctuation",
				"Fix grammar",
				"Remove filler words",
			},
		},
		{
			name: "only grammar",
			opts: PostProcessingOptions{
				FixGrammar: true,
			},
			keywords: nil,
			contains: []string{
				"Fix grammar",
			},
		},
		{
			name: "with keywords",
			opts: PostProcessingOptions{
				RemoveStutters: true,
			},
			keywords: []string{"Kubernetes", "TypeScript", "hyprvoice"},
			contains: []string{
				"Kubernetes",
				"TypeScript",
				"hyprvoice",
				"Context keywords",
			},
		},
		{
			name:     "no options - should have default",
			opts:     PostProcessingOptions{},
			keywords: nil,
			contains: []string{
				"Clean up the text",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := BuildSystemPrompt(tc.opts, tc.keywords)
			for _, expected := range tc.contains {
				if !strings.Contains(result, expected) {
					t.Errorf("expected prompt to contain %q, got: %s", expected, result)
				}
			}
		})
	}
}

func TestBuildUserPrompt(t *testing.T) {
	tests := []struct {
		name         string
		text         string
		customPrompt string
		expected     string
	}{
		{
			name:         "no custom prompt",
			text:         "hello world",
			customPrompt: "",
			expected:     "hello world",
		},
		{
			name:         "with custom prompt",
			text:         "hello world",
			customPrompt: "Format as a haiku",
			expected:     "Format as a haiku\n\nText to process:\nhello world",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := BuildUserPrompt(tc.text, tc.customPrompt)
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestNewAdapter(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name:    "builds adapter for a hosted provider",
			cfg:     Config{Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "sk-test-key", Model: "gpt-4o-mini", RequiresAPIKey: true},
			wantErr: false,
		},
		{
			name:    "builds adapter for a keyless local endpoint",
			cfg:     Config{Provider: "openai-compatible", BaseURL: "http://localhost:11434/v1", Model: "qwen3:8b"},
			wantErr: false,
		},
		{
			name:    "rejects missing base URL",
			cfg:     Config{Provider: "openai", APIKey: "sk-test-key", Model: "gpt-4o-mini"},
			wantErr: true,
		},
		{
			name:    "rejects missing model",
			cfg:     Config{Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "sk-test-key"},
			wantErr: true,
		},
		{
			name:    "rejects missing key when the provider needs one",
			cfg:     Config{Provider: "openai", BaseURL: "https://api.openai.com/v1", Model: "gpt-4o-mini", RequiresAPIKey: true},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			adapter, err := NewAdapter(tc.cfg)

			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if _, ok := adapter.(*ChatAdapter); !ok {
				t.Errorf("expected *ChatAdapter, got %T", adapter)
			}
		})
	}
}

func TestStripPreamble(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "leaves clean output alone",
			input:    "The meeting is at three.",
			expected: "The meeting is at three.",
		},
		{
			name:     "removes a conversational lead-in",
			input:    "Sure, here's the cleaned-up text:\nThe meeting is at three.",
			expected: "The meeting is at three.",
		},
		{
			name:     "removes a code fence",
			input:    "```\nThe meeting is at three.\n```",
			expected: "The meeting is at three.",
		},
		{
			name:     "removes a lead-in wrapping a fence",
			input:    "Here is the corrected text:\n```\nThe meeting is at three.\n```",
			expected: "The meeting is at three.",
		},
		{
			name:     "keeps dictation that merely starts with here is the",
			input:    "Here is the thing:\nI never agreed to that.",
			expected: "Here is the thing:\nI never agreed to that.",
		},
		{
			name:     "keeps a colon line that is not a known lead-in",
			input:    "Shopping list:\nbread, milk",
			expected: "Shopping list:\nbread, milk",
		},
		{
			name:     "keeps multi-line dictation",
			input:    "First point.\nSecond point.",
			expected: "First point.\nSecond point.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := StripPreamble(tc.input)
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
		})
	}
}

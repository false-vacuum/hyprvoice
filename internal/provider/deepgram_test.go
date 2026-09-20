package provider

import "testing"

func TestDeepgramProvider(t *testing.T) {
	p := GetProvider("deepgram")
	if p == nil {
		t.Fatal("deepgram provider not registered")
	}

	if p.Name() != "deepgram" {
		t.Errorf("Name() = %q, want %q", p.Name(), "deepgram")
	}

	if !p.RequiresAPIKey() {
		t.Error("RequiresAPIKey() should return true")
	}

	if p.IsLocal() {
		t.Error("IsLocal() should return false")
	}
}

func TestDeepgramProvider_Models(t *testing.T) {
	// Flux is a separate API from the v1 nova models, with its own adapter and
	// no batch mode, so capabilities are asserted per model.
	want := map[string]struct {
		adapterType   string
		supportsBatch bool
	}{
		"nova-3":          {adapterType: AdapterDeepgram, supportsBatch: true},
		"nova-2":          {adapterType: AdapterDeepgram, supportsBatch: true},
		"flux-general-en": {adapterType: AdapterDeepgramFlux, supportsBatch: false},
	}

	p := &DeepgramProvider{}
	models := p.Models()

	if len(models) != len(want) {
		t.Errorf("Models() returned %d models, want %d", len(models), len(want))
	}

	for _, m := range models {
		expected, ok := want[m.ID]
		if !ok {
			t.Errorf("unexpected model %s", m.ID)
			continue
		}
		if !m.SupportsStreaming {
			t.Errorf("model %s should support streaming", m.ID)
		}
		if m.SupportsBatch != expected.supportsBatch {
			t.Errorf("model %s SupportsBatch = %v, want %v", m.ID, m.SupportsBatch, expected.supportsBatch)
		}
		if m.AdapterType != expected.adapterType {
			t.Errorf("model %s has AdapterType %q, want %q", m.ID, m.AdapterType, expected.adapterType)
		}
		if m.Local {
			t.Errorf("model %s should not be local", m.ID)
		}
	}
}

func TestDeepgramProvider_FluxIsEnglishOnly(t *testing.T) {
	p := &DeepgramProvider{}

	flux := findModel(t, p.Models(), "flux-general-en")

	supported := map[string]bool{"en": true, "en-US": true, "": true, "es": false, "de": false, "multi": false}
	for code, want := range supported {
		if got := flux.SupportsLanguage(code); got != want {
			t.Errorf("flux-general-en.SupportsLanguage(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestDeepgramProvider_Nova3Languages(t *testing.T) {
	p := &DeepgramProvider{}

	nova3 := findModel(t, p.Models(), "nova-3")

	// nova-3 should support many languages from our list
	supportedTests := []struct {
		code string
		want bool
	}{
		{"en", true},
		{"es", true},
		{"fr", true},
		{"de", true},
		{"ja", true},
		{"", true}, // auto always supported
	}

	for _, tt := range supportedTests {
		got := nova3.SupportsLanguage(tt.code)
		if got != tt.want {
			t.Errorf("nova-3.SupportsLanguage(%q) = %v, want %v", tt.code, got, tt.want)
		}
	}
}

func TestDeepgramProvider_DefaultModel(t *testing.T) {
	p := &DeepgramProvider{}

	if got := p.DefaultModel(Transcription); got != "nova-3" {
		t.Errorf("DefaultModel(Transcription) = %q, want 'nova-3'", got)
	}

	if got := p.DefaultModel(LLM); got != "" {
		t.Errorf("DefaultModel(LLM) = %q, want empty (no LLM support)", got)
	}
}

func TestDeepgramProvider_V1Endpoints(t *testing.T) {
	p := &DeepgramProvider{}

	for _, id := range []string{"nova-3", "nova-2"} {
		m := findModel(t, p.Models(), id)

		// batch endpoint (HTTP)
		if m.Endpoint == nil {
			t.Fatalf("model %s has nil Endpoint", m.ID)
		}
		if m.Endpoint.BaseURL != "https://api.deepgram.com" {
			t.Errorf("model %s has Endpoint.BaseURL %q, want 'https://api.deepgram.com'", m.ID, m.Endpoint.BaseURL)
		}
		if m.Endpoint.Path != "/v1/listen" {
			t.Errorf("model %s has Endpoint.Path %q, want '/v1/listen'", m.ID, m.Endpoint.Path)
		}

		// streaming endpoint (WebSocket)
		if m.StreamingEndpoint == nil {
			t.Fatalf("model %s has nil StreamingEndpoint", m.ID)
		}
		if m.StreamingEndpoint.BaseURL != "wss://api.deepgram.com" {
			t.Errorf("model %s has StreamingEndpoint.BaseURL %q, want 'wss://api.deepgram.com'", m.ID, m.StreamingEndpoint.BaseURL)
		}
		if m.StreamingEndpoint.Path != "/v1/listen" {
			t.Errorf("model %s has StreamingEndpoint.Path %q, want '/v1/listen'", m.ID, m.StreamingEndpoint.Path)
		}
	}
}

// Flux speaks a different API on a different path, and has no batch mode to
// point a second endpoint at.
func TestDeepgramProvider_FluxEndpoint(t *testing.T) {
	p := &DeepgramProvider{}

	flux := findModel(t, p.Models(), "flux-general-en")

	if flux.Endpoint == nil {
		t.Fatal("flux-general-en has nil Endpoint")
	}
	if flux.Endpoint.BaseURL != "wss://api.deepgram.com" {
		t.Errorf("Endpoint.BaseURL = %q, want 'wss://api.deepgram.com'", flux.Endpoint.BaseURL)
	}
	if flux.Endpoint.Path != "/v2/listen" {
		t.Errorf("Endpoint.Path = %q, want '/v2/listen'", flux.Endpoint.Path)
	}
	if flux.StreamingEndpoint != nil {
		t.Errorf("StreamingEndpoint = %+v, want nil so streaming uses Endpoint", flux.StreamingEndpoint)
	}
}

// findModel returns the named model from a catalog, failing the test if absent.
func findModel(t *testing.T, models []Model, id string) *Model {
	t.Helper()

	for i := range models {
		if models[i].ID == id {
			return &models[i]
		}
	}
	t.Fatalf("model %s not found", id)
	return nil
}

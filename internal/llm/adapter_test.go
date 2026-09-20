package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestChatAdapterAgainstCompatibleServer covers the custom-endpoint path end
// to end: a keyless local server, the OpenAI request shape, and the cleanup of
// a chatty response.
func TestChatAdapterAgainstCompatibleServer(t *testing.T) {
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{"message": map[string]string{"role": "assistant", "content": "Sure, here's the cleaned text:\n```\nThe meeting is at three.\n```"}},
			},
		})
	}))
	defer srv.Close()

	adapter, err := NewAdapter(Config{
		Provider: "openai-compatible",
		BaseURL:  srv.URL + "/v1",
		Model:    "qwen3:8b",
	})
	if err != nil {
		t.Fatalf("NewAdapter: %v", err)
	}

	out, err := adapter.Process(context.Background(), "um the the meeting is at three")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	if gotPath != "/v1/chat/completions" {
		t.Errorf("expected path /v1/chat/completions, got %s", gotPath)
	}
	// A local server is given no credentials, so no header should be sent.
	if gotAuth != "" {
		t.Errorf("expected no Authorization header, got %q", gotAuth)
	}
	if !strings.Contains(gotBody, `"qwen3:8b"`) {
		t.Errorf("expected model id in request body, got %s", gotBody)
	}
	if out != "The meeting is at three." {
		t.Errorf("expected preamble and fence stripped, got %q", out)
	}
}

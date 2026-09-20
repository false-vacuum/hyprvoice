package transcriber

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leonardotrapani/hyprvoice/internal/provider"
)

func TestDeepgramFluxAdapter_ImplementsStreamingAdapter(t *testing.T) {
	var _ StreamingAdapter = (*DeepgramFluxAdapter)(nil)
}

func TestDeepgramFluxAdapter_Creation(t *testing.T) {
	// Arrange
	endpoint := &provider.EndpointConfig{BaseURL: "wss://api.deepgram.com", Path: "/v2/listen"}

	// Act
	adapter := NewDeepgramFluxAdapter(endpoint, "test-api-key", "flux-general-en", nil)

	// Assert
	if adapter.apiKey != "test-api-key" {
		t.Errorf("apiKey = %q, want %q", adapter.apiKey, "test-api-key")
	}
	if adapter.model != "flux-general-en" {
		t.Errorf("model = %q, want %q", adapter.model, "flux-general-en")
	}
	if adapter.maxRetries != 3 {
		t.Errorf("maxRetries = %d, want %d", adapter.maxRetries, 3)
	}
}

func TestDeepgramFluxAdapter_BuildURL(t *testing.T) {
	tests := []struct {
		name     string
		keywords []string
		wantURL  []string // URL must contain all these substrings
		wantNot  []string // URL must not contain any of these substrings
	}{
		{
			name:    "audio format",
			wantURL: []string{"model=flux-general-en", "encoding=linear16", "sample_rate=16000"},
		},
		{
			name: "no v1 transcription options",
			// Flux has no equivalent of these: turn detection and formatting
			// are part of the model.
			wantNot: []string{"interim_results", "smart_format", "punctuate", "endpointing", "language"},
		},
		{
			name:     "keyterms",
			keywords: []string{"hyprvoice", "wayland compositor"},
			wantURL:  []string{"keyterm=hyprvoice", "keyterm=wayland+compositor"},
			wantNot:  []string{"keywords="},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			endpoint := &provider.EndpointConfig{BaseURL: "wss://api.deepgram.com", Path: "/v2/listen"}
			adapter := NewDeepgramFluxAdapter(endpoint, "test-key", "flux-general-en", tt.keywords)

			// Act
			url, err := adapter.buildURL()
			if err != nil {
				t.Fatalf("buildURL() error = %v", err)
			}

			// Assert
			for _, want := range tt.wantURL {
				if !strings.Contains(url, want) {
					t.Errorf("buildURL() = %q, want to contain %q", url, want)
				}
			}
			for _, notWant := range tt.wantNot {
				if strings.Contains(url, notWant) {
					t.Errorf("buildURL() = %q, want it not to contain %q", url, notWant)
				}
			}
		})
	}
}

func TestDeepgramFluxAdapter_SendChunkNotStarted(t *testing.T) {
	// Arrange
	endpoint := &provider.EndpointConfig{BaseURL: "wss://api.deepgram.com", Path: "/v2/listen"}
	adapter := NewDeepgramFluxAdapter(endpoint, "test-key", "flux-general-en", nil)

	// Act
	err := adapter.SendChunk([]byte("audio data"))

	// Assert
	if err == nil {
		t.Fatal("SendChunk() should return error when adapter not started")
	}
	if !strings.Contains(err.Error(), "not started") {
		t.Errorf("error should mention 'not started', got: %v", err)
	}
}

func TestDeepgramFluxAdapter_CloseNotStarted(t *testing.T) {
	// Arrange
	endpoint := &provider.EndpointConfig{BaseURL: "wss://api.deepgram.com", Path: "/v2/listen"}
	adapter := NewDeepgramFluxAdapter(endpoint, "test-key", "flux-general-en", nil)

	// Act / Assert
	if err := adapter.Close(); err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}

// A turn's transcript is cumulative, so every event before EndOfTurn is an
// interim result carrying the whole turn so far.
func TestDeepgramFluxAdapter_TurnEventsBecomeResults(t *testing.T) {
	// Arrange
	turn := []fluxMessage{
		{Type: "TurnInfo", Event: fluxEventStartOfTurn, Transcript: "Hi I"},
		{Type: "TurnInfo", Event: fluxEventUpdate, Transcript: "Hi I need to"},
		{Type: "TurnInfo", Event: fluxEventEagerEndOfTurn, Transcript: "Hi I need to cancel"},
		{Type: "TurnInfo", Event: fluxEventTurnResumed, Transcript: "Hi I need to cancel my subscription"},
		{Type: "TurnInfo", Event: fluxEventEndOfTurn, Trigger: "model", Transcript: "Hi I need to cancel my subscription."},
	}

	adapter, cleanup := startFluxAdapter(t, func(conn *websocket.Conn) {
		_ = conn.WriteJSON(fluxMessage{Type: "Connected", RequestID: "test-123"})
		for _, msg := range turn {
			_ = conn.WriteJSON(msg)
		}
		time.Sleep(50 * time.Millisecond)
	})
	defer cleanup()

	// Act
	results := collectFluxResults(t, adapter, len(turn))

	// Assert
	want := []TranscriptionResult{
		{Text: "Hi I", IsFinal: false},
		{Text: "Hi I need to", IsFinal: false},
		{Text: "Hi I need to cancel", IsFinal: false},
		{Text: "Hi I need to cancel my subscription", IsFinal: false},
		{Text: "Hi I need to cancel my subscription.", IsFinal: true},
	}
	for i, wantResult := range want {
		if results[i].Text != wantResult.Text || results[i].IsFinal != wantResult.IsFinal {
			t.Errorf("result %d = {%q, final=%v}, want {%q, final=%v}",
				i, results[i].Text, results[i].IsFinal, wantResult.Text, wantResult.IsFinal)
		}
	}
}

func TestDeepgramFluxAdapter_ErrorBecomesResultError(t *testing.T) {
	// Arrange
	adapter, cleanup := startFluxAdapter(t, func(conn *websocket.Conn) {
		_ = conn.WriteJSON(fluxMessage{
			Type:        "Error",
			Code:        "INVALID_AUDIO",
			Description: "audio could not be decoded",
		})
		time.Sleep(50 * time.Millisecond)
	})
	defer cleanup()

	// Act
	results := collectFluxResults(t, adapter, 1)

	// Assert
	if results[0].Error == nil {
		t.Fatalf("result = %+v, want an error", results[0])
	}
	for _, want := range []string{"INVALID_AUDIO", "audio could not be decoded"} {
		if !strings.Contains(results[0].Error.Error(), want) {
			t.Errorf("error = %v, want it to mention %q", results[0].Error, want)
		}
	}
}

// A ForceEndTurn that races the model's own end of turn comes back as a
// warning, which is routine and must not reach the user as a failure.
func TestDeepgramFluxAdapter_WarningIsNotAnError(t *testing.T) {
	// Arrange
	adapter, cleanup := startFluxAdapter(t, func(conn *websocket.Conn) {
		_ = conn.WriteJSON(fluxMessage{
			Type:        "Warning",
			Code:        "FORCE_END_TURN_NO_ACTIVE_TURN",
			Description: "no turn in progress",
		})
		_ = conn.WriteJSON(fluxMessage{Type: "TurnInfo", Event: fluxEventEndOfTurn, Transcript: "hello"})
		time.Sleep(50 * time.Millisecond)
	})
	defer cleanup()

	// Act
	results := collectFluxResults(t, adapter, 1)

	// Assert: only the transcript arrives, the warning is swallowed
	if results[0].Error != nil {
		t.Errorf("result error = %v, want the warning to be ignored", results[0].Error)
	}
	if results[0].Text != "hello" {
		t.Errorf("result text = %q, want %q", results[0].Text, "hello")
	}
}

// Releasing the key ends the dictation mid-turn as far as Flux is concerned.
func TestDeepgramFluxAdapter_FinalizeForcesEndOfTurn(t *testing.T) {
	// Arrange
	var mu sync.Mutex
	var clientMessages []string

	adapter, cleanup := startFluxAdapter(t, func(conn *websocket.Conn) {
		_ = conn.WriteJSON(fluxMessage{Type: "TurnInfo", Event: fluxEventStartOfTurn, Transcript: "hello wor"})

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}

			var msg fluxControlMessage
			if err := json.Unmarshal(data, &msg); err != nil || msg.Type == "" {
				continue
			}

			mu.Lock()
			clientMessages = append(clientMessages, msg.Type)
			mu.Unlock()

			if msg.Type == "ForceEndTurn" {
				_ = conn.WriteJSON(fluxMessage{
					Type: "TurnInfo", Event: fluxEventEndOfTurn, Trigger: "manual", Transcript: "hello world",
				})
			}
		}
	})
	defer cleanup()

	// wait for the turn to open, so Finalize has something to force
	results := collectFluxResults(t, adapter, 1)
	if results[0].Text != "hello wor" {
		t.Fatalf("first result = %q, want the interim draft", results[0].Text)
	}

	// Act
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := adapter.Finalize(ctx); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}

	// Assert
	final := collectFluxResults(t, adapter, 1)[0]
	if !final.IsFinal || final.Text != "hello world" {
		t.Errorf("final result = {%q, final=%v}, want {%q, final=true}", final.Text, final.IsFinal, "hello world")
	}

	// the server records CloseStream when it reads it, which can trail the
	// final result the test already has in hand
	sent := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), clientMessages...)
	}
	waitFor(t, func() bool { return len(sent()) >= 2 })

	if got := sent(); got[0] != "ForceEndTurn" || got[1] != "CloseStream" {
		t.Errorf("client sent %v, want ForceEndTurn then CloseStream", got)
	}
}

// Without the salvage path, the tail of what the user said would be lost
// whenever the turn does not close.
func TestDeepgramFluxAdapter_FinalizeKeepsUnclosedTurn(t *testing.T) {
	// Arrange: a server that never answers ForceEndTurn
	adapter, cleanup := startFluxAdapter(t, func(conn *websocket.Conn) {
		_ = conn.WriteJSON(fluxMessage{Type: "TurnInfo", Event: fluxEventUpdate, Transcript: "never finished"})

		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})
	defer cleanup()

	collectFluxResults(t, adapter, 1) // the interim draft

	// Act
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_ = adapter.Finalize(ctx)

	// Assert
	final := collectFluxResults(t, adapter, 1)[0]
	if !final.IsFinal || final.Text != "never finished" {
		t.Errorf("final result = {%q, final=%v}, want the draft kept as {%q, final=true}",
			final.Text, final.IsFinal, "never finished")
	}
}

func TestDeepgramFluxAdapter_FinalizeNotStarted(t *testing.T) {
	// Arrange
	endpoint := &provider.EndpointConfig{BaseURL: "wss://api.deepgram.com", Path: "/v2/listen"}
	adapter := NewDeepgramFluxAdapter(endpoint, "test-key", "flux-general-en", nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Act / Assert
	if err := adapter.Finalize(ctx); err != nil {
		t.Errorf("Finalize() error = %v, want nil", err)
	}
}

// startFluxAdapter runs a mock Flux server with the given handler and returns a
// started adapter pointed at it, plus a cleanup that closes both.
func startFluxAdapter(t *testing.T, handler func(*websocket.Conn)) (*DeepgramFluxAdapter, func()) {
	t.Helper()

	server := mockDeepgramServer(t, handler)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	endpoint := &provider.EndpointConfig{BaseURL: wsURL, Path: ""}
	adapter := NewDeepgramFluxAdapter(endpoint, "test-api-key", "flux-general-en", nil)

	if err := adapter.Start(context.Background(), ""); err != nil {
		server.Close()
		t.Fatalf("Start() error = %v", err)
	}

	return adapter, func() {
		_ = adapter.Close()
		server.Close()
	}
}

// collectFluxResults reads exactly n results, failing the test if they do not
// arrive in time.
func collectFluxResults(t *testing.T, adapter *DeepgramFluxAdapter, n int) []TranscriptionResult {
	t.Helper()

	results := make([]TranscriptionResult, 0, n)
	timeout := time.After(2 * time.Second)

	for len(results) < n {
		select {
		case result, ok := <-adapter.Results():
			if !ok {
				t.Fatalf("results channel closed after %d of %d results", len(results), n)
			}
			results = append(results, result)
		case <-timeout:
			t.Fatalf("timed out after %d of %d results", len(results), n)
		}
	}
	return results
}

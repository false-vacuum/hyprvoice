package transcriber

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/leonardotrapani/hyprvoice/internal/provider"
)

// fluxFinalizeTimeout bounds the wait for the EndOfTurn that answers a
// ForceEndTurn. The caller's context is the recording timeout, which is
// minutes long, so waiting on it alone would hang injection if the turn never
// closes.
const fluxFinalizeTimeout = 3 * time.Second

// Turn lifecycle events carried by a TurnInfo message.
// See https://developers.deepgram.com/docs/flux/state
const (
	fluxEventStartOfTurn    = "StartOfTurn"
	fluxEventUpdate         = "Update"
	fluxEventEagerEndOfTurn = "EagerEndOfTurn"
	fluxEventTurnResumed    = "TurnResumed"
	fluxEventEndOfTurn      = "EndOfTurn"
)

// DeepgramFluxAdapter implements StreamingAdapter for Deepgram Flux, the
// turn-based streaming API on /v2/listen.
//
// Flux is a different protocol from the v1 models, not a variant of them. It
// reports no is_final/speech_final per result; instead it emits TurnInfo
// events describing the lifecycle of a conversational turn, and the transcript
// in each event is cumulative for that turn rather than incremental. Only
// EndOfTurn is final, so every other event becomes an interim result carrying
// the whole turn so far.
type DeepgramFluxAdapter struct {
	endpoint  *provider.EndpointConfig
	apiKey    string
	model     string
	keywords  []string
	conn      *websocket.Conn
	resultsCh chan TranscriptionResult
	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	wg        sync.WaitGroup
	started   bool

	// reconnection config
	maxRetries  int
	retryDelays []time.Duration

	// turn state: the cumulative transcript of the turn currently in progress,
	// kept so a turn that never closes cleanly can still be salvaged
	turnOpen bool
	pending  string

	// finalization signaling
	finalizeDone chan struct{}
	finalizing   bool // true when Finalize() has been called
	closeResults sync.Once
}

// fluxControlMessage is a client message on the control channel
// (CloseStream, ForceEndTurn).
type fluxControlMessage struct {
	Type string `json:"type"`
}

// fluxMessage is a server message on /v2/listen.
//
// Only the fields this adapter acts on are parsed. Confidences and audio
// window bounds are left out on purpose: the API reference renders them
// inconsistently as numbers or strings, and a type mismatch on a field nobody
// reads would cost the whole message.
type fluxMessage struct {
	Type        string `json:"type"`
	RequestID   string `json:"request_id,omitempty"`
	Event       string `json:"event,omitempty"`
	TurnIndex   int    `json:"turn_index,omitempty"`
	Transcript  string `json:"transcript,omitempty"`
	Trigger     string `json:"trigger,omitempty"`
	Code        string `json:"code,omitempty"`
	Description string `json:"description,omitempty"`
}

// NewDeepgramFluxAdapter creates a new streaming adapter for Deepgram Flux.
// endpoint: the WebSocket endpoint config (wss://api.deepgram.com, /v2/listen)
// apiKey: Deepgram API key
// model: model ID (e.g., "flux-general-en")
func NewDeepgramFluxAdapter(endpoint *provider.EndpointConfig, apiKey, model string, keywords []string) *DeepgramFluxAdapter {
	return &DeepgramFluxAdapter{
		endpoint:     endpoint,
		apiKey:       apiKey,
		model:        model,
		keywords:     keywords,
		resultsCh:    make(chan TranscriptionResult, 100),
		maxRetries:   3,
		retryDelays:  defaultRetryDelays,
		finalizeDone: make(chan struct{}, 1),
	}
}

// Start initiates the WebSocket connection to Deepgram Flux.
// The lang argument is ignored: flux-general-en is English-only.
func (a *DeepgramFluxAdapter) Start(ctx context.Context, lang string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.started {
		return fmt.Errorf("adapter already started")
	}

	a.ctx, a.cancel = context.WithCancel(ctx)

	if err := a.connectLocked(); err != nil {
		return err
	}
	a.started = true

	a.wg.Add(1)
	go a.readLoop()

	log.Printf("deepgram flux: connected, model=%s", a.model)
	return nil
}

// connectLocked establishes WebSocket connection. Must be called with mu held.
func (a *DeepgramFluxAdapter) connectLocked() error {
	wsURL, err := a.buildURL()
	if err != nil {
		return fmt.Errorf("build websocket url: %w", err)
	}

	headers := http.Header{}
	headers.Set("Authorization", "Token "+a.apiKey)

	log.Printf("deepgram flux: connecting to %s", wsURL)
	conn, resp, err := websocket.DefaultDialer.DialContext(a.ctx, wsURL, headers)
	if err != nil {
		if resp != nil {
			log.Printf("deepgram flux: dial failed with status %d", resp.StatusCode)
		}
		return fmt.Errorf("websocket dial: %w", err)
	}
	a.conn = conn
	return nil
}

// reconnect attempts to re-establish the WebSocket connection with exponential backoff.
// Returns true if reconnection succeeded.
func (a *DeepgramFluxAdapter) reconnect() bool {
	for attempt := 0; attempt < a.maxRetries; attempt++ {
		select {
		case <-a.ctx.Done():
			return false
		default:
		}

		// wait before retry (skip wait on first attempt)
		if attempt > 0 {
			delay := a.retryDelays[attempt-1]
			if attempt-1 >= len(a.retryDelays) {
				delay = a.retryDelays[len(a.retryDelays)-1]
			}
			log.Printf("deepgram flux: reconnect attempt %d/%d after %v", attempt+1, a.maxRetries, delay)

			select {
			case <-a.ctx.Done():
				return false
			case <-time.After(delay):
			}
		} else {
			log.Printf("deepgram flux: reconnect attempt %d/%d", attempt+1, a.maxRetries)
		}

		a.mu.Lock()
		if a.conn != nil {
			a.conn.Close()
			a.conn = nil
		}

		// A new connection starts a new turn, so whatever the old one was
		// midway through is not coming back.
		a.turnOpen = false
		a.pending = ""

		err := a.connectLocked()
		a.mu.Unlock()

		if err == nil {
			log.Printf("deepgram flux: reconnected successfully")
			// notify caller of brief interruption
			select {
			case a.resultsCh <- TranscriptionResult{Error: fmt.Errorf("connection interrupted, reconnected"), IsFinal: false}:
			default:
			}
			return true
		}

		log.Printf("deepgram flux: reconnect failed: %v", err)
	}

	return false
}

// buildURL constructs the WebSocket URL with query parameters.
//
// Flux takes none of the v1 transcription options: interim_results,
// smart_format, punctuate and endpointing have no equivalent, because turn
// detection and formatting are part of the model. Nor does it take a language,
// since flux-general-en is English-only.
func (a *DeepgramFluxAdapter) buildURL() (string, error) {
	baseURL := a.endpoint.BaseURL + a.endpoint.Path

	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base url: %w", err)
	}

	q := u.Query()
	q.Set("model", a.model)
	q.Set("encoding", "linear16") // 16-bit linear PCM
	q.Set("sample_rate", "16000") // 16kHz

	addDeepgramKeywords(q, a.model, a.keywords)

	u.RawQuery = q.Encode()
	return u.String(), nil
}

// readLoop reads messages from the WebSocket and sends results to the channel
func (a *DeepgramFluxAdapter) readLoop() {
	defer a.wg.Done()

	for {
		select {
		case <-a.ctx.Done():
			return
		default:
		}

		a.mu.Lock()
		conn := a.conn
		a.mu.Unlock()

		if conn == nil {
			// no connection, try to reconnect
			if !a.reconnect() {
				a.resultsCh <- TranscriptionResult{Error: fmt.Errorf("connection lost, reconnection failed after %d attempts", a.maxRetries)}
				return
			}
			continue
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			select {
			case <-a.ctx.Done():
				return
			default:
			}

			a.mu.Lock()
			finalizing := a.finalizing
			a.mu.Unlock()

			if finalizing {
				// expected close after finalization, signal done and exit gracefully
				select {
				case a.finalizeDone <- struct{}{}:
				default:
				}
				return
			}

			log.Printf("deepgram flux: read error: %v, attempting reconnection", err)
			if !a.reconnect() {
				a.resultsCh <- TranscriptionResult{Error: fmt.Errorf("websocket read: %w, reconnection failed", err)}
				return
			}
			continue
		}

		var msg fluxMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			log.Printf("deepgram flux: parse error: %v", err)
			continue
		}

		switch msg.Type {
		case "Connected":
			log.Printf("deepgram flux: session started, request_id=%s", msg.RequestID)

		case "TurnInfo":
			a.handleTurnInfo(msg)

		case "Error", "FatalError":
			errMsg := msg.Description
			if msg.Code != "" {
				errMsg = fmt.Sprintf("%s: %s", msg.Code, errMsg)
			}
			log.Printf("deepgram flux: error: %s", errMsg)
			a.resultsCh <- TranscriptionResult{Error: fmt.Errorf("deepgram flux: %s", errMsg)}

		case "Warning":
			// Warnings are informational. A ForceEndTurn that races the model's
			// own end of turn reports FORCE_END_TURN_NO_ACTIVE_TURN here, which
			// is normal and must not surface as a transcription error.
			log.Printf("deepgram flux: warning: %s %s", msg.Code, msg.Description)

		default:
			log.Printf("deepgram flux: unknown message type: %s", msg.Type)
		}
	}
}

// handleTurnInfo turns one turn event into a transcription result.
//
// Every event carries the cumulative transcript of its turn, so an unfinished
// turn is reported as an interim result that supersedes the previous one, and
// EndOfTurn is the only final.
func (a *DeepgramFluxAdapter) handleTurnInfo(msg fluxMessage) {
	if msg.Event == fluxEventEndOfTurn {
		a.mu.Lock()
		a.turnOpen = false
		a.pending = ""
		a.mu.Unlock()

		log.Printf("deepgram flux: turn %d ended (%s): %q", msg.TurnIndex, msg.Trigger, msg.Transcript)

		// signal finalization (non-blocking)
		select {
		case a.finalizeDone <- struct{}{}:
		default:
		}

		if msg.Transcript != "" {
			a.resultsCh <- TranscriptionResult{Text: msg.Transcript, IsFinal: true}
		}
		return
	}

	switch msg.Event {
	case fluxEventStartOfTurn, fluxEventUpdate, fluxEventEagerEndOfTurn, fluxEventTurnResumed:
		a.mu.Lock()
		a.turnOpen = true
		a.pending = msg.Transcript
		a.mu.Unlock()

		if msg.Transcript != "" {
			a.resultsCh <- TranscriptionResult{Text: msg.Transcript, IsFinal: false}
		}

	default:
		log.Printf("deepgram flux: unknown turn event: %s", msg.Event)
	}
}

// SendChunk sends raw binary audio to the WebSocket
func (a *DeepgramFluxAdapter) SendChunk(audio []byte) error {
	a.mu.Lock()
	if !a.started {
		a.mu.Unlock()
		return fmt.Errorf("adapter not started")
	}
	conn := a.conn
	a.mu.Unlock()

	select {
	case <-a.ctx.Done():
		return a.ctx.Err()
	default:
	}

	if conn == nil {
		return fmt.Errorf("no connection")
	}

	a.mu.Lock()
	err := a.conn.WriteMessage(websocket.BinaryMessage, audio)
	a.mu.Unlock()

	if err != nil {
		log.Printf("deepgram flux: write error: %v, attempting reconnection", err)
		if a.reconnect() {
			a.mu.Lock()
			err = a.conn.WriteMessage(websocket.BinaryMessage, audio)
			a.mu.Unlock()
			if err == nil {
				return nil
			}
		}
		return fmt.Errorf("websocket write: %w", err)
	}

	return nil
}

// Results returns the channel for receiving transcription results
func (a *DeepgramFluxAdapter) Results() <-chan TranscriptionResult {
	return a.resultsCh
}

// Finalize ends the turn in progress and signals end of audio.
//
// A dictation ends the moment the user releases the key, mid-turn as far as
// Flux is concerned, so ForceEndTurn is sent to close the turn on the audio
// already transcribed. If no EndOfTurn comes back in time, the turn's draft is
// promoted to a final result rather than dropping the user's last words.
func (a *DeepgramFluxAdapter) Finalize(ctx context.Context) error {
	a.mu.Lock()
	if !a.started || a.conn == nil {
		a.mu.Unlock()
		return nil
	}
	turnOpen := a.turnOpen
	a.mu.Unlock()

	// drain any previous finalize signals
	select {
	case <-a.finalizeDone:
	default:
	}

	// mark as finalizing to prevent reconnection attempts on normal close
	a.mu.Lock()
	a.finalizing = true
	a.mu.Unlock()

	if turnOpen {
		if err := a.writeControl(fluxControlMessage{Type: "ForceEndTurn"}); err != nil {
			log.Printf("deepgram flux: force end turn write error: %v", err)
		}
	}

	if err := a.writeControl(fluxControlMessage{Type: "CloseStream"}); err != nil {
		log.Printf("deepgram flux: finalize write error: %v", err)
		return fmt.Errorf("finalize write: %w", err)
	}

	log.Printf("deepgram flux: sent CloseStream, waiting for final transcript")

	timer := time.NewTimer(fluxFinalizeTimeout)
	defer timer.Stop()

	select {
	case <-a.finalizeDone:
		log.Printf("deepgram flux: finalize complete")
		a.flushPendingTurn()
		return nil
	case <-timer.C:
		log.Printf("deepgram flux: finalize timeout")
		a.flushPendingTurn()
		return nil
	case <-ctx.Done():
		a.flushPendingTurn()
		return ctx.Err()
	case <-a.ctx.Done():
		a.flushPendingTurn()
		return a.ctx.Err()
	}
}

// flushPendingTurn promotes an unfinished turn's draft to a final result, so
// speech that Flux never closed a turn around still reaches the transcript.
// Does nothing in the normal case, where EndOfTurn already cleared the draft.
func (a *DeepgramFluxAdapter) flushPendingTurn() {
	a.mu.Lock()
	text := a.pending
	open := a.turnOpen
	a.turnOpen = false
	a.pending = ""
	a.mu.Unlock()

	if !open || text == "" {
		return
	}

	log.Printf("deepgram flux: turn never closed, keeping draft as final: %q", text)
	select {
	case a.resultsCh <- TranscriptionResult{Text: text, IsFinal: true}:
	default:
		log.Printf("deepgram flux: results channel full, dropping salvaged transcript")
	}
}

// writeControl sends one JSON control message.
func (a *DeepgramFluxAdapter) writeControl(msg fluxControlMessage) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.conn == nil {
		return fmt.Errorf("no connection")
	}
	return a.conn.WriteJSON(msg)
}

// Close gracefully closes the WebSocket connection
func (a *DeepgramFluxAdapter) Close() error {
	a.mu.Lock()

	if !a.started {
		a.mu.Unlock()
		return nil
	}

	// mark as finalizing to prevent reconnection attempts
	a.finalizing = true

	// cancel context first to signal reader to stop
	if a.cancel != nil {
		a.cancel()
	}

	conn := a.conn

	a.started = false
	a.mu.Unlock()

	// close websocket outside of lock (readLoop may be blocked on read)
	if conn != nil {
		// send close frame (best effort)
		_ = conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
		conn.Close()
	}

	// wait for reader to finish
	a.wg.Wait()

	// The results channel is closed here rather than by readLoop, because
	// Finalize can still salvage an unfinished turn onto it after the reader
	// has gone.
	a.closeResults.Do(func() { close(a.resultsCh) })

	log.Printf("deepgram flux: closed")
	return nil
}

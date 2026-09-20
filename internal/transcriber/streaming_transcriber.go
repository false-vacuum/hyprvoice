package transcriber

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/leonardotrapani/hyprvoice/internal/recording"
)

// partialBuffer is how many transcript snapshots may queue up for a consumer
// that is not keeping up. Each snapshot supersedes the one before it, so a
// shallow buffer is enough to absorb a brief stall.
const partialBuffer = 16

// StreamingTranscriber wraps a StreamingAdapter and implements the Transcriber interface.
// It streams audio chunks to the adapter in real-time and accumulates transcription results.
type StreamingTranscriber struct {
	adapter  StreamingAdapter
	language string

	// accumulated final text, plus the unconfirmed tail from the most recent
	// interim result
	finalText strings.Builder
	draft     string
	partialCh chan TranscriptUpdate
	mu        sync.Mutex
	fatalErr  error

	// coordination
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewStreamingTranscriber(adapter StreamingAdapter, language string) *StreamingTranscriber {
	return &StreamingTranscriber{
		adapter:   adapter,
		language:  language,
		partialCh: make(chan TranscriptUpdate, partialBuffer),
	}
}

func (t *StreamingTranscriber) Start(ctx context.Context, frameCh <-chan recording.AudioFrame) (<-chan error, error) {
	t.ctx, t.cancel = context.WithCancel(ctx)

	if err := t.adapter.Start(t.ctx, t.language); err != nil {
		t.cancel()
		return nil, err
	}

	errCh := make(chan error, 2)

	// goroutine 1: read audio frames and send to adapter
	t.wg.Add(1)
	go t.sendAudio(frameCh, errCh)

	// goroutine 2: read results from adapter and accumulate
	t.wg.Add(1)
	go t.receiveResults(errCh)

	return errCh, nil
}

func (t *StreamingTranscriber) sendAudio(frameCh <-chan recording.AudioFrame, errCh chan<- error) {
	defer t.wg.Done()

	for {
		select {
		case <-t.ctx.Done():
			return
		case frame, ok := <-frameCh:
			if !ok {
				return
			}
			if err := t.adapter.SendChunk(frame.Data); err != nil {
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					if t.ctx.Err() == nil && t.cancel != nil {
						t.cancel()
					}
					return
				}
				if IsFatalTranscriptionError(err) {
					if t.setFatalErr(err) {
						select {
						case errCh <- err:
						default:
						}
					}
					if t.cancel != nil {
						t.cancel()
					}
					return
				}
				select {
				case errCh <- err:
				default:
				}
				// don't treat send errors as fatal - adapter may handle reconnection
				log.Printf("streaming transcriber: send error: %v", err)
			}
		}
	}
}

func (t *StreamingTranscriber) receiveResults(errCh chan<- error) {
	defer t.wg.Done()

	resultsCh := t.adapter.Results()
	for {
		select {
		case <-t.ctx.Done():
			// context cancelled, drain any remaining results before exiting
			t.drainRemainingResults(resultsCh)
			return
		case result, ok := <-resultsCh:
			if !ok {
				return
			}
			t.processResult(result, errCh)
		}
	}
}

func (t *StreamingTranscriber) processResult(result TranscriptionResult, errCh chan<- error) {
	if result.Error != nil {
		if IsFatalTranscriptionError(result.Error) {
			if t.setFatalErr(result.Error) {
				select {
				case errCh <- result.Error:
				default:
				}
			}
			log.Printf("streaming transcriber: result error: %v", result.Error)
			if t.cancel != nil {
				t.cancel()
			}
			return
		}
		select {
		case errCh <- result.Error:
		default:
		}
		log.Printf("streaming transcriber: result error: %v", result.Error)
		return
	}
	t.recordText(result.Text, result.IsFinal)
}

// recordText folds one result into the transcript and publishes the resulting
// snapshot. A final result is appended to the confirmed text and clears the
// draft; an interim result replaces the draft.
//
// Empty text is ignored: providers emit blank interim results during silence,
// and acting on them would blank the draft and make the display flicker.
func (t *StreamingTranscriber) recordText(text string, isFinal bool) {
	if text == "" {
		return
	}

	t.mu.Lock()
	if isFinal {
		if t.finalText.Len() > 0 {
			t.finalText.WriteString(" ")
		}
		t.finalText.WriteString(text)
		t.draft = ""
	} else {
		t.draft = text
	}
	update := TranscriptUpdate{Final: t.finalText.String(), Draft: t.draft}
	t.mu.Unlock()

	select {
	case t.partialCh <- update:
	default:
		// Deliberately dropped. The next snapshot carries the whole transcript
		// again, so a consumer that falls behind catches up rather than losing
		// text, and transcription never waits on it.
	}
}

// Partials returns the channel of transcript snapshots. It is never closed:
// Stop can run more than once (the pipeline both defers it and calls it when
// injecting), so consumers stop on their own context instead.
func (t *StreamingTranscriber) Partials() <-chan TranscriptUpdate {
	return t.partialCh
}

func (t *StreamingTranscriber) drainRemainingResults(resultsCh <-chan TranscriptionResult) {
	// give a short window to collect any final results already in the channel
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case result, ok := <-resultsCh:
			if !ok {
				return
			}
			// Finals only: a late interim result would revise a tail nobody
			// is going to see, and never reaches GetFinalTranscription.
			if result.IsFinal {
				t.recordText(result.Text, true)
			}
		case <-timeout:
			return
		}
	}
}

func (t *StreamingTranscriber) Stop(ctx context.Context) error {
	// finalize adapter first to commit pending audio and wait for final results
	// this must happen before canceling context so receiveResults can collect them
	if err := t.adapter.Finalize(ctx); err != nil {
		log.Printf("streaming transcriber: finalize error (continuing): %v", err)
	}

	// now cancel context to stop goroutines
	if t.cancel != nil {
		t.cancel()
	}

	// wait for goroutines to finish
	t.wg.Wait()

	// close the adapter
	closeErr := t.adapter.Close()
	if fatalErr := t.getFatalErr(); fatalErr != nil {
		return fatalErr
	}
	return closeErr
}

func (t *StreamingTranscriber) GetFinalTranscription() (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fatalErr != nil {
		return "", t.fatalErr
	}
	return t.finalText.String(), nil
}

func (t *StreamingTranscriber) setFatalErr(err error) bool {
	if err == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.fatalErr != nil {
		return false
	}
	t.fatalErr = err
	return true
}

func (t *StreamingTranscriber) getFatalErr() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.fatalErr
}

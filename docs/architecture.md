# Architecture

This doc describes how the CLI, daemon, pipeline, and adapters compose the system.

## Overview
Hyprvoice is split into a thin CLI and a long-lived daemon. The CLI sends single-character IPC commands to the daemon. The daemon owns lifecycle and runs a pipeline state machine that coordinates recording, transcription, optional LLM cleanup, and text injection.

## Components
- CLI: command parsing and IPC client (`cmd/hyprvoice/main.go`).
- Daemon: IPC server, lifecycle, pipeline ownership (`internal/daemon/daemon.go`).
- Pipeline: state machine orchestration (`internal/pipeline/`).
- Recording: PipeWire capture (`internal/recording/`).
- Transcription: batch + streaming adapters (`internal/transcriber/`).
- LLM post-processing: adapters and prompt builders (`internal/llm/`).
- Injection: wtype/ydotool/clipboard backends (`internal/injection/`).
- Provider registry: model metadata and adapter selection (`internal/provider/`).
- Config manager: load/validate + hot reload (`internal/config/`).

## IPC control plane
The daemon listens on a unix socket and accepts single-character commands.

- Socket path: `~/.cache/hyprvoice/control.sock` (see `internal/bus/bus.go`).
- Command bytes: `t` toggle, `c` cancel, `s` status, `w` watch, `v` version, `q` quit.
- Responses are line-based: `OK ...`, `STATUS ...`, or `ERR ...`.

The CLI writes one command byte and reads the response; the daemon maps commands to pipeline actions.

### Status stream
`w` is the exception: the daemon holds the connection open and writes one JSON event per line until the client disconnects, which is what `hyprvoice status --follow --format json` reads. `bus.SendCommand` covers the request/response commands; `bus.OpenStream` hands back the open connection for this one.

An event (`pipeline.StatusEvent`) carries:

- `status`: the pipeline state.
- `listening`: whether the microphone is open. Separate from `status` because with a streaming transcriber the pipeline sits in `transcribing` for the whole utterance, so status alone cannot say whether the user is still speaking.
- `levels`: normalized 0..1 loudness values for the audio since the last event. Measured only while something is subscribed.
- `transcript`: `final` and `draft`, on events driven by a streaming transcriber.
- `notice`: a user-facing message, on events the daemon publishes in place of a desktop notification.

`internal/daemon/stream.go` fans events out through a hub whose sends never block: a subscriber that stops reading loses events rather than stalling the pipeline that produced them.

## Pipeline state machine
The pipeline is a long-lived goroutine managed by the daemon. It exposes a small interface and uses channels to coordinate actions and notifications.

States (from `internal/pipeline/pipeline.go`):

`idle -> recording -> transcribing -> processing -> injecting -> idle`

Key transitions:
- Toggle while idle: start recorder + transcriber, move to recording/transcribing.
- Inject action: stop recorder, finalize transcription, optional LLM processing, inject text.
- Cancel: stop current action and return to idle.

Key interface (simplified):
- `Pipeline.Run()` starts the pipeline loop.
- `Pipeline.Stop()` stops the current run.
- `Pipeline.GetActionCh()` receives actions (toggle inject).
- `Pipeline.GetNotifyCh()` emits user-facing events.
- `Pipeline.GetErrorCh()` emits errors for the daemon to handle.
- `Pipeline.GetPartialCh()` emits transcript snapshots while a streaming transcriber runs.

## Recording
`internal/recording/recording.go` defines `Recorder` with `Start/Stop/IsRecording`.
The default implementation wraps `pw-record` and emits `AudioFrame` chunks on a buffered channel.

## Transcription
`internal/transcriber/transcriber.go` defines the core interfaces:

- `Transcriber`: lifecycle + `GetFinalTranscription()`.
- `BatchAdapter`: `Transcribe(audio, opts)` for full-file transcription.
- `StreamingAdapter`: `Start/SendChunk/Results/Finalize/Close` for realtime.

`NewTranscriber()` selects between `SimpleTranscriber` (batch) and `StreamingTranscriber` (streaming) based on provider model metadata. Streaming adapters deliver incremental `TranscriptionResult` events and a final transcript on stop/finalize.

### Interim transcripts
`StreamingTranscriber` also implements the optional `PartialTranscriber` interface, publishing a `TranscriptUpdate` every time a result changes the transcript:

- `Final`: everything the provider has confirmed so far.
- `Draft`: the unconfirmed tail, which the next update replaces wholesale rather than extends.

The two are kept apart so a consumer can render confirmed and unconfirmed text differently without tracking state. The pipeline forwards these to `GetPartialCh()`.

Sends are non-blocking at both hops and drop when the buffer is full: each snapshot carries the whole transcript, so a slow consumer is corrected by the next one and transcription is never held up. `SimpleTranscriber` does not implement `PartialTranscriber`, so batch models produce no partials and consumers fall back to status alone.

Adapters that speak a provider's realtime protocol live one per file in `internal/transcriber/`, each owning its own connection handling. `AdapterDeepgram` covers the Deepgram v1 models; `AdapterDeepgramFlux` covers Flux, whose `/v2/listen` turn events are a different protocol rather than a variant of v1 (see `docs/providers.md`).

## LLM post-processing
`internal/llm/llm.go` defines an `Adapter` interface with `Process(text, config)`.
Adapters (OpenAI, Groq) use a shared prompt builder in `internal/llm/prompt.go`.
The pipeline invokes LLM processing only if enabled in config.

## Injection
`internal/injection/injection.go` defines `Injector` and an ordered list of backends.
`internal/injection/backend.go` defines the `Backend` interface (`Name/Available/Inject`).
Backends include:
- `wtype` (Wayland typing)
- `ydotool` (uinput typing)
- `wl-clipboard` fallback

The injector tries backends in order and falls back to clipboard when typing fails.

## Provider registry and adapter selection
Providers register themselves via `internal/provider/provider.go` and return model catalogs.
Each `Model` includes:
- `AdapterType` (which adapter to use)
- `Endpoint` and optional `StreamingEndpoint`
- `SupportedLanguages` and model capabilities

`internal/provider/names.go` holds adapter constants and provider names. `internal/provider/model.go` implements language compatibility checks. `internal/provider/provider.go` exposes helpers like `GetModel`, `ModelsForLanguage`, and `ValidateModelLanguage`.

## Language compatibility
`internal/language/language.go` defines the canonical language list and provider-specific formatting (ex: Deepgram locale mapping). Model-level language filters enforce compatibility at config time and runtime.

## Config lifecycle and hot reload
`internal/config/load.go` loads config, applies defaults, and resolves env-based API keys. `internal/config/validate.go` enforces model/language compatibility and provider requirements. `internal/config/convert.go` converts config into runtime structs for the pipeline.

`internal/config/manager.go` watches `~/.config/hyprvoice/config.toml` and triggers reloads with a debounce. The daemon wires `onConfigReload` to stop any running pipeline, refresh notifiers, and apply new settings without a restart.

## Notifications and errors
The pipeline emits notification events and errors via channels. The daemon consumes them and uses `internal/notify` to display status changes to the user.

`notifications.type` selects the notifier: `desktop` (notify-send), `overlay`, `log`, or `none`.

## Overlay
The overlay is an on-screen indicator near the center of the screen, showing what the daemon is doing and, while a streaming model is running, the transcript as it is recognised: confirmed text at full opacity, the unconfirmed tail dimmed.

It is a separate process (`overlay/hyprvoice-overlay`, Python + GTK4 + gtk4-layer-shell) rather than part of the daemon, because Go has no practical wlr-layer-shell binding. It is an ordinary subscriber to the status stream, so the daemon neither depends on it nor waits for it, and runs unchanged when it is absent.

Two pieces connect them:

- `notify.Overlay` publishes messages onto the status stream instead of shelling out to `notify-send`. It only hands the message over; the overlay draws it.
- `internal/daemon/overlay.go` keeps one overlay process running while `notifications.type` is `overlay`, restarting it if it exits and logging a warning if it is not installed. `HYPRVOICE_OVERLAY_CMD` overrides the command, for running it from a checkout.

The surface takes no keyboard focus and has an empty input region, so it can neither steal input from the window being dictated into nor swallow a click.

## Extending the system
Common extension points:

- Add a new transcription provider:
  - Define a provider catalog in `internal/provider/`.
  - Implement a `BatchAdapter` or `StreamingAdapter` in `internal/transcriber/`.
  - Add adapter constants in `internal/provider/names.go`.
  - Update provider docs in `docs/providers.md`.
  - Run `hyprvoice test-models` to verify the integration (see `docs/testing.md`).

- Add a new injection backend:
  - Implement `Backend` in `internal/injection/`.
  - Register it in the injector order (config driven).

- Add a new LLM adapter:
  - Implement `Adapter` in `internal/llm/`.
  - Wire it in `NewAdapter()` and expose config knobs.

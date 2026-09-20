#!/usr/bin/env python3
"""Checks for the overlay's state folding: python3 overlay/test_state.py

Plain asserts and no test runner, since the overlay ships as a single script
with no Python packaging around it. The GTK parts are not covered here; they
need a compositor.
"""

import importlib.machinery
import importlib.util
import os
import sys
from pathlib import Path

# Skip the LD_PRELOAD re-exec: this imports the module, it does not run the app.
os.environ["HYPRVOICE_OVERLAY_PRELOADED"] = "1"


def load_overlay():
    path = Path(__file__).with_name("hyprvoice-overlay")
    loader = importlib.machinery.SourceFileLoader("hyprvoice_overlay", str(path))
    spec = importlib.util.spec_from_loader(loader.name, loader)
    module = importlib.util.module_from_spec(spec)
    # Registered before exec so dataclasses can resolve the module.
    sys.modules[loader.name] = module
    spec.loader.exec_module(module)
    return module


ov = load_overlay()


def test_stale_event_does_not_resurrect_a_finished_dictation():
    # Arrange: the daemon publishes notices from a different goroutine than
    # pipeline events, so an older event can arrive last.
    state = ov.State()
    state.apply({"status": "transcribing", "listening": True, "at": "2026-09-20T10:22:49.100000-05:00"})

    # Act
    state.apply({"status": "idle", "listening": False, "at": "2026-09-20T10:22:49.806338-05:00"})
    state.apply({"status": "transcribing", "listening": False, "at": "2026-09-20T10:22:49.806219-05:00"})

    # Assert
    assert state.status == "idle", f"stale event won: {state.status}"
    assert not state.visible, "overlay would stay on screen after the dictation ended"


def test_stale_event_still_shows_its_notice():
    # Arrange
    state = ov.State()
    state.apply({"status": "idle", "at": "2026-09-20T10:22:49.900000-05:00"})

    # Act
    state.apply({
        "status": "recording",
        "notice": {"body": "recording failed", "is_error": True},
        "at": "2026-09-20T10:22:49.100000-05:00",
    })

    # Assert
    assert state.error == "recording failed", "a late notice was dropped"
    assert state.status == "idle", f"stale status applied: {state.status}"
    assert state.visible, "an error should keep the overlay up"


def test_a_tap_with_nothing_said_never_shows_typing():
    # Arrange: the pipeline sets Injecting before it knows the transcript is
    # empty, so the overlay must decide this for itself.
    state = ov.State()
    state.apply({"status": "recording", "listening": True})

    # Act: the mic closes with nothing transcribed.
    state.apply({"status": "injecting", "listening": False})

    # Assert
    assert not state.visible, "an empty dictation flashed a card"


def test_an_open_mic_shows_even_with_no_words_yet():
    state = ov.State()

    state.apply({"status": "recording", "listening": True})

    assert state.visible, "the card must be up while the mic is open"


def test_typing_stays_up_when_there_is_text():
    state = ov.State()
    state.apply({"status": "transcribing", "listening": True,
                 "transcript": {"final": "hello there", "draft": ""}})

    state.apply({"status": "injecting", "listening": False})

    assert state.visible, "a real dictation was hidden while being typed"


def test_going_idle_clears_the_transcript():
    # Arrange
    state = ov.State()
    state.apply({"status": "transcribing", "transcript": {"final": "hello", "draft": "wor"}})

    # Act
    state.apply({"status": "idle"})

    # Assert
    assert state.final == "" and state.draft == "", "transcript survived into the next dictation"


def test_draft_is_dimmed_and_replaced_not_appended():
    # Arrange
    state = ov.State()

    # Act
    state.apply({"status": "transcribing", "transcript": {"final": "", "draft": "the quick"}})
    state.apply({"status": "transcribing", "transcript": {"final": "", "draft": "the quick brown"}})

    # Assert
    markup = ov.transcript_markup(state.final, state.draft)
    assert markup == '<span alpha="45%">the quick brown</span>', markup


def test_final_text_is_not_dimmed():
    assert ov.transcript_markup("hello", "wor") == 'hello <span alpha="45%">wor</span>'
    assert ov.transcript_markup("hello", "") == "hello"


def test_markup_is_escaped():
    # A transcript containing markup characters must not break the label.
    assert ov.transcript_markup("a < b & c", "") == "a &lt; b &amp; c"


def test_short_transcript_is_left_alone():
    # Arrange
    final, draft = "hello there", "friend"

    # Act
    kept_final, kept_draft = ov.transcript_tail(final, draft)

    # Assert
    assert (kept_final, kept_draft) == (final, draft)


def test_long_run_on_keeps_only_the_newest_words():
    # Arrange: dictation with no punctuation, well past the budget.
    final = " ".join(f"word{n}" for n in range(200))

    # Act
    kept_final, kept_draft = ov.transcript_tail(final, "")

    # Assert
    assert kept_draft == ""
    assert len(kept_final) <= ov.TRANSCRIPT_BUDGET, len(kept_final)
    assert kept_final.startswith(ov.ELLIPSIS + " "), kept_final
    assert kept_final.endswith("word199"), kept_final


def test_elided_tail_starts_on_a_whole_word():
    # Arrange
    final = "alpha " + "beta " * 60 + "omega"

    # Act
    kept_final, _ = ov.transcript_tail(final, "")

    # Assert
    body = kept_final[len(ov.ELLIPSIS) + 1:]
    assert body.split()[0] in ("beta", "omega"), body[:20]


def test_draft_keeps_the_budget_and_final_gives_way():
    # The draft is the newest text, so it is the part that must stay readable.
    final = " ".join(f"old{n}" for n in range(200))
    draft = "and this is what is being said right now"

    kept_final, kept_draft = ov.transcript_tail(final, draft)

    assert kept_draft == draft, kept_draft
    assert kept_final.startswith(ov.ELLIPSIS), kept_final
    assert len(kept_final) + len(kept_draft) <= ov.TRANSCRIPT_BUDGET


def test_draft_alone_longer_than_the_budget_is_trimmed():
    # Arrange
    draft = " ".join(f"word{n}" for n in range(200))

    # Act
    kept_final, kept_draft = ov.transcript_tail("", draft)

    # Assert
    assert kept_final == ""
    assert len(kept_draft) <= ov.TRANSCRIPT_BUDGET, len(kept_draft)
    assert kept_draft.endswith("word199"), kept_draft


def test_silence_draws_no_bar():
    assert ov.bar_fraction(0.0) == 0.0


def test_room_noise_stays_on_the_floor():
    # Well below the meter's zero, so it reads as flat rather than as a voice.
    assert ov.bar_fraction(0.002) == 0.0


def test_speech_range_spreads_across_the_meter():
    # Arrange: the band a normal speaking voice occupies.
    quiet, mid, loud = ov.bar_fraction(0.02), ov.bar_fraction(0.08), ov.bar_fraction(0.3)

    # Assert: each step is clearly taller, rather than all three sitting flat.
    assert 0.2 < quiet < mid < loud < 1.0, (quiet, mid, loud)
    assert mid - quiet > 0.1, f"too little movement across speech: {quiet} -> {mid}"


def test_full_scale_clamps():
    assert ov.bar_fraction(1.0) == 1.0
    assert ov.bar_fraction(4.0) == 1.0


def test_a_silent_dot_is_at_its_minimum():
    assert ov.dot_diameter(0.0, 1.0) == ov.DOT_MIN_PX


def test_a_dot_at_full_voice_reaches_its_maximum():
    assert ov.dot_diameter(1.0, 1.0) == ov.DOT_MAX_PX


def test_middle_dots_move_more_than_the_outer_ones():
    # The weights are what make the row read as one shape rather than five
    # independent meters.
    outer = ov.dot_diameter(1.0, ov.DOT_WEIGHTS[0])
    middle = ov.dot_diameter(1.0, ov.DOT_WEIGHTS[len(ov.DOT_WEIGHTS) // 2])
    assert outer < middle, (outer, middle)


def test_weights_are_symmetric():
    assert ov.DOT_WEIGHTS == tuple(reversed(ov.DOT_WEIGHTS)), ov.DOT_WEIGHTS


def test_there_is_a_weight_for_every_dot():
    assert len(ov.DOT_WEIGHTS) == ov.DOT_COUNT


def test_levels_keep_only_the_most_recent():
    # Arrange
    state = ov.State()

    # Act: twice as many windows as the meter can hold bars for.
    for _ in range(ov.METER_BARS * ov.WINDOWS_PER_BAR * 2):
        state.apply({"status": "transcribing", "levels": [0.5]})

    # Assert
    assert len(state.levels) == ov.METER_BARS, len(state.levels)


def test_windows_are_averaged_into_one_bar():
    # Arrange
    state = ov.State()
    windows = [0.2, 0.4, 0.6, 0.8][: ov.WINDOWS_PER_BAR]

    # Act
    state.apply({"status": "transcribing", "levels": windows})

    # Assert
    assert state.levels == [sum(windows) / len(windows)], state.levels


def test_a_partial_bar_waits_for_the_rest_of_its_windows():
    # Arrange
    state = ov.State()

    # Act: one window short of a whole bar.
    state.apply({"status": "transcribing", "levels": [0.5] * (ov.WINDOWS_PER_BAR - 1)})

    # Assert
    assert state.levels == [], state.levels

    # Act: the window that completes it.
    state.apply({"status": "transcribing", "levels": [0.5]})

    # Assert
    assert len(state.levels) == 1, state.levels


def test_going_idle_drops_a_half_built_bar():
    # Otherwise the next dictation opens with a bar made of the last one's audio.
    state = ov.State()
    state.apply({"status": "transcribing", "levels": [0.5] * (ov.WINDOWS_PER_BAR - 1)})

    state.apply({"status": "idle"})

    assert state.pending == [], state.pending


def test_listening_distinguishes_an_open_mic():
    # Status alone cannot say: a streaming transcriber sits in "transcribing"
    # for the whole utterance and again while it wraps up.
    state = ov.State()

    state.apply({"status": "transcribing", "listening": True})
    assert state.label == "Listening", state.label

    state.apply({"status": "transcribing", "listening": False})
    assert state.label == "Transcribing", state.label


def main():
    tests = [v for k, v in sorted(globals().items()) if k.startswith("test_")]
    for test in tests:
        test()
        print(f"ok   {test.__name__}")
    print(f"\n{len(tests)} checks passed")


if __name__ == "__main__":
    main()

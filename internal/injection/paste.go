package injection

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"
)

// pasteSettleDelay is how long the paste is given to land before the previous
// clipboard is put back. Restoring immediately would swap the content out from
// under the application as it reads it.
const pasteSettleDelay = 250 * time.Millisecond

// modifierNames maps what a config file says to what wtype expects.
var modifierNames = map[string]string{
	"ctrl":    "ctrl",
	"control": "ctrl",
	"shift":   "shift",
	"alt":     "alt",
	"altgr":   "altgr",
	"super":   "logo",
	"logo":    "logo",
	"win":     "logo",
}

// SelectionPrimary is the mouse-selection buffer, separate from the clipboard
// and generally ignored by clipboard managers, so text pasted through it
// leaves no history entry.
const SelectionPrimary = "primary"

// PasteApp is how one application takes a paste.
type PasteApp struct {
	// Key is the combination that pastes in this app.
	Key string
	// Selection is which buffer it pastes from, "clipboard" or "primary".
	Selection string
}

// UsesPrimary reports whether this app pastes from the primary selection.
func (a PasteApp) UsesPrimary() bool {
	return a.Selection == SelectionPrimary
}

// PasteConfig describes how the paste backend puts text into an application.
type PasteConfig struct {
	// Default is used when the focused app has no entry in Apps, or when the
	// focused app could not be determined at all.
	Default PasteApp
	// Apps maps an app id to how that app pastes, because no single answer is
	// right everywhere: terminals mostly use ctrl+shift+v, or shift+Insert
	// from the primary selection, while the rest of the desktop uses ctrl+v.
	Apps map[string]PasteApp
	// RestoreClipboard puts the previous clipboard contents back afterwards.
	// It does nothing for an app pasting from the primary selection, which
	// never touches the clipboard in the first place.
	RestoreClipboard bool
}

type pasteBackend struct {
	config PasteConfig
}

func NewPasteBackend(config PasteConfig) Backend {
	return &pasteBackend{config: config}
}

func (p *pasteBackend) Name() string {
	return "paste"
}

func (p *pasteBackend) Available() error {
	for _, tool := range []string{"wl-copy", "wtype"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s not found: %w (install wl-clipboard and wtype)", tool, err)
		}
	}
	return nil
}

// Inject copies the text and sends the focused application's paste key.
//
// Two commands regardless of length, where typing costs one key event per
// character: a long dictation lands at once instead of racing whatever the
// user types next.
func (p *pasteBackend) Inject(ctx context.Context, text string, timeout time.Duration) error {
	if text == "" {
		return fmt.Errorf("cannot inject empty text")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	focused := focusedApp(ctx)
	if focused == "" {
		log.Printf("Injection: could not tell which app is focused, pasting with %s", p.config.Default.Key)
	}
	app := p.appFor(focused)
	args, err := wtypeKeyArgs(app.Key)
	if err != nil {
		return err
	}

	// Nothing to save or put back when pasting through the primary selection:
	// the clipboard is never touched.
	previous, hadPrevious := "", false
	if p.config.RestoreClipboard && !app.UsesPrimary() {
		previous, hadPrevious = currentClipboard(ctx)
	}

	if err := exec.CommandContext(ctx, "wl-copy", copyArgs(app, text)...).Run(); err != nil {
		return fmt.Errorf("wl-copy failed: %w", err)
	}

	if err := exec.CommandContext(ctx, "wtype", args...).Run(); err != nil {
		return fmt.Errorf("wtype %s failed: %w", app.Key, err)
	}

	if app.UsesPrimary() {
		return nil
	}
	if hadPrevious {
		p.restore(ctx, previous)
	} else if p.config.RestoreClipboard {
		// There was nothing to put back, so leaving the dictation sitting in
		// the clipboard would be the one case where it outlives the paste.
		p.clear(ctx)
	}
	return nil
}

// copyArgs builds the wl-copy call for one paste.
func copyArgs(app PasteApp, text string) []string {
	args := make([]string, 0, 5)
	if app.UsesPrimary() {
		args = append(args, "--primary")
	} else {
		// Asks clipboard managers not to keep it in their history. Whether
		// one obeys is up to it, which is why primary is preferred where the
		// application supports it.
		args = append(args, "--sensitive")
	}
	return append(args, "--", text)
}

// appFor picks how to paste into an app, falling back to the default when the
// app is unknown or could not be determined.
func (p *pasteBackend) appFor(app string) PasteApp {
	if app != "" {
		if entry, ok := p.config.Apps[strings.ToLower(app)]; ok {
			return entry
		}
	}
	return p.config.Default
}

func (p *pasteBackend) clear(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(pasteSettleDelay):
	}

	// Best effort, as with restore: the text is already delivered.
	_ = exec.CommandContext(ctx, "wl-copy", "--clear").Run()
}

func (p *pasteBackend) restore(ctx context.Context, previous string) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(pasteSettleDelay):
	}

	// Best effort: the text is already delivered, and failing to put the old
	// clipboard back is not worth failing the injection over.
	_ = exec.CommandContext(ctx, "wl-copy", "--", previous).Run()
}

func currentClipboard(ctx context.Context) (string, bool) {
	out, err := exec.CommandContext(ctx, "wl-paste", "--no-newline").Output()
	if err != nil {
		// An empty or non-text clipboard is not an error worth reporting;
		// there is simply nothing to put back.
		return "", false
	}
	return string(out), true
}

// wtypeKeyArgs turns "ctrl+shift+v" into the press and release arguments
// wtype needs, modifiers pressed in order and released in reverse.
func wtypeKeyArgs(combination string) ([]string, error) {
	parts := strings.Split(strings.TrimSpace(combination), "+")
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return nil, fmt.Errorf("invalid paste key %q", combination)
	}

	key := parts[len(parts)-1]
	modifiers := make([]string, 0, len(parts)-1)
	for _, part := range parts[:len(parts)-1] {
		name, ok := modifierNames[strings.ToLower(strings.TrimSpace(part))]
		if !ok {
			return nil, fmt.Errorf("invalid paste key %q: unknown modifier %q", combination, part)
		}
		modifiers = append(modifiers, name)
	}

	args := make([]string, 0, len(modifiers)*4+2)
	for _, modifier := range modifiers {
		args = append(args, "-M", modifier)
	}
	args = append(args, "-k", key)
	for i := len(modifiers) - 1; i >= 0; i-- {
		args = append(args, "-m", modifiers[i])
	}
	return args, nil
}

// ValidatePasteKey reports whether a combination like "ctrl+shift+v" can be
// sent, so a typo in the config fails the load rather than the dictation.
func ValidatePasteKey(combination string) error {
	_, err := wtypeKeyArgs(combination)
	return err
}

package injection

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestWtypeKeyArgs_PressesModifiersAndReleasesInReverse(t *testing.T) {
	// Arrange / Act
	args, err := wtypeKeyArgs("ctrl+shift+v")

	// Assert: wtype holds modifiers until they are released, so the order
	// matters; releasing in the same order can leave one stuck down.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"-M", "ctrl", "-M", "shift", "-k", "v", "-m", "shift", "-m", "ctrl"}
	if !slices.Equal(args, want) {
		t.Errorf("wtypeKeyArgs() = %v, want %v", args, want)
	}
}

func TestWtypeKeyArgs_PlainKeyNeedsNoModifiers(t *testing.T) {
	args, err := wtypeKeyArgs("Insert")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := []string{"-k", "Insert"}; !slices.Equal(args, want) {
		t.Errorf("wtypeKeyArgs() = %v, want %v", args, want)
	}
}

func TestWtypeKeyArgs_NormalisesModifierNames(t *testing.T) {
	// "super" and "win" are what people write; wtype only knows "logo".
	for _, name := range []string{"super", "win", "logo", "SUPER"} {
		args, err := wtypeKeyArgs(name + "+v")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if args[1] != "logo" {
			t.Errorf("%s mapped to %q, want logo", name, args[1])
		}
	}
}

func TestWtypeKeyArgs_RejectsRubbish(t *testing.T) {
	for _, combination := range []string{"", "ctrl+", "meta+v", "   "} {
		if _, err := wtypeKeyArgs(combination); err == nil {
			t.Errorf("%q was accepted, want an error", combination)
		}
	}
}

func TestKeyFor_UsesTheAppsOwnKey(t *testing.T) {
	// Arrange
	backend := &pasteBackend{config: PasteConfig{
		Default: PasteApp{Key: "ctrl+v", Selection: "clipboard"},
		Apps:    map[string]PasteApp{"kitty": {Key: "shift+Insert", Selection: "primary"}},
	}}

	// Act / Assert
	got := backend.appFor("kitty")
	if got.Key != "shift+Insert" {
		t.Errorf("appFor(kitty).Key = %q, want shift+Insert", got.Key)
	}
	if !got.UsesPrimary() {
		t.Error("appFor(kitty) should paste from the primary selection")
	}
}

func TestKeyFor_MatchesRegardlessOfCase(t *testing.T) {
	// Compositors report an app id however the app spelled it: sway says
	// "Alacritty" for the XWayland class but "kitty" for the Wayland app id.
	backend := &pasteBackend{config: PasteConfig{
		Default: PasteApp{Key: "ctrl+v"},
		Apps:    map[string]PasteApp{"alacritty": {Key: "ctrl+shift+v"}},
	}}

	if got := backend.appFor("Alacritty"); got.Key != "ctrl+shift+v" {
		t.Errorf("appFor(Alacritty).Key = %q, want ctrl+shift+v", got.Key)
	}
}

func TestKeyFor_FallsBackWhenFocusIsUnknown(t *testing.T) {
	// An empty app id is the normal answer on a compositor that cannot be
	// queried, and must still produce a working paste.
	backend := &pasteBackend{config: PasteConfig{
		Default: PasteApp{Key: "ctrl+v", Selection: "clipboard"},
		Apps:    map[string]PasteApp{"kitty": {Key: "shift+Insert", Selection: "primary"}},
	}}

	if got := backend.appFor(""); got.Key != "ctrl+v" || got.UsesPrimary() {
		t.Errorf("appFor(unknown) = %+v, want the clipboard default", got)
	}
	if got := backend.appFor("firefox"); got.Key != "ctrl+v" {
		t.Errorf("appFor(firefox).Key = %q, want ctrl+v", got.Key)
	}
}

func TestFindSwayFocus_ReachesANestedWindow(t *testing.T) {
	// Arrange: the focused window is always a leaf, several levels down.
	var tree swayNode
	raw := `{"nodes":[{"nodes":[{"focused":false,"app_id":"firefox"},
	         {"focused":true,"app_id":"kitty"}]}]}`
	if err := json.Unmarshal([]byte(raw), &tree); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}

	// Act / Assert
	if got := findSwayFocus(tree); got != "kitty" {
		t.Errorf("findSwayFocus() = %q, want kitty", got)
	}
}

func TestFindSwayFocus_FallsBackToTheXWaylandClass(t *testing.T) {
	// XWayland clients report no app_id, only a window class.
	var tree swayNode
	raw := `{"nodes":[{"focused":true,"app_id":"","window_properties":{"class":"Alacritty"}}]}`
	if err := json.Unmarshal([]byte(raw), &tree); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}

	if got := findSwayFocus(tree); got != "Alacritty" {
		t.Errorf("findSwayFocus() = %q, want Alacritty", got)
	}
}

func TestFindSwayFocus_FindsFloatingWindows(t *testing.T) {
	var tree swayNode
	raw := `{"nodes":[{"floating_nodes":[{"focused":true,"app_id":"foot"}]}]}`
	if err := json.Unmarshal([]byte(raw), &tree); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}

	if got := findSwayFocus(tree); got != "foot" {
		t.Errorf("findSwayFocus() = %q, want foot", got)
	}
}

func TestFindSwayFocus_ReportsNothingWhenNothingIsFocused(t *testing.T) {
	var tree swayNode
	raw := `{"nodes":[{"focused":false,"app_id":"firefox"}]}`
	if err := json.Unmarshal([]byte(raw), &tree); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}

	if got := findSwayFocus(tree); got != "" {
		t.Errorf("findSwayFocus() = %q, want empty", got)
	}
}

func TestPasteBackend_RefusesEmptyText(t *testing.T) {
	backend := NewPasteBackend(PasteConfig{Default: PasteApp{Key: "ctrl+v"}})
	if err := backend.Inject(t.Context(), "", 0); err == nil {
		t.Error("empty text was accepted")
	}
}

func TestCopyArgs_PrimaryNeverTouchesTheClipboard(t *testing.T) {
	// Arrange: the point of primary is that clipboard managers ignore it.
	app := PasteApp{Key: "shift+Insert", Selection: SelectionPrimary}

	// Act
	args := copyArgs(app, "hello")

	// Assert
	if !slices.Contains(args, "--primary") {
		t.Errorf("copyArgs() = %v, want --primary", args)
	}
	if slices.Contains(args, "--sensitive") {
		t.Errorf("copyArgs() = %v, --sensitive is meaningless for primary", args)
	}
}

func TestCopyArgs_ClipboardAsksManagersNotToKeepIt(t *testing.T) {
	app := PasteApp{Key: "ctrl+v", Selection: "clipboard"}

	args := copyArgs(app, "hello")

	if !slices.Contains(args, "--sensitive") {
		t.Errorf("copyArgs() = %v, want --sensitive", args)
	}
	if slices.Contains(args, "--primary") {
		t.Errorf("copyArgs() = %v, want the clipboard", args)
	}
}

func TestCopyArgs_PassesTextAfterASeparator(t *testing.T) {
	// A dictation starting with a dash must not be read as a flag.
	args := copyArgs(PasteApp{Key: "ctrl+v"}, "-- not a flag")

	if args[len(args)-2] != "--" || args[len(args)-1] != "-- not a flag" {
		t.Errorf("copyArgs() = %v, want the text last, after --", args)
	}
}

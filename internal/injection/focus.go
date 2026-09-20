package injection

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"time"
)

// focusTimeout bounds the query for the focused window. It runs on every
// injection, so a wedged compositor must not hold up the text.
const focusTimeout = 400 * time.Millisecond

// focusedApp reports the app id of the window that will receive the text, or
// an empty string when it cannot be determined.
//
// Every compositor answers this differently and none of it is required: an
// empty answer just means the default paste key is used, so the caller gets a
// working paste either way.
func focusedApp(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, focusTimeout)
	defer cancel()

	for _, query := range []func(context.Context) string{swayFocusedApp, hyprlandFocusedApp} {
		if app := query(ctx); app != "" {
			return app
		}
	}
	return ""
}

// swayNode is the part of sway's tree this needs. Wayland clients report
// app_id; XWayland clients report a window class instead.
type swayNode struct {
	Focused          bool       `json:"focused"`
	AppID            string     `json:"app_id"`
	Nodes            []swayNode `json:"nodes"`
	FloatingNodes    []swayNode `json:"floating_nodes"`
	WindowProperties struct {
		Class string `json:"class"`
	} `json:"window_properties"`
}

func swayFocusedApp(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "swaymsg", "-t", "get_tree", "-r").Output()
	if err != nil {
		return ""
	}

	var tree swayNode
	if err := json.Unmarshal(out, &tree); err != nil {
		return ""
	}
	return findSwayFocus(tree)
}

func findSwayFocus(node swayNode) string {
	if node.Focused {
		if node.AppID != "" {
			return node.AppID
		}
		return node.WindowProperties.Class
	}

	for _, child := range append(append([]swayNode{}, node.Nodes...), node.FloatingNodes...) {
		if app := findSwayFocus(child); app != "" {
			return app
		}
	}
	return ""
}

func hyprlandFocusedApp(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "hyprctl", "activewindow", "-j").Output()
	if err != nil {
		return ""
	}

	var window struct {
		Class string `json:"class"`
	}
	if err := json.Unmarshal(out, &window); err != nil {
		return ""
	}
	return strings.TrimSpace(window.Class)
}

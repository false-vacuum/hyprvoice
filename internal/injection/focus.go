package injection

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	pattern := filepath.Join(runtimeDir, fmt.Sprintf("sway-ipc.%d.*.sock", os.Getuid()))
	for _, socket := range liveSockets(os.Getenv("SWAYSOCK"), pattern) {
		out, err := exec.CommandContext(ctx, "swaymsg", "-s", socket, "-t", "get_tree", "-r").Output()
		if err != nil {
			continue
		}

		var tree swayNode
		if err := json.Unmarshal(out, &tree); err != nil {
			continue
		}
		return findSwayFocus(tree)
	}
	return ""
}

// liveSockets lists the compositor IPC sockets worth trying, the one the
// environment names first.
//
// The daemon runs as a user service, so its environment is whatever the
// compositor session that started it exported. Restarting the compositor
// leaves that pointing at a socket that no longer exists, and the new one is
// found by its naming pattern instead.
func liveSockets(fromEnv, pattern string) []string {
	var sockets []string
	if fromEnv != "" {
		if _, err := os.Stat(fromEnv); err == nil {
			sockets = append(sockets, fromEnv)
		}
	}

	// Glob only fails on a malformed pattern, which leaves just the env socket.
	matches, _ := filepath.Glob(pattern)
	for _, match := range matches {
		if match != fromEnv {
			sockets = append(sockets, match)
		}
	}
	return sockets
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
	hyprDir := filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), "hypr")
	fromEnv := ""
	if signature := os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"); signature != "" {
		fromEnv = filepath.Join(hyprDir, signature, ".socket.sock")
	}

	for _, socket := range liveSockets(fromEnv, filepath.Join(hyprDir, "*", ".socket.sock")) {
		// hyprctl names an instance by the directory its socket lives in.
		signature := filepath.Base(filepath.Dir(socket))
		out, err := exec.CommandContext(ctx, "hyprctl", "--instance", signature, "activewindow", "-j").Output()
		if err != nil {
			continue
		}

		var window struct {
			Class string `json:"class"`
		}
		if err := json.Unmarshal(out, &window); err != nil {
			continue
		}
		return strings.TrimSpace(window.Class)
	}
	return ""
}

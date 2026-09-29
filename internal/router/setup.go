package router

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// shimName is the stable statusline entry point in the plugin data directory.
// The plugin root changes on every update; the data directory does not.
const shimName = "statusline.sh"

// SettingsPath is the user settings file Claude Code reads.
func SettingsPath(env Getenv) string {
	if d := env("CLAUDE_CONFIG_DIR"); d != "" {
		return filepath.Join(d, "settings.json")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "settings.json")
}

// shellQuote wraps s in single quotes for POSIX sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Setup prepares the statusline wrapper and prints the exact settings change
// for Claude to apply with its Edit tool (so the user reviews the diff).
// It writes only inside the plugin data directory.
func Setup(w io.Writer, env Env, root string) error {
	if env.DataDir == "" {
		return errors.New(`data directory unknown: pass --data "${CLAUDE_PLUGIN_DATA}"`)
	}
	if err := os.MkdirAll(env.DataDir, 0o755); err != nil {
		return err
	}
	if root != "" {
		if err := os.WriteFile(filepath.Join(env.DataDir, "root"), []byte(root), 0o644); err != nil {
			return err
		}
	}

	settingsPath := SettingsPath(env.Getenv)
	current, err := readStatusLine(settingsPath)
	if err != nil {
		return err
	}
	shim := filepath.Join(env.DataDir, shimName)

	if current != nil && strings.Contains(fmt.Sprint(current["command"]), shim) {
		fmt.Fprintf(w, "ALREADY INSTALLED: %s statusLine already runs cc-router. Nothing to change.\n", settingsPath)
		return writeShim(shim, env.DataDir)
	}

	// Save the user's statusline so the wrapper can run it and --remove can restore it.
	orig := []byte("null")
	if current != nil {
		orig, _ = json.Marshal(current)
	}
	if err := os.WriteFile(filepath.Join(env.DataDir, origStatusLineFile), orig, 0o644); err != nil {
		return err
	}
	if err := writeShim(shim, env.DataDir); err != nil {
		return err
	}

	proposed := map[string]any{}
	for k, v := range current {
		proposed[k] = v
	}
	proposed["type"] = "command"
	// If the plugin is gone the shim exits non-zero before reading stdin, and
	// the user's own command runs instead.
	cmd := shellQuote(shim)
	if orig := savedStatusCommand(env.DataDir); orig != "" {
		cmd += " || " + orig
	}
	proposed["command"] = cmd
	if _, ok := proposed["refreshInterval"]; !ok {
		proposed["refreshInterval"] = 5
	}

	cur := "(none)"
	if current != nil {
		b, _ := json.MarshalIndent(current, "  ", "  ")
		cur = string(b)
	}
	next, _ := json.MarshalIndent(proposed, "  ", "  ")
	fmt.Fprintf(w, "SETTINGS FILE: %s\n\nCURRENT statusLine:\n  %s\n\nNEW statusLine:\n  %s\n", settingsPath, cur, next)
	fmt.Fprintln(w, "\nYour existing statusline (if any) keeps rendering; cc-router records quota and model from the same input.")
	return nil
}

// Remove prints the statusLine value to restore.
func Remove(w io.Writer, env Env) error {
	if env.DataDir == "" {
		return errors.New(`data directory unknown: pass --data "${CLAUDE_PLUGIN_DATA}"`)
	}
	raw, err := os.ReadFile(filepath.Join(env.DataDir, origStatusLineFile))
	if err != nil {
		return errors.New("no saved statusline: setup was never run from this install")
	}
	settingsPath := SettingsPath(env.Getenv)
	current, err := readStatusLine(settingsPath)
	if err != nil {
		return err
	}
	if current == nil || !strings.Contains(fmt.Sprint(current["command"]), filepath.Join(env.DataDir, shimName)) {
		fmt.Fprintf(w, "NOT INSTALLED: %s statusLine does not run cc-router. Nothing to change.\n", settingsPath)
		return nil
	}
	fmt.Fprintf(w, "SETTINGS FILE: %s\n\n", settingsPath)
	if strings.TrimSpace(string(raw)) == "null" {
		fmt.Fprintln(w, "RESTORE: remove the \"statusLine\" key (there was none before setup).")
		return nil
	}
	var v any
	_ = json.Unmarshal(raw, &v)
	b, _ := json.MarshalIndent(v, "  ", "  ")
	fmt.Fprintf(w, "RESTORE statusLine to:\n  %s\n", b)
	return nil
}

func readStatusLine(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var s struct {
		StatusLine map[string]any `json:"statusLine"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	return s.StatusLine, nil
}

func writeShim(path, dataDir string) error {
	d := shellQuote(dataDir)
	script := `#!/bin/sh
# cc-router statusline entry point (written by /cc-router:setup).
# Exits 1 without reading stdin when the plugin is missing, so a fallback
# command after "||" in settings.json still gets the statusline input.
d=` + d + `
root=$(cat "$d/root" 2>/dev/null)
[ -n "$root" ] && [ -x "$root/bin/cc-router" ] && exec "$root/bin/cc-router" statusline --data "$d"
exit 1
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return err
	}
	return os.Chmod(path, 0o755)
}

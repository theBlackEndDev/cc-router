package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// statuslineInput is the subset of Claude Code's statusline stdin we use.
type statuslineInput struct {
	SessionID string `json:"session_id"`
	Model     struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	RateLimits *struct {
		FiveHour *Window `json:"five_hour"`
		SevenDay *Window `json:"seven_day"`
	} `json:"rate_limits"`
}

// SessionState is what the statusline records per session for the hooks,
// which never see the main model themselves.
type SessionState struct {
	TS      int64  `json:"ts"`
	ModelID string `json:"model_id"`
}

// origStatusLine is the user's statusLine setting saved by setup, if any.
const origStatusLineFile = "statusline-original.json"

// wrappedTimeout bounds the user's own statusline command.
const wrappedTimeout = 5 * time.Second

// Statusline records the quota and session model, then prints the user's own
// statusline (if setup saved one) or a compact default.
func Statusline(stdin io.Reader, stdout io.Writer, env Env) error {
	raw, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	var in statuslineInput
	parseErr := json.Unmarshal(raw, &in)
	now := env.Now()

	var q *Quota
	if parseErr == nil && env.DataDir != "" {
		if in.RateLimits != nil && (in.RateLimits.FiveHour != nil || in.RateLimits.SevenDay != nil) {
			q = &Quota{TS: now.Unix(), FiveHour: in.RateLimits.FiveHour, SevenDay: in.RateLimits.SevenDay}
			_ = WriteJSON(env.DataDir, "quota.json", q)
		}
		if in.SessionID != "" && in.Model.ID != "" {
			_ = WriteJSON(filepath.Join(env.DataDir, "sessions"), safeName(in.SessionID)+".json", SessionState{TS: now.Unix(), ModelID: in.Model.ID})
		}
	}

	var note string
	if q != nil {
		cfg := Load(env.Getenv, env.DataDir)
		if name, w := q.Highest(now); w != nil && w.UsedPercentage >= cfg.DegradeAt {
			note = fmt.Sprintf("cc-router ↓ subagents stepped down (%s %.0f%%)", name, w.UsedPercentage)
		}
	}

	if cmd := savedStatusCommand(env.DataDir); cmd != "" {
		out := runWrapped(cmd, raw)
		stdout.Write(out)
		if note != "" {
			if len(out) > 0 && !bytes.HasSuffix(out, []byte("\n")) {
				fmt.Fprintln(stdout)
			}
			fmt.Fprintln(stdout, note)
		}
		return parseErr
	}

	parts := []string{in.Model.DisplayName}
	if q != nil {
		for _, c := range []struct {
			n string
			w *Window
		}{{"5h", q.FiveHour}, {"7d", q.SevenDay}} {
			if c.w != nil {
				parts = append(parts, fmt.Sprintf("%s %.0f%%", c.n, c.w.UsedPercentage))
			}
		}
	}
	if note != "" {
		parts = append(parts, "↓ step-down")
	}
	fmt.Fprintln(stdout, strings.Join(nonEmpty(parts), " · "))
	return parseErr
}

// savedStatusCommand returns the user's original statusline command, if setup
// saved one and it is a command-type statusline.
func savedStatusCommand(dataDir string) string {
	if dataDir == "" {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, origStatusLineFile))
	if err != nil {
		return ""
	}
	var s struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}
	if json.Unmarshal(raw, &s) != nil || (s.Type != "" && s.Type != "command") {
		return ""
	}
	return s.Command
}

func runWrapped(command string, stdin []byte) []byte {
	ctx, cancel := context.WithTimeout(context.Background(), wrappedTimeout)
	defer cancel()
	c := exec.CommandContext(ctx, "sh", "-c", command)
	c.Stdin = bytes.NewReader(stdin)
	c.Stderr = io.Discard
	out, _ := c.Output() // show whatever it printed, even on error
	return out
}

func nonEmpty(ss []string) []string {
	out := ss[:0]
	for _, s := range ss {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

// safeName keeps session IDs usable as file names.
func safeName(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return r
		}
		return '_'
	}, s)
}

// ReadSession returns the statusline's latest record for a session, or nil.
func ReadSession(dataDir, sessionID string) *SessionState {
	if dataDir == "" || sessionID == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, "sessions", safeName(sessionID)+".json"))
	if err != nil {
		return nil
	}
	var s SessionState
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	return &s
}

// pruneSessions deletes per-session files older than maxAge.
func pruneSessions(dataDir string, now time.Time, maxAge time.Duration) {
	for _, sub := range []string{"sessions", "notices"} {
		dir := filepath.Join(dataDir, sub)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > maxAge {
				os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
}

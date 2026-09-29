package router

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// SwitchRetryWindow is how long a blocked /model switch can be repeated to
// go through anyway.
const SwitchRetryWindow = 60 * time.Second

type level int

const (
	levelNone level = iota
	levelWarn
	levelDegrade
)

func quotaLevel(c Config, w *Window) level {
	switch {
	case w == nil:
		return levelNone
	case w.UsedPercentage >= c.DegradeAt:
		return levelDegrade
	case w.UsedPercentage >= c.WarnAt:
		return levelWarn
	}
	return levelNone
}

// notice records, per session, the highest level already announced for each
// quota window instance (identified by its reset time).
type notice struct {
	ResetsAt int64 `json:"resets_at"`
	Level    level `json:"level"`
}

func resetClock(w *Window) string { return time.Unix(w.ResetsAt, 0).Format("Jan 2 15:04") }

// userPromptSubmit tells Claude (and through it the user) once per session
// when a quota window crosses warn_at or degrade_at. Silent otherwise, so it
// costs nothing on ordinary prompts.
func userPromptSubmit(in *HookInput, env Env) ([]byte, error) {
	if env.DataDir == "" || in.SessionID == "" {
		return nil, nil
	}
	cfg := Load(env.Getenv, env.DataDir)
	name, w := ReadQuota(env.DataDir).Highest(env.Now())
	lvl := quotaLevel(cfg, w)
	if lvl == levelNone {
		return nil, nil
	}

	dir := filepath.Join(env.DataDir, "notices")
	file := safeName(in.SessionID) + ".json"
	seen := map[string]notice{}
	if raw, err := os.ReadFile(filepath.Join(dir, file)); err == nil {
		_ = json.Unmarshal(raw, &seen)
	}
	if prev, ok := seen[name]; ok && prev.ResetsAt == w.ResetsAt && prev.Level >= lvl {
		return nil, nil
	}
	seen[name] = notice{ResetsAt: w.ResetsAt, Level: lvl}
	err := WriteJSON(dir, file, seen)

	var msg string
	if lvl == levelDegrade {
		msg = fmt.Sprintf("cc-router: %s quota window at %.0f%% (resets %s). Subagents are now stepped down one tier.", name, w.UsedPercentage, resetClock(w))
		if s := ReadSession(env.DataDir, in.SessionID); s != nil && Family(s.ModelID) == int(Plan) {
			msg += " The main session is on " + s.ModelID + "; `/model sonnet` would stretch the remaining quota."
		}
	} else {
		msg = fmt.Sprintf("cc-router: %s quota window at %.0f%% (resets %s). Prefer cheaper subagents for recon and mechanical work.", name, w.UsedPercentage, resetClock(w))
	}
	out, mErr := json.Marshal(map[string]any{
		"systemMessage": msg,
		"hookSpecificOutput": map[string]any{
			"hookEventName":     "UserPromptSubmit",
			"additionalContext": msg + " Mention this to the user in one line.",
		},
	})
	if mErr != nil {
		return nil, mErr
	}
	return out, err
}

type pendingSwitch struct {
	To string `json:"to"`
	TS int64  `json:"ts"`
}

// preModelSwitch blocks the first switch to a plan-tier model while quota is
// at or above degrade_at, and lets the same switch through if the user repeats
// it within SwitchRetryWindow. Claude Code hooks can only block, not ask, so
// the repeat is the confirmation. The confirmation lasts the whole window
// rather than being single-use: Claude Code 2.1.284 calls PreModelSwitch more
// than once for a single /model command.
func preModelSwitch(in *HookInput, env Env) (Result, error) {
	target := in.ToModel
	if target == "" {
		target = in.RequestedModel
	}
	cfg := Load(env.Getenv, env.DataDir)
	planRank := Family(cfg.Models[Plan])
	if planRank < 0 {
		planRank = int(Plan)
	}
	if env.DataDir == "" || Family(target) < planRank {
		return Result{}, nil
	}
	name, w := ReadQuota(env.DataDir).Highest(env.Now())
	if quotaLevel(cfg, w) != levelDegrade {
		return Result{}, nil
	}

	now := env.Now()
	file := "switch-" + safeName(in.SessionID) + ".json"
	path := filepath.Join(env.DataDir, file)
	var p pendingSwitch
	if raw, err := os.ReadFile(path); err == nil && json.Unmarshal(raw, &p) == nil &&
		p.To == target && now.Sub(time.Unix(p.TS, 0)) <= SwitchRetryWindow {
		return Result{}, AppendLog(env.DataDir, "decisions.jsonl", map[string]any{
			"event": "model-switch", "session": in.SessionID, "to": target, "allowed": true, "reason": "confirmed by repeat",
		})
	}
	if err := WriteJSON(env.DataDir, file, pendingSwitch{To: target, TS: now.Unix()}); err != nil {
		return Result{}, err // can't record the pending switch, so a repeat could never pass: fail open
	}
	err := AppendLog(env.DataDir, "decisions.jsonl", map[string]any{
		"event": "model-switch", "session": in.SessionID, "to": target, "allowed": false,
		"reason": fmt.Sprintf("%s quota %.0f%%", name, w.UsedPercentage),
	})
	return Result{Block: fmt.Sprintf(
		"cc-router: the %s quota window is at %.0f%% (resets %s) and %s draws it down fastest. Run the same /model command again within %d seconds to switch anyway.",
		name, w.UsedPercentage, resetClock(w), target, int(SwitchRetryWindow.Seconds()))}, err
}

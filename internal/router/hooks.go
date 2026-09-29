package router

import (
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// HookInput is the subset of Claude Code's hook stdin that cc-router reads.
type HookInput struct {
	SessionID      string                     `json:"session_id"`
	HookEventName  string                     `json:"hook_event_name"`
	AgentID        *string                    `json:"agent_id"` // present as null in the main session
	PermissionMode string                     `json:"permission_mode"`
	Source         string                     `json:"source"`
	ToolName       string                     `json:"tool_name"`
	ToolInput      map[string]json.RawMessage `json:"tool_input"`
	ToolResponse   json.RawMessage            `json:"tool_response"`
}

// InMainSession reports whether the hook fired in the main conversation.
func (h *HookInput) InMainSession() bool { return h.AgentID == nil || *h.AgentID == "" }

func (h *HookInput) toolString(key string) string {
	var s string
	if raw, ok := h.ToolInput[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// Env carries everything a hook needs from the outside world.
type Env struct {
	Getenv  Getenv
	DataDir string
	Now     func() time.Time
}

// Policy is injected into the main session at start so Claude knows which
// agent to pick. Tier comes from the agent, so choosing the agent is routing.
const Policy = `cc-router is active. Pick subagents by the kind of work; the router sets each subagent's model by tier.
- Codebase search, reading many files, quick web lookups → cc-router:scout (grunt tier). Ask it a question; act on its digest instead of reading everything yourself.
- Multi-source web research, comparing options → cc-router:researcher (build tier).
- Implementation from a clear spec → cc-router:worker (build tier).
- Code review → cc-router:reviewer (build tier). Draft plans → cc-router:planner (build tier).
- Docs, summaries, mechanical edits → cc-router:scribe (grunt tier).
Don't pass a model to subagents unless you want a cheaper one; more expensive requests are capped to the tier.`

// Run dispatches one hook event. It returns the JSON to print (nil for none).
// Errors are returned for logging only; callers must still exit 0.
func Run(event string, stdin io.Reader, env Env) ([]byte, error) {
	var in HookInput
	if err := json.NewDecoder(stdin).Decode(&in); err != nil {
		return nil, fmt.Errorf("decode %s input: %w", event, err)
	}
	switch event {
	case "session-start":
		return sessionStart(&in)
	case "pre-agent":
		return preAgent(&in, env)
	case "post-agent":
		return nil, postAgent(&in, env)
	}
	return nil, fmt.Errorf("unknown hook %q", event)
}

type hookOutput struct {
	HookSpecificOutput map[string]any `json:"hookSpecificOutput"`
}

func sessionStart(in *HookInput) ([]byte, error) {
	return json.Marshal(hookOutput{map[string]any{
		"hookEventName":     "SessionStart",
		"additionalContext": Policy,
	}})
}

func preAgent(in *HookInput, env Env) ([]byte, error) {
	if in.ToolName != "Agent" && in.ToolName != "Task" {
		return nil, nil
	}
	cfg := Load(env.Getenv, env.DataDir)
	subType := in.toolString("subagent_type")
	requested := in.toolString("model")
	d := Route(cfg, subType, requested, ReadQuota(env.DataDir), env.Now())

	logErr := AppendLog(env.DataDir, "decisions.jsonl", map[string]any{
		"event": "route", "session": in.SessionID, "subagent_type": subType,
		"requested": requested, "routed": d.Routed, "tier": d.Tier.String(),
		"effective": d.Effective.String(), "model": d.Model, "reason": d.Reason,
	})
	if !d.Routed || d.Model == requested {
		return nil, logErr
	}

	updated := make(map[string]json.RawMessage, len(in.ToolInput)+1)
	for k, v := range in.ToolInput {
		updated[k] = v
	}
	model, _ := json.Marshal(d.Model)
	updated["model"] = model

	out, err := json.Marshal(hookOutput{map[string]any{
		"hookEventName":     "PreToolUse",
		"updatedInput":      updated,
		"additionalContext": fmt.Sprintf("cc-router: %s → %s (%s)", subType, d.Model, d.Reason),
	}})
	if err != nil {
		return nil, err
	}
	return out, logErr
}

func postAgent(in *HookInput, env Env) error {
	var resp struct {
		ResolvedModel string `json:"resolvedModel"`
		TotalTokens   int    `json:"totalTokens"`
		TotalMs       int    `json:"totalDurationMs"`
	}
	_ = json.Unmarshal(in.ToolResponse, &resp)
	return AppendLog(env.DataDir, "decisions.jsonl", map[string]any{
		"event": "ran", "session": in.SessionID, "subagent_type": in.toolString("subagent_type"),
		"requested": in.toolString("model"), "resolved_model": resp.ResolvedModel,
		"tokens": resp.TotalTokens, "duration_ms": resp.TotalMs,
	})
}

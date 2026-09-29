package router

// The scout guard, ported from pi's router/dispatch.ts.
//
// It targets unbounded intake in an expensive main session: a search whose
// output size is unknown before it runs, and whose output then stays in the
// Opus context for the rest of the session. Three properties keep it a
// guardrail rather than a cage:
//   - It only fires in the main session, and only when that session is on the
//     plan-tier model (or in plan mode). Cheap sessions are left alone.
//   - A block is never final: repeating the identical call proceeds, and the
//     model is told so.
//   - It gives up: after MaxNudges it stays quiet for the rest of the session.
//
// Long read runs only get a note (additionalContext), never a block.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var searchTools = map[string]bool{"rg": true, "grep": true, "egrep": true, "fgrep": true, "ag": true, "ack": true, "find": true, "fd": true}

// Markers that the caller already bounded the output.
var boundedRe = []*regexp.Regexp{
	regexp.MustCompile(`\|\s*(head|tail|wc)\b`),
	regexp.MustCompile(`(^|\s)-(c|l)\b`),
	regexp.MustCompile(`--count\b`),
	regexp.MustCompile(`--files-with-matches\b`),
	regexp.MustCompile(`--files\b`),
	regexp.MustCompile(`-m\s*\d+`),
	regexp.MustCompile(`--max-count\b`),
}

// Roots where a recursive search is guaranteed to be enormous.
var broadRootRe = []*regexp.Regexp{
	regexp.MustCompile(`(^|\s)~/?(\s|$)`),
	regexp.MustCompile(`(^|\s)\$HOME\b`),
	regexp.MustCompile(`(^|\s)/(\s|$)`),
	regexp.MustCompile(`(^|\s)~/\.claude\b`),
	regexp.MustCompile(`(^|\s)/(Users|home)/[^/\s]+/?(\s|$)`),
	regexp.MustCompile(`node_modules`),
}

var (
	singleQuoted = regexp.MustCompile(`'[^']*'`)
	doubleQuoted = regexp.MustCompile(`"[^"]*"`)
	stageSplit   = regexp.MustCompile(`&&|\|\||;`)
	groupBound   = regexp.MustCompile(`[})]\s*\|\s*(head|tail|wc)\b`)
	extRe        = regexp.MustCompile(`\.\w+$`)
)

// IsUnboundedSweep reports whether a shell command is a search tool with no
// bound that is recursive from the cwd or aimed at a huge tree.
func IsUnboundedSweep(command string) bool { return isUnboundedSweepIn(command, "") }

// isUnboundedSweepIn is IsUnboundedSweep with the session's working directory,
// so bare names of existing files or folders ("grep -r x internal cmd") count
// as targets.
func isUnboundedSweepIn(command, cwd string) bool {
	// A pattern like "rg 'find ~/'" must not be read as a path argument.
	cleaned := doubleQuoted.ReplaceAllString(singleQuoted.ReplaceAllString(command, "''"), `""`)
	if groupBound.MatchString(cleaned) {
		return false // "{ ...; } | head": the whole group's output is bounded
	}
	for _, seg := range stageSplit.Split(cleaned, -1) {
		seg = strings.TrimSpace(seg)
		fields := strings.Fields(seg)
		for len(fields) > 0 && (fields[0] == "{" || fields[0] == "(") {
			fields = fields[1:] // brace or subshell group
		}
		if len(fields) == 0 {
			continue
		}
		first := fields[0]
		if i := strings.LastIndex(first, "/"); i >= 0 {
			first = first[i+1:]
		}
		// Only the first stage of a pipeline counts; `cmd | rg x` is filtering.
		if !searchTools[first] {
			continue
		}
		if anyMatch(boundedRe, seg) {
			continue
		}
		if anyMatch(broadRootRe, seg) || !hasPathArgument(fields[1:], cwd) {
			return true
		}
	}
	return false
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

// hasPathArgument: the first non-flag token is the pattern; any later token
// with a slash or a file extension, or that names an existing file or folder
// in cwd, is a target and narrows the search. "." is the cwd itself.
func hasPathArgument(tokens []string, cwd string) bool {
	seenPattern := false
	for _, t := range tokens {
		if strings.HasPrefix(t, "-") {
			continue
		}
		if !seenPattern {
			seenPattern = true
			continue
		}
		if strings.Contains(t, "/") || extRe.MatchString(t) {
			return true
		}
		if cwd != "" && t != "." && t != ".." && !strings.ContainsAny(t, "*?$`") {
			if _, err := os.Stat(filepath.Join(cwd, t)); err == nil {
				return true
			}
		}
	}
	return false
}

// guardState is per-session and persisted between hook processes.
type guardState struct {
	ReadRun int      `json:"read_run"`
	Nudges  int      `json:"nudges"`
	Allowed []string `json:"allowed"` // signatures already refused once
}

func guardPath(dataDir, session string) string {
	return filepath.Join(dataDir, "guard", safeName(session)+".json")
}

func loadGuard(dataDir, session string) guardState {
	var g guardState
	if raw, err := os.ReadFile(guardPath(dataDir, session)); err == nil {
		_ = json.Unmarshal(raw, &g)
	}
	return g
}

func (g *guardState) allowedHas(sig string) bool {
	for _, s := range g.Allowed {
		if s == sig {
			return true
		}
	}
	return false
}

// guardActive reports whether the main session is expensive enough to guard.
func guardActive(in *HookInput, cfg Config, env Env) bool {
	if !cfg.Guard.Enabled || !in.InMainSession() || env.DataDir == "" || in.SessionID == "" {
		return false
	}
	if in.PermissionMode == "plan" {
		return true
	}
	model := ""
	if s := ReadSession(env.DataDir, in.SessionID); s != nil {
		model = s.ModelID // statusline or PostModelSwitch: current even right after /model
	} else {
		model = modelFromTranscript(in.TranscriptPath) // works without /cc-router:setup
	}
	if model == "" {
		return false // main model unknown: stay out of the way
	}
	planRank := Family(cfg.Models[Plan])
	if planRank < 0 {
		planRank = int(Plan)
	}
	return Family(model) >= planRank
}

// transcriptTail is how much of the transcript end is scanned for the model.
const transcriptTail = 256 << 10

var transcriptModelRe = regexp.MustCompile(`"model":"([^"]+)"`)

// modelFromTranscript returns the model of the latest assistant message in a
// session transcript (JSONL), skipping Claude Code's "<synthetic>" entries.
//
// Measured on 2.1.285: in interactive sessions assistant messages are flushed
// by the second tool call of a session, so only the very first call is missed;
// in -p mode nothing is flushed until the turn ends, so this finds nothing.
// The statusline (after /cc-router:setup) covers both.
// Quotes inside message text are escaped in JSONL, so user content can't match.
func modelFromTranscript(path string) string {
	if path == "" {
		return ""
	}
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > transcriptTail {
		f.Seek(st.Size()-transcriptTail, 0)
	}
	buf := make([]byte, transcriptTail)
	n, _ := io.ReadFull(f, buf)
	matches := transcriptModelRe.FindAllSubmatch(buf[:n], -1)
	for i := len(matches) - 1; i >= 0; i-- {
		if m := string(matches[i][1]); m != "<synthetic>" {
			return m
		}
	}
	return ""
}

const sweepReason = `cc-router: this is an unbounded search. Its output size is unknown before it runs, and whatever comes back stays in this expensive main context for the rest of the session.

Prefer: the Agent tool with subagent_type "cc-router:scout" and the question you are actually asking. A scout reads in its own cheap context and returns a digest.

If you do need the raw output here, bound it (| head -50, -l, --count) or re-issue this exact call and it will run.`

// preTool is the guard's PreToolUse handler for Bash, Grep, Read and edits.
func preTool(in *HookInput, env Env) ([]byte, error) {
	cfg := Load(env.Getenv, env.DataDir)
	if !guardActive(in, cfg, env) {
		return nil, nil
	}
	g := loadGuard(env.DataDir, in.SessionID)
	before := g

	var out []byte
	switch in.ToolName {
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		// Real work started; the reads that led here were the productive kind.
		g.ReadRun = 0
	case "Bash":
		if cmd := in.toolString("command"); isUnboundedSweepIn(cmd, in.Cwd) {
			out = g.deny(cfg, "bash:"+strings.TrimSpace(cmd), sweepReason)
		}
	case "Grep":
		// Grep's default output mode lists files; "content" with no head_limit
		// is the unbounded case.
		if in.toolString("output_mode") == "content" && !in.hasKey("head_limit") {
			sig, _ := json.Marshal(in.ToolInput)
			out = g.deny(cfg, "grep:"+string(sig), sweepReason)
		}
	case "Read":
		// A read with an explicit window is a deliberate, bounded look.
		if in.hasKey("offset") || in.hasKey("limit") {
			break
		}
		g.ReadRun++
		if g.ReadRun > cfg.Guard.ReadRun && g.Nudges < cfg.Guard.MaxNudges {
			g.Nudges++
			n := g.ReadRun
			g.ReadRun = 0
			out, _ = json.Marshal(hookOutput{map[string]any{
				"hookEventName": "PreToolUse",
				"additionalContext": fmt.Sprintf("cc-router: that is %d full-file reads in a row with no edit between them. "+
					"Reading many files to answer one question is scout work, and each file stays in this expensive context. "+
					`Consider the Agent tool with subagent_type "cc-router:scout" and act on its digest.`, n),
			}})
		}
	}

	if g.ReadRun != before.ReadRun || g.Nudges != before.Nudges || len(g.Allowed) != len(before.Allowed) {
		if err := WriteJSON(filepath.Dir(guardPath(env.DataDir, in.SessionID)), filepath.Base(guardPath(env.DataDir, in.SessionID)), g); err != nil {
			return nil, err // can't remember the refusal, so a repeat would be refused forever: fail open
		}
	}
	if out != nil {
		_ = AppendLog(env.DataDir, "decisions.jsonl", map[string]any{
			"event": "guard", "session": in.SessionID, "tool": in.ToolName, "nudges": g.Nudges,
		})
	}
	return out, nil
}

// deny refuses a call once. The identical call afterwards proceeds.
func (g *guardState) deny(cfg Config, sig, reason string) []byte {
	if g.Nudges >= cfg.Guard.MaxNudges || g.allowedHas(sig) {
		return nil
	}
	g.Allowed = append(g.Allowed, sig)
	g.Nudges++
	g.ReadRun = 0
	out, _ := json.Marshal(hookOutput{map[string]any{
		"hookEventName":            "PreToolUse",
		"permissionDecision":       "deny",
		"permissionDecisionReason": reason,
	}})
	return out
}

// resetReadRun is called when work is delegated: a scout launch ends the run.
func resetReadRun(env Env, session string) {
	if env.DataDir == "" || session == "" {
		return
	}
	p := guardPath(env.DataDir, session)
	if _, err := os.Stat(p); err != nil {
		return
	}
	g := loadGuard(env.DataDir, session)
	if g.ReadRun != 0 {
		g.ReadRun = 0
		_ = WriteJSON(filepath.Dir(p), filepath.Base(p), g)
	}
}

// recordModel stores the main model when a hook learns it (SessionStart may
// carry it; PostModelSwitch always does), so the guard works even before the
// statusline has refreshed or when setup was never run.
func recordModel(env Env, session, model string) error {
	if env.DataDir == "" || session == "" || model == "" {
		return nil
	}
	return WriteJSON(filepath.Join(env.DataDir, "sessions"), safeName(session)+".json", SessionState{TS: env.Now().Unix(), ModelID: model})
}

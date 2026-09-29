package router

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// Cases ported from pi router/dispatch.test.ts.
func TestIsUnboundedSweep(t *testing.T) {
	flag := map[string]string{
		"rg across ~/.claude":  `rg -i "kimi" ~/.claude/`,
		"rg no path (rec cwd)": `rg "TODO"`,
		"grep -r home":         `grep -r pattern $HOME`,
		"find home":            `find ~ -name "*.ts"`,
		"rg node_modules":      `rg foo node_modules/`,
		"linux home":           `grep -rn foo /home/alice`,
		"second stage sweep":   `cd src && rg TODO`,
	}
	pass := map[string]string{
		"piped to head":      `rg -i "kimi" ~/.claude/ | head -20`,
		"count only":         `rg --count pattern ~/`,
		"files-with-matches": `rg -l pattern ~/`,
		"targeted file":      `rg "decide" policy.ts`,
		"targeted subdir":    `rg "decide" src/router/`,
		"max-count":          `rg -m 5 pattern ~/`,
		"not a search tool":  `git log --oneline`,
		"filter stage only":  `cat file.txt | grep foo`,
		"quoted path decoy":  `rg 'find ~/ -name' src/app.ts`,
		"wc bound":           `grep -r x ~/ | wc -l`,
		"empty":              ``,
	}
	for name, cmd := range flag {
		if !IsUnboundedSweep(cmd) {
			t.Errorf("%s: %q should be flagged", name, cmd)
		}
	}
	for name, cmd := range pass {
		if IsUnboundedSweep(cmd) {
			t.Errorf("%s: %q should pass", name, cmd)
		}
	}
}

type guardHarness struct {
	t   *testing.T
	env Env
}

func newGuard(t *testing.T, mainModel, extraEnv string) *guardHarness {
	env := testEnv(t)
	if extraEnv != "" {
		k, v, _ := strings.Cut(extraEnv, "=")
		env.Getenv = func(key string) string {
			if key == k {
				return v
			}
			return ""
		}
	}
	if mainModel != "" {
		if err := recordModel(env, "sess-1", mainModel); err != nil {
			t.Fatal(err)
		}
	}
	return &guardHarness{t, env}
}

// call runs the pre-tool hook and returns "deny", "note" or "".
func (h *guardHarness) call(tool string, input map[string]any, opts ...string) string {
	h.t.Helper()
	in := map[string]any{"session_id": "sess-1", "hook_event_name": "PreToolUse", "agent_id": nil,
		"permission_mode": "default", "tool_name": tool, "tool_input": input}
	for _, o := range opts {
		k, v, _ := strings.Cut(o, "=")
		in[k] = v
	}
	raw, _ := json.Marshal(in)
	res, err := Run("pre-tool", strings.NewReader(string(raw)), h.env)
	if err != nil {
		h.t.Fatal(err)
	}
	if len(res.Stdout) == 0 {
		return ""
	}
	var out struct {
		H map[string]any `json:"hookSpecificOutput"`
	}
	json.Unmarshal(res.Stdout, &out)
	if out.H["permissionDecision"] == "deny" {
		return "deny"
	}
	if out.H["additionalContext"] != nil {
		return "note"
	}
	return "?"
}

func bash(cmd string) map[string]any { return map[string]any{"command": cmd} }

func TestGuardSweepDeniedOnceThenRepeatProceeds(t *testing.T) {
	g := newGuard(t, "claude-opus-5-5", "")
	if got := g.call("Bash", bash("rg foo ~/")); got != "deny" {
		t.Fatalf("first sweep: got %q, want deny", got)
	}
	if got := g.call("Bash", bash("rg foo ~/")); got != "" {
		t.Errorf("identical repeat must proceed, got %q", got)
	}
	if got := g.call("Bash", bash("rg foo ~/ | head -50")); got != "" {
		t.Errorf("bounded version must pass, got %q", got)
	}
}

func TestGuardFallsSilentAfterBudget(t *testing.T) {
	g := newGuard(t, "claude-opus-5-5", "")
	for _, c := range []string{"rg a ~/", "rg b ~/"} { // default maxNudges = 2
		if got := g.call("Bash", bash(c)); got != "deny" {
			t.Fatalf("%s: got %q", c, got)
		}
	}
	if got := g.call("Bash", bash("rg c ~/")); got != "" {
		t.Errorf("after budget the guard must be silent, got %q", got)
	}
}

func TestGuardOnlyOnExpensiveMainSession(t *testing.T) {
	if got := newGuard(t, "claude-sonnet-5-5", "").call("Bash", bash("rg foo ~/")); got != "" {
		t.Errorf("sonnet main session: got %q, want silence", got)
	}
	if got := newGuard(t, "", "").call("Bash", bash("rg foo ~/")); got != "" {
		t.Errorf("unknown main model: got %q, want silence", got)
	}
	if got := newGuard(t, "", "").call("Bash", bash("rg foo ~/"), "permission_mode=plan"); got != "deny" {
		t.Errorf("plan mode should guard even with unknown model, got %q", got)
	}
	if got := newGuard(t, "claude-opus-5-5", "").call("Bash", bash("rg foo ~/"), "agent_id=a51e9784732d611c9"); got != "" {
		t.Errorf("inside a subagent: got %q, want silence", got)
	}
	if got := newGuard(t, "claude-opus-5-5", "CLAUDE_PLUGIN_OPTION_GUARD_ENABLED=false").call("Bash", bash("rg foo ~/")); got != "" {
		t.Errorf("guard disabled: got %q", got)
	}
	if got := newGuard(t, "claude-fable-5-1", "CLAUDE_PLUGIN_OPTION_PLAN_MODEL=fable").call("Bash", bash("rg foo ~/")); got != "deny" {
		t.Errorf("custom plan model (fable) should be guarded, got %q", got)
	}
}

func TestGuardGrepTool(t *testing.T) {
	g := newGuard(t, "claude-opus-5-5", "")
	if got := g.call("Grep", map[string]any{"pattern": "x"}); got != "" {
		t.Errorf("default files mode is bounded, got %q", got)
	}
	if got := g.call("Grep", map[string]any{"pattern": "x", "output_mode": "content", "head_limit": 30}); got != "" {
		t.Errorf("head_limit bounds it, got %q", got)
	}
	if got := g.call("Grep", map[string]any{"pattern": "x", "output_mode": "content"}); got != "deny" {
		t.Errorf("content with no head_limit: got %q, want deny", got)
	}
}

func TestGuardReadRunNotesNeverBlocks(t *testing.T) {
	g := newGuard(t, "claude-opus-5-5", "")
	read := func(p string) string { return g.call("Read", map[string]any{"file_path": p}) }
	for i := 1; i <= 6; i++ { // default readRun = 6
		if got := read("/f"); got != "" {
			t.Fatalf("read %d: got %q", i, got)
		}
	}
	if got := read("/f7"); got != "note" {
		t.Fatalf("read 7: got %q, want note (not deny)", got)
	}
	if got := read("/f8"); got != "" {
		t.Errorf("run resets after a note, got %q", got)
	}
}

func TestGuardReadRunResets(t *testing.T) {
	g := newGuard(t, "claude-opus-5-5", "")
	read := func() string { return g.call("Read", map[string]any{"file_path": "/f"}) }
	for i := 0; i < 5; i++ {
		read()
	}
	g.call("Edit", map[string]any{"file_path": "/f"})
	for i := 0; i < 6; i++ {
		if got := read(); got != "" {
			t.Fatalf("edit should reset the run; read %d got %q", i+1, got)
		}
	}
	// Launching an agent also resets the run.
	Run("pre-agent", strings.NewReader(`{"session_id":"sess-1","tool_name":"Agent","agent_id":null,"tool_input":{"subagent_type":"cc-router:scout","prompt":"q"}}`), g.env)
	if got := read(); got != "" {
		t.Errorf("agent launch should reset the run, got %q", got)
	}
	for i := 0; i < 20; i++ {
		if got := g.call("Read", map[string]any{"file_path": "/f", "limit": 50}); got != "" {
			t.Fatalf("bounded reads never count, got %q", got)
		}
	}
}

func TestPostModelSwitchRecordsModel(t *testing.T) {
	env := testEnv(t)
	Run("post-model-switch", strings.NewReader(`{"session_id":"sess-1","from_model":"claude-sonnet-5-5","to_model":"claude-opus-5-5"}`), env)
	if s := ReadSession(env.DataDir, "sess-1"); s == nil || s.ModelID != "claude-opus-5-5" {
		t.Errorf("session model = %+v", s)
	}
	Run("session-start", strings.NewReader(`{"session_id":"sess-2","source":"startup","model":"claude-haiku-4-5"}`), env)
	if s := ReadSession(env.DataDir, "sess-2"); s == nil || s.ModelID != "claude-haiku-4-5" {
		t.Errorf("SessionStart model not recorded: %+v", s)
	}
}

func TestModelFromTranscript(t *testing.T) {
	dir := t.TempDir()
	p := dir + "/t.jsonl"
	lines := []string{
		`{"type":"assistant","message":{"model":"claude-sonnet-5-5","content":[]}}`,
		`{"type":"user","message":{"content":"say \"model\":\"claude-haiku-4-5\" please"}}`,
		`{"type":"assistant","message":{"model":"claude-opus-5-5","content":[]}}`,
		`{"type":"assistant","message":{"model":"<synthetic>","content":[]}}`,
	}
	os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o644)
	if got := modelFromTranscript(p); got != "claude-opus-5-5" {
		t.Errorf("got %q, want claude-opus-5-5 (skip synthetic, ignore escaped text)", got)
	}
	if got := modelFromTranscript(dir + "/missing.jsonl"); got != "" {
		t.Errorf("missing file: got %q", got)
	}

	// The guard uses it when there is no session state.
	g := newGuard(t, "", "")
	if got := g.call("Bash", bash("rg foo ~/"), "transcript_path="+p); got != "deny" {
		t.Errorf("transcript says opus: got %q, want deny", got)
	}
}

func TestSweepNamedFoldersAndGroups(t *testing.T) {
	cwd := t.TempDir()
	os.Mkdir(cwd+"/internal", 0o755)
	os.Mkdir(cwd+"/cmd", 0o755)
	for cmd, want := range map[string]bool{
		`grep -rnE 'WriteJSON\(' internal cmd | grep -v _test`: false, // seen live as a false positive
		`grep -rn x nosuchdir`:                                 true,  // not an existing path: still a guess
		`grep -rnE 'x' --include='*' . --exclude-dir=.git`:     true,  // "." is the whole cwd
		`{ grep -rn x .; sed -n 1,5p f.go; }`:                  true,  // brace group
		`{ grep -rn x .; } | head -50`:                         false, // bounded group
		`( rg TODO )`:                                          true,  // subshell
	} {
		if got := isUnboundedSweepIn(cmd, cwd); got != want {
			t.Errorf("%q: got %v, want %v", cmd, got, want)
		}
	}
}

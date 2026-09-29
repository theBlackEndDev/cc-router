package router

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testEnv(t *testing.T) Env {
	return Env{Getenv: func(string) string { return "" }, DataDir: t.TempDir(), Now: func() time.Time { return now }}
}

// Shape captured from Claude Code 2.1.284 (spike S2).
const agentInput = `{"session_id":"s1","hook_event_name":"PreToolUse","agent_id":null,"permission_mode":"default",
 "tool_name":"Agent","tool_input":{"description":"Find X","prompt":"Where is X defined?","subagent_type":%q,"run_in_background":false%s}}`

func runPre(t *testing.T, env Env, subType, modelField string) map[string]any {
	t.Helper()
	in := strings.NewReader(fmt.Sprintf(agentInput, subType, modelField))
	res, err := Run("pre-agent", in, env)
	if err != nil {
		t.Fatal(err)
	}
	out := res.Stdout
	if out == nil {
		return nil
	}
	var v map[string]any
	if err := json.Unmarshal(out, &v); err != nil {
		t.Fatalf("bad output %s: %v", out, err)
	}
	return v
}

func TestPreAgentRewritesModelAndKeepsFields(t *testing.T) {
	env := testEnv(t)
	v := runPre(t, env, "Explore", "")
	hso := v["hookSpecificOutput"].(map[string]any)
	if hso["hookEventName"] != "PreToolUse" {
		t.Fatalf("hookEventName = %v", hso["hookEventName"])
	}
	if _, ok := hso["permissionDecision"]; ok {
		t.Error("must not grant permission; only rewrite input")
	}
	ui := hso["updatedInput"].(map[string]any)
	if ui["model"] != "haiku" {
		t.Errorf("model = %v, want haiku", ui["model"])
	}
	for _, k := range []string{"description", "prompt", "subagent_type", "run_in_background"} {
		if _, ok := ui[k]; !ok {
			t.Errorf("updatedInput dropped %q", k)
		}
	}

	recs := readLog(t, env.DataDir)
	if len(recs) != 1 || recs[0]["model"] != "haiku" || recs[0]["event"] != "route" {
		t.Errorf("decision log = %v", recs)
	}
}

func TestPreAgentNoOutputWhenUnroutedOrUnchanged(t *testing.T) {
	env := testEnv(t)
	if v := runPre(t, env, "my-own-agent", `,"model":"opus"`); v != nil {
		t.Errorf("unrouted agent got output %v", v)
	}
	if v := runPre(t, env, "cc-router:scout", `,"model":"haiku"`); v != nil {
		t.Errorf("already-correct model got output %v", v)
	}
	if n := len(readLog(t, env.DataDir)); n != 2 {
		t.Errorf("want 2 log lines, got %d", n)
	}
}

func TestPreAgentUsesQuotaSnapshot(t *testing.T) {
	env := testEnv(t)
	if err := WriteJSON(env.DataDir, "quota.json", quota(92, 40)); err != nil {
		t.Fatal(err)
	}
	v := runPre(t, env, "cc-router:worker", "")
	ui := v["hookSpecificOutput"].(map[string]any)["updatedInput"].(map[string]any)
	if ui["model"] != "haiku" {
		t.Errorf("under quota pressure worker should step down to haiku, got %v", ui["model"])
	}
}

func TestSessionStartInjectsPolicy(t *testing.T) {
	env := testEnv(t)
	env.Getenv = func(k string) string {
		if k == "CLAUDE_PLUGIN_ROOT" {
			return "/opt/plugins/cc-router/0.1.0"
		}
		return ""
	}
	res, err := Run("session-start", strings.NewReader(`{"session_id":"s1","hook_event_name":"SessionStart","source":"startup"}`), env)
	if err != nil {
		t.Fatal(err)
	}
	out := res.Stdout
	if root, _ := os.ReadFile(filepath.Join(env.DataDir, "root")); string(root) != "/opt/plugins/cc-router/0.1.0" {
		t.Errorf("root pointer = %q", root)
	}
	if !strings.Contains(string(out), "cc-router:scout") || !strings.Contains(string(out), `"SessionStart"`) {
		t.Errorf("policy missing: %s", out)
	}
}

func TestPostAgentLogsResolvedModel(t *testing.T) {
	env := testEnv(t)
	in := `{"session_id":"s1","tool_name":"Agent","tool_input":{"subagent_type":"Explore","model":"haiku"},
	 "tool_response":{"resolvedModel":"claude-haiku-4-5-20251001","totalTokens":1234,"totalDurationMs":950}}`
	if _, err := Run("post-agent", strings.NewReader(in), env); err != nil {
		t.Fatal(err)
	}
	recs := readLog(t, env.DataDir)
	if len(recs) != 1 || recs[0]["resolved_model"] != "claude-haiku-4-5-20251001" {
		t.Errorf("log = %v", recs)
	}
}

func TestBadInputIsAnErrorNotAPanic(t *testing.T) {
	if _, err := Run("pre-agent", strings.NewReader("not json"), testEnv(t)); err == nil {
		t.Error("want error for bad JSON")
	}
}

func TestMainSessionDetection(t *testing.T) {
	var h HookInput
	json.Unmarshal([]byte(`{"agent_id":null}`), &h)
	if !h.InMainSession() {
		t.Error("agent_id null should be main session")
	}
	json.Unmarshal([]byte(`{"agent_id":"a51e9784732d611c9"}`), &h)
	if h.InMainSession() {
		t.Error("agent_id set should be a subagent")
	}
}

func readLog(t *testing.T, dir string) []map[string]any {
	t.Helper()
	f, err := os.Open(filepath.Join(dir, "decisions.jsonl"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var recs []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		json.Unmarshal(sc.Bytes(), &m)
		recs = append(recs, m)
	}
	return recs
}

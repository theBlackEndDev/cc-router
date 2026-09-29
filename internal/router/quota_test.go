package router

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Statusline input shape captured from Claude Code 2.1.284 on a Pro account (spike S3).
const slInput = `{"session_id":"sess-1","model":{"id":"claude-opus-5-5","display_name":"Opus 5.5"},
 "rate_limits":{"five_hour":{"used_percentage":%s,"resets_at":1800003600},"seven_day":{"used_percentage":31,"resets_at":1800086400}},
 "cwd":"/tmp","version":"2.1.284"}`

func sl(t *testing.T, env Env, pct string) string {
	t.Helper()
	var out bytes.Buffer
	if err := Statusline(strings.NewReader(strings.Replace(slInput, "%s", pct, 1)), &out, env); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestStatuslineRecordsQuotaAndSession(t *testing.T) {
	env := testEnv(t)
	out := sl(t, env, "26")
	if out != "Opus 5.5 · 5h 26% · 7d 31%\n" {
		t.Errorf("default line = %q", out)
	}
	q := ReadQuota(env.DataDir)
	if q == nil || q.FiveHour.UsedPercentage != 26 || q.TS != now.Unix() {
		t.Fatalf("quota.json = %+v", q)
	}
	if s := ReadSession(env.DataDir, "sess-1"); s == nil || s.ModelID != "claude-opus-5-5" {
		t.Errorf("session state = %+v", s)
	}
	if out := sl(t, env, "90"); !strings.Contains(out, "↓ step-down") {
		t.Errorf("step-down not shown: %q", out)
	}
}

func TestStatuslineWithoutRateLimitsKeepsOldQuota(t *testing.T) {
	env := testEnv(t)
	sl(t, env, "40")
	var out bytes.Buffer
	Statusline(strings.NewReader(`{"session_id":"s","model":{"id":"x","display_name":"X"}}`), &out, env)
	if q := ReadQuota(env.DataDir); q == nil || q.FiveHour.UsedPercentage != 40 {
		t.Errorf("API-key session must not erase subscriber quota: %+v", q)
	}
}

func TestStatuslineWrapsUserCommand(t *testing.T) {
	env := testEnv(t)
	os.WriteFile(filepath.Join(env.DataDir, origStatusLineFile), []byte(`{"type":"command","command":"printf 'mine:'; cat | head -c 13"}`), 0o644)
	out := sl(t, env, "26")
	if !strings.HasPrefix(out, `mine:{"session_id"`) {
		t.Errorf("wrapped command did not get stdin or output: %q", out)
	}
	if out := sl(t, env, "95"); !strings.Contains(out, "\ncc-router ↓ subagents stepped down (5h 95%)") {
		t.Errorf("step-down note missing under wrapped output: %q", out)
	}
}

func settingsEnv(t *testing.T, settings string) (Env, string) {
	env := testEnv(t)
	cfgDir := t.TempDir()
	if settings != "" {
		os.WriteFile(filepath.Join(cfgDir, "settings.json"), []byte(settings), 0o644)
	}
	env.Getenv = func(k string) string {
		if k == "CLAUDE_CONFIG_DIR" {
			return cfgDir
		}
		return ""
	}
	return env, cfgDir
}

func TestSetupWrapsExistingStatusline(t *testing.T) {
	env, _ := settingsEnv(t, `{"model":"opus","statusLine":{"type":"command","command":"~/.claude/sl.sh","padding":1}}`)
	var out bytes.Buffer
	if err := Setup(&out, env, "/plugins/cc-router/0.1.0"); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	shim := filepath.Join(env.DataDir, shimName)
	for _, want := range []string{`"command": "'` + shim + `' || ~/.claude/sl.sh"`, `"padding": 1`, `"refreshInterval": 5`, "CURRENT statusLine"} {
		if !strings.Contains(s, want) {
			t.Errorf("setup output missing %s:\n%s", want, s)
		}
	}
	if st, err := os.Stat(shim); err != nil || st.Mode()&0o111 == 0 {
		t.Errorf("shim not executable: %v", err)
	}
	if got := savedStatusCommand(env.DataDir); got != "~/.claude/sl.sh" {
		t.Errorf("original not saved: %q", got)
	}
}

func TestSetupThenRemoveRoundTrip(t *testing.T) {
	env, cfgDir := settingsEnv(t, `{"model":"opus"}`)
	var out bytes.Buffer
	if err := Setup(&out, env, "/r"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "||") {
		t.Errorf("no original, so no fallback expected:\n%s", out.String())
	}
	// Simulate Claude applying the edit.
	shim := filepath.Join(env.DataDir, shimName)
	applied, _ := json.Marshal(map[string]any{"model": "opus", "statusLine": map[string]any{"type": "command", "command": shellQuote(shim)}})
	os.WriteFile(filepath.Join(cfgDir, "settings.json"), applied, 0o644)

	out.Reset()
	if err := Setup(&out, env, "/r"); err != nil || !strings.Contains(out.String(), "ALREADY INSTALLED") {
		t.Errorf("second setup should be a no-op: %v %s", err, out.String())
	}
	out.Reset()
	if err := Remove(&out, env); err != nil || !strings.Contains(out.String(), `remove the "statusLine" key`) {
		t.Errorf("remove: %v %s", err, out.String())
	}
}

func TestShimFallsBackWhenPluginMissing(t *testing.T) {
	env, _ := settingsEnv(t, "")
	var out bytes.Buffer
	if err := Setup(&out, env, "/does/not/exist"); err != nil {
		t.Fatal(err)
	}
	// settings command would be: 'shim' || original. The shim must exit non-zero
	// without output when the plugin root is gone.
	cmd := shellQuote(filepath.Join(env.DataDir, shimName)) + " || echo fallback-ran"
	got := runWrapped(cmd, []byte(`{}`))
	if string(got) != "fallback-ran\n" {
		t.Errorf("fallback output = %q", got)
	}
}

func TestUserPromptSubmitNoticesOncePerLevel(t *testing.T) {
	env := testEnv(t)
	prompt := func() string {
		res, err := Run("user-prompt-submit", strings.NewReader(`{"session_id":"sess-1","hook_event_name":"UserPromptSubmit","prompt":"hi"}`), env)
		if err != nil {
			t.Fatal(err)
		}
		return string(res.Stdout)
	}
	if out := prompt(); out != "" {
		t.Errorf("no quota data: want silence, got %s", out)
	}
	sl(t, env, "50")
	if out := prompt(); out != "" {
		t.Errorf("below warn_at: want silence, got %s", out)
	}
	sl(t, env, "72")
	if out := prompt(); !strings.Contains(out, "5h quota window at 72%") || !strings.Contains(out, "systemMessage") {
		t.Errorf("warn notice missing: %s", out)
	}
	sl(t, env, "75")
	if out := prompt(); out != "" {
		t.Errorf("same level again: want silence, got %s", out)
	}
	sl(t, env, "88")
	out := prompt()
	if !strings.Contains(out, "stepped down one tier") || !strings.Contains(out, "/model sonnet") {
		t.Errorf("degrade notice (with main-on-opus hint) missing: %s", out)
	}
	if out := prompt(); out != "" {
		t.Errorf("degrade repeated: %s", out)
	}
}

func TestPreModelSwitchBlockOnceThenAllow(t *testing.T) {
	env := testEnv(t)
	clock := now
	env.Now = func() time.Time { return clock }
	sw := func(to string) Result {
		res, err := Run("pre-model-switch", strings.NewReader(`{"session_id":"sess-1","hook_event_name":"PreModelSwitch","from_model":"claude-sonnet-5-5","to_model":"`+to+`","requested_model":"opus"}`), env)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	if r := sw("claude-opus-5-5"); r.Block != "" {
		t.Errorf("no quota data: should allow, got %q", r.Block)
	}
	WriteJSON(env.DataDir, "quota.json", quota(90, 10))
	if r := sw("claude-sonnet-5-5"); r.Block != "" {
		t.Errorf("switch to sonnet should never block: %q", r.Block)
	}
	r := sw("claude-opus-5-5")
	if !strings.Contains(r.Block, "90%") || !strings.Contains(r.Block, "again within 60 seconds") {
		t.Fatalf("first opus switch should block with reason, got %q", r.Block)
	}
	clock = clock.Add(30 * time.Second)
	if r := sw("claude-opus-5-5"); r.Block != "" {
		t.Errorf("repeat within window should pass, got %q", r.Block)
	}
	// Claude Code calls PreModelSwitch more than once per /model (seen live on
	// 2.1.284), so the confirmation must hold for the whole window.
	if r := sw("claude-opus-5-5"); r.Block != "" {
		t.Errorf("second call inside the window should still pass, got %q", r.Block)
	}
	if r := sw("opus"); r.Block == "" {
		t.Error("confirmation is per target model; a different target string should block")
	}
	clock = clock.Add(2 * time.Minute)
	if r := sw("claude-opus-5-5"); r.Block == "" {
		t.Error("repeat after the window expired should block again")
	}
}

func TestPruneRemovesOldPerSessionFiles(t *testing.T) {
	env := testEnv(t)
	old := now.Add(-8 * 24 * time.Hour)
	for _, p := range []string{"sessions/a.json", "notices/a.json", "guard/a.json", "switch-a.json"} {
		full := filepath.Join(env.DataDir, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		os.WriteFile(full, []byte("{}"), 0o644)
		os.Chtimes(full, old, old)
	}
	os.WriteFile(filepath.Join(env.DataDir, "switch-fresh.json"), []byte("{}"), 0o644)
	fresh := now.Add(-time.Hour)
	os.Chtimes(filepath.Join(env.DataDir, "switch-fresh.json"), fresh, fresh)

	pruneSessions(env.DataDir, now, 7*24*time.Hour)
	for _, p := range []string{"sessions/a.json", "notices/a.json", "guard/a.json", "switch-a.json"} {
		if _, err := os.Stat(filepath.Join(env.DataDir, p)); err == nil {
			t.Errorf("%s older than 7 days was not pruned", p)
		}
	}
	if _, err := os.Stat(filepath.Join(env.DataDir, "switch-fresh.json")); err != nil {
		t.Error("fresh switch file was pruned")
	}
}

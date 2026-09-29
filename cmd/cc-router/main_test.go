package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func call(t *testing.T, stdin string, dataDir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var o, e bytes.Buffer
	getenv := func(k string) string {
		if k == "CLAUDE_PLUGIN_DATA" {
			return dataDir
		}
		return ""
	}
	code = run(args, strings.NewReader(stdin), &o, &e, getenv, func() time.Time { return time.Unix(1_800_000_000, 0) })
	return code, o.String(), e.String()
}

func TestHookFailsOpenAndLogs(t *testing.T) {
	dir := t.TempDir()
	code, out, _ := call(t, "not json", dir, "hook", "pre-agent")
	if code != 0 || out != "" {
		t.Errorf("bad input: code=%d out=%q, want 0 and silence", code, out)
	}
	if log, _ := os.ReadFile(filepath.Join(dir, "errors.log")); !strings.Contains(string(log), "decode pre-agent input") {
		t.Errorf("error not logged: %q", log)
	}
	if code, _, _ := call(t, "{}", dir, "hook", "no-such-event"); code != 0 {
		t.Errorf("unknown event must exit 0, got %d", code)
	}
	if code, _, _ := call(t, "", dir, "hook"); code != 0 {
		t.Errorf("missing event must exit 0, got %d", code)
	}
}

func TestHookRoutesViaDataFlag(t *testing.T) {
	dir := t.TempDir()
	in := `{"tool_name":"Agent","agent_id":null,"tool_input":{"subagent_type":"Explore","prompt":"x"}}`
	code, out, _ := call(t, in, "", "hook", "pre-agent", "--data", dir)
	if code != 0 || !strings.Contains(out, `"model":"haiku"`) {
		t.Errorf("code=%d out=%s", code, out)
	}
	if _, err := os.Stat(filepath.Join(dir, "decisions.jsonl")); err != nil {
		t.Error("--data not honoured")
	}
}

func TestHookBlockExitsTwo(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "quota.json"), []byte(`{"ts":1800000000,"five_hour":{"used_percentage":95,"resets_at":1800003600}}`), 0o644)
	code, out, errOut := call(t, `{"session_id":"s","to_model":"claude-opus-5-5"}`, dir, "hook", "pre-model-switch")
	if code != 2 || out != "" || !strings.Contains(errOut, "95%") {
		t.Errorf("code=%d out=%q stderr=%q", code, out, errOut)
	}
}

func TestStatuslineFailsOpen(t *testing.T) {
	code, out, _ := call(t, "garbage", t.TempDir(), "statusline")
	if code != 0 {
		t.Errorf("statusline must exit 0, got %d (out %q)", code, out)
	}
}

func TestUsageAndUnknown(t *testing.T) {
	if code, _, _ := call(t, "", ""); code != 2 {
		t.Errorf("no args: want 2, got %d", code)
	}
	if code, _, _ := call(t, "", "", "bogus"); code != 2 {
		t.Errorf("unknown command: want 2, got %d", code)
	}
	if code, out, _ := call(t, "", "", "version"); code != 0 || out != "dev\n" {
		t.Errorf("version: %d %q", code, out)
	}
}

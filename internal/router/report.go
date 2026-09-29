package router

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MinClaudeVersion is the oldest Claude Code release cc-router was verified on.
const MinClaudeVersion = "2.1.281"

// Status prints the effective tiers, route table, quota and recent decisions.
func Status(w io.Writer, env Env) {
	cfg := Load(env.Getenv, env.DataDir)
	fmt.Fprintf(w, "Tiers:  plan=%s  build=%s  grunt=%s\n", cfg.Models[Plan], cfg.Models[Build], cfg.Models[Grunt])
	fmt.Fprintf(w, "Quota:  warn at %.0f%%, step down at %.0f%%\n", cfg.WarnAt, cfg.DegradeAt)
	fmt.Fprintf(w, "Guard:  enabled=%v readRun=%d maxNudges=%d\n", cfg.Guard.Enabled, cfg.Guard.ReadRun, cfg.Guard.MaxNudges)

	names := make([]string, 0, len(cfg.Routes))
	for n := range cfg.Routes {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Fprintln(w, "\nRoutes:")
	for _, n := range names {
		t := cfg.Routes[n]
		fmt.Fprintf(w, "  %-22s %-6s → %s\n", n, t, cfg.Models[t])
	}

	fmt.Fprintln(w, "\nQuota snapshot:")
	fmt.Fprintln(w, "  "+describeQuota(ReadQuota(env.DataDir), env.Now()))

	fmt.Fprintln(w, "\nRecent decisions:")
	lines := tail(filepath.Join(env.DataDir, "decisions.jsonl"), 10)
	if len(lines) == 0 {
		fmt.Fprintln(w, "  none yet")
	}
	for _, l := range lines {
		fmt.Fprintln(w, "  "+l)
	}
	for _, warn := range cfg.Warnings {
		fmt.Fprintln(w, "WARNING: "+warn)
	}
}

func describeQuota(q *Quota, now time.Time) string {
	if q == nil {
		return "none (statusline not wired; run /cc-router:setup for quota awareness)"
	}
	age := now.Sub(time.Unix(q.TS, 0)).Round(time.Second)
	var parts []string
	for _, c := range []struct {
		n string
		w *Window
	}{{"5h", q.FiveHour}, {"7d", q.SevenDay}} {
		if c.w != nil {
			parts = append(parts, fmt.Sprintf("%s %.0f%% (resets %s)", c.n, c.w.UsedPercentage, time.Unix(c.w.ResetsAt, 0).Format("Jan 2 15:04")))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "no rate-limit windows reported (API key or non-subscriber)")
	}
	stale := ""
	if age > QuotaMaxAge {
		stale = " — STALE, ignored"
	}
	return fmt.Sprintf("%s; captured %s ago%s", strings.Join(parts, ", "), age, stale)
}

func tail(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}

// Doctor checks the install and prints one line per check. Returns false if
// any check failed.
func Doctor(w io.Writer, env Env, version string) bool {
	ok := true
	check := func(pass bool, label, detail string) {
		mark := "ok  "
		if !pass {
			mark, ok = "FAIL", false
		}
		fmt.Fprintf(w, "[%s] %-18s %s\n", mark, label, detail)
	}
	info := func(label, detail string) { fmt.Fprintf(w, "[info] %-18s %s\n", label, detail) }

	check(true, "binary", fmt.Sprintf("cc-router %s %s/%s", version, runtime.GOOS, runtime.GOARCH))

	if env.DataDir == "" {
		check(false, "data dir", "unknown: pass --data \"${CLAUDE_PLUGIN_DATA}\"")
	} else {
		err := os.MkdirAll(env.DataDir, 0o755)
		if err == nil {
			err = WriteJSON(env.DataDir, ".doctor", map[string]any{"ts": env.Now().Unix()})
			os.Remove(filepath.Join(env.DataDir, ".doctor"))
		}
		check(err == nil, "data dir", fmt.Sprintf("%s %s", env.DataDir, errText(err)))
	}

	if out, err := exec.Command("claude", "--version").Output(); err != nil {
		info("claude", "`claude --version` failed; can't check version")
	} else {
		v := strings.Fields(string(out))
		have := ""
		if len(v) > 0 {
			have = v[0]
		}
		check(versionAtLeast(have, MinClaudeVersion), "claude version", fmt.Sprintf("%s (need >= %s)", have, MinClaudeVersion))
	}

	cfg := Load(env.Getenv, env.DataDir)
	check(len(cfg.Warnings) == 0, "config", strings.Join(append([]string{fmt.Sprintf("plan=%s build=%s grunt=%s", cfg.Models[Plan], cfg.Models[Build], cfg.Models[Grunt])}, cfg.Warnings...), "; "))

	if env.Getenv("CLAUDE_CODE_SUBAGENT_MODEL_FORCE") == "1" {
		check(false, "subagent force", "CLAUDE_CODE_SUBAGENT_MODEL_FORCE=1 overrides every routing decision; unset it")
	}
	if m := env.Getenv("CLAUDE_CODE_SUBAGENT_MODEL"); m != "" {
		info("subagent default", "CLAUDE_CODE_SUBAGENT_MODEL="+m+" (applies to agents without a model: and not in the route table)")
	}

	info("quota", describeQuota(ReadQuota(env.DataDir), env.Now()))

	if lines := tail(filepath.Join(env.DataDir, "decisions.jsonl"), 1); len(lines) == 1 {
		var rec struct {
			TS string `json:"ts"`
		}
		_ = json.Unmarshal([]byte(lines[0]), &rec)
		info("hooks", "last routing decision at "+rec.TS)
	} else {
		info("hooks", "no routing decisions yet (normal until a subagent is launched)")
	}
	return ok
}

func errText(err error) string {
	if err != nil {
		return "(" + err.Error() + ")"
	}
	return "(writable)"
}

// versionAtLeast compares dotted numeric versions ("2.1.284" >= "2.1.281").
func versionAtLeast(have, want string) bool {
	h, w := strings.Split(have, "."), strings.Split(want, ".")
	for i := 0; i < len(w); i++ {
		var hv int
		if i < len(h) {
			hv, _ = strconv.Atoi(h[i])
		}
		wv, _ := strconv.Atoi(w[i])
		if hv != wv {
			return hv > wv
		}
	}
	return true
}

package router

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Unix(1_800_000_000, 0)

func quota(pct5h, pct7d float64) *Quota {
	return &Quota{
		TS:       now.Unix() - 30,
		FiveHour: &Window{UsedPercentage: pct5h, ResetsAt: now.Unix() + 3600},
		SevenDay: &Window{UsedPercentage: pct7d, ResetsAt: now.Unix() + 86400},
	}
}

func TestRoute(t *testing.T) {
	cfg := Default()
	cases := []struct {
		name      string
		subType   string
		requested string
		q         *Quota
		routed    bool
		model     string
		effective Tier
	}{
		{"scout gets grunt", "cc-router:scout", "", nil, true, "haiku", Grunt},
		{"worker gets build", "cc-router:worker", "", nil, true, "sonnet", Build},
		{"researcher gets build", "cc-router:researcher", "", nil, true, "sonnet", Build},
		{"built-in Explore to grunt", "Explore", "", nil, true, "haiku", Grunt},
		{"built-in general-purpose to build", "general-purpose", "", nil, true, "sonnet", Build},
		{"built-in Plan to plan", "Plan", "", nil, true, "opus", Plan},
		{"unknown agent untouched", "my-agent", "opus", nil, false, "", Grunt},

		{"cap: higher request replaced", "cc-router:scout", "opus", nil, true, "haiku", Grunt},
		{"cap: full ID higher replaced", "cc-router:worker", "claude-opus-5-5", nil, true, "sonnet", Build},
		{"cap: lower request kept", "cc-router:worker", "haiku", nil, true, "haiku", Build},
		{"cap: same family full ID kept", "cc-router:worker", "claude-sonnet-5-5", nil, true, "claude-sonnet-5-5", Build},
		{"cap: inherit ranks as opus", "cc-router:worker", "inherit", nil, true, "sonnet", Build},
		{"cap: unknown ID replaced", "cc-router:worker", "mystery-model", nil, true, "sonnet", Build},

		{"quota below threshold", "cc-router:worker", "", quota(84, 10), true, "sonnet", Build},
		{"quota 5h at threshold steps down", "cc-router:worker", "", quota(85, 10), true, "haiku", Grunt},
		{"quota 7d steps down", "Plan", "", quota(10, 90), true, "sonnet", Build},
		{"grunt never below grunt", "cc-router:scout", "", quota(99, 99), true, "haiku", Grunt},
		{"quota + cap: request above stepped tier", "Plan", "opus", quota(90, 0), true, "sonnet", Build},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Route(cfg, c.subType, c.requested, c.q, now)
			if d.Routed != c.routed {
				t.Fatalf("routed = %v, want %v (%s)", d.Routed, c.routed, d.Reason)
			}
			if !c.routed {
				return
			}
			if d.Model != c.model || d.Effective != c.effective {
				t.Errorf("got model=%s effective=%s, want model=%s effective=%s (%s)", d.Model, d.Effective, c.model, c.effective, d.Reason)
			}
		})
	}
}

func TestQuotaIgnoredWhenStaleOrReset(t *testing.T) {
	cfg := Default()
	stale := quota(95, 95)
	stale.TS = now.Add(-QuotaMaxAge - time.Second).Unix()
	if d := Route(cfg, "cc-router:worker", "", stale, now); d.Model != "sonnet" {
		t.Errorf("stale snapshot stepped down: %s", d.Reason)
	}
	reset := quota(95, 10)
	reset.FiveHour.ResetsAt = now.Unix() - 1
	if d := Route(cfg, "cc-router:worker", "", reset, now); d.Model != "sonnet" {
		t.Errorf("reset window stepped down: %s", d.Reason)
	}
}

func TestFamily(t *testing.T) {
	for in, want := range map[string]int{
		"haiku": 0, "claude-haiku-4-5-20251001": 0, "sonnet": 1, "sonnet[1m]": 1,
		"claude-sonnet-5-5": 1, "opus": 2, "opusplan": 2, "fable": 2, "best": 2, "inherit": 2,
		"us.anthropic.claude-opus-5-5-v1:0": 2, "gpt-9": -1, "": -1,
	} {
		if got := Family(in); got != want {
			t.Errorf("Family(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestLoad(t *testing.T) {
	env := func(m map[string]string) Getenv { return func(k string) string { return m[k] } }

	c := Load(env(nil), "")
	if c.Models != [3]string{"haiku", "sonnet", "opus"} || len(c.Warnings) != 0 {
		t.Fatalf("defaults wrong: %+v", c)
	}

	c = Load(env(map[string]string{
		"CLAUDE_PLUGIN_OPTION_PLAN_MODEL":    "fable",
		"CLAUDE_PLUGIN_OPTION_grunt_model":   "claude-haiku-4-5",
		"CLAUDE_PLUGIN_OPTION_DEGRADE_AT":    "90",
		"CLAUDE_PLUGIN_OPTION_GUARD_ENABLED": "false",
	}), "")
	if c.Models[Plan] != "fable" || c.Models[Grunt] != "claude-haiku-4-5" || c.DegradeAt != 90 || c.Guard.Enabled {
		t.Errorf("env options not applied: %+v", c)
	}

	c = Load(env(map[string]string{"CLAUDE_PLUGIN_OPTION_WARN_AT": "150"}), "")
	if c.WarnAt != 70 || len(c.Warnings) != 1 {
		t.Errorf("bad warn_at should keep default with warning: %+v", c)
	}
	c = Load(env(map[string]string{"CLAUDE_PLUGIN_OPTION_WARN_AT": "95"}), "")
	if c.WarnAt != 70 || c.DegradeAt != 85 || len(c.Warnings) != 1 {
		t.Errorf("warn_at > degrade_at should reset both: %+v", c)
	}

	dir := t.TempDir()
	write := func(s string) { os.WriteFile(filepath.Join(dir, "config.json"), []byte(s), 0o644) }
	write(`{"routes":{"my-agent":"build","Explore":"none","bad":"huge"},"guard":{"readRun":3}}`)
	c = Load(env(nil), dir)
	if c.Routes["my-agent"] != Build || c.Guard.ReadRun != 3 {
		t.Errorf("config.json not merged: %+v", c)
	}
	if _, ok := c.Routes["Explore"]; ok {
		t.Error(`route "none" should remove the entry`)
	}
	if len(c.Warnings) != 1 {
		t.Errorf("want 1 warning for unknown tier, got %v", c.Warnings)
	}

	write(`{not json`)
	c = Load(env(nil), dir)
	if len(c.Warnings) != 1 || c.Routes["Explore"] != Grunt {
		t.Errorf("broken config.json should warn and keep defaults: %+v", c)
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, c := range []struct {
		have, want string
		ok         bool
	}{{"2.1.284", "2.1.281", true}, {"2.1.281", "2.1.281", true}, {"2.1.99", "2.1.281", false}, {"3.0.0", "2.1.281", true}, {"", "2.1.281", false}} {
		if got := versionAtLeast(c.have, c.want); got != c.ok {
			t.Errorf("versionAtLeast(%q, %q) = %v", c.have, c.want, got)
		}
	}
}

func TestFormatDecision(t *testing.T) {
	for in, want := range map[string]string{
		`{"event":"route","routed":true,"subagent_type":"cc-router:scout","model":"haiku","reason":"tier grunt","ts":"x"}`: "routed      cc-router:scout → haiku (tier grunt)",
		`{"event":"route","routed":false,"subagent_type":"my-agent","ts":"x"}`:                                             "left alone  my-agent (not in the route table)",
		`{"event":"ran","subagent_type":"Explore","resolved_model":"claude-haiku-4-5","tokens":10340,"duration_ms":4277}`:  "finished    Explore on claude-haiku-4-5, 10340 tokens, 4.3s",
		`{"event":"guard","tool":"Bash"}`: "guard       stepped in on a Bash call",
		`{"event":"model-switch","to":"claude-opus-5-5","allowed":false,"reason":"7d quota 90%"}`: "/model      paused switch to claude-opus-5-5 (7d quota 90%)",
		`not json`: "not json",
	} {
		if got := formatDecision(in); !strings.HasSuffix(got, want) {
			t.Errorf("formatDecision(%s)\n got %q\nwant suffix %q", in, got, want)
		}
	}
}

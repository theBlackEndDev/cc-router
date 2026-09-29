// Package router holds cc-router's decision logic: tiers, the route table, the
// Cap rule, quota step-down, and the hook entry points that wire them to
// Claude Code.
package router

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Tier orders models by cost. Higher is more expensive.
type Tier int

const (
	Grunt Tier = iota
	Build
	Plan
)

var tierNames = [...]string{"grunt", "build", "plan"}

func (t Tier) String() string {
	if t < Grunt || t > Plan {
		return "unknown"
	}
	return tierNames[t]
}

func parseTier(s string) (Tier, bool) {
	for i, n := range tierNames {
		if strings.EqualFold(s, n) {
			return Tier(i), true
		}
	}
	return 0, false
}

// GuardConfig controls the scout guard (Phase 5).
type GuardConfig struct {
	Enabled   bool `json:"enabled"`
	ReadRun   int  `json:"readRun"`
	MaxNudges int  `json:"maxNudges"`
}

// Config is the merged result of defaults, plugin userConfig (env) and the
// optional config.json in the plugin data directory.
type Config struct {
	Models    [3]string // indexed by Tier
	WarnAt    float64   // percent, 1..100
	DegradeAt float64   // percent, 1..100
	Guard     GuardConfig
	Routes    map[string]Tier
	Warnings  []string
}

// DefaultRoutes maps subagent_type to tier. Types not listed are left alone.
func DefaultRoutes() map[string]Tier {
	return map[string]Tier{
		"cc-router:scout":      Grunt,
		"cc-router:scribe":     Grunt,
		"cc-router:researcher": Build,
		"cc-router:worker":     Build,
		"cc-router:reviewer":   Build,
		"cc-router:planner":    Build,
		"Explore":              Grunt,
		"general-purpose":      Build,
		"Plan":                 Plan,
	}
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Models:    [3]string{"haiku", "sonnet", "opus"},
		WarnAt:    70,
		DegradeAt: 85,
		Guard:     GuardConfig{Enabled: true, ReadRun: 6, MaxNudges: 2},
		Routes:    DefaultRoutes(),
	}
}

// Getenv abstracts os.Getenv for tests.
type Getenv func(string) string

// option reads a plugin userConfig value. Claude Code exports them as
// CLAUDE_PLUGIN_OPTION_<KEY>; accept both the upper-cased and the literal key.
func option(env Getenv, key string) string {
	if v := env("CLAUDE_PLUGIN_OPTION_" + strings.ToUpper(key)); v != "" {
		return v
	}
	return env("CLAUDE_PLUGIN_OPTION_" + key)
}

// fileConfig is the shape of <data>/config.json. Every field is optional.
type fileConfig struct {
	Routes map[string]string `json:"routes"`
	Guard  *struct {
		ReadRun   *int `json:"readRun"`
		MaxNudges *int `json:"maxNudges"`
	} `json:"guard"`
}

// Load merges defaults, userConfig from env, and <dataDir>/config.json.
// Bad values keep the default and add a warning; Load never fails.
func Load(env Getenv, dataDir string) Config {
	c := Default()

	for t, key := range [...]string{"grunt_model", "build_model", "plan_model"} {
		if v := strings.TrimSpace(option(env, key)); v != "" {
			c.Models[t] = v
		}
	}
	c.WarnAt = percentOption(env, "warn_at", c.WarnAt, &c.Warnings)
	c.DegradeAt = percentOption(env, "degrade_at", c.DegradeAt, &c.Warnings)
	if c.WarnAt > c.DegradeAt {
		c.Warnings = append(c.Warnings, fmt.Sprintf("warn_at (%g) exceeds degrade_at (%g), using defaults", c.WarnAt, c.DegradeAt))
		d := Default()
		c.WarnAt, c.DegradeAt = d.WarnAt, d.DegradeAt
	}
	if v := option(env, "guard_enabled"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			c.Guard.Enabled = b
		} else {
			c.Warnings = append(c.Warnings, fmt.Sprintf("guard_enabled: expected true/false, got %q", v))
		}
	}

	if dataDir != "" {
		mergeFile(&c, filepath.Join(dataDir, "config.json"))
	}
	return c
}

func percentOption(env Getenv, key string, def float64, warnings *[]string) float64 {
	v := strings.TrimSpace(option(env, key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 || f > 100 {
		*warnings = append(*warnings, fmt.Sprintf("%s: expected a number in (0, 100], got %q", key, v))
		return def
	}
	return f
}

func mergeFile(c *Config, path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			c.Warnings = append(c.Warnings, fmt.Sprintf("config.json: %v", err))
		}
		return
	}
	var fc fileConfig
	if err := json.Unmarshal(raw, &fc); err != nil {
		c.Warnings = append(c.Warnings, fmt.Sprintf("config.json: %v, ignoring file", err))
		return
	}
	for name, tier := range fc.Routes {
		if tier == "" || strings.EqualFold(tier, "none") {
			delete(c.Routes, name)
			continue
		}
		t, ok := parseTier(tier)
		if !ok {
			c.Warnings = append(c.Warnings, fmt.Sprintf("config.json routes.%s: unknown tier %q", name, tier))
			continue
		}
		c.Routes[name] = t
	}
	if fc.Guard != nil {
		if p := fc.Guard.ReadRun; p != nil {
			if *p > 0 {
				c.Guard.ReadRun = *p
			} else {
				c.Warnings = append(c.Warnings, "config.json guard.readRun: must be > 0")
			}
		}
		if p := fc.Guard.MaxNudges; p != nil {
			if *p >= 0 {
				c.Guard.MaxNudges = *p
			} else {
				c.Warnings = append(c.Warnings, "config.json guard.maxNudges: must be >= 0")
			}
		}
	}
}

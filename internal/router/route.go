package router

import (
	"fmt"
	"strings"
	"time"
)

// Family ranks a model alias or ID by cost: haiku < sonnet < opus-class.
// Unknown IDs return -1. "inherit" means the main session's model, which may
// be Opus, so it ranks as the most expensive.
func Family(model string) int {
	m := strings.ToLower(model)
	switch {
	case strings.Contains(m, "haiku"):
		return int(Grunt)
	case strings.Contains(m, "sonnet"):
		return int(Build)
	case strings.Contains(m, "opus"), strings.Contains(m, "fable"), m == "best", m == "inherit":
		return int(Plan)
	}
	return -1
}

// Window is one rate-limit window as reported to the statusline.
type Window struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"` // epoch seconds
}

// Quota is the account-wide snapshot written by the statusline wrapper.
type Quota struct {
	TS       int64   `json:"ts"` // epoch seconds when captured
	FiveHour *Window `json:"five_hour,omitempty"`
	SevenDay *Window `json:"seven_day,omitempty"`
}

// QuotaMaxAge is how old a snapshot may be before it is ignored.
const QuotaMaxAge = 15 * time.Minute

// Highest returns the most-used window that is fresh and has not reset.
func (q *Quota) Highest(now time.Time) (name string, w *Window) {
	if q == nil || now.Sub(time.Unix(q.TS, 0)) > QuotaMaxAge {
		return "", nil
	}
	for _, c := range []struct {
		n string
		w *Window
	}{{"5h", q.FiveHour}, {"7d", q.SevenDay}} {
		if c.w == nil || c.w.ResetsAt <= now.Unix() {
			continue
		}
		if w == nil || c.w.UsedPercentage > w.UsedPercentage {
			name, w = c.n, c.w
		}
	}
	return name, w
}

// Decision is the router's verdict for one Agent tool call.
type Decision struct {
	Routed    bool   // false: subagent type not in the route table, leave untouched
	Tier      Tier   // tier from the route table
	Effective Tier   // tier after quota step-down
	Model     string // model to run
	Reason    string
}

// Route applies the route table, quota step-down and the Cap rule.
//
// Cap: Claude may pass a model cheaper than (or equal to) the effective tier
// and it is kept; anything more expensive, or anything the router cannot rank,
// is replaced with the effective tier's model.
func Route(c Config, subagentType, requested string, q *Quota, now time.Time) Decision {
	tier, ok := c.Routes[subagentType]
	if !ok {
		return Decision{Reason: "not in route table"}
	}
	d := Decision{Routed: true, Tier: tier, Effective: tier}
	reasons := []string{"tier " + tier.String()}

	if name, w := q.Highest(now); w != nil && w.UsedPercentage >= c.DegradeAt && tier > Grunt {
		d.Effective = tier - 1
		reasons = append(reasons, fmt.Sprintf("quota %s %.0f%% >= %.0f%%, stepped down to %s", name, w.UsedPercentage, c.DegradeAt, d.Effective))
	}
	d.Model = c.Models[d.Effective]

	if requested != "" && requested != d.Model {
		fr, ft := Family(requested), Family(d.Model)
		if fr >= 0 && ft >= 0 && fr <= ft {
			d.Model = requested
			reasons = append(reasons, "kept caller's model "+requested)
		} else {
			reasons = append(reasons, "capped caller's model "+requested)
		}
	}
	d.Reason = strings.Join(reasons, "; ")
	return d
}

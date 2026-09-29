# cc-router

**Use the right Claude model for each job, automatically.**

cc-router is a plugin for [Claude Code](https://code.claude.com/docs/en/overview). When
Claude hands work to a helper (a *subagent*), cc-router picks the model for that helper:
a cheap, fast model for searching and reading, a mid-range model for writing code, and the
most capable model only for planning. When your usage limit is getting close, it steps
everything down one level so you don't run out halfway through the day.

You don't have to change how you work. Install it, and it runs in the background.

It works on **macOS and Linux** (Intel and ARM). You don't need Go, Node, Python or anything
else installed; the plugin ships ready-to-run programs.

---

## Contents

1. [Why you might want this](#why-you-might-want-this)
2. [Five words to know first](#five-words-to-know-first)
3. [Install](#install)
4. [What you'll notice day to day](#what-youll-notice-day-to-day)
5. [How it decides](#how-it-decides)
6. [Settings](#settings)
7. [Commands](#commands)
8. [Things it can't do](#things-it-cant-do)
9. [Troubleshooting](#troubleshooting)
10. [Uninstall](#uninstall)
11. [What it stores and where](#what-it-stores-and-where)
12. [Working on cc-router itself](#working-on-cc-router-itself)

---

## Why you might want this

On many Claude plans, Claude Code starts on **Opus**, the most capable model and also
the one that uses up your usage limit fastest. By default, helpers that Claude starts
inherit that same model. So when Claude sends a helper to "go find where the login code
lives", that search runs on Opus too, even though a much cheaper model would find it just
as well.

cc-router fixes that:

- Searching and reading run on **Haiku** (cheap and fast).
- Writing code, reviewing and research run on **Sonnet** (strong and mid-priced).
- **Opus** is kept for planning.
- When you're close to your limit, everything moves down one step, and you get told.

---

## Five words to know first

| Word | What it means here |
|---|---|
| **Model** | Which Claude "brain" answers. Claude Code calls them by short names: `haiku` (cheapest), `sonnet` (middle), `opus` (most capable, most expensive). |
| **Subagent** | A helper Claude starts to do one piece of work in its own separate conversation, then report back. Claude Code starts these through a tool called **Agent**. |
| **Tier** | cc-router's word for a price level. There are three: **grunt** (cheap), **build** (middle) and **plan** (top). Each tier has a model. |
| **Quota window** | Subscription plans limit usage over a **5-hour** window and a **7-day** window. Claude Code reports how much of each you've used as a percentage. |
| **Statusline** | The line of text at the bottom of the Claude Code screen. It's the only place Claude Code shares your quota percentages, which is why cc-router's setup step hooks into it. |

---

## Install

### Before you start

- Claude Code **2.1.281 or newer**. Check with:
  ```bash
  claude --version
  ```
  If it's older, update it (Claude Code usually updates itself when you start it).
- macOS or Linux. Windows isn't supported yet.

### Step 1: Add the marketplace and install the plugin

A *marketplace* is just a list of plugins Claude Code can install. This repository is
both the marketplace and the plugin. Run these two commands in your normal terminal (not
inside Claude Code):

```bash
claude plugin marketplace add theblackenddev/cc-router
claude plugin install cc-router@cc-router
```

You should see something like:

```
✔ Successfully added marketplace: cc-router
✔ Successfully installed plugin: cc-router@cc-router (scope: user)
6 userConfig options not yet set — run /plugin configure cc-router@cc-router in Claude Code, or pass --config KEY=VALUE.
```

> **The "options not yet set" line is fine.** It means you're using the default settings,
> which are the ones described in this README. You only need to change them if you want
> different models or thresholds (see [Settings](#settings)).

### Step 2: Start (or restart) Claude Code

If Claude Code was already open, type `/reload-plugins` or restart it. From now on, every
subagent is routed. **This step alone gives you the model routing.**

### Step 3 (recommended): Turn on quota awareness

Inside Claude Code, type:

```
/cc-router:setup
```

This connects cc-router to the statusline so it can see your usage percentages. Here's what
happens:

1. Claude shows you a **small change to the `statusLine` entry** in your
   `~/.claude/settings.json` file and asks you to approve it. Nothing else in the file is
   touched. If you already have a custom statusline, it keeps working; cc-router just reads
   the same information alongside it.
2. Claude asks two optional questions. **It's safe to answer "no" to both.**
   - *Set the main model to `opusplan`?* Claude Code has a **plan mode** where it thinks
     through a task before changing anything (press `shift+tab` to switch into it).
     `opusplan` means: use Opus while in plan mode, and Sonnet the rest of the time.
   - *Set `CLAUDE_CODE_SUBAGENT_MODEL`?* Read [the note about your own agents](#a-note-about-your-own-agents)
     before saying yes.

You can undo this at any time with `/cc-router:setup remove`, which puts your settings file
back exactly as it was.

### Step 4: Check it worked

```
/cc-router:doctor
```

A healthy install looks like this:

```
[ok  ] binary             cc-router v0.1.0 linux/amd64
[ok  ] data dir           /home/you/.claude/plugins/data/cc-router-cc-router (writable)
[ok  ] claude version     2.1.285 (need >= 2.1.281)
[ok  ] config             plan=opus build=sonnet grunt=haiku
[ok  ] statusline         wired through cc-router
[info] quota              5h 28% (resets …), 7d 35% (resets …); captured 1s ago
[info] hooks              last routing decision at …
```

Any line marked `FAIL` comes with a short explanation of how to fix it.

---

## What you'll notice day to day

Mostly nothing, which is the point. A few things do show up:

**Subagents run on cheaper models.** Claude sees a short note each time, for example:

```
cc-router: Explore → haiku (tier grunt)
```

**A quota notice, once, when you pass a threshold.** At 70% of a window you get a heads-up;
at 85% subagents step down a tier:

```
UserPromptSubmit says: cc-router: 7d quota window at 86% (resets Oct 3 16:00). Subagents are now stepped down one tier.
```

Your statusline also shows `cc-router ↓ subagents stepped down (7d 86%)` while that's
happening.

**A pause before switching to Opus when you're low.** If a quota window is at 85% or more
and you type `/model opus` (or switch to whatever your plan-tier model is), cc-router stops
it once and tells you why. Type the same command again within 60 seconds and it goes
through. (Claude Code may then ask its own "Switch model?" question as usual.)

**The "scout guard" (only when your main session is on Opus).** If Claude tries to run a
search whose output could be huge, such as `grep -rn something .` with no limit, cc-router
stops it once and suggests handing the question to the cheap `scout` helper instead:

```
PreToolUse:Bash hook error: cc-router: this is an unbounded search. …
```

That "hook error" wording comes from Claude Code; nothing is broken. Claude then either
uses the scout, adds a limit such as `| head -50`, or runs the exact same command again,
which cc-router always lets through. Long runs of reading whole files only get a gentle
note, never a block. The guard speaks up **at most twice per session** (blocks and notes
count together), then stays quiet, so it never gets in the way for long.

---

## How it decides

### The three tiers

| Tier | Default model | Used for |
|---|---|---|
| **grunt** | `haiku` | Searching code, reading lots of files, quick web lookups, summaries, simple repetitive edits |
| **build** | `sonnet` | Writing code, code review, in-depth research, drafting plans |
| **plan** | `opus` | Big-picture planning (Claude Code's built-in `Plan` helper) |

### Which helper goes to which tier

The tier comes from **which helper Claude starts**, not from reading your request. cc-router
adds six helpers and also routes three that come with Claude Code:

| Helper | Tier | What it's for |
|---|---|---|
| `cc-router:scout` | grunt | Find things in the code, answer "where / how" questions, one-page web lookups. Read-only. |
| `cc-router:scribe` | grunt | Docs, summaries, mechanical edits like renames |
| `cc-router:researcher` | build | Research across many sources and comparing options |
| `cc-router:worker` | build | Implement a change from a clear description |
| `cc-router:reviewer` | build | Review code for bugs and risks. Read-only. |
| `cc-router:planner` | build | Draft a step-by-step plan for the main session to check |
| `Explore` (built in) | grunt | Claude Code's own code-search helper |
| `general-purpose` (built in) | build | Claude Code's catch-all helper |
| `Plan` (built in) | plan | Claude Code's planning helper |

**Your own subagents are left alone.** Anything not in this table keeps whatever model it
already uses. You can add your own helpers to the table (see [Settings](#settings)).

At the start of every session cc-router gives Claude a short guide explaining which helper
to use for which kind of work. You can also ask directly, for example: *"use the
cc-router:scout agent to find where sessions are created"*.

### The "no upgrades" rule

Claude can ask for a specific model when it starts a helper. cc-router lets it choose a
model that costs **the same or less** than the tier's, but never a **more expensive** one.
For example, if Claude asks for `scout` on Opus, the scout still runs on Haiku. A model name
cc-router doesn't recognise is also replaced with the tier's model, to be safe. If a task truly needs a
stronger model, the right move is to use a higher-tier helper (such as `researcher` instead
of `scout`).

### When quota runs low

When either quota window reaches the **step-down** threshold (85% by default), every tier
drops one level: plan → build, build → grunt. Grunt stays on grunt. When the window resets,
routing goes back to normal on its own.

This needs [Step 3](#step-3-recommended-turn-on-quota-awareness). Without it, cc-router can't
see your usage, so it routes by tier only. If you use an API key instead of a subscription,
Claude Code doesn't report usage percentages, so quota features stay off.

---

## Settings

### Change models or thresholds

Inside Claude Code:

```
/plugin configure cc-router@cc-router
```

Or when installing:

```bash
claude plugin install cc-router@cc-router --config plan_model=opus --config degrade_at=90
```

| Setting | Default | What it does |
|---|---|---|
| `plan_model` | `opus` | Model for the plan tier |
| `build_model` | `sonnet` | Model for the build tier |
| `grunt_model` | `haiku` | Model for the grunt tier |
| `warn_at` | `70` | Percentage of a quota window that triggers a heads-up |
| `degrade_at` | `85` | Percentage that steps every tier down one level |
| `guard_enabled` | `true` | Turn the scout guard on or off |

Model settings accept any name Claude Code understands: `haiku`, `sonnet`, `opus`,
`fable`, `best`, or a full model ID. If you use Claude Code through Amazon Bedrock,
Google Cloud or Microsoft Foundry, the short names still work.

To be sure a changed setting is used everywhere, start a new Claude Code session.
(`/cc-router:status` and `/cc-router:doctor` show the settings as they were when the
current session started.)

### Route your own helpers (optional)

To put your own subagents into a tier, create this file:

`~/.claude/plugins/data/cc-router-cc-router/config.json`

```json
{
  "routes": {
    "my-test-writer": "build",
    "my-log-reader": "grunt",
    "Explore": "none"
  },
  "guard": { "readRun": 6, "maxNudges": 2 }
}
```

- The names on the left are subagent names, exactly as Claude Code shows them
  (plugin helpers look like `plugin-name:agent-name`).
- `"none"` takes a helper out of routing completely.
- `readRun` is how many whole-file reads in a row are fine; the gentle "consider the scout"
  note comes on the next one (the 7th, with the default of 6). `maxNudges` is how many
  times per session the guard speaks up before going quiet.

Changes to this file take effect immediately, no restart needed. `/cc-router:doctor` warns
you if the file has a mistake; bad entries are skipped and the rest keeps working.

### A note about your own agents

Setup offers to set `CLAUDE_CODE_SUBAGENT_MODEL`. That makes helpers that cc-router can't
see directly (such as the ones behind `/deep-research`) use Sonnet instead of inheriting
Opus.

**Be aware:** that setting also applies to **your own** subagents that don't have a
`model:` line in their definition file. If you have agents of your own, either:

- add a `model:` line to each of them (for example `model: opus`) so they keep the model you
  want, or
- accept that they'll run on Sonnet.

If you're not sure, answer "no". You can always add it later.

---

## Commands

Type these inside Claude Code:

| Command | What it does |
|---|---|
| `/cc-router:setup` | Connect to the statusline for quota awareness (asks before changing anything) |
| `/cc-router:setup remove` | Undo setup and restore your settings exactly |
| `/cc-router:status` | Show the tier models, the routing table, your quota and the last 10 decisions |
| `/cc-router:doctor` | Check the install and explain any problems |

Here's what `/cc-router:status` looks like after the scout has run once:

```
Tiers:  plan=opus  build=sonnet  grunt=haiku
Quota:  warn at 70%, step down at 85%
Guard:  enabled=true readRun=6 maxNudges=2

Routes:
  Explore                grunt  → haiku
  Plan                   plan   → opus
  cc-router:planner      build  → sonnet
  cc-router:researcher   build  → sonnet
  cc-router:reviewer     build  → sonnet
  cc-router:scout        grunt  → haiku
  cc-router:scribe       grunt  → haiku
  cc-router:worker       build  → sonnet
  general-purpose        build  → sonnet

Quota snapshot:
  5h 28% (resets Sep 29 20:15), 7d 35% (resets Oct 2 17:15); captured 1s ago

Recent decisions:
  Sep 29 17:15  routed      cc-router:scout → haiku (tier grunt)
  Sep 29 17:15  finished    cc-router:scout on claude-haiku-4-5-20251001, 10340 tokens, 4.3s
```

---

## Things it can't do

These are limits of what Claude Code lets plugins do, not bugs:

- **It can't change your main session's model for you.** It can warn you and pause a switch
  to Opus, but only you can run `/model`.
- **The first tool call of each session isn't guarded** unless you've run setup. The scout
  guard needs to know your main model; without the statusline, it can only find out after
  Claude's first reply is saved.
- **In non-interactive mode (`claude -p`)** the scout guard only works if you've run setup,
  and quota features depend on the statusline, which that mode may not run.
- **Workflow helpers and agent teams aren't routed**, because Claude Code starts them without
  the Agent tool. `CLAUDE_CODE_SUBAGENT_MODEL` covers them if you want.
- **Quota features need a subscription.** API-key users get tier routing only.
- **Windows** isn't supported yet.

---

## Troubleshooting

**Start with `/cc-router:doctor`.** It checks nearly everything below.

**"Nothing seems to be routed."**
Run `/cc-router:status` and look at *Recent decisions*. If it's empty, no subagent has been
started yet. Try: *"use the cc-router:scout agent to list the files in this folder"*, then
check again.

**"My statusline disappeared after setup."**
Wait a few seconds (it refreshes on a timer) or restart Claude Code. If it's still blank,
run `/cc-router:setup remove` and tell us what happened (see below).

**"Claude says a hook blocked my command."**
That's the scout guard or the Opus pause, both described in
[What you'll notice](#what-youll-notice-day-to-day). Repeating the same command goes
through. To turn the guard off: `/plugin configure cc-router@cc-router` and set
`guard_enabled` to `false`.

**Reporting a bug.**
Start Claude Code with debug logging on, reproduce the problem, then attach the log:

```bash
CC_ROUTER_DEBUG=1 claude
# ...reproduce...
cat ~/.claude/plugins/data/cc-router-cc-router/debug.log
cat ~/.claude/plugins/data/cc-router-cc-router/errors.log
```

Open an issue at https://github.com/theblackenddev/cc-router/issues with the output of
`/cc-router:doctor` and those logs. The debug log contains the hook inputs, which can
include file paths and prompts, so look it over before sharing.

**cc-router never breaks Claude Code.** If anything inside it fails, it writes the error to
`errors.log` and gets out of the way. The two deliberate exceptions are the scout guard and
the Opus pause, and both let a repeated command through.

---

## Uninstall

1. If you ran setup, undo it first (inside Claude Code):
   ```
   /cc-router:setup remove
   ```
2. Remove the plugin (in your terminal):
   ```bash
   claude plugin uninstall cc-router@cc-router
   ```
3. Optionally remove the marketplace too:
   ```bash
   claude plugin marketplace remove cc-router
   ```

**Please do step 1 first.** If you skip it:

- and you had your own statusline before setup, it keeps working, because the setup change
  falls back to your original command when cc-router isn't there;
- but if you had **no** statusline before setup, the bottom line goes blank. Fix it by
  deleting the `"statusLine"` entry from `~/.claude/settings.json`.

If setup also set `"model": "opusplan"` or `CLAUDE_CODE_SUBAGENT_MODEL` because you said yes,
those stay in `~/.claude/settings.json`; delete them by hand if you don't want them.

---

## What it stores and where

cc-router makes **no network requests**. Everything stays on your machine, in the folder
Claude Code gives each plugin:

`~/.claude/plugins/data/cc-router-cc-router/`

| File | What's in it |
|---|---|
| `decisions.jsonl` | One line per routing decision (helper name, tier, model, reason). Rotates at 1 MB. |
| `decisions.jsonl.1` | The previous log, kept when `decisions.jsonl` rotates |
| `quota.json` | Your latest usage percentages, copied from the statusline |
| `sessions/`, `notices/`, `guard/`, `switch-*.json` | Small per-session files; cleaned up after 7 days |
| `options.json` | Your settings as of the last session start (so `/cc-router:doctor` can show them) |
| `root` | Where the current version of the plugin is installed (it moves on every update) |
| `statusline.sh`, `statusline-original.json` | Created by setup; the second is your original statusline, kept so `remove` can restore it |
| `errors.log`, `debug.log` | Only if something failed, or if you turned on `CC_ROUTER_DEBUG` |

The only file outside that folder it ever changes is your Claude Code settings file
(`~/.claude/settings.json`, or `$CLAUDE_CONFIG_DIR/settings.json` if you've set that), and
only through `/cc-router:setup`, which shows you the change and asks first.

---

## Working on cc-router itself

You need [Go](https://go.dev/dl/) 1.22 or newer and Claude Code.

```bash
git clone https://github.com/theblackenddev/cc-router
cd cc-router
make test        # go vet + all tests
make build       # build for your machine only
claude --plugin-dir ./plugins/cc-router   # try your changes without installing
```

### How the pieces fit

```
plugins/cc-router/
  .claude-plugin/plugin.json   plugin name, version and the settings users can change
  hooks/hooks.json             which Claude Code events call cc-router
  agents/*.md                  the six helpers (scout, worker, …)
  skills/*/SKILL.md            the /cc-router:* commands
  bin/cc-router                small shell script that picks the right program for your OS
  libexec/cc-router-<os>-<arch> the programs themselves (built by `make dist`)
cmd/cc-router/                 command-line entry point
internal/router/               all the logic, with tests next to each file
```

Every hook calls the same program with a different event name, for example
`cc-router hook pre-agent`. Claude Code sends the event as JSON on standard input, and
cc-router answers with JSON on standard output. `internal/router/hooks.go` is the best
place to start reading.

### Releasing

The built programs are committed to the repository, because that's what people install.
Before tagging a release:

```bash
VERSION=v0.2.0 make dist   # builds all four: macOS/Linux × Intel/ARM
make validate              # checks the plugin and marketplace files
```

Then bump `version` in `plugins/cc-router/.claude-plugin/plugin.json`, commit, and tag.

## License

[MIT](LICENSE)

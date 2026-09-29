---
name: setup
description: Wire cc-router into the Claude Code statusline so it can see quota usage (needed for quota warnings and step-down), and optionally set opusplan and a default subagent model. Pass "remove" to undo.
disable-model-invocation: true
argument-hint: "[remove]"
allowed-tools: Bash(cc-router setup *)
---
The user's argument is: `$ARGUMENTS`

## If the argument is "remove"

1. Run: `cc-router setup --remove --data "${CLAUDE_PLUGIN_DATA}"`
2. If it prints NOT INSTALLED, tell the user there is nothing to undo and stop.
3. Otherwise edit the SETTINGS FILE it names with the Edit tool, doing exactly what RESTORE says (replace the `"statusLine"` value, or delete the key). Change nothing else.
4. Tell the user it's undone. If they had set `"model": "opusplan"` or `CLAUDE_CODE_SUBAGENT_MODEL` during setup, remind them those are separate settings they can remove by hand.

## Otherwise (install)

1. Run: `cc-router setup --data "${CLAUDE_PLUGIN_DATA}" --root "${CLAUDE_PLUGIN_ROOT}"`
2. If it prints ALREADY INSTALLED, say so and skip to step 4.
3. Show the user CURRENT and NEW in one short message, then edit the SETTINGS FILE with the Edit tool: set the top-level `"statusLine"` key to the NEW value exactly as printed (add the key if missing). Keep every other key and the file's formatting unchanged. The user approves the edit in the permission prompt; if they decline, stop.
4. Ask the user two optional questions, one at a time, and apply each "yes" with a separate Edit to the same file:
   - **Main session model.** "Set the main model to `opusplan`? Claude Code then uses Opus in plan mode and Sonnet while building." If yes, set top-level `"model": "opusplan"`.
   - **Default subagent model.** "Set `CLAUDE_CODE_SUBAGENT_MODEL` to the build model (sonnet by default)? Workflow agents such as /deep-research then run on it instead of inheriting Opus. It also applies to your own agents that have no `model:` line." If yes, add `"CLAUDE_CODE_SUBAGENT_MODEL": "sonnet"` inside the top-level `"env"` object (create it if missing).
5. Finish with: the statusline picks up the change on its next refresh (restart Claude Code if it doesn't), and `/cc-router:doctor` confirms everything is wired.

---
name: doctor
description: Check the cc-router install - binary, data directory, Claude Code version, configuration, quota wiring and hook activity.
disable-model-invocation: true
allowed-tools: Bash(cc-router doctor *)
---
Run this and show the output to the user as-is:

```bash
cc-router doctor --data "${CLAUDE_PLUGIN_DATA}"
```

For every line marked FAIL, explain the fix in one sentence. If everything passes, say so in one line.

---
name: status
description: Show cc-router's tier models, route table, quota snapshot and recent routing decisions.
disable-model-invocation: true
allowed-tools: Bash(cc-router status *)
---
Run this and show the output to the user as-is, then add a one-line summary (which tier is most used, and whether quota step-down is active):

```bash
cc-router status --data "${CLAUDE_PLUGIN_DATA}"
```

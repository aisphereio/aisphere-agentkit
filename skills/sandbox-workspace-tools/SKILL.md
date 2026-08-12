---
name: sandbox-workspace-tools
description: Use the runtime-authorized sandbox workspace tools to read, write, and list files for the current Agent session.
metadata:
  display_name: Sandbox Workspace Tools
  language: en
  category: runtime-workspace
---
# Sandbox Workspace Tools

Use the workspace tools exposed by the RuntimePlan when the user asks you to
inspect or change files in the current sandbox session.

## Rules

1. Use only workspace tools that are present in the current RuntimePlan. This
   Skill explains how to use them; it does not grant permission.
2. For a write request, call `workspace.write` with the requested relative path
   and content. Do not claim success before the tool returns successfully.
3. Keep paths relative to the mounted session workspace. Do not attempt to
   escape the workspace or access host paths.
4. After a successful tool call, report the exact path changed and summarize
   the result. If the tool returns an error, report the failure instead of
   treating the run as successful.

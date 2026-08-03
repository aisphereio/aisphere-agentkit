# Sub Agent Task Runner

This document defines the unified sub-agent invocation model.

## One concept: `sub_agents`

All parent-child agent relationships are declared in the parent agent YAML under `sub_agents`.

A child can be invoked with two semantics:

- `invocation.mode: handoff`: native ADK `transfer_to_agent`. The child may take over the conversation.
- `invocation.mode: task`: runtime background task. The child runs and returns compact results to the controller.

## Task-mode generated tools

When an LlmAgent has one or more task-mode sub_agents, runtime injects `SubAgentTaskRunnerToolset` with:

- `subagent_list_tasks`
- `subagent_run_task`
- `subagent_run_tasks`

The user-facing conversation should remain with the controller agent. Task-mode children write large outputs to workspace and return only refs/summary.

## YAML example

```yaml
sub_agents:
  - id: chapter_worker
    ref: book_chapter_research_worker
    role: chapter_worker
    invocation:
      mode: task
      execution: serial
      parallel: false
      max_concurrency: 1
    context:
      mode: fresh
      inherit_parent_messages: false
    workspace:
      mode: fork_commit
      commit_to_parent: true
    output:
      mode: summary_only
      max_chars: 1500
```

## Controller call example

```json
{
  "agent_id": "chapter_worker",
  "mode": "serial",
  "shared": {
    "run_id": "book_dialogue__tangqi__001_010",
    "book_id": "eb43f765c8746752",
    "book_name": "唐骑",
    "skill_id": "chapter-character-dialogue-research"
  },
  "tasks": [
    {"chapter_no": 1},
    {"chapter_no": 2},
    {"chapter_no": 3}
  ]
}
```

## Return semantics

Task-mode always returns to the parent/controller. It must not update the active conversational owner like `transfer_to_agent`.


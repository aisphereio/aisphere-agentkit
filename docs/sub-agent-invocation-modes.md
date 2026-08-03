# Sub Agent Invocation Modes

This patch makes Sub Agents the single place to declare parent/child agent relationships.
The runtime then chooses how to expose each child based on `sub_agents[].invocation.mode`.

## Modes

### `handoff`

Native ADK `transfer_to_agent` behavior.

- The child is added to the parent agent's `SubAgents` list.
- The LLM gets `transfer_to_agent`.
- Control may move to the target agent.
- The next user turn may continue with the transferred agent depending on runner logic.

Use it for expert handoff and conversational switching.

```yaml
sub_agents:
  - id: ops_expert
    ref: ops_expert
    invocation:
      mode: handoff
```

### `task`

Background task behavior implemented today with `AgentTool`.

- The child is exposed to the parent as a callable tool.
- The child runs in an isolated/fresh/sticky child session according to policy.
- The child returns a compact result to the parent.
- The parent remains the user-facing decision agent.

Use it for workers, chapter analysis, review tasks, and file-producing jobs.

```yaml
sub_agents:
  - id: chapter_worker
    ref: book_chapter_research_worker
    role: worker
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

## Compatibility

Legacy forms still work:

```yaml
sub_agents:
  - config_path: ../book_chapter_research_worker/root_agent.yaml
    mode: tool
    context_mode: fresh
    max_output_chars: 1500
```

They are normalized to `task` mode.

## Future batch runner

`task` mode is currently backed by `AgentTool`. A later `SubAgentTaskRunnerToolset` can use the same `sub_agents` declarations to provide deterministic serial/parallel fan-out/fan-in without changing agent YAML.

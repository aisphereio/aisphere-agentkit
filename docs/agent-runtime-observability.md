# Agent Runtime Observability

This document defines the first engineering pass for making ADK agent execution observable.

## Goal

A single user request should be traceable from HTTP/SSE into runner, agent, model, tool, and later skill/sub-agent execution.

The core questions this layer answers are:

- Which agent was selected by the runner?
- Which agents entered and exited during the invocation?
- Which tools were visible to the model?
- Which tools were actually called?
- Which model call started, completed, or failed?
- Where did the invocation fail or stop?

## Current implementation scope

This pass intentionally avoids building a new observability system. It extends the existing `internal/runtimetrace` JSONL trace recorder and adds normalized events.

Implemented in this pass:

- `internal/runtimetrace/events.go`
  - Standard event names such as `invocation.started`, `agent.enter`, `tools.bound`, `tool.call`, and `model.call.completed`.
- `internal/runtimetrace/multi.go`
  - `MultiRecorder`, a fan-out recorder for future sinks such as PG `run_steps`, OpenTelemetry, or external trace systems.
- `internal/runtimetrace/trace.go`
  - Adds `run_id` to trace events.
  - Adds `WithRunID` / `RunID` helpers.
  - Removes the hard dependency on the `agent` package to avoid import cycles when agent lifecycle tracing is added.
- `agent/agent.go`
  - Records `agent.enter`, `agent.exit`, and `agent.error` around every agent call.
- `runner/runner.go`
  - Records `invocation.started`, `agent.selected`, `invocation.completed`, and `invocation.failed`.
- `internal/llminternal/runtime_trace.go`
  - Records normalized `model.call.started`, `model.call.completed`, `model.call.error`, `tools.bound`, `tool.call`, `tool.result.normalized`, and `tool.error` events.
  - Keeps old events like `llm.request.final`, `llm.response.final`, `tool.call.detected`, and `tool.result` for compatibility.

## Concept model

Do not mix up these terms:

- `run_sse`: HTTP/SSE transport endpoint.
- `Runner`: internal execution engine.
- `Run`: one platform execution record, eventually stored in PG.
- `Agent`: executable unit.
- `SubAgent`: an agent delegated to or orchestrated by a parent agent.
- `Tool`: callable capability visible to the model.
- `AgentTool`: a special tool implementation that runs another agent internally.
- `Skill`: prompt/instruction package injected into the agent context. It is not a tool call.

A concise rule:

```text
Skill is injected.
Tool is called.
Agent is run.
Runner dispatches.
run_sse streams.
```

## Event taxonomy

### Invocation events

```text
invocation.started
agent.selected
invocation.completed
invocation.failed
```

These are emitted by `runner.Run`.

### Agent events

```text
agent.enter
agent.exit
agent.error
```

These are emitted by `agent.Run` and represent actual agent call boundaries.

### Model events

```text
model.call.started
model.call.completed
model.call.error
```

These are emitted around final LLM request/response trace recording.

### Tool events

```text
tools.bound
tool.call
tool.result.normalized
tool.error
```

These distinguish:

- tools made visible to the model (`tools.bound`)
- tools actually requested by the model (`tool.call`)
- tool execution result (`tool.result.normalized` / `tool.error`)

### Skill events reserved for next pass

```text
skill.declared
skill.resolved
skill.injected
skill.skipped
skill.error
```

This pass defines the names but does not yet wire the skill loader. The next pass should record at least `skill.injected` when skill content is appended to the final prompt/context.

## How to verify

Enable runtime tracing in `adk.yaml`:

```yaml
runtime:
  tracing:
    enabled: true
    root: ./.adk/data/traces
    dump_llm_request: true
    dump_llm_response: true
    dump_tool_events: true
    dump_stream_chunks: false
    max_content_chars: 8000
```

Run an agent once, then inspect the trace directory:

```powershell
Get-ChildItem .\.adk\data\traces | Sort-Object LastWriteTime -Descending | Select-Object -First 3
Get-Content .\.adk\data\traces\<invocation_id>.jsonl -Tail 80
```

Expected event types include:

```text
invocation.started
agent.selected
agent.enter
model.call.started
tools.bound
llm.request.final
tool.call
tool.result.normalized
model.call.completed
agent.exit
invocation.completed
```

If a run fails, expected events include:

```text
agent.error
model.call.error
tool.error
invocation.failed
```

## Current limitations

This pass does not yet implement:

- PG `run_steps` sink.
- `/run_sse` automatic PG run creation.
- Agent graph API.
- Explicit `skill.injected` tracing.
- Frontend run timeline UI.
- AgentTool nested-run correlation.

## Next recommended step

Implement a `RunStepRecorder` that uses `MultiRecorder` to write selected normalized events to PG `run_steps` while keeping detailed payloads in JSONL trace files.

Recommended whitelist for PG:

```text
invocation.started
invocation.completed
invocation.failed
agent.selected
agent.enter
agent.exit
agent.error
skill.injected
tools.bound
tool.call
tool.result.normalized
tool.error
```

Do not write full LLM prompts, stream chunks, or full model responses into PG.

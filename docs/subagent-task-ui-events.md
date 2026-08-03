# Sub-agent task UI events

`SubAgentTaskRunnerToolset` now emits runtime trace events that are also streamed as `runtime.log` SSE rows.

Frontend can render these as expandable sub-agent task cards:

- `subagent.task.plan`: group card for a task batch.
- `subagent.task.started`: single task card becomes running.
- `subagent.task.completed`: single task card becomes completed and shows artifact refs.
- `subagent.task.failed`: single task card becomes failed and shows error.
- `subagent.task.batch_completed`: group card summary.

Each event has `data.ui`:

```json
{
  "component": "subagent_task_card",
  "status": "running|completed|failed",
  "expandable": true,
  "title": "book_chapter_research_worker · chapter 1"
}
```

For a group event:

```json
{
  "component": "subagent_task_group",
  "status": "planned|completed|failed",
  "expandable": true,
  "title": "book_chapter_research_worker × 3"
}
```

The ADK web UI can map these rows to cards instead of showing only a blocking tool chip.

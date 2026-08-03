// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package runtimetrace

// Standard runtime trace event names.
//
// Older event names such as "llm.request.final" and "tool.call.detected" are
// kept for compatibility. New observability surfaces should prefer these
// normalized names where possible.
const (
	// EventRuntimeLog is the SSE/transport envelope emitted by run_sse for
	// selected runtime trace events. The original trace event name is carried in
	// the payload as event_type.
	EventRuntimeLog = "runtime.log"

	EventInvocationStarted   = "invocation.started"
	EventInvocationCompleted = "invocation.completed"
	EventInvocationFailed    = "invocation.failed"
	EventAgentSelected       = "agent.selected"

	EventAgentEnter = "agent.enter"
	EventAgentExit  = "agent.exit"
	EventAgentError = "agent.error"

	EventModelCallStarted   = "model.call.started"
	EventModelCallCompleted = "model.call.completed"
	EventModelCallError     = "model.call.error"

	EventToolsBound = "tools.bound"
	EventToolCall   = "tool.call"
	EventToolResult = "tool.result.normalized"
	EventToolError  = "tool.error"

	EventSkillDeclared = "skill.declared"
	EventSkillResolved = "skill.resolved"
	EventSkillInjected = "skill.injected"
	EventSkillSkipped  = "skill.skipped"
	EventSkillError    = "skill.error"

	EventSubAgentTaskPlan           = "subagent.task.plan"
	EventSubAgentTaskStarted        = "subagent.task.started"
	EventSubAgentTaskCompleted      = "subagent.task.completed"
	EventSubAgentTaskFailed         = "subagent.task.failed"
	EventSubAgentTaskBatchCompleted = "subagent.task.batch_completed"
	// Child task observability events are emitted on the parent invocation so
	// the UI can render an inline, expandable sub-agent run monitor even when
	// the child session itself is ephemeral/in-memory.
	EventSubAgentTaskSessionCreated  = "subagent.task.session_created"
	EventSubAgentTaskPrompt          = "subagent.task.prompt"
	EventSubAgentTaskChildEvent      = "subagent.task.child_event"
	EventSubAgentTaskSessionDisposed = "subagent.task.session_disposed"
)

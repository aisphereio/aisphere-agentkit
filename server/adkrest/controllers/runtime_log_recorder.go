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

package controllers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/adk/internal/runtimetrace"
)

const maxRuntimeLogDataBytes = 12 * 1024

type runtimeLogSSERecorder struct {
	emit  func(string) error
	store SubAgentTaskObserveStore
}

func newRuntimeLogSSERecorder(emit func(string) error, store ...SubAgentTaskObserveStore) *runtimeLogSSERecorder {
	var s SubAgentTaskObserveStore
	if len(store) > 0 {
		s = store[0]
	}
	return &runtimeLogSSERecorder{emit: emit, store: s}
}

func (r *runtimeLogSSERecorder) Enabled() bool { return r != nil && (r.emit != nil || r.store != nil) }
func (r *runtimeLogSSERecorder) Root() string  { return "" }
func (r *runtimeLogSSERecorder) List() ([]runtimetrace.TraceFile, error) {
	return nil, nil
}
func (r *runtimeLogSSERecorder) Read(string, int) ([]runtimetrace.Event, error) {
	return nil, nil
}

func (r *runtimeLogSSERecorder) Record(ctx context.Context, ev runtimetrace.Event) {
	if r == nil || !isRuntimeLogEvent(ev.Type) {
		return
	}
	ev = runtimetrace.Enrich(ctx, ev)
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	payload := map[string]any{
		"type":          runtimetrace.EventRuntimeLog,
		"event_type":    ev.Type,
		"time":          ev.Time.Format(time.RFC3339Nano),
		"run_id":        ev.RunID,
		"invocation_id": ev.InvocationID,
		"app_name":      ev.AppName,
		"user_id":       ev.UserID,
		"session_id":    ev.SessionID,
		"agent_name":    ev.AgentName,
		"branch":        ev.Branch,
		"data":          ev.Data,
	}
	payload["event_id"] = runtimeLogEventID(payload)
	if r.store != nil {
		r.store.Record(payload)
	}
	if r.emit == nil {
		return
	}
	payload["data"] = trimRuntimeLogData(ev.Data)
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = r.emit(string(b))
}

func isRuntimeLogEvent(eventType string) bool {
	switch eventType {
	case runtimetrace.EventInvocationStarted,
		runtimetrace.EventInvocationCompleted,
		runtimetrace.EventInvocationFailed,
		runtimetrace.EventAgentSelected,
		runtimetrace.EventAgentEnter,
		runtimetrace.EventAgentExit,
		runtimetrace.EventAgentError,
		runtimetrace.EventModelCallStarted,
		runtimetrace.EventModelCallCompleted,
		runtimetrace.EventModelCallError,
		"llm.request.budget_check",
		"llm.request.final",
		"llm.response.final",
		runtimetrace.EventToolsBound,
		runtimetrace.EventToolCall,
		runtimetrace.EventToolResult,
		runtimetrace.EventToolError,
		runtimetrace.EventSkillDeclared,
		runtimetrace.EventSkillResolved,
		runtimetrace.EventSkillInjected,
		runtimetrace.EventSkillSkipped,
		runtimetrace.EventSkillError,
		runtimetrace.EventSubAgentTaskPlan,
		runtimetrace.EventSubAgentTaskStarted,
		runtimetrace.EventSubAgentTaskCompleted,
		runtimetrace.EventSubAgentTaskFailed,
		runtimetrace.EventSubAgentTaskBatchCompleted,
		runtimetrace.EventSubAgentTaskSessionCreated,
		runtimetrace.EventSubAgentTaskPrompt,
		runtimetrace.EventSubAgentTaskChildEvent,
		runtimetrace.EventSubAgentTaskSessionDisposed:
		return true
	default:
		return false
	}
}

func runtimeLogEventID(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	data, _ := payload["data"].(map[string]any)
	parts := []any{
		payload["type"],
		payload["event_type"],
		payload["run_id"],
		payload["invocation_id"],
		payload["app_name"],
		payload["user_id"],
		payload["session_id"],
		payload["agent_name"],
		payload["branch"],
		payload["time"],
		data["agent_id"],
		data["agent_name"],
		data["task_id"],
		data["index"],
		data["run_id"],
		data["chapter_no"],
		data["child_session_id"],
		data["child_invocation_id"],
		data["event_id"],
		data["status"],
		data["kind"],
	}
	if ui, ok := data["ui"].(map[string]any); ok {
		parts = append(parts, ui["component"], ui["status"], ui["title"])
	}
	b, err := json.Marshal(parts)
	if err != nil {
		b = []byte(fmt.Sprint(parts...))
	}
	sum := sha256.Sum256(b)
	return "rt_" + hex.EncodeToString(sum[:16])
}

func trimRuntimeLogData(data map[string]any) map[string]any {
	if data == nil {
		return nil
	}
	b, err := json.Marshal(data)
	if err != nil || len(b) <= maxRuntimeLogDataBytes {
		return data
	}
	out := map[string]any{
		"truncated":      true,
		"original_bytes": len(b),
	}
	for _, key := range []string{"message", "error", "error_message", "status", "agent_name", "agent_id", "tool_name", "model", "selected_agent", "skill_id", "artifact_name", "task_id", "chapter_no", "run_id", "ui", "usage", "usage_metadata", "input_chars", "estimated_input_tokens", "estimated_total_tokens", "max_output_tokens", "context_window", "over_budget", "tool_count", "content_count"} {
		if value, ok := data[key]; ok {
			out[key] = value
		}
	}
	return out
}

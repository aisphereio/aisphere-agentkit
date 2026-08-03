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
	"testing"
	"time"

	"google.golang.org/adk/internal/runtimetrace"
)

func TestRuntimeLogSSERecorderAddsEventID(t *testing.T) {
	store := NewMemorySubAgentTaskObserveStore(time.Hour)
	rec := newRuntimeLogSSERecorder(nil, store)

	rec.Record(context.Background(), runtimetrace.Event{
		Type:         runtimetrace.EventSubAgentTaskBatchCompleted,
		Time:         time.Date(2026, 6, 12, 1, 2, 3, 4, time.UTC),
		InvocationID: "inv-1",
		AppName:      "app",
		UserID:       "user",
		SessionID:    "session",
		AgentName:    "manager",
		Data: map[string]any{
			"agent_id": "worker",
			"task_id":  "task_0001",
			"ui": map[string]any{
				"component": "subagent_task_group",
				"status":    "completed",
				"title":     "worker completed 1/1",
			},
		},
	})

	events := store.List(context.Background(), "app", "user", "session")
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1", len(events))
	}
	if got, _ := events[0]["event_id"].(string); got == "" {
		t.Fatalf("event_id is empty: %#v", events[0])
	}
}

func TestMemorySubAgentTaskObserveStoreDeduplicatesEventID(t *testing.T) {
	store := NewMemorySubAgentTaskObserveStore(time.Hour)
	payload := map[string]any{
		"event_id":   "rt_duplicate",
		"type":       runtimetrace.EventRuntimeLog,
		"event_type": runtimetrace.EventSubAgentTaskBatchCompleted,
		"app_name":   "app",
		"user_id":    "user",
		"session_id": "session",
		"data": map[string]any{
			"agent_id": "worker",
			"ui": map[string]any{
				"component": "subagent_task_group",
				"status":    "completed",
			},
		},
	}

	store.Record(payload)
	store.Record(payload)

	events := store.List(context.Background(), "app", "user", "session")
	if len(events) != 1 {
		t.Fatalf("event count = %d, want 1: %#v", len(events), events)
	}
}

func TestRuntimeStoredEventIsError(t *testing.T) {
	tests := []struct {
		name  string
		event map[string]any
		want  bool
	}{
		{
			name: "tool error event type",
			event: map[string]any{
				"event_type": runtimetrace.EventToolError,
				"data":       map[string]any{"name": "session_workspace_read_file", "error": "file not found"},
			},
			want: true,
		},
		{
			name: "subagent failed event type",
			event: map[string]any{
				"event_type": runtimetrace.EventSubAgentTaskFailed,
				"data":       map[string]any{"agent_id": "worker", "error": "missing_input"},
			},
			want: true,
		},
		{
			name: "normal tool result",
			event: map[string]any{
				"event_type": runtimetrace.EventToolResult,
				"data":       map[string]any{"name": "get_book_info"},
			},
			want: false,
		},
		{
			name: "error data on legacy event",
			event: map[string]any{
				"event_type": "tool.result",
				"data":       map[string]any{"error_message": "bad args"},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := runtimeStoredEventIsError(tt.event); got != tt.want {
				t.Fatalf("runtimeStoredEventIsError() = %v, want %v", got, tt.want)
			}
		})
	}
}

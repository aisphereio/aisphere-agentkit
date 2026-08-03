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

package requestuserformtool

import (
	"context"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool/toolconfirmation"
)

type testContext struct {
	context.Context
	confirmation *toolconfirmation.ToolConfirmation
	hint         string
	payload      any
}

func (c *testContext) ToolConfirmation() *toolconfirmation.ToolConfirmation {
	return c.confirmation
}

func (c *testContext) RequestConfirmation(hint string, payload any) error {
	c.hint = hint
	c.payload = payload
	return nil
}

func (c *testContext) Actions() *session.EventActions { return &session.EventActions{} }
func (c *testContext) FunctionCallID() string         { return "test-function-call-id" }
func (c *testContext) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
	return nil, nil
}
func (c *testContext) AgentName() string                    { return "test-agent" }
func (c *testContext) ReadonlyState() session.ReadonlyState { return nil }
func (c *testContext) State() session.State                 { return nil }
func (c *testContext) Artifacts() agent.Artifacts           { return nil }
func (c *testContext) InvocationID() string                 { return "test-invocation-id" }
func (c *testContext) UserContent() *genai.Content          { return nil }
func (c *testContext) AppName() string                      { return "test-app" }
func (c *testContext) Branch() string                       { return "test-branch" }
func (c *testContext) SessionID() string                    { return "test-session-id" }
func (c *testContext) UserID() string                       { return "test-user-id" }

func TestRunRequestsUserForm(t *testing.T) {
	tool := &requestUserFormTool{}

	ctx := &testContext{Context: context.Background()}
	result, err := tool.Run(ctx, map[string]any{
		"title":       "Novel profile",
		"description": "Collect project settings.",
		"response_schema": map[string]any{
			"type": "object",
			"required": []any{
				"genre",
			},
			"properties": map[string]any{
				"genre": map[string]any{
					"type": "string",
					"enum": []any{"urban", "fantasy"},
				},
			},
		},
		"ui_schema": map[string]any{
			"genre": map[string]any{"widget": "select"},
		},
		"initial_values": map[string]any{"genre": "urban"},
		"assist_label":   "AI fill draft",
		"assist_prompt":  "Fill a commercially useful draft from the user's partial choices.",
	})
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}
	if result["status"] != "pending" {
		t.Fatalf("status = %v, want pending", result["status"])
	}
	if ctx.hint != "Novel profile" {
		t.Fatalf("hint = %q, want Novel profile", ctx.hint)
	}
	payload, ok := ctx.payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want map[string]any", ctx.payload)
	}
	if payload["kind"] != "user_form" {
		t.Fatalf("payload kind = %v, want user_form", payload["kind"])
	}
	if payload["response_schema"] == nil {
		t.Fatal("payload response_schema is nil")
	}
	if payload["assist_label"] != "AI fill draft" {
		t.Fatalf("assist_label = %v, want AI fill draft", payload["assist_label"])
	}
	if payload["assist_prompt"] == nil {
		t.Fatal("payload assist_prompt is nil")
	}
}

func TestRunNormalizesMissingSchemaType(t *testing.T) {
	tool := &requestUserFormTool{}

	ctx := &testContext{Context: context.Background()}
	_, err := tool.Run(ctx, map[string]any{
		"title": "Novel profile",
		"response_schema": map[string]any{
			"properties": map[string]any{
				"genre": map[string]any{"type": "string"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}
	payload := ctx.payload.(map[string]any)
	schema := payload["response_schema"].(map[string]any)
	if schema["type"] != "object" {
		t.Fatalf("schema type = %v, want object", schema["type"])
	}
}

func TestRunReturnsSubmittedUserForm(t *testing.T) {
	tool := &requestUserFormTool{}

	ctx := &testContext{
		Context: context.Background(),
		confirmation: &toolconfirmation.ToolConfirmation{
			Confirmed: true,
			Payload: map[string]any{
				"kind": "user_form_response",
				"values": map[string]any{
					"genre": "urban",
				},
			},
		},
	}
	result, err := tool.Run(ctx, map[string]any{})
	if err != nil {
		t.Fatalf("Run() failed: %v", err)
	}
	if result["status"] != "answered" {
		t.Fatalf("status = %v, want answered", result["status"])
	}
	if result["answer"] == nil {
		t.Fatal("answer is nil")
	}
}

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

// Package requestuserformtool provides a built-in Human-in-the-loop tool for
// collecting structured user input with a frontend-rendered JSON schema form.
package requestuserformtool

import (
	"fmt"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/toolinternal/toolutils"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

const requestUserFormInstructions = `
You can ask the user to fill a structured form when several related fields are needed.
Use request_user_form for multi-field intake, configuration, project setup, or domain-specific briefs.
Provide a JSON-schema-like object in response_schema. Prefer concise labels and defaults.
Use enum for single-choice fields, array items with enum for multi-choice fields, and ui_schema widget textarea for long text.
Use assist_label and assist_prompt when the frontend should show an AI-fill button. If the user requests AI assistance, the returned answer kind is user_form_assist_request with partial_values; generate reasonable initial_values and call request_user_form again for confirmation.
After request_user_form returns an answered result, continue the task using the submitted values.
`

type requestUserFormTool struct{}

// New creates a built-in request_user_form tool.
func New() (tool.Tool, error) {
	return &requestUserFormTool{}, nil
}

func (t *requestUserFormTool) Name() string { return "request_user_form" }

func (t *requestUserFormTool) Description() string {
	return "Ask the user to fill a structured form described by a JSON schema, then pause the agent run until the user submits it."
}

func (t *requestUserFormTool) IsLongRunning() bool { return false }

func (t *requestUserFormTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"title": {
					Type:        genai.TypeString,
					Description: "Short user-facing form title.",
				},
				"description": {
					Type:        genai.TypeString,
					Description: "Optional user-facing context explaining why the form is needed.",
				},
				"response_schema": {
					Type:        genai.TypeObject,
					Description: "JSON-schema-like object describing the form result. Use type object with properties, required, enum, items, title, description, and default where useful.",
				},
				"ui_schema": {
					Type:        genai.TypeObject,
					Description: "Optional UI hints keyed by field name. Supported hints include widget such as select, radio, segmented, textarea, checkbox, tags, and placeholder.",
				},
				"initial_values": {
					Type:        genai.TypeObject,
					Description: "Optional initial values keyed by field name.",
				},
				"submit_label": {
					Type:        genai.TypeString,
					Description: "Optional label for the submit button.",
				},
				"assist_label": {
					Type:        genai.TypeString,
					Description: "Optional label for an AI-assist button that asks the agent to fill a draft.",
				},
				"assist_prompt": {
					Type:        genai.TypeString,
					Description: "Optional instructions for how the agent should fill a draft when the user clicks the AI-assist button.",
				},
			},
			Required: []string{"title", "response_schema"},
		},
	}
}

func (t *requestUserFormTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if err := toolutils.PackTool(req, t); err != nil {
		return err
	}
	utils.AppendInstructions(req, requestUserFormInstructions)
	return nil
}

func (t *requestUserFormTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("request_user_form args must be an object")
	}

	if confirmation := ctx.ToolConfirmation(); confirmation != nil {
		if !confirmation.Confirmed {
			return map[string]any{
				"status":  "rejected",
				"message": "User rejected the form request.",
			}, nil
		}
		return map[string]any{
			"status": "answered",
			"answer": confirmation.Payload,
		}, nil
	}

	title, err := requiredString(m, "title")
	if err != nil {
		return nil, err
	}
	responseSchema, err := requiredObject(m, "response_schema")
	if err != nil {
		return nil, err
	}
	schemaType, _ := optionalString(responseSchema, "type")
	if schemaType != "" && !strings.EqualFold(schemaType, "object") {
		return nil, fmt.Errorf("response_schema.type must be object")
	}
	if schemaType == "" {
		responseSchema["type"] = "object"
	}
	if _, err := requiredObject(responseSchema, "properties"); err != nil {
		return nil, fmt.Errorf("response_schema.properties is required and must be an object")
	}

	payload := map[string]any{
		"kind":            "user_form",
		"title":           title,
		"response_schema": responseSchema,
		"original_args":   m,
	}
	if description, ok := optionalString(m, "description"); ok && description != "" {
		payload["description"] = description
	}
	if submitLabel, ok := optionalString(m, "submit_label"); ok && submitLabel != "" {
		payload["submit_label"] = submitLabel
	}
	if assistLabel, ok := optionalString(m, "assist_label"); ok && assistLabel != "" {
		payload["assist_label"] = assistLabel
	}
	if assistPrompt, ok := optionalString(m, "assist_prompt"); ok && assistPrompt != "" {
		payload["assist_prompt"] = assistPrompt
	}
	if uiSchema, ok := optionalObject(m, "ui_schema"); ok {
		payload["ui_schema"] = uiSchema
	}
	if initialValues, ok := optionalObject(m, "initial_values"); ok {
		payload["initial_values"] = initialValues
	}

	if err := ctx.RequestConfirmation(title, payload); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":  "pending",
		"message": "Waiting for user form submission.",
		"title":   title,
	}, nil
}

func requiredString(m map[string]any, key string) (string, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", fmt.Errorf("missing required argument %q", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("argument %q must be a string", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("argument %q cannot be empty", key)
	}
	return s, nil
}

func requiredObject(m map[string]any, key string) (map[string]any, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return nil, fmt.Errorf("missing required argument %q", key)
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("argument %q must be an object", key)
	}
	return obj, nil
}

func optionalString(m map[string]any, key string) (string, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(s), true
}

func optionalObject(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return nil, false
	}
	obj, ok := v.(map[string]any)
	return obj, ok
}

var _ tool.Tool = (*requestUserFormTool)(nil)

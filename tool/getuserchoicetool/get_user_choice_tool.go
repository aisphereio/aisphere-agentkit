// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package getuserchoicetool provides a built-in Human-in-the-loop tool for
// asking the user to choose from options during an agent run.
package getuserchoicetool

import (
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/toolinternal/toolutils"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

const getUserChoiceInstructions = `
You can ask the user to make a choice when important information is missing, ambiguous, or needs explicit confirmation.
Use get_user_choice when you need the user to choose among 2-6 options, confirm a creative direction, or provide a custom option.
Do not silently invent high-impact business or creative decisions when the user should decide.
After get_user_choice returns an answered result, continue the task using the user's selected option.
`

type getUserChoiceTool struct{}

type choiceOption struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
}

// New creates a built-in get_user_choice tool.
func New() (tool.Tool, error) {
	return &getUserChoiceTool{}, nil
}

func (t *getUserChoiceTool) Name() string { return "get_user_choice" }

func (t *getUserChoiceTool) Description() string {
	return "Ask the user to choose among options or provide a custom answer, then pause the agent run until the user responds."
}

func (t *getUserChoiceTool) IsLongRunning() bool { return false }

func (t *getUserChoiceTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"question": {
					Type:        genai.TypeString,
					Description: "The clear question to ask the user.",
				},
				"choices": {
					Type:        genai.TypeArray,
					Description: "A list of 2-6 choices. Each choice should have id, title, and optional description.",
					Items: &genai.Schema{
						Type: genai.TypeObject,
						Properties: map[string]*genai.Schema{
							"id": {
								Type:        genai.TypeString,
								Description: "Stable machine-readable choice id, for example student or worker.",
							},
							"title": {
								Type:        genai.TypeString,
								Description: "Short user-facing choice title.",
							},
							"description": {
								Type:        genai.TypeString,
								Description: "Optional explanation of the choice.",
							},
						},
						Required: []string{"id", "title"},
					},
				},
				"multi_select": {
					Type:        genai.TypeBoolean,
					Description: "Whether the user may select multiple choices. Defaults to false.",
				},
				"allow_custom": {
					Type:        genai.TypeBoolean,
					Description: "Whether the user may provide a custom answer. Defaults to true.",
				},
				"context": {
					Type:        genai.TypeString,
					Description: "Optional brief context explaining why the choice is needed.",
				},
			},
			Required: []string{"question", "choices"},
		},
	}
}

func (t *getUserChoiceTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if err := toolutils.PackTool(req, t); err != nil {
		return err
	}
	utils.AppendInstructions(req, getUserChoiceInstructions)
	return nil
}

func (t *getUserChoiceTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("get_user_choice args must be an object")
	}

	if confirmation := ctx.ToolConfirmation(); confirmation != nil {
		if !confirmation.Confirmed {
			return map[string]any{
				"status":  "rejected",
				"message": "User rejected the choice request.",
			}, nil
		}
		return map[string]any{
			"status": "answered",
			"answer": confirmation.Payload,
		}, nil
	}

	question, err := requiredString(m, "question")
	if err != nil {
		return nil, err
	}
	choices, err := parseChoices(m["choices"])
	if err != nil {
		return nil, err
	}
	if len(choices) < 1 {
		return nil, fmt.Errorf("get_user_choice requires at least one choice")
	}

	multiSelect := optionalBool(m, "multi_select", false)
	allowCustom := optionalBool(m, "allow_custom", true)
	contextText, _ := optionalString(m, "context")

	payload := map[string]any{
		"kind":          "user_choice",
		"question":      question,
		"choices":       choices,
		"multi_select":  multiSelect,
		"allow_custom":  allowCustom,
		"original_args": m,
	}
	if contextText != "" {
		payload["context"] = contextText
	}

	if err := ctx.RequestConfirmation(question, payload); err != nil {
		return nil, err
	}
	return map[string]any{
		"status":   "pending",
		"message":  "Waiting for user choice.",
		"question": question,
	}, nil
}

func parseChoices(raw any) ([]choiceOption, error) {
	if raw == nil {
		return nil, fmt.Errorf("missing required argument %q", "choices")
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal choices: %w", err)
	}
	var choices []choiceOption
	if err := json.Unmarshal(b, &choices); err != nil {
		return nil, fmt.Errorf("choices must be an array of objects with id and title: %w", err)
	}
	for i := range choices {
		choices[i].ID = strings.TrimSpace(choices[i].ID)
		choices[i].Title = strings.TrimSpace(choices[i].Title)
		choices[i].Description = strings.TrimSpace(choices[i].Description)
		if choices[i].ID == "" {
			return nil, fmt.Errorf("choices[%d].id is required", i)
		}
		if choices[i].Title == "" {
			return nil, fmt.Errorf("choices[%d].title is required", i)
		}
	}
	return choices, nil
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

func optionalBool(m map[string]any, key string, fallback bool) bool {
	v, ok := m[key]
	if !ok || v == nil {
		return fallback
	}
	b, ok := v.(bool)
	if !ok {
		return fallback
	}
	return b
}

var _ tool.Tool = (*getUserChoiceTool)(nil)

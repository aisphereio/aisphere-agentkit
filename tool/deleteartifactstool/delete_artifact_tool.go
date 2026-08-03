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

// Package deleteartifactstool defines a tool for deleting artifacts from the
// current app/user/session artifact workspace.
package deleteartifactstool

import (
	"context"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/toolinternal/toolutils"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

type deleteCapableArtifacts interface {
	Delete(ctx context.Context, name string) error
}

// New creates a tool that deletes one artifact from the current workspace.
func New() tool.Tool {
	return &deleteArtifactTool{}
}

type deleteArtifactTool struct{}

func (t *deleteArtifactTool) Name() string { return "delete_artifact" }

func (t *deleteArtifactTool) Description() string {
	return "Deletes a file from the current artifact workspace. Use carefully after confirming the intended file name."
}

func (t *deleteArtifactTool) IsLongRunning() bool { return false }

func (t *deleteArtifactTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"file_name": {
					Type:        genai.TypeString,
					Description: "Flat artifact file name to delete, for example opening_brief.md. Do not include path separators.",
				},
			},
			Required: []string{"file_name"},
		},
	}
}

func (t *deleteArtifactTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if err := toolutils.PackTool(req, t); err != nil {
		return err
	}
	utils.AppendInstructions(req, "Use delete_artifact only when the user explicitly asks to remove an artifact or when a workflow clearly requires deleting a generated intermediate file. Prefer listing artifacts first when the file name is ambiguous.")
	return nil
}

func (t *deleteArtifactTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("unexpected args type, got: %T", args)
	}
	fileName, err := requiredString(m, "file_name")
	if err != nil {
		return nil, err
	}
	if err := validateArtifactName(fileName); err != nil {
		return nil, err
	}
	artifacts, ok := ctx.Artifacts().(deleteCapableArtifacts)
	if !ok {
		return nil, fmt.Errorf("artifact service does not support delete through this context")
	}
	if err := artifacts.Delete(ctx, fileName); err != nil {
		return nil, fmt.Errorf("delete artifact %q: %w", fileName, err)
	}
	return map[string]any{"file_name": fileName, "deleted": true}, nil
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

func validateArtifactName(name string) error {
	if name == "" || name == "." || name == ".." {
		return fmt.Errorf("invalid artifact name %q", name)
	}
	withoutUserPrefix := strings.TrimPrefix(name, "user:")
	if strings.ContainsAny(withoutUserPrefix, `/\\`) || strings.Contains(withoutUserPrefix, "..") {
		return fmt.Errorf("artifact name %q must be a flat file name without path separators", name)
	}
	return nil
}

var _ tool.Tool = (*deleteArtifactTool)(nil)

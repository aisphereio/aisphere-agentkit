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

// Package listartifactstool defines a tool for listing artifacts in the current
// app/user/session artifact workspace.
package listartifactstool

import (
	"fmt"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/artifactfilter"
	"google.golang.org/adk/internal/toolinternal/toolutils"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

// New creates a tool that lists files in the current artifact workspace.
func New() tool.Tool {
	return &listArtifactsTool{}
}

type listArtifactsTool struct{}

func (t *listArtifactsTool) Name() string { return "list_artifacts" }

func (t *listArtifactsTool) Description() string {
	return "Lists files in the current artifact workspace for this app, user, and session."
}

func (t *listArtifactsTool) IsLongRunning() bool { return false }

func (t *listArtifactsTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters:  &genai.Schema{Type: genai.TypeObject},
	}
}

func (t *listArtifactsTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if err := toolutils.PackTool(req, t); err != nil {
		return err
	}
	utils.AppendInstructions(req, "You have an artifact workspace. Use list_artifacts when you need to inspect which files exist before reading, updating, or deleting them.")
	return nil
}

func (t *listArtifactsTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	resp, err := ctx.Artifacts().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list artifacts: %w", err)
	}
	fileNames := []string{}
	if resp != nil && resp.FileNames != nil {
		fileNames = artifactfilter.VisibleFileNames(resp.FileNames)
	}
	return map[string]any{"artifact_names": fileNames, "count": len(fileNames)}, nil
}

var _ tool.Tool = (*listArtifactsTool)(nil)

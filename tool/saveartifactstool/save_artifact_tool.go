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

// Package saveartifactstool provides a builtin tool for writing files into the
// current ADK artifact workspace. It intentionally writes through
// tool.Context.Artifacts instead of the host filesystem directly so files are
// scoped by app/user/session and every write can be recorded in EventActions.
package saveartifactstool

import (
	"encoding/base64"
	"fmt"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/internal/artifactvalidation"
	"google.golang.org/adk/internal/toolinternal/toolutils"
	"google.golang.org/adk/internal/utils"
	"google.golang.org/adk/model"
	"google.golang.org/adk/tool"
)

const saveArtifactInstructions = `
You have access to an artifact workspace for the current app/user/session.
Use save_artifact when the user asks you to save, persist, create, update, or write an intermediate artifact such as a brief, outline, plan, profile, report, markdown file, JSON file, or note.
Use load_artifacts when the user asks you to read or continue from existing artifacts.
Artifact file names must be flat names such as "opening_brief.md" or "character_profile.json". Do not include path separators.
Saving the same file name creates a new version rather than exposing a host filesystem path.
If the file should be user-scoped across sessions, use the "user:" prefix, for example "user:writing_preferences.md".
Certain model-generated protocol artifacts are schema-validated before saving. In book_dissector, chapter_skill_pack_*.json, reconstruction_gap_report_*.json, and cross_chapter_skill_candidates_*.json must be valid JSON matching their required fields.
`

type saveArtifactTool struct{}

// New creates a builtin save_artifact tool.
func New() (tool.Tool, error) {
	return &saveArtifactTool{}, nil
}

func (t *saveArtifactTool) Name() string { return "save_artifact" }

func (t *saveArtifactTool) Description() string {
	return "Save text or base64 content into the current ADK artifact workspace."
}

func (t *saveArtifactTool) IsLongRunning() bool { return false }

func (t *saveArtifactTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: "OBJECT",
			Properties: map[string]*genai.Schema{
				"file_name": {
					Type:        "STRING",
					Description: "Flat artifact name to save, for example opening_brief.md. Do not include path separators. Prefix with user: for user-scoped artifacts.",
				},
				"content": {
					Type:        "STRING",
					Description: "Text content to save. Use this for markdown, JSON, YAML, CSV, or plain text artifacts.",
				},
				"content_base64": {
					Type:        "STRING",
					Description: "Optional base64-encoded binary content. Use either content or content_base64, not both.",
				},
				"mime_type": {
					Type:        "STRING",
					Description: "Optional MIME type. If omitted, it is inferred from the file extension.",
				},
			},
			Required: []string{"file_name"},
		},
	}
}

func (t *saveArtifactTool) ProcessRequest(ctx tool.Context, req *model.LLMRequest) error {
	if err := toolutils.PackTool(req, t); err != nil {
		return err
	}
	utils.AppendInstructions(req, saveArtifactInstructions)
	return nil
}

func (t *saveArtifactTool) Run(ctx tool.Context, args any) (map[string]any, error) {
	m, ok := args.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("save_artifact args must be an object")
	}

	fileName, err := requiredString(m, "file_name")
	if err != nil {
		return nil, err
	}
	if err := validateArtifactName(fileName); err != nil {
		return nil, err
	}

	content, hasContent, err := optionalString(m, "content")
	if err != nil {
		return nil, err
	}
	contentBase64, hasBase64, err := optionalString(m, "content_base64")
	if err != nil {
		return nil, err
	}
	if hasContent && hasBase64 {
		return nil, fmt.Errorf("save_artifact accepts either content or content_base64, not both")
	}
	if !hasContent && !hasBase64 {
		return nil, fmt.Errorf("save_artifact requires content or content_base64")
	}

	mimeType, hasMimeType, err := optionalString(m, "mime_type")
	if err != nil {
		return nil, err
	}
	if !hasMimeType || mimeType == "" {
		mimeType = inferMimeType(fileName)
	}

	var data []byte
	if hasBase64 {
		data, err = base64.StdEncoding.DecodeString(contentBase64)
		if err != nil {
			return nil, fmt.Errorf("invalid content_base64: %w", err)
		}
	} else {
		data = []byte(content)
	}

	validation := artifactvalidation.Validate(fileName, mimeType, data)
	if validation.Enforced && !validation.Valid {
		return nil, fmt.Errorf("artifact %q failed %s validation: %s", fileName, validation.SchemaID, strings.Join(validation.Errors, "; "))
	}

	part := &genai.Part{InlineData: &genai.Blob{MIMEType: mimeType, Data: data}}
	saveResp, err := ctx.Artifacts().Save(ctx, fileName, part)
	if err != nil {
		return nil, fmt.Errorf("save artifact %q: %w", fileName, err)
	}
	var version int64
	if saveResp != nil {
		version = saveResp.Version
	}

	result := map[string]any{
		"file_name": fileName,
		"version":   version,
		"mime_type": mimeType,
		"bytes":     len(data),
	}
	if validation.Enforced {
		result["validation"] = validation
	}
	return result, nil
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

func optionalString(m map[string]any, key string) (string, bool, error) {
	v, ok := m[key]
	if !ok || v == nil {
		return "", false, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", true, fmt.Errorf("argument %q must be a string", key)
	}
	return strings.TrimSpace(s), true, nil
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

func inferMimeType(fileName string) string {
	lower := strings.ToLower(fileName)
	switch {
	case strings.HasSuffix(lower, ".md"), strings.HasSuffix(lower, ".markdown"):
		return "text/markdown"
	case strings.HasSuffix(lower, ".json"):
		return "application/json"
	case strings.HasSuffix(lower, ".yaml"), strings.HasSuffix(lower, ".yml"):
		return "application/yaml"
	case strings.HasSuffix(lower, ".html"), strings.HasSuffix(lower, ".htm"):
		return "text/html"
	case strings.HasSuffix(lower, ".csv"):
		return "text/csv"
	case strings.HasSuffix(lower, ".txt"):
		return "text/plain"
	default:
		return "text/plain"
	}
}

var _ tool.Tool = (*saveArtifactTool)(nil)

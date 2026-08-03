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

// Package uploadstool exposes safe platform upload management tools to agents.
// Agents can inspect upload metadata, preview bounded text snippets, and attach
// an upload to the current artifact workspace without ever receiving the full
// file in the user prompt.
package uploadstool

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/platform/store"
	"google.golang.org/adk/internal/platform/uploads"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

const defaultAttachMaxBytes int64 = 128 << 20 // 128 MiB

// NewToolset creates upload management tools.
func NewToolset() (tool.Toolset, error) {
	ts := &Toolset{}
	builders := []func() (tool.Tool, error){
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "upload_list",
				Description: "List platform uploads for the current user/session. Returns metadata and handling policy only; it never returns full file content.",
			}, ts.List)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "upload_get",
				Description: "Get platform upload metadata and handling policy by upload_id. Use this before deciding whether to preview, preprocess, or attach.",
			}, ts.Get)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "upload_preview",
				Description: "Read a small bounded preview of a text-like upload. Never use this as full file content; use registered preprocessing tools for large files.",
			}, ts.Preview)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "upload_attach_artifact",
				Description: "Attach a platform upload to the current app/user/session artifact workspace so artifact-based tools can process it. Does not expose raw content to the model.",
			}, ts.AttachArtifact)
		},
	}
	for _, build := range builders {
		t, err := build()
		if err != nil {
			return nil, err
		}
		ts.tools = append(ts.tools, t)
	}
	return ts, nil
}

// Toolset groups upload management tools.
type Toolset struct {
	tools []tool.Tool
}

func (t *Toolset) Name() string { return "UploadToolset" }

func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	return t.tools, nil
}

type ListArgs struct {
	TenantID     string `json:"tenant_id,omitempty" jsonschema:"Optional tenant id. Defaults to default."`
	UserID       string `json:"user_id,omitempty" jsonschema:"Optional user id. Defaults to the current user."`
	AppName      string `json:"app_name,omitempty" jsonschema:"Optional app name. Defaults to the current app."`
	SessionID    string `json:"session_id,omitempty" jsonschema:"Optional session id. Defaults to the current session."`
	Purpose      string `json:"purpose,omitempty" jsonschema:"Optional purpose filter such as book_source."`
	Status       string `json:"status,omitempty" jsonschema:"Optional status filter. Defaults to active. Use all to include deleted rows."`
	HandlingMode string `json:"handling_mode,omitempty" jsonschema:"Optional handling mode filter such as preprocess_required."`
	Limit        int    `json:"limit,omitempty" jsonschema:"Maximum rows to return. Defaults to 100, max 500."`
}

type UploadSummary struct {
	ID             string `json:"id"`
	OriginalName   string `json:"original_name"`
	MIMEType       string `json:"mime_type,omitempty"`
	SizeBytes      int64  `json:"size_bytes"`
	SHA256         string `json:"sha256,omitempty"`
	Status         string `json:"status"`
	Purpose        string `json:"purpose,omitempty"`
	HandlingMode   string `json:"handling_mode,omitempty"`
	InlineEligible bool   `json:"inline_eligible"`
	Previewable    bool   `json:"previewable"`
	PolicyReason   string `json:"policy_reason,omitempty"`
}

type ListResult struct {
	Uploads []UploadSummary `json:"uploads"`
}

type GetArgs struct {
	TenantID string `json:"tenant_id,omitempty" jsonschema:"Optional tenant id. Defaults to default."`
	UploadID string `json:"upload_id" jsonschema:"Platform upload id."`
}

type PreviewArgs struct {
	TenantID string `json:"tenant_id,omitempty" jsonschema:"Optional tenant id. Defaults to default."`
	UploadID string `json:"upload_id" jsonschema:"Platform upload id."`
	MaxBytes int64  `json:"max_bytes,omitempty" jsonschema:"Maximum preview bytes. Defaults to 64KiB and is capped by the service."`
}

type AttachArtifactArgs struct {
	TenantID     string `json:"tenant_id,omitempty" jsonschema:"Optional tenant id. Defaults to default."`
	UploadID     string `json:"upload_id" jsonschema:"Platform upload id."`
	ArtifactName string `json:"artifact_name,omitempty" jsonschema:"Flat artifact name. Defaults to the upload original name."`
	MaxBytes     int64  `json:"max_bytes,omitempty" jsonschema:"Maximum bytes allowed for attaching. Defaults to 128MiB."`
}

type AttachArtifactResult struct {
	UploadID      string `json:"upload_id"`
	ArtifactName  string `json:"artifact_name"`
	Version       int64  `json:"version"`
	MIMEType      string `json:"mime_type"`
	Bytes         int    `json:"bytes"`
	HandlingMode  string `json:"handling_mode"`
	NextToolHint  string `json:"next_tool_hint,omitempty"`
	SafetyMessage string `json:"safety_message"`
}

func (t *Toolset) List(ctx tool.Context, args ListArgs) (*ListResult, error) {
	svc, err := serviceFromContext(ctx)
	if err != nil {
		return nil, err
	}
	items, err := svc.List(ctx, uploads.ListFilter{
		TenantID:     tenant(args.TenantID),
		UserID:       firstNonEmpty(args.UserID, ctx.UserID()),
		ProjectID:    projectIDFromState(ctx),
		AppName:      firstNonEmpty(args.AppName, ctx.AppName()),
		SessionID:    sessionFilter(ctx, args.SessionID),
		Purpose:      args.Purpose,
		Status:       args.Status,
		HandlingMode: args.HandlingMode,
		Limit:        args.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]UploadSummary, 0, len(items))
	for i := range items {
		out = append(out, summarizeUpload(&items[i]))
	}
	return &ListResult{Uploads: out}, nil
}

func (t *Toolset) Get(ctx tool.Context, args GetArgs) (*UploadSummary, error) {
	svc, err := serviceFromContext(ctx)
	if err != nil {
		return nil, err
	}
	upload, err := svc.Get(ctx, tenant(args.TenantID), strings.TrimSpace(args.UploadID))
	if err != nil {
		return nil, err
	}
	if projectID := projectIDFromState(ctx); projectID != "" && strings.TrimSpace(upload.ProjectID) != projectID {
		return nil, fmt.Errorf("upload %s does not belong to the current workspace; select the matching project in the top project selector", upload.ID)
	}
	summary := summarizeUpload(upload)
	return &summary, nil
}

func (t *Toolset) Preview(ctx tool.Context, args PreviewArgs) (*uploads.PreviewResult, error) {
	svc, err := serviceFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if projectID := projectIDFromState(ctx); projectID != "" {
		upload, err := svc.Get(ctx, tenant(args.TenantID), strings.TrimSpace(args.UploadID))
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(upload.ProjectID) != projectID {
			return nil, fmt.Errorf("upload %s does not belong to the current workspace; select the matching project in the top project selector", upload.ID)
		}
	}
	return svc.Preview(ctx, tenant(args.TenantID), strings.TrimSpace(args.UploadID), args.MaxBytes)
}

func (t *Toolset) AttachArtifact(ctx tool.Context, args AttachArtifactArgs) (*AttachArtifactResult, error) {
	svc, err := serviceFromContext(ctx)
	if err != nil {
		return nil, err
	}
	reader, upload, err := svc.Open(ctx, tenant(args.TenantID), strings.TrimSpace(args.UploadID))
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if projectID := projectIDFromState(ctx); projectID != "" && strings.TrimSpace(upload.ProjectID) != projectID {
		return nil, fmt.Errorf("upload %s does not belong to the current workspace; select the matching project in the top project selector", upload.ID)
	}
	maxBytes := args.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultAttachMaxBytes
	}
	limited := &io.LimitedReader{R: reader, N: maxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read upload: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("upload %s is too large to attach in one step: %d bytes exceeds max_bytes=%d; use a streaming/processor script instead", upload.ID, len(data), maxBytes)
	}
	artifactName := args.ArtifactName
	if artifactName == "" {
		artifactName = upload.OriginalName
	}
	artifactName = filepath.Base(artifactName)
	if artifactName == "" || artifactName == "." || artifactName == ".." || strings.ContainsAny(artifactName, `/\\`) {
		return nil, fmt.Errorf("invalid artifact_name %q", args.ArtifactName)
	}
	part := &genai.Part{InlineData: &genai.Blob{MIMEType: upload.MIMEType, Data: data}}
	resp, err := ctx.Artifacts().Save(ctx, artifactName, part)
	if err != nil {
		return nil, fmt.Errorf("save upload as artifact: %w", err)
	}
	version := int64(0)
	if resp != nil {
		version = resp.Version
	}
	return &AttachArtifactResult{
		UploadID:      upload.ID,
		ArtifactName:  artifactName,
		Version:       version,
		MIMEType:      upload.MIMEType,
		Bytes:         len(data),
		HandlingMode:  upload.HandlingMode,
		NextToolHint:  nextToolHint(upload),
		SafetyMessage: "upload was saved to artifact workspace; raw content was not injected into model context",
	}, nil
}

func serviceFromContext(ctx tool.Context) (uploads.Service, error) {
	cfg := runtimeconfig.FromContext(ctx)
	if cfg == nil {
		return nil, fmt.Errorf("runtime config is not available")
	}
	db, err := store.OpenGORM(cfg.Storage.Database)
	if err != nil {
		return nil, fmt.Errorf("open platform database: %w", err)
	}
	if cfg.Storage.Database.AutoMigrate {
		if err := uploads.AutoMigrate(db); err != nil {
			return nil, fmt.Errorf("migrate platform uploads: %w", err)
		}
	}
	return uploads.NewService(db, cfg.Storage.Upload.Root), nil
}

func tenant(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func projectIDFromState(ctx tool.Context) string {
	state := ctx.State()
	if state == nil {
		return ""
	}
	for _, key := range []string{"project_id", "projectId"} {
		value, err := state.Get(key)
		if err == nil {
			if projectID, ok := value.(string); ok && strings.TrimSpace(projectID) != "" {
				return strings.TrimSpace(projectID)
			}
		}
	}
	return ""
}

func sessionFilter(ctx tool.Context, explicitSessionID string) string {
	if strings.TrimSpace(explicitSessionID) != "" {
		return strings.TrimSpace(explicitSessionID)
	}
	if projectIDFromState(ctx) != "" {
		return ""
	}
	return ctx.SessionID()
}

func summarizeUpload(upload *uploads.Upload) UploadSummary {
	if upload == nil {
		return UploadSummary{}
	}
	return UploadSummary{
		ID:             upload.ID,
		OriginalName:   upload.OriginalName,
		MIMEType:       upload.MIMEType,
		SizeBytes:      upload.SizeBytes,
		SHA256:         upload.SHA256,
		Status:         upload.Status,
		Purpose:        upload.Purpose,
		HandlingMode:   upload.HandlingMode,
		InlineEligible: upload.InlineEligible,
		Previewable:    upload.Previewable,
		PolicyReason:   upload.PolicyReason,
	}
}

func nextToolHint(upload *uploads.Upload) string {
	if upload == nil {
		return ""
	}
	if upload.Purpose == "book_source" || strings.EqualFold(filepath.Ext(upload.OriginalName), ".txt") {
		return "For book sources, call book_split_from_artifact with source_artifact=" + upload.OriginalName + " or the artifact_name returned here."
	}
	return "Use artifact tools or a registered processor for further handling."
}

var _ tool.Toolset = (*Toolset)(nil)

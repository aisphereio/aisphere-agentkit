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

package runtimeconfig

import (
	"fmt"
	"os"
	"sort"
)

// FrontendModel is a sanitized model option returned to WebUI/Builder. It must
// never include API keys, authorization headers, or other secrets.
type FrontendModel struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Spec          string `json:"spec"`
	Provider      string `json:"provider"`
	Model         string `json:"model"`
	ContextWindow int64  `json:"contextWindow,omitempty"`
	IsDefault     bool   `json:"isDefault,omitempty"`
	Kind          string `json:"kind"`
}

// FrontendTool describes one tool option in the Builder. Args is used by the
// current embedded WebUI to render argument fields for builtin tools.
type FrontendTool struct {
	Name            string   `json:"name"`
	Title           string   `json:"title"`
	Category        string   `json:"category"`
	Provider        string   `json:"provider"`
	Description     string   `json:"description"`
	Available       bool     `json:"available"`
	GoogleDependent bool     `json:"googleDependent"`
	Reason          string   `json:"reason,omitempty"`
	Args            []string `json:"args,omitempty"`
	Icon            string   `json:"icon,omitempty"`
}

// FrontendMCPServer is the sanitized server registry sent to the Builder UI.
// It intentionally omits header values and other secrets.
type FrontendMCPServer struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description,omitempty"`
	Transport      string   `json:"transport"`
	Endpoint       string   `json:"endpoint"`
	Namespace      string   `json:"namespace"`
	Enabled        bool     `json:"enabled"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
	HeaderKeys     []string `json:"headerKeys,omitempty"`
	ToolFilter     []string `json:"toolFilter,omitempty"`
}

// FrontendUploadConfig describes upload policies and endpoints for the frontend.
type FrontendUploadConfig struct {
	Enabled              bool     `json:"enabled"`
	UploadEndpoint       string   `json:"uploadEndpoint"`
	ListEndpoint         string   `json:"listEndpoint"`
	PreviewEndpoint      string   `json:"previewEndpoint"`
	ContentEndpoint      string   `json:"contentEndpoint"`
	AttachEndpoint       string   `json:"attachEndpoint"`
	RejectLargeInline    bool     `json:"rejectLargeInline"`
	MaxInlineTextChars   int      `json:"maxInlineTextChars"`
	MaxInlineDataBytes   int64    `json:"maxInlineDataBytes"`
	HandlingModes        []string `json:"handlingModes"`
	DefaultPurpose       string   `json:"defaultPurpose"`
	ReferenceMessageHint string   `json:"referenceMessageHint"`
}

// FrontendBuilderDefaults contains builder defaults that used to be hardcoded
// in the WebUI bundle.
type FrontendBuilderDefaults struct {
	DefaultModel      string   `json:"defaultModel"`
	DefaultAgentClass string   `json:"defaultAgentClass"`
	AgentClasses      []string `json:"agentClasses"`
	RecommendedTools  []string `json:"recommendedTools"`
}

// FrontendUploadConfig returns the upload-center contract for WebUI/Admin.
func (c *Config) FrontendUploadConfig() FrontendUploadConfig {
	policy := InputPolicyConfig{RejectLargeInline: true, MaxInlineTextChars: 64000, MaxInlineDataBytes: 256 << 10}
	if c != nil {
		policy = c.Runtime.InputPolicy
	}
	return FrontendUploadConfig{
		Enabled:              true,
		UploadEndpoint:       "/platform/uploads",
		ListEndpoint:         "/platform/uploads",
		PreviewEndpoint:      "/platform/uploads/{upload_id}/preview",
		ContentEndpoint:      "/platform/uploads/{upload_id}/content",
		AttachEndpoint:       "/platform/uploads/{upload_id}/attach-artifact",
		RejectLargeInline:    policy.RejectLargeInline,
		MaxInlineTextChars:   policy.MaxInlineTextChars,
		MaxInlineDataBytes:   policy.MaxInlineDataBytes,
		HandlingModes:        []string{"reference_only", "inline_small_text", "preprocess_required", "retrieval_index", "tool_workspace", "artifact_ready", "blocked"},
		DefaultPurpose:       "general",
		ReferenceMessageHint: "Send upload_id and metadata to the agent; never send raw file content by default.",
	}
}

// FrontendMCPServers returns configured MCP servers without exposing secret
// header values. The UI uses this list to let an Agent bind MCP tools.
func (c *Config) FrontendMCPServers() []FrontendMCPServer {
	if c == nil || len(c.MCP.Servers) == 0 {
		return nil
	}
	ids := make([]string, 0, len(c.MCP.Servers))
	for id := range c.MCP.Servers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]FrontendMCPServer, 0, len(ids))
	for _, id := range ids {
		srv := c.MCP.Servers[id]
		headerKeys := make([]string, 0, len(srv.Headers))
		for k := range srv.Headers {
			headerKeys = append(headerKeys, k)
		}
		sort.Strings(headerKeys)
		out = append(out, FrontendMCPServer{
			ID:             id,
			Name:           firstNonEmpty(srv.Name, id),
			Description:    srv.Description,
			Transport:      firstNonEmpty(srv.Transport, "streamable_http"),
			Endpoint:       os.ExpandEnv(srv.Endpoint),
			Namespace:      firstNonEmpty(srv.Namespace, id),
			Enabled:        srv.Enabled || srv.Endpoint != "",
			TimeoutSeconds: srv.TimeoutSeconds,
			HeaderKeys:     headerKeys,
			ToolFilter:     append([]string(nil), srv.ToolFilter...),
		})
	}
	return out
}

// FrontendModels returns all model references that the frontend may save in
// root_agent.yaml. It includes aliases and direct spec IDs, but only exposes
// sanitized provider/model metadata.
func (c *Config) FrontendModels() []FrontendModel {
	if c == nil {
		return nil
	}
	seen := map[string]bool{}
	out := []FrontendModel{}
	add := func(id, kind string) {
		if id == "" || seen[id] {
			return
		}
		specID, spec, ok := c.ResolveModelSpec(id)
		if !ok {
			return
		}
		provider := spec.Provider
		if provider == "" {
			provider = "openai"
		}
		modelName := spec.Model
		if modelName == "" {
			modelName = specID
		}
		label := id
		if id != specID {
			label = fmt.Sprintf("%s → %s / %s", id, specID, modelName)
		} else {
			label = fmt.Sprintf("%s / %s", id, modelName)
		}
		seen[id] = true
		out = append(out, FrontendModel{
			ID:            id,
			Label:         label,
			Spec:          specID,
			Provider:      provider,
			Model:         modelName,
			ContextWindow: firstInt64(spec.ContextWindow, c.Runtime.ContextWindow),
			IsDefault:     id == c.Models.Default,
			Kind:          kind,
		})
	}

	add(c.Models.Default, "default")

	aliasKeys := make([]string, 0, len(c.Models.Aliases))
	for k := range c.Models.Aliases {
		aliasKeys = append(aliasKeys, k)
	}
	sort.Strings(aliasKeys)
	for _, k := range aliasKeys {
		add(k, "alias")
	}

	specKeys := make([]string, 0, len(c.Models.Specs))
	for k := range c.Models.Specs {
		specKeys = append(specKeys, k)
	}
	sort.Strings(specKeys)
	for _, k := range specKeys {
		add(k, "spec")
	}

	return out
}

// FrontendBuilderDefaults returns defaults for creating a new agent/app.
func (c *Config) FrontendBuilderDefaults() FrontendBuilderDefaults {
	defaultModel := "default"
	if c != nil {
		defaultModel = firstNonEmpty(c.Builder.DefaultModel, c.Models.Default, "default")
	}
	return FrontendBuilderDefaults{
		DefaultModel:      defaultModel,
		DefaultAgentClass: "LlmAgent",
		AgentClasses:      []string{"LlmAgent", "LoopAgent", "ParallelAgent", "SequentialAgent"},
		RecommendedTools:  []string{"get_user_choice", "request_user_form", "save_artifact", "list_artifacts", "load_artifacts", "files_retrieval", "EnvToolset", "UploadToolset", "ProjectArtifactToolset", "BookPreprocessorToolset", "NovelStoreToolset", "BookSkillRunToolset", "PlanRunToolset", "SkillAuthoringToolset"},
	}
}

// FrontendTools returns the tool catalog for the current runtime. Keep Google
// and Vertex-native tools in the catalog as unavailable so the frontend can
// render them disabled instead of silently offering broken options.
func (c *Config) FrontendTools() []FrontendTool {
	return []FrontendTool{
		{
			Name:        "save_artifact",
			Title:       "save_artifact",
			Category:    "Artifact Tools",
			Provider:    "core",
			Available:   true,
			Description: "Save text or base64 content into the current artifact workspace.",
			Icon:        "save",
		},
		{
			Name:        "list_artifacts",
			Title:       "list_artifacts",
			Category:    "Artifact Tools",
			Provider:    "core",
			Available:   true,
			Description: "List files in the current artifact workspace.",
			Icon:        "folder_open",
		},
		{
			Name:        "load_artifacts",
			Title:       "load_artifacts",
			Category:    "Artifact Tools",
			Provider:    "core",
			Available:   true,
			Description: "Load existing artifacts from the current app/user/session workspace.",
			Icon:        "image",
		},
		{
			Name:        "delete_artifact",
			Title:       "delete_artifact",
			Category:    "Artifact Tools",
			Provider:    "core",
			Available:   true,
			Description: "Delete a file from the current artifact workspace.",
			Icon:        "delete",
		},
		{
			Name:        "files_retrieval",
			Title:       "files_retrieval",
			Category:    "Artifact Tools",
			Provider:    "core",
			Available:   true,
			Description: "Search text-like artifacts and return relevant snippets. Local-first replacement for FilesRetrieval.",
			Icon:        "manage_search",
		},
		{
			Name:        "UploadToolset",
			Title:       "UploadToolset",
			Category:    "Upload Tools",
			Provider:    "core",
			Available:   true,
			Description: "Inspect platform uploads, read bounded previews, and attach uploads to the current artifact workspace without injecting raw files into model context.",
			Icon:        "upload_file",
		},
		{
			Name:        "ProjectArtifactToolset",
			Title:       "ProjectArtifactToolset",
			Category:    "Project Tools",
			Provider:    "core",
			Available:   true,
			Description: "Create/mount project workspaces and register durable artifacts with visibility/default-mount metadata for cross-session agent handoff.",
			Icon:        "workspaces",
		},
		{
			Name:        "NovelStoreToolset",
			Title:       "NovelStoreToolset",
			Category:    "Book Tools",
			Provider:    "core",
			Available:   true,
			Description: "Project-scoped novel source/split/chapter lifecycle tools backed by ObjectStore/MinIO plus database metadata.",
			Icon:        "library_books",
		},
		{
			Name:        "BookPreprocessorToolset",
			Title:       "BookPreprocessorToolset",
			Category:    "Book Tools",
			Provider:    "core",
			Available:   true,
			Description: "Deterministic book ingestion processor with preview/commit chapter splitting, encoding detection, manifest generation, chapter loading, resplitting, and manual boundary correction.",
			Icon:        "menu_book",
		},
		{
			Name:        "BookSkillRunToolset",
			Title:       "BookSkillRunToolset",
			Category:    "Book Tools",
			Provider:    "core",
			Available:   true,
			Description: "Manage durable Book-to-Skill long-run state: plan chapter batches, resume from new sessions, track skill versions, and record per-batch artifacts.",
			Icon:        "route",
		},
		{
			Name:        "PlanRunToolset",
			Title:       "PlanRunToolset",
			Category:    "Planning Tools",
			Provider:    "core",
			Available:   true,
			Description: "Manage bounded automatic plan loops with durable state, iteration checkpoints, pause/resume, and project artifact registration.",
			Icon:        "account_tree",
		},
		{
			Name:        "SkillAuthoringToolset",
			Title:       "SkillAuthoringToolset",
			Category:    "Skill Tools",
			Provider:    "core",
			Available:   true,
			Description: "Validate and save model-generated writing-method drafts as real filesystem-backed ADK Skills for admin review and publishing.",
			Icon:        "psychology",
		},
		{
			Name:        "load_memory",
			Title:       "load_memory",
			Category:    "Context Tools",
			Provider:    "core",
			Available:   true,
			Description: "Let the model search user/app memory when it decides memory is needed.",
			Icon:        "memory",
		},
		{
			Name:        "preload_memory",
			Title:       "preload_memory",
			Category:    "Context Tools",
			Provider:    "core",
			Available:   true,
			Description: "Automatically search memory before each model request and inject relevant results.",
			Icon:        "memory",
		},
		{
			Name:        "get_user_choice",
			Title:       "get_user_choice",
			Category:    "Agent Function Tools",
			Provider:    "core",
			Available:   true,
			Description: "Ask the user to choose among options or provide a custom answer, then pause the agent run until the user responds.",
			Icon:        "how_to_reg",
		},
		{
			Name:        "request_user_form",
			Title:       "request_user_form",
			Category:    "Agent Function Tools",
			Provider:    "core",
			Available:   true,
			Description: "Ask the user to fill a structured form described by a JSON schema, then pause the agent run until the user submits it.",
			Icon:        "dynamic_form",
		},
		{
			Name:        "exit_loop",
			Title:       "exit_loop",
			Category:    "Agent Function Tools",
			Provider:    "core",
			Available:   true,
			Description: "Exit the current LoopAgent when the objective has been met.",
			Icon:        "sync",
		},
		{
			Name:        "EnvToolset",
			Title:       "EnvToolset",
			Category:    "Environment Tools",
			Provider:    "core",
			Available:   true,
			Description: "Guarded environment management toolset for Linux, Docker, Kubernetes, Go, and Python. Exposes standard operations, command risk analysis, and approval-gated guarded shell.",
			Args:        []string{"config_path", "default_safety_mode", "default_freedom_level", "default_max_output_bytes", "default_timeout_seconds", "allow_local", "dry_run_default"},
			Icon:        "terminal",
		},
		{
			Name:            "google_search",
			Title:           "google_search",
			Category:        "Search Tools",
			Provider:        "google",
			Available:       false,
			GoogleDependent: true,
			Reason:          "Google/Gemini native search is not enabled in OpenAI-compatible mode.",
			Description:     "Google native search tool.",
			Icon:            "search",
		},
		{
			Name:            "url_context",
			Title:           "url_context",
			Category:        "Context Tools",
			Provider:        "google",
			Available:       false,
			GoogleDependent: true,
			Reason:          "Current implementation is Gemini native URL context; implement a local HTTP fetcher before enabling for OpenAI-compatible models.",
			Description:     "Gemini native URL context tool.",
			Icon:            "link",
		},
		{
			Name:            "VertexAiRagRetrieval",
			Title:           "VertexAiRagRetrieval",
			Category:        "Context Tools",
			Provider:        "google",
			Available:       false,
			GoogleDependent: true,
			Reason:          "Vertex AI RAG is not configured.",
			Description:     "Vertex AI RAG retrieval tool.",
			Icon:            "find_in_page",
		},
		{
			Name:            "VertexAiSearchTool",
			Title:           "VertexAiSearchTool",
			Category:        "Search Tools",
			Provider:        "google",
			Available:       false,
			GoogleDependent: true,
			Reason:          "Vertex AI Search is not configured.",
			Description:     "Vertex AI Search tool.",
			Icon:            "search",
		},
		{
			Name:            "EnterpriseWebSearchTool",
			Title:           "EnterpriseWebSearchTool",
			Category:        "Search Tools",
			Provider:        "google",
			Available:       false,
			GoogleDependent: true,
			Reason:          "Enterprise web search provider is not configured.",
			Description:     "Enterprise web search tool.",
			Icon:            "web",
		},
		{
			Name:        "FilesRetrieval",
			Title:       "FilesRetrieval",
			Category:    "Context Tools",
			Provider:    "core",
			Available:   false,
			Reason:      "Local file retrieval/RAG tool has not been implemented yet. Use load_artifacts for exact artifact reads.",
			Description: "Retrieve relevant chunks from uploaded files. Planned local implementation.",
			Icon:        "find_in_page",
		},
		{
			Name:        "LongRunningFunctionTool",
			Title:       "LongRunningFunctionTool",
			Category:    "Agent Function Tools",
			Provider:    "core",
			Available:   false,
			Reason:      "Requires a concrete registered function name in args.func.",
			Description: "Wrap a concrete long-running function tool.",
			Args:        []string{"func"},
			Icon:        "data_object",
		},
	}
}

func firstInt64(values ...int64) int64 {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

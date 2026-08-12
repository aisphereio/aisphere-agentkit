// Copyright 2025 Google LLC
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

// Package aihubruntime synchronizes runtime-local skills from AIHub's
// permission-aware Catalog API. AIHub is the single source of truth; Runtime
// keeps immutable, checksum-verified execution caches and mounts a locked
// SkillSnapshot into each session/sandbox.
package aihubruntime

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/internal/skillservice"
)

type Client struct {
	cfg        runtimeconfig.AIHubSkillConfig
	httpClient *http.Client
}

type (
	cookieHeaderContextKey   struct{}
	requestHeadersContextKey struct{}
)

var forwardedPrincipalHeaders = []string{
	// Hub production authn verifies this signed principal token. Runtime must
	// carry it across the single control-plane resolve call so the Hub sees
	// the same user that initiated the run in the browser.
	"X-Aisphere-Principal-JWT",
	"X-Aisphere-Auth-Verified",
	"X-Aisphere-Subject",
	"X-Aisphere-Subject-Type",
	"X-Aisphere-Org-ID",
	"X-Aisphere-Project-ID",
	"X-Aisphere-Username",
	"X-Aisphere-Name",
}

// WithCookieHeader attaches the authenticated browser cookie for a single
// backend-to-Hub call. The value is never persisted in cache or snapshots.
func WithCookieHeader(ctx context.Context, cookieHeader string) context.Context {
	if strings.ContainsAny(cookieHeader, "\r\n") {
		cookieHeader = ""
	}
	return context.WithValue(ctx, cookieHeaderContextKey{}, strings.TrimSpace(cookieHeader))
}

func cookieHeaderFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(cookieHeaderContextKey{}).(string)
	return value
}

// WithRequestHeaders carries gateway-authenticated identity headers through
// the Runtime request context for the single Hub call that resolves a
// session. The values are never persisted in session state or snapshots.
func WithRequestHeaders(ctx context.Context, headers http.Header) context.Context {
	forwarded := make(map[string]string, len(forwardedPrincipalHeaders))
	for _, name := range forwardedPrincipalHeaders {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			forwarded[name] = value
		}
	}
	return context.WithValue(ctx, requestHeadersContextKey{}, forwarded)
}

func requestHeadersFromContext(ctx context.Context) map[string]string {
	if ctx == nil {
		return nil
	}
	values, _ := ctx.Value(requestHeadersContextKey{}).(map[string]string)
	return values
}

type SyncResult struct {
	SkillSet     string   `json:"skillSet,omitempty"`
	Revision     string   `json:"revision,omitempty"`
	SnapshotID   string   `json:"snapshotId,omitempty"`
	Discovered   int      `json:"discovered"`
	Downloaded   int      `json:"downloaded"`
	Activated    []string `json:"activated,omitempty"`
	Imported     []string `json:"imported"` // kept for old logs
	Skipped      []string `json:"skipped,omitempty"`
	Pruned       []string `json:"pruned,omitempty"`
	CacheRoot    string   `json:"cacheRoot,omitempty"`
	ActivePolicy string   `json:"activePolicy,omitempty"`
}

type skillSetManifestResponse struct {
	SkillSet catalogSkillSetSnapshot `json:"skillset"`
	Revision string                  `json:"revision,omitempty"`
	ETag     string                  `json:"etag,omitempty"`
	Total    int                     `json:"total"`
}

type catalogSkillSetSnapshot struct {
	Name        string     `json:"name"`
	Object      string     `json:"object"`
	Revision    string     `json:"revision"`
	ETag        string     `json:"etag"`
	GeneratedAt string     `json:"generatedAt"`
	Members     []skillRef `json:"members"`
}

type skillsResponse struct {
	Items []catalogSkill `json:"items"`
	Total int            `json:"total"`
}

type changesResponse struct {
	Cursor string         `json:"cursor"`
	Events []catalogEvent `json:"events"`
}

type catalogEvent struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Object       string         `json:"object"`
	ResourceType string         `json:"resourceType"`
	ResourceID   string         `json:"resourceId"`
	SkillSet     string         `json:"skillset,omitempty"`
	Version      string         `json:"version,omitempty"`
	Revision     string         `json:"revision,omitempty"`
	Payload      map[string]any `json:"payload,omitempty"`
	CreatedAt    int64          `json:"createdAt,omitempty"`
}

type catalogSkill struct {
	Name          string          `json:"name"`
	LatestVersion string          `json:"latestVersion"`
	Revision      string          `json:"revision,omitempty"`
	SHA256        string          `json:"sha256,omitempty"`
	Download      catalogDownload `json:"download"`
}

type skillRef struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Revision       string `json:"revision,omitempty"`
	Source         string `json:"source,omitempty"`
	Object         string `json:"object,omitempty"`
	CommitSHA      string `json:"commitSHA,omitempty"`
	TreeSHA        string `json:"treeSHA,omitempty"`
	ManifestSHA256 string `json:"manifestSHA256,omitempty"`
	ViaSkillSet    string `json:"viaSkillSet,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	MD5            string `json:"md5,omitempty"`
	Size           int64  `json:"size,omitempty"`
	DownloadURL    string `json:"downloadUrl"`
}

type catalogDownload struct {
	URL     string `json:"url"`
	Version string `json:"version"`
	MD5     string `json:"md5"`
	Size    int64  `json:"size,omitempty"`
}

type SkillSnapshot struct {
	SnapshotID  string              `json:"snapshotId"`
	RuntimeID   string              `json:"runtimeId"`
	SessionID   string              `json:"sessionId,omitempty"`
	SkillSet    string              `json:"skillset,omitempty"`
	Revision    string              `json:"revision"`
	GeneratedAt string              `json:"generatedAt"`
	Policy      string              `json:"policy"`
	Skills      []SkillSnapshotItem `json:"skills"`
}

// AgentDefinition is the Hub-owned immutable file payload for a single Agent
// version. It is intentionally transport-only; configuration parsing stays in
// AgentKit's configurable package.
type AgentDefinition struct {
	EntryPoint string            `json:"entryPoint"`
	Files      map[string]string `json:"files"`
	Sandbox    SandboxSpec       `json:"sandbox,omitempty"`
	Model      ModelSpec         `json:"model,omitempty"`
}

// ToolSnapshotItem is the permission-filtered, version-pinned tool contract
// returned by Hub for one Agent snapshot. Runtime never accepts tool names
// from the model; it only exposes this allowlisted set to the sandbox worker.
type ToolSnapshotItem struct {
	Name          string                 `json:"name"`
	Version       string                 `json:"version"`
	Revision      string                 `json:"revision"`
	Object        string                 `json:"object"`
	Status        string                 `json:"status,omitempty"`
	Runtime       map[string]interface{} `json:"runtime,omitempty"`
	Execution     map[string]interface{} `json:"execution,omitempty"`
	InputSchema   map[string]interface{} `json:"inputSchema,omitempty"`
	OutputSchema  map[string]interface{} `json:"outputSchema,omitempty"`
	TimeoutMillis int64                  `json:"timeoutMillis,omitempty"`
	Retry         map[string]interface{} `json:"retry,omitempty"`
	Metadata      map[string]interface{} `json:"metadata,omitempty"`
}

// ModelSpec is the product-layer model requirement resolved by Hub for an
// Agent snapshot. Runtime/worker sends this to aisphere-gateway.
type ModelSpec struct {
	Profile   string         `json:"profile,omitempty"`
	Model     string         `json:"model,omitempty"`
	Provider  string         `json:"provider,omitempty"`
	BaseURL   string         `json:"baseURL,omitempty"`
	APIFormat string         `json:"apiFormat,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

// SandboxSpec is the product-layer sandbox requirement resolved by Hub for an
// Agent snapshot. Runtime sends it to aisphere-sandbox Adapter; it is not a
// Kubernetes Pod spec.
type SandboxSpec struct {
	Profile     string         `json:"profile,omitempty"`
	Reuse       string         `json:"reuse,omitempty"`
	TemplateRef string         `json:"templateRef,omitempty"`
	WarmPoolRef string         `json:"warmPoolRef,omitempty"`
	NetworkMode string         `json:"networkMode,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

type AgentSnapshot struct {
	SnapshotID    string              `json:"snapshotId"`
	RuntimeID     string              `json:"runtimeId"`
	SessionID     string              `json:"sessionId"`
	AgentID       string              `json:"agentId"`
	AgentVersion  string              `json:"agentVersion"`
	AgentRevision string              `json:"agentRevision"`
	GeneratedAt   string              `json:"generatedAt"`
	Policy        string              `json:"policy"`
	Definition    AgentDefinition     `json:"definition"`
	Sandbox       SandboxSpec         `json:"sandbox,omitempty"`
	Model         ModelSpec           `json:"model,omitempty"`
	Skills        []SkillSnapshotItem `json:"skills"`
	Tools         []ToolSnapshotItem  `json:"tools,omitempty"`
	Authorization map[string]any      `json:"authorization,omitempty"`
}

type AgentListItem struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName,omitempty"`
	Description string `json:"description,omitempty"`
}

type SkillSnapshotItem struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Revision       string `json:"revision,omitempty"`
	Source         string `json:"source,omitempty"`
	Object         string `json:"object,omitempty"`
	CommitSHA      string `json:"commitSHA,omitempty"`
	TreeSHA        string `json:"treeSHA,omitempty"`
	ManifestSHA256 string `json:"manifestSHA256,omitempty"`
	ViaSkillSet    string `json:"viaSkillSet,omitempty"`
	SHA256         string `json:"sha256,omitempty"`
	MD5            string `json:"md5,omitempty"`
	Size           int64  `json:"size,omitempty"`
	DownloadURL    string `json:"downloadUrl,omitempty"`
	CachePath      string `json:"cachePath,omitempty"`
	MountPath      string `json:"mountPath,omitempty"`
}

type resolveSessionRequest struct {
	RuntimeID       string   `json:"runtimeId"`
	SessionID       string   `json:"sessionId"`
	SkillSet        string   `json:"skillset"`
	Policy          string   `json:"policy"`
	RequestedSkills []string `json:"requestedSkills,omitempty"`
}

type resolveSessionResponse struct {
	RuntimeID   string     `json:"runtimeId"`
	SessionID   string     `json:"sessionId"`
	SkillSet    string     `json:"skillset"`
	SnapshotID  string     `json:"snapshotId"`
	Revision    string     `json:"revision"`
	GeneratedAt string     `json:"generatedAt"`
	Policy      string     `json:"policy"`
	Skills      []skillRef `json:"skills"`
}

type resolveAgentRequest struct {
	RuntimeID         string   `json:"runtimeId"`
	SessionID         string   `json:"sessionId"`
	Policy            string   `json:"policy"`
	Version           string   `json:"version,omitempty"`
	ApprovalConfirmed bool     `json:"approvalConfirmed,omitempty"`
	ApprovedTools     []string `json:"approvedTools,omitempty"`
}

// resolveAgentV1Request is the newer Hub Agent HTTP contract. Keep it
// separate from the legacy Runtime request so either Hub can be selected by
// the same Runtime binary during the migration window.
type resolveAgentV1Request struct {
	RuntimeID         string   `json:"runtimeId"`
	SessionID         string   `json:"sessionId"`
	Version           string   `json:"version,omitempty"`
	ApprovalConfirmed bool     `json:"approvalConfirmed,omitempty"`
	ApprovedTools     []string `json:"approvedTools,omitempty"`
}

// AgentResolveOptions carries the caller's run approval decision into Hub.
// Hub remains the authorization authority; Runtime only forwards this decision
// and consumes the resulting immutable snapshot.
type AgentResolveOptions struct {
	Version           string
	ApprovalConfirmed bool
	ApprovedTools     []string
}

type resolveAgentResponse struct {
	SnapshotID    string          `json:"snapshotId"`
	RuntimeID     string          `json:"runtimeId"`
	SessionID     string          `json:"sessionId"`
	AgentID       string          `json:"agentId"`
	AgentVersion  string          `json:"agentVersion"`
	AgentRevision string          `json:"agentRevision"`
	GeneratedAt   string          `json:"generatedAt"`
	Policy        string          `json:"policy"`
	Definition    AgentDefinition `json:"definition"`
	Sandbox       SandboxSpec     `json:"sandbox,omitempty"`
	// Model stays raw: Hub v1 serializes the model snapshot either as a flat
	// ModelSpec or with a nested provider object. normalizeModelSpec handles
	// both shapes.
	Model         json.RawMessage    `json:"model,omitempty"`
	Skills        []skillRef         `json:"skills"`
	Tools         []ToolSnapshotItem `json:"tools,omitempty"`
	Authorization map[string]any     `json:"authorization,omitempty"`
}

// normalizeModelSpec parses Hub's model snapshot into a flat ModelSpec.
// Accepted shapes:
//   - {"profile":…, "model":"deepseek-...", "baseURL":…, "provider":…}          (flat)
//   - {"profileId":…, "model":{"providerModelId":…}, "baseUrl":…}               (nested endpoint)
//   - {"model":{…}, "profile":{…}, "endpoint":{…}, "reasoning":{…}}             (resource v2)
//
// The resource-v2 shape keeps connection details under "endpoint": base_url,
// adapter (provider), api_format and provider_model_id; "credential_ref" is
// forwarded through metadata so the resolver can surface the API key without
// the token ever entering the executed plan's authorization tree.
func normalizeModelSpec(raw json.RawMessage) (ModelSpec, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return ModelSpec{}, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return ModelSpec{}, err
	}
	spec := ModelSpec{}
	if v, ok := obj["profileId"].(string); ok {
		spec.Profile = v
	}
	if v, ok := obj["profile"].(map[string]any); ok {
		if code, ok := v["code"].(string); ok {
			spec.Profile = firstNonEmpty(spec.Profile, code)
		}
	} else if v, ok := obj["profile"].(string); ok && spec.Profile == "" {
		spec.Profile = v
	}
	if v, ok := obj["provider"].(string); ok {
		spec.Provider = v
	}
	if v, ok := obj["baseUrl"].(string); ok {
		spec.BaseURL = v
	}
	if v, ok := obj["baseURL"].(string); ok && spec.BaseURL == "" {
		spec.BaseURL = v
	}
	if v, ok := obj["apiFormat"].(string); ok {
		spec.APIFormat = v
	}
	switch m := obj["model"].(type) {
	case string:
		spec.Model = m
	case map[string]any:
		if v, ok := m["providerModelId"].(string); ok {
			spec.Model = v
		} else if v, ok := m["name"].(string); ok {
			spec.Model = v
		} else if v, ok := m["id"].(string); ok {
			spec.Model = v
		} else if v, ok := m["code"].(string); ok {
			spec.Model = v
		}
	}
	// Resource v2: endpoint carries the connection contract.
	if endpoint, ok := obj["endpoint"].(map[string]any); ok {
		if v, ok := endpoint["baseUrl"].(string); ok {
			spec.BaseURL = v
		}
		if v, ok := endpoint["adapter"].(string); ok {
			spec.Provider = v
		} else if v, ok := endpoint["apiFormat"].(string); ok {
			spec.Provider = firstNonEmpty(spec.Provider, v)
		}
		if v, ok := endpoint["apiFormat"].(string); ok {
			spec.APIFormat = v
		}
		if v, ok := endpoint["providerModelId"].(string); ok {
			// endpoint.providerModelId is the authoritative connection name;
			// it overrides the catalog's internal model UUID.
			spec.Model = v
		}
		if v, ok := endpoint["credentialRef"].(string); ok {
			if spec.Metadata == nil {
				spec.Metadata = map[string]any{}
			}
			spec.Metadata["credentialRef"] = v
		}
		if v, ok := endpoint["requestDefaults"].(map[string]any); ok {
			if spec.Metadata == nil {
				spec.Metadata = map[string]any{}
			}
			for key, value := range v {
				if _, exists := spec.Metadata[key]; !exists {
					spec.Metadata[key] = value
				}
			}
		}
	}
	if v, ok := obj["metadata"].(map[string]any); ok {
		if spec.Metadata == nil {
			spec.Metadata = map[string]any{}
		}
		for key, value := range v {
			if _, exists := spec.Metadata[key]; !exists {
				spec.Metadata[key] = value
			}
		}
	} else if v, ok := obj["defaultParameters"].(map[string]any); ok {
		if spec.Metadata == nil {
			spec.Metadata = map[string]any{}
		}
		for key, value := range v {
			spec.Metadata[key] = value
		}
	}
	return spec, nil
}

func New(cfg runtimeconfig.AIHubSkillConfig) (*Client, error) {
	cfg.Endpoint = strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if !cfg.Enabled {
		return nil, nil
	}
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("skills.aihub.endpoint is required when skills.aihub.enabled=true")
	}
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 30
	}
	if cfg.AuthHeader == "" {
		cfg.AuthHeader = "Authorization"
	}
	if cfg.AuthScheme == "" && strings.EqualFold(cfg.AuthHeader, "Authorization") {
		cfg.AuthScheme = "Bearer"
	}
	if cfg.Reload.Policy == "" {
		cfg.Reload.Policy = "new_sessions_only"
	}
	if cfg.Revoke.Policy == "" {
		cfg.Revoke.Policy = "disable_new_sessions"
	}
	if cfg.Watch.ReconnectSeconds <= 0 {
		cfg.Watch.ReconnectSeconds = 5
	}
	if cfg.Watch.PollIntervalSeconds <= 0 {
		cfg.Watch.PollIntervalSeconds = 30
	}
	return &Client{cfg: cfg, httpClient: &http.Client{Timeout: time.Duration(cfg.TimeoutSeconds) * time.Second}}, nil
}

func (c *Client) Enabled() bool { return c != nil && c.cfg.Enabled }

func (c *Client) SyncSkills(ctx context.Context, svc skillservice.Service) (*SyncResult, error) {
	if c == nil || !c.cfg.Enabled {
		return &SyncResult{}, nil
	}
	if svc == nil {
		return nil, fmt.Errorf("skill service is nil")
	}
	snap, err := c.resolveStartupSnapshot(ctx)
	if err != nil {
		return nil, err
	}
	root := svc.Root()
	result := &SyncResult{SkillSet: snap.SkillSet, Revision: snap.Revision, SnapshotID: snap.SnapshotID, Discovered: len(snap.Skills), Imported: []string{}, Activated: []string{}, Skipped: []string{}, Pruned: []string{}, CacheRoot: c.cacheRoot(root), ActivePolicy: c.cfg.Reload.Policy}
	for i := range snap.Skills {
		item := &snap.Skills[i]
		if item.Name == "" || item.Version == "" || item.MountPath == "" {
			result.Skipped = append(result.Skipped, item.Name+": incomplete manifest item")
			continue
		}
		changed, err := c.ensureCached(ctx, root, item)
		if err != nil {
			result.Skipped = append(result.Skipped, item.Name+": "+err.Error())
			continue
		}
		if changed {
			result.Downloaded++
		}
		if err := activateSkillVersion(root, *item); err != nil {
			result.Skipped = append(result.Skipped, item.Name+": activate: "+err.Error())
			continue
		}
		result.Activated = append(result.Activated, item.Name+"@"+item.Version)
		result.Imported = append(result.Imported, item.Name)
	}
	if err := c.writeSnapshot(root, snap); err != nil {
		result.Skipped = append(result.Skipped, "snapshot: "+err.Error())
	}
	pruned, err := c.pruneOldVersions(root, snap)
	if err != nil {
		result.Skipped = append(result.Skipped, "prune: "+err.Error())
	} else {
		result.Pruned = pruned
	}
	return result, nil
}

func (c *Client) ResolveSessionSnapshot(ctx context.Context, root, sessionID string, requested []string) (*SkillSnapshot, error) {
	req := resolveSessionRequest{RuntimeID: c.runtimeID(), SessionID: sessionID, SkillSet: c.cfg.SkillSet, Policy: firstNonEmpty(c.cfg.Reload.Policy, "latest_authorized"), RequestedSkills: requested}
	body, _ := json.Marshal(req)
	var out resolveSessionResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v3/aihub/runtime/sessions/resolve", bytes.NewReader(body), &out); err != nil {
		return nil, err
	}
	snap := c.snapshotFromRefs(out.SkillSet, out.Revision, out.SnapshotID, out.GeneratedAt, out.SessionID, out.Policy, out.Skills)
	for i := range snap.Skills {
		if _, err := c.ensureCached(ctx, root, &snap.Skills[i]); err != nil {
			return nil, err
		}
	}
	if err := c.writeSessionSnapshot(root, snap); err != nil {
		return nil, err
	}
	return snap, nil
}

// ResolveAgentSnapshot asks Hub to authorize and pin an Agent definition for
// one session. Hub evaluates Agent and Skill permissions from the forwarded
// authenticated browser session.
func (c *Client) ResolveAgentSnapshot(ctx context.Context, agentID, sessionID string) (*AgentSnapshot, error) {
	return c.ResolveAgentSnapshotWithOptions(ctx, agentID, sessionID, AgentResolveOptions{})
}

// ResolveAgentSnapshotWithOptions asks Hub to authorize and pin an Agent
// definition for one session, including an explicit per-run approval decision.
func (c *Client) ResolveAgentSnapshotWithOptions(ctx context.Context, agentID, sessionID string, options AgentResolveOptions) (*AgentSnapshot, error) {
	if c == nil || !c.cfg.Enabled {
		return nil, fmt.Errorf("aihub client is disabled")
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil, fmt.Errorf("agent id is required")
	}
	request := resolveAgentRequest{
		RuntimeID: c.runtimeID(), SessionID: strings.TrimSpace(sessionID), Policy: "pinned_authorized",
		Version: options.Version, ApprovalConfirmed: options.ApprovalConfirmed,
		ApprovedTools: options.ApprovedTools,
	}
	body, _ := json.Marshal(request)
	var response resolveAgentResponse
	path := "/v3/aihub/runtime/agents/" + url.PathEscape(agentID) + "/resolve"
	if err := c.doJSON(ctx, http.MethodPost, path, bytes.NewReader(body), &response); err != nil {
		if !isHTTPNotFound(err) {
			return nil, err
		}
		// New Kernelized Hub exposes the same immutable resolve concept under
		// /v1. Its authorization remains Hub-owned; Runtime only adapts the
		// transport shape and never falls back on permission failures.
		v1Request, _ := json.Marshal(resolveAgentV1Request{
			RuntimeID: c.runtimeID(), SessionID: strings.TrimSpace(sessionID),
			Version: options.Version, ApprovalConfirmed: options.ApprovalConfirmed,
			ApprovedTools: options.ApprovedTools,
		})
		if v1Err := c.doJSON(ctx, http.MethodPost, "/v1/agents/"+url.PathEscape(agentID)+":resolve", bytes.NewReader(v1Request), &response); v1Err != nil {
			return nil, fmt.Errorf("resolve agent via legacy and v1 Hub contracts: legacy=%v; v1=%w", err, v1Err)
		}
	}
	items := make([]SkillSnapshotItem, 0, len(response.Skills))
	for _, ref := range response.Skills {
		items = append(items, SkillSnapshotItem{
			Name: ref.Name, Version: ref.Version, Revision: ref.Revision,
			Source: ref.Source, Object: firstNonEmpty(ref.Object, "aihub:skill:"+ref.Name),
			CommitSHA: ref.CommitSHA, TreeSHA: ref.TreeSHA, ManifestSHA256: ref.ManifestSHA256,
			ViaSkillSet: ref.ViaSkillSet, SHA256: ref.SHA256, MD5: ref.MD5, Size: ref.Size,
			DownloadURL: ref.DownloadURL,
		})
	}
	sandbox := response.Sandbox
	if sandbox.Profile == "" && response.Definition.Sandbox.Profile != "" {
		sandbox = response.Definition.Sandbox
	}
	model, err := normalizeModelSpec(response.Model)
	if err != nil {
		return nil, fmt.Errorf("parse model snapshot: %w", err)
	}
	if modelSpecEmpty(model) {
		model = response.Definition.Model
	}
	return &AgentSnapshot{
		SnapshotID: response.SnapshotID, RuntimeID: response.RuntimeID, SessionID: response.SessionID,
		AgentID: response.AgentID, AgentVersion: response.AgentVersion, AgentRevision: response.AgentRevision,
		GeneratedAt: response.GeneratedAt, Policy: response.Policy, Definition: response.Definition,
		Sandbox: sandbox, Model: model, Skills: items, Tools: response.Tools,
		Authorization: response.Authorization,
	}, nil
}

func modelSpecEmpty(model ModelSpec) bool {
	return model.Profile == "" && model.Model == "" && model.Provider == "" && model.BaseURL == "" && model.APIFormat == "" && len(model.Metadata) == 0
}

// CacheAgentSnapshotSkills downloads and verifies the exact skill versions
// authorized in an Agent snapshot. The caller may then materialize the cached
// files into a sandbox workspace without giving the sandbox Hub credentials.
func (c *Client) CacheAgentSnapshotSkills(ctx context.Context, root string, snapshot *AgentSnapshot) error {
	if c == nil || !c.Enabled() || snapshot == nil {
		return nil
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return fmt.Errorf("skill cache root is required")
	}
	for i := range snapshot.Skills {
		item := &snapshot.Skills[i]
		switch strings.ToLower(strings.TrimSpace(item.Source)) {
		case "builtin":
			// Built-ins are packaged with the immutable Runtime image. CachePath
			// may be supplied explicitly for tests or alternate images.
			continue
		case "catalog":
			if strings.TrimSpace(item.DownloadURL) == "" && strings.TrimSpace(item.CachePath) == "" {
				return skillRuntimeError(CodeSkillPackageURLRequired, fmt.Errorf("catalog skill %s@%s has no package URL", item.Name, item.Version))
			}
		case "":
			return skillRuntimeError(CodeSkillMaterializeFailed, fmt.Errorf("skill %s@%s has no source", item.Name, item.Version))
		default:
			return skillRuntimeError(CodeSkillMaterializeFailed, fmt.Errorf("skill %s@%s has unsupported source %q", item.Name, item.Version, item.Source))
		}
		if _, err := c.ensureCached(ctx, root, &snapshot.Skills[i]); err != nil {
			return fmt.Errorf("cache skill %s@%s: %w", snapshot.Skills[i].Name, snapshot.Skills[i].Version, err)
		}
	}
	return nil
}

// PrepareAgentSnapshotSkillRoot materializes exactly the skill versions in an
// Agent snapshot into an isolated Runtime-side root. This is used by the
// in-process GoRunner; sandbox workers use their own .aisphere/skills mount.
// The session-specific root prevents one session's pinned skill set from
// changing another session's skill context.
func PrepareAgentSnapshotSkillRoot(root string, snapshot *AgentSnapshot) (string, error) {
	if snapshot == nil {
		return "", fmt.Errorf("agent snapshot is required")
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return "", fmt.Errorf("skill cache root is required")
	}
	key := safePath(firstNonEmpty(snapshot.SessionID, snapshot.SnapshotID))
	destination := filepath.Join(root, ".aihub", "sessions", key, "runtime-skills")
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return "", fmt.Errorf("create runtime skill root: %w", err)
	}
	for i := range snapshot.Skills {
		item := &snapshot.Skills[i]
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		var source string
		switch strings.ToLower(strings.TrimSpace(item.Source)) {
		case "builtin":
			source = strings.TrimSpace(item.CachePath)
			if source == "" {
				source = filepath.Join(root, safePath(name))
			}
		case "catalog":
			source = strings.TrimSpace(item.CachePath)
			if source == "" {
				return "", skillRuntimeError(CodeSkillMaterializeFailed, fmt.Errorf("catalog skill %s@%s is not cached", name, item.Version))
			}
		case "":
			return "", skillRuntimeError(CodeSkillMaterializeFailed, fmt.Errorf("skill %s@%s has no source", name, item.Version))
		default:
			return "", skillRuntimeError(CodeSkillMaterializeFailed, fmt.Errorf("skill %s@%s has unsupported source %q", name, item.Version, item.Source))
		}
		if _, err := os.Stat(filepath.Join(source, skillservice.SkillFileName)); err != nil {
			return "", skillRuntimeError(CodeSkillMaterializeFailed, fmt.Errorf("skill %s@%s is not materialized at %s: %w", name, item.Version, source, err))
		}
		target := filepath.Join(destination, safePath(name))
		tmp := target + ".tmp"
		if err := os.RemoveAll(tmp); err != nil {
			return "", fmt.Errorf("clear temporary skill root %s: %w", name, err)
		}
		if err := copyDir(source, tmp); err != nil {
			_ = os.RemoveAll(tmp)
			return "", fmt.Errorf("copy skill %s@%s: %w", name, item.Version, err)
		}
		if err := os.RemoveAll(target); err != nil {
			_ = os.RemoveAll(tmp)
			return "", fmt.Errorf("replace skill %s: %w", name, err)
		}
		if err := os.Rename(tmp, target); err != nil {
			_ = os.RemoveAll(tmp)
			return "", fmt.Errorf("activate skill %s: %w", name, err)
		}
		item.MountPath = filepath.ToSlash(filepath.Join(".aihub", "sessions", key, "runtime-skills", safePath(name)))
	}
	return destination, nil
}

// ListAgents returns the Agent identifiers that Hub has already filtered for
// the authenticated request principal.
func (c *Client) ListAgents(ctx context.Context) ([]AgentListItem, error) {
	if c == nil || !c.cfg.Enabled {
		return nil, fmt.Errorf("aihub client is disabled")
	}
	var response struct {
		Items []AgentListItem `json:"items"`
	}
	if err := c.doJSON(ctx, http.MethodGet, "/v3/aihub/agents", nil, &response); err != nil {
		if !isHTTPNotFound(err) {
			return nil, err
		}
		if v1Err := c.doJSON(ctx, http.MethodGet, "/v1/agents", nil, &response); v1Err != nil {
			return nil, fmt.Errorf("list agents via legacy and v1 Hub contracts: legacy=%v; v1=%w", err, v1Err)
		}
	}
	return response.Items, nil
}

func (c *Client) Watch(ctx context.Context, svc skillservice.Service, onChange func(*SyncResult)) {
	if c == nil || !c.cfg.Enabled || !c.cfg.Watch.Enabled || svc == nil {
		return
	}
	go func() {
		root := svc.Root()
		cursor := c.readWatchCursor(root)
		ticker := time.NewTicker(time.Duration(c.cfg.Watch.PollIntervalSeconds) * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				changes, err := c.fetchChanges(ctx, cursor)
				if err != nil {
					continue
				}
				if changes.Cursor != "" {
					cursor = changes.Cursor
					_ = c.writeWatchCursor(root, cursor)
				}
				if len(changes.Events) == 0 {
					continue
				}
				result, err := c.SyncSkills(ctx, svc)
				if err != nil {
					continue
				}
				_ = c.ReportInstalledSkills(ctx, svc, result)
				if onChange != nil {
					onChange(result)
				}
			}
		}
	}()
}

func (c *Client) fetchChanges(ctx context.Context, cursor string) (*changesResponse, error) {
	q := url.Values{}
	if strings.TrimSpace(c.cfg.SkillSet) != "" {
		q.Set("skillset", c.cfg.SkillSet)
	}
	if strings.TrimSpace(cursor) != "" {
		q.Set("since", cursor)
	}
	var out changesResponse
	path := "/v3/aihub/catalog/changes"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) watchStatePath(root string) string {
	return filepath.Join(c.cacheRoot(root), "watch.json")
}

func (c *Client) readWatchCursor(root string) string {
	b, err := os.ReadFile(c.watchStatePath(root))
	if err != nil {
		return ""
	}
	var state struct {
		Cursor string `json:"cursor"`
	}
	_ = json.Unmarshal(b, &state)
	return state.Cursor
}

func (c *Client) writeWatchCursor(root, cursor string) error {
	path := c.watchStatePath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(map[string]any{"cursor": cursor, "updatedAt": time.Now().UTC().Format(time.RFC3339)}, "", "  ")
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func (c *Client) ReportInstalledSkills(ctx context.Context, svc skillservice.Service, result *SyncResult) error {
	if c == nil || !c.cfg.Enabled || !c.cfg.ReportOnStart || svc == nil {
		return nil
	}
	items, err := svc.List(ctx)
	if err != nil {
		return err
	}
	skills := make([]map[string]any, 0, len(items))
	for _, item := range items {
		skills = append(skills, map[string]any{"name": item.Name, "version": item.Version, "status": item.Status, "updatedAt": item.UpdatedAt})
	}
	hostname, _ := os.Hostname()
	payload := map[string]any{"runtimeId": c.runtimeID(), "hostname": hostname, "skillSet": c.cfg.SkillSet, "skills": skills}
	if result != nil {
		payload["sync"] = result
	}
	body, _ := json.Marshal(payload)
	var out map[string]any
	return c.doJSON(ctx, http.MethodPost, "/v3/aihub/runtime/installed-skills", bytes.NewReader(body), &out)
}

func (c *Client) resolveStartupSnapshot(ctx context.Context) (*SkillSnapshot, error) {
	refs, rev, generatedAt, err := c.discover(ctx)
	if err != nil {
		return nil, err
	}
	return c.snapshotFromRefs(c.cfg.SkillSet, rev, "", generatedAt, "", firstNonEmpty(c.cfg.Reload.Policy, "new_sessions_only"), refs), nil
}

func (c *Client) snapshotFromRefs(skillset, revision, snapshotID, generatedAt, sessionID, policy string, refs []skillRef) *SkillSnapshot {
	if generatedAt == "" {
		generatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if revision == "" {
		revision = digestRefs(refs)
	}
	if snapshotID == "" {
		snapshotID = "snap_" + revisionShort(revision)
	}
	skills := make([]SkillSnapshotItem, 0, len(refs))
	for _, ref := range refs {
		version := firstNonEmpty(ref.Version, "latest")
		source := strings.ToLower(strings.TrimSpace(ref.Source))
		if source == "" {
			// Legacy discovery contracts only returned catalog package fields.
			// Normalize that transport shape once; execution paths still switch
			// exclusively on the explicit Source field.
			source = "catalog"
		}
		item := SkillSnapshotItem{
			Name: ref.Name, Version: version, Revision: ref.Revision,
			Source: source, Object: firstNonEmpty(ref.Object, "aihub:skill:"+ref.Name),
			CommitSHA: ref.CommitSHA, TreeSHA: ref.TreeSHA, ManifestSHA256: ref.ManifestSHA256,
			ViaSkillSet: ref.ViaSkillSet, SHA256: ref.SHA256, MD5: ref.MD5, Size: ref.Size,
			DownloadURL: ref.DownloadURL, MountPath: filepath.ToSlash(filepath.Join(ref.Name)),
		}
		skills = append(skills, item)
	}
	return &SkillSnapshot{SnapshotID: snapshotID, RuntimeID: c.runtimeID(), SessionID: sessionID, SkillSet: skillset, Revision: revision, GeneratedAt: generatedAt, Policy: policy, Skills: skills}
}

func (c *Client) discover(ctx context.Context) ([]skillRef, string, string, error) {
	if strings.TrimSpace(c.cfg.SkillSet) != "" {
		path := "/v3/aihub/catalog/skillsets/" + url.PathEscape(c.cfg.SkillSet) + "/manifest"
		var out skillSetManifestResponse
		if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
			return nil, "", "", err
		}
		rev := firstNonEmpty(out.SkillSet.Revision, out.Revision, out.ETag)
		return out.SkillSet.Members, rev, out.SkillSet.GeneratedAt, nil
	}
	var out skillsResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v3/aihub/catalog/skills?pageSize=200", nil, &out); err != nil {
		return nil, "", "", err
	}
	refs := make([]skillRef, 0, len(out.Items))
	for _, item := range out.Items {
		refs = append(refs, skillRef{Name: item.Name, Version: firstNonEmpty(item.Download.Version, item.LatestVersion), Revision: item.Revision, SHA256: item.SHA256, MD5: item.Download.MD5, Size: item.Download.Size, DownloadURL: item.Download.URL})
	}
	return refs, digestRefs(refs), time.Now().UTC().Format(time.RFC3339), nil
}

func (c *Client) ensureCached(ctx context.Context, root string, item *SkillSnapshotItem) (bool, error) {
	versionDir := c.versionDir(root, item.Name, item.Version)
	marker := filepath.Join(versionDir, ".aihub-version.json")
	if data, err := os.ReadFile(marker); err == nil {
		var old SkillSnapshotItem
		_ = json.Unmarshal(data, &old)
		if old.Version == item.Version && (item.SHA256 == "" || old.SHA256 == item.SHA256) {
			item.CachePath = versionDir
			return false, nil
		}
	}
	data, err := c.download(ctx, item.DownloadURL)
	if err != nil {
		return false, skillRuntimeError(CodeSkillPackageDownloadFailed, err)
	}
	if item.SHA256 != "" && sha256Hex(data) != strings.TrimPrefix(item.SHA256, "sha256:") {
		return false, skillRuntimeError(CodeSkillPackageDigestMismatch, fmt.Errorf("sha256 mismatch"))
	}
	tmp := versionDir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := unzipSkillPackage(data, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return false, skillRuntimeError(CodeSkillPackageUnpackFailed, err)
	}
	if err := os.MkdirAll(filepath.Dir(versionDir), 0o755); err != nil {
		return false, skillRuntimeError(CodeSkillMaterializeFailed, err)
	}
	_ = os.RemoveAll(versionDir)
	if err := os.Rename(tmp, versionDir); err != nil {
		return false, skillRuntimeError(CodeSkillMaterializeFailed, err)
	}
	item.CachePath = versionDir
	b, _ := json.MarshalIndent(item, "", "  ")
	_ = os.WriteFile(marker, append(b, '\n'), 0o644)
	return true, nil
}

func activateSkillVersion(root string, item SkillSnapshotItem) error {
	if item.CachePath == "" {
		return fmt.Errorf("empty cache path")
	}
	active := filepath.Join(root, item.Name)
	tmp := active + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := copyDir(item.CachePath, tmp); err != nil {
		return err
	}
	_ = os.RemoveAll(active)
	return os.Rename(tmp, active)
}

func (c *Client) writeSnapshot(root string, snap *SkillSnapshot) error {
	if snap == nil {
		return nil
	}
	dir := filepath.Join(c.cacheRoot(root), "snapshots")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(snap, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "latest.json"), append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, snap.SnapshotID+".json"), append(b, '\n'), 0o644)
}

func (c *Client) writeSessionSnapshot(root string, snap *SkillSnapshot) error {
	if snap == nil || snap.SessionID == "" {
		return c.writeSnapshot(root, snap)
	}
	dir := filepath.Join(root, ".aihub", "sessions", snap.SessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(snap, "", "  ")
	return os.WriteFile(filepath.Join(dir, "snapshot.json"), append(b, '\n'), 0o644)
}

func (c *Client) pruneOldVersions(root string, snap *SkillSnapshot) ([]string, error) {
	keep := c.cfg.Reload.KeepOldVersions
	if keep <= 0 {
		return nil, nil
	}
	active := map[string]map[string]bool{}
	if snap != nil {
		for _, item := range snap.Skills {
			if active[item.Name] == nil {
				active[item.Name] = map[string]bool{}
			}
			active[item.Name][item.Version] = true
		}
	}
	rootDir := filepath.Join(c.cacheRoot(root), "skills")
	entries, err := os.ReadDir(rootDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	pruned := []string{}
	for _, skillEntry := range entries {
		if !skillEntry.IsDir() {
			continue
		}
		skillName := skillEntry.Name()
		versionsDir := filepath.Join(rootDir, skillName, "versions")
		versions, err := os.ReadDir(versionsDir)
		if err != nil {
			continue
		}
		type versionInfo struct {
			name string
			path string
			mod  time.Time
		}
		infos := []versionInfo{}
		for _, ve := range versions {
			if !ve.IsDir() {
				continue
			}
			info, _ := ve.Info()
			mod := time.Time{}
			if info != nil {
				mod = info.ModTime()
			}
			infos = append(infos, versionInfo{name: ve.Name(), path: filepath.Join(versionsDir, ve.Name()), mod: mod})
		}
		sort.Slice(infos, func(i, j int) bool { return infos[i].mod.After(infos[j].mod) })
		kept := 0
		for _, info := range infos {
			if active[skillName] != nil && active[skillName][info.name] {
				kept++
				continue
			}
			if kept < keep {
				kept++
				continue
			}
			if err := os.RemoveAll(info.path); err == nil {
				pruned = append(pruned, skillName+"@"+info.name)
			}
		}
	}
	return pruned, nil
}

func (c *Client) cacheRoot(root string) string { return filepath.Join(root, ".aihub") }
func (c *Client) versionDir(root, name, version string) string {
	return filepath.Join(c.cacheRoot(root), "skills", safePath(name), "versions", safePath(version))
}

func (c *Client) download(ctx context.Context, rawURL string) ([]byte, error) {
	fullURL := c.resolveURL(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	c.decorate(req)
	c.applyCookieHeader(ctx, req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("download %s failed: http=%d body=%s", rawURL, resp.StatusCode, string(b))
	}
	return io.ReadAll(resp.Body)
}

func (c *Client) doJSON(ctx context.Context, method, rawPath string, body io.Reader, out any) error {
	fullURL := c.resolveURL(rawPath)
	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.decorate(req)
	c.applyCookieHeader(ctx, req)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if len(b) > 4096 {
			b = b[:4096]
		}
		return fmt.Errorf("aihub catalog http=%d body=%s", resp.StatusCode, string(b))
	}
	if out == nil || len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, out)
}

func isHTTPNotFound(err error) bool {
	return err != nil && strings.Contains(err.Error(), "http=404")
}

func (c *Client) applyCookieHeader(ctx context.Context, req *http.Request) {
	if cookieHeaderFromContext(ctx) == "" || req == nil || req.URL == nil {
		return
	}
	hubURL, err := url.Parse(c.cfg.Endpoint)
	if err != nil || !strings.EqualFold(req.URL.Host, hubURL.Host) {
		return
	}
	req.Header.Set("Cookie", cookieHeaderFromContext(ctx))
}

func (c *Client) decorate(req *http.Request) {
	for k, v := range c.cfg.ExtraHeaders {
		if strings.TrimSpace(k) != "" && strings.TrimSpace(v) != "" {
			req.Header.Set(k, os.ExpandEnv(v))
		}
	}
	token := strings.TrimSpace(c.cfg.Token)
	if token == "" && strings.TrimSpace(c.cfg.TokenEnv) != "" {
		token = os.Getenv(strings.TrimSpace(c.cfg.TokenEnv))
	}
	if token == "" || strings.TrimSpace(c.cfg.AuthHeader) == "" {
		c.applyForwardedPrincipalHeaders(req)
		return
	}
	value := token
	if strings.TrimSpace(c.cfg.AuthScheme) != "" {
		value = strings.TrimSpace(c.cfg.AuthScheme) + " " + value
	}
	req.Header.Set(c.cfg.AuthHeader, value)
	c.applyForwardedPrincipalHeaders(req)
}

func (c *Client) applyForwardedPrincipalHeaders(req *http.Request) {
	if req == nil {
		return
	}
	values := requestHeadersFromContext(req.Context())
	if token := strings.TrimSpace(values["X-Aisphere-Principal-JWT"]); token != "" {
		req.Header.Set("X-Aisphere-Principal-JWT", token)
		return
	}
	if !strings.EqualFold(values["X-Aisphere-Auth-Verified"], "true") || strings.TrimSpace(values["X-Aisphere-Subject"]) == "" {
		return
	}
	for _, name := range forwardedPrincipalHeaders {
		if value := strings.TrimSpace(values[name]); value != "" {
			req.Header.Set(name, value)
		}
	}
}

func (c *Client) resolveURL(raw string) string {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return raw
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return c.cfg.Endpoint + raw
}

func (c *Client) runtimeID() string {
	if strings.TrimSpace(c.cfg.RuntimeID) != "" {
		return strings.TrimSpace(c.cfg.RuntimeID)
	}
	hostname, _ := os.Hostname()
	if hostname != "" {
		return "runtime:" + hostname
	}
	return "runtime:local"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func safePath(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "/", "_")
	s = strings.ReplaceAll(s, "\\", "_")
	if s == "" {
		return "_"
	}
	return s
}
func sha256Hex(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func revisionShort(s string) string {
	s = strings.TrimPrefix(s, "sha256:")
	if len(s) > 16 {
		return s[:16]
	}
	return s
}
func digestRefs(refs []skillRef) string { b, _ := json.Marshal(refs); return sha256Hex(b) }

func unzipSkillPackage(data []byte, dst string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	base := ""
	for _, f := range zr.File {
		name := filepath.ToSlash(f.Name)
		if strings.HasSuffix(name, "/SKILL.md") {
			base = strings.TrimSuffix(name, "/SKILL.md")
			break
		}
		if name == "SKILL.md" {
			base = ""
			break
		}
	}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(f.Name)
		if base != "" {
			if !strings.HasPrefix(name, base+"/") {
				continue
			}
			name = strings.TrimPrefix(name, base+"/")
		}
		name = strings.TrimPrefix(name, "/")
		if name == "" || strings.Contains(name, "..") {
			continue
		}
		target := filepath.Join(dst, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, content, f.Mode()); err != nil {
			return err
		}
	}
	if _, err := os.Stat(filepath.Join(dst, skillservice.SkillFileName)); err != nil {
		return fmt.Errorf("package missing %s", skillservice.SkillFileName)
	}
	return nil
}

func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}

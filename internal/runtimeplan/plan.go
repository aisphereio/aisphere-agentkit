// Package runtimeplan converts Hub-authorized Agent snapshots into the
// execution contract consumed by AgentKit Runtime. Hub remains the control
// plane; RuntimePlan is the execution-plane boundary.
package runtimeplan

import (
	"fmt"
	"maps"
	"strings"

	"google.golang.org/adk/internal/aihubruntime"
	"gopkg.in/yaml.v3"
)

type RuntimePlan struct {
	SnapshotID  string `json:"snapshotId"`
	RuntimeID   string `json:"runtimeId"`
	SessionID   string `json:"sessionId"`
	Policy      string `json:"policy"`
	GeneratedAt string `json:"generatedAt,omitempty"`

	Agent         AgentSpec         `json:"agent"`
	Model         ModelSpec         `json:"model,omitempty"`
	Sandbox       SandboxSpec       `json:"sandbox,omitempty"`
	Skills        []SkillBinding    `json:"skills,omitempty"`
	Tools         []ToolBinding     `json:"tools,omitempty"`
	MCPServers    []MCPBinding      `json:"mcpServers,omitempty"`
	Authorization AuthorizationSpec `json:"authorization,omitempty"`
}

type AgentSpec struct {
	ID          string            `json:"id"`
	Version     string            `json:"version,omitempty"`
	Revision    string            `json:"revision,omitempty"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	EntryPoint  string            `json:"entryPoint"`
	Instruction string            `json:"instruction,omitempty"`
	Files       map[string]string `json:"files,omitempty"`
}

type ModelSpec = aihubruntime.ModelSpec
type SandboxSpec = aihubruntime.SandboxSpec

type SkillBinding struct {
	Name           string `json:"name"`
	Version        string `json:"version,omitempty"`
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

type ToolBinding struct {
	Name             string                 `json:"name"`
	RuntimeName      string                 `json:"runtimeName,omitempty"`
	Version          string                 `json:"version,omitempty"`
	Revision         string                 `json:"revision,omitempty"`
	Object           string                 `json:"object,omitempty"`
	Status           string                 `json:"status,omitempty"`
	RuntimeType      string                 `json:"runtimeType,omitempty"`
	ApprovalMode     string                 `json:"approvalMode,omitempty"`
	Required         bool                   `json:"required,omitempty"`
	Approved         bool                   `json:"approved,omitempty"`
	RequiresApproval bool                   `json:"requiresApproval,omitempty"`
	Capabilities     []string               `json:"capabilities,omitempty"`
	Permissions      []IAMPermission        `json:"permissions,omitempty"`
	Runtime          map[string]interface{} `json:"runtime,omitempty"`
	Execution        map[string]interface{} `json:"execution,omitempty"`
	InputSchema      map[string]interface{} `json:"inputSchema,omitempty"`
	OutputSchema     map[string]interface{} `json:"outputSchema,omitempty"`
	TimeoutMillis    int64                  `json:"timeoutMillis,omitempty"`
	Retry            map[string]interface{} `json:"retry,omitempty"`
	Metadata         map[string]interface{} `json:"metadata,omitempty"`
}

type MCPBinding struct {
	Name       string                 `json:"name"`
	Server     string                 `json:"server,omitempty"`
	Transport  string                 `json:"transport,omitempty"`
	ToolFilter []string               `json:"toolFilter,omitempty"`
	Runtime    map[string]interface{} `json:"runtime,omitempty"`
}

type AuthorizationSpec struct {
	PrincipalSubject     string         `json:"principalSubject,omitempty"`
	PrincipalPropagation string         `json:"principalPropagation,omitempty"`
	IAMEnforcement       string         `json:"iamEnforcement,omitempty"`
	RequiresApproval     bool           `json:"requiresApproval,omitempty"`
	ApprovalConfirmed    bool           `json:"approvalConfirmed,omitempty"`
	Tools                []ToolApproval `json:"tools,omitempty"`
	Raw                  map[string]any `json:"raw,omitempty"`
}

type ToolApproval struct {
	Tool         string          `json:"tool"`
	Version      string          `json:"version,omitempty"`
	Required     bool            `json:"required,omitempty"`
	ApprovalMode string          `json:"approvalMode,omitempty"`
	Approved     bool            `json:"approved,omitempty"`
	Capabilities []string        `json:"capabilities,omitempty"`
	Permissions  []IAMPermission `json:"permissions,omitempty"`
}

type IAMPermission struct {
	ResourceType string `json:"resourceType,omitempty"`
	Permission   string `json:"permission,omitempty"`
	Enforcement  string `json:"enforcement,omitempty"`
}

func FromSnapshot(snapshot *aihubruntime.AgentSnapshot) (*RuntimePlan, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("agent snapshot is required")
	}
	if strings.TrimSpace(snapshot.SnapshotID) == "" {
		return nil, fmt.Errorf("agent snapshot id is required")
	}
	if strings.TrimSpace(snapshot.AgentID) == "" {
		return nil, fmt.Errorf("agent id is required")
	}
	if strings.TrimSpace(snapshot.Definition.EntryPoint) == "" {
		return nil, fmt.Errorf("agent definition entry point is required")
	}
	if len(snapshot.Definition.Files) == 0 {
		return nil, fmt.Errorf("agent definition files are required")
	}
	entryContent, ok := snapshot.Definition.Files[snapshot.Definition.EntryPoint]
	if !ok {
		return nil, fmt.Errorf("agent definition entry point %q is not present in files", snapshot.Definition.EntryPoint)
	}
	entry, err := parseEntryPoint(entryContent)
	if err != nil {
		return nil, err
	}
	model := snapshot.Model
	if isZeroModel(model) {
		model = snapshot.Definition.Model
	}
	authorization := parseAuthorization(snapshot.Authorization)
	tools := make([]ToolBinding, 0, len(snapshot.Tools))
	for _, item := range snapshot.Tools {
		tool := convertTool(item, authorization)
		if tool.Name != "" {
			tools = append(tools, tool)
		}
	}
	plan := &RuntimePlan{
		SnapshotID:  snapshot.SnapshotID,
		RuntimeID:   snapshot.RuntimeID,
		SessionID:   snapshot.SessionID,
		Policy:      snapshot.Policy,
		GeneratedAt: snapshot.GeneratedAt,
		Agent: AgentSpec{
			ID:          snapshot.AgentID,
			Version:     snapshot.AgentVersion,
			Revision:    snapshot.AgentRevision,
			Name:        firstNonEmpty(entry.Name, snapshot.AgentID),
			Description: entry.Description,
			EntryPoint:  snapshot.Definition.EntryPoint,
			Instruction: entry.Instruction,
			Files:       maps.Clone(snapshot.Definition.Files),
		},
		Model:         model,
		Sandbox:       snapshot.Sandbox,
		Skills:        convertSkills(snapshot.Skills),
		Tools:         tools,
		MCPServers:    convertMCPServers(tools),
		Authorization: authorization,
	}
	return plan, nil
}

type entryPointConfig struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Instruction string `yaml:"instruction"`
}

func parseEntryPoint(content string) (entryPointConfig, error) {
	var cfg entryPointConfig
	if err := yaml.Unmarshal([]byte(content), &cfg); err != nil {
		return cfg, fmt.Errorf("parse agent entry point: %w", err)
	}
	return cfg, nil
}

func convertSkills(items []aihubruntime.SkillSnapshotItem) []SkillBinding {
	out := make([]SkillBinding, 0, len(items))
	for _, item := range items {
		name := strings.TrimSpace(item.Name)
		if name == "" {
			continue
		}
		out = append(out, SkillBinding{
			Name: name, Version: item.Version, Revision: item.Revision,
			Object: item.Object, CommitSHA: item.CommitSHA, TreeSHA: item.TreeSHA,
			ManifestSHA256: item.ManifestSHA256, ViaSkillSet: item.ViaSkillSet,
			SHA256: item.SHA256, MD5: item.MD5, Size: item.Size, DownloadURL: item.DownloadURL,
			CachePath: item.CachePath, MountPath: item.MountPath,
			Source: firstNonEmpty(item.Source, sourceFromSkill(item)),
		})
	}
	return out
}

func sourceFromSkill(item aihubruntime.SkillSnapshotItem) string {
	if strings.Contains(item.Object, "builtin-skills") {
		return "builtin"
	}
	if item.DownloadURL != "" || item.CachePath != "" {
		return "catalog"
	}
	return ""
}

func convertTool(item aihubruntime.ToolSnapshotItem, auth AuthorizationSpec) ToolBinding {
	name := strings.TrimSpace(item.Name)
	approval := auth.ApprovalFor(name)
	runtimeType := stringFromMap(item.Runtime, "type")
	if runtimeType == "" {
		runtimeType = stringFromMap(item.Execution, "runtime")
	}
	if runtimeType == "" {
		runtimeType = stringFromMap(item.Execution, "type")
	}
	capabilities := stringSliceFromMap(item.Execution, "capabilities")
	if len(capabilities) == 0 && len(approval.Capabilities) > 0 {
		capabilities = append(capabilities, approval.Capabilities...)
	}
	approvalMode := firstNonEmpty(approval.ApprovalMode, stringFromMap(item.Runtime, "approvalMode"), stringFromMap(item.Metadata, "approvalMode"))
	runtimeName := firstNonEmpty(stringFromMap(item.Runtime, "name"), stringFromMap(item.Execution, "name"), name)
	return ToolBinding{
		Name: name, RuntimeName: runtimeName, Version: item.Version, Revision: item.Revision, Object: item.Object,
		Status: item.Status, RuntimeType: runtimeType, ApprovalMode: approvalMode,
		Required: approval.Required, Approved: approval.Approved,
		RequiresApproval: approvalMode == "per_run" && !approval.Approved,
		Capabilities:     capabilities, Permissions: approval.Permissions,
		Runtime: maps.Clone(item.Runtime), Execution: maps.Clone(item.Execution),
		InputSchema: maps.Clone(item.InputSchema), OutputSchema: maps.Clone(item.OutputSchema),
		TimeoutMillis: item.TimeoutMillis, Retry: maps.Clone(item.Retry), Metadata: maps.Clone(item.Metadata),
	}
}

func convertMCPServers(tools []ToolBinding) []MCPBinding {
	out := []MCPBinding{}
	for _, tool := range tools {
		if !strings.EqualFold(tool.RuntimeType, "mcp") {
			continue
		}
		out = append(out, MCPBinding{
			Name: tool.Name, Server: firstNonEmpty(stringFromMap(tool.Runtime, "server"), stringFromMap(tool.Execution, "server")),
			Transport:  firstNonEmpty(stringFromMap(tool.Runtime, "transport"), stringFromMap(tool.Execution, "transport")),
			ToolFilter: mcpToolFilter(tool),
			Runtime:    maps.Clone(tool.Runtime),
		})
	}
	return out
}

func mcpToolFilter(binding ToolBinding) []string {
	filter := stringSliceFromMap(binding.Runtime, "toolFilter")
	if len(filter) == 0 {
		filter = stringSliceFromMap(binding.Runtime, "tool_filter")
	}
	if len(filter) == 0 {
		if remoteName := firstNonEmpty(binding.RuntimeName, stringFromMap(binding.Runtime, "name"), stringFromMap(binding.Execution, "name")); remoteName != "" {
			filter = []string{remoteName}
		}
	}
	return filter
}

func parseAuthorization(raw map[string]any) AuthorizationSpec {
	spec := AuthorizationSpec{Raw: maps.Clone(raw)}
	if raw == nil {
		return spec
	}
	spec.PrincipalSubject = stringValue(raw["principalSubject"])
	spec.PrincipalPropagation = stringValue(raw["principalPropagation"])
	spec.IAMEnforcement = stringValue(raw["iamEnforcement"])
	spec.RequiresApproval = boolValue(raw["requiresApproval"])
	spec.ApprovalConfirmed = boolValue(raw["approvalConfirmed"])
	for _, value := range anySlice(raw["tools"]) {
		m, ok := value.(map[string]any)
		if !ok {
			continue
		}
		approval := ToolApproval{
			Tool: stringValue(m["tool"]), Version: stringValue(m["version"]),
			Required: boolValue(m["required"]), ApprovalMode: stringValue(m["approvalMode"]),
			Approved: boolValue(m["approved"]), Capabilities: stringSlice(m["capabilities"]),
			Permissions: permissions(m["permissions"]),
		}
		if approval.Tool != "" {
			spec.Tools = append(spec.Tools, approval)
		}
	}
	return spec
}

func (a AuthorizationSpec) ApprovalFor(name string) ToolApproval {
	for _, item := range a.Tools {
		if item.Tool == name {
			return item
		}
	}
	return ToolApproval{}
}

func permissions(raw any) []IAMPermission {
	out := []IAMPermission{}
	for _, value := range anySlice(raw) {
		m, ok := value.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, IAMPermission{
			ResourceType: stringValue(m["resourceType"]),
			Permission:   stringValue(m["permission"]),
			Enforcement:  stringValue(m["enforcement"]),
		})
	}
	return out
}

func isZeroModel(model aihubruntime.ModelSpec) bool {
	return model.Profile == "" && model.Model == "" && model.Provider == "" && model.BaseURL == "" && model.APIFormat == "" && len(model.Metadata) == 0
}

func stringFromMap(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	return stringValue(m[key])
}

func stringValue(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	case nil:
		return ""
	}
}

func boolValue(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func anySlice(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	default:
		return nil
	}
}

func stringSlice(value any) []string {
	out := []string{}
	for _, item := range anySlice(value) {
		text := stringValue(item)
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

func stringSliceFromMap(m map[string]interface{}, key string) []string {
	if m == nil {
		return nil
	}
	return stringSlice(m[key])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

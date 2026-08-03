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

// Package projectartifacttool provides an artifact-backed project workspace
// registry. It lets agents hand off durable artifacts through a project-level
// catalog instead of through chat history.
package projectartifacttool

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

const (
	SchemaVersion = "project-artifacts/v1"

	VisibilitySessionPrivate = "session_private"
	VisibilityProjectVisible = "project_visible"
	VisibilityProjectDefault = "project_default"
	VisibilitySystemHidden   = "system_hidden"
	VisibilityPublished      = "published"

	mountedProjectArtifact     = "mounted_project.json"
	projectRegistryNamePattern = "project__%s__artifacts.json"
	userArtifactPrefix         = "user:"
)

// NewToolset creates project workspace tools for agents.
func NewToolset() (tool.Toolset, error) {
	ts := &Toolset{}
	builders := []func() (tool.Tool, error){
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "project_workspace_create",
				Description: "Create or update an artifact-backed project workspace registry, then mount it into the current session. Use this before splitting books or running cross-session workflows.",
			}, ts.Create)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "project_workspace_mount",
				Description: "Mount an existing project workspace into the current session so subsequent tools can register and discover project artifacts without repeating project_id.",
			}, ts.Mount)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "project_workspace_get",
				Description: "Load the current or specified project workspace registry, including visible/default artifacts and hidden system artifacts when requested.",
			}, ts.Get)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "project_artifact_register",
				Description: "Register an existing artifact into the current project workspace with type, title, visibility, producer agent, mountability, and default target agents.",
			}, ts.Register)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "project_artifact_list",
				Description: "List project artifacts by type, visibility, producer agent, default target agent, or mountability. Use this to choose what a new session/agent should see.",
			}, ts.List)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "project_artifact_update",
				Description: "Update visibility, title, description, mountability, default target agents, or tags of a registered project artifact.",
			}, ts.Update)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "project_artifact_defaults",
				Description: "Return mountable project artifacts that should be visible by default for a target agent. Use this before starting a new session for another agent.",
			}, ts.Defaults)
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

// Toolset groups all project workspace tools.
type Toolset struct {
	tools []tool.Tool
}

func (t *Toolset) Name() string { return "ProjectArtifactToolset" }

func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) { return t.tools, nil }

type CreateArgs struct {
	ProjectID   string   `json:"project_id,omitempty" jsonschema:"Stable project id. If omitted, generated from display_name/name."`
	Name        string   `json:"name,omitempty" jsonschema:"Stable project name. Defaults to project_id."`
	DisplayName string   `json:"display_name,omitempty" jsonschema:"Human-readable Chinese display name shown in project UI."`
	Description string   `json:"description,omitempty" jsonschema:"Project description shown to users."`
	AppName     string   `json:"app_name,omitempty" jsonschema:"Optional app/agent namespace this project belongs to."`
	Tags        []string `json:"tags,omitempty" jsonschema:"Project tags."`
	Mount       *bool    `json:"mount,omitempty" jsonschema:"Whether to mount the project into the current session. Defaults to true."`
	Overwrite   bool     `json:"overwrite,omitempty" jsonschema:"If true, replace name/description/tags on existing registry while preserving artifacts."`
}

type ProjectIDArgs struct {
	ProjectID     string `json:"project_id,omitempty" jsonschema:"Project id. If omitted, uses mounted_project.json."`
	IncludeHidden bool   `json:"include_hidden,omitempty" jsonschema:"Whether to include system_hidden/session_private artifacts."`
}

type RegisterArgs struct {
	ProjectID        string            `json:"project_id,omitempty" jsonschema:"Project id. If omitted, uses mounted project; if no project is mounted, pass project_id explicitly."`
	ArtifactID       string            `json:"artifact_id,omitempty" jsonschema:"Stable id for this registry entry. Defaults from artifact_name/type."`
	ArtifactName     string            `json:"artifact_name" jsonschema:"Artifact filename/ref to register, for example user:book_xxx__manifest.json."`
	Type             string            `json:"type" jsonschema:"Artifact type, for example book.source, book.chapter_manifest, book.chapter, skill.version, skill.delta, batch.analysis, run.state."`
	Title            string            `json:"title,omitempty" jsonschema:"Human-readable title."`
	Description      string            `json:"description,omitempty" jsonschema:"What this artifact is for."`
	ProducerAgent    string            `json:"producer_agent,omitempty" jsonschema:"Agent/tool that produced the artifact, such as book_dissector or book_skill_runner."`
	Visibility       string            `json:"visibility,omitempty" jsonschema:"session_private, project_visible, project_default, system_hidden, or published. Defaults to project_visible."`
	Mountable        *bool             `json:"mountable,omitempty" jsonschema:"Whether this artifact can be mounted/selected by other sessions. Defaults based on visibility."`
	DefaultForAgents []string          `json:"default_for_agents,omitempty" jsonschema:"Agent ids that should see this artifact by default."`
	Tags             []string          `json:"tags,omitempty" jsonschema:"Tags for filtering."`
	BookID           string            `json:"book_id,omitempty"`
	RunID            string            `json:"run_id,omitempty"`
	BatchIndex       int               `json:"batch_index,omitempty"`
	StartChapter     int               `json:"start_chapter,omitempty"`
	EndChapter       int               `json:"end_chapter,omitempty"`
	SkillVersion     int               `json:"skill_version,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty" jsonschema:"Additional small string metadata."`
}

type ListArgs struct {
	ProjectID       string   `json:"project_id,omitempty"`
	Types           []string `json:"types,omitempty" jsonschema:"Optional artifact types to include."`
	Visibility      string   `json:"visibility,omitempty" jsonschema:"Optional visibility filter."`
	ProducerAgent   string   `json:"producer_agent,omitempty"`
	DefaultForAgent string   `json:"default_for_agent,omitempty" jsonschema:"Return artifacts default for this agent."`
	MountableOnly   bool     `json:"mountable_only,omitempty"`
	IncludeHidden   bool     `json:"include_hidden,omitempty"`
	Limit           int      `json:"limit,omitempty"`
}

type UpdateArgs struct {
	ProjectID        string   `json:"project_id,omitempty"`
	ArtifactID       string   `json:"artifact_id,omitempty" jsonschema:"Registry artifact id. Use either artifact_id or artifact_name."`
	ArtifactName     string   `json:"artifact_name,omitempty" jsonschema:"Artifact filename/ref. Use either artifact_id or artifact_name."`
	Title            *string  `json:"title,omitempty"`
	Description      *string  `json:"description,omitempty"`
	Visibility       *string  `json:"visibility,omitempty"`
	Mountable        *bool    `json:"mountable,omitempty"`
	DefaultForAgents []string `json:"default_for_agents,omitempty"`
	Tags             []string `json:"tags,omitempty"`
}

type DefaultsArgs struct {
	ProjectID      string `json:"project_id,omitempty"`
	AgentID        string `json:"agent_id" jsonschema:"Agent id, for example book_skill_runner."`
	IncludeVisible bool   `json:"include_visible,omitempty" jsonschema:"If true, include all project_default plus visible artifacts explicitly defaulted for the agent."`
	IncludeSystem  bool   `json:"include_system,omitempty" jsonschema:"If true, include system_hidden defaults such as run state pointers."`
	MountableOnly  *bool  `json:"mountable_only,omitempty" jsonschema:"Defaults to true."`
}

type ProjectRegistry struct {
	SchemaVersion string            `json:"schema_version"`
	ProjectID     string            `json:"project_id"`
	Name          string            `json:"name"`
	DisplayName   string            `json:"display_name,omitempty"`
	Description   string            `json:"description,omitempty"`
	AppName       string            `json:"app_name,omitempty"`
	Tags          []string          `json:"tags,omitempty"`
	ArtifactCount int               `json:"artifact_count"`
	Artifacts     []ProjectArtifact `json:"artifacts"`
	CreatedAt     string            `json:"created_at"`
	UpdatedAt     string            `json:"updated_at"`
}

type ProjectArtifact struct {
	ArtifactID       string            `json:"artifact_id"`
	ArtifactName     string            `json:"artifact_name"`
	Type             string            `json:"type"`
	Title            string            `json:"title,omitempty"`
	Description      string            `json:"description,omitempty"`
	ProducerAgent    string            `json:"producer_agent,omitempty"`
	Visibility       string            `json:"visibility"`
	Mountable        bool              `json:"mountable"`
	DefaultForAgents []string          `json:"default_for_agents,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	BookID           string            `json:"book_id,omitempty"`
	RunID            string            `json:"run_id,omitempty"`
	BatchIndex       int               `json:"batch_index,omitempty"`
	StartChapter     int               `json:"start_chapter,omitempty"`
	EndChapter       int               `json:"end_chapter,omitempty"`
	SkillVersion     int               `json:"skill_version,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	CreatedAt        string            `json:"created_at"`
	UpdatedAt        string            `json:"updated_at"`
}

type MountedProject struct {
	ProjectID        string `json:"project_id"`
	Name             string `json:"name,omitempty"`
	DisplayName      string `json:"display_name,omitempty"`
	RegistryArtifact string `json:"registry_artifact"`
	MountedAt        string `json:"mounted_at"`
}

type ProjectResult struct {
	Project          ProjectRegistry `json:"project"`
	RegistryArtifact string          `json:"registry_artifact"`
	Mounted          *MountedProject `json:"mounted,omitempty"`
	Instructions     []string        `json:"instructions,omitempty"`
}

type ListResult struct {
	ProjectID        string            `json:"project_id"`
	RegistryArtifact string            `json:"registry_artifact"`
	Count            int               `json:"count"`
	Artifacts        []ProjectArtifact `json:"artifacts"`
}

type RegisterResult struct {
	ProjectID        string          `json:"project_id"`
	RegistryArtifact string          `json:"registry_artifact"`
	Artifact         ProjectArtifact `json:"artifact"`
	Count            int             `json:"count"`
}

func (t *Toolset) Create(ctx tool.Context, args CreateArgs) (ProjectResult, error) {
	registry, created, err := EnsureProject(ctx, EnsureProjectRequest{
		ProjectID:   args.ProjectID,
		Name:        args.Name,
		DisplayName: args.DisplayName,
		Description: args.Description,
		AppName:     args.AppName,
		Tags:        args.Tags,
		Overwrite:   args.Overwrite,
	})
	if err != nil {
		return ProjectResult{}, err
	}
	var mounted *MountedProject
	if boolPtrDefault(args.Mount, true) {
		m, err := mountProject(ctx, registry)
		if err != nil {
			return ProjectResult{}, err
		}
		mounted = &m
	}
	verb := "已创建"
	if !created {
		verb = "已加载"
	}
	return ProjectResult{Project: registry, RegistryArtifact: registryArtifactName(registry.ProjectID), Mounted: mounted, Instructions: []string{
		fmt.Sprintf("项目 %s：%s。", verb, firstNonEmpty(registry.DisplayName, registry.Name, registry.ProjectID)),
		"后续产物请用 project_artifact_register 登记到项目；新 session 可先 project_workspace_mount。",
	}}, nil
}

func (t *Toolset) Mount(ctx tool.Context, args ProjectIDArgs) (ProjectResult, error) {
	projectID, err := ResolveProjectID(ctx, args.ProjectID)
	if err != nil {
		return ProjectResult{}, err
	}
	registry, _, err := EnsureProject(ctx, EnsureProjectRequest{ProjectID: projectID, AppName: ctx.AppName()})
	if err != nil {
		return ProjectResult{}, err
	}
	mounted, err := mountProject(ctx, registry)
	if err != nil {
		return ProjectResult{}, err
	}
	return ProjectResult{Project: filterProject(registry, args.IncludeHidden), RegistryArtifact: registryArtifactName(registry.ProjectID), Mounted: &mounted}, nil
}

func (t *Toolset) Get(ctx tool.Context, args ProjectIDArgs) (ProjectResult, error) {
	projectID, err := ResolveProjectID(ctx, args.ProjectID)
	if err != nil {
		return ProjectResult{}, err
	}
	registry, _, err := EnsureProject(ctx, EnsureProjectRequest{ProjectID: projectID, AppName: ctx.AppName()})
	if err != nil {
		return ProjectResult{}, err
	}
	mounted, err := LoadMountedProject(ctx)
	if err != nil || mounted == nil || normalizeProjectID(mounted.ProjectID) != registry.ProjectID {
		m, mountErr := mountProject(ctx, registry)
		if mountErr == nil {
			mounted = &m
		}
	}
	return ProjectResult{Project: filterProject(registry, args.IncludeHidden), RegistryArtifact: registryArtifactName(registry.ProjectID), Mounted: mounted}, nil
}

func (t *Toolset) Register(ctx tool.Context, args RegisterArgs) (RegisterResult, error) {
	artifact, registry, err := RegisterArtifact(ctx, RegisterArtifactRequest(args))
	if err != nil {
		return RegisterResult{}, err
	}
	return RegisterResult{ProjectID: registry.ProjectID, RegistryArtifact: registryArtifactName(registry.ProjectID), Artifact: artifact, Count: len(registry.Artifacts)}, nil
}

func (t *Toolset) List(ctx tool.Context, args ListArgs) (ListResult, error) {
	projectID, err := ResolveProjectID(ctx, args.ProjectID)
	if err != nil {
		return ListResult{}, err
	}
	registry, _, err := EnsureProject(ctx, EnsureProjectRequest{ProjectID: projectID, AppName: ctx.AppName()})
	if err != nil {
		return ListResult{}, err
	}
	arts := filterArtifacts(registry.Artifacts, args)
	return ListResult{ProjectID: registry.ProjectID, RegistryArtifact: registryArtifactName(registry.ProjectID), Count: len(arts), Artifacts: arts}, nil
}

func (t *Toolset) Update(ctx tool.Context, args UpdateArgs) (RegisterResult, error) {
	projectID, err := ResolveProjectID(ctx, args.ProjectID)
	if err != nil {
		return RegisterResult{}, err
	}
	registry, _, err := EnsureProject(ctx, EnsureProjectRequest{ProjectID: projectID, AppName: ctx.AppName()})
	if err != nil {
		return RegisterResult{}, err
	}
	idx := -1
	for i, art := range registry.Artifacts {
		if args.ArtifactID != "" && art.ArtifactID == sanitizeID(args.ArtifactID) {
			idx = i
			break
		}
		if args.ArtifactName != "" && art.ArtifactName == strings.TrimSpace(args.ArtifactName) {
			idx = i
			break
		}
	}
	if idx < 0 {
		return RegisterResult{}, fmt.Errorf("project artifact not found: artifact_id=%q artifact_name=%q", args.ArtifactID, args.ArtifactName)
	}
	now := nowRFC3339()
	art := registry.Artifacts[idx]
	if args.Title != nil {
		art.Title = strings.TrimSpace(*args.Title)
	}
	if args.Description != nil {
		art.Description = strings.TrimSpace(*args.Description)
	}
	if args.Visibility != nil {
		v := normalizeVisibility(*args.Visibility)
		if v == "" {
			return RegisterResult{}, fmt.Errorf("invalid visibility %q", *args.Visibility)
		}
		art.Visibility = v
	}
	if args.Mountable != nil {
		art.Mountable = *args.Mountable
	}
	if args.DefaultForAgents != nil {
		art.DefaultForAgents = normalizeStringList(args.DefaultForAgents)
	}
	if args.Tags != nil {
		art.Tags = normalizeStringList(args.Tags)
	}
	art.UpdatedAt = now
	registry.Artifacts[idx] = art
	registry.UpdatedAt = now
	if err := SaveProject(ctx, &registry); err != nil {
		return RegisterResult{}, err
	}
	return RegisterResult{ProjectID: registry.ProjectID, RegistryArtifact: registryArtifactName(registry.ProjectID), Artifact: art, Count: len(registry.Artifacts)}, nil
}

func (t *Toolset) Defaults(ctx tool.Context, args DefaultsArgs) (ListResult, error) {
	if strings.TrimSpace(args.AgentID) == "" {
		return ListResult{}, fmt.Errorf("agent_id is required")
	}
	projectID, err := ResolveProjectID(ctx, args.ProjectID)
	if err != nil {
		return ListResult{}, err
	}
	registry, _, err := EnsureProject(ctx, EnsureProjectRequest{ProjectID: projectID, AppName: ctx.AppName()})
	if err != nil {
		return ListResult{}, err
	}
	mountableOnly := boolPtrDefault(args.MountableOnly, true)
	agentID := strings.ToLower(strings.TrimSpace(args.AgentID))
	arts := []ProjectArtifact{}
	for _, art := range registry.Artifacts {
		if mountableOnly && !art.Mountable {
			continue
		}
		if !args.IncludeSystem && art.Visibility == VisibilitySystemHidden {
			continue
		}
		if isHiddenVisibility(art.Visibility) && art.Visibility != VisibilitySystemHidden {
			continue
		}
		if art.Visibility == VisibilityProjectDefault || containsFold(art.DefaultForAgents, agentID) {
			arts = append(arts, art)
			continue
		}
		if args.IncludeVisible && art.Visibility == VisibilityProjectVisible && containsFold(art.DefaultForAgents, agentID) {
			arts = append(arts, art)
		}
	}
	sortArtifacts(arts)
	return ListResult{ProjectID: registry.ProjectID, RegistryArtifact: registryArtifactName(registry.ProjectID), Count: len(arts), Artifacts: arts}, nil
}

// EnsureProjectRequest is used by other packages to create/update a registry.
type EnsureProjectRequest struct {
	ProjectID   string
	Name        string
	DisplayName string
	Description string
	AppName     string
	Tags        []string
	Overwrite   bool
}

// RegisterArtifactRequest is the programmatic version of RegisterArgs.
type RegisterArtifactRequest RegisterArgs

// EnsureProject loads or creates an artifact-backed project registry.
func EnsureProject(ctx tool.Context, req EnsureProjectRequest) (ProjectRegistry, bool, error) {
	projectID := normalizeProjectID(req.ProjectID)
	if projectID == "" {
		projectID = sanitizeID(firstNonEmpty(req.Name, req.DisplayName))
	}
	if projectID == "" {
		projectID = "project_" + time.Now().UTC().Format("20060102150405")
	}
	now := nowRFC3339()
	registry, err := LoadProject(ctx, projectID)
	if err == nil {
		changed := false
		if registry.ProjectID != projectID {
			registry.ProjectID = projectID
			changed = true
		}
		if req.Overwrite || registry.Name == "" {
			if v := strings.TrimSpace(req.Name); v != "" && registry.Name != v {
				registry.Name = v
				changed = true
			}
		}
		if req.Overwrite || registry.DisplayName == "" {
			if v := strings.TrimSpace(req.DisplayName); v != "" && registry.DisplayName != v {
				registry.DisplayName = v
				changed = true
			}
		}
		if req.Overwrite || registry.Description == "" {
			if v := strings.TrimSpace(req.Description); v != "" && registry.Description != v {
				registry.Description = v
				changed = true
			}
		}
		if req.AppName != "" && req.Overwrite && registry.AppName != req.AppName {
			registry.AppName = strings.TrimSpace(req.AppName)
			changed = true
		}
		if len(req.Tags) > 0 && (req.Overwrite || len(registry.Tags) == 0) {
			registry.Tags = normalizeStringList(req.Tags)
			changed = true
		}
		if changed {
			registry.UpdatedAt = now
			if err := SaveProject(ctx, &registry); err != nil {
				return ProjectRegistry{}, false, err
			}
		}
		return registry, false, nil
	}
	registry = ProjectRegistry{
		SchemaVersion: SchemaVersion,
		ProjectID:     projectID,
		Name:          firstNonEmpty(strings.TrimSpace(req.Name), projectID),
		DisplayName:   firstNonEmpty(strings.TrimSpace(req.DisplayName), strings.TrimSpace(req.Name), projectID),
		Description:   strings.TrimSpace(req.Description),
		AppName:       strings.TrimSpace(req.AppName),
		Tags:          normalizeStringList(req.Tags),
		Artifacts:     []ProjectArtifact{},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := SaveProject(ctx, &registry); err != nil {
		return ProjectRegistry{}, false, err
	}
	return registry, true, nil
}

// RegisterArtifact inserts or updates a project artifact registry entry.
func RegisterArtifact(ctx tool.Context, req RegisterArtifactRequest) (ProjectArtifact, ProjectRegistry, error) {
	projectID, err := ResolveProjectID(ctx, req.ProjectID)
	if err != nil {
		return ProjectArtifact{}, ProjectRegistry{}, err
	}
	registry, _, err := EnsureProject(ctx, EnsureProjectRequest{ProjectID: projectID, AppName: ctx.AppName()})
	if err != nil {
		return ProjectArtifact{}, ProjectRegistry{}, err
	}
	artifactName := strings.TrimSpace(req.ArtifactName)
	if artifactName == "" {
		return ProjectArtifact{}, ProjectRegistry{}, fmt.Errorf("artifact_name is required")
	}
	artifactType := strings.TrimSpace(req.Type)
	if artifactType == "" {
		return ProjectArtifact{}, ProjectRegistry{}, fmt.Errorf("type is required")
	}
	visibility := normalizeVisibility(req.Visibility)
	if visibility == "" {
		visibility = VisibilityProjectVisible
	}
	mountable := defaultMountable(visibility)
	if req.Mountable != nil {
		mountable = *req.Mountable
	}
	now := nowRFC3339()
	artifactID := sanitizeID(req.ArtifactID)
	if artifactID == "" {
		artifactID = artifactIDFrom(artifactName, artifactType)
	}
	art := ProjectArtifact{
		ArtifactID:       artifactID,
		ArtifactName:     artifactName,
		Type:             artifactType,
		Title:            strings.TrimSpace(req.Title),
		Description:      strings.TrimSpace(req.Description),
		ProducerAgent:    strings.TrimSpace(req.ProducerAgent),
		Visibility:       visibility,
		Mountable:        mountable,
		DefaultForAgents: normalizeStringList(req.DefaultForAgents),
		Tags:             normalizeStringList(req.Tags),
		BookID:           strings.TrimSpace(req.BookID),
		RunID:            strings.TrimSpace(req.RunID),
		BatchIndex:       req.BatchIndex,
		StartChapter:     req.StartChapter,
		EndChapter:       req.EndChapter,
		SkillVersion:     req.SkillVersion,
		Metadata:         normalizeMetadata(req.Metadata),
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if art.Title == "" {
		art.Title = defaultTitle(art)
	}
	updated := false
	for i := range registry.Artifacts {
		if registry.Artifacts[i].ArtifactID == art.ArtifactID || registry.Artifacts[i].ArtifactName == art.ArtifactName {
			existing := registry.Artifacts[i]
			if registry.Artifacts[i].CreatedAt != "" {
				art.CreatedAt = registry.Artifacts[i].CreatedAt
			}
			merged := mergeArtifact(existing, art)
			if projectArtifactSemanticallyEqual(existing, merged) {
				return existing, registry, nil
			}
			registry.Artifacts[i] = merged
			art = registry.Artifacts[i]
			updated = true
			break
		}
	}
	if !updated {
		registry.Artifacts = append(registry.Artifacts, art)
	}
	sortArtifacts(registry.Artifacts)
	registry.ArtifactCount = len(registry.Artifacts)
	registry.UpdatedAt = now
	if err := SaveProject(ctx, &registry); err != nil {
		return ProjectArtifact{}, ProjectRegistry{}, err
	}
	return art, registry, nil
}

func projectArtifactSemanticallyEqual(a, b ProjectArtifact) bool {
	a = comparableProjectArtifact(a)
	b = comparableProjectArtifact(b)
	return reflect.DeepEqual(a, b)
}

func comparableProjectArtifact(a ProjectArtifact) ProjectArtifact {
	a.CreatedAt = ""
	a.UpdatedAt = ""
	if len(a.DefaultForAgents) == 0 {
		a.DefaultForAgents = nil
	}
	if len(a.Tags) == 0 {
		a.Tags = nil
	}
	if len(a.Metadata) == 0 {
		a.Metadata = nil
	}
	return a
}

// RemoveArtifacts removes registry entries matched by predicate and saves the registry.
// It only updates the project registry; deleting the underlying artifact file must be
// handled by the platform artifact service/admin API.
func RemoveArtifacts(ctx tool.Context, projectID string, predicate func(ProjectArtifact) bool) (ProjectRegistry, int, error) {
	projectID = sanitizeID(projectID)
	if projectID == "" {
		var err error
		projectID, err = ResolveProjectID(ctx, "")
		if err != nil {
			return ProjectRegistry{}, 0, err
		}
	}
	registry, err := LoadProject(ctx, projectID)
	if err != nil {
		return ProjectRegistry{}, 0, err
	}
	if predicate == nil {
		return registry, 0, nil
	}
	kept := make([]ProjectArtifact, 0, len(registry.Artifacts))
	removed := 0
	for _, art := range registry.Artifacts {
		if predicate(art) {
			removed++
			continue
		}
		kept = append(kept, art)
	}
	if removed == 0 {
		return registry, 0, nil
	}
	registry.Artifacts = kept
	sortArtifacts(registry.Artifacts)
	registry.ArtifactCount = len(registry.Artifacts)
	registry.UpdatedAt = nowRFC3339()
	if err := SaveProject(ctx, &registry); err != nil {
		return ProjectRegistry{}, 0, err
	}
	return registry, removed, nil
}

// LoadProject loads a project registry artifact.
func LoadProject(ctx tool.Context, projectID string) (ProjectRegistry, error) {
	projectID = normalizeProjectID(projectID)
	if projectID == "" {
		return ProjectRegistry{}, fmt.Errorf("project_id is required")
	}
	text, err := loadArtifactText(ctx, registryArtifactName(projectID))
	if err != nil {
		return ProjectRegistry{}, err
	}
	var registry ProjectRegistry
	if err := json.Unmarshal([]byte(text), &registry); err != nil {
		return ProjectRegistry{}, fmt.Errorf("decode project registry %q: %w", projectID, err)
	}
	registry.ProjectID = projectID
	if registry.SchemaVersion == "" {
		registry.SchemaVersion = SchemaVersion
	}
	registry.ArtifactCount = len(registry.Artifacts)
	return registry, nil
}

// SaveProject writes a project registry artifact.
func SaveProject(ctx tool.Context, registry *ProjectRegistry) error {
	if registry == nil {
		return fmt.Errorf("project registry is nil")
	}
	registry.ProjectID = normalizeProjectID(registry.ProjectID)
	if registry.ProjectID == "" {
		return fmt.Errorf("project_id is required")
	}
	registry.SchemaVersion = SchemaVersion
	registry.ArtifactCount = len(registry.Artifacts)
	if registry.CreatedAt == "" {
		registry.CreatedAt = nowRFC3339()
	}
	registry.UpdatedAt = firstNonEmpty(registry.UpdatedAt, nowRFC3339())
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return saveTextArtifact(ctx, registryArtifactName(registry.ProjectID), string(data), "application/json; charset=utf-8")
}

// ResolveProjectID resolves an explicit id or mounted_project.json.
func ResolveProjectID(ctx tool.Context, explicit string) (string, error) {
	projectID := normalizeProjectID(explicit)
	if projectID != "" {
		return projectID, nil
	}
	// The frontend project selector is the canonical runtime boundary. Prefer
	// current session state over mounted_project.json so switching projects in the
	// UI cannot reuse a stale mounted pointer from an older session turn.
	if stateProjectID := projectIDFromSessionState(ctx); stateProjectID != "" {
		return stateProjectID, nil
	}
	mounted, err := LoadMountedProject(ctx)
	if err == nil && mounted != nil {
		projectID = normalizeProjectID(mounted.ProjectID)
		if projectID == "" {
			return "", fmt.Errorf("mounted_project.json has empty project_id")
		}
		return projectID, nil
	}
	return "", fmt.Errorf("current workspace is not selected; choose a project in the top project selector: %w", err)
}

func projectIDFromSessionState(ctx tool.Context) string {
	if ctx == nil {
		return ""
	}
	if state := ctx.State(); state != nil {
		if projectID := projectIDFromStateGetter(state.Get); projectID != "" {
			return projectID
		}
	}
	if state := ctx.ReadonlyState(); state != nil {
		if projectID := projectIDFromStateGetter(state.Get); projectID != "" {
			return projectID
		}
	}
	return ""
}

func projectIDFromStateGetter(get func(string) (any, error)) string {
	if get == nil {
		return ""
	}
	if v, err := get("project_id"); err == nil {
		if projectID := normalizeProjectID(stringValue(v)); projectID != "" {
			return projectID
		}
	}
	if v, err := get("__project_context__"); err == nil {
		if projectID := projectIDFromContextValue(v); projectID != "" {
			return projectID
		}
	}
	return ""
}

func projectIDFromContextValue(v any) string {
	switch m := v.(type) {
	case map[string]any:
		return normalizeProjectID(stringValue(m["project_id"]))
	case map[string]string:
		return normalizeProjectID(m["project_id"])
	default:
		return ""
	}
}

func stringValue(v any) string {
	switch s := v.(type) {
	case string:
		return s
	default:
		return ""
	}
}

// LoadMountedProject loads the current session's project pointer.
func LoadMountedProject(ctx tool.Context) (*MountedProject, error) {
	text, err := loadArtifactText(ctx, mountedProjectArtifact)
	if err != nil {
		return nil, err
	}
	var mounted MountedProject
	if err := json.Unmarshal([]byte(text), &mounted); err != nil {
		return nil, fmt.Errorf("decode mounted_project.json: %w", err)
	}
	return &mounted, nil
}

// MountProject saves the mounted_project.json pointer for a registry.
func MountProject(ctx tool.Context, registry ProjectRegistry) (MountedProject, error) {
	return mountProject(ctx, registry)
}

func mountProject(ctx tool.Context, registry ProjectRegistry) (MountedProject, error) {
	mounted := MountedProject{
		ProjectID:        registry.ProjectID,
		Name:             registry.Name,
		DisplayName:      registry.DisplayName,
		RegistryArtifact: registryArtifactName(registry.ProjectID),
		MountedAt:        nowRFC3339(),
	}
	data, err := json.MarshalIndent(mounted, "", "  ")
	if err != nil {
		return MountedProject{}, err
	}
	if err := saveTextArtifact(ctx, mountedProjectArtifact, string(data), "application/json; charset=utf-8"); err != nil {
		return MountedProject{}, fmt.Errorf("save mounted project pointer: %w", err)
	}
	mountProjectState(ctx, registry.ProjectID)
	return mounted, nil
}

func mountProjectState(ctx tool.Context, projectID string) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return
	}
	state := ctx.State()
	if state == nil {
		return
	}
	_ = state.Set("project_id", projectID)
	_ = state.Set("projectId", projectID)
}

func filterProject(registry ProjectRegistry, includeHidden bool) ProjectRegistry {
	if includeHidden {
		return registry
	}
	registry.Artifacts = filterArtifacts(registry.Artifacts, ListArgs{})
	registry.ArtifactCount = len(registry.Artifacts)
	return registry
}

func filterArtifacts(in []ProjectArtifact, args ListArgs) []ProjectArtifact {
	typeSet := map[string]bool{}
	for _, typ := range args.Types {
		if typ = strings.TrimSpace(typ); typ != "" {
			typeSet[typ] = true
		}
	}
	visibility := normalizeVisibility(args.Visibility)
	producer := strings.TrimSpace(args.ProducerAgent)
	defaultAgent := strings.ToLower(strings.TrimSpace(args.DefaultForAgent))
	out := []ProjectArtifact{}
	for _, art := range in {
		if len(typeSet) > 0 && !typeSet[art.Type] {
			continue
		}
		if visibility != "" && art.Visibility != visibility {
			continue
		}
		if producer != "" && art.ProducerAgent != producer {
			continue
		}
		if defaultAgent != "" && !containsFold(art.DefaultForAgents, defaultAgent) && art.Visibility != VisibilityProjectDefault {
			continue
		}
		if args.MountableOnly && !art.Mountable {
			continue
		}
		if !args.IncludeHidden && isHiddenVisibility(art.Visibility) {
			continue
		}
		out = append(out, art)
	}
	sortArtifacts(out)
	limit := args.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func mergeArtifact(old, next ProjectArtifact) ProjectArtifact {
	if next.ArtifactName == "" {
		next.ArtifactName = old.ArtifactName
	}
	if next.Type == "" {
		next.Type = old.Type
	}
	if next.Title == "" {
		next.Title = old.Title
	}
	if next.Description == "" {
		next.Description = old.Description
	}
	if next.ProducerAgent == "" {
		next.ProducerAgent = old.ProducerAgent
	}
	if next.Visibility == "" {
		next.Visibility = old.Visibility
	}
	if next.DefaultForAgents == nil {
		next.DefaultForAgents = old.DefaultForAgents
	}
	if next.Tags == nil {
		next.Tags = old.Tags
	}
	if next.BookID == "" {
		next.BookID = old.BookID
	}
	if next.RunID == "" {
		next.RunID = old.RunID
	}
	if next.BatchIndex == 0 {
		next.BatchIndex = old.BatchIndex
	}
	if next.StartChapter == 0 {
		next.StartChapter = old.StartChapter
	}
	if next.EndChapter == 0 {
		next.EndChapter = old.EndChapter
	}
	if next.SkillVersion == 0 {
		next.SkillVersion = old.SkillVersion
	}
	if next.Metadata == nil {
		next.Metadata = old.Metadata
	}
	if old.CreatedAt != "" {
		next.CreatedAt = old.CreatedAt
	}
	return next
}

func defaultMountable(visibility string) bool {
	switch visibility {
	case VisibilitySystemHidden, VisibilitySessionPrivate:
		return false
	default:
		return true
	}
}

func normalizeVisibility(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "visible", "project":
		return VisibilityProjectVisible
	case VisibilitySessionPrivate, "private", "session":
		return VisibilitySessionPrivate
	case VisibilityProjectVisible:
		return VisibilityProjectVisible
	case VisibilityProjectDefault, "default":
		return VisibilityProjectDefault
	case VisibilitySystemHidden, "hidden", "system":
		return VisibilitySystemHidden
	case VisibilityPublished, "publish":
		return VisibilityPublished
	default:
		return ""
	}
}

func isHiddenVisibility(v string) bool {
	return v == VisibilitySystemHidden || v == VisibilitySessionPrivate
}

func sortArtifacts(arts []ProjectArtifact) {
	sort.SliceStable(arts, func(i, j int) bool {
		if arts[i].Visibility != arts[j].Visibility {
			return visibilityRank(arts[i].Visibility) < visibilityRank(arts[j].Visibility)
		}
		if arts[i].Type != arts[j].Type {
			return arts[i].Type < arts[j].Type
		}
		return arts[i].ArtifactName < arts[j].ArtifactName
	})
}

func visibilityRank(v string) int {
	switch v {
	case VisibilityProjectDefault:
		return 0
	case VisibilityProjectVisible:
		return 1
	case VisibilityPublished:
		return 2
	case VisibilitySessionPrivate:
		return 3
	case VisibilitySystemHidden:
		return 4
	default:
		return 9
	}
}

func defaultTitle(art ProjectArtifact) string {
	switch art.Type {
	case "book.source":
		return "原始书籍正文"
	case "book.chapter_manifest":
		return "章节索引"
	case "book.chapter":
		if art.StartChapter > 0 {
			return fmt.Sprintf("第 %d 章", art.StartChapter)
		}
	case "run.state":
		return "长任务状态"
	case "run.latest":
		return "最新长任务指针"
	case "batch.analysis":
		return fmt.Sprintf("第 %d-%d 章批次分析", art.StartChapter, art.EndChapter)
	case "skill.delta":
		return fmt.Sprintf("第 %d-%d 章 Skill 增量", art.StartChapter, art.EndChapter)
	case "skill.version":
		if art.SkillVersion > 0 {
			return fmt.Sprintf("Skill v%03d", art.SkillVersion)
		}
	case "skill.evaluation":
		if art.SkillVersion > 0 {
			return fmt.Sprintf("Skill v%03d 质量检查", art.SkillVersion)
		}
	}
	return art.ArtifactName
}

func artifactIDFrom(name, typ string) string {
	base := strings.TrimPrefix(strings.TrimSpace(name), userArtifactPrefix)
	base = strings.TrimSuffix(base, ".json")
	base = strings.TrimSuffix(base, ".md")
	base = strings.TrimSuffix(base, ".txt")
	base = typ + "__" + base
	out := sanitizeID(base)
	if out == "" {
		out = uuid.NewString()
	}
	return out
}

func registryArtifactName(projectID string) string {
	return userScopedArtifactName(fmt.Sprintf(projectRegistryNamePattern, sanitizeID(projectID)))
}

func userScopedArtifactName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, userArtifactPrefix) {
		return name
	}
	return userArtifactPrefix + name
}

func loadArtifactText(ctx tool.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("artifact name is required")
	}
	resp, err := ctx.Artifacts().Load(ctx, name)
	if err != nil {
		return "", fmt.Errorf("load artifact %q: %w", name, err)
	}
	if resp == nil || resp.Part == nil {
		return "", fmt.Errorf("artifact %q is empty", name)
	}
	if resp.Part.Text != "" {
		return normalizeNewlines(resp.Part.Text), nil
	}
	if resp.Part.InlineData == nil {
		return "", fmt.Errorf("artifact %q has no text or inline data", name)
	}
	return normalizeNewlines(string(resp.Part.InlineData.Data)), nil
}

func saveTextArtifact(ctx tool.Context, name, content, mimeType string) error {
	_, err := ctx.Artifacts().Save(ctx, name, &genai.Part{InlineData: &genai.Blob{MIMEType: mimeType, Data: []byte(content)}})
	return err
}

func normalizeProjectID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	// Preserve the platform project id exactly enough for database/upload comparisons.
	// Artifact filenames still use sanitizeID via registryArtifactName.
	s = strings.Trim(s, " \t\r\n")
	if utf8.RuneCountInString(s) > 128 {
		r := []rune(s)
		s = string(r[:128])
	}
	return s
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok || unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if utf8.RuneCountInString(out) > 72 {
		r := []rune(out)
		out = string(r[:72])
	}
	return out
}

func normalizeStringList(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		key := strings.ToLower(v)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, v)
	}
	return out
}

func normalizeMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range in {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func containsFold(values []string, needle string) bool {
	needle = strings.ToLower(strings.TrimSpace(needle))
	if needle == "" {
		return false
	}
	for _, v := range values {
		if strings.ToLower(strings.TrimSpace(v)) == needle {
			return true
		}
	}
	return false
}

func boolPtrDefault(v *bool, fallback bool) bool {
	if v == nil {
		return fallback
	}
	return *v
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

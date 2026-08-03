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

package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gorilla/mux"
	"google.golang.org/genai"

	"google.golang.org/adk/artifact"
	"google.golang.org/adk/internal/platform/auth"
	"google.golang.org/adk/internal/platform/projects"
)

const (
	projectRegistrySchemaVersion = "project-artifacts/v1"
	projectRegistryNameFormat    = "user:project__%s__artifacts.json"
	projectArtifactSessionID     = "project_admin"

	visibilitySessionPrivate = "session_private"
	visibilityProjectVisible = "project_visible"
	visibilityProjectDefault = "project_default"
	visibilitySystemHidden   = "system_hidden"
	visibilityPublished      = "published"
)

type platformProjectRegistry struct {
	SchemaVersion string                    `json:"schema_version"`
	ProjectID     string                    `json:"project_id"`
	Name          string                    `json:"name"`
	DisplayName   string                    `json:"display_name,omitempty"`
	Description   string                    `json:"description,omitempty"`
	AppName       string                    `json:"app_name,omitempty"`
	Tags          []string                  `json:"tags,omitempty"`
	ArtifactCount int                       `json:"artifact_count"`
	Artifacts     []platformProjectArtifact `json:"artifacts"`
	CreatedAt     string                    `json:"created_at"`
	UpdatedAt     string                    `json:"updated_at"`
}

type platformProjectArtifact struct {
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

type projectArtifactScope struct {
	ProjectID      string            `json:"project_id"`
	RegistryName   string            `json:"registry_artifact"`
	AppName        string            `json:"app_name"`
	UserID         string            `json:"user_id"`
	PlatformRecord *projects.Project `json:"platform_project,omitempty"`
}

type projectArtifactListResponse struct {
	Scope     projectArtifactScope      `json:"scope"`
	Project   platformProjectRegistry   `json:"project"`
	Count     int                       `json:"count"`
	Artifacts []platformProjectArtifact `json:"artifacts"`
}

type projectArtifactContentResponse struct {
	Scope        projectArtifactScope    `json:"scope"`
	Artifact     platformProjectArtifact `json:"artifact"`
	ArtifactName string                  `json:"artifact_name"`
	MimeType     string                  `json:"mime_type"`
	Text         string                  `json:"text,omitempty"`
	SizeBytes    int                     `json:"size_bytes"`
}

type projectWorkspaceListResponse struct {
	Scope struct {
		AppName string `json:"app_name"`
		UserID  string `json:"user_id"`
	} `json:"scope"`
	Count     int                       `json:"count"`
	Projects  []platformProjectRegistry `json:"projects"`
	Artifacts []string                  `json:"registry_artifacts"`
}

func (c *PlatformProjectsAPIController) ListProjectWorkspacesHandler(rw http.ResponseWriter, req *http.Request) {
	if c.artifactService == nil {
		http.Error(rw, "artifact service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	q := req.URL.Query()
	appName := firstNonEmptyProjectArtifact(q.Get("app_name"), "book_dissector")
	userID := firstNonEmptyProjectArtifact(q.Get("user_id"), p.UserID)
	resp, err := c.artifactService.List(req.Context(), &artifact.ListRequest{AppName: appName, UserID: userID, SessionID: projectArtifactSessionID})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	out := projectWorkspaceListResponse{}
	out.Scope.AppName = appName
	out.Scope.UserID = userID
	for _, name := range resp.FileNames {
		if !isProjectRegistryArtifact(name) {
			continue
		}
		registry, err := c.loadProjectRegistryByName(req, projectArtifactScope{RegistryName: name, AppName: appName, UserID: userID})
		if err != nil {
			continue
		}
		out.Artifacts = append(out.Artifacts, name)
		out.Projects = append(out.Projects, registry)
	}
	sort.SliceStable(out.Projects, func(i, j int) bool { return out.Projects[i].UpdatedAt > out.Projects[j].UpdatedAt })
	out.Count = len(out.Projects)
	EncodeJSONResponse(out, http.StatusOK, rw)
}

func (c *PlatformProjectsAPIController) ListProjectArtifactsHandler(rw http.ResponseWriter, req *http.Request) {
	scope, registry, ok := c.projectArtifactRegistryFromRequestAllowEmpty(rw, req)
	if !ok {
		return
	}
	arts := filterPlatformProjectArtifacts(registry.Artifacts, req)
	EncodeJSONResponse(projectArtifactListResponse{Scope: scope, Project: registry, Count: len(arts), Artifacts: arts}, http.StatusOK, rw)
}

func (c *PlatformProjectsAPIController) GetProjectArtifactHandler(rw http.ResponseWriter, req *http.Request) {
	scope, registry, ok := c.projectArtifactRegistryFromRequest(rw, req)
	if !ok {
		return
	}
	art, ok := findPlatformProjectArtifact(registry.Artifacts, mux.Vars(req)["artifact_id"])
	if !ok {
		http.Error(rw, "project artifact not found", http.StatusNotFound)
		return
	}
	EncodeJSONResponse(map[string]any{"scope": scope, "artifact": art}, http.StatusOK, rw)
}

func (c *PlatformProjectsAPIController) UpdateProjectArtifactHandler(rw http.ResponseWriter, req *http.Request) {
	scope, registry, ok := c.projectArtifactRegistryFromRequest(rw, req)
	if !ok {
		return
	}
	idx := findPlatformProjectArtifactIndex(registry.Artifacts, mux.Vars(req)["artifact_id"])
	if idx < 0 {
		http.Error(rw, "project artifact not found", http.StatusNotFound)
		return
	}
	var body struct {
		Title            *string  `json:"title"`
		Description      *string  `json:"description"`
		Visibility       *string  `json:"visibility"`
		Mountable        *bool    `json:"mountable"`
		DefaultForAgents []string `json:"default_for_agents"`
		Tags             []string `json:"tags"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	art := registry.Artifacts[idx]
	if body.Title != nil {
		art.Title = strings.TrimSpace(*body.Title)
	}
	if body.Description != nil {
		art.Description = strings.TrimSpace(*body.Description)
	}
	if body.Visibility != nil {
		visibility := normalizeProjectArtifactVisibility(*body.Visibility)
		if visibility == "" {
			http.Error(rw, "invalid visibility", http.StatusBadRequest)
			return
		}
		art.Visibility = visibility
	}
	if body.Mountable != nil {
		art.Mountable = *body.Mountable
	}
	if body.DefaultForAgents != nil {
		art.DefaultForAgents = normalizeProjectArtifactStringList(body.DefaultForAgents)
	}
	if body.Tags != nil {
		art.Tags = normalizeProjectArtifactStringList(body.Tags)
	}
	art.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	registry.Artifacts[idx] = art
	registry.UpdatedAt = art.UpdatedAt
	registry.ArtifactCount = len(registry.Artifacts)
	if err := c.saveProjectRegistry(req, scope, registry); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(map[string]any{"scope": scope, "artifact": art, "project": registry}, http.StatusOK, rw)
}

func (c *PlatformProjectsAPIController) DeleteProjectArtifactHandler(rw http.ResponseWriter, req *http.Request) {
	scope, registry, ok := c.projectArtifactRegistryFromRequest(rw, req)
	if !ok {
		return
	}
	idx := findPlatformProjectArtifactIndex(registry.Artifacts, mux.Vars(req)["artifact_id"])
	if idx < 0 {
		http.Error(rw, "project artifact not found", http.StatusNotFound)
		return
	}
	art := registry.Artifacts[idx]
	registry.Artifacts = append(registry.Artifacts[:idx], registry.Artifacts[idx+1:]...)
	registry.ArtifactCount = len(registry.Artifacts)
	registry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := c.saveProjectRegistry(req, scope, registry); err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	deleteFile, _ := strconv.ParseBool(req.URL.Query().Get("delete_file"))
	if deleteFile && art.ArtifactName != "" {
		if err := c.artifactService.Delete(req.Context(), &artifact.DeleteRequest{AppName: scope.AppName, UserID: scope.UserID, SessionID: projectArtifactSessionID, FileName: art.ArtifactName}); err != nil {
			http.Error(rw, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	EncodeJSONResponse(map[string]any{"ok": true, "deleted_artifact": art, "delete_file": deleteFile, "project": registry}, http.StatusOK, rw)
}

func (c *PlatformProjectsAPIController) LoadProjectArtifactContentHandler(rw http.ResponseWriter, req *http.Request) {
	scope, registry, ok := c.projectArtifactRegistryFromRequest(rw, req)
	if !ok {
		return
	}
	art, ok := findPlatformProjectArtifact(registry.Artifacts, mux.Vars(req)["artifact_id"])
	if !ok {
		http.Error(rw, "project artifact not found", http.StatusNotFound)
		return
	}
	if art.ArtifactName == "" {
		http.Error(rw, "artifact_name is empty", http.StatusBadRequest)
		return
	}
	resp, err := c.artifactService.Load(req.Context(), &artifact.LoadRequest{AppName: scope.AppName, UserID: scope.UserID, SessionID: projectArtifactSessionID, FileName: art.ArtifactName})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	text, mimeType, size := partToText(resp.Part)
	if req.URL.Query().Get("raw") == "true" {
		rw.Header().Set("Content-Type", firstNonEmptyProjectArtifact(mimeType, "text/plain; charset=utf-8"))
		_, _ = rw.Write([]byte(text))
		return
	}
	EncodeJSONResponse(projectArtifactContentResponse{Scope: scope, Artifact: art, ArtifactName: art.ArtifactName, MimeType: mimeType, Text: text, SizeBytes: size}, http.StatusOK, rw)
}

func (c *PlatformProjectsAPIController) projectArtifactRegistryFromRequest(rw http.ResponseWriter, req *http.Request) (projectArtifactScope, platformProjectRegistry, bool) {
	if c.artifactService == nil {
		http.Error(rw, "artifact service is not enabled", http.StatusNotImplemented)
		return projectArtifactScope{}, platformProjectRegistry{}, false
	}
	scope, err := c.projectArtifactScopeFromRequest(req)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return projectArtifactScope{}, platformProjectRegistry{}, false
	}
	registry, err := c.loadProjectRegistryByName(req, scope)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusNotFound)
		return projectArtifactScope{}, platformProjectRegistry{}, false
	}
	return scope, registry, true
}

func (c *PlatformProjectsAPIController) projectArtifactRegistryFromRequestAllowEmpty(rw http.ResponseWriter, req *http.Request) (projectArtifactScope, platformProjectRegistry, bool) {
	if c.artifactService == nil {
		http.Error(rw, "artifact service is not enabled", http.StatusNotImplemented)
		return projectArtifactScope{}, platformProjectRegistry{}, false
	}
	scope, err := c.projectArtifactScopeFromRequest(req)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return projectArtifactScope{}, platformProjectRegistry{}, false
	}
	registry, err := c.loadProjectRegistryByName(req, scope)
	if err == nil {
		return scope, registry, true
	}
	registry = emptyPlatformProjectRegistry(scope)
	return scope, registry, true
}

func emptyPlatformProjectRegistry(scope projectArtifactScope) platformProjectRegistry {
	now := time.Now().UTC().Format(time.RFC3339)
	registry := platformProjectRegistry{
		SchemaVersion: projectRegistrySchemaVersion,
		ProjectID:     scope.ProjectID,
		Name:          scope.ProjectID,
		AppName:       scope.AppName,
		ArtifactCount: 0,
		Artifacts:     []platformProjectArtifact{},
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if scope.PlatformRecord != nil {
		registry.Name = firstNonEmptyProjectArtifact(scope.PlatformRecord.Name, scope.ProjectID)
		registry.DisplayName = scope.PlatformRecord.DisplayName
		registry.Description = scope.PlatformRecord.Description
		registry.AppName = firstNonEmptyProjectArtifact(scope.PlatformRecord.AppName, scope.AppName)
	}
	return registry
}

func (c *PlatformProjectsAPIController) projectArtifactScopeFromRequest(req *http.Request) (projectArtifactScope, error) {
	p := auth.FromContext(req.Context())
	projectID := sanitizeProjectArtifactID(mux.Vars(req)["project_id"])
	if projectID == "" {
		return projectArtifactScope{}, fmt.Errorf("project_id is required")
	}
	scope := projectArtifactScope{ProjectID: projectID, RegistryName: projectRegistryArtifactName(projectID)}
	var project *projects.Project
	if c.service != nil {
		loaded, err := c.service.Get(req.Context(), p.TenantID, mux.Vars(req)["project_id"])
		if err == nil {
			project = loaded
			scope.PlatformRecord = loaded
		}
	}
	q := req.URL.Query()
	scope.AppName = firstNonEmptyProjectArtifact(q.Get("app_name"), projectString(project, "app"), "book_dissector")
	scope.UserID = firstNonEmptyProjectArtifact(q.Get("user_id"), projectString(project, "owner"), p.UserID)
	if q.Get("registry_artifact") != "" {
		scope.RegistryName = q.Get("registry_artifact")
	}
	return scope, nil
}

func (c *PlatformProjectsAPIController) loadProjectRegistryByName(req *http.Request, scope projectArtifactScope) (platformProjectRegistry, error) {
	resp, err := c.artifactService.Load(req.Context(), &artifact.LoadRequest{AppName: scope.AppName, UserID: scope.UserID, SessionID: projectArtifactSessionID, FileName: scope.RegistryName})
	if err != nil {
		return platformProjectRegistry{}, err
	}
	text, _, _ := partToText(resp.Part)
	var registry platformProjectRegistry
	if err := json.Unmarshal([]byte(text), &registry); err != nil {
		return platformProjectRegistry{}, err
	}
	if registry.ProjectID == "" {
		registry.ProjectID = scope.ProjectID
	}
	if registry.SchemaVersion == "" {
		registry.SchemaVersion = projectRegistrySchemaVersion
	}
	registry.ArtifactCount = len(registry.Artifacts)
	return registry, nil
}

func (c *PlatformProjectsAPIController) saveProjectRegistry(req *http.Request, scope projectArtifactScope, registry platformProjectRegistry) error {
	registry.SchemaVersion = projectRegistrySchemaVersion
	registry.ProjectID = firstNonEmptyProjectArtifact(registry.ProjectID, scope.ProjectID)
	registry.ArtifactCount = len(registry.Artifacts)
	if registry.CreatedAt == "" {
		registry.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	registry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	_, err = c.artifactService.Save(req.Context(), &artifact.SaveRequest{AppName: scope.AppName, UserID: scope.UserID, SessionID: projectArtifactSessionID, FileName: scope.RegistryName, Part: &genai.Part{InlineData: &genai.Blob{MIMEType: "application/json; charset=utf-8", Data: data}}})
	return err
}

func filterPlatformProjectArtifacts(in []platformProjectArtifact, req *http.Request) []platformProjectArtifact {
	q := req.URL.Query()
	includeHidden, _ := strconv.ParseBool(q.Get("include_hidden"))
	mountableOnly, _ := strconv.ParseBool(q.Get("mountable_only"))
	visibility := normalizeProjectArtifactVisibility(q.Get("visibility"))
	typeFilter := strings.TrimSpace(q.Get("type"))
	producer := strings.TrimSpace(q.Get("producer_agent"))
	defaultFor := strings.ToLower(strings.TrimSpace(q.Get("default_for_agent")))
	keyword := strings.ToLower(strings.TrimSpace(q.Get("q")))
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	out := []platformProjectArtifact{}
	for _, art := range in {
		if !includeHidden && isHiddenProjectArtifactVisibility(art.Visibility) {
			continue
		}
		if visibility != "" && art.Visibility != visibility {
			continue
		}
		if typeFilter != "" && art.Type != typeFilter {
			continue
		}
		if producer != "" && art.ProducerAgent != producer {
			continue
		}
		if defaultFor != "" && !containsProjectArtifactFold(art.DefaultForAgents, defaultFor) && art.Visibility != visibilityProjectDefault {
			continue
		}
		if mountableOnly && !art.Mountable {
			continue
		}
		if keyword != "" && !strings.Contains(strings.ToLower(strings.Join([]string{art.ArtifactID, art.ArtifactName, art.Type, art.Title, art.Description, art.ProducerAgent, strings.Join(art.Tags, " ")}, " ")), keyword) {
			continue
		}
		out = append(out, art)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Visibility != out[j].Visibility {
			return projectArtifactVisibilityRank(out[i].Visibility) < projectArtifactVisibilityRank(out[j].Visibility)
		}
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].ArtifactName < out[j].ArtifactName
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func findPlatformProjectArtifact(in []platformProjectArtifact, id string) (platformProjectArtifact, bool) {
	idx := findPlatformProjectArtifactIndex(in, id)
	if idx < 0 {
		return platformProjectArtifact{}, false
	}
	return in[idx], true
}

func findPlatformProjectArtifactIndex(in []platformProjectArtifact, id string) int {
	id = strings.TrimSpace(id)
	for i, art := range in {
		if art.ArtifactID == id || art.ArtifactName == id {
			return i
		}
	}
	return -1
}

func partToText(part *genai.Part) (text string, mimeType string, size int) {
	if part == nil {
		return "", "text/plain; charset=utf-8", 0
	}
	if part.Text != "" {
		return part.Text, "text/plain; charset=utf-8", len([]byte(part.Text))
	}
	if part.InlineData != nil {
		mimeType = firstNonEmptyProjectArtifact(part.InlineData.MIMEType, "application/octet-stream")
		return string(part.InlineData.Data), mimeType, len(part.InlineData.Data)
	}
	return "", "text/plain; charset=utf-8", 0
}

func projectString(project *projects.Project, field string) string {
	if project == nil {
		return ""
	}
	switch field {
	case "app":
		return project.AppName
	case "owner":
		return project.OwnerUserID
	default:
		return ""
	}
}

func projectRegistryArtifactName(projectID string) string {
	return fmt.Sprintf(projectRegistryNameFormat, sanitizeProjectArtifactID(projectID))
}

func isProjectRegistryArtifact(name string) bool {
	return strings.HasPrefix(name, "user:project__") && strings.HasSuffix(name, "__artifacts.json")
}

func normalizeProjectArtifactVisibility(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "":
		return ""
	case "visible", "project", visibilityProjectVisible:
		return visibilityProjectVisible
	case "default", visibilityProjectDefault:
		return visibilityProjectDefault
	case "private", "session", visibilitySessionPrivate:
		return visibilitySessionPrivate
	case "hidden", "system", visibilitySystemHidden:
		return visibilitySystemHidden
	case "publish", visibilityPublished:
		return visibilityPublished
	default:
		return ""
	}
}

func isHiddenProjectArtifactVisibility(v string) bool {
	return v == visibilitySystemHidden || v == visibilitySessionPrivate
}

func projectArtifactVisibilityRank(v string) int {
	switch v {
	case visibilityProjectDefault:
		return 0
	case visibilityProjectVisible:
		return 1
	case visibilityPublished:
		return 2
	case visibilitySessionPrivate:
		return 3
	case visibilitySystemHidden:
		return 4
	default:
		return 9
	}
}

func normalizeProjectArtifactStringList(values []string) []string {
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

func containsProjectArtifactFold(values []string, needle string) bool {
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

func sanitizeProjectArtifactID(s string) string {
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

func firstNonEmptyProjectArtifact(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

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
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"google.golang.org/adk/artifact"

	"google.golang.org/adk/internal/platform/auth"
	"google.golang.org/adk/internal/platform/novelstore"
	"google.golang.org/adk/internal/platform/projects"
	"google.golang.org/adk/internal/platform/uploads"
	"google.golang.org/adk/session"
)

// PlatformProjectsAPIController exposes durable project/workbench records.
type PlatformProjectsAPIController struct {
	service         projects.Service
	artifactService artifact.Service
	sessionService  session.Service
	uploadService   uploads.Service
	novelService    *novelstore.Service
}

func NewPlatformProjectsAPIController(service projects.Service, artifactService artifact.Service, sessionService session.Service, uploadService uploads.Service, novelService *novelstore.Service) *PlatformProjectsAPIController {
	return &PlatformProjectsAPIController{
		service:         service,
		artifactService: artifactService,
		sessionService:  sessionService,
		uploadService:   uploadService,
		novelService:    novelService,
	}
}

type projectDeleteSummary struct {
	SessionsDeleted       int `json:"sessions_deleted"`
	SessionArtifactsClean int `json:"session_artifacts_deleted"`
	UploadsDeleted        int `json:"uploads_deleted"`
	ProjectArtifactsClean int `json:"project_artifacts_deleted"`
	RegistryDeleted       int `json:"registry_deleted"`
	BooksDeleted          int `json:"books_deleted"`
}

func (c *PlatformProjectsAPIController) ListProjectsHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform project service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	q := req.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	projects, err := c.service.List(req.Context(), projects.ListFilter{TenantID: p.TenantID, OwnerUserID: q.Get("owner_user_id"), AppName: q.Get("app_name"), Status: q.Get("status"), Limit: limit})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(projects, http.StatusOK, rw)
}

func (c *PlatformProjectsAPIController) CreateProjectHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform project service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	var body struct {
		OwnerUserID  string `json:"owner_user_id"`
		Name         string `json:"name"`
		DisplayName  string `json:"display_name"`
		Description  string `json:"description"`
		AppName      string `json:"app_name"`
		Status       string `json:"status"`
		MetadataJSON string `json:"metadata_json"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	owner := body.OwnerUserID
	if owner == "" {
		owner = p.UserID
	}
	project, err := c.service.Create(req.Context(), projects.CreateRequest{TenantID: p.TenantID, OwnerUserID: owner, Name: body.Name, DisplayName: body.DisplayName, Description: body.Description, AppName: body.AppName, Status: body.Status, MetadataJSON: body.MetadataJSON})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	EncodeJSONResponse(project, http.StatusCreated, rw)
}

func (c *PlatformProjectsAPIController) GetProjectHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform project service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["project_id"]
	project, err := c.service.Get(req.Context(), p.TenantID, id)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(project, http.StatusOK, rw)
}

func (c *PlatformProjectsAPIController) UpdateProjectHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform project service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["project_id"]
	var body struct {
		Name         *string `json:"name"`
		DisplayName  *string `json:"display_name"`
		Description  *string `json:"description"`
		AppName      *string `json:"app_name"`
		Status       *string `json:"status"`
		MetadataJSON *string `json:"metadata_json"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	project, err := c.service.Update(req.Context(), p.TenantID, id, projects.UpdateRequest{Name: body.Name, DisplayName: body.DisplayName, Description: body.Description, AppName: body.AppName, Status: body.Status, MetadataJSON: body.MetadataJSON})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(project, http.StatusOK, rw)
}

func (c *PlatformProjectsAPIController) ArchiveProjectHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform project service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["project_id"]
	project, err := c.service.Archive(req.Context(), p.TenantID, id)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(project, http.StatusOK, rw)
}

func (c *PlatformProjectsAPIController) DeleteProjectHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform project service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	id := mux.Vars(req)["project_id"]
	project, err := c.service.Get(req.Context(), p.TenantID, id)
	if err != nil {
		writePlatformError(rw, err)
		return
	}

	summary, err := c.deleteProjectResources(req, p, project)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := c.service.Delete(req.Context(), p.TenantID, id); err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(map[string]any{
		"ok":         true,
		"project":    project,
		"project_id": project.ID,
		"summary":    summary,
	}, http.StatusOK, rw)
}

func (c *PlatformProjectsAPIController) deleteProjectResources(req *http.Request, principal auth.Principal, project *projects.Project) (projectDeleteSummary, error) {
	var summary projectDeleteSummary

	sessions, err := c.listProjectSessions(req.Context(), principal, project)
	if err != nil {
		return summary, err
	}
	for _, sess := range sessions {
		artifactNames := listSessionArtifactNames(req.Context(), c.artifactService, sess)
		if err := c.deleteSessionArtifacts(req.Context(), sess); err != nil {
			return summary, err
		}
		for _, name := range artifactNames {
			if !isUserScopedArtifactFile(name) {
				summary.SessionArtifactsClean++
			}
		}
		if err := c.sessionService.Delete(req.Context(), &session.DeleteRequest{
			AppName:   sess.AppName(),
			UserID:    sess.UserID(),
			SessionID: sess.ID(),
		}); err != nil {
			return summary, err
		}
		summary.SessionsDeleted++
	}

	uploadsDeleted, err := c.deleteProjectUploads(req.Context(), principal, project)
	if err != nil {
		return summary, err
	}
	summary.UploadsDeleted = uploadsDeleted

	projectArtifactsDeleted, registryDeleted, err := c.deleteProjectArtifacts(req, principal, project)
	if err != nil {
		return summary, err
	}
	summary.ProjectArtifactsClean = projectArtifactsDeleted
	summary.RegistryDeleted = registryDeleted

	booksDeleted, err := c.deleteProjectBooks(req.Context(), principal, project)
	if err != nil {
		return summary, err
	}
	summary.BooksDeleted = booksDeleted

	return summary, nil
}

func (c *PlatformProjectsAPIController) listProjectSessions(ctx context.Context, principal auth.Principal, project *projects.Project) ([]session.Session, error) {
	if c.sessionService == nil {
		return nil, nil
	}
	appName := strings.TrimSpace(project.AppName)
	if appName == "" {
		return nil, nil
	}
	if global, ok := c.sessionService.(session.GlobalService); ok {
		resp, err := global.ListAll(ctx, &session.ListAllRequest{AppName: appName, Limit: 200})
		if err != nil {
			return nil, err
		}
		out := make([]session.Session, 0, len(resp.Sessions))
		for _, sess := range resp.Sessions {
			if sessionBelongsToProject(sess, project.ID) {
				out = append(out, sess)
			}
		}
		return out, nil
	}
	userID := firstNonEmptyProjectArtifact(project.OwnerUserID, principal.UserID)
	resp, err := c.sessionService.List(ctx, &session.ListRequest{AppName: appName, UserID: userID})
	if err != nil {
		return nil, err
	}
	out := make([]session.Session, 0, len(resp.Sessions))
	for _, sess := range resp.Sessions {
		if sessionBelongsToProject(sess, project.ID) {
			out = append(out, sess)
		}
	}
	return out, nil
}

func (c *PlatformProjectsAPIController) deleteSessionArtifacts(ctx context.Context, sess session.Session) error {
	if c.artifactService == nil || sess == nil {
		return nil
	}
	for _, fileName := range listSessionArtifactNames(ctx, c.artifactService, sess) {
		if isUserScopedArtifactFile(fileName) {
			continue
		}
		if err := c.artifactService.Delete(ctx, &artifact.DeleteRequest{
			AppName:   sess.AppName(),
			UserID:    sess.UserID(),
			SessionID: sess.ID(),
			FileName:  fileName,
		}); err != nil {
			return err
		}
	}
	return nil
}

func listSessionArtifactNames(ctx context.Context, svc artifact.Service, sess session.Session) []string {
	if svc == nil || sess == nil {
		return nil
	}
	resp, err := svc.List(ctx, &artifact.ListRequest{AppName: sess.AppName(), UserID: sess.UserID(), SessionID: sess.ID()})
	if err != nil || resp == nil {
		return nil
	}
	return resp.FileNames
}

func (c *PlatformProjectsAPIController) deleteProjectUploads(ctx context.Context, principal auth.Principal, project *projects.Project) (int, error) {
	if c.uploadService == nil {
		return 0, nil
	}
	deleted := 0
	for {
		items, err := c.uploadService.List(ctx, uploads.ListFilter{
			TenantID:  principal.TenantID,
			ProjectID: project.ID,
			Status:    uploads.StatusActive,
			Limit:     500,
		})
		if err != nil {
			return deleted, err
		}
		if len(items) == 0 {
			return deleted, nil
		}
		for _, item := range items {
			if err := c.uploadService.Delete(ctx, principal.TenantID, item.ID); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
}

func (c *PlatformProjectsAPIController) deleteProjectArtifacts(req *http.Request, principal auth.Principal, project *projects.Project) (int, int, error) {
	if c.artifactService == nil {
		return 0, 0, nil
	}
	scope := projectArtifactScope{
		ProjectID:      sanitizeProjectArtifactID(project.ID),
		RegistryName:   projectRegistryArtifactName(project.ID),
		AppName:        firstNonEmptyProjectArtifact(project.AppName, "book_dissector"),
		UserID:         firstNonEmptyProjectArtifact(project.OwnerUserID, principal.UserID),
		PlatformRecord: project,
	}
	registry, err := c.loadProjectRegistryByName(req, scope)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || strings.Contains(strings.ToLower(err.Error()), "not found") {
			return 0, 0, nil
		}
		_ = c.artifactService.Delete(req.Context(), &artifact.DeleteRequest{AppName: scope.AppName, UserID: scope.UserID, SessionID: projectArtifactSessionID, FileName: scope.RegistryName})
		return 0, 1, nil
	}
	deletedFiles := 0
	for _, art := range registry.Artifacts {
		if strings.TrimSpace(art.ArtifactName) == "" {
			continue
		}
		if err := c.artifactService.Delete(req.Context(), &artifact.DeleteRequest{
			AppName:   scope.AppName,
			UserID:    scope.UserID,
			SessionID: projectArtifactSessionID,
			FileName:  art.ArtifactName,
		}); err != nil {
			return deletedFiles, 0, err
		}
		deletedFiles++
	}
	if err := c.artifactService.Delete(req.Context(), &artifact.DeleteRequest{
		AppName:   scope.AppName,
		UserID:    scope.UserID,
		SessionID: projectArtifactSessionID,
		FileName:  scope.RegistryName,
	}); err != nil {
		return deletedFiles, 0, err
	}
	return deletedFiles, 1, nil
}

func (c *PlatformProjectsAPIController) deleteProjectBooks(ctx context.Context, principal auth.Principal, project *projects.Project) (int, error) {
	if c.novelService == nil {
		return 0, nil
	}
	deleted := 0
	for {
		books, err := c.novelService.ListBooks(ctx, novelstore.ListBooksRequest{
			TenantID:  principal.TenantID,
			ProjectID: project.ID,
			Status:    novelstore.StatusActive,
			Limit:     500,
		})
		if err != nil {
			return deleted, err
		}
		if len(books) == 0 {
			return deleted, nil
		}
		for _, book := range books {
			if _, err := c.novelService.DeleteBook(ctx, novelstore.DeleteBookRequest{
				TenantID:      principal.TenantID,
				ProjectID:     project.ID,
				BookID:        book.ID,
				DeleteObjects: true,
			}); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
}

func sessionBelongsToProject(sess session.Session, projectID string) bool {
	if sess == nil || strings.TrimSpace(projectID) == "" {
		return false
	}
	for _, key := range []string{"project_id", "projectId"} {
		value, err := sess.State().Get(key)
		if err == nil && strings.TrimSpace(stringProjectID(value)) == strings.TrimSpace(projectID) {
			return true
		}
	}
	return false
}

func isUserScopedArtifactFile(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), "user:")
}

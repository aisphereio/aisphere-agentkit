package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"google.golang.org/genai"
	"gorm.io/gorm"

	"google.golang.org/adk/artifact"
	"google.golang.org/adk/internal/platform/auth"
	"google.golang.org/adk/internal/platform/projects"
	"google.golang.org/adk/internal/platform/uploads"
	"google.golang.org/adk/session"
	sessiondb "google.golang.org/adk/session/database"
)

func TestDeleteProjectHandlerCascadesProjectAssets(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := projects.AutoMigrate(db); err != nil {
		t.Fatalf("projects migrate: %v", err)
	}
	if err := uploads.AutoMigrate(db); err != nil {
		t.Fatalf("uploads migrate: %v", err)
	}
	sessionSvc, err := sessiondb.NewSessionService(sqlite.Open("file::memory:?cache=shared"))
	if err != nil {
		t.Fatalf("new session service: %v", err)
	}
	if err := sessiondb.AutoMigrate(sessionSvc); err != nil {
		t.Fatalf("session migrate: %v", err)
	}

	projectSvc := projects.NewService(db)
	uploadRoot := t.TempDir()
	uploadSvc := uploads.NewService(db, uploadRoot)
	artifactSvc := artifact.InMemoryService()

	ctx := auth.WithPrincipal(context.Background(), auth.DefaultPrincipal())
	project, err := projectSvc.Create(ctx, projects.CreateRequest{
		TenantID:    "default",
		OwnerUserID: "admin",
		Name:        "cascade-delete",
		DisplayName: "Cascade Delete",
		AppName:     "book_dissector",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	createdSession, err := sessionSvc.Create(ctx, &session.CreateRequest{
		AppName:   "book_dissector",
		UserID:    "admin",
		SessionID: "session-1",
		State:     map[string]any{"project_id": project.ID},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := artifactSvc.Save(ctx, &artifact.SaveRequest{
		AppName:   "book_dissector",
		UserID:    "admin",
		SessionID: createdSession.Session.ID(),
		FileName:  "session-note.txt",
		Part:      &genai.Part{Text: "hello session artifact"},
	}); err != nil {
		t.Fatalf("save session artifact: %v", err)
	}

	if _, err := uploadSvc.Create(ctx, uploads.CreateRequest{
		TenantID:     "default",
		UserID:       "admin",
		ProjectID:    project.ID,
		AppName:      "book_dissector",
		SessionID:    createdSession.Session.ID(),
		OriginalName: "chapter.txt",
		MIMEType:     "text/plain; charset=utf-8",
		Reader:       strings.NewReader("upload body"),
	}); err != nil {
		t.Fatalf("create upload: %v", err)
	}

	controller := NewPlatformProjectsAPIController(projectSvc, artifactSvc, sessionSvc, uploadSvc, nil)
	req := httptest.NewRequest(http.MethodDelete, "/platform/projects/"+project.ID, nil).WithContext(ctx)
	req = mux.SetURLVars(req, map[string]string{"project_id": project.ID})
	scope := projectArtifactScope{
		ProjectID:      sanitizeProjectArtifactID(project.ID),
		RegistryName:   projectRegistryArtifactName(project.ID),
		AppName:        "book_dissector",
		UserID:         "admin",
		PlatformRecord: project,
	}
	if _, err := artifactSvc.Save(ctx, &artifact.SaveRequest{
		AppName:   scope.AppName,
		UserID:    scope.UserID,
		SessionID: projectArtifactSessionID,
		FileName:  "project-skill.md",
		Part:      &genai.Part{Text: "project artifact"},
	}); err != nil {
		t.Fatalf("save project artifact file: %v", err)
	}
	registry := emptyPlatformProjectRegistry(scope)
	registry.Artifacts = []platformProjectArtifact{{
		ArtifactID:   "art-1",
		ArtifactName: "project-skill.md",
		Type:         "skill",
		Visibility:   visibilityProjectVisible,
		Mountable:    true,
		CreatedAt:    registry.CreatedAt,
		UpdatedAt:    registry.UpdatedAt,
	}}
	registry.ArtifactCount = 1
	if err := controller.saveProjectRegistry(req, scope, registry); err != nil {
		t.Fatalf("save project registry: %v", err)
	}

	rr := httptest.NewRecorder()
	controller.DeleteProjectHandler(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}

	var resp struct {
		OK      bool                 `json:"ok"`
		Project map[string]any       `json:"project"`
		Summary projectDeleteSummary `json:"summary"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatalf("expected ok response, got %+v", resp)
	}
	if resp.Summary.SessionsDeleted != 1 || resp.Summary.UploadsDeleted != 1 || resp.Summary.SessionArtifactsClean != 1 || resp.Summary.ProjectArtifactsClean != 1 || resp.Summary.RegistryDeleted != 1 {
		t.Fatalf("unexpected summary: %+v", resp.Summary)
	}

	if _, err := projectSvc.Get(ctx, "default", project.ID); err == nil {
		t.Fatalf("project still exists after delete")
	}
	if _, err := sessionSvc.Get(ctx, &session.GetRequest{AppName: "book_dissector", UserID: "admin", SessionID: createdSession.Session.ID()}); err == nil {
		t.Fatalf("session still exists after delete")
	}
	items, err := uploadSvc.List(ctx, uploads.ListFilter{TenantID: "default", ProjectID: project.ID, Status: uploads.StatusActive, Limit: 100})
	if err != nil {
		t.Fatalf("list uploads: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("uploads len=%d, want 0", len(items))
	}
	if resp, err := artifactSvc.List(ctx, &artifact.ListRequest{AppName: "book_dissector", UserID: "admin", SessionID: createdSession.Session.ID()}); err != nil {
		t.Fatalf("list session artifacts: %v", err)
	} else if len(resp.FileNames) != 0 {
		t.Fatalf("session artifacts still exist: %+v", resp.FileNames)
	}
	if _, err := artifactSvc.Load(ctx, &artifact.LoadRequest{AppName: scope.AppName, UserID: scope.UserID, SessionID: projectArtifactSessionID, FileName: scope.RegistryName}); err == nil {
		t.Fatalf("project registry still exists after delete")
	}
}

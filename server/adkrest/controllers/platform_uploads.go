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
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/mux"
	"google.golang.org/genai"

	"google.golang.org/adk/artifact"
	"google.golang.org/adk/internal/platform/auth"
	"google.golang.org/adk/internal/platform/uploads"
)

const maxUploadMemory = 32 << 20 // 32 MiB multipart memory before spilling to temp files.

// PlatformUploadsAPIController exposes the platform upload center.
type PlatformUploadsAPIController struct {
	service         uploads.Service
	artifactService artifact.Service
}

func NewPlatformUploadsAPIController(service uploads.Service, artifactService artifact.Service) *PlatformUploadsAPIController {
	return &PlatformUploadsAPIController{service: service, artifactService: artifactService}
}

func (c *PlatformUploadsAPIController) ListUploadsHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform upload service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	q := req.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	userID := q.Get("user_id")
	if userID == "" {
		userID = p.UserID
	}
	items, err := c.service.List(req.Context(), uploads.ListFilter{
		TenantID:     p.TenantID,
		UserID:       userID,
		ProjectID:    q.Get("project_id"),
		AppName:      q.Get("app_name"),
		SessionID:    q.Get("session_id"),
		Purpose:      q.Get("purpose"),
		Status:       q.Get("status"),
		HandlingMode: q.Get("handling_mode"),
		Limit:        limit,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusInternalServerError)
		return
	}
	EncodeJSONResponse(items, http.StatusOK, rw)
}

func (c *PlatformUploadsAPIController) CreateUploadHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform upload service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())

	contentType := strings.ToLower(req.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		c.createMultipartUpload(rw, req, p)
		return
	}
	if strings.HasPrefix(contentType, "application/json") {
		c.createJSONUpload(rw, req, p)
		return
	}
	c.createRawUpload(rw, req, p)
}

func (c *PlatformUploadsAPIController) createMultipartUpload(rw http.ResponseWriter, req *http.Request, p auth.Principal) {
	if err := req.ParseMultipartForm(maxUploadMemory); err != nil {
		http.Error(rw, fmt.Sprintf("parse multipart form: %v", err), http.StatusBadRequest)
		return
	}
	file, header, err := req.FormFile("file")
	if err != nil {
		http.Error(rw, "multipart field 'file' is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = req.FormValue("mime_type")
	}
	upload, err := c.service.Create(req.Context(), uploads.CreateRequest{
		TenantID:     p.TenantID,
		UserID:       firstNonEmptyString(req.FormValue("user_id"), p.UserID),
		ProjectID:    req.FormValue("project_id"),
		AppName:      req.FormValue("app_name"),
		SessionID:    req.FormValue("session_id"),
		Purpose:      req.FormValue("purpose"),
		OriginalName: firstNonEmptyString(req.FormValue("file_name"), header.Filename),
		MIMEType:     mimeType,
		MetadataJSON: req.FormValue("metadata_json"),
		Reader:       file,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	EncodeJSONResponse(upload, http.StatusCreated, rw)
}

func (c *PlatformUploadsAPIController) createJSONUpload(rw http.ResponseWriter, req *http.Request, p auth.Principal) {
	var body struct {
		UserID        string `json:"user_id"`
		ProjectID     string `json:"project_id"`
		AppName       string `json:"app_name"`
		SessionID     string `json:"session_id"`
		Purpose       string `json:"purpose"`
		FileName      string `json:"file_name"`
		MIMEType      string `json:"mime_type"`
		MetadataJSON  string `json:"metadata_json"`
		Content       string `json:"content"`
		ContentBase64 string `json:"content_base64"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.FileName) == "" {
		http.Error(rw, "file_name is required", http.StatusBadRequest)
		return
	}
	var reader io.Reader
	if body.ContentBase64 != "" {
		data, err := base64.StdEncoding.DecodeString(body.ContentBase64)
		if err != nil {
			http.Error(rw, fmt.Sprintf("invalid content_base64: %v", err), http.StatusBadRequest)
			return
		}
		reader = bytes.NewReader(data)
	} else if body.Content != "" {
		reader = strings.NewReader(body.Content)
	} else {
		http.Error(rw, "content or content_base64 is required", http.StatusBadRequest)
		return
	}
	upload, err := c.service.Create(req.Context(), uploads.CreateRequest{
		TenantID:     p.TenantID,
		UserID:       firstNonEmptyString(body.UserID, p.UserID),
		ProjectID:    body.ProjectID,
		AppName:      body.AppName,
		SessionID:    body.SessionID,
		Purpose:      body.Purpose,
		OriginalName: body.FileName,
		MIMEType:     body.MIMEType,
		MetadataJSON: body.MetadataJSON,
		Reader:       reader,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	EncodeJSONResponse(upload, http.StatusCreated, rw)
}

func (c *PlatformUploadsAPIController) createRawUpload(rw http.ResponseWriter, req *http.Request, p auth.Principal) {
	q := req.URL.Query()
	fileName := firstNonEmptyString(q.Get("file_name"), req.Header.Get("X-Upload-File-Name"), "upload.bin")
	upload, err := c.service.Create(req.Context(), uploads.CreateRequest{
		TenantID:     p.TenantID,
		UserID:       firstNonEmptyString(q.Get("user_id"), p.UserID),
		ProjectID:    q.Get("project_id"),
		AppName:      q.Get("app_name"),
		SessionID:    q.Get("session_id"),
		Purpose:      q.Get("purpose"),
		OriginalName: fileName,
		MIMEType:     req.Header.Get("Content-Type"),
		MetadataJSON: q.Get("metadata_json"),
		Reader:       req.Body,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	EncodeJSONResponse(upload, http.StatusCreated, rw)
}

func (c *PlatformUploadsAPIController) GetUploadHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform upload service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	upload, err := c.service.Get(req.Context(), p.TenantID, mux.Vars(req)["upload_id"])
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	if !uploadMatchesProject(upload, req.URL.Query().Get("project_id")) {
		http.Error(rw, "upload does not belong to project", http.StatusForbidden)
		return
	}
	EncodeJSONResponse(upload, http.StatusOK, rw)
}

func (c *PlatformUploadsAPIController) PreviewUploadHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform upload service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	maxBytes, _ := strconv.ParseInt(req.URL.Query().Get("max_bytes"), 10, 64)
	preview, err := c.service.Preview(req.Context(), p.TenantID, mux.Vars(req)["upload_id"], maxBytes)
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	if req.URL.Query().Get("project_id") != "" {
		upload, err := c.service.Get(req.Context(), p.TenantID, mux.Vars(req)["upload_id"])
		if err != nil {
			writePlatformError(rw, err)
			return
		}
		if !uploadMatchesProject(upload, req.URL.Query().Get("project_id")) {
			http.Error(rw, "upload does not belong to project", http.StatusForbidden)
			return
		}
	}
	EncodeJSONResponse(preview, http.StatusOK, rw)
}

func (c *PlatformUploadsAPIController) DownloadUploadHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform upload service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	reader, upload, err := c.service.Open(req.Context(), p.TenantID, mux.Vars(req)["upload_id"])
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	defer reader.Close()
	if !uploadMatchesProject(upload, req.URL.Query().Get("project_id")) {
		http.Error(rw, "upload does not belong to project", http.StatusForbidden)
		return
	}
	if upload.MIMEType != "" {
		rw.Header().Set("Content-Type", upload.MIMEType)
	}
	rw.Header().Set("Content-Disposition", contentDisposition(upload.OriginalName))
	rw.Header().Set("X-Upload-ID", upload.ID)
	rw.Header().Set("X-Upload-SHA256", upload.SHA256)
	if upload.SizeBytes > 0 {
		rw.Header().Set("Content-Length", strconv.FormatInt(upload.SizeBytes, 10))
	}
	_, _ = io.Copy(rw, reader)
}

func (c *PlatformUploadsAPIController) DeleteUploadHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform upload service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	upload, err := c.service.Get(req.Context(), p.TenantID, mux.Vars(req)["upload_id"])
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	if !uploadMatchesProject(upload, req.URL.Query().Get("project_id")) {
		http.Error(rw, "upload does not belong to project", http.StatusForbidden)
		return
	}
	if err := c.service.Delete(req.Context(), p.TenantID, mux.Vars(req)["upload_id"]); err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(map[string]any{"deleted": true}, http.StatusOK, rw)
}

func (c *PlatformUploadsAPIController) AttachUploadToArtifactHandler(rw http.ResponseWriter, req *http.Request) {
	if c.service == nil {
		http.Error(rw, "platform upload service is not enabled", http.StatusNotImplemented)
		return
	}
	if c.artifactService == nil {
		http.Error(rw, "artifact service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	var body struct {
		AppName      string `json:"app_name"`
		UserID       string `json:"user_id"`
		SessionID    string `json:"session_id"`
		ArtifactName string `json:"artifact_name"`
		ProjectID    string `json:"project_id"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.AppName) == "" || strings.TrimSpace(body.SessionID) == "" {
		http.Error(rw, "app_name and session_id are required", http.StatusBadRequest)
		return
	}
	userID := firstNonEmptyString(body.UserID, p.UserID)
	reader, upload, err := c.service.Open(req.Context(), p.TenantID, mux.Vars(req)["upload_id"])
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	defer reader.Close()
	projectID := firstNonEmptyString(body.ProjectID, upload.ProjectID)
	if !uploadMatchesProject(upload, projectID) {
		http.Error(rw, "upload does not belong to project", http.StatusForbidden)
		return
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		http.Error(rw, fmt.Sprintf("read upload: %v", err), http.StatusInternalServerError)
		return
	}
	artifactName := body.ArtifactName
	if artifactName == "" {
		artifactName = upload.OriginalName
	}
	artifactName = filepath.Base(artifactName)
	if artifactName == "." || artifactName == "" {
		http.Error(rw, "invalid artifact_name", http.StatusBadRequest)
		return
	}
	resp, err := c.artifactService.Save(req.Context(), &artifact.SaveRequest{
		AppName:   body.AppName,
		UserID:    userID,
		SessionID: body.SessionID,
		FileName:  artifactName,
		Part:      &genai.Part{InlineData: &genai.Blob{MIMEType: upload.MIMEType, Data: data}},
	})
	if err != nil {
		http.Error(rw, fmt.Sprintf("save artifact: %v", err), http.StatusInternalServerError)
		return
	}
	version := int64(0)
	if resp != nil {
		version = resp.Version
	}
	if projectID != "" {
		projectVersion, err := c.saveUploadProjectArtifact(req, projectID, body.AppName, userID, body.SessionID, artifactName, upload, data)
		if err != nil {
			http.Error(rw, fmt.Sprintf("save project artifact: %v", err), http.StatusInternalServerError)
			return
		}
		if projectVersion > 0 {
			version = projectVersion
		}
	}
	EncodeJSONResponse(map[string]any{
		"upload_id":     upload.ID,
		"artifact_name": artifactName,
		"app_name":      body.AppName,
		"user_id":       userID,
		"session_id":    body.SessionID,
		"version":       version,
		"mime_type":     upload.MIMEType,
		"bytes":         len(data),
	}, http.StatusOK, rw)
}

func (c *PlatformUploadsAPIController) saveUploadProjectArtifact(req *http.Request, projectID, appName, userID, sessionID, artifactName string, upload *uploads.Upload, data []byte) (int64, error) {
	if c.artifactService == nil || strings.TrimSpace(projectID) == "" {
		return 0, nil
	}
	scope := projectArtifactScope{
		ProjectID:    sanitizeProjectArtifactID(projectID),
		RegistryName: projectRegistryArtifactName(projectID),
		AppName:      firstNonEmptyProjectArtifact(appName, upload.AppName, "book_dissector"),
		UserID:       firstNonEmptyProjectArtifact(userID, upload.UserID),
	}
	resp, err := c.artifactService.Save(req.Context(), &artifact.SaveRequest{
		AppName:   scope.AppName,
		UserID:    scope.UserID,
		SessionID: projectArtifactSessionID,
		FileName:  artifactName,
		Part:      &genai.Part{InlineData: &genai.Blob{MIMEType: upload.MIMEType, Data: data}},
	})
	if err != nil {
		return 0, err
	}
	version := int64(0)
	if resp != nil {
		version = resp.Version
	}
	registry, err := loadUploadProjectRegistry(req, c.artifactService, scope)
	if err != nil {
		registry = emptyPlatformProjectRegistry(scope)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	art := platformProjectArtifact{
		ArtifactID:    "upload:" + upload.ID,
		ArtifactName:  artifactName,
		Type:          "upload",
		Title:         upload.OriginalName,
		ProducerAgent: appName,
		Visibility:    visibilityProjectVisible,
		Mountable:     true,
		Metadata: map[string]string{
			"upload_id":         upload.ID,
			"source_session_id": sessionID,
			"artifact_version":  strconv.FormatInt(version, 10),
			"mime_type":         upload.MIMEType,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	idx := findPlatformProjectArtifactIndex(registry.Artifacts, art.ArtifactID)
	if idx < 0 {
		idx = findPlatformProjectArtifactIndex(registry.Artifacts, art.ArtifactName)
	}
	if idx >= 0 {
		art.CreatedAt = firstNonEmptyProjectArtifact(registry.Artifacts[idx].CreatedAt, now)
		registry.Artifacts[idx] = art
	} else {
		registry.Artifacts = append(registry.Artifacts, art)
	}
	registry.UpdatedAt = now
	registry.ArtifactCount = len(registry.Artifacts)
	return version, saveUploadProjectRegistry(req, c.artifactService, scope, registry)
}

func loadUploadProjectRegistry(req *http.Request, artifactService artifact.Service, scope projectArtifactScope) (platformProjectRegistry, error) {
	resp, err := artifactService.Load(req.Context(), &artifact.LoadRequest{
		AppName:   scope.AppName,
		UserID:    scope.UserID,
		SessionID: projectArtifactSessionID,
		FileName:  scope.RegistryName,
	})
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

func saveUploadProjectRegistry(req *http.Request, artifactService artifact.Service, scope projectArtifactScope, registry platformProjectRegistry) error {
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
	_, err = artifactService.Save(req.Context(), &artifact.SaveRequest{
		AppName:   scope.AppName,
		UserID:    scope.UserID,
		SessionID: projectArtifactSessionID,
		FileName:  scope.RegistryName,
		Part:      &genai.Part{InlineData: &genai.Blob{MIMEType: "application/json; charset=utf-8", Data: data}},
	})
	return err
}

func uploadMatchesProject(upload *uploads.Upload, projectID string) bool {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return true
	}
	return upload != nil && strings.TrimSpace(upload.ProjectID) == projectID
}

func contentDisposition(name string) string {
	name = strings.ReplaceAll(filepath.Base(name), "\"", "_")
	if name == "" || name == "." {
		name = "upload.bin"
	}
	return fmt.Sprintf("attachment; filename=\"%s\"", name)
}

func firstNonEmptyString(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

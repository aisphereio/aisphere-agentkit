// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"google.golang.org/adk/internal/skillservice"
	"google.golang.org/adk/tool/skilltoolset/skill"
)

// SkillsAPIController exposes filesystem-backed ADK skills to the Admin/WebUI.
//
// The controller intentionally mirrors the useful SkillHub management surface
// without importing SkillHub's Java stack: CRUD, searchable metadata, package
// import/export, and resource file management.
type SkillsAPIController struct {
	svc skillservice.Service
}

func NewSkillsAPIController(svc skillservice.Service) *SkillsAPIController {
	return &SkillsAPIController{svc: svc}
}

func (c *SkillsAPIController) ListSkillsHandler(rw http.ResponseWriter, req *http.Request) {
	if c == nil || c.svc == nil {
		EncodeJSONResponse(map[string]any{"skills": []any{}, "total": 0}, http.StatusOK, rw)
		return
	}
	skills, err := c.svc.List(req.Context())
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	filtered := filterSkillSummaries(skills, req.URL.Query())
	EncodeJSONResponse(map[string]any{"skills": filtered, "total": len(filtered)}, http.StatusOK, rw)
}

func (c *SkillsAPIController) GetSkillHandler(rw http.ResponseWriter, req *http.Request) {
	detail, err := c.svc.Get(req.Context(), mux.Vars(req)["name"])
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(detail, http.StatusOK, rw)
}

func (c *SkillsAPIController) CreateSkillHandler(rw http.ResponseWriter, req *http.Request) {
	var body skillservice.SaveRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(rw, http.StatusBadRequest, "failed to decode skill request: "+err.Error())
		return
	}
	detail, err := c.svc.Save(req.Context(), body)
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(detail, http.StatusCreated, rw)
}

func (c *SkillsAPIController) UpdateSkillHandler(rw http.ResponseWriter, req *http.Request) {
	var body skillservice.SaveRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(rw, http.StatusBadRequest, "failed to decode skill request: "+err.Error())
		return
	}
	name := mux.Vars(req)["name"]
	if strings.TrimSpace(body.Name) == "" {
		body.Name = name
	}
	detail, err := c.svc.Save(req.Context(), body)
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(detail, http.StatusOK, rw)
}

func (c *SkillsAPIController) DeleteSkillHandler(rw http.ResponseWriter, req *http.Request) {
	q := req.URL.Query()
	opts := skillservice.DeleteOptions{
		Force:       strings.EqualFold(q.Get("force"), "true") || q.Get("force") == "1",
		Physical:    strings.EqualFold(q.Get("physical"), "true") || q.Get("physical") == "1",
		RequestedBy: req.Header.Get("X-User-Id"),
	}
	if err := c.svc.DeleteWithOptions(req.Context(), mux.Vars(req)["name"], opts); err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(map[string]any{"ok": true}, http.StatusOK, rw)
}

func (c *SkillsAPIController) ImportSkillHandler(rw http.ResponseWriter, req *http.Request) {
	if c == nil || c.svc == nil {
		respondError(rw, http.StatusServiceUnavailable, "skill service is not enabled")
		return
	}

	var filename string
	var data []byte
	contentType := req.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") {
		if err := req.ParseMultipartForm(64 << 20); err != nil {
			respondError(rw, http.StatusBadRequest, "failed to parse multipart form: "+err.Error())
			return
		}
		file, header, err := req.FormFile("file")
		if err != nil {
			respondError(rw, http.StatusBadRequest, "multipart field 'file' is required")
			return
		}
		defer file.Close()
		filename = header.Filename
		data, err = io.ReadAll(io.LimitReader(file, 64<<20))
		if err != nil {
			respondError(rw, http.StatusBadRequest, "failed to read uploaded file: "+err.Error())
			return
		}
	} else {
		filename = req.URL.Query().Get("filename")
		if filename == "" {
			filename = "SKILL.md"
		}
		var err error
		data, err = io.ReadAll(io.LimitReader(req.Body, 64<<20))
		if err != nil {
			respondError(rw, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return
		}
	}

	detail, err := c.svc.ImportPackage(req.Context(), filename, data)
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(detail, http.StatusCreated, rw)
}

func (c *SkillsAPIController) ExportSkillHandler(rw http.ResponseWriter, req *http.Request) {
	data, filename, err := c.svc.ExportPackage(req.Context(), mux.Vars(req)["name"])
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	if filename == "" {
		filename = "skill.zip"
	}
	rw.Header().Set("Content-Type", "application/zip")
	rw.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	rw.Header().Set("Content-Length", strconv.Itoa(len(data)))
	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write(data)
}

func (c *SkillsAPIController) ValidateSkillHandler(rw http.ResponseWriter, req *http.Request) {
	var body skillservice.SaveRequest
	if req.Body != nil && req.ContentLength != 0 {
		_ = json.NewDecoder(req.Body).Decode(&body)
	}
	name := mux.Vars(req)["name"]
	var (
		result *skillservice.ValidationResult
		err    error
	)
	if strings.TrimSpace(body.Name) != "" || strings.TrimSpace(body.RawMarkdown) != "" || strings.TrimSpace(body.Description) != "" || strings.TrimSpace(body.Instructions) != "" {
		if strings.TrimSpace(body.Name) == "" {
			body.Name = name
		}
		result, err = c.svc.Validate(req.Context(), body)
	} else {
		result, err = c.svc.ValidateExisting(req.Context(), name)
	}
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(result, http.StatusOK, rw)
}

func (c *SkillsAPIController) SkillReferencesHandler(rw http.ResponseWriter, req *http.Request) {
	refs, err := c.svc.References(req.Context(), mux.Vars(req)["name"])
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(refs, http.StatusOK, rw)
}

func (c *SkillsAPIController) UpdateSkillStatusHandler(rw http.ResponseWriter, req *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(rw, http.StatusBadRequest, "failed to decode status request: "+err.Error())
		return
	}
	detail, err := c.svc.UpdateStatus(req.Context(), mux.Vars(req)["name"], body.Status, req.Header.Get("X-User-Id"))
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(detail, http.StatusOK, rw)
}

func (c *SkillsAPIController) PublishSkillHandler(rw http.ResponseWriter, req *http.Request) {
	detail, err := c.svc.UpdateStatus(req.Context(), mux.Vars(req)["name"], skillservice.StatusPublished, req.Header.Get("X-User-Id"))
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(detail, http.StatusOK, rw)
}

func (c *SkillsAPIController) DeprecateSkillHandler(rw http.ResponseWriter, req *http.Request) {
	detail, err := c.svc.UpdateStatus(req.Context(), mux.Vars(req)["name"], skillservice.StatusDeprecated, req.Header.Get("X-User-Id"))
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(detail, http.StatusOK, rw)
}

func (c *SkillsAPIController) ArchiveSkillHandler(rw http.ResponseWriter, req *http.Request) {
	detail, err := c.svc.UpdateStatus(req.Context(), mux.Vars(req)["name"], skillservice.StatusArchived, req.Header.Get("X-User-Id"))
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(detail, http.StatusOK, rw)
}

func (c *SkillsAPIController) ListSkillResourcesHandler(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	subpath := req.URL.Query().Get("path")
	resources, err := c.svc.ListResources(req.Context(), vars["name"], subpath)
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(map[string]any{"resources": resources}, http.StatusOK, rw)
}

func (c *SkillsAPIController) GetSkillResourceHandler(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	resourcePath := skillservice.ResourcePathForURL(vars["resourcePath"])
	data, err := c.svc.LoadResource(req.Context(), vars["name"], resourcePath)
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	contentType := http.DetectContentType(data)
	if strings.HasPrefix(contentType, "text/plain") || strings.HasSuffix(resourcePath, ".md") || strings.HasSuffix(resourcePath, ".json") || strings.HasSuffix(resourcePath, ".yaml") || strings.HasSuffix(resourcePath, ".yml") || strings.HasSuffix(resourcePath, ".txt") {
		contentType = "text/plain; charset=utf-8"
	}
	rw.Header().Set("Content-Type", contentType)
	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write(data)
}

func (c *SkillsAPIController) SaveSkillResourceHandler(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	var body skillservice.SaveResourceRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		respondError(rw, http.StatusBadRequest, "failed to decode resource request: "+err.Error())
		return
	}
	data, err := skillservice.DecodeResourceContent(body)
	if err != nil {
		respondSkillError(rw, err)
		return
	}
	if err := c.svc.SaveResource(req.Context(), vars["name"], body.Path, data); err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(map[string]any{"ok": true, "path": body.Path}, http.StatusOK, rw)
}

func (c *SkillsAPIController) DeleteSkillResourceHandler(rw http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	resourcePath := skillservice.ResourcePathForURL(vars["resourcePath"])
	if err := c.svc.DeleteResource(req.Context(), vars["name"], resourcePath); err != nil {
		respondSkillError(rw, err)
		return
	}
	EncodeJSONResponse(map[string]any{"ok": true}, http.StatusOK, rw)
}

func respondSkillError(rw http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, skill.ErrSkillNotFound), errors.Is(err, skill.ErrResourceNotFound):
		respondError(rw, http.StatusNotFound, err.Error())
	case errors.Is(err, skill.ErrInvalidSkillName), errors.Is(err, skill.ErrInvalidFrontmatter), errors.Is(err, skill.ErrInvalidResourcePath):
		respondError(rw, http.StatusBadRequest, err.Error())
	case errors.Is(err, skillservice.ErrSkillInUse):
		respondError(rw, http.StatusConflict, err.Error())
	default:
		respondError(rw, http.StatusInternalServerError, err.Error())
	}
}

func respondError(w http.ResponseWriter, status int, v any) {
	message := http.StatusText(status)

	switch err := v.(type) {
	case nil:
		// keep default status text
	case error:
		message = err.Error()
	case string:
		message = err
	default:
		message = fmt.Sprint(err)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":  message,
		"status": status,
	})
}

func filterSkillSummaries(skills []skillservice.Summary, q valuesReader) []skillservice.Summary {
	query := strings.ToLower(strings.TrimSpace(firstQuery(q, "q", "query", "search")))
	label := strings.ToLower(strings.TrimSpace(q.Get("label")))
	tag := strings.ToLower(strings.TrimSpace(q.Get("tag")))
	tool := strings.ToLower(strings.TrimSpace(q.Get("tool")))
	status := strings.ToLower(strings.TrimSpace(q.Get("status")))
	visibility := strings.ToLower(strings.TrimSpace(q.Get("visibility")))
	category := strings.ToLower(strings.TrimSpace(q.Get("category")))

	out := make([]skillservice.Summary, 0, len(skills))
	for _, s := range skills {
		if query != "" && !skillSummaryContains(s, query) {
			continue
		}
		if label != "" && !containsFold(s.Labels, label) {
			continue
		}
		if tag != "" && !containsFold(s.Tags, tag) {
			continue
		}
		if tool != "" && !containsFold(s.AllowedTools, tool) {
			continue
		}
		if status != "" && strings.ToLower(s.Status) != status {
			continue
		}
		if visibility != "" && strings.ToLower(s.Visibility) != visibility {
			continue
		}
		if category != "" && strings.ToLower(s.Category) != category {
			continue
		}
		out = append(out, s)
	}
	return out
}

type valuesReader interface {
	Get(string) string
}

func firstQuery(q valuesReader, keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(q.Get(key)); v != "" {
			return v
		}
	}
	return ""
}

func skillSummaryContains(s skillservice.Summary, query string) bool {
	parts := []string{s.Name, s.DisplayName, s.Description, s.License, s.Compatibility, s.Version, s.Status, s.Visibility, s.Category, s.Owner, s.Changelog}
	parts = append(parts, s.AllowedTools...)
	parts = append(parts, s.Labels...)
	parts = append(parts, s.Tags...)
	for k, v := range s.Metadata {
		parts = append(parts, k, v)
	}
	for _, p := range parts {
		if strings.Contains(strings.ToLower(p), query) {
			return true
		}
	}
	return false
}

func containsFold(values []string, target string) bool {
	for _, v := range values {
		if strings.ToLower(strings.TrimSpace(v)) == target {
			return true
		}
	}
	return false
}

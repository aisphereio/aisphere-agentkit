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
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/mux"

	"google.golang.org/adk/internal/platform/auth"
	"google.golang.org/adk/internal/platform/novelstore"
	"google.golang.org/adk/internal/platform/uploads"
)

// PlatformNovelsAPIController exposes deterministic, model-free novel source
// processing under a project. It is the UI/API counterpart of
// NovelStoreToolset: upload/import/UTF-8 normalization/chapter split are
// backend operations and do not require LLM participation.
type PlatformNovelsAPIController struct {
	novelService  *novelstore.Service
	uploadService uploads.Service
}

func NewPlatformNovelsAPIController(novelService *novelstore.Service, uploadService uploads.Service) *PlatformNovelsAPIController {
	return &PlatformNovelsAPIController{novelService: novelService, uploadService: uploadService}
}

type NovelIngestResponse struct {
	Upload  *uploads.Upload                `json:"upload,omitempty"`
	Import  *novelstore.ImportUploadResult `json:"import,omitempty"`
	Preview *novelstore.SplitResult        `json:"preview,omitempty"`
	Split   *novelstore.SplitResult        `json:"split,omitempty"`
	Book    *novelstore.Book               `json:"book,omitempty"`
	Next    string                         `json:"next,omitempty"`
	Meta    map[string]string              `json:"meta,omitempty"`
}

func (c *PlatformNovelsAPIController) requireServices(rw http.ResponseWriter) bool {
	if c.novelService == nil {
		http.Error(rw, "novel store service is not enabled", http.StatusNotImplemented)
		return false
	}
	if c.uploadService == nil {
		http.Error(rw, "platform upload service is not enabled", http.StatusNotImplemented)
		return false
	}
	return true
}

func (c *PlatformNovelsAPIController) ListBooksHandler(rw http.ResponseWriter, req *http.Request) {
	if c.novelService == nil {
		http.Error(rw, "novel store service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	q := req.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	books, err := c.novelService.ListBooks(req.Context(), novelstore.ListBooksRequest{
		TenantID:  p.TenantID,
		ProjectID: mux.Vars(req)["project_id"],
		Status:    q.Get("status"),
		Limit:     limit,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(books, http.StatusOK, rw)
}

func (c *PlatformNovelsAPIController) GetBookHandler(rw http.ResponseWriter, req *http.Request) {
	if c.novelService == nil {
		http.Error(rw, "novel store service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	vars := mux.Vars(req)
	book, err := c.novelService.GetBook(req.Context(), p.TenantID, vars["project_id"], vars["book_id"])
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(book, http.StatusOK, rw)
}

func (c *PlatformNovelsAPIController) ImportFileHandler(rw http.ResponseWriter, req *http.Request) {
	if !c.requireServices(rw) {
		return
	}
	p := auth.FromContext(req.Context())
	projectID := mux.Vars(req)["project_id"]
	if strings.TrimSpace(projectID) == "" {
		http.Error(rw, "project_id is required", http.StatusBadRequest)
		return
	}
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
	upload, err := c.uploadService.Create(req.Context(), uploads.CreateRequest{
		TenantID:     p.TenantID,
		UserID:       firstNonEmptyString(req.FormValue("user_id"), p.UserID),
		ProjectID:    projectID,
		Purpose:      firstNonEmptyString(req.FormValue("purpose"), "novel_source"),
		OriginalName: firstNonEmptyString(req.FormValue("file_name"), header.Filename),
		MIMEType:     mimeType,
		MetadataJSON: req.FormValue("metadata_json"),
		Reader:       file,
	})
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	response, err := c.importUploadAndMaybeSplit(req.Context(), p, projectID, upload.ID, importOptions{
		Title:                req.FormValue("title"),
		Author:               req.FormValue("author"),
		EncodingHint:         req.FormValue("encoding_hint"),
		MinChapterChars:      parseInt(req.FormValue("min_chapter_chars")),
		PreviewLimit:         parseInt(req.FormValue("preview_limit")),
		ChapterTitlePatterns: parseStringList(req.FormValue("chapter_title_patterns")),
		LeadingContentPolicy: req.FormValue("leading_content_policy"),
		AutoCommit:           parseBoolDefault(req.FormValue("auto_commit"), true),
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	response.Upload = upload
	EncodeJSONResponse(response, http.StatusCreated, rw)
}

func (c *PlatformNovelsAPIController) ImportUploadHandler(rw http.ResponseWriter, req *http.Request) {
	if !c.requireServices(rw) {
		return
	}
	p := auth.FromContext(req.Context())
	projectID := mux.Vars(req)["project_id"]
	var body struct {
		UploadID             string   `json:"upload_id"`
		Title                string   `json:"title"`
		Author               string   `json:"author"`
		EncodingHint         string   `json:"encoding_hint"`
		MinChapterChars      int      `json:"min_chapter_chars"`
		PreviewLimit         int      `json:"preview_limit"`
		ChapterTitlePatterns []string `json:"chapter_title_patterns"`
		LeadingContentPolicy string   `json:"leading_content_policy"`
		AutoCommit           *bool    `json:"auto_commit"`
	}
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(rw, err.Error(), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(body.UploadID) == "" {
		http.Error(rw, "upload_id is required", http.StatusBadRequest)
		return
	}
	autoCommit := true
	if body.AutoCommit != nil {
		autoCommit = *body.AutoCommit
	}
	response, err := c.importUploadAndMaybeSplit(req.Context(), p, projectID, body.UploadID, importOptions{
		Title:                body.Title,
		Author:               body.Author,
		EncodingHint:         body.EncodingHint,
		MinChapterChars:      body.MinChapterChars,
		PreviewLimit:         body.PreviewLimit,
		ChapterTitlePatterns: body.ChapterTitlePatterns,
		LeadingContentPolicy: body.LeadingContentPolicy,
		AutoCommit:           autoCommit,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(response, http.StatusCreated, rw)
}

func (c *PlatformNovelsAPIController) SplitPreviewHandler(rw http.ResponseWriter, req *http.Request) {
	if c.novelService == nil {
		http.Error(rw, "novel store service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	vars := mux.Vars(req)
	var body struct {
		ChapterTitlePatterns []string `json:"chapter_title_patterns"`
		MinChapterChars      int      `json:"min_chapter_chars"`
		LeadingContentPolicy string   `json:"leading_content_policy"`
		PreviewLimit         int      `json:"preview_limit"`
	}
	if req.Body != nil {
		_ = json.NewDecoder(req.Body).Decode(&body)
	}
	preview, err := c.novelService.SplitPreview(req.Context(), novelstore.SplitPreviewRequest{
		TenantID:             p.TenantID,
		ProjectID:            vars["project_id"],
		BookID:               vars["book_id"],
		ChapterTitlePatterns: body.ChapterTitlePatterns,
		MinChapterChars:      body.MinChapterChars,
		LeadingContentPolicy: body.LeadingContentPolicy,
		PreviewLimit:         body.PreviewLimit,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(preview, http.StatusOK, rw)
}

func (c *PlatformNovelsAPIController) SplitCommitHandler(rw http.ResponseWriter, req *http.Request) {
	if c.novelService == nil {
		http.Error(rw, "novel store service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	vars := mux.Vars(req)
	var body struct {
		ChapterTitlePatterns []string `json:"chapter_title_patterns"`
		MinChapterChars      int      `json:"min_chapter_chars"`
		LeadingContentPolicy string   `json:"leading_content_policy"`
		SupersedeActive      *bool    `json:"supersede_active"`
	}
	if req.Body != nil {
		_ = json.NewDecoder(req.Body).Decode(&body)
	}
	supersede := true
	if body.SupersedeActive != nil {
		supersede = *body.SupersedeActive
	}
	split, err := c.novelService.SplitCommit(req.Context(), novelstore.SplitCommitRequest{
		TenantID:             p.TenantID,
		ProjectID:            vars["project_id"],
		BookID:               vars["book_id"],
		ChapterTitlePatterns: body.ChapterTitlePatterns,
		MinChapterChars:      body.MinChapterChars,
		LeadingContentPolicy: body.LeadingContentPolicy,
		SupersedeActive:      supersede,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(split, http.StatusCreated, rw)
}

func (c *PlatformNovelsAPIController) ListChaptersHandler(rw http.ResponseWriter, req *http.Request) {
	if c.novelService == nil {
		http.Error(rw, "novel store service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	vars := mux.Vars(req)
	q := req.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	chapters, err := c.novelService.ListChapters(req.Context(), novelstore.ListChaptersRequest{
		TenantID:  p.TenantID,
		ProjectID: vars["project_id"],
		BookID:    vars["book_id"],
		SplitID:   q.Get("split_id"),
		Status:    q.Get("status"),
		Limit:     limit,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(chapters, http.StatusOK, rw)
}

func (c *PlatformNovelsAPIController) GetChapterHandler(rw http.ResponseWriter, req *http.Request) {
	if c.novelService == nil {
		http.Error(rw, "novel store service is not enabled", http.StatusNotImplemented)
		return
	}
	p := auth.FromContext(req.Context())
	vars := mux.Vars(req)
	q := req.URL.Query()
	chapterNo, _ := strconv.Atoi(vars["chapter_no"])
	maxChars, _ := strconv.Atoi(q.Get("max_chars"))
	result, err := c.novelService.GetChapter(req.Context(), novelstore.GetChapterRequest{
		TenantID:        p.TenantID,
		ProjectID:       vars["project_id"],
		BookID:          vars["book_id"],
		SplitID:         q.Get("split_id"),
		ChapterNo:       chapterNo,
		IncludePrevTail: q.Get("include_prev_tail") == "true",
		IncludeNextHead: q.Get("include_next_head") == "true",
		MaxChars:        maxChars,
	})
	if err != nil {
		writePlatformError(rw, err)
		return
	}
	EncodeJSONResponse(result, http.StatusOK, rw)
}

type importOptions struct {
	Title                string
	Author               string
	EncodingHint         string
	MinChapterChars      int
	PreviewLimit         int
	ChapterTitlePatterns []string
	LeadingContentPolicy string
	AutoCommit           bool
}

func (c *PlatformNovelsAPIController) importUploadAndMaybeSplit(ctx context.Context, p auth.Principal, projectID, uploadID string, opts importOptions) (*NovelIngestResponse, error) {
	upload, err := c.uploadService.Get(ctx, p.TenantID, strings.TrimSpace(uploadID))
	if err != nil {
		return nil, err
	}
	if upload.ProjectID != "" && upload.ProjectID != projectID {
		return nil, fmt.Errorf("upload %s belongs to project_id=%s, not requested project_id=%s", upload.ID, upload.ProjectID, projectID)
	}
	r, _, err := c.uploadService.Open(ctx, p.TenantID, upload.ID)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	imported, err := c.novelService.ImportUpload(ctx, novelstore.ImportUploadRequest{
		TenantID:     p.TenantID,
		ProjectID:    projectID,
		OwnerUserID:  firstNonEmptyString(upload.UserID, p.UserID),
		UploadID:     upload.ID,
		OriginalName: upload.OriginalName,
		Title:        opts.Title,
		Author:       opts.Author,
		EncodingHint: opts.EncodingHint,
		Reader:       r,
		SizeBytes:    upload.SizeBytes,
		SourceSHA256: upload.SHA256,
	})
	if err != nil {
		return nil, err
	}
	preview, err := c.novelService.SplitPreview(ctx, novelstore.SplitPreviewRequest{
		TenantID:             p.TenantID,
		ProjectID:            projectID,
		BookID:               imported.Book.ID,
		ChapterTitlePatterns: opts.ChapterTitlePatterns,
		MinChapterChars:      opts.MinChapterChars,
		LeadingContentPolicy: opts.LeadingContentPolicy,
		PreviewLimit:         opts.PreviewLimit,
	})
	if err != nil {
		return nil, err
	}
	var split *novelstore.SplitResult
	if opts.AutoCommit {
		split, err = c.novelService.SplitCommit(ctx, novelstore.SplitCommitRequest{
			TenantID:             p.TenantID,
			ProjectID:            projectID,
			BookID:               imported.Book.ID,
			ChapterTitlePatterns: opts.ChapterTitlePatterns,
			MinChapterChars:      opts.MinChapterChars,
			LeadingContentPolicy: opts.LeadingContentPolicy,
			SupersedeActive:      true,
		})
		if err != nil {
			return nil, err
		}
	}
	book, _ := c.novelService.GetBook(ctx, p.TenantID, projectID, imported.Book.ID)
	return &NovelIngestResponse{
		Import:  imported,
		Preview: preview,
		Split:   split,
		Book:    firstNonNilBook(book, imported.Book),
		Next:    "book_id/split_id/chapter_no are now stable inputs for other agents and sessions; use NovelStoreToolset to read chapters without re-uploading or re-splitting",
		Meta: map[string]string{
			"pipeline": "upload->utf8_normalize->deterministic_split->novelstore",
		},
	}, nil
}

func parseInt(v string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

func parseBoolDefault(v string, def bool) bool {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func parseStringList(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	var arr []string
	if strings.HasPrefix(v, "[") {
		if err := json.Unmarshal([]byte(v), &arr); err == nil {
			return trimStringList(arr)
		}
	}
	parts := strings.FieldsFunc(v, func(r rune) bool { return r == '\n' || r == '\r' })
	return trimStringList(parts)
}

func trimStringList(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, strings.TrimSpace(v))
		}
	}
	return out
}

func firstNonNilBook(values ...*novelstore.Book) *novelstore.Book {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

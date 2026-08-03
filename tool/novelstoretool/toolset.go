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

// Package novelstoretool exposes deterministic novel storage tools. Agents use
// these tools for book/source/split/chapter lifecycle operations instead of
// directly reading/writing object storage paths or saving large chapters as generic artifacts.
package novelstoretool

import (
	"fmt"
	"strconv"
	"strings"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/platform/novelstore"
	"google.golang.org/adk/internal/platform/objectstore"
	"google.golang.org/adk/internal/platform/store"
	"google.golang.org/adk/internal/platform/uploads"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/adk/tool/projectartifacttool"
)

// NewToolset creates the NovelStore toolset.
func NewToolset() (tool.Toolset, error) {
	ts := &Toolset{}
	builders := []func() (tool.Tool, error){
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{Name: "novel_import_upload", Description: "Import a Platform Upload from the current workspace into NovelStore as a book source stored in the configured ObjectStore. Does not expose the full file to model context."}, ts.ImportUpload)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{Name: "novel_ingest_upload", Description: "Deterministically import a Platform Upload, normalize it to UTF-8, split chapters, and optionally commit the split in one backend operation. This is programmatic preprocessing, not model analysis."}, ts.IngestUpload)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{Name: "novel_split_preview", Description: "Preview deterministic novel chapter splitting without saving chapters. Show chapter count, first titles, and warnings before committing."}, ts.SplitPreview)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{Name: "novel_split_commit", Description: "Commit a confirmed split into NovelStore: create split version, manifest, chapter objects, and mark the new split active.", RequireConfirmation: true}, ts.SplitCommit)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{Name: "novel_list_books", Description: "List NovelStore books in the current workspace and their active split pointers."}, ts.ListBooks)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{Name: "novel_get_book", Description: "Get one NovelStore book in the current workspace by book_id."}, ts.GetBook)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{Name: "novel_list_chapters", Description: "List chapters for a book's active split or a specified split without returning full chapter text."}, ts.ListChapters)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{Name: "novel_get_chapter", Description: "Read one bounded chapter from NovelStore. Use max_chars and pass book_id/split_id/chapter_no to downstream agents instead of pasting full books."}, ts.GetChapter)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{Name: "novel_delete_split", Description: "Soft-delete a wrong split version and optionally delete its ObjectStore objects after user confirmation.", RequireConfirmation: true}, ts.DeleteSplit)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{Name: "novel_delete_book", Description: "Soft-delete a NovelStore book and optionally delete all book objects from ObjectStore/MinIO after user confirmation.", RequireConfirmation: true}, ts.DeleteBook)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{Name: "novel_save_artifact", Description: "Save a novel-domain artifact such as chapter_analysis, gap_report, or chapter_skill_pack under book/project metadata and ObjectStore."}, ts.SaveArtifact)
		},
	}
	for _, build := range builders {
		tl, err := build()
		if err != nil {
			return nil, err
		}
		ts.tools = append(ts.tools, tl)
	}
	return ts, nil
}

// Toolset groups NovelStore tools.
type Toolset struct{ tools []tool.Tool }

func (t *Toolset) Name() string { return "NovelStoreToolset" }

func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) { return t.tools, nil }

type ImportUploadArgs struct {
	TenantID     string `json:"tenant_id,omitempty" jsonschema:"Optional tenant id. Defaults to default."`
	UploadID     string `json:"upload_id" jsonschema:"Platform upload id to import."`
	Title        string `json:"title,omitempty" jsonschema:"Optional book title. Defaults from upload filename."`
	Author       string `json:"author,omitempty"`
	EncodingHint string `json:"encoding_hint,omitempty" jsonschema:"auto, utf-8, gbk, gb18030."`
}

type IngestUploadArgs struct {
	TenantID             string   `json:"tenant_id,omitempty" jsonschema:"Optional tenant id. Defaults to default."`
	UploadID             string   `json:"upload_id" jsonschema:"Platform upload id to import."`
	Title                string   `json:"title,omitempty" jsonschema:"Optional book title. Defaults from upload filename."`
	Author               string   `json:"author,omitempty"`
	EncodingHint         string   `json:"encoding_hint,omitempty" jsonschema:"auto, utf-8, gbk, gb18030."`
	ChapterTitlePatterns []string `json:"chapter_title_patterns,omitempty" jsonschema:"Optional Go regexp patterns for chapter title lines."`
	MinChapterChars      int      `json:"min_chapter_chars,omitempty"`
	LeadingContentPolicy string   `json:"leading_content_policy,omitempty" jsonschema:"How to handle text before the first detected chapter title: auto, keep, merge, or drop. Use drop when the user says intro/preface/front matter should not become chapter 1."`
	PreviewLimit         int      `json:"preview_limit,omitempty" jsonschema:"Number of detected chapter titles to return. Defaults to 20."`
	AutoCommit           *bool    `json:"auto_commit,omitempty" jsonschema:"If true, commit the split immediately after preview. Defaults true."`
}

type BookPublic struct {
	ID             string `json:"id"`
	Title          string `json:"title"`
	Author         string `json:"author,omitempty"`
	SourceUploadID string `json:"source_upload_id,omitempty"`
	CurrentSplitID string `json:"current_split_id,omitempty"`
	ChapterCount   int    `json:"chapter_count"`
	TotalChars     int    `json:"total_chars"`
	SizeBytes      int64  `json:"size_bytes"`
	SHA256         string `json:"sha256,omitempty"`
	Encoding       string `json:"encoding,omitempty"`
	Status         string `json:"status"`
}

type ImportUploadPublic struct {
	Book       *BookPublic `json:"book"`
	Encoding   string      `json:"encoding"`
	Warnings   []string    `json:"warnings,omitempty"`
	NextAction string      `json:"next_action"`
}

type ChapterPreviewPublic struct {
	No        int    `json:"no"`
	Title     string `json:"title"`
	StartChar int    `json:"start_char"`
	EndChar   int    `json:"end_char"`
	CharCount int    `json:"char_count"`
	ChapterID string `json:"chapter_id,omitempty"`
}

type SplitResultPublic struct {
	BookID               string                 `json:"book_id"`
	SplitID              string                 `json:"split_id,omitempty"`
	Title                string                 `json:"title"`
	ChapterCount         int                    `json:"chapter_count"`
	TotalChars           int                    `json:"total_chars"`
	TotalBytes           int64                  `json:"total_bytes"`
	SplitMethod          string                 `json:"split_method"`
	LeadingContentPolicy string                 `json:"leading_content_policy,omitempty"`
	Warnings             []string               `json:"warnings,omitempty"`
	ChaptersPreview      []ChapterPreviewPublic `json:"chapters_preview"`
	Status               string                 `json:"status,omitempty"`
}

type ChapterPublic struct {
	ID        string `json:"id"`
	BookID    string `json:"book_id"`
	SplitID   string `json:"split_id"`
	ChapterNo int    `json:"chapter_no"`
	Title     string `json:"title"`
	CharCount int    `json:"char_count"`
	ByteCount int64  `json:"byte_count"`
	SHA256    string `json:"sha256,omitempty"`
	StartChar int    `json:"start_char"`
	EndChar   int    `json:"end_char"`
	Status    string `json:"status"`
}

type ChapterContentPublic struct {
	Chapter       ChapterPublic `json:"chapter"`
	Content       string        `json:"content"`
	Truncated     bool          `json:"truncated"`
	PrevTail      string        `json:"prev_tail,omitempty"`
	NextHead      string        `json:"next_head,omitempty"`
	SafetyMessage string        `json:"safety_message"`
}

type ArtifactPublic struct {
	ID        string `json:"id"`
	BookID    string `json:"book_id"`
	SplitID   string `json:"split_id,omitempty"`
	ChapterID string `json:"chapter_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Title     string `json:"title,omitempty"`
	MIMEType  string `json:"mime_type,omitempty"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256,omitempty"`
	Status    string `json:"status"`
}

type IngestUploadResult struct {
	Import              *ImportUploadPublic `json:"import"`
	Preview             *SplitResultPublic  `json:"preview"`
	Split               *SplitResultPublic  `json:"split,omitempty"`
	Book                *BookPublic         `json:"book,omitempty"`
	ProjectStateWarning string              `json:"project_state_warning,omitempty"`
	Next                string              `json:"next"`
}

type SplitPreviewArgs struct {
	TenantID             string   `json:"tenant_id,omitempty"`
	BookID               string   `json:"book_id"`
	ChapterTitlePatterns []string `json:"chapter_title_patterns,omitempty" jsonschema:"Optional Go regexp patterns for chapter title lines."`
	MinChapterChars      int      `json:"min_chapter_chars,omitempty"`
	LeadingContentPolicy string   `json:"leading_content_policy,omitempty" jsonschema:"How to handle text before the first detected chapter title: auto, keep, merge, or drop."`
	PreviewLimit         int      `json:"preview_limit,omitempty" jsonschema:"Number of detected chapter titles to return. Defaults to 20."`
}

type SplitCommitArgs struct {
	TenantID             string   `json:"tenant_id,omitempty"`
	BookID               string   `json:"book_id"`
	ChapterTitlePatterns []string `json:"chapter_title_patterns,omitempty"`
	MinChapterChars      int      `json:"min_chapter_chars,omitempty"`
	LeadingContentPolicy string   `json:"leading_content_policy,omitempty" jsonschema:"How to handle text before the first detected chapter title: auto, keep, merge, or drop."`
	ReplaceActive        bool     `json:"replace_active,omitempty" jsonschema:"Only true when the user explicitly asks to re-split/replace the current active split. Default false makes commit idempotent."`
}

type ListBooksArgs struct {
	TenantID string `json:"tenant_id,omitempty"`
	Status   string `json:"status,omitempty" jsonschema:"active, archived, deleted, all. Defaults active."`
	Limit    int    `json:"limit,omitempty"`
}

type GetBookArgs struct {
	TenantID string `json:"tenant_id,omitempty"`
	BookID   string `json:"book_id"`
}

type ListChaptersArgs struct {
	TenantID string `json:"tenant_id,omitempty"`
	BookID   string `json:"book_id"`
	SplitID  string `json:"split_id,omitempty" jsonschema:"active or concrete split id. Defaults active."`
	Status   string `json:"status,omitempty"`
	Limit    int    `json:"limit,omitempty"`
}

type GetChapterArgs struct {
	TenantID         string `json:"tenant_id,omitempty"`
	BookID           string `json:"book_id"`
	SplitID          string `json:"split_id,omitempty" jsonschema:"active or concrete split id. Defaults active."`
	ChapterNo        int    `json:"chapter_no,omitempty"`
	ChapterID        string `json:"chapter_id,omitempty"`
	IncludePrevTail  bool   `json:"include_prev_tail,omitempty"`
	IncludeNextHead  bool   `json:"include_next_head,omitempty"`
	NeighborMaxChars int    `json:"neighbor_max_chars,omitempty"`
	MaxChars         int    `json:"max_chars,omitempty" jsonschema:"Maximum chapter chars returned to the model. Use a bounded value for large chapters."`
}

type DeleteSplitArgs struct {
	TenantID      string `json:"tenant_id,omitempty"`
	BookID        string `json:"book_id"`
	SplitID       string `json:"split_id"`
	Force         bool   `json:"force,omitempty" jsonschema:"Required to delete current active split after explicit user confirmation."`
	DeleteObjects bool   `json:"delete_objects,omitempty" jsonschema:"If true, also delete ObjectStore files. Defaults false."`
}

type DeleteBookArgs struct {
	TenantID      string `json:"tenant_id,omitempty"`
	BookID        string `json:"book_id"`
	DeleteObjects bool   `json:"delete_objects,omitempty" jsonschema:"If true, also delete ObjectStore files. Defaults false."`
}

type SaveArtifactArgs struct {
	TenantID     string `json:"tenant_id,omitempty"`
	BookID       string `json:"book_id"`
	SplitID      string `json:"split_id,omitempty"`
	ChapterID    string `json:"chapter_id,omitempty"`
	RunID        string `json:"run_id,omitempty"`
	Kind         string `json:"kind" jsonschema:"chapter_analysis, chapter_skill_pack, gap_report, batch_summary, export_package, etc."`
	Name         string `json:"name,omitempty"`
	Title        string `json:"title,omitempty"`
	MIMEType     string `json:"mime_type,omitempty"`
	Content      string `json:"content" jsonschema:"Artifact content. Keep this for structured reports/skill packs, not raw full novels."`
	MetadataJSON string `json:"metadata_json,omitempty"`
}

func (t *Toolset) ImportUpload(ctx tool.Context, args ImportUploadArgs) (*ImportUploadPublic, error) {
	ns, upSvc, err := servicesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveCurrentProjectID(ctx, "")
	if err != nil {
		return nil, err
	}
	r, upload, err := upSvc.Open(ctx, tenant(args.TenantID), strings.TrimSpace(args.UploadID))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if upload.ProjectID != "" && upload.ProjectID != projectID {
		return nil, fmt.Errorf("upload %s does not belong to the current workspace; select the matching project in the top project selector", upload.ID)
	}
	result, err := ns.ImportUpload(ctx, novelstore.ImportUploadRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, OwnerUserID: firstNonEmpty(upload.UserID, ctx.UserID()), UploadID: upload.ID, OriginalName: upload.OriginalName, Title: args.Title, Author: args.Author, EncodingHint: args.EncodingHint, Reader: r, SizeBytes: upload.SizeBytes, SourceSHA256: upload.SHA256})
	if err != nil {
		return nil, err
	}
	return publicImportUpload(result), nil
}

func (t *Toolset) IngestUpload(ctx tool.Context, args IngestUploadArgs) (*IngestUploadResult, error) {
	ns, upSvc, err := servicesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveCurrentProjectID(ctx, "")
	if err != nil {
		return nil, err
	}
	r, upload, err := upSvc.Open(ctx, tenant(args.TenantID), strings.TrimSpace(args.UploadID))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if upload.ProjectID != "" && upload.ProjectID != projectID {
		return nil, fmt.Errorf("upload %s does not belong to the current workspace; select the matching project in the top project selector", upload.ID)
	}
	imported, err := ns.ImportUpload(ctx, novelstore.ImportUploadRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, OwnerUserID: firstNonEmpty(upload.UserID, ctx.UserID()), UploadID: upload.ID, OriginalName: upload.OriginalName, Title: args.Title, Author: args.Author, EncodingHint: args.EncodingHint, Reader: r, SizeBytes: upload.SizeBytes, SourceSHA256: upload.SHA256})
	if err != nil {
		return nil, err
	}
	_, projectStateWarning := registerNovelProjectState(ctx, projectID, imported.Book, nil)
	preview, err := ns.SplitPreview(ctx, novelstore.SplitPreviewRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, BookID: imported.Book.ID, ChapterTitlePatterns: args.ChapterTitlePatterns, MinChapterChars: args.MinChapterChars, LeadingContentPolicy: args.LeadingContentPolicy, PreviewLimit: args.PreviewLimit})
	if err != nil {
		return nil, err
	}
	autoCommit := false
	if args.AutoCommit != nil {
		autoCommit = *args.AutoCommit
	}
	if imported.Book != nil && imported.Book.CurrentSplitID != "" {
		current, warn := currentActiveSplitResult(ctx, ns, tenant(args.TenantID), projectID, imported.Book)
		if warn != "" {
			preview.Warnings = append(preview.Warnings, warn)
		}
		_, projectStateWarning = registerNovelProjectState(ctx, projectID, imported.Book, current)
		return &IngestUploadResult{Import: publicImportUpload(imported), Preview: publicSplit(preview), Split: publicSplit(current), Book: publicBook(imported.Book), ProjectStateWarning: projectStateWarning, Next: "already_active: this upload already has an active split; reuse book_id/split_id and do not call novel_split_commit again unless the user explicitly asks to re-split"}, nil
	}
	if !autoCommit {
		return &IngestUploadResult{Import: publicImportUpload(imported), Preview: publicSplit(preview), Book: publicBook(imported.Book), ProjectStateWarning: projectStateWarning, Next: "preview_only: show the preview and call novel_split_commit only after user confirmation"}, nil
	}
	split, err := ns.SplitCommit(ctx, novelstore.SplitCommitRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, BookID: imported.Book.ID, ChapterTitlePatterns: args.ChapterTitlePatterns, MinChapterChars: args.MinChapterChars, LeadingContentPolicy: args.LeadingContentPolicy, SupersedeActive: true})
	if err != nil {
		return nil, err
	}
	book, _ := ns.GetBook(ctx, tenant(args.TenantID), projectID, imported.Book.ID)
	_, projectStateWarning = registerNovelProjectState(ctx, projectID, firstNonNilBook(book, imported.Book), split)
	return &IngestUploadResult{Import: publicImportUpload(imported), Preview: publicSplit(preview), Split: publicSplit(split), Book: publicBook(firstNonNilBook(book, imported.Book)), ProjectStateWarning: projectStateWarning, Next: "done: active split is available; do not call novel_split_commit again"}, nil
}

func (t *Toolset) SplitPreview(ctx tool.Context, args SplitPreviewArgs) (*SplitResultPublic, error) {
	ns, _, err := servicesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveCurrentProjectID(ctx, "")
	if err != nil {
		return nil, err
	}
	split, err := ns.SplitPreview(ctx, novelstore.SplitPreviewRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, BookID: args.BookID, ChapterTitlePatterns: args.ChapterTitlePatterns, MinChapterChars: args.MinChapterChars, LeadingContentPolicy: args.LeadingContentPolicy, PreviewLimit: args.PreviewLimit})
	if err != nil {
		return nil, err
	}
	return publicSplit(split), nil
}

func (t *Toolset) SplitCommit(ctx tool.Context, args SplitCommitArgs) (*SplitResultPublic, error) {
	ns, _, err := servicesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveCurrentProjectID(ctx, "")
	if err != nil {
		return nil, err
	}
	if book, err := ns.GetBook(ctx, tenant(args.TenantID), projectID, args.BookID); err == nil && book.CurrentSplitID != "" && !args.ReplaceActive {
		current, warn := currentActiveSplitResult(ctx, ns, tenant(args.TenantID), projectID, book)
		if current == nil {
			return nil, fmt.Errorf("book %s already has active split %s; pass replace_active=true only after the user explicitly asks to re-split", book.ID, book.CurrentSplitID)
		}
		current.Warnings = append(current.Warnings, "already_active: split commit skipped because the book already has an active split; reuse current split_id instead of cutting again")
		if warn != "" {
			current.Warnings = append(current.Warnings, warn)
		}
		if _, warning := registerNovelProjectState(ctx, projectID, book, current); warning != "" {
			current.Warnings = append(current.Warnings, "project_state_register_failed: "+warning)
		}
		return publicSplit(current), nil
	}
	split, err := ns.SplitCommit(ctx, novelstore.SplitCommitRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, BookID: args.BookID, ChapterTitlePatterns: args.ChapterTitlePatterns, MinChapterChars: args.MinChapterChars, LeadingContentPolicy: args.LeadingContentPolicy, SupersedeActive: true})
	if err != nil {
		return nil, err
	}
	book, _ := ns.GetBook(ctx, tenant(args.TenantID), projectID, args.BookID)
	if _, warning := registerNovelProjectState(ctx, projectID, firstNonNilBook(book, &novelstore.Book{ID: args.BookID, ProjectID: projectID}), split); warning != "" {
		split.Warnings = append(split.Warnings, "project_state_register_failed: "+warning)
	}
	return publicSplit(split), nil
}

func (t *Toolset) ListBooks(ctx tool.Context, args ListBooksArgs) ([]BookPublic, error) {
	ns, _, err := servicesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveCurrentProjectID(ctx, "")
	if err != nil {
		return nil, err
	}
	books, err := ns.ListBooks(ctx, novelstore.ListBooksRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, Status: args.Status, Limit: args.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]BookPublic, 0, len(books))
	for i := range books {
		if books[i].CurrentSplitID != "" {
			current, _ := currentActiveSplitResult(ctx, ns, tenant(args.TenantID), projectID, &books[i])
			_, _ = registerNovelProjectState(ctx, projectID, &books[i], current)
		}
		out = append(out, *publicBook(&books[i]))
	}
	return out, nil
}

func (t *Toolset) GetBook(ctx tool.Context, args GetBookArgs) (*BookPublic, error) {
	ns, _, err := servicesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveCurrentProjectID(ctx, "")
	if err != nil {
		return nil, err
	}
	book, err := ns.GetBook(ctx, tenant(args.TenantID), projectID, args.BookID)
	if err != nil {
		return nil, err
	}
	if book.CurrentSplitID != "" {
		current, _ := currentActiveSplitResult(ctx, ns, tenant(args.TenantID), projectID, book)
		_, _ = registerNovelProjectState(ctx, projectID, book, current)
	}
	return publicBook(book), nil
}

func (t *Toolset) ListChapters(ctx tool.Context, args ListChaptersArgs) ([]ChapterPublic, error) {
	ns, _, err := servicesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveCurrentProjectID(ctx, "")
	if err != nil {
		return nil, err
	}
	chapters, err := ns.ListChapters(ctx, novelstore.ListChaptersRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, BookID: args.BookID, SplitID: args.SplitID, Status: args.Status, Limit: args.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]ChapterPublic, 0, len(chapters))
	for i := range chapters {
		out = append(out, publicChapter(chapters[i]))
	}
	return out, nil
}

func (t *Toolset) GetChapter(ctx tool.Context, args GetChapterArgs) (*ChapterContentPublic, error) {
	ns, _, err := servicesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveCurrentProjectID(ctx, "")
	if err != nil {
		return nil, err
	}
	result, err := ns.GetChapter(ctx, novelstore.GetChapterRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, BookID: args.BookID, SplitID: args.SplitID, ChapterNo: args.ChapterNo, ChapterID: args.ChapterID, IncludePrevTail: args.IncludePrevTail, IncludeNextHead: args.IncludeNextHead, NeighborMaxChars: args.NeighborMaxChars, MaxChars: args.MaxChars})
	if err != nil {
		return nil, err
	}
	return publicChapterContent(result), nil
}

func (t *Toolset) DeleteSplit(ctx tool.Context, args DeleteSplitArgs) (*novelstore.DeleteResult, error) {
	ns, _, err := servicesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveCurrentProjectID(ctx, "")
	if err != nil {
		return nil, err
	}
	return ns.DeleteSplit(ctx, novelstore.DeleteSplitRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, BookID: args.BookID, SplitID: args.SplitID, Force: args.Force, DeleteObjects: args.DeleteObjects})
}

func (t *Toolset) DeleteBook(ctx tool.Context, args DeleteBookArgs) (*novelstore.DeleteResult, error) {
	ns, _, err := servicesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveCurrentProjectID(ctx, "")
	if err != nil {
		return nil, err
	}
	return ns.DeleteBook(ctx, novelstore.DeleteBookRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, BookID: args.BookID, DeleteObjects: args.DeleteObjects})
}

func (t *Toolset) SaveArtifact(ctx tool.Context, args SaveArtifactArgs) (*ArtifactPublic, error) {
	ns, _, err := servicesFromContext(ctx)
	if err != nil {
		return nil, err
	}
	projectID, err := resolveCurrentProjectID(ctx, "")
	if err != nil {
		return nil, err
	}
	artifact, err := ns.SaveArtifact(ctx, novelstore.SaveArtifactRequest{TenantID: tenant(args.TenantID), ProjectID: projectID, BookID: args.BookID, SplitID: args.SplitID, ChapterID: args.ChapterID, RunID: firstNonEmpty(args.RunID, ctx.InvocationID()), Kind: args.Kind, Name: args.Name, Title: args.Title, MIMEType: args.MIMEType, Content: []byte(args.Content), MetadataJSON: args.MetadataJSON})
	if err != nil {
		return nil, err
	}
	return publicArtifact(artifact), nil
}

func resolveCurrentProjectID(ctx tool.Context, explicit string) (string, error) {
	projectID, err := projectartifacttool.ResolveProjectID(ctx, explicit)
	if err != nil {
		return "", fmt.Errorf("current workspace is not selected; choose a project in the top project selector before using NovelStore tools: %w", err)
	}
	return projectID, nil
}

func servicesFromContext(ctx tool.Context) (*novelstore.Service, uploads.Service, error) {
	cfg := runtimeconfig.FromContext(ctx)
	if cfg == nil {
		return nil, nil, fmt.Errorf("runtime config is not available")
	}
	db, err := store.OpenGORM(cfg.Storage.Database)
	if err != nil {
		return nil, nil, fmt.Errorf("open platform database: %w", err)
	}
	if cfg.Storage.Database.AutoMigrate {
		if err := uploads.AutoMigrate(db); err != nil {
			return nil, nil, fmt.Errorf("migrate platform uploads: %w", err)
		}
		if err := novelstore.AutoMigrate(db); err != nil {
			return nil, nil, fmt.Errorf("migrate novelstore: %w", err)
		}
	}
	obj, err := objectstore.FromRuntimeConfig(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("open object store: %w", err)
	}
	return novelstore.NewService(db, obj), uploads.NewService(db, cfg.Storage.Upload.Root), nil
}

func currentActiveSplitResult(ctx tool.Context, ns *novelstore.Service, tenantID, projectID string, book *novelstore.Book) (*novelstore.SplitResult, string) {
	if book == nil || strings.TrimSpace(book.CurrentSplitID) == "" {
		return nil, ""
	}
	chapters, err := ns.ListChapters(ctx, novelstore.ListChaptersRequest{TenantID: tenantID, ProjectID: projectID, BookID: book.ID, SplitID: book.CurrentSplitID, Limit: 20})
	if err != nil {
		return &novelstore.SplitResult{BookID: book.ID, ProjectID: projectID, SplitID: book.CurrentSplitID, Title: book.Title, ChapterCount: book.ChapterCount, TotalChars: book.TotalChars, TotalBytes: book.SizeBytes, Status: "already_active"}, "list current active chapters failed: " + err.Error()
	}
	preview := make([]novelstore.ChapterSummary, 0, len(chapters))
	for _, ch := range chapters {
		preview = append(preview, novelstore.ChapterSummary{No: ch.ChapterNo, Title: ch.Title, StartChar: ch.StartChar, EndChar: ch.EndChar, CharCount: ch.CharCount, ObjectKey: ch.ObjectKey, ChapterID: ch.ID})
	}
	return &novelstore.SplitResult{BookID: book.ID, ProjectID: projectID, SplitID: book.CurrentSplitID, Title: book.Title, ChapterCount: book.ChapterCount, TotalChars: book.TotalChars, TotalBytes: book.SizeBytes, ChaptersPreview: preview, Status: "already_active"}, ""
}

func publicImportUpload(in *novelstore.ImportUploadResult) *ImportUploadPublic {
	if in == nil {
		return nil
	}
	return &ImportUploadPublic{Book: publicBook(in.Book), Encoding: in.Encoding, Warnings: in.Warnings, NextAction: in.NextAction}
}

func publicBook(in *novelstore.Book) *BookPublic {
	if in == nil {
		return nil
	}
	return &BookPublic{ID: in.ID, Title: in.Title, Author: in.Author, SourceUploadID: in.SourceUploadID, CurrentSplitID: in.CurrentSplitID, ChapterCount: in.ChapterCount, TotalChars: in.TotalChars, SizeBytes: in.SizeBytes, SHA256: in.SHA256, Encoding: in.Encoding, Status: in.Status}
}

func publicSplit(in *novelstore.SplitResult) *SplitResultPublic {
	if in == nil {
		return nil
	}
	chapters := make([]ChapterPreviewPublic, 0, len(in.ChaptersPreview))
	for _, ch := range in.ChaptersPreview {
		chapters = append(chapters, ChapterPreviewPublic{No: ch.No, Title: ch.Title, StartChar: ch.StartChar, EndChar: ch.EndChar, CharCount: ch.CharCount, ChapterID: ch.ChapterID})
	}
	return &SplitResultPublic{BookID: in.BookID, SplitID: in.SplitID, Title: in.Title, ChapterCount: in.ChapterCount, TotalChars: in.TotalChars, TotalBytes: in.TotalBytes, SplitMethod: in.SplitMethod, LeadingContentPolicy: in.LeadingContentPolicy, Warnings: in.Warnings, ChaptersPreview: chapters, Status: in.Status}
}

func publicChapter(in novelstore.Chapter) ChapterPublic {
	return ChapterPublic{ID: in.ID, BookID: in.BookID, SplitID: in.SplitID, ChapterNo: in.ChapterNo, Title: in.Title, CharCount: in.CharCount, ByteCount: in.ByteCount, SHA256: in.SHA256, StartChar: in.StartChar, EndChar: in.EndChar, Status: in.Status}
}

func publicChapterContent(in *novelstore.ChapterContentResult) *ChapterContentPublic {
	if in == nil {
		return nil
	}
	return &ChapterContentPublic{Chapter: publicChapter(in.Chapter), Content: in.Content, Truncated: in.Truncated, PrevTail: in.PrevTail, NextHead: in.NextHead, SafetyMessage: in.SafetyMessage}
}

func publicArtifact(in *novelstore.Artifact) *ArtifactPublic {
	if in == nil {
		return nil
	}
	return &ArtifactPublic{ID: in.ID, BookID: in.BookID, SplitID: in.SplitID, ChapterID: in.ChapterID, RunID: in.RunID, Kind: in.Kind, Name: in.Name, Title: in.Title, MIMEType: in.MIMEType, SizeBytes: in.SizeBytes, SHA256: in.SHA256, Status: in.Status}
}

func tenant(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return novelstore.DefaultTenantID
	}
	return v
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func firstNonNilBook(values ...*novelstore.Book) *novelstore.Book {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return nil
}

func registerNovelProjectState(ctx tool.Context, projectID string, book *novelstore.Book, split *novelstore.SplitResult) ([]projectartifacttool.ProjectArtifact, string) {
	requests := novelProjectStateRequests(projectID, book, split)
	if len(requests) == 0 {
		return nil, ""
	}
	displayName := projectID
	if book != nil {
		displayName = firstNonEmpty(book.Title, book.ID, projectID)
	}
	registry, _, err := projectartifacttool.EnsureProject(ctx, projectartifacttool.EnsureProjectRequest{
		ProjectID:   projectID,
		Name:        projectID,
		DisplayName: displayName,
		Description: "Novel project state and durable NovelStore pointers.",
		AppName:     "novel_pipeline",
		Tags:        []string{"novel", "project-state"},
		Overwrite:   false,
	})
	if err != nil {
		return nil, err.Error()
	}
	if _, err := projectartifacttool.MountProject(ctx, registry); err != nil {
		return nil, err.Error()
	}
	if book != nil && book.CurrentSplitID != "" {
		if _, _, err := projectartifacttool.RemoveArtifacts(ctx, projectID, func(art projectartifacttool.ProjectArtifact) bool {
			return art.Type == "book.chapter" || art.Type == "book.chapter_manifest"
		}); err != nil {
			return nil, err.Error()
		}
	}
	artifacts := make([]projectartifacttool.ProjectArtifact, 0, len(requests))
	for _, req := range requests {
		artifact, _, err := projectartifacttool.RegisterArtifact(ctx, req)
		if err != nil {
			return artifacts, err.Error()
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, ""
}

func novelProjectStateRequests(projectID string, book *novelstore.Book, split *novelstore.SplitResult) []projectartifacttool.RegisterArtifactRequest {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" || book == nil || strings.TrimSpace(book.ID) == "" {
		return nil
	}
	mountable := true
	defaultAgents := []string{"novel_pipeline", "novel_store_manager", "book_dissector", "book_skill_runner"}
	title := firstNonEmpty(book.Title, book.ID)
	bookMetadata := map[string]string{
		"storage":          "novelstore",
		"metadata_store":   "postgres",
		"object_store":     "filesystem",
		"book_id":          book.ID,
		"project_id":       projectID,
		"title":            title,
		"author":           book.Author,
		"source_upload_id": book.SourceUploadID,
		"current_split_id": book.CurrentSplitID,
		"chapter_count":    strconv.Itoa(book.ChapterCount),
		"total_chars":      strconv.Itoa(book.TotalChars),
		"size_bytes":       strconv.FormatInt(book.SizeBytes, 10),
		"sha256":           book.SHA256,
		"encoding":         book.Encoding,
		"status":           book.Status,
		"has_active_split": strconv.FormatBool(book.CurrentSplitID != ""),
	}
	requests := []projectartifacttool.RegisterArtifactRequest{
		{
			ProjectID:        projectID,
			ArtifactID:       "novel_book__" + book.ID,
			ArtifactName:     "novelstore:book:" + book.ID,
			Type:             "novel.book",
			Title:            title,
			Description:      "Logical NovelStore book pointer. Use NovelStoreToolset with book_id; do not load this as a normal artifact file.",
			ProducerAgent:    "novel_store_manager",
			Visibility:       projectartifacttool.VisibilityProjectDefault,
			Mountable:        &mountable,
			DefaultForAgents: defaultAgents,
			Tags:             []string{"novel", "book", "novelstore"},
			BookID:           book.ID,
			Metadata:         bookMetadata,
		},
	}
	splitID := ""
	chapterCount := 0
	totalChars := 0
	totalBytes := int64(0)
	splitMethod := ""
	warningCount := 0
	status := novelstore.StatusActive
	if split != nil {
		splitID = split.SplitID
		chapterCount = split.ChapterCount
		totalChars = split.TotalChars
		totalBytes = split.TotalBytes
		splitMethod = split.SplitMethod
		warningCount = len(split.Warnings)
		status = split.Status
	}
	if splitID == "" {
		splitID = book.CurrentSplitID
		chapterCount = book.ChapterCount
		totalChars = book.TotalChars
	}
	if splitID == "" {
		return requests
	}
	requests = append(requests, projectartifacttool.RegisterArtifactRequest{
		ProjectID:        projectID,
		ArtifactID:       "novel_active_split__" + book.ID,
		ArtifactName:     "novelstore:active_split:" + book.ID,
		Type:             "novel.active_split",
		Title:            title + " active split",
		Description:      "Current active NovelStore split pointer. Use book_id/split_id/chapter_no with NovelStoreToolset instead of repeating split.",
		ProducerAgent:    "novel_store_manager",
		Visibility:       projectartifacttool.VisibilityProjectDefault,
		Mountable:        &mountable,
		DefaultForAgents: defaultAgents,
		Tags:             []string{"novel", "split", "active", "novelstore"},
		BookID:           book.ID,
		StartChapter:     1,
		EndChapter:       chapterCount,
		Metadata: map[string]string{
			"storage":          "novelstore",
			"metadata_store":   "postgres",
			"object_store":     "filesystem",
			"book_id":          book.ID,
			"project_id":       projectID,
			"split_id":         splitID,
			"active_split_id":  splitID,
			"title":            title,
			"source_upload_id": book.SourceUploadID,
			"chapter_count":    strconv.Itoa(chapterCount),
			"total_chars":      strconv.Itoa(totalChars),
			"total_bytes":      strconv.FormatInt(totalBytes, 10),
			"split_method":     splitMethod,
			"warning_count":    strconv.Itoa(warningCount),
			"status":           status,
		},
	})
	return requests
}

var _ tool.Toolset = (*Toolset)(nil)

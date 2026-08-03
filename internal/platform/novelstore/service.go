package novelstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/text/encoding/simplifiedchinese"
	"gorm.io/gorm"

	"google.golang.org/adk/internal/platform/objectstore"
)

const (
	DefaultTenantID        = "default"
	defaultMaxImportBytes  = 512 << 20
	defaultPreviewChapters = 20

	LeadingContentAuto  = "auto"
	LeadingContentKeep  = "keep"
	LeadingContentMerge = "merge"
	LeadingContentDrop  = "drop"
)

var defaultChapterPatterns = []string{
	`(?m)^\s*第[零〇一二三四五六七八九十百千万两0-9]+[章节卷集回篇部]\s*[^\n\r]{0,80}$`,
	`(?m)^\s*Chapter\s+[0-9]+\b[^\n\r]{0,80}$`,
	`(?m)^\s*[0-9]{1,4}[、.．]\s*[^\n\r]{1,80}$`,
	`(?m)^\s*(?:序|序章|楔子|引子|前言)\s*$`,
	`(?m)^\s*[【\[〔][^】\]〕\r\n]{1,32}[】\]〕]\s*(?:楔子|序|序章|尾声?|尾|终章|番外(?:\s*\S{0,20})?|第[零〇一二三四五六七八九十百千万两0-9]+[章节卷集回篇部]?|[零〇一二三四五六七八九十百千万两壹贰叁肆伍陆柒捌玖拾0-9]{1,8})(?:\s*[：:、.．\-]\s*[^\r\n]{0,60})?\s*$`,
}

// Service coordinates novel metadata and object storage.
type Service struct {
	db    *gorm.DB
	store objectstore.Store
}

func NewService(db *gorm.DB, store objectstore.Store) *Service {
	return &Service{db: db, store: store}
}

type ImportUploadRequest struct {
	TenantID       string
	ProjectID      string
	OwnerUserID    string
	UploadID       string
	OriginalName   string
	Title          string
	Author         string
	EncodingHint   string
	Reader         io.Reader
	SizeBytes      int64
	SourceSHA256   string
	MaxImportBytes int64
	ForceNew       bool
}

type ImportUploadResult struct {
	Book       *Book    `json:"book"`
	Encoding   string   `json:"encoding"`
	Warnings   []string `json:"warnings,omitempty"`
	NextAction string   `json:"next_action"`
}

type SplitPreviewRequest struct {
	TenantID             string
	ProjectID            string
	BookID               string
	ChapterTitlePatterns []string
	MinChapterChars      int
	LeadingContentPolicy string
	PreviewLimit         int
}

type SplitCommitRequest struct {
	TenantID             string
	ProjectID            string
	BookID               string
	ChapterTitlePatterns []string
	MinChapterChars      int
	LeadingContentPolicy string
	SupersedeActive      bool
}

type ListBooksRequest struct {
	TenantID  string
	ProjectID string
	Status    string
	Limit     int
}

type ListChaptersRequest struct {
	TenantID  string
	ProjectID string
	BookID    string
	SplitID   string
	Status    string
	Limit     int
}

type GetChapterRequest struct {
	TenantID         string
	ProjectID        string
	BookID           string
	SplitID          string
	ChapterNo        int
	ChapterID        string
	IncludePrevTail  bool
	IncludeNextHead  bool
	NeighborMaxChars int
	MaxChars         int
}

type DeleteSplitRequest struct {
	TenantID      string
	ProjectID     string
	BookID        string
	SplitID       string
	Force         bool
	DeleteObjects bool
}

type DeleteBookRequest struct {
	TenantID      string
	ProjectID     string
	BookID        string
	DeleteObjects bool
}

type SaveArtifactRequest struct {
	TenantID     string
	ProjectID    string
	BookID       string
	SplitID      string
	ChapterID    string
	RunID        string
	Kind         string
	Name         string
	Title        string
	MIMEType     string
	Content      []byte
	MetadataJSON string
}

type SplitResult struct {
	BookID               string           `json:"book_id"`
	ProjectID            string           `json:"project_id"`
	SplitID              string           `json:"split_id,omitempty"`
	Title                string           `json:"title"`
	ChapterCount         int              `json:"chapter_count"`
	TotalChars           int              `json:"total_chars"`
	TotalBytes           int64            `json:"total_bytes"`
	SplitMethod          string           `json:"split_method"`
	LeadingContentPolicy string           `json:"leading_content_policy,omitempty"`
	Warnings             []string         `json:"warnings,omitempty"`
	ChaptersPreview      []ChapterSummary `json:"chapters_preview"`
	ManifestObjectKey    string           `json:"manifest_object_key,omitempty"`
	Status               string           `json:"status,omitempty"`
}

type ChapterSummary struct {
	No        int    `json:"no"`
	Title     string `json:"title"`
	StartChar int    `json:"start_char"`
	EndChar   int    `json:"end_char"`
	CharCount int    `json:"char_count"`
	ObjectKey string `json:"object_key,omitempty"`
	ChapterID string `json:"chapter_id,omitempty"`
}

type ChapterContentResult struct {
	Chapter       Chapter `json:"chapter"`
	Content       string  `json:"content"`
	Truncated     bool    `json:"truncated"`
	PrevTail      string  `json:"prev_tail,omitempty"`
	NextHead      string  `json:"next_head,omitempty"`
	SafetyMessage string  `json:"safety_message"`
}

type DeleteResult struct {
	Deleted        bool   `json:"deleted"`
	SoftDeleted    bool   `json:"soft_deleted"`
	ObjectsDeleted bool   `json:"objects_deleted"`
	Message        string `json:"message"`
}

type Manifest struct {
	SchemaVersion        string           `json:"schema_version"`
	BookID               string           `json:"book_id"`
	ProjectID            string           `json:"project_id"`
	SplitID              string           `json:"split_id"`
	Title                string           `json:"title"`
	SourceObjectKey      string           `json:"source_object_key"`
	ManifestObjectKey    string           `json:"manifest_object_key"`
	ChapterCount         int              `json:"chapter_count"`
	TotalChars           int              `json:"total_chars"`
	TotalBytes           int64            `json:"total_bytes"`
	SplitMethod          string           `json:"split_method"`
	ChapterPatterns      []string         `json:"chapter_patterns,omitempty"`
	MinChapterChars      int              `json:"min_chapter_chars"`
	LeadingContentPolicy string           `json:"leading_content_policy,omitempty"`
	Warnings             []string         `json:"warnings,omitempty"`
	Chapters             []ChapterSummary `json:"chapters"`
	CreatedAt            string           `json:"created_at"`
}

func (s *Service) ImportUpload(ctx context.Context, req ImportUploadRequest) (*ImportUploadResult, error) {
	if req.Reader == nil {
		return nil, fmt.Errorf("reader is required")
	}
	tenantID := normalizeTenant(req.TenantID)
	projectID := strings.TrimSpace(req.ProjectID)
	if projectID == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	if strings.TrimSpace(req.UploadID) != "" && !req.ForceNew {
		var existing Book
		if err := s.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND source_upload_id = ? AND status = ?", tenantID, projectID, strings.TrimSpace(req.UploadID), StatusActive).Order("updated_at DESC").First(&existing).Error; err == nil {
			return &ImportUploadResult{Book: &existing, Encoding: existing.Encoding, Warnings: []string{"upload_already_imported: reusing existing active book"}, NextAction: "reuse existing book; if current_split_id is set, do not import or split again"}, nil
		} else if err != nil && err != gorm.ErrRecordNotFound {
			return nil, err
		}
	}
	maxBytes := req.MaxImportBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxImportBytes
	}
	limited := &io.LimitedReader{R: req.Reader, N: maxBytes + 1}
	raw, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read upload source: %w", err)
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("upload too large for novel import: %d bytes exceeds max_import_bytes=%d", len(raw), maxBytes)
	}
	text, encoding := decodeText(raw, req.EncodingHint)
	text = normalizeNewlines(text)
	utf8Bytes := []byte(text)
	h := sha256.Sum256(utf8Bytes)
	sha := hex.EncodeToString(h[:])
	if req.SourceSHA256 != "" {
		sha = req.SourceSHA256
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = titleFromName(req.OriginalName)
	}
	if title == "" {
		title = "未命名小说"
	}
	bookID := uuid.NewString()
	sourceKey := sourceObjectKey(tenantID, projectID, bookID)
	if _, err := s.store.Put(ctx, sourceKey, bytes.NewReader(utf8Bytes), int64(len(utf8Bytes)), objectstore.PutOptions{ContentType: "text/plain; charset=utf-8", Metadata: map[string]string{"book_id": bookID, "project_id": projectID}}); err != nil {
		return nil, err
	}
	book := &Book{
		ID:              bookID,
		TenantID:        tenantID,
		ProjectID:       projectID,
		OwnerUserID:     strings.TrimSpace(req.OwnerUserID),
		Title:           title,
		Author:          strings.TrimSpace(req.Author),
		SourceUploadID:  strings.TrimSpace(req.UploadID),
		SourceObjectKey: sourceKey,
		TotalChars:      utf8.RuneCountInString(text),
		SizeBytes:       int64(len(utf8Bytes)),
		SHA256:          sha,
		Encoding:        encoding,
		Status:          StatusActive,
	}
	if err := s.db.WithContext(ctx).Create(book).Error; err != nil {
		_ = s.store.Delete(ctx, sourceKey)
		return nil, err
	}
	return &ImportUploadResult{Book: book, Encoding: encoding, NextAction: "call novel_split_preview, review warnings, then confirm novel_split_commit"}, nil
}

func (s *Service) SplitPreview(ctx context.Context, req SplitPreviewRequest) (*SplitResult, error) {
	book, text, err := s.loadBookSource(ctx, req.TenantID, req.ProjectID, req.BookID)
	if err != nil {
		return nil, err
	}
	parts, warnings, method, err := splitText(text, req.ChapterTitlePatterns, req.MinChapterChars, req.LeadingContentPolicy)
	if err != nil {
		return nil, err
	}
	previewLimit := req.PreviewLimit
	if previewLimit <= 0 || previewLimit > 100 {
		previewLimit = defaultPreviewChapters
	}
	summaries := make([]ChapterSummary, 0, minInt(len(parts), previewLimit))
	for i, ch := range parts {
		if i >= previewLimit {
			break
		}
		summaries = append(summaries, ChapterSummary{No: i + 1, Title: ch.Title, StartChar: ch.StartChar, EndChar: ch.EndChar, CharCount: utf8.RuneCountInString(ch.Text)})
	}
	return &SplitResult{BookID: book.ID, ProjectID: book.ProjectID, Title: book.Title, ChapterCount: len(parts), TotalChars: utf8.RuneCountInString(text), TotalBytes: int64(len([]byte(text))), SplitMethod: method, LeadingContentPolicy: normalizeLeadingContentPolicy(req.LeadingContentPolicy), Warnings: warnings, ChaptersPreview: summaries, Status: "preview_only"}, nil
}

func (s *Service) SplitCommit(ctx context.Context, req SplitCommitRequest) (*SplitResult, error) {
	book, text, err := s.loadBookSource(ctx, req.TenantID, req.ProjectID, req.BookID)
	if err != nil {
		return nil, err
	}
	parts, warnings, method, err := splitText(text, req.ChapterTitlePatterns, req.MinChapterChars, req.LeadingContentPolicy)
	if err != nil {
		return nil, err
	}
	tenantID := normalizeTenant(req.TenantID)
	if req.ProjectID == "" {
		req.ProjectID = book.ProjectID
	}
	splitID := uuid.NewString()
	manifestKey := manifestObjectKey(tenantID, book.ProjectID, book.ID, splitID)
	createdAt := time.Now().UTC()
	chapters := make([]Chapter, 0, len(parts))
	summaries := make([]ChapterSummary, 0, len(parts))
	for i, part := range parts {
		chapterNo := i + 1
		chapterID := uuid.NewString()
		chapterBytes := []byte(part.Text)
		chHash := sha256.Sum256(chapterBytes)
		objectKey := chapterObjectKey(tenantID, book.ProjectID, book.ID, splitID, chapterNo)
		if _, err := s.store.Put(ctx, objectKey, bytes.NewReader(chapterBytes), int64(len(chapterBytes)), objectstore.PutOptions{ContentType: "text/plain; charset=utf-8", Metadata: map[string]string{"book_id": book.ID, "split_id": splitID, "chapter_no": fmt.Sprintf("%d", chapterNo)}}); err != nil {
			_ = s.store.DeletePrefix(ctx, splitPrefix(tenantID, book.ProjectID, book.ID, splitID))
			return nil, err
		}
		chapters = append(chapters, Chapter{ID: chapterID, TenantID: tenantID, ProjectID: book.ProjectID, BookID: book.ID, SplitID: splitID, ChapterNo: chapterNo, Title: part.Title, ObjectKey: objectKey, CharCount: utf8.RuneCountInString(part.Text), ByteCount: int64(len(chapterBytes)), SHA256: hex.EncodeToString(chHash[:]), StartChar: part.StartChar, EndChar: part.EndChar, Status: StatusActive})
		summaries = append(summaries, ChapterSummary{No: chapterNo, Title: part.Title, StartChar: part.StartChar, EndChar: part.EndChar, CharCount: utf8.RuneCountInString(part.Text), ObjectKey: objectKey, ChapterID: chapterID})
	}
	manifest := Manifest{SchemaVersion: "novelstore.v1", BookID: book.ID, ProjectID: book.ProjectID, SplitID: splitID, Title: book.Title, SourceObjectKey: book.SourceObjectKey, ManifestObjectKey: manifestKey, ChapterCount: len(parts), TotalChars: utf8.RuneCountInString(text), TotalBytes: int64(len([]byte(text))), SplitMethod: method, ChapterPatterns: effectivePatterns(req.ChapterTitlePatterns), MinChapterChars: effectiveMinChapterChars(req.MinChapterChars), LeadingContentPolicy: normalizeLeadingContentPolicy(req.LeadingContentPolicy), Warnings: warnings, Chapters: summaries, CreatedAt: createdAt.Format(time.RFC3339)}
	manifestBytes, _ := json.MarshalIndent(manifest, "", "  ")
	if _, err := s.store.Put(ctx, manifestKey, bytes.NewReader(manifestBytes), int64(len(manifestBytes)), objectstore.PutOptions{ContentType: "application/json", Metadata: map[string]string{"book_id": book.ID, "split_id": splitID}}); err != nil {
		_ = s.store.DeletePrefix(ctx, splitPrefix(tenantID, book.ProjectID, book.ID, splitID))
		return nil, err
	}
	warningsJSON, _ := json.Marshal(warnings)
	split := &Split{ID: splitID, TenantID: tenantID, ProjectID: book.ProjectID, BookID: book.ID, SourceObjectKey: book.SourceObjectKey, ManifestObjectKey: manifestKey, SplitMethod: method, ChapterCount: len(parts), TotalChars: utf8.RuneCountInString(text), TotalBytes: int64(len([]byte(text))), WarningsJSON: string(warningsJSON), Status: StatusActive}
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if book.CurrentSplitID != "" || req.SupersedeActive {
			if err := tx.Model(&Split{}).Where("tenant_id = ? AND project_id = ? AND book_id = ? AND status = ?", tenantID, book.ProjectID, book.ID, StatusActive).Updates(map[string]any{"status": StatusSuperseded, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
			if err := tx.Model(&Chapter{}).Where("tenant_id = ? AND project_id = ? AND book_id = ? AND status = ?", tenantID, book.ProjectID, book.ID, StatusActive).Updates(map[string]any{"status": StatusSuperseded, "updated_at": time.Now()}).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(split).Error; err != nil {
			return err
		}
		if len(chapters) > 0 {
			if err := tx.Create(&chapters).Error; err != nil {
				return err
			}
		}
		return tx.Model(&Book{}).Where("tenant_id = ? AND project_id = ? AND id = ?", tenantID, book.ProjectID, book.ID).Updates(map[string]any{"current_split_id": splitID, "chapter_count": len(parts), "updated_at": time.Now()}).Error
	}); err != nil {
		_ = s.store.DeletePrefix(ctx, splitPrefix(tenantID, book.ProjectID, book.ID, splitID))
		return nil, err
	}
	preview := summaries
	if len(preview) > defaultPreviewChapters {
		preview = preview[:defaultPreviewChapters]
	}
	return &SplitResult{BookID: book.ID, ProjectID: book.ProjectID, SplitID: splitID, Title: book.Title, ChapterCount: len(parts), TotalChars: utf8.RuneCountInString(text), TotalBytes: int64(len([]byte(text))), SplitMethod: method, LeadingContentPolicy: normalizeLeadingContentPolicy(req.LeadingContentPolicy), Warnings: warnings, ChaptersPreview: preview, ManifestObjectKey: manifestKey, Status: StatusActive}, nil
}

func (s *Service) ListBooks(ctx context.Context, req ListBooksRequest) ([]Book, error) {
	tenantID := normalizeTenant(req.TenantID)
	if strings.TrimSpace(req.ProjectID) == "" {
		return nil, fmt.Errorf("project_id is required")
	}
	q := s.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ?", tenantID, strings.TrimSpace(req.ProjectID))
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = StatusActive
	}
	if status != "all" {
		q = q.Where("status = ?", status)
	}
	limit := req.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var books []Book
	if err := q.Order("updated_at DESC").Limit(limit).Find(&books).Error; err != nil {
		return nil, err
	}
	return books, nil
}

func (s *Service) GetBook(ctx context.Context, tenantID, projectID, bookID string) (*Book, error) {
	var book Book
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND id = ?", normalizeTenant(tenantID), strings.TrimSpace(projectID), strings.TrimSpace(bookID)).First(&book).Error; err != nil {
		return nil, err
	}
	return &book, nil
}

func (s *Service) ListChapters(ctx context.Context, req ListChaptersRequest) ([]Chapter, error) {
	book, err := s.GetBook(ctx, req.TenantID, req.ProjectID, req.BookID)
	if err != nil {
		return nil, err
	}
	splitID := strings.TrimSpace(req.SplitID)
	if splitID == "" || splitID == "active" {
		splitID = book.CurrentSplitID
	}
	if splitID == "" {
		return nil, fmt.Errorf("book %s has no active split", book.ID)
	}
	q := s.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND book_id = ? AND split_id = ?", book.TenantID, book.ProjectID, book.ID, splitID)
	status := strings.TrimSpace(req.Status)
	if status == "" {
		status = StatusActive
	}
	if status != "all" {
		q = q.Where("status = ?", status)
	}
	limit := req.Limit
	if limit <= 0 || limit > 2000 {
		limit = 1000
	}
	var chapters []Chapter
	if err := q.Order("chapter_no ASC").Limit(limit).Find(&chapters).Error; err != nil {
		return nil, err
	}
	return chapters, nil
}

func (s *Service) GetChapter(ctx context.Context, req GetChapterRequest) (*ChapterContentResult, error) {
	book, err := s.GetBook(ctx, req.TenantID, req.ProjectID, req.BookID)
	if err != nil {
		return nil, err
	}
	splitID := strings.TrimSpace(req.SplitID)
	if splitID == "" || splitID == "active" {
		splitID = book.CurrentSplitID
	}
	if splitID == "" {
		return nil, fmt.Errorf("book %s has no active split", book.ID)
	}
	q := s.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND book_id = ? AND split_id = ?", book.TenantID, book.ProjectID, book.ID, splitID)
	if strings.TrimSpace(req.ChapterID) != "" {
		q = q.Where("id = ?", strings.TrimSpace(req.ChapterID))
	} else {
		if req.ChapterNo <= 0 {
			return nil, fmt.Errorf("chapter_no or chapter_id is required")
		}
		q = q.Where("chapter_no = ?", req.ChapterNo)
	}
	var ch Chapter
	if err := q.First(&ch).Error; err != nil {
		return nil, err
	}
	content, truncated, err := s.readTextObject(ctx, ch.ObjectKey, req.MaxChars)
	if err != nil {
		return nil, err
	}
	result := &ChapterContentResult{Chapter: ch, Content: content, Truncated: truncated, SafetyMessage: "chapter content is bounded by max_chars; use book_id/split_id/chapter_no for downstream agents instead of pasting full books into chat"}
	neighborMax := req.NeighborMaxChars
	if neighborMax <= 0 || neighborMax > 4000 {
		neighborMax = 1200
	}
	if req.IncludePrevTail && ch.ChapterNo > 1 {
		if prev, err := s.getChapterByNo(ctx, book.TenantID, book.ProjectID, book.ID, splitID, ch.ChapterNo-1); err == nil {
			if txt, _, err := s.readTextObject(ctx, prev.ObjectKey, 0); err == nil {
				result.PrevTail = tailRunes(txt, neighborMax)
			}
		}
	}
	if req.IncludeNextHead {
		if next, err := s.getChapterByNo(ctx, book.TenantID, book.ProjectID, book.ID, splitID, ch.ChapterNo+1); err == nil {
			if txt, _, err := s.readTextObject(ctx, next.ObjectKey, neighborMax); err == nil {
				result.NextHead = txt
			}
		}
	}
	return result, nil
}

func (s *Service) DeleteSplit(ctx context.Context, req DeleteSplitRequest) (*DeleteResult, error) {
	tenantID := normalizeTenant(req.TenantID)
	book, err := s.GetBook(ctx, tenantID, req.ProjectID, req.BookID)
	if err != nil {
		return nil, err
	}
	splitID := strings.TrimSpace(req.SplitID)
	if splitID == "" {
		return nil, fmt.Errorf("split_id is required")
	}
	if book.CurrentSplitID == splitID && !req.Force {
		return nil, fmt.Errorf("split %s is current active split; pass force=true after user confirmation or create another active split first", splitID)
	}
	now := time.Now()
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Split{}).Where("tenant_id = ? AND project_id = ? AND book_id = ? AND id = ?", tenantID, book.ProjectID, book.ID, splitID).Updates(map[string]any{"status": StatusDeleted, "deleted_at": &now, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Chapter{}).Where("tenant_id = ? AND project_id = ? AND book_id = ? AND split_id = ?", tenantID, book.ProjectID, book.ID, splitID).Updates(map[string]any{"status": StatusDeleted, "deleted_at": &now, "updated_at": now}).Error; err != nil {
			return err
		}
		if book.CurrentSplitID == splitID {
			return tx.Model(&Book{}).Where("tenant_id = ? AND project_id = ? AND id = ?", tenantID, book.ProjectID, book.ID).Updates(map[string]any{"current_split_id": "", "chapter_count": 0, "updated_at": now}).Error
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if req.DeleteObjects {
		if err := s.store.DeletePrefix(ctx, splitPrefix(tenantID, book.ProjectID, book.ID, splitID)); err != nil {
			return nil, err
		}
	}
	return &DeleteResult{Deleted: true, SoftDeleted: true, ObjectsDeleted: req.DeleteObjects, Message: "split metadata was soft-deleted"}, nil
}

func (s *Service) DeleteBook(ctx context.Context, req DeleteBookRequest) (*DeleteResult, error) {
	tenantID := normalizeTenant(req.TenantID)
	book, err := s.GetBook(ctx, tenantID, req.ProjectID, req.BookID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Split{}).Where("tenant_id = ? AND project_id = ? AND book_id = ?", tenantID, book.ProjectID, book.ID).Updates(map[string]any{"status": StatusDeleted, "deleted_at": &now, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Chapter{}).Where("tenant_id = ? AND project_id = ? AND book_id = ?", tenantID, book.ProjectID, book.ID).Updates(map[string]any{"status": StatusDeleted, "deleted_at": &now, "updated_at": now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Artifact{}).Where("tenant_id = ? AND project_id = ? AND book_id = ?", tenantID, book.ProjectID, book.ID).Updates(map[string]any{"status": StatusDeleted, "deleted_at": &now, "updated_at": now}).Error; err != nil {
			return err
		}
		return tx.Model(&Book{}).Where("tenant_id = ? AND project_id = ? AND id = ?", tenantID, book.ProjectID, book.ID).Updates(map[string]any{"status": StatusDeleted, "deleted_at": &now, "updated_at": now}).Error
	}); err != nil {
		return nil, err
	}
	if req.DeleteObjects {
		if err := s.store.DeletePrefix(ctx, bookPrefix(tenantID, book.ProjectID, book.ID)); err != nil {
			return nil, err
		}
	}
	return &DeleteResult{Deleted: true, SoftDeleted: true, ObjectsDeleted: req.DeleteObjects, Message: "book metadata was soft-deleted"}, nil
}

func (s *Service) SaveArtifact(ctx context.Context, req SaveArtifactRequest) (*Artifact, error) {
	tenantID := normalizeTenant(req.TenantID)
	if strings.TrimSpace(req.ProjectID) == "" || strings.TrimSpace(req.BookID) == "" {
		return nil, fmt.Errorf("project_id and book_id are required")
	}
	kind := strings.TrimSpace(req.Kind)
	if kind == "" {
		return nil, fmt.Errorf("kind is required")
	}
	name := filepath.Base(strings.TrimSpace(req.Name))
	if name == "" || name == "." || name == ".." {
		name = kind + ".json"
	}
	artifactID := uuid.NewString()
	objectKey := artifactObjectKey(tenantID, req.ProjectID, req.BookID, artifactID, name)
	mimeType := strings.TrimSpace(req.MIMEType)
	if mimeType == "" {
		mimeType = "application/json"
	}
	if _, err := s.store.Put(ctx, objectKey, bytes.NewReader(req.Content), int64(len(req.Content)), objectstore.PutOptions{ContentType: mimeType}); err != nil {
		return nil, err
	}
	h := sha256.Sum256(req.Content)
	artifact := &Artifact{ID: artifactID, TenantID: tenantID, ProjectID: strings.TrimSpace(req.ProjectID), BookID: strings.TrimSpace(req.BookID), SplitID: strings.TrimSpace(req.SplitID), ChapterID: strings.TrimSpace(req.ChapterID), RunID: strings.TrimSpace(req.RunID), Kind: kind, Name: name, Title: strings.TrimSpace(req.Title), ObjectKey: objectKey, MIMEType: mimeType, SizeBytes: int64(len(req.Content)), SHA256: hex.EncodeToString(h[:]), MetadataJSON: strings.TrimSpace(req.MetadataJSON), Status: StatusActive}
	if err := s.db.WithContext(ctx).Create(artifact).Error; err != nil {
		_ = s.store.Delete(ctx, objectKey)
		return nil, err
	}
	return artifact, nil
}

func (s *Service) loadBookSource(ctx context.Context, tenantID, projectID, bookID string) (*Book, string, error) {
	book, err := s.GetBook(ctx, tenantID, projectID, bookID)
	if err != nil {
		return nil, "", err
	}
	if book.Status == StatusDeleted {
		return nil, "", fmt.Errorf("book %s is deleted", book.ID)
	}
	r, _, err := s.store.Get(ctx, book.SourceObjectKey)
	if err != nil {
		return nil, "", err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, "", err
	}
	return book, string(data), nil
}

func (s *Service) readTextObject(ctx context.Context, key string, maxChars int) (string, bool, error) {
	r, _, err := s.store.Get(ctx, key)
	if err != nil {
		return "", false, err
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		return "", false, err
	}
	text := string(data)
	if maxChars <= 0 {
		return text, false, nil
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text, false, nil
	}
	return string(runes[:maxChars]), true, nil
}

func (s *Service) getChapterByNo(ctx context.Context, tenantID, projectID, bookID, splitID string, chapterNo int) (*Chapter, error) {
	var ch Chapter
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND project_id = ? AND book_id = ? AND split_id = ? AND chapter_no = ? AND status = ?", tenantID, projectID, bookID, splitID, chapterNo, StatusActive).First(&ch).Error; err != nil {
		return nil, err
	}
	return &ch, nil
}

type splitPart struct {
	Title     string
	Text      string
	StartChar int
	EndChar   int
}

type chapterStart struct {
	Title string
	Start int
	End   int
}

func splitText(text string, patterns []string, minChapterChars int, leadingPolicy string) ([]splitPart, []string, string, error) {
	text = normalizeNewlines(text)
	if strings.TrimSpace(text) == "" {
		return nil, nil, "regex_lines", fmt.Errorf("book source is empty")
	}
	minChars := effectiveMinChapterChars(minChapterChars)
	policy := normalizeLeadingContentPolicy(leadingPolicy)
	starts, err := detectChapterStarts(text, effectivePatterns(patterns))
	if err != nil {
		return nil, nil, "regex_lines", err
	}
	var warnings []string
	if len(starts) == 0 {
		warnings = append(warnings, "未检测到明确章节标题，已把全文作为单章；请检查章节标题规则")
		return []splitPart{{Title: "全文", Text: strings.TrimSpace(text), StartChar: 0, EndChar: utf8.RuneCountInString(text)}}, warnings, "single_document_fallback", nil
	}
	if starts[0].Start > 0 {
		prefix := strings.TrimSpace(text[:starts[0].Start])
		if prefix != "" {
			prefixChars := utf8.RuneCountInString(prefix)
			switch policy {
			case LeadingContentDrop:
				warnings = append(warnings, fmt.Sprintf("已丢弃首章标题前的前置内容（%d 字）；如需保留简介/序言，请用 leading_content_policy=keep 或 merge 重切", prefixChars))
			case LeadingContentKeep:
				starts = append([]chapterStart{{Title: "前置内容", Start: 0, End: 0}}, starts...)
			case LeadingContentMerge:
				warnings = append(warnings, fmt.Sprintf("首章标题前存在前置内容（%d 字），已并入第一章", prefixChars))
				starts[0].Start = 0
			default:
				if prefixChars >= minChars {
					starts = append([]chapterStart{{Title: "前置内容", Start: 0, End: 0}}, starts...)
				} else {
					warnings = append(warnings, "首章标题前存在较短前置内容，已并入第一章；如这是简介/序言，可用 leading_content_policy=drop 重切")
					starts[0].Start = 0
				}
			}
		}
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Start < starts[j].Start })
	var parts []splitPart
	for i, st := range starts {
		start := st.Start
		end := len(text)
		if i+1 < len(starts) {
			end = starts[i+1].Start
		}
		if start < 0 || start >= end || end > len(text) {
			continue
		}
		chunk := strings.TrimSpace(text[start:end])
		if chunk == "" {
			continue
		}
		if utf8.RuneCountInString(chunk) < minChars && len(parts) > 0 {
			last := &parts[len(parts)-1]
			last.Text = strings.TrimSpace(last.Text + "\n\n" + chunk)
			last.EndChar = utf8.RuneCountInString(text[:end])
			last.Title = strings.TrimSpace(last.Title)
			continue
		}
		parts = append(parts, splitPart{Title: cleanTitle(st.Title, len(parts)+1), Text: chunk, StartChar: utf8.RuneCountInString(text[:start]), EndChar: utf8.RuneCountInString(text[:end])})
	}
	if len(parts) == 0 {
		return []splitPart{{Title: "全文", Text: strings.TrimSpace(text), StartChar: 0, EndChar: utf8.RuneCountInString(text)}}, append(warnings, "章节标题匹配后没有形成有效章节，已回退为单章"), "single_document_fallback", nil
	}
	if len(parts) < 3 {
		warnings = append(warnings, fmt.Sprintf("仅检测到 %d 章，可能章节规则过严或源文本不是完整小说", len(parts)))
	}
	return parts, warnings, "regex_lines", nil
}

func normalizeLeadingContentPolicy(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", LeadingContentAuto:
		return LeadingContentAuto
	case LeadingContentKeep, "separate", "front_matter":
		return LeadingContentKeep
	case LeadingContentMerge, "join", "append":
		return LeadingContentMerge
	case LeadingContentDrop, "discard", "skip", "remove":
		return LeadingContentDrop
	default:
		return LeadingContentAuto
	}
}

func detectChapterStarts(text string, patterns []string) ([]chapterStart, error) {
	seen := map[int]chapterStart{}
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid chapter pattern %q: %w", pattern, err)
		}
		matches := re.FindAllStringIndex(text, -1)
		for _, m := range matches {
			line := strings.TrimSpace(text[m[0]:m[1]])
			if line == "" || looksLikeIntroTitle(line) {
				continue
			}
			seen[m[0]] = chapterStart{Title: line, Start: m[0], End: m[1]}
		}
	}
	starts := make([]chapterStart, 0, len(seen))
	for _, st := range seen {
		starts = append(starts, st)
	}
	sort.Slice(starts, func(i, j int) bool { return starts[i].Start < starts[j].Start })
	return starts, nil
}

func decodeText(data []byte, hint string) (string, string) {
	h := strings.ToLower(strings.TrimSpace(hint))
	if h == "gbk" || h == "gb18030" || h == "gb2312" {
		if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil && utf8.Valid(decoded) {
			return string(decoded), "gb18030"
		}
	}
	if utf8.Valid(data) {
		return string(data), "utf-8"
	}
	if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil && utf8.Valid(decoded) {
		return string(decoded), "gb18030"
	}
	return string(data), "unknown"
}

func normalizeTenant(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return DefaultTenantID
	}
	return v
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimRight(s, "\x00")
}

func effectivePatterns(patterns []string) []string {
	var out []string
	for _, p := range patterns {
		if strings.TrimSpace(p) != "" {
			out = append(out, strings.TrimSpace(p))
		}
	}
	if len(out) == 0 {
		out = append(out, defaultChapterPatterns...)
	}
	return out
}

func effectiveMinChapterChars(v int) int {
	if v <= 0 {
		return 200
	}
	return v
}

func cleanTitle(title string, no int) string {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Sprintf("第%d章", no)
	}
	if len([]rune(title)) > 120 {
		title = string([]rune(title)[:120])
	}
	return title
}

func looksLikeIntroTitle(line string) bool {
	line = strings.TrimSpace(line)
	line = strings.Trim(line, "：:　 ")
	switch line {
	case "简介", "内容简介", "作品简介", "小说简介", "作者简介", "文案", "书籍简介":
		return true
	default:
		return false
	}
}

func titleFromName(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	ext := filepath.Ext(base)
	return strings.TrimSpace(strings.TrimSuffix(base, ext))
}

func sourceObjectKey(tenantID, projectID, bookID string) string {
	return fmt.Sprintf("tenants/%s/projects/%s/novels/%s/source/source_utf8.txt", tenantID, projectID, bookID)
}

func bookPrefix(tenantID, projectID, bookID string) string {
	return fmt.Sprintf("tenants/%s/projects/%s/novels/%s/", tenantID, projectID, bookID)
}

func splitPrefix(tenantID, projectID, bookID, splitID string) string {
	return fmt.Sprintf("tenants/%s/projects/%s/novels/%s/splits/%s/", tenantID, projectID, bookID, splitID)
}

func manifestObjectKey(tenantID, projectID, bookID, splitID string) string {
	return fmt.Sprintf("tenants/%s/projects/%s/novels/%s/splits/%s/manifest.json", tenantID, projectID, bookID, splitID)
}

func chapterObjectKey(tenantID, projectID, bookID, splitID string, chapterNo int) string {
	return fmt.Sprintf("tenants/%s/projects/%s/novels/%s/splits/%s/chapters/%06d.txt", tenantID, projectID, bookID, splitID, chapterNo)
}

func artifactObjectKey(tenantID, projectID, bookID, artifactID, name string) string {
	return fmt.Sprintf("tenants/%s/projects/%s/novels/%s/artifacts/%s/%s", tenantID, projectID, bookID, artifactID, filepath.Base(name))
}

func tailRunes(s string, n int) string {
	if n <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

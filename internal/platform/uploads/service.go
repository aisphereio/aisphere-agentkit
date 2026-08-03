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

package uploads

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"gorm.io/gorm"
)

const defaultMaxUploadBytes int64 = 512 << 20 // 512 MiB

// Service manages raw user uploads before they are promoted into artifacts or processed by tools.
type Service interface {
	Create(ctx context.Context, req CreateRequest) (*Upload, error)
	Get(ctx context.Context, tenantID, id string) (*Upload, error)
	List(ctx context.Context, filter ListFilter) ([]Upload, error)
	Open(ctx context.Context, tenantID, id string) (io.ReadCloser, *Upload, error)
	Preview(ctx context.Context, tenantID, id string, maxBytes int64) (*PreviewResult, error)
	Delete(ctx context.Context, tenantID, id string) error
}

type CreateRequest struct {
	TenantID     string
	UserID       string
	ProjectID    string
	AppName      string
	SessionID    string
	Purpose      string
	OriginalName string
	MIMEType     string
	MetadataJSON string
	Reader       io.Reader
	MaxBytes     int64
}

type ListFilter struct {
	TenantID     string
	UserID       string
	ProjectID    string
	AppName      string
	SessionID    string
	Purpose      string
	Status       string
	HandlingMode string
	Limit        int
}

// PreviewResult is a bounded, safe preview of an upload. It is never meant to
// carry a full file into model context.
type PreviewResult struct {
	UploadID     string `json:"upload_id"`
	OriginalName string `json:"original_name"`
	MIMEType     string `json:"mime_type"`
	SizeBytes    int64  `json:"size_bytes"`
	HandlingMode string `json:"handling_mode"`
	Previewable  bool   `json:"previewable"`
	DisplayMode  string `json:"display_mode"`
	Encoding     string `json:"encoding,omitempty"`
	Content      string `json:"content,omitempty"`
	BytesRead    int64  `json:"bytes_read"`
	Truncated    bool   `json:"truncated"`
	Warning      string `json:"warning,omitempty"`
}

type gormService struct {
	db   *gorm.DB
	root string
}

func NewService(db *gorm.DB, root string) Service {
	return &gormService{db: db, root: root}
}

func (s *gormService) Create(ctx context.Context, req CreateRequest) (*Upload, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	if strings.TrimSpace(req.UserID) == "" {
		return nil, fmt.Errorf("user_id is required")
	}
	if req.Reader == nil {
		return nil, fmt.Errorf("reader is required")
	}
	originalName := strings.TrimSpace(req.OriginalName)
	if originalName == "" {
		originalName = "upload.bin"
	}
	if s.root == "" {
		return nil, fmt.Errorf("upload storage root is required")
	}

	maxBytes := req.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxUploadBytes
	}

	upload := &Upload{
		TenantID:     strings.TrimSpace(req.TenantID),
		UserID:       strings.TrimSpace(req.UserID),
		ProjectID:    strings.TrimSpace(req.ProjectID),
		AppName:      strings.TrimSpace(req.AppName),
		SessionID:    strings.TrimSpace(req.SessionID),
		Purpose:      strings.TrimSpace(req.Purpose),
		OriginalName: filepath.Base(originalName),
		MIMEType:     strings.TrimSpace(req.MIMEType),
		MetadataJSON: strings.TrimSpace(req.MetadataJSON),
		Status:       StatusActive,
	}
	if upload.MIMEType == "" {
		upload.MIMEType = mime.TypeByExtension(strings.ToLower(filepath.Ext(upload.OriginalName)))
	}

	// Create the ID before we need the storage path.
	if err := upload.BeforeCreate(nil); err != nil {
		return nil, err
	}
	upload.StoredName = buildStoredName(upload.ID, upload.OriginalName)
	storagePath := filepath.Join(upload.TenantID, upload.UserID, upload.ID[:2], upload.StoredName)
	upload.StoragePath = filepath.ToSlash(storagePath)
	absolutePath := filepath.Join(s.root, storagePath)
	if err := os.MkdirAll(filepath.Dir(absolutePath), 0o755); err != nil {
		return nil, fmt.Errorf("create upload directory: %w", err)
	}

	tmpPath := absolutePath + ".tmp"
	out, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("create upload temp file: %w", err)
	}
	defer func() { _ = os.Remove(tmpPath) }()

	h := sha256.New()
	limited := &io.LimitedReader{R: req.Reader, N: maxBytes + 1}
	written, copyErr := io.Copy(out, io.TeeReader(limited, h))
	closeErr := out.Close()
	if copyErr != nil {
		return nil, fmt.Errorf("write upload file: %w", copyErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close upload file: %w", closeErr)
	}
	if written > maxBytes {
		return nil, fmt.Errorf("upload too large: %d bytes exceeds limit %d", written, maxBytes)
	}
	upload.SizeBytes = written
	upload.SHA256 = hex.EncodeToString(h.Sum(nil))

	if upload.MIMEType == "" {
		if detected, err := detectFileMIME(tmpPath); err == nil && detected != "" {
			upload.MIMEType = detected
		}
	}
	if upload.MIMEType == "" {
		upload.MIMEType = "application/octet-stream"
	}
	policy := Classify(upload.OriginalName, upload.MIMEType, upload.SizeBytes, upload.Purpose)
	upload.HandlingMode = policy.HandlingMode
	upload.InlineEligible = policy.InlineEligible
	upload.Previewable = policy.Previewable
	upload.PolicyReason = policy.Reason

	if err := os.Rename(tmpPath, absolutePath); err != nil {
		return nil, fmt.Errorf("commit upload file: %w", err)
	}
	if err := s.db.WithContext(ctx).Create(upload).Error; err != nil {
		_ = os.Remove(absolutePath)
		return nil, err
	}
	return upload, nil
}

func (s *gormService) Get(ctx context.Context, tenantID, id string) (*Upload, error) {
	var upload Upload
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND id = ?", tenantID, id).First(&upload).Error; err != nil {
		return nil, err
	}
	return &upload, nil
}

func (s *gormService) List(ctx context.Context, filter ListFilter) ([]Upload, error) {
	if strings.TrimSpace(filter.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	q := s.db.WithContext(ctx).Where("tenant_id = ?", filter.TenantID)
	if filter.UserID != "" {
		q = q.Where("user_id = ?", filter.UserID)
	}
	if filter.ProjectID != "" {
		q = q.Where("project_id = ?", filter.ProjectID)
	}
	if filter.AppName != "" {
		q = q.Where("app_name = ?", filter.AppName)
	}
	if filter.SessionID != "" {
		q = q.Where("session_id = ?", filter.SessionID)
	}
	if filter.Purpose != "" {
		q = q.Where("purpose = ?", filter.Purpose)
	}
	status := filter.Status
	if status == "" {
		status = StatusActive
	}
	if status != "all" {
		q = q.Where("status = ?", status)
	}
	if filter.HandlingMode != "" {
		q = q.Where("handling_mode = ?", filter.HandlingMode)
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	var out []Upload
	err := q.Order("created_at DESC").Limit(limit).Find(&out).Error
	return out, err
}

func (s *gormService) Open(ctx context.Context, tenantID, id string) (io.ReadCloser, *Upload, error) {
	upload, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return nil, nil, err
	}
	if upload.Status == StatusDeleted {
		return nil, nil, fmt.Errorf("upload %s has been deleted", id)
	}
	path, err := s.absolutePath(upload)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	return f, upload, nil
}

func (s *gormService) Preview(ctx context.Context, tenantID, id string, maxBytes int64) (*PreviewResult, error) {
	reader, upload, err := s.Open(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	if maxBytes <= 0 || maxBytes > defaultPreviewLimitBytes {
		maxBytes = defaultPreviewLimitBytes
	}
	result := &PreviewResult{
		UploadID:     upload.ID,
		OriginalName: upload.OriginalName,
		MIMEType:     upload.MIMEType,
		SizeBytes:    upload.SizeBytes,
		HandlingMode: upload.HandlingMode,
		Previewable:  upload.Previewable,
		DisplayMode:  "text",
		Warning:      "preview is bounded and must not be treated as full file content",
	}
	if !upload.Previewable {
		result.DisplayMode = "none"
		result.Warning = "this upload type is not text-previewable; use a registered extractor/tool instead"
		return result, nil
	}
	limited := &io.LimitedReader{R: reader, N: maxBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read upload preview: %w", err)
	}
	if int64(len(data)) > maxBytes {
		data = data[:maxBytes]
		result.Truncated = true
	}
	result.BytesRead = int64(len(data))
	text, encoding := decodePreviewText(data)
	result.Content = text
	result.Encoding = encoding
	return result, nil
}

func (s *gormService) Delete(ctx context.Context, tenantID, id string) error {
	upload, err := s.Get(ctx, tenantID, id)
	if err != nil {
		return err
	}
	path, pathErr := s.absolutePath(upload)
	if err := s.db.WithContext(ctx).Model(&Upload{}).Where("tenant_id = ? AND id = ?", tenantID, id).Updates(map[string]any{"status": StatusDeleted, "updated_at": time.Now()}).Error; err != nil {
		return err
	}
	if pathErr == nil {
		_ = os.Remove(path)
	}
	return nil
}

func (s *gormService) absolutePath(upload *Upload) (string, error) {
	if upload == nil {
		return "", fmt.Errorf("upload is nil")
	}
	clean := filepath.Clean(filepath.FromSlash(upload.StoragePath))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", fmt.Errorf("invalid upload storage path")
	}
	return filepath.Join(s.root, clean), nil
}

var unsafeNameChars = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func buildStoredName(id, original string) string {
	base := filepath.Base(original)
	base = strings.TrimSpace(base)
	if base == "" || base == "." || base == ".." {
		base = "upload.bin"
	}
	base = unsafeNameChars.ReplaceAllString(base, "_")
	base = strings.Trim(base, "._-")
	if base == "" {
		base = "upload.bin"
	}
	if len(base) > 160 {
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		if len(ext) > 24 {
			ext = ext[:24]
		}
		maxStem := 160 - len(ext)
		if maxStem < 1 {
			maxStem = 1
		}
		if len(stem) > maxStem {
			stem = stem[:maxStem]
		}
		base = stem + ext
	}
	shortID := id
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}
	return shortID + "__" + base
}

func decodePreviewText(data []byte) (string, string) {
	if utf8.Valid(data) {
		return strings.TrimRight(string(data), "\x00"), "utf-8"
	}
	if decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(data); err == nil && utf8.Valid(decoded) {
		return strings.TrimRight(string(decoded), "\x00"), "gb18030"
	}
	return strings.TrimRight(string(data), "\x00"), "unknown"
}

func detectFileMIME(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}

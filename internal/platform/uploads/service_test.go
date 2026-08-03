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
	"io"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestUploadServiceLifecycle(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := AutoMigrate(db); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	svc := NewService(db, t.TempDir())
	ctx := context.Background()

	upload, err := svc.Create(ctx, CreateRequest{
		TenantID:     "default",
		UserID:       "admin",
		ProjectID:    "project-1",
		AppName:      "book_dissector",
		SessionID:    "session-1",
		Purpose:      "book_source",
		OriginalName: "测试 book.txt",
		MIMEType:     "text/plain",
		Reader:       strings.NewReader("第一章 测试\n正文"),
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if upload.ID == "" || upload.SizeBytes <= 0 || upload.SHA256 == "" || upload.Status != StatusActive {
		t.Fatalf("unexpected upload: %+v", upload)
	}
	if upload.HandlingMode != HandlingPreprocessRequired || upload.InlineEligible {
		t.Fatalf("book_source policy = %s inline=%v, want preprocess_required inline=false", upload.HandlingMode, upload.InlineEligible)
	}

	listed, err := svc.List(ctx, ListFilter{TenantID: "default", UserID: "admin", Purpose: "book_source"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List len=%d, want 1", len(listed))
	}

	r, got, err := svc.Open(ctx, "default", upload.ID)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if got.OriginalName != "测试 book.txt" || !strings.Contains(string(data), "第一章") {
		t.Fatalf("unexpected open result: upload=%+v data=%q", got, string(data))
	}

	preview, err := svc.Preview(ctx, "default", upload.ID, 16)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if !preview.Previewable || preview.Content == "" || preview.Encoding == "" {
		t.Fatalf("unexpected preview: %+v", preview)
	}

	if err := svc.Delete(ctx, "default", upload.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, _, err := svc.Open(ctx, "default", upload.ID); err == nil {
		t.Fatalf("Open after Delete succeeded, want error")
	}
}

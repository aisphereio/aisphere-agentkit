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

package novelstoretool

import (
	"testing"

	"google.golang.org/adk/internal/platform/novelstore"
	"google.golang.org/adk/tool/projectartifacttool"
)

func TestNovelProjectStateRequests(t *testing.T) {
	book := &novelstore.Book{
		ID:             "book-1",
		ProjectID:      "project-1",
		Title:          "Demo Book",
		Author:         "Demo Author",
		SourceUploadID: "upload-1",
		CurrentSplitID: "split-1",
		ChapterCount:   258,
		TotalChars:     123456,
		SizeBytes:      789,
		SHA256:         "abc",
		Encoding:       "utf-8",
		Status:         novelstore.StatusActive,
	}
	split := &novelstore.SplitResult{
		BookID:       book.ID,
		ProjectID:    book.ProjectID,
		SplitID:      book.CurrentSplitID,
		Title:        book.Title,
		ChapterCount: book.ChapterCount,
		TotalChars:   book.TotalChars,
		TotalBytes:   789,
		SplitMethod:  "regex",
		Status:       novelstore.StatusActive,
		Warnings:     []string{"suspicious heading"},
	}

	requests := novelProjectStateRequests(book.ProjectID, book, split)
	if len(requests) != 2 {
		t.Fatalf("len(requests) = %d, want 2", len(requests))
	}
	if requests[0].Type != "novel.book" {
		t.Fatalf("first type = %q, want novel.book", requests[0].Type)
	}
	if requests[0].Visibility != projectartifacttool.VisibilityProjectDefault {
		t.Fatalf("book visibility = %q, want project_default", requests[0].Visibility)
	}
	if requests[0].Metadata["has_active_split"] != "true" {
		t.Fatalf("book has_active_split = %q, want true", requests[0].Metadata["has_active_split"])
	}
	if requests[1].Type != "novel.active_split" {
		t.Fatalf("second type = %q, want novel.active_split", requests[1].Type)
	}
	if requests[1].Metadata["split_id"] != "split-1" {
		t.Fatalf("split_id = %q, want split-1", requests[1].Metadata["split_id"])
	}
	if requests[1].Metadata["chapter_count"] != "258" {
		t.Fatalf("chapter_count = %q, want 258", requests[1].Metadata["chapter_count"])
	}
	if requests[1].Metadata["warning_count"] != "1" {
		t.Fatalf("warning_count = %q, want 1", requests[1].Metadata["warning_count"])
	}
}

func TestNovelProjectStateRequestsWithoutSplit(t *testing.T) {
	book := &novelstore.Book{ID: "book-1", ProjectID: "project-1", Title: "Demo Book"}

	requests := novelProjectStateRequests(book.ProjectID, book, nil)
	if len(requests) != 1 {
		t.Fatalf("len(requests) = %d, want 1", len(requests))
	}
	if requests[0].Type != "novel.book" {
		t.Fatalf("type = %q, want novel.book", requests[0].Type)
	}
	if requests[0].Metadata["has_active_split"] != "false" {
		t.Fatalf("has_active_split = %q, want false", requests[0].Metadata["has_active_split"])
	}
}

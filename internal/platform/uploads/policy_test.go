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

import "testing"

func TestClassifyUploadPolicy(t *testing.T) {
	tests := []struct {
		name       string
		fileName   string
		mimeType   string
		size       int64
		purpose    string
		wantMode   string
		wantInline bool
	}{
		{name: "small text", fileName: "note.txt", mimeType: "text/plain", size: 1024, wantMode: HandlingInlineSmallText, wantInline: true},
		{name: "book source", fileName: "book.txt", mimeType: "text/plain", size: 1024, purpose: "book_source", wantMode: HandlingPreprocessRequired, wantInline: false},
		{name: "large log", fileName: "app.log", mimeType: "text/plain", size: 2 << 20, wantMode: HandlingPreprocessRequired, wantInline: false},
		{name: "xlsx", fileName: "data.xlsx", mimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", size: 1024, wantMode: HandlingToolWorkspace, wantInline: false},
		{name: "exe", fileName: "bad.exe", mimeType: "application/x-msdownload", size: 1024, wantMode: HandlingBlocked, wantInline: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Classify(tt.fileName, tt.mimeType, tt.size, tt.purpose)
			if got.HandlingMode != tt.wantMode || got.InlineEligible != tt.wantInline {
				t.Fatalf("Classify() = mode=%s inline=%v, want mode=%s inline=%v", got.HandlingMode, got.InlineEligible, tt.wantMode, tt.wantInline)
			}
		})
	}
}

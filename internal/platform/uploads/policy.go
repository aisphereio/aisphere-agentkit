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
	"path/filepath"
	"strings"
)

const (
	HandlingReferenceOnly      = "reference_only"
	HandlingInlineSmallText    = "inline_small_text"
	HandlingPreprocessRequired = "preprocess_required"
	HandlingRetrievalIndex     = "retrieval_index"
	HandlingToolWorkspace      = "tool_workspace"
	HandlingArtifactReady      = "artifact_ready"
	HandlingBlocked            = "blocked"
)

const (
	defaultInlineTextLimitBytes = int64(16 << 10) // 16 KiB
	defaultPreviewLimitBytes    = int64(64 << 10) // 64 KiB
	defaultLargeFileBytes       = int64(1 << 20)  // 1 MiB
)

// Policy describes how a raw upload should be presented to users and agents.
// The policy is advisory at upload time and enforceable at runtime by the
// controllers. Raw uploads are never injected into model prompts by default.
type Policy struct {
	HandlingMode     string   `json:"handling_mode"`
	InlineEligible   bool     `json:"inline_eligible"`
	Previewable      bool     `json:"previewable"`
	MaxInlineBytes   int64    `json:"max_inline_bytes"`
	MaxPreviewBytes  int64    `json:"max_preview_bytes"`
	Reason           string   `json:"reason"`
	SuggestedActions []string `json:"suggested_actions,omitempty"`
}

// Classify returns the default handling policy for an upload based on stable
// metadata. It deliberately errs toward reference/preprocess modes for large
// files so the frontend does not accidentally send the full content to the LLM.
func Classify(originalName, mimeType string, sizeBytes int64, purpose string) Policy {
	name := strings.ToLower(filepath.Base(originalName))
	ext := strings.ToLower(filepath.Ext(name))
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))
	purpose = strings.ToLower(strings.TrimSpace(purpose))

	p := Policy{
		HandlingMode:    HandlingReferenceOnly,
		InlineEligible:  false,
		Previewable:     isTextLike(ext, mimeType),
		MaxInlineBytes:  defaultInlineTextLimitBytes,
		MaxPreviewBytes: defaultPreviewLimitBytes,
		Reason:          "raw uploads are stored as references by default and are not injected into model context",
		SuggestedActions: []string{
			"preview",
			"attach_artifact",
		},
	}

	if isBlockedExecutable(ext, mimeType) {
		p.HandlingMode = HandlingBlocked
		p.Previewable = false
		p.Reason = "executable or potentially unsafe upload type"
		p.SuggestedActions = []string{"delete"}
		return p
	}

	switch {
	case purpose == "book_source" || ext == ".epub":
		p.HandlingMode = HandlingPreprocessRequired
		p.InlineEligible = false
		p.Previewable = isTextLike(ext, mimeType) || ext == ".epub"
		p.Reason = "book sources must be split/indexed before agent analysis"
		p.SuggestedActions = []string{"preview", "book_split", "attach_artifact"}
	case ext == ".zip" || ext == ".tar" || ext == ".gz" || ext == ".tgz" || ext == ".rar" || ext == ".7z":
		p.HandlingMode = HandlingToolWorkspace
		p.Previewable = false
		p.Reason = "archive uploads should be unpacked or scanned by a registered tool/script"
		p.SuggestedActions = []string{"unpack", "scan", "delete"}
	case ext == ".xlsx" || ext == ".xls" || ext == ".csv":
		p.HandlingMode = HandlingToolWorkspace
		p.Previewable = ext == ".csv"
		p.Reason = "tabular uploads should be profiled or converted by a tool before model use"
		p.SuggestedActions = []string{"preview", "profile_table", "attach_artifact"}
	case ext == ".pdf" || ext == ".docx" || ext == ".doc":
		p.HandlingMode = HandlingPreprocessRequired
		p.Previewable = false
		p.Reason = "document uploads should be extracted or indexed before model use"
		p.SuggestedActions = []string{"extract_text", "build_index", "attach_artifact"}
	case isImageLike(ext, mimeType):
		p.HandlingMode = HandlingReferenceOnly
		p.Previewable = false
		p.Reason = "images stay as references unless a visual model/tool explicitly reads them"
		p.SuggestedActions = []string{"view", "analyze_image"}
	case isTextLike(ext, mimeType) && sizeBytes > 0 && sizeBytes <= defaultInlineTextLimitBytes:
		p.HandlingMode = HandlingInlineSmallText
		p.InlineEligible = true
		p.Previewable = true
		p.Reason = "small text upload is eligible for explicit inline use"
		p.SuggestedActions = []string{"preview", "inline_if_user_confirms", "attach_artifact"}
	case isTextLike(ext, mimeType) && sizeBytes > defaultLargeFileBytes:
		p.HandlingMode = HandlingPreprocessRequired
		p.InlineEligible = false
		p.Previewable = true
		p.Reason = "large text upload should be chunked, split, or indexed before model use"
		p.SuggestedActions = []string{"preview", "chunk", "attach_artifact"}
	case isTextLike(ext, mimeType):
		p.HandlingMode = HandlingReferenceOnly
		p.InlineEligible = false
		p.Previewable = true
		p.Reason = "text upload is stored as a reference; inline use requires explicit user/tool action"
		p.SuggestedActions = []string{"preview", "attach_artifact"}
	}
	return p
}

func isTextLike(ext, mimeType string) bool {
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	switch ext {
	case ".txt", ".md", ".markdown", ".json", ".yaml", ".yml", ".csv", ".log", ".xml", ".html", ".htm", ".go", ".ts", ".tsx", ".js", ".jsx", ".py", ".java", ".c", ".cpp", ".h", ".hpp", ".rs", ".sh", ".ps1", ".sql", ".toml", ".ini", ".properties":
		return true
	default:
		return false
	}
}

func isImageLike(ext, mimeType string) bool {
	if strings.HasPrefix(mimeType, "image/") {
		return true
	}
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".svg":
		return true
	default:
		return false
	}
}

func isBlockedExecutable(ext, mimeType string) bool {
	if strings.Contains(mimeType, "x-msdownload") || strings.Contains(mimeType, "x-dosexec") {
		return true
	}
	switch ext {
	case ".exe", ".dll", ".msi", ".bat", ".cmd", ".com", ".scr":
		return true
	default:
		return false
	}
}

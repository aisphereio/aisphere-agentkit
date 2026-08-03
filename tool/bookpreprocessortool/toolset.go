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

// Package bookpreprocessortool provides deterministic book ingestion and
// chapter-splitting tools for the book dissector agent.
package bookpreprocessortool

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/adk/tool/projectartifacttool"
)

const (
	sourceArtifactSuffix   = "__source_utf8.txt"
	manifestArtifactSuffix = "__manifest.json"
	chapterArtifactFormat  = "%s__chapter_%04d.txt"
	mountedBookArtifact    = "mounted_book.json"
	userArtifactPrefix     = "user:"
)

var defaultChapterPatterns = []string{
	// Legacy/common chapter titles. Keep these first to preserve existing behavior.
	`(?m)^\s*第[零〇一二三四五六七八九十百千万两0-9]+[章节卷集回篇部]\s*[^\n\r]{0,80}$`,
	`(?m)^\s*Chapter\s+[0-9]+\b[^\n\r]{0,80}$`,
	`(?m)^\s*[0-9]{1,4}[、.．]\s*[^\n\r]{1,80}$`,

	// Standalone front/prologue chapter titles. These are safe because they must occupy
	// the whole line, so "内容简介" or ordinary paragraph text will not be split.
	`(?m)^\s*(?:序|序章|楔子|引子|前言)\s*$`,

	// Story-section titles such as:
	//   【灰狐】楔子 / 【灰狐】第一节 / 【庆忌】01 / 【乖龙】壹 / 【照海】尾
	// This covers anthology/arc based books without replacing the legacy rules above.
	`(?m)^\s*[【\[〔][^】\]〕\r\n]{1,32}[】\]〕]\s*(?:楔子|序|序章|尾声?|尾|终章|番外(?:\s*\S{0,20})?|第[零〇一二三四五六七八九十百千万两0-9]+[章节卷集回篇部]?|[零〇一二三四五六七八九十百千万两壹贰叁肆伍陆柒捌玖拾0-9]{1,8})(?:\s*[：:、.．\-]\s*[^\r\n]{0,60})?\s*$`,
}

// NewToolset creates the deterministic book preprocessing toolset.
func NewToolset() (tool.Toolset, error) {
	ts := &Toolset{}
	builders := []func() (tool.Tool, error){
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_split_preview",
				Description: "Preview deterministic book chapter splitting without saving chapters or manifest. Use this before asking the user to confirm a split.",
			}, ts.SplitPreview)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:                "book_split_commit",
				Description:         "Commit a previously previewed deterministic book split by saving normalized source, chapter artifacts, manifest, and project artifact registrations.",
				RequireConfirmation: true,
			}, ts.SplitCommit)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_split_from_artifact",
				Description: "Load an uploaded text-like artifact, detect/decode text encoding, split it into chapter artifacts, and save a book manifest. Use this before any LLM chapter analysis.",
			}, ts.SplitFromArtifact)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_resplit",
				Description: "Re-split a previously imported book from its normalized UTF-8 source using new chapter title patterns.",
			}, ts.Resplit)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_apply_manual_boundaries",
				Description: "Replace a book's chapter split using explicit manual character boundaries. This is for user-corrected chapter starts/ends.",
			}, ts.ApplyManualBoundaries)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_get_manifest",
				Description: "Load a saved book manifest containing chapter titles, artifacts, character ranges, and split metadata.",
			}, ts.GetManifest)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_get_chapter",
				Description: "Load one split chapter by book_id and chapter_no. Can include previous chapter tail and next chapter head for continuity analysis.",
			}, ts.GetChapter)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_list_books",
				Description: "List imported books in the current artifact workspace by reading saved book manifests.",
			}, ts.ListBooks)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_mount",
				Description: "Mount an imported user-scoped book into the current session, so later requests can refer to the current book without re-splitting.",
			}, ts.MountBook)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_publish_to_library",
				Description: "Copy a legacy session-scoped split book into the user-scoped book library so it can be reused by new sessions. Use this for books split before user-scoped artifacts were introduced.",
			}, ts.PublishBookToLibrary)
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

// Toolset groups all deterministic book preprocessing tools.
type Toolset struct {
	tools []tool.Tool
}

func (t *Toolset) Name() string { return "BookPreprocessorToolset" }

func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	return t.tools, nil
}

func currentProjectID(ctx tool.Context) (string, error) {
	projectID, err := projectartifacttool.ResolveProjectID(ctx, "")
	if err != nil {
		return "", fmt.Errorf("current workspace is not selected; choose a project in the top project selector: %w", err)
	}
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return "", fmt.Errorf("current workspace is not selected; choose a project in the top project selector")
	}
	return projectID, nil
}

func sameProjectID(a, b string) bool {
	return strings.TrimSpace(a) != "" && strings.TrimSpace(a) == strings.TrimSpace(b)
}

func guardLegacySplitWriteAllowed(ctx tool.Context, projectID, bookID string) error {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return nil
	}
	registry, err := projectartifacttool.LoadProject(ctx, projectID)
	if err != nil {
		// Legacy projects may not have a registry yet. In that case there is no
		// framework-level NovelStore state to protect, so preserve old behavior.
		return nil
	}
	bookID = sanitizeBookID(bookID)
	for _, art := range registry.Artifacts {
		if art.Type != "novel.active_split" {
			continue
		}
		artBookID := sanitizeBookID(firstNonEmpty(art.BookID, art.Metadata["book_id"]))
		if bookID != "" && artBookID != "" && artBookID != bookID {
			continue
		}
		splitID := strings.TrimSpace(firstNonEmpty(art.Metadata["split_id"], art.Metadata["active_split_id"]))
		if splitID == "" && strings.TrimSpace(art.ArtifactName) == "" {
			continue
		}
		if art.Metadata != nil && strings.EqualFold(strings.TrimSpace(art.Metadata["status"]), "deleted") {
			continue
		}
		if splitID == "" {
			splitID = strings.TrimSpace(art.ArtifactName)
		}
		return fmt.Errorf("current project already has NovelStore active split %q for book %q; legacy book split writes are blocked by the framework. Use NovelStore chapter APIs or explicitly re-split through NovelStore replace_active flow", splitID, firstNonEmpty(artBookID, bookID, "unknown"))
	}
	return nil
}

// SplitFromArtifactArgs configures a first-time import/split run.
type SplitFromArtifactArgs struct {
	SourceArtifact       string   `json:"source_artifact" jsonschema:"Artifact file name of the uploaded TXT/Markdown/plain-text book to split."`
	Title                string   `json:"title,omitempty" jsonschema:"Optional book title. Defaults to the source artifact name."`
	BookID               string   `json:"book_id,omitempty" jsonschema:"Optional stable book id. If omitted a safe id is generated."`
	EncodingHint         string   `json:"encoding_hint,omitempty" jsonschema:"Optional encoding hint: auto, utf-8, gbk, gb18030."`
	ChapterTitlePatterns []string `json:"chapter_title_patterns,omitempty" jsonschema:"Optional regular expressions for chapter title lines. Go regexp syntax; multiline mode is allowed."`
	MinChapterChars      int      `json:"min_chapter_chars,omitempty" jsonschema:"Minimum characters for a detected chapter chunk. Defaults to 200."`
	SaveChapters         *bool    `json:"save_chapters,omitempty" jsonschema:"Whether to save chapter artifacts. Defaults to true."`
}

// ResplitArgs configures splitting from normalized source.
type ResplitArgs struct {
	BookID               string   `json:"book_id"`
	ChapterTitlePatterns []string `json:"chapter_title_patterns,omitempty"`
	MinChapterChars      int      `json:"min_chapter_chars,omitempty"`
	SaveChapters         *bool    `json:"save_chapters,omitempty"`
}

// ManualBoundary is a user-corrected chapter range in rune/character indexes.
type ManualBoundary struct {
	Title     string `json:"title"`
	StartChar int    `json:"start_char"`
	EndChar   int    `json:"end_char"`
}

// ApplyManualBoundariesArgs replaces the generated split with manual ranges.
type ApplyManualBoundariesArgs struct {
	BookID       string           `json:"book_id"`
	Boundaries   []ManualBoundary `json:"boundaries"`
	SaveChapters *bool            `json:"save_chapters,omitempty"`
}

type BookIDArgs struct {
	BookID string `json:"book_id,omitempty" jsonschema:"Book id. If omitted, the currently mounted book is used."`
}

type MountBookArgs struct {
	BookID string `json:"book_id" jsonschema:"Book id returned by book_list_books or book_split_from_artifact."`
}

type GetChapterArgs struct {
	BookID           string `json:"book_id,omitempty" jsonschema:"Book id. If omitted, the currently mounted book is used."`
	ChapterNo        int    `json:"chapter_no"`
	IncludePrevTail  bool   `json:"include_prev_tail,omitempty"`
	IncludeNextHead  bool   `json:"include_next_head,omitempty"`
	NeighborMaxChars int    `json:"neighbor_max_chars,omitempty"`
	MaxChars         int    `json:"max_chars,omitempty"`
}

type ListBooksArgs struct{}

type SplitResult struct {
	BookID             string           `json:"book_id"`
	ProjectID          string           `json:"project_id,omitempty"`
	Title              string           `json:"title"`
	SourceArtifact     string           `json:"source_artifact"`
	SourceUTF8Artifact string           `json:"source_utf8_artifact"`
	ManifestArtifact   string           `json:"manifest_artifact"`
	Encoding           string           `json:"encoding"`
	ChapterCount       int              `json:"chapter_count"`
	TotalChars         int              `json:"total_chars"`
	TotalBytes         int              `json:"total_bytes"`
	SplitMethod        string           `json:"split_method"`
	Warnings           []string         `json:"warnings,omitempty"`
	ChaptersPreview    []ChapterSummary `json:"chapters_preview"`
}

type BookManifest struct {
	BookID             string           `json:"book_id"`
	ProjectID          string           `json:"project_id,omitempty"`
	Title              string           `json:"title"`
	SourceArtifact     string           `json:"source_artifact"`
	SourceUTF8Artifact string           `json:"source_utf8_artifact"`
	ManifestArtifact   string           `json:"manifest_artifact"`
	Encoding           string           `json:"encoding"`
	TotalChars         int              `json:"total_chars"`
	TotalBytes         int              `json:"total_bytes"`
	ChapterCount       int              `json:"chapter_count"`
	SplitMethod        string           `json:"split_method"`
	ChapterPatterns    []string         `json:"chapter_patterns,omitempty"`
	MinChapterChars    int              `json:"min_chapter_chars"`
	CreatedAt          string           `json:"created_at"`
	UpdatedAt          string           `json:"updated_at"`
	Chapters           []ChapterSummary `json:"chapters"`
	Warnings           []string         `json:"warnings,omitempty"`
	Scope              string           `json:"scope,omitempty"`
}

type MountedBook struct {
	BookID           string `json:"book_id"`
	ProjectID        string `json:"project_id,omitempty"`
	Title            string `json:"title"`
	ManifestArtifact string `json:"manifest_artifact"`
	ChapterCount     int    `json:"chapter_count"`
	MountedAt        string `json:"mounted_at"`
}

type ChapterSummary struct {
	No        int    `json:"no"`
	Title     string `json:"title"`
	Artifact  string `json:"artifact"`
	StartChar int    `json:"start_char"`
	EndChar   int    `json:"end_char"`
	CharCount int    `json:"char_count"`
}

type ManifestResult struct {
	Manifest BookManifest `json:"manifest"`
	Mounted  *MountedBook `json:"mounted,omitempty"`
}

type ChapterResult struct {
	BookID    string `json:"book_id"`
	ChapterNo int    `json:"chapter_no"`
	Title     string `json:"title"`
	Artifact  string `json:"artifact"`
	StartChar int    `json:"start_char"`
	EndChar   int    `json:"end_char"`
	CharCount int    `json:"char_count"`
	Content   string `json:"content"`
	PrevTail  string `json:"prev_tail,omitempty"`
	NextHead  string `json:"next_head,omitempty"`
	Truncated bool   `json:"truncated"`
	MaxChars  int    `json:"max_chars,omitempty"`
}

type ListBooksResult struct {
	Count       int          `json:"count"`
	Books       []BookInfo   `json:"books"`
	MountedBook *MountedBook `json:"mounted_book,omitempty"`
}

type BookInfo struct {
	BookID           string `json:"book_id"`
	ProjectID        string `json:"project_id,omitempty"`
	Title            string `json:"title"`
	ChapterCount     int    `json:"chapter_count"`
	ManifestArtifact string `json:"manifest_artifact"`
	UpdatedAt        string `json:"updated_at"`
	Scope            string `json:"scope"`
	Mounted          bool   `json:"mounted"`
}

type PublishedBookResult struct {
	BookID             string   `json:"book_id"`
	ProjectID          string   `json:"project_id,omitempty"`
	Title              string   `json:"title"`
	ManifestArtifact   string   `json:"manifest_artifact"`
	SourceUTF8Artifact string   `json:"source_utf8_artifact"`
	ChapterCount       int      `json:"chapter_count"`
	Warnings           []string `json:"warnings,omitempty"`
}

type SplitPreviewResult struct {
	Status             string               `json:"status"`
	BookID             string               `json:"book_id"`
	ProjectID          string               `json:"project_id,omitempty"`
	Title              string               `json:"title"`
	SourceArtifact     string               `json:"source_artifact"`
	SourceUTF8Artifact string               `json:"source_utf8_artifact"`
	ManifestArtifact   string               `json:"manifest_artifact"`
	Encoding           string               `json:"encoding"`
	ChapterCount       int                  `json:"chapter_count"`
	TotalChars         int                  `json:"total_chars"`
	TotalBytes         int                  `json:"total_bytes"`
	SplitMethod        string               `json:"split_method"`
	ChapterPatterns    []string             `json:"chapter_patterns,omitempty"`
	MinChapterChars    int                  `json:"min_chapter_chars"`
	SplitAnalysis      ChapterSplitAnalysis `json:"split_analysis,omitempty"`
	Warnings           []string             `json:"warnings,omitempty"`
	ChaptersPreview    []ChapterSummary     `json:"chapters_preview"`
	RequiresConfirm    bool                 `json:"requires_confirm"`
}

func (t *Toolset) SplitPreview(ctx tool.Context, args SplitFromArtifactArgs) (SplitPreviewResult, error) {
	return t.previewSplitFromArtifact(ctx, args)
}

func (t *Toolset) SplitCommit(ctx tool.Context, args SplitFromArtifactArgs) (SplitResult, error) {
	return t.SplitFromArtifact(ctx, args)
}

func (t *Toolset) previewSplitFromArtifact(ctx tool.Context, args SplitFromArtifactArgs) (SplitPreviewResult, error) {
	if strings.TrimSpace(args.SourceArtifact) == "" {
		return SplitPreviewResult{}, fmt.Errorf("source_artifact is required")
	}
	text, encodingName, rawBytes, err := loadArtifactText(ctx, args.SourceArtifact, args.EncodingHint)
	if err != nil {
		return SplitPreviewResult{}, err
	}
	title := strings.TrimSpace(args.Title)
	if title == "" {
		title = strings.TrimSuffix(args.SourceArtifact, ".txt")
	}
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return SplitPreviewResult{}, err
	}
	bookID := sanitizeBookID(args.BookID)
	if bookID == "" {
		bookID = generatedBookID(title, rawBytes)
	}
	patterns := normalizedPatterns(args.ChapterTitlePatterns)
	minChars := minChapterChars(args.MinChapterChars)
	analysis := analyzeChapterCandidates(text, patterns, minChars)
	chunks, warnings, err := splitText(text, patterns, minChars)
	if err != nil {
		return SplitPreviewResult{}, err
	}
	if analysis.NeedLLM {
		warnings = append(warnings, "chapter split confidence is low or ambiguous; ask an LLM to review split_analysis before commit if possible")
	}
	chapters := make([]ChapterSummary, 0, len(chunks))
	for i, chunk := range chunks {
		no := i + 1
		title := strings.TrimSpace(chunk.Title)
		if title == "" {
			title = fmt.Sprintf("第 %d 章", no)
		}
		title = compactOneLine(title, 120)
		chapters = append(chapters, ChapterSummary{
			No:        no,
			Title:     title,
			Artifact:  chapterArtifactName(bookID, no),
			StartChar: chunk.StartChar,
			EndChar:   chunk.EndChar,
			CharCount: utf8.RuneCountInString(chunk.Text),
		})
	}
	preview := chapters
	if len(preview) > 20 {
		preview = preview[:20]
	}
	return SplitPreviewResult{
		Status:             "preview",
		BookID:             bookID,
		ProjectID:          projectID,
		Title:              title,
		SourceArtifact:     args.SourceArtifact,
		SourceUTF8Artifact: sourceArtifactName(bookID),
		ManifestArtifact:   manifestArtifactName(bookID),
		Encoding:           encodingName,
		ChapterCount:       len(chunks),
		TotalChars:         utf8.RuneCountInString(text),
		TotalBytes:         len([]byte(text)),
		SplitMethod:        "auto_pattern_preview",
		ChapterPatterns:    patterns,
		MinChapterChars:    minChars,
		SplitAnalysis:      analysis,
		Warnings:           warnings,
		ChaptersPreview:    preview,
		RequiresConfirm:    true,
	}, nil
}

func (t *Toolset) SplitFromArtifact(ctx tool.Context, args SplitFromArtifactArgs) (SplitResult, error) {
	if strings.TrimSpace(args.SourceArtifact) == "" {
		return SplitResult{}, fmt.Errorf("source_artifact is required")
	}
	text, encodingName, rawBytes, err := loadArtifactText(ctx, args.SourceArtifact, args.EncodingHint)
	if err != nil {
		return SplitResult{}, err
	}
	title := strings.TrimSpace(args.Title)
	if title == "" {
		title = strings.TrimSuffix(args.SourceArtifact, ".txt")
	}
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return SplitResult{}, err
	}
	bookID := sanitizeBookID(args.BookID)
	if bookID == "" {
		bookID = generatedBookID(title, rawBytes)
	}
	if err := guardLegacySplitWriteAllowed(ctx, projectID, bookID); err != nil {
		return SplitResult{}, err
	}
	sourceUTF8Artifact := sourceArtifactName(bookID)
	if err := saveTextArtifact(ctx, sourceUTF8Artifact, text, "text/plain; charset=utf-8"); err != nil {
		return SplitResult{}, fmt.Errorf("save normalized source: %w", err)
	}
	return t.splitAndSave(ctx, splitRequest{
		BookID:             bookID,
		ProjectID:          projectID,
		Title:              title,
		SourceArtifact:     args.SourceArtifact,
		SourceUTF8Artifact: sourceUTF8Artifact,
		Encoding:           encodingName,
		Text:               text,
		Patterns:           normalizedPatterns(args.ChapterTitlePatterns),
		MinChapterChars:    minChapterChars(args.MinChapterChars),
		SaveChapters:       boolDefault(args.SaveChapters, true),
		SplitMethod:        "auto_pattern",
		ExistingCreatedAt:  "",
	})
}

func (t *Toolset) Resplit(ctx tool.Context, args ResplitArgs) (SplitResult, error) {
	bookID := sanitizeBookID(args.BookID)
	if bookID == "" {
		return SplitResult{}, fmt.Errorf("book_id is required")
	}
	manifest, err := loadManifest(ctx, bookID)
	if err != nil {
		return SplitResult{}, err
	}
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return SplitResult{}, err
	}
	if !sameProjectID(manifest.ProjectID, projectID) {
		return SplitResult{}, fmt.Errorf("book %s does not belong to the current workspace", bookID)
	}
	if err := guardLegacySplitWriteAllowed(ctx, projectID, bookID); err != nil {
		return SplitResult{}, err
	}
	text, _, _, err := loadArtifactText(ctx, manifest.SourceUTF8Artifact, "utf-8")
	if err != nil {
		return SplitResult{}, err
	}
	patterns := normalizedPatterns(args.ChapterTitlePatterns)
	return t.splitAndSave(ctx, splitRequest{
		BookID:             bookID,
		ProjectID:          projectID,
		Title:              manifest.Title,
		SourceArtifact:     manifest.SourceArtifact,
		SourceUTF8Artifact: manifest.SourceUTF8Artifact,
		Encoding:           manifest.Encoding,
		Text:               text,
		Patterns:           patterns,
		MinChapterChars:    minChapterChars(args.MinChapterChars),
		SaveChapters:       boolDefault(args.SaveChapters, true),
		SplitMethod:        "auto_pattern_resplit",
		ExistingCreatedAt:  manifest.CreatedAt,
	})
}

func (t *Toolset) ApplyManualBoundaries(ctx tool.Context, args ApplyManualBoundariesArgs) (SplitResult, error) {
	bookID := sanitizeBookID(args.BookID)
	if bookID == "" {
		return SplitResult{}, fmt.Errorf("book_id is required")
	}
	if len(args.Boundaries) == 0 {
		return SplitResult{}, fmt.Errorf("boundaries cannot be empty")
	}
	manifest, err := loadManifest(ctx, bookID)
	if err != nil {
		return SplitResult{}, err
	}
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return SplitResult{}, err
	}
	if !sameProjectID(manifest.ProjectID, projectID) {
		return SplitResult{}, fmt.Errorf("book %s does not belong to the current workspace", bookID)
	}
	if err := guardLegacySplitWriteAllowed(ctx, projectID, bookID); err != nil {
		return SplitResult{}, err
	}
	text, _, _, err := loadArtifactText(ctx, manifest.SourceUTF8Artifact, "utf-8")
	if err != nil {
		return SplitResult{}, err
	}
	runes := []rune(text)
	total := len(runes)
	boundaries := append([]ManualBoundary(nil), args.Boundaries...)
	sort.SliceStable(boundaries, func(i, j int) bool { return boundaries[i].StartChar < boundaries[j].StartChar })

	chunks := make([]chapterChunk, 0, len(boundaries))
	warnings := []string{}
	prevEnd := 0
	for i, b := range boundaries {
		if b.StartChar < 0 || b.EndChar < 0 || b.StartChar >= b.EndChar || b.EndChar > total {
			return SplitResult{}, fmt.Errorf("invalid boundary at index %d: start=%d end=%d total=%d", i, b.StartChar, b.EndChar, total)
		}
		if i > 0 && b.StartChar < prevEnd {
			return SplitResult{}, fmt.Errorf("boundary index %d overlaps previous range: start=%d previous_end=%d", i, b.StartChar, prevEnd)
		}
		if b.StartChar > prevEnd {
			gap := strings.TrimSpace(string(runes[prevEnd:b.StartChar]))
			if gap != "" {
				warnings = append(warnings, fmt.Sprintf("manual boundaries leave non-empty gap between char %d and %d", prevEnd, b.StartChar))
			}
		}
		chunkText := strings.TrimSpace(string(runes[b.StartChar:b.EndChar]))
		title := strings.TrimSpace(b.Title)
		if title == "" {
			title = firstNonEmptyLine(chunkText)
		}
		chunks = append(chunks, chapterChunk{Title: title, Text: chunkText, StartChar: b.StartChar, EndChar: b.EndChar})
		prevEnd = b.EndChar
	}
	if prevEnd < total && strings.TrimSpace(string(runes[prevEnd:])) != "" {
		warnings = append(warnings, fmt.Sprintf("manual boundaries leave non-empty tail after char %d", prevEnd))
	}
	return t.saveChunks(ctx, manifest, chunks, "manual_boundaries", nil, manifest.MinChapterChars, boolDefault(args.SaveChapters, true), warnings)
}

func (t *Toolset) GetManifest(ctx tool.Context, args BookIDArgs) (ManifestResult, error) {
	bookID, mounted, err := resolveBookID(ctx, args.BookID)
	if err != nil {
		return ManifestResult{}, err
	}
	manifest, err := loadManifest(ctx, bookID)
	if err != nil {
		return ManifestResult{}, err
	}
	return ManifestResult{Manifest: manifest, Mounted: mounted}, nil
}

func (t *Toolset) GetChapter(ctx tool.Context, args GetChapterArgs) (ChapterResult, error) {
	bookID, _, err := resolveBookID(ctx, args.BookID)
	if err != nil {
		return ChapterResult{}, err
	}
	manifest, err := loadManifest(ctx, bookID)
	if err != nil {
		return ChapterResult{}, err
	}
	if args.ChapterNo <= 0 || args.ChapterNo > len(manifest.Chapters) {
		return ChapterResult{}, fmt.Errorf("chapter_no %d out of range 1..%d", args.ChapterNo, len(manifest.Chapters))
	}
	ch := manifest.Chapters[args.ChapterNo-1]
	text, _, _, err := loadArtifactText(ctx, ch.Artifact, "utf-8")
	if err != nil {
		return ChapterResult{}, err
	}
	maxChars := args.MaxChars
	truncated := false
	if maxChars > 0 {
		text, truncated = limitRunes(text, maxChars)
	}
	result := ChapterResult{
		BookID:    bookID,
		ChapterNo: ch.No,
		Title:     ch.Title,
		Artifact:  ch.Artifact,
		StartChar: ch.StartChar,
		EndChar:   ch.EndChar,
		CharCount: ch.CharCount,
		Content:   text,
		Truncated: truncated,
		MaxChars:  maxChars,
	}
	neighborMax := args.NeighborMaxChars
	if neighborMax <= 0 {
		neighborMax = 800
	}
	if args.IncludePrevTail && args.ChapterNo > 1 {
		prev := manifest.Chapters[args.ChapterNo-2]
		prevText, _, _, err := loadArtifactText(ctx, prev.Artifact, "utf-8")
		if err == nil {
			result.PrevTail = tailRunes(prevText, neighborMax)
		}
	}
	if args.IncludeNextHead && args.ChapterNo < len(manifest.Chapters) {
		next := manifest.Chapters[args.ChapterNo]
		nextText, _, _, err := loadArtifactText(ctx, next.Artifact, "utf-8")
		if err == nil {
			result.NextHead = headRunes(nextText, neighborMax)
		}
	}
	return result, nil
}

func (t *Toolset) ListBooks(ctx tool.Context, args ListBooksArgs) (ListBooksResult, error) {
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return ListBooksResult{}, err
	}
	resp, err := ctx.Artifacts().List(ctx)
	if err != nil {
		return ListBooksResult{}, fmt.Errorf("list artifacts: %w", err)
	}
	mounted, _ := loadMountedBook(ctx)
	if mounted != nil && !sameProjectID(mounted.ProjectID, projectID) {
		mounted = nil
	}
	booksByID := map[string]BookInfo{}
	for _, name := range resp.FileNames {
		if !strings.HasSuffix(name, manifestArtifactSuffix) {
			continue
		}
		text, _, _, err := loadArtifactText(ctx, name, "utf-8")
		if err != nil {
			continue
		}
		var m BookManifest
		if err := json.Unmarshal([]byte(text), &m); err != nil {
			continue
		}
		if !sameProjectID(m.ProjectID, projectID) {
			continue
		}
		scope := "session"
		if isUserScopedArtifactName(name) || m.Scope == "user" {
			scope = "user"
		}
		mountedFlag := mounted != nil && mounted.BookID == m.BookID
		info := BookInfo{BookID: m.BookID, ProjectID: "", Title: m.Title, ChapterCount: m.ChapterCount, ManifestArtifact: name, UpdatedAt: m.UpdatedAt, Scope: scope, Mounted: mountedFlag}
		if old, ok := booksByID[m.BookID]; !ok || bookInfoLess(old, info) {
			booksByID[m.BookID] = info
		}
	}
	books := make([]BookInfo, 0, len(booksByID))
	for _, b := range booksByID {
		books = append(books, b)
	}
	sort.SliceStable(books, func(i, j int) bool {
		if books[i].Mounted != books[j].Mounted {
			return books[i].Mounted
		}
		return books[i].UpdatedAt > books[j].UpdatedAt
	})
	return ListBooksResult{Count: len(books), Books: books, MountedBook: mounted}, nil
}

func (t *Toolset) MountBook(ctx tool.Context, args MountBookArgs) (MountedBook, error) {
	bookID := sanitizeBookID(args.BookID)
	if bookID == "" {
		return MountedBook{}, fmt.Errorf("book_id is required")
	}
	manifest, err := loadManifest(ctx, bookID)
	if err != nil {
		return MountedBook{}, err
	}
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return MountedBook{}, err
	}
	if !sameProjectID(manifest.ProjectID, projectID) {
		return MountedBook{}, fmt.Errorf("book %s does not belong to the current workspace", bookID)
	}
	mounted := MountedBook{
		BookID:           manifest.BookID,
		ProjectID:        projectID,
		Title:            manifest.Title,
		ManifestArtifact: manifest.ManifestArtifact,
		ChapterCount:     manifest.ChapterCount,
		MountedAt:        nowRFC3339(),
	}
	data, err := json.MarshalIndent(mounted, "", "  ")
	if err != nil {
		return MountedBook{}, err
	}
	if err := saveTextArtifact(ctx, mountedBookArtifact, string(data), "application/json; charset=utf-8"); err != nil {
		return MountedBook{}, fmt.Errorf("save mounted book pointer: %w", err)
	}
	registry, _, err := projectartifacttool.EnsureProject(ctx, projectartifacttool.EnsureProjectRequest{
		ProjectID:   projectID,
		Name:        projectID,
		DisplayName: firstNonEmpty(mounted.Title, projectID),
		Description: fmt.Sprintf("《%s》拆书项目，共 %d 章。", firstNonEmpty(mounted.Title, mounted.BookID), mounted.ChapterCount),
		Tags:        []string{"book", "dissection", "skill"},
	})
	if err != nil {
		return MountedBook{}, err
	}
	if _, err := projectartifacttool.MountProject(ctx, registry); err != nil {
		return MountedBook{}, err
	}
	return mounted, nil
}

func (t *Toolset) PublishBookToLibrary(ctx tool.Context, args BookIDArgs) (PublishedBookResult, error) {
	bookID, _, err := resolveBookID(ctx, args.BookID)
	if err != nil {
		// Publishing is often used before mounting; fall back to explicit id validation for clearer errors.
		bookID = sanitizeBookID(args.BookID)
		if bookID == "" {
			return PublishedBookResult{}, err
		}
	}
	manifest, err := loadManifest(ctx, bookID)
	if err != nil {
		return PublishedBookResult{}, err
	}
	warnings := []string{}
	if manifest.SourceUTF8Artifact != "" {
		text, _, _, err := loadArtifactText(ctx, manifest.SourceUTF8Artifact, "utf-8")
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("source_utf8 artifact not copied: %v", err))
		} else {
			manifest.SourceUTF8Artifact = sourceArtifactName(bookID)
			if err := saveTextArtifact(ctx, manifest.SourceUTF8Artifact, text, "text/plain; charset=utf-8"); err != nil {
				return PublishedBookResult{}, fmt.Errorf("save user-scoped source: %w", err)
			}
		}
	}
	for i := range manifest.Chapters {
		oldName := manifest.Chapters[i].Artifact
		text, _, _, err := loadArtifactText(ctx, oldName, "utf-8")
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("chapter %d not copied: %v", manifest.Chapters[i].No, err))
			continue
		}
		newName := chapterArtifactName(bookID, manifest.Chapters[i].No)
		if err := saveTextArtifact(ctx, newName, text, "text/plain; charset=utf-8"); err != nil {
			return PublishedBookResult{}, fmt.Errorf("save user-scoped chapter %d: %w", manifest.Chapters[i].No, err)
		}
		manifest.Chapters[i].Artifact = newName
	}
	manifest.ManifestArtifact = manifestArtifactName(bookID)
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return PublishedBookResult{}, err
	}
	manifest.ProjectID = projectID
	manifest.Scope = "user"
	manifest.UpdatedAt = nowRFC3339()
	manifest.Warnings = append(manifest.Warnings, warnings...)
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return PublishedBookResult{}, err
	}
	if err := saveTextArtifact(ctx, manifest.ManifestArtifact, string(data), "application/json; charset=utf-8"); err != nil {
		return PublishedBookResult{}, fmt.Errorf("save user-scoped manifest: %w", err)
	}
	if err := registerBookProjectArtifacts(ctx, manifest, true); err != nil {
		return PublishedBookResult{}, fmt.Errorf("register project artifacts: %w", err)
	}
	return PublishedBookResult{
		BookID:             manifest.BookID,
		ProjectID:          "",
		Title:              manifest.Title,
		ManifestArtifact:   manifest.ManifestArtifact,
		SourceUTF8Artifact: manifest.SourceUTF8Artifact,
		ChapterCount:       manifest.ChapterCount,
		Warnings:           warnings,
	}, nil
}

type splitRequest struct {
	BookID             string
	ProjectID          string
	Title              string
	SourceArtifact     string
	SourceUTF8Artifact string
	Encoding           string
	Text               string
	Patterns           []string
	MinChapterChars    int
	SaveChapters       bool
	SplitMethod        string
	ExistingCreatedAt  string
}

type chapterChunk struct {
	Title     string
	Text      string
	StartChar int
	EndChar   int
}

func (t *Toolset) splitAndSave(ctx tool.Context, req splitRequest) (SplitResult, error) {
	chunks, warnings, err := splitText(req.Text, req.Patterns, req.MinChapterChars)
	if err != nil {
		return SplitResult{}, err
	}
	createdAt := req.ExistingCreatedAt
	if createdAt == "" {
		createdAt = nowRFC3339()
	}
	manifest := BookManifest{
		BookID:             req.BookID,
		ProjectID:          req.ProjectID,
		Title:              req.Title,
		SourceArtifact:     req.SourceArtifact,
		SourceUTF8Artifact: req.SourceUTF8Artifact,
		ManifestArtifact:   manifestArtifactName(req.BookID),
		Encoding:           req.Encoding,
		TotalChars:         utf8.RuneCountInString(req.Text),
		TotalBytes:         len([]byte(req.Text)),
		SplitMethod:        req.SplitMethod,
		ChapterPatterns:    req.Patterns,
		MinChapterChars:    req.MinChapterChars,
		CreatedAt:          createdAt,
		UpdatedAt:          nowRFC3339(),
		Warnings:           warnings,
		Scope:              "user",
	}
	return t.saveChunks(ctx, manifest, chunks, req.SplitMethod, req.Patterns, req.MinChapterChars, req.SaveChapters, warnings)
}

func (t *Toolset) saveChunks(ctx tool.Context, manifest BookManifest, chunks []chapterChunk, splitMethod string, patterns []string, minChars int, saveChapters bool, warnings []string) (SplitResult, error) {
	manifest.SplitMethod = splitMethod
	manifest.ChapterPatterns = patterns
	manifest.MinChapterChars = minChars
	manifest.UpdatedAt = nowRFC3339()
	manifest.ChapterCount = len(chunks)
	manifest.Warnings = warnings
	manifest.ManifestArtifact = manifestArtifactName(manifest.BookID)
	manifest.Chapters = make([]ChapterSummary, 0, len(chunks))

	for i, chunk := range chunks {
		no := i + 1
		artifactName := chapterArtifactName(manifest.BookID, no)
		title := strings.TrimSpace(chunk.Title)
		if title == "" {
			title = fmt.Sprintf("第%d章", no)
		}
		title = compactOneLine(title, 120)
		if saveChapters {
			if err := saveTextArtifact(ctx, artifactName, chunk.Text, "text/plain; charset=utf-8"); err != nil {
				return SplitResult{}, fmt.Errorf("save chapter %d: %w", no, err)
			}
		}
		manifest.Chapters = append(manifest.Chapters, ChapterSummary{
			No:        no,
			Title:     title,
			Artifact:  artifactName,
			StartChar: chunk.StartChar,
			EndChar:   chunk.EndChar,
			CharCount: utf8.RuneCountInString(chunk.Text),
		})
	}

	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return SplitResult{}, err
	}
	if err := saveTextArtifact(ctx, manifest.ManifestArtifact, string(manifestJSON), "application/json; charset=utf-8"); err != nil {
		return SplitResult{}, fmt.Errorf("save manifest: %w", err)
	}
	if err := registerBookProjectArtifacts(ctx, manifest, saveChapters); err != nil {
		return SplitResult{}, fmt.Errorf("register project artifacts: %w", err)
	}
	preview := manifest.Chapters
	if len(preview) > 20 {
		preview = preview[:20]
	}
	return SplitResult{
		BookID:             manifest.BookID,
		ProjectID:          manifest.ProjectID,
		Title:              manifest.Title,
		SourceArtifact:     manifest.SourceArtifact,
		SourceUTF8Artifact: manifest.SourceUTF8Artifact,
		ManifestArtifact:   manifest.ManifestArtifact,
		Encoding:           manifest.Encoding,
		ChapterCount:       manifest.ChapterCount,
		TotalChars:         manifest.TotalChars,
		TotalBytes:         manifest.TotalBytes,
		SplitMethod:        manifest.SplitMethod,
		Warnings:           manifest.Warnings,
		ChaptersPreview:    preview,
	}, nil
}

func registerBookProjectArtifacts(ctx tool.Context, manifest BookManifest, chaptersSaved bool) error {
	projectID := strings.TrimSpace(manifest.ProjectID)
	if projectID == "" {
		resolved, err := currentProjectID(ctx)
		if err != nil {
			return err
		}
		projectID = resolved
	}
	registry, _, err := projectartifacttool.EnsureProject(ctx, projectartifacttool.EnsureProjectRequest{
		ProjectID:   projectID,
		Name:        projectID,
		DisplayName: firstNonEmpty(manifest.Title, projectID),
		Description: fmt.Sprintf("《%s》拆书项目，共 %d 章。", firstNonEmpty(manifest.Title, manifest.BookID), manifest.ChapterCount),
		Tags:        []string{"book", "dissection", "skill"},
	})
	if err != nil {
		return err
	}
	if _, err := projectartifacttool.MountProject(ctx, registry); err != nil {
		return err
	}

	// A book split is a replaceable derived asset set. Re-splitting/manual boundary
	// correction may reduce, merge, or renumber chapters. If we only upsert new
	// chapter entries, stale chapter registry rows from the previous split remain
	// visible in Project/Admin/Web and look like duplicate chapters. Replace the
	// registered chapter set for this book before adding the current manifest.
	chapterNamePrefix := strings.TrimSuffix(chapterArtifactName(manifest.BookID, 1), "0001.txt")
	if _, _, err := projectartifacttool.RemoveArtifacts(ctx, projectID, func(art projectartifacttool.ProjectArtifact) bool {
		if art.Type != "book.chapter" {
			return false
		}
		if strings.TrimSpace(art.BookID) == manifest.BookID {
			return true
		}
		return strings.HasPrefix(strings.TrimSpace(art.ArtifactName), chapterNamePrefix)
	}); err != nil {
		return fmt.Errorf("clear stale book.chapter registry entries: %w", err)
	}

	if manifest.SourceUTF8Artifact != "" {
		if _, _, err := projectartifacttool.RegisterArtifact(ctx, projectartifacttool.RegisterArtifactRequest{
			ProjectID:        projectID,
			ArtifactName:     manifest.SourceUTF8Artifact,
			Type:             "book.source",
			Title:            "原始书籍正文",
			Description:      "规范化为 UTF-8 后的小说全文，供重切章和人工校验使用。",
			ProducerAgent:    "book_dissector",
			Visibility:       projectartifacttool.VisibilityProjectVisible,
			DefaultForAgents: []string{"book_dissector"},
			BookID:           manifest.BookID,
		}); err != nil {
			return err
		}
	}
	if _, _, err := projectartifacttool.RegisterArtifact(ctx, projectartifacttool.RegisterArtifactRequest{
		ProjectID:        projectID,
		ArtifactName:     manifest.ManifestArtifact,
		Type:             "book.chapter_manifest",
		Title:            "章节索引",
		Description:      "记录章节标题、序号、正文 artifact、字符范围，是后续拆书和 Skill 长任务的默认入口。",
		ProducerAgent:    "book_dissector",
		Visibility:       projectartifacttool.VisibilityProjectDefault,
		Mountable:        boolPtr(true),
		DefaultForAgents: []string{"book_dissector", "book_skill_runner"},
		BookID:           manifest.BookID,
		Metadata: map[string]string{
			"chapter_count": fmt.Sprintf("%d", manifest.ChapterCount),
			"split_method":  manifest.SplitMethod,
		},
	}); err != nil {
		return err
	}
	if !chaptersSaved {
		return nil
	}
	for _, ch := range manifest.Chapters {
		if strings.TrimSpace(ch.Artifact) == "" {
			continue
		}
		if _, _, err := projectartifacttool.RegisterArtifact(ctx, projectartifacttool.RegisterArtifactRequest{
			ProjectID:        projectID,
			ArtifactName:     ch.Artifact,
			Type:             "book.chapter",
			Title:            firstNonEmpty(ch.Title, fmt.Sprintf("第 %d 章", ch.No)),
			Description:      fmt.Sprintf("《%s》第 %d 章正文。", firstNonEmpty(manifest.Title, manifest.BookID), ch.No),
			ProducerAgent:    "book_dissector",
			Visibility:       projectartifacttool.VisibilityProjectVisible,
			Mountable:        boolPtr(true),
			DefaultForAgents: []string{"book_skill_runner"},
			BookID:           manifest.BookID,
			StartChapter:     ch.No,
			EndChapter:       ch.No,
			Metadata: map[string]string{
				"char_count": fmt.Sprintf("%d", ch.CharCount),
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func boolPtr(v bool) *bool { return &v }

func splitText(text string, patterns []string, minChars int) ([]chapterChunk, []string, error) {
	text = normalizeNewlines(text)
	if strings.TrimSpace(text) == "" {
		return nil, nil, fmt.Errorf("book text is empty")
	}
	matches, matchWarnings := findChapterTitleMatches(text, patterns)
	warnings := append([]string{}, matchWarnings...)
	if len(matches) == 0 {
		warnings = append(warnings, "no chapter title matched; saved the whole text as one chapter")
		return []chapterChunk{{Title: "全文", Text: strings.TrimSpace(text), StartChar: 0, EndChar: utf8.RuneCountInString(text)}}, warnings, nil
	}

	chunks := []chapterChunk{}
	if matches[0].StartByte > 0 {
		prefaceText := strings.TrimSpace(text[:matches[0].StartByte])
		if prefaceText != "" {
			warnings = append(warnings, fmt.Sprintf("ignored non-chapter preface text before the first matched chapter title: %d chars", utf8.RuneCountInString(prefaceText)))
		}
	}

	for i, m := range matches {
		start := m.StartByte
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1].StartByte
		}
		chunkText := strings.TrimSpace(text[start:end])
		if chunkText == "" {
			continue
		}
		charCount := utf8.RuneCountInString(chunkText)
		if charCount < minChars {
			warnings = append(warnings, fmt.Sprintf("chapter candidate %q is short: %d chars", m.Title, charCount))
		}
		chunks = append(chunks, chapterChunk{Title: m.Title, Text: chunkText, StartChar: byteToRuneIndex(text, start), EndChar: byteToRuneIndex(text, end)})
	}
	if len(chunks) == 0 {
		warnings = append(warnings, "all chapter candidates were empty; saved the whole text as one chapter")
		chunks = []chapterChunk{{Title: "全文", Text: strings.TrimSpace(text), StartChar: 0, EndChar: utf8.RuneCountInString(text)}}
	}
	return chunks, warnings, nil
}

type titleMatch struct {
	StartByte int
	EndByte   int
	Title     string
}

// ChapterSplitAnalysis is intentionally lightweight and preview-oriented. The
// deterministic splitter still owns the actual cut; an LLM may inspect this
// sequence summary only when NeedLLM is true, then recommend a pattern group or
// manual boundaries without seeing or rewriting the full book text.
type ChapterSplitAnalysis struct {
	SelectedName string                         `json:"selected_name,omitempty"`
	Confidence   float64                        `json:"confidence"`
	NeedLLM      bool                           `json:"need_llm"`
	Groups       []ChapterPatternCandidateGroup `json:"groups,omitempty"`
	Warnings     []string                       `json:"warnings,omitempty"`
}

type ChapterPatternCandidateGroup struct {
	Name     string   `json:"name"`
	Pattern  string   `json:"pattern,omitempty"`
	Count    int      `json:"count"`
	Score    float64  `json:"score"`
	AvgChars int      `json:"avg_chars,omitempty"`
	MinChars int      `json:"min_chars,omitempty"`
	MaxChars int      `json:"max_chars,omitempty"`
	Samples  []string `json:"samples,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func analyzeChapterCandidates(text string, patterns []string, minChars int) ChapterSplitAnalysis {
	text = normalizeNewlines(text)
	analysis := ChapterSplitAnalysis{}
	if strings.TrimSpace(text) == "" {
		analysis.NeedLLM = false
		analysis.Warnings = append(analysis.Warnings, "book text is empty")
		return analysis
	}

	groups := make([]ChapterPatternCandidateGroup, 0, len(patterns)+1)
	for i, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			groups = append(groups, ChapterPatternCandidateGroup{
				Name:     chapterPatternName(p, i),
				Pattern:  p,
				Warnings: []string{fmt.Sprintf("invalid regexp: %v", err)},
			})
			continue
		}
		idxs := re.FindAllStringIndex(text, -1)
		matches := make([]titleMatch, 0, len(idxs))
		for _, idx := range idxs {
			matches = append(matches, titleMatch{
				StartByte: idx[0],
				EndByte:   idx[1],
				Title:     compactOneLine(text[idx[0]:idx[1]], 120),
			})
		}
		groups = append(groups, scoreCandidateGroup(text, chapterPatternName(p, i), p, matches, minChars))
	}

	combinedMatches, combinedWarnings := findChapterTitleMatches(text, patterns)
	combined := scoreCandidateGroup(text, "combined_active_patterns", "", combinedMatches, minChars)
	combined.Warnings = append(combined.Warnings, combinedWarnings...)
	if len(patterns) > 1 || combined.Count > 0 {
		groups = append(groups, combined)
	}

	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].Score != groups[j].Score {
			return groups[i].Score > groups[j].Score
		}
		return groups[i].Count > groups[j].Count
	})
	analysis.Groups = groups

	if len(groups) == 0 || groups[0].Count == 0 {
		analysis.Confidence = 0
		analysis.NeedLLM = false
		analysis.Warnings = append(analysis.Warnings, "no candidate chapter titles matched")
		return analysis
	}

	best := groups[0]
	analysis.SelectedName = best.Name
	analysis.Confidence = best.Score

	if best.Score < 0.65 {
		analysis.NeedLLM = true
		analysis.Warnings = append(analysis.Warnings, "best chapter candidate group has low score")
	}
	if len(groups) > 1 && groups[1].Count > 0 && groups[1].Score >= best.Score*0.85 {
		analysis.NeedLLM = true
		analysis.Warnings = append(analysis.Warnings, "multiple chapter candidate groups have close scores")
	}
	return analysis
}

func scoreCandidateGroup(text, name, pattern string, matches []titleMatch, minChars int) ChapterPatternCandidateGroup {
	group := ChapterPatternCandidateGroup{Name: name, Pattern: pattern, Count: len(matches)}
	if len(matches) == 0 {
		return group
	}
	chunkSizes := make([]int, 0, len(matches))
	for i, m := range matches {
		end := len(text)
		if i+1 < len(matches) {
			end = matches[i+1].StartByte
		}
		chunkText := strings.TrimSpace(text[m.StartByte:end])
		chunkChars := utf8.RuneCountInString(chunkText)
		chunkSizes = append(chunkSizes, chunkChars)
		if len(group.Samples) < 12 {
			group.Samples = append(group.Samples, m.Title)
		}
	}

	total := 0
	shortCount := 0
	group.MinChars = chunkSizes[0]
	group.MaxChars = chunkSizes[0]
	for _, n := range chunkSizes {
		total += n
		if n < group.MinChars {
			group.MinChars = n
		}
		if n > group.MaxChars {
			group.MaxChars = n
		}
		if n < minChars {
			shortCount++
		}
	}
	group.AvgChars = total / len(chunkSizes)

	score := 0.0
	switch {
	case group.Count >= 10:
		score += 0.45
	case group.Count >= 5:
		score += 0.35
	case group.Count >= 2:
		score += 0.20
	default:
		score += 0.05
	}
	if group.AvgChars >= maxInt(minChars, 300) {
		score += 0.25
	} else if group.AvgChars >= maxInt(minChars/2, 100) {
		score += 0.15
	}
	shortRatio := float64(shortCount) / float64(len(chunkSizes))
	if shortRatio <= 0.10 {
		score += 0.20
	} else if shortRatio <= 0.25 {
		score += 0.10
	} else {
		group.Warnings = append(group.Warnings, fmt.Sprintf("too many short chunks: %.0f%% below min_chapter_chars", shortRatio*100))
	}
	if !hasOverlongCandidateTitle(matches, 80) {
		score += 0.10
	}
	if score > 1 {
		score = 1
	}
	group.Score = score
	return group
}

func chapterPatternName(pattern string, index int) string {
	if index >= 0 && index < len(defaultChapterPatterns) && pattern == defaultChapterPatterns[index] {
		switch index {
		case 0:
			return "legacy_chinese_ordinal"
		case 1:
			return "legacy_english_chapter"
		case 2:
			return "legacy_numeric_title"
		case 3:
			return "standalone_prologue"
		case 4:
			return "bracket_story_section"
		}
	}
	return fmt.Sprintf("custom_pattern_%d", index+1)
}

func hasOverlongCandidateTitle(matches []titleMatch, maxRunes int) bool {
	for _, m := range matches {
		if utf8.RuneCountInString(m.Title) > maxRunes {
			return true
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func findChapterTitleMatches(text string, patterns []string) ([]titleMatch, []string) {
	byStart := map[int]titleMatch{}
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			continue
		}
		idxs := re.FindAllStringIndex(text, -1)
		for _, idx := range idxs {
			title := compactOneLine(text[idx[0]:idx[1]], 120)
			if existing, ok := byStart[idx[0]]; !ok || len(title) > len(existing.Title) {
				byStart[idx[0]] = titleMatch{StartByte: idx[0], EndByte: idx[1], Title: title}
			}
		}
	}
	matches := make([]titleMatch, 0, len(byStart))
	for _, m := range byStart {
		matches = append(matches, m)
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].StartByte < matches[j].StartByte })
	return collapseAdjacentDuplicateTitleMatches(matches)
}

func collapseAdjacentDuplicateTitleMatches(matches []titleMatch) ([]titleMatch, []string) {
	if len(matches) < 2 {
		return matches, nil
	}
	const adjacentDuplicateMaxGapBytes = 240
	collapsed := make([]titleMatch, 0, len(matches))
	warnings := []string{}
	for _, m := range matches {
		if len(collapsed) == 0 {
			collapsed = append(collapsed, m)
			continue
		}
		prev := collapsed[len(collapsed)-1]
		gap := m.StartByte - prev.EndByte
		if gap >= 0 && gap <= adjacentDuplicateMaxGapBytes && isDuplicateChapterTitle(prev.Title, m.Title) {
			chosen := chooseDuplicateTitle(prev, m)
			collapsed[len(collapsed)-1] = chosen
			warnings = append(warnings, fmt.Sprintf("deduplicated adjacent duplicate chapter title %q / %q; kept %q", prev.Title, m.Title, chosen.Title))
			continue
		}
		collapsed = append(collapsed, m)
	}
	return collapsed, warnings
}

func chooseDuplicateTitle(a, b titleMatch) titleMatch {
	if len([]rune(b.Title)) >= len([]rune(a.Title)) {
		return b
	}
	return a
}

func isDuplicateChapterTitle(a, b string) bool {
	aKey := normalizeChapterTitleKey(a)
	bKey := normalizeChapterTitleKey(b)
	if aKey != "" && aKey == bKey {
		return true
	}
	aOrdinal := chapterOrdinalKey(aKey)
	bOrdinal := chapterOrdinalKey(bKey)
	return aOrdinal != "" && aOrdinal == bOrdinal
}

func normalizeChapterTitleKey(title string) string {
	var b strings.Builder
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

func chapterOrdinalKey(normalizedTitle string) string {
	runes := []rune(normalizedTitle)
	if len(runes) == 0 {
		return ""
	}
	if runes[0] == '第' {
		for i, r := range runes {
			switch r {
			case '章', '节', '回', '卷', '集':
				if i > 0 {
					return string(runes[:i+1])
				}
			}
		}
	}
	const englishPrefix = "chapter"
	if strings.HasPrefix(normalizedTitle, englishPrefix) {
		i := len(englishPrefix)
		j := i
		for j < len(normalizedTitle) {
			r, size := utf8.DecodeRuneInString(normalizedTitle[j:])
			if !unicode.IsNumber(r) {
				break
			}
			j += size
		}
		if j > i {
			return normalizedTitle[:j]
		}
	}
	return ""
}

func loadManifest(ctx tool.Context, bookID string) (BookManifest, error) {
	bookID = sanitizeBookID(bookID)
	if bookID == "" {
		return BookManifest{}, fmt.Errorf("book_id is required; call book_list_books then book_mount first, or pass book_id explicitly")
	}
	names := []string{manifestArtifactName(bookID), legacyManifestArtifactName(bookID)}
	var lastErr error
	for _, name := range names {
		text, _, _, err := loadArtifactText(ctx, name, "utf-8")
		if err != nil {
			lastErr = err
			continue
		}
		var manifest BookManifest
		if err := json.Unmarshal([]byte(text), &manifest); err != nil {
			return BookManifest{}, fmt.Errorf("decode manifest %q: %w", name, err)
		}
		projectID, projectErr := currentProjectID(ctx)
		if projectErr != nil {
			return BookManifest{}, projectErr
		}
		if !sameProjectID(manifest.ProjectID, projectID) {
			lastErr = fmt.Errorf("manifest %q does not belong to the current workspace", name)
			continue
		}
		if manifest.ManifestArtifact == "" {
			manifest.ManifestArtifact = name
		}
		return manifest, nil
	}
	if lastErr != nil {
		return BookManifest{}, lastErr
	}
	return BookManifest{}, fmt.Errorf("manifest for book_id %q not found", bookID)
}

func loadArtifactText(ctx tool.Context, name, encodingHint string) (string, string, []byte, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", nil, fmt.Errorf("artifact name is required")
	}
	resp, err := ctx.Artifacts().Load(ctx, name)
	if err != nil {
		return "", "", nil, fmt.Errorf("load artifact %q: %w", name, err)
	}
	if resp == nil || resp.Part == nil {
		return "", "", nil, fmt.Errorf("artifact %q is empty", name)
	}
	if resp.Part.Text != "" {
		raw := []byte(resp.Part.Text)
		return normalizeNewlines(resp.Part.Text), "utf-8", raw, nil
	}
	if resp.Part.InlineData == nil {
		return "", "", nil, fmt.Errorf("artifact %q has no text or inline data", name)
	}
	raw := resp.Part.InlineData.Data
	text, enc, err := decodeBytes(raw, encodingHint)
	if err != nil {
		return "", "", nil, err
	}
	return normalizeNewlines(text), enc, raw, nil
}

func decodeBytes(raw []byte, hint string) (string, string, error) {
	hint = strings.ToLower(strings.TrimSpace(hint))
	switch hint {
	case "", "auto":
		if utf8.Valid(raw) {
			return strings.TrimPrefix(string(raw), "\ufeff"), "utf-8", nil
		}
		decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
		if err != nil {
			return "", "", fmt.Errorf("decode as gb18030: %w", err)
		}
		return strings.TrimPrefix(string(decoded), "\ufeff"), "gb18030", nil
	case "utf-8", "utf8":
		if !utf8.Valid(raw) {
			return "", "", fmt.Errorf("input is not valid UTF-8")
		}
		return strings.TrimPrefix(string(raw), "\ufeff"), "utf-8", nil
	case "gbk", "gb18030", "gb2312":
		decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
		if err != nil {
			return "", "", fmt.Errorf("decode as gb18030: %w", err)
		}
		return strings.TrimPrefix(string(decoded), "\ufeff"), "gb18030", nil
	default:
		return "", "", fmt.Errorf("unsupported encoding_hint %q; use auto, utf-8, gbk, gb18030", hint)
	}
}

func saveTextArtifact(ctx tool.Context, name, content, mimeType string) error {
	_, err := ctx.Artifacts().Save(ctx, name, &genai.Part{InlineData: &genai.Blob{MIMEType: mimeType, Data: []byte(content)}})
	return err
}

func normalizedPatterns(patterns []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, p := range patterns {
		p = strings.TrimSpace(p)
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, p)
	}
	if len(out) == 0 {
		out = append(out, defaultChapterPatterns...)
	}
	return out
}

func minChapterChars(v int) int {
	if v <= 0 {
		return 200
	}
	return v
}

func boolDefault(p *bool, fallback bool) bool {
	if p == nil {
		return fallback
	}
	return *p
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = compactOneLine(line, 120)
		if line != "" {
			return line
		}
	}
	return ""
}

func compactOneLine(s string, maxRunes int) string {
	s = strings.TrimSpace(strings.Join(strings.Fields(s), " "))
	if maxRunes > 0 && utf8.RuneCountInString(s) > maxRunes {
		r := []rune(s)
		return string(r[:maxRunes])
	}
	return s
}

func byteToRuneIndex(s string, byteIndex int) int {
	if byteIndex <= 0 {
		return 0
	}
	if byteIndex > len(s) {
		byteIndex = len(s)
	}
	return utf8.RuneCountInString(s[:byteIndex])
}

func limitRunes(s string, max int) (string, bool) {
	if max <= 0 {
		return s, false
	}
	r := []rune(s)
	if len(r) <= max {
		return s, false
	}
	return string(r[:max]), true
}

func headRunes(s string, max int) string {
	out, _ := limitRunes(s, max)
	return out
}

func tailRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[len(r)-max:])
}

func sanitizeBookID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if utf8.RuneCountInString(out) > 48 {
		r := []rune(out)
		out = string(r[:48])
	}
	return out
}

func generatedBookID(title string, raw []byte) string {
	base := sanitizeBookID(title)
	if base == "" {
		base = "book"
	}
	if utf8.RuneCountInString(base) > 24 {
		r := []rune(base)
		base = string(r[:24])
	}
	return fmt.Sprintf("book_%s_%08x_%s", time.Now().UTC().Format("20060102150405"), crc32.ChecksumIEEE(raw), base)
}

func sourceArtifactName(bookID string) string {
	return userScopedBookArtifactName(bookID + sourceArtifactSuffix)
}
func manifestArtifactName(bookID string) string {
	return userScopedBookArtifactName(bookID + manifestArtifactSuffix)
}
func legacyManifestArtifactName(bookID string) string { return bookID + manifestArtifactSuffix }
func chapterArtifactName(bookID string, no int) string {
	return userScopedBookArtifactName(fmt.Sprintf(chapterArtifactFormat, bookID, no))
}

func userScopedBookArtifactName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, userArtifactPrefix) {
		return name
	}
	return userArtifactPrefix + name
}

func isUserScopedArtifactName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), userArtifactPrefix)
}

func resolveBookID(ctx tool.Context, explicit string) (string, *MountedBook, error) {
	bookID := sanitizeBookID(explicit)
	mounted, _ := loadMountedBook(ctx)
	if bookID != "" {
		return bookID, mounted, nil
	}
	if mounted != nil && mounted.BookID != "" {
		return sanitizeBookID(mounted.BookID), mounted, nil
	}
	return "", nil, fmt.Errorf("book_id is required; call book_list_books then book_mount first, or pass book_id explicitly")
}

func loadMountedBook(ctx tool.Context) (*MountedBook, error) {
	text, _, _, err := loadArtifactText(ctx, mountedBookArtifact, "utf-8")
	if err != nil {
		return nil, err
	}
	var mounted MountedBook
	if err := json.Unmarshal([]byte(text), &mounted); err != nil {
		return nil, err
	}
	if sanitizeBookID(mounted.BookID) == "" {
		return nil, fmt.Errorf("mounted_book.json has empty book_id")
	}
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return nil, err
	}
	if !sameProjectID(mounted.ProjectID, projectID) {
		return nil, fmt.Errorf("mounted book belongs to a different workspace")
	}
	return &mounted, nil
}

func bookInfoLess(a, b BookInfo) bool {
	if a.Scope != b.Scope {
		return a.Scope == "session" && b.Scope == "user"
	}
	return a.UpdatedAt < b.UpdatedAt
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

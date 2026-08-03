package bookpreprocessortool

import (
	"context"
	"strings"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	artifactinternal "google.golang.org/adk/internal/artifact"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool/projectartifacttool"
	"google.golang.org/adk/tool/toolconfirmation"
)

type testToolContext struct {
	context.Context
	artifacts agent.Artifacts
	actions   session.EventActions
}

func (c *testToolContext) UserContent() *genai.Content { return nil }
func (c *testToolContext) InvocationID() string        { return "invocation" }
func (c *testToolContext) AgentName() string           { return "book_dissector" }
func (c *testToolContext) ReadonlyState() session.ReadonlyState {
	return nil
}
func (c *testToolContext) UserID() string    { return "user" }
func (c *testToolContext) AppName() string   { return "book_dissector" }
func (c *testToolContext) SessionID() string { return "session" }
func (c *testToolContext) Branch() string    { return "" }
func (c *testToolContext) Artifacts() agent.Artifacts {
	return c.artifacts
}
func (c *testToolContext) State() session.State { return nil }
func (c *testToolContext) FunctionCallID() string {
	return "call"
}
func (c *testToolContext) Actions() *session.EventActions {
	return &c.actions
}
func (c *testToolContext) SearchMemory(context.Context, string) (*memory.SearchResponse, error) {
	return &memory.SearchResponse{}, nil
}
func (c *testToolContext) ToolConfirmation() *toolconfirmation.ToolConfirmation {
	return nil
}
func (c *testToolContext) RequestConfirmation(string, any) error {
	return nil
}

func TestSplitPreviewDoesNotSaveDerivedArtifacts(t *testing.T) {
	ctx := newBookToolTestContext(t)
	ts := &Toolset{}

	result, err := ts.SplitPreview(ctx, SplitFromArtifactArgs{
		SourceArtifact:  "source.txt",
		Title:           "Demo Book",
		BookID:          "demo_book",
		MinChapterChars: 1,
	})
	if err != nil {
		t.Fatalf("SplitPreview() error = %v", err)
	}
	if result.Status != "preview" {
		t.Fatalf("Status = %q, want preview", result.Status)
	}
	if result.ChapterCount != 2 {
		t.Fatalf("ChapterCount = %d, want 2", result.ChapterCount)
	}
	if _, err := ctx.artifacts.Load(ctx, result.ManifestArtifact); err == nil {
		t.Fatalf("preview saved manifest artifact %q", result.ManifestArtifact)
	}
	if _, err := ctx.artifacts.Load(ctx, result.ChaptersPreview[0].Artifact); err == nil {
		t.Fatalf("preview saved chapter artifact %q", result.ChaptersPreview[0].Artifact)
	}
}

func TestSplitCommitSavesDerivedArtifacts(t *testing.T) {
	ctx := newBookToolTestContext(t)
	ts := &Toolset{}

	result, err := ts.SplitCommit(ctx, SplitFromArtifactArgs{
		SourceArtifact:  "source.txt",
		Title:           "Demo Book",
		BookID:          "demo_book",
		MinChapterChars: 1,
	})
	if err != nil {
		t.Fatalf("SplitCommit() error = %v", err)
	}
	if result.ChapterCount != 2 {
		t.Fatalf("ChapterCount = %d, want 2", result.ChapterCount)
	}
	if _, err := ctx.artifacts.Load(ctx, result.ManifestArtifact); err != nil {
		t.Fatalf("manifest artifact was not saved: %v", err)
	}
	if _, err := ctx.artifacts.Load(ctx, result.ChaptersPreview[0].Artifact); err != nil {
		t.Fatalf("chapter artifact was not saved: %v", err)
	}
}

func TestSplitCommitBlockedWhenNovelStoreActiveSplitExists(t *testing.T) {
	ctx := newBookToolTestContext(t)
	ts := &Toolset{}

	if _, _, err := projectartifacttool.RegisterArtifact(ctx, projectartifacttool.RegisterArtifactRequest{
		ProjectID:        "test_project",
		ArtifactName:     "novelstore:active_split:demo_book",
		Type:             "novel.active_split",
		Title:            "Demo Book active split",
		ProducerAgent:    "novel_store_manager",
		Visibility:       projectartifacttool.VisibilityProjectDefault,
		DefaultForAgents: []string{"book_dissector", "book_skill_runner"},
		BookID:           "demo_book",
		Metadata: map[string]string{
			"book_id":         "demo_book",
			"active_split_id": "split_1",
			"chapter_count":   "2",
			"status":          "active",
		},
	}); err != nil {
		t.Fatalf("register NovelStore active split: %v", err)
	}

	_, err := ts.SplitCommit(ctx, SplitFromArtifactArgs{
		SourceArtifact:  "source.txt",
		Title:           "Demo Book",
		BookID:          "demo_book",
		MinChapterChars: 1,
	})
	if err == nil {
		t.Fatalf("SplitCommit() error = nil, want NovelStore active split guard")
	}
	if !strings.Contains(err.Error(), "NovelStore active split") {
		t.Fatalf("SplitCommit() error = %v, want NovelStore active split guard", err)
	}
	if _, err := ctx.artifacts.Load(ctx, manifestArtifactName("demo_book")); err == nil {
		t.Fatalf("blocked split saved legacy manifest")
	}
}

func TestSplitPreviewCollapsesAdjacentDuplicateChapterTitle(t *testing.T) {
	source := "第八十三章 前局\n上一章内容足够长\n第八十四章定局\n    第八十四章 定局\n本章内容\n第八十五章 后局\n下一章内容"
	ctx := newBookToolTestContextWithSource(t, source)
	ts := &Toolset{}

	result, err := ts.SplitPreview(ctx, SplitFromArtifactArgs{
		SourceArtifact:  "source.txt",
		Title:           "Demo Book",
		BookID:          "demo_book",
		MinChapterChars: 1,
	})
	if err != nil {
		t.Fatalf("SplitPreview() error = %v", err)
	}
	if result.ChapterCount != 3 {
		t.Fatalf("ChapterCount = %d, want 3", result.ChapterCount)
	}
	if len(result.Warnings) == 0 {
		t.Fatalf("Warnings is empty, want duplicate-title warning")
	}
	if got := result.ChaptersPreview[1].Title; got != "第八十四章 定局" {
		t.Fatalf("chapter 2 title = %q, want later duplicate title", got)
	}
}

func TestSplitPreviewSupportsBracketStorySections(t *testing.T) {
	source := `百妖谱
作者：裟椤双树
内容简介
桃都鬼医桃夭只治妖怪不治人。

==========================================================
序
桃都山有大桃树，盘屈三千里。

【灰狐】楔子
我救的不是他。

【灰狐】第一节
太平兴国元年，成都，郊外。

【乖龙】壹
春雨落在瓦上。
`
	ctx := newBookToolTestContextWithSource(t, source)
	ts := &Toolset{}

	result, err := ts.SplitPreview(ctx, SplitFromArtifactArgs{
		SourceArtifact:  "source.txt",
		Title:           "百妖谱",
		BookID:          "baiyaopu",
		MinChapterChars: 1,
	})
	if err != nil {
		t.Fatalf("SplitPreview() error = %v", err)
	}
	if result.ChapterCount != 4 {
		t.Fatalf("ChapterCount = %d, want 4; preview=%+v", result.ChapterCount, result.ChaptersPreview)
	}
	wantTitles := []string{"序", "【灰狐】楔子", "【灰狐】第一节", "【乖龙】壹"}
	for i, want := range wantTitles {
		if got := result.ChaptersPreview[i].Title; got != want {
			t.Fatalf("chapter %d title = %q, want %q", i+1, got, want)
		}
	}
	if result.SplitAnalysis.SelectedName == "" {
		t.Fatalf("SplitAnalysis.SelectedName is empty")
	}
	if got := result.ChaptersPreview[0].Title; got == "内容简介" {
		t.Fatalf("frontmatter was incorrectly detected as a chapter title")
	}
}

func newBookToolTestContext(t *testing.T) *testToolContext {
	t.Helper()
	source := "第一章 开始\n内容一\n第二章 继续\n内容二"
	return newBookToolTestContextWithSource(t, source)
}

func newBookToolTestContextWithSource(t *testing.T, source string) *testToolContext {
	t.Helper()
	arts := &artifactinternal.Artifacts{
		Service:   artifact.InMemoryService(),
		AppName:   "book_dissector",
		UserID:    "user",
		SessionID: "session",
	}
	if _, err := arts.Save(t.Context(), "source.txt", genai.NewPartFromText(source)); err != nil {
		t.Fatalf("save source artifact: %v", err)
	}
	ctx := &testToolContext{Context: t.Context(), artifacts: arts}
	registry, _, err := projectartifacttool.EnsureProject(ctx, projectartifacttool.EnsureProjectRequest{
		ProjectID:   "test_project",
		Name:        "test_project",
		DisplayName: "Test Project",
	})
	if err != nil {
		t.Fatalf("ensure test project: %v", err)
	}
	if _, err := projectartifacttool.MountProject(ctx, registry); err != nil {
		t.Fatalf("mount test project: %v", err)
	}
	return ctx
}

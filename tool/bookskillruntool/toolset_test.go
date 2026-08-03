package bookskillruntool

import (
	"context"
	"iter"
	"strings"
	"testing"

	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/artifact"
	artifactinternal "google.golang.org/adk/internal/artifact"
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"
	bookpreprocessortool "google.golang.org/adk/tool/bookpreprocessortool"
	"google.golang.org/adk/tool/projectartifacttool"
	"google.golang.org/adk/tool/toolconfirmation"
)

type testToolContext struct {
	context.Context
	artifacts agent.Artifacts
	state     session.State
	appName   string
	sessionID string
}

func (c *testToolContext) UserContent() *genai.Content { return nil }
func (c *testToolContext) InvocationID() string        { return "invocation" }
func (c *testToolContext) AgentName() string           { return c.appName }
func (c *testToolContext) ReadonlyState() session.ReadonlyState {
	return c.state
}
func (c *testToolContext) UserID() string  { return "user" }
func (c *testToolContext) AppName() string { return c.appName }
func (c *testToolContext) SessionID() string {
	if c.sessionID != "" {
		return c.sessionID
	}
	return "session"
}
func (c *testToolContext) Branch() string { return "" }
func (c *testToolContext) Artifacts() agent.Artifacts {
	return c.artifacts
}
func (c *testToolContext) State() session.State { return c.state }
func (c *testToolContext) FunctionCallID() string {
	return "call"
}
func (c *testToolContext) Actions() *session.EventActions {
	return &session.EventActions{}
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

type mapState map[string]any

func (s mapState) Get(key string) (any, error) {
	v, ok := s[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return v, nil
}

func (s mapState) Set(key string, value any) error {
	s[key] = value
	return nil
}

func (s mapState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range s {
			if !yield(k, v) {
				return
			}
		}
	}
}

func TestStartResolvesBookFromProjectAcrossAgents(t *testing.T) {
	service := artifact.InMemoryService()
	dissectorCtx := newTestContext(service, "book_dissector", "split-session", mapState{"project_id": "demo_project"})
	if _, err := dissectorCtx.artifacts.Save(t.Context(), "source.txt", genai.NewPartFromText("第一章 开始\n内容一\n第二章 继续\n内容二")); err != nil {
		t.Fatalf("save source: %v", err)
	}
	preprocessor := &bookpreprocessortool.Toolset{}
	if _, err := preprocessor.SplitFromArtifact(dissectorCtx, bookpreprocessortool.SplitFromArtifactArgs{
		SourceArtifact:  "source.txt",
		Title:           "Demo Book",
		BookID:          "demo_book",
		MinChapterChars: 1,
	}); err != nil {
		t.Fatalf("SplitFromArtifact() error = %v", err)
	}

	runnerState := mapState{"project_id": "demo_project"}
	runnerCtx := newTestContext(service, "book_skill_runner", "runner-session", runnerState)
	runner := &Toolset{}
	result, err := runner.Start(runnerCtx, StartArgs{
		BatchSize: 1,
		Goal:      "extract dialogue writing skill",
		SkillID:   "novel-dialogue-power-dynamics",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.State.BookID != "demo_book" {
		t.Fatalf("BookID = %q, want demo_book", result.State.BookID)
	}
	if result.State.ProjectID != "demo_project" {
		t.Fatalf("ProjectID = %q, want demo_project", result.State.ProjectID)
	}
	if result.NextBatch == nil || len(result.NextBatch.InputChapterArtifacts) != 1 {
		t.Fatalf("NextBatch = %#v, want one input chapter artifact", result.NextBatch)
	}
}

func TestStartResolvesNovelStoreActiveSplitFromProject(t *testing.T) {
	service := artifact.InMemoryService()
	ctx := newTestContext(service, "book_skill_runner", "runner-session", mapState{"project_id": "demo_project"})
	if _, _, err := projectartifacttool.EnsureProject(ctx, projectartifacttool.EnsureProjectRequest{
		ProjectID:   "demo_project",
		Name:        "demo_project",
		DisplayName: "Demo Project",
	}); err != nil {
		t.Fatalf("EnsureProject() error = %v", err)
	}
	if _, _, err := projectartifacttool.RegisterArtifact(ctx, projectartifacttool.RegisterArtifactRequest{
		ProjectID:        "demo_project",
		ArtifactID:       "novel_active_split__novel_book",
		ArtifactName:     "novelstore:active_split:novel_book",
		Type:             "novel.active_split",
		Title:            "Novel Book active split",
		Visibility:       projectartifacttool.VisibilityProjectDefault,
		DefaultForAgents: []string{"book_skill_runner"},
		BookID:           "novel_book",
		StartChapter:     1,
		EndChapter:       12,
		Metadata: map[string]string{
			"book_id":       "novel_book",
			"split_id":      "split_1",
			"title":         "Novel Book",
			"chapter_count": "12",
		},
	}); err != nil {
		t.Fatalf("RegisterArtifact() error = %v", err)
	}

	runner := &Toolset{}
	result, err := runner.Start(ctx, StartArgs{
		BatchSize:       5,
		Goal:            "extract dialogue writing skill",
		SkillID:         "novel-dialogue-power-gap",
		SkillFocus:      "dialogue",
		TargetTechnique: "通过对白体现权力差",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if result.State.BookID != "novel_book" {
		t.Fatalf("BookID = %q, want novel_book", result.State.BookID)
	}
	if result.State.ManifestArtifact != "novelstore:active_split:novel_book" {
		t.Fatalf("ManifestArtifact = %q, want NovelStore pointer", result.State.ManifestArtifact)
	}
	if got, want := len(result.State.Batches), 3; got != want {
		t.Fatalf("batches = %d, want %d", got, want)
	}
	if result.NextBatch == nil || len(result.NextBatch.InputChapterArtifacts) != 5 {
		t.Fatalf("NextBatch = %#v, want five NovelStore chapter pointers", result.NextBatch)
	}
	if got := result.NextBatch.InputChapterArtifacts[0]; got != "novelstore:novel_book:split_1:chapter:0001" {
		t.Fatalf("first input = %q, want NovelStore chapter pointer", got)
	}
}

func TestSkillRunUsesArtifactVersionsForIterations(t *testing.T) {
	service := artifact.InMemoryService()
	ctx := newTestContext(service, "book_skill_runner", "runner-session", mapState{"project_id": "demo_project"})
	if _, err := ctx.artifacts.Save(t.Context(), "user:demo_book__manifest.json", genai.NewPartFromText(`{
  "book_id": "demo_book",
  "project_id": "demo_project",
  "title": "Demo Book",
  "chapter_count": 2,
  "chapters": [
    {"no": 1, "title": "第一章", "artifact": "user:demo_book__chapter_0001.txt"},
    {"no": 2, "title": "第二章", "artifact": "user:demo_book__chapter_0002.txt"}
  ]
}`)); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	runner := &Toolset{}
	start, err := runner.Start(ctx, StartArgs{
		BookID:    "demo_book",
		RunID:     "demo_run",
		BatchSize: 1,
		Goal:      "extract dialogue skill",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if len(start.State.Batches) != 2 {
		t.Fatalf("batches = %d, want 2", len(start.State.Batches))
	}
	if start.State.Batches[0].SkillVersionArtifact != start.State.Batches[1].SkillVersionArtifact {
		t.Fatalf("skill artifact should be stable across batches: %q vs %q", start.State.Batches[0].SkillVersionArtifact, start.State.Batches[1].SkillVersionArtifact)
	}

	first := start.State.Batches[0]
	saveBatchArtifacts(t, ctx, first, "v1")
	recorded, err := runner.RecordBatch(ctx, RecordBatchArgs{
		RunID:      start.State.RunID,
		BatchIndex: first.Index,
		Status:     batchCompleted,
	})
	if err != nil {
		t.Fatalf("RecordBatch(first) error = %v", err)
	}
	if recorded.State.CurrentArtifactVersion != 1 {
		t.Fatalf("CurrentArtifactVersion after first = %d, want 1", recorded.State.CurrentArtifactVersion)
	}

	second := recorded.State.Batches[1]
	saveBatchArtifacts(t, ctx, second, "v2")
	recorded, err = runner.RecordBatch(ctx, RecordBatchArgs{
		RunID:      start.State.RunID,
		BatchIndex: second.Index,
		Status:     batchCompleted,
	})
	if err != nil {
		t.Fatalf("RecordBatch(second) error = %v", err)
	}
	if recorded.State.CurrentArtifactVersion != 2 {
		t.Fatalf("CurrentArtifactVersion after second = %d, want 2", recorded.State.CurrentArtifactVersion)
	}
	versions, err := ctx.artifacts.Versions(ctx, first.SkillVersionArtifact)
	if err != nil {
		t.Fatalf("Versions(skill artifact) error = %v", err)
	}
	if len(versions.Versions) != 2 {
		t.Fatalf("skill artifact versions = %v, want two versions", versions.Versions)
	}

	registry, err := projectartifacttool.LoadProject(ctx, "demo_project")
	if err != nil {
		t.Fatalf("LoadProject() error = %v", err)
	}
	skillEntries := 0
	for _, art := range registry.Artifacts {
		if art.Type == "skill.version" {
			skillEntries++
			if art.ArtifactName != first.SkillVersionArtifact {
				t.Fatalf("registered skill artifact = %q, want %q", art.ArtifactName, first.SkillVersionArtifact)
			}
			if art.Metadata["artifact_version"] != "2" {
				t.Fatalf("registered artifact_version = %q, want 2", art.Metadata["artifact_version"])
			}
		}
	}
	if skillEntries != 1 {
		t.Fatalf("skill.version registry entries = %d, want 1", skillEntries)
	}
}

func TestPrepareBatchLoadsBoundedChapterContext(t *testing.T) {
	service := artifact.InMemoryService()
	ctx := newTestContext(service, "book_skill_runner", "runner-session", mapState{"project_id": "demo_project"})
	longChapter := strings.Repeat("甲说：“这是一句有权力差的对白。”\n", 200)
	if _, err := ctx.artifacts.Save(t.Context(), "user:demo_book__manifest.json", genai.NewPartFromText(`{
  "book_id": "demo_book",
  "project_id": "demo_project",
  "title": "Demo Book",
  "chapter_count": 2,
  "chapters": [
    {"no": 1, "title": "第一章", "artifact": "user:demo_book__chapter_0001.txt"},
    {"no": 2, "title": "第二章", "artifact": "user:demo_book__chapter_0002.txt"}
  ]
}`)); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	if _, err := ctx.artifacts.Save(t.Context(), "user:demo_book__chapter_0001.txt", genai.NewPartFromText(longChapter)); err != nil {
		t.Fatalf("save chapter1: %v", err)
	}
	if _, err := ctx.artifacts.Save(t.Context(), "user:demo_book__chapter_0002.txt", genai.NewPartFromText("乙说：“短章。”")); err != nil {
		t.Fatalf("save chapter2: %v", err)
	}

	runner := &Toolset{}
	start, err := runner.Start(ctx, StartArgs{BookID: "demo_book", RunID: "demo_run", BatchSize: 2, SkillID: "novel-dialogue-power-gap", SkillFocus: "dialogue"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	prepared, err := runner.PrepareBatch(ctx, PrepareBatchArgs{
		RunID:              start.State.RunID,
		Focus:              "dialogue",
		MaxBatchChars:      260,
		MaxCharsPerChapter: 160,
	})
	if err != nil {
		t.Fatalf("PrepareBatch() error = %v", err)
	}
	if prepared.Done {
		t.Fatalf("PrepareBatch() done = true, want false")
	}
	if prepared.Batch == nil || prepared.Batch.Index != 1 {
		t.Fatalf("prepared batch = %#v, want first batch", prepared.Batch)
	}
	if got := prepared.ContextCharCount; got > 260 {
		t.Fatalf("ContextCharCount = %d, want <= 260", got)
	}
	if len(prepared.Chapters) != 2 {
		t.Fatalf("chapters = %d, want 2", len(prepared.Chapters))
	}
	if !prepared.Chapters[0].Truncated {
		t.Fatalf("first chapter should be truncated")
	}
	if prepared.Chapters[0].Content == "" || len([]rune(prepared.Chapters[0].Content)) > 160 {
		t.Fatalf("first chapter content length = %d, want 1..160", len([]rune(prepared.Chapters[0].Content)))
	}
	if prepared.State.RunID != "demo_run" || prepared.State.ProjectID != "demo_project" {
		t.Fatalf("state summary = %#v, want run/project ids", prepared.State)
	}
}

func TestRecordOutputsSavesArtifactsAndAdvancesRun(t *testing.T) {
	service := artifact.InMemoryService()
	ctx := newTestContext(service, "book_skill_runner", "runner-session", mapState{"project_id": "demo_project"})
	if _, err := ctx.artifacts.Save(t.Context(), "user:demo_book__manifest.json", genai.NewPartFromText(`{
  "book_id": "demo_book",
  "project_id": "demo_project",
  "title": "Demo Book",
  "chapter_count": 1,
  "chapters": [
    {"no": 1, "title": "第一章", "artifact": "user:demo_book__chapter_0001.txt"}
  ]
}`)); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	runner := &Toolset{}
	start, err := runner.Start(ctx, StartArgs{BookID: "demo_book", RunID: "demo_run", BatchSize: 1, SkillID: "novel-dialogue-power-gap"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	recorded, err := runner.RecordOutputs(ctx, RecordOutputsArgs{
		RunID:        start.State.RunID,
		BatchIndex:   1,
		Analysis:     "analysis body",
		SkillDelta:   `{"updates":["delta"]}`,
		MergedSkill:  "# Skill\n\nusable body",
		QualityNotes: `{"quality":"ok"}`,
	})
	if err != nil {
		t.Fatalf("RecordOutputs() error = %v", err)
	}
	if recorded.State.Status != statusCompleted {
		t.Fatalf("status = %q, want completed", recorded.State.Status)
	}
	if recorded.State.CurrentArtifactVersion != 1 {
		t.Fatalf("CurrentArtifactVersion = %d, want 1", recorded.State.CurrentArtifactVersion)
	}
	skillText, err := loadArtifactText(ctx, recorded.State.CurrentSkillArtifact)
	if err != nil {
		t.Fatalf("load current skill: %v", err)
	}
	if !strings.Contains(skillText, "usable body") {
		t.Fatalf("skill artifact content = %q, want merged skill", skillText)
	}
	registry, err := projectartifacttool.LoadProject(ctx, "demo_project")
	if err != nil {
		t.Fatalf("LoadProject() error = %v", err)
	}
	foundSkill := false
	for _, art := range registry.Artifacts {
		if art.Type == "skill.version" && art.ArtifactName == recorded.State.CurrentSkillArtifact {
			foundSkill = true
			if art.Metadata["artifact_version"] != "1" {
				t.Fatalf("artifact_version = %q, want 1", art.Metadata["artifact_version"])
			}
		}
		if strings.Contains(art.ArtifactName, "project__demo_book") {
			t.Fatalf("registered artifact under book-id project-like name: %#v", art)
		}
	}
	if !foundSkill {
		t.Fatalf("skill.version artifact was not registered in project")
	}
}

func TestRecordOutputsRejectsRunFromDifferentProject(t *testing.T) {
	service := artifact.InMemoryService()
	projectACtx := newTestContext(service, "book_skill_runner", "runner-session-a", mapState{"project_id": "project_a"})
	if _, err := projectACtx.artifacts.Save(t.Context(), "user:demo_book__manifest.json", genai.NewPartFromText(`{
  "book_id": "demo_book",
  "project_id": "project_a",
  "title": "Demo Book",
  "chapter_count": 1,
  "chapters": [
    {"no": 1, "title": "第一章", "artifact": "user:demo_book__chapter_0001.txt"}
  ]
}`)); err != nil {
		t.Fatalf("save manifest: %v", err)
	}
	runner := &Toolset{}
	if _, err := runner.Start(projectACtx, StartArgs{BookID: "demo_book", RunID: "demo_run", BatchSize: 1}); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	projectBCtx := newTestContext(service, "book_skill_runner", "runner-session-b", mapState{"project_id": "project_b"})
	_, err := runner.RecordOutputs(projectBCtx, RecordOutputsArgs{
		RunID:       "demo_run",
		BatchIndex:  1,
		Analysis:    "analysis",
		SkillDelta:  "{}",
		MergedSkill: "# Skill",
	})
	if err == nil {
		t.Fatal("RecordOutputs() error = nil, want project isolation error")
	}
	if !strings.Contains(err.Error(), "not current workspace") {
		t.Fatalf("RecordOutputs() error = %v, want current workspace isolation error", err)
	}
}

func saveBatchArtifacts(t *testing.T, ctx *testToolContext, b BatchPlan, suffix string) {
	t.Helper()
	for name, content := range map[string]string{
		b.AnalysisArtifact:     "analysis " + suffix,
		b.SkillDeltaArtifact:   `{"delta":"` + suffix + `"}`,
		b.SkillVersionArtifact: "skill " + suffix,
		b.EvaluationArtifact:   `{"evaluation":"` + suffix + `"}`,
	} {
		if _, err := ctx.artifacts.Save(t.Context(), name, genai.NewPartFromText(content)); err != nil {
			t.Fatalf("save %s: %v", name, err)
		}
	}
}

func newTestContext(service artifact.Service, appName, sessionID string, state session.State) *testToolContext {
	return &testToolContext{
		Context: context.Background(),
		artifacts: &artifactinternal.Artifacts{
			Service:   service,
			AppName:   appName,
			UserID:    "user",
			SessionID: sessionID,
		},
		state:     state,
		appName:   appName,
		sessionID: sessionID,
	}
}

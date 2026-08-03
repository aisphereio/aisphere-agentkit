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

// Package bookskillruntool provides deterministic long-run state tools for
// iterative Book-to-Skill workflows. The LLM still performs analysis and skill
// writing, but durable progress, batch ranges, and artifact names are generated
// by code so the workflow can be resumed from a new session.
package bookskillruntool

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/internal/platform/novelstore"
	"google.golang.org/adk/internal/platform/objectstore"
	"google.golang.org/adk/internal/platform/store"
	"google.golang.org/adk/internal/runtimeconfig"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/adk/tool/projectartifacttool"
)

const (
	stateSchemaVersion = "book-skill-run/v1"
	userArtifactPrefix = "user:"

	mountedBookArtifact      = "mounted_book.json"
	manifestArtifactSuffix   = "__manifest.json"
	legacyManifestSuffix     = "__manifest.json"
	runStateArtifactPrefix   = "book_skill_run__"
	runStateArtifactSuffix   = "__state.json"
	runLatestArtifactPattern = "%s__book_skill_run_latest.json"

	statusCreated   = "created"
	statusRunning   = "running"
	statusPaused    = "paused"
	statusCompleted = "completed"
	statusFailed    = "failed"

	batchPending   = "pending"
	batchRunning   = "running"
	batchCompleted = "completed"
	batchFailed    = "failed"
	batchSkipped   = "skipped"

	defaultBatchContextChars      = 50000
	defaultChapterContextChars    = 8000
	defaultPreviousSkillMaxChars  = 12000
	maxPreparedBatchContextChars  = 100000
	maxPreparedChapterContextRune = 20000
)

// NewToolset creates the Book-to-Skill long-run toolset.
func NewToolset() (tool.Toolset, error) {
	ts := &Toolset{}
	builders := []func() (tool.Tool, error){
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_skill_run_start",
				Description: "Create a durable Book-to-Skill iterative run from a split book manifest. It plans chapter batches and saves a user-scoped run state artifact so future sessions can resume without re-splitting the book.",
			}, ts.Start)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_skill_run_get",
				Description: "Load a Book-to-Skill run state by run_id, or the latest run for a book. Use this after opening a new session to resume an existing long task.",
			}, ts.Get)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_skill_run_next_batch",
				Description: "Return the next pending chapter batch and deterministic artifact names for analysis, delta, evaluation, and the next skill version. Optionally marks the batch as running.",
			}, ts.NextBatch)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_skill_run_prepare_batch",
				Description: "Prepare one bounded Book-to-Skill batch for the model: resolve the current project run, read only the next batch's limited chapter context, include the previous skill version if present, and mark the batch running.",
			}, ts.PrepareBatch)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_skill_run_record_batch",
				Description: "Record completion/failure of a Book-to-Skill batch and advance the durable run state to the next chapter range.",
			}, ts.RecordBatch)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_skill_run_record_outputs",
				Description: "Save analysis, skill delta, merged SKILL.md, and evaluation text to canonical versioned artifacts, then mark the prepared batch completed in the durable run state.",
			}, ts.RecordOutputs)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_skill_run_pause",
				Description: "Pause, resume, fail, or complete a Book-to-Skill run state without changing batch artifacts.",
			}, ts.UpdateStatus)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "book_skill_run_list",
				Description: "List user-scoped Book-to-Skill runs visible in the current artifact workspace.",
			}, ts.List)
		},
	}
	for _, build := range builders {
		t, err := build()
		if err != nil {
			return nil, err
		}
		ts.tools = append(ts.tools, t)
	}
	return ts, nil
}

// Toolset groups Book-to-Skill long-run state tools.
type Toolset struct {
	tools []tool.Tool
}

func (t *Toolset) Name() string { return "BookSkillRunToolset" }

func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) { return t.tools, nil }

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

// StartArgs configures a new long-run.
type StartArgs struct {
	BookID               string   `json:"book_id,omitempty" jsonschema:"Split book id. If empty, the currently mounted book is used."`
	RunID                string   `json:"run_id,omitempty" jsonschema:"Optional stable run id. Defaults to a generated id."`
	StartChapter         int      `json:"start_chapter,omitempty" jsonschema:"First chapter to process. Defaults to 1."`
	EndChapter           int      `json:"end_chapter,omitempty" jsonschema:"Last chapter to process. Defaults to the book's last chapter."`
	BatchSize            int      `json:"batch_size,omitempty" jsonschema:"How many chapters per iteration. Defaults to 5."`
	SkillID              string   `json:"skill_id,omitempty" jsonschema:"Stable target skill id/name being evolved."`
	SkillFocus           string   `json:"skill_focus,omitempty" jsonschema:"Skill focus area, for example dialogue, scene, plot, character, or style."`
	TargetTechnique      string   `json:"target_technique,omitempty" jsonschema:"Single technique to train in this run, for example 用对白体现权力差."`
	AbstractionLevel     string   `json:"abstraction_level,omitempty" jsonschema:"How abstract the target skill should be. Recommended values: atomic_technique, module, framework. Defaults to atomic_technique."`
	TransferScope        string   `json:"transfer_scope,omitempty" jsonschema:"Where the skill should transfer, for example 都市/玄幻/职场/家族/门派等权力不对等场景."`
	NonGoals             []string `json:"non_goals,omitempty" jsonschema:"What this run must not extract, such as source setting, plot summary, role relationships, or book-specific style."`
	ExcludedTerms        []string `json:"excluded_terms,omitempty" jsonschema:"Book names, character names, places, proper nouns, chapter numbers, and project identifiers forbidden in runtime SKILL.md body."`
	RuntimeBodyPolicy    string   `json:"runtime_body_policy,omitempty" jsonschema:"Policy for runtime skill body. Use source_free for reusable skills."`
	EvidencePolicy       string   `json:"evidence_policy,omitempty" jsonschema:"Policy for evidence. Use metadata_only so evidence stays in analysis/evaluation/metadata, not runtime SKILL.md body."`
	InitialSkillArtifact string   `json:"initial_skill_artifact,omitempty" jsonschema:"Optional artifact containing the starting SKILL.md or seed skill."`
	Goal                 string   `json:"goal,omitempty" jsonschema:"Human-readable objective of this run."`
	Overwrite            bool     `json:"overwrite,omitempty" jsonschema:"Whether to overwrite an existing state artifact with the same run_id."`
}

type RunLookupArgs struct {
	RunID  string `json:"run_id,omitempty" jsonschema:"Run id. If empty, book_id latest pointer is used."`
	BookID string `json:"book_id,omitempty" jsonschema:"Book id used to resolve the latest run when run_id is empty. If empty, the mounted book is used."`
}

type NextBatchArgs struct {
	RunID       string `json:"run_id,omitempty" jsonschema:"Run id. If empty, book_id latest pointer is used."`
	BookID      string `json:"book_id,omitempty" jsonschema:"Book id used to resolve latest run when run_id is empty."`
	MarkRunning *bool  `json:"mark_running,omitempty" jsonschema:"Whether to mark the returned batch as running. Defaults to true."`
	SessionID   string `json:"session_id,omitempty" jsonschema:"Optional external session id to record on the running batch."`
}

type PrepareBatchArgs struct {
	RunID              string `json:"run_id,omitempty" jsonschema:"Run id. If empty, book_id latest pointer is used."`
	BookID             string `json:"book_id,omitempty" jsonschema:"Book id used to resolve latest run when run_id is empty."`
	Focus              string `json:"focus,omitempty" jsonschema:"Optional focus hint such as dialogue. Dialogue runs receive dialogue-oriented bounded excerpts."`
	BatchSize          int    `json:"batch_size,omitempty" jsonschema:"Reserved for future auto-start flows. Existing runs keep their configured batch size."`
	MaxBatchChars      int    `json:"max_batch_chars,omitempty" jsonschema:"Maximum chapter context characters returned in this batch. Defaults to 50000 and is capped at 100000."`
	MaxCharsPerChapter int    `json:"max_chars_per_chapter,omitempty" jsonschema:"Maximum characters returned for one chapter. Defaults to 8000 and is capped at 20000."`
	MarkRunning        *bool  `json:"mark_running,omitempty" jsonschema:"Whether to mark the returned batch as running. Defaults to true."`
	SessionID          string `json:"session_id,omitempty" jsonschema:"Optional external session id to record on the running batch."`
}

type RecordBatchArgs struct {
	RunID                string   `json:"run_id" jsonschema:"Run id."`
	BatchIndex           int      `json:"batch_index,omitempty" jsonschema:"Batch index returned by book_skill_run_next_batch."`
	StartChapter         int      `json:"start_chapter,omitempty" jsonschema:"Batch start chapter, used if batch_index is omitted."`
	EndChapter           int      `json:"end_chapter,omitempty" jsonschema:"Batch end chapter, used if batch_index is omitted."`
	Status               string   `json:"status" jsonschema:"completed, failed, skipped, or running."`
	AnalysisArtifact     string   `json:"analysis_artifact,omitempty" jsonschema:"Artifact containing batch/chapter analysis."`
	SkillDeltaArtifact   string   `json:"skill_delta_artifact,omitempty" jsonschema:"Artifact containing skill delta for this batch."`
	SkillVersionArtifact string   `json:"skill_version_artifact,omitempty" jsonschema:"Artifact containing the merged next SKILL.md version."`
	EvaluationArtifact   string   `json:"evaluation_artifact,omitempty" jsonschema:"Artifact containing evaluator/check result."`
	SourceArtifacts      []string `json:"source_artifacts,omitempty" jsonschema:"Extra source artifacts used in this batch."`
	Notes                string   `json:"notes,omitempty" jsonschema:"Short human-readable notes."`
	ErrorMessage         string   `json:"error_message,omitempty" jsonschema:"Failure reason when status=failed."`
}

type RecordOutputsArgs struct {
	RunID        string `json:"run_id" jsonschema:"Run id."`
	BatchIndex   int    `json:"batch_index,omitempty" jsonschema:"Batch index returned by book_skill_run_prepare_batch."`
	Analysis     string `json:"analysis" jsonschema:"Batch analysis markdown or JSON. Store source evidence here, not in the runtime skill body."`
	SkillDelta   string `json:"skill_delta" jsonschema:"Incremental skill delta markdown or JSON."`
	MergedSkill  string `json:"merged_skill" jsonschema:"Complete merged runtime SKILL.md body for the next version."`
	QualityNotes string `json:"quality_notes,omitempty" jsonschema:"Evaluation or quality notes markdown/JSON. If empty, a minimal evaluation artifact is saved."`
	Notes        string `json:"notes,omitempty" jsonschema:"Short status note to store on the batch."`
}

type UpdateStatusArgs struct {
	RunID        string `json:"run_id" jsonschema:"Run id."`
	Status       string `json:"status" jsonschema:"paused, running, failed, completed."`
	Reason       string `json:"reason,omitempty" jsonschema:"Reason to record on the run state."`
	ErrorMessage string `json:"error_message,omitempty" jsonschema:"Failure reason when status=failed."`
}

type ListArgs struct {
	BookID string `json:"book_id,omitempty" jsonschema:"Optional book id filter."`
	Status string `json:"status,omitempty" jsonschema:"Optional run status filter."`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of runs to return. Defaults to 50."`
}

type StartResult struct {
	State          RunState   `json:"state"`
	StateArtifact  string     `json:"state_artifact"`
	LatestArtifact string     `json:"latest_artifact,omitempty"`
	NextBatch      *BatchPlan `json:"next_batch,omitempty"`
	Instructions   []string   `json:"instructions"`
}

type RunStateResult struct {
	State         RunState   `json:"state"`
	StateArtifact string     `json:"state_artifact"`
	NextBatch     *BatchPlan `json:"next_batch,omitempty"`
}

type BatchResult struct {
	State         RunState   `json:"state"`
	StateArtifact string     `json:"state_artifact"`
	Batch         *BatchPlan `json:"batch,omitempty"`
	Done          bool       `json:"done"`
	Instructions  []string   `json:"instructions,omitempty"`
}

type PreparedBatchResult struct {
	Done                  bool              `json:"done"`
	State                 RunStateSummary   `json:"state"`
	StateArtifact         string            `json:"state_artifact"`
	Batch                 *BatchPlan        `json:"batch,omitempty"`
	Chapters              []PreparedChapter `json:"chapters,omitempty"`
	CurrentSkillArtifact  string            `json:"current_skill_artifact,omitempty"`
	CurrentSkillVersion   int               `json:"current_skill_version"`
	CurrentSkillBody      string            `json:"current_skill_body,omitempty"`
	CurrentSkillTruncated bool              `json:"current_skill_truncated,omitempty"`
	ContextCharCount      int               `json:"context_char_count"`
	Instructions          []string          `json:"instructions,omitempty"`
}

type RunStateSummary struct {
	RunID                  string `json:"run_id"`
	ProjectID              string `json:"project_id,omitempty"`
	BookID                 string `json:"book_id"`
	Title                  string `json:"title,omitempty"`
	Status                 string `json:"status"`
	Progress               string `json:"progress"`
	CurrentSkillVersion    int    `json:"current_skill_version"`
	CurrentSkillArtifact   string `json:"current_skill_artifact,omitempty"`
	CurrentArtifactVersion int    `json:"current_artifact_version,omitempty"`
}

type PreparedChapter struct {
	ChapterNo      int    `json:"chapter_no"`
	Title          string `json:"title,omitempty"`
	SourceArtifact string `json:"source_artifact,omitempty"`
	CharCount      int    `json:"char_count,omitempty"`
	Content        string `json:"content"`
	Truncated      bool   `json:"truncated"`
	SafetyMessage  string `json:"safety_message,omitempty"`
}

type ListResult struct {
	Count int          `json:"count"`
	Runs  []RunSummary `json:"runs"`
}

type RunSummary struct {
	RunID                  string `json:"run_id"`
	BookID                 string `json:"book_id"`
	Title                  string `json:"title,omitempty"`
	Status                 string `json:"status"`
	Progress               string `json:"progress"`
	CurrentSkillVersion    int    `json:"current_skill_version"`
	CurrentSkillArtifact   string `json:"current_skill_artifact,omitempty"`
	CurrentArtifactVersion int    `json:"current_artifact_version,omitempty"`
	StateArtifact          string `json:"state_artifact"`
	UpdatedAt              string `json:"updated_at"`
}

// RunState is the durable state saved as a user-scoped artifact. It deliberately
// stores file names, not large content.
type RunState struct {
	SchemaVersion          string      `json:"schema_version"`
	RunID                  string      `json:"run_id"`
	ProjectID              string      `json:"project_id,omitempty"`
	BookID                 string      `json:"book_id"`
	Title                  string      `json:"title,omitempty"`
	ManifestArtifact       string      `json:"manifest_artifact"`
	ChapterCount           int         `json:"chapter_count"`
	StartChapter           int         `json:"start_chapter"`
	EndChapter             int         `json:"end_chapter"`
	BatchSize              int         `json:"batch_size"`
	Status                 string      `json:"status"`
	Goal                   string      `json:"goal,omitempty"`
	SkillID                string      `json:"skill_id,omitempty"`
	SkillFocus             string      `json:"skill_focus,omitempty"`
	TargetTechnique        string      `json:"target_technique,omitempty"`
	AbstractionLevel       string      `json:"abstraction_level,omitempty"`
	TransferScope          string      `json:"transfer_scope,omitempty"`
	NonGoals               []string    `json:"non_goals,omitempty"`
	ExcludedTerms          []string    `json:"excluded_terms,omitempty"`
	RuntimeBodyPolicy      string      `json:"runtime_body_policy,omitempty"`
	EvidencePolicy         string      `json:"evidence_policy,omitempty"`
	InitialSkillArtifact   string      `json:"initial_skill_artifact,omitempty"`
	CurrentSkillVersion    int         `json:"current_skill_version"`
	CurrentSkillArtifact   string      `json:"current_skill_artifact,omitempty"`
	CurrentArtifactVersion int         `json:"current_artifact_version,omitempty"`
	Batches                []BatchPlan `json:"batches"`
	CreatedAt              string      `json:"created_at"`
	UpdatedAt              string      `json:"updated_at"`
	ErrorMessage           string      `json:"error_message,omitempty"`
	LastNote               string      `json:"last_note,omitempty"`
}

type BatchPlan struct {
	Index                       int      `json:"index"`
	StartChapter                int      `json:"start_chapter"`
	EndChapter                  int      `json:"end_chapter"`
	Status                      string   `json:"status"`
	InputChapterArtifacts       []string `json:"input_chapter_artifacts,omitempty"`
	AnalysisArtifact            string   `json:"analysis_artifact"`
	AnalysisArtifactVersion     int      `json:"analysis_artifact_version,omitempty"`
	SkillDeltaArtifact          string   `json:"skill_delta_artifact"`
	SkillDeltaArtifactVersion   int      `json:"skill_delta_artifact_version,omitempty"`
	SkillVersionArtifact        string   `json:"skill_version_artifact"`
	SkillVersionArtifactVersion int      `json:"skill_version_artifact_version,omitempty"`
	EvaluationArtifact          string   `json:"evaluation_artifact"`
	EvaluationArtifactVersion   int      `json:"evaluation_artifact_version,omitempty"`
	SourceArtifacts             []string `json:"source_artifacts,omitempty"`
	SessionID                   string   `json:"session_id,omitempty"`
	StartedAt                   string   `json:"started_at,omitempty"`
	CompletedAt                 string   `json:"completed_at,omitempty"`
	Notes                       string   `json:"notes,omitempty"`
	ErrorMessage                string   `json:"error_message,omitempty"`
}

type latestPointer struct {
	RunID         string `json:"run_id"`
	BookID        string `json:"book_id"`
	StateArtifact string `json:"state_artifact"`
	UpdatedAt     string `json:"updated_at"`
}

type bookManifest struct {
	BookID           string           `json:"book_id"`
	ProjectID        string           `json:"project_id,omitempty"`
	Title            string           `json:"title"`
	ManifestArtifact string           `json:"manifest_artifact"`
	ChapterCount     int              `json:"chapter_count"`
	Chapters         []chapterSummary `json:"chapters"`
}

type chapterSummary struct {
	No       int    `json:"no"`
	Title    string `json:"title"`
	Artifact string `json:"artifact"`
}

type mountedBook struct {
	BookID           string `json:"book_id"`
	ProjectID        string `json:"project_id,omitempty"`
	Title            string `json:"title"`
	ManifestArtifact string `json:"manifest_artifact"`
	ChapterCount     int    `json:"chapter_count"`
}

func (t *Toolset) Start(ctx tool.Context, args StartArgs) (StartResult, error) {
	bookID, err := resolveBookID(ctx, args.BookID)
	if err != nil {
		return StartResult{}, err
	}
	manifest, err := loadManifest(ctx, bookID)
	if err != nil {
		return StartResult{}, err
	}
	chapterCount := manifest.ChapterCount
	if chapterCount == 0 {
		chapterCount = len(manifest.Chapters)
	}
	if chapterCount <= 0 {
		return StartResult{}, fmt.Errorf("manifest for book_id %q has no chapters", bookID)
	}
	start := args.StartChapter
	if start <= 0 {
		start = 1
	}
	end := args.EndChapter
	if end <= 0 || end > chapterCount {
		end = chapterCount
	}
	if start > end || start < 1 {
		return StartResult{}, fmt.Errorf("invalid chapter range: %d..%d, chapter_count=%d", start, end, chapterCount)
	}
	batchSize := args.BatchSize
	if batchSize <= 0 {
		batchSize = 5
	}
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return StartResult{}, err
	}
	if !sameProjectID(manifest.ProjectID, projectID) {
		return StartResult{}, fmt.Errorf("book %s does not belong to the current workspace", bookID)
	}
	if _, err := ensureRunProject(ctx, projectID, manifest); err != nil {
		return StartResult{}, err
	}
	if strings.TrimSpace(args.RunID) == "" && !args.Overwrite {
		if pointer, err := loadLatestPointer(ctx, manifest.BookID); err == nil {
			if existing, err := loadStateByArtifact(ctx, pointer.StateArtifact); err == nil &&
				sameProjectID(existing.ProjectID, projectID) &&
				sanitizeID(existing.BookID) == sanitizeID(manifest.BookID) &&
				existing.Status != statusFailed {
				return StartResult{
					State:          existing,
					StateArtifact:  pointer.StateArtifact,
					LatestArtifact: latestPointerArtifactName(existing.BookID),
					NextBatch:      nextPendingBatch(existing),
					Instructions:   startInstructions(existing),
				}, nil
			}
		}
	}
	runID := sanitizeID(args.RunID)
	if runID == "" {
		runID = fmt.Sprintf("%s_%s", sanitizeID(bookID), time.Now().UTC().Format("20060102150405"))
	}
	stateArtifact := stateArtifactName(runID)
	if !args.Overwrite {
		if _, err := loadStateByArtifact(ctx, stateArtifact); err == nil {
			return StartResult{}, fmt.Errorf("run state %q already exists; pass overwrite=true only after deciding to replace it", stateArtifact)
		}
	}
	now := nowRFC3339()
	state := RunState{
		SchemaVersion:        stateSchemaVersion,
		RunID:                runID,
		ProjectID:            projectID,
		BookID:               manifest.BookID,
		Title:                manifest.Title,
		ManifestArtifact:     firstNonEmpty(manifest.ManifestArtifact, manifestArtifactName(manifest.BookID)),
		ChapterCount:         chapterCount,
		StartChapter:         start,
		EndChapter:           end,
		BatchSize:            batchSize,
		Status:               statusCreated,
		Goal:                 strings.TrimSpace(args.Goal),
		SkillID:              strings.TrimSpace(args.SkillID),
		SkillFocus:           strings.TrimSpace(args.SkillFocus),
		TargetTechnique:      strings.TrimSpace(args.TargetTechnique),
		AbstractionLevel:     firstNonEmpty(strings.TrimSpace(args.AbstractionLevel), "atomic_technique"),
		TransferScope:        strings.TrimSpace(args.TransferScope),
		NonGoals:             normalizeRunList(args.NonGoals),
		ExcludedTerms:        normalizeRunList(args.ExcludedTerms),
		RuntimeBodyPolicy:    firstNonEmpty(strings.TrimSpace(args.RuntimeBodyPolicy), "source_free"),
		EvidencePolicy:       firstNonEmpty(strings.TrimSpace(args.EvidencePolicy), "metadata_only"),
		InitialSkillArtifact: strings.TrimSpace(args.InitialSkillArtifact),
		CurrentSkillVersion:  0,
		CurrentSkillArtifact: strings.TrimSpace(args.InitialSkillArtifact),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	state.Batches = buildBatches(state, manifest.Chapters)
	if err := saveState(ctx, &state); err != nil {
		return StartResult{}, err
	}
	latestArtifact := latestPointerArtifactName(state.BookID)
	if err := saveLatestPointer(ctx, state, stateArtifact); err != nil {
		return StartResult{}, err
	}
	if err := registerRunStateArtifacts(ctx, state, stateArtifact, latestArtifact); err != nil {
		return StartResult{}, err
	}
	return StartResult{
		State:          state,
		StateArtifact:  stateArtifact,
		LatestArtifact: latestArtifact,
		NextBatch:      nextPendingBatch(state),
		Instructions:   startInstructions(state),
	}, nil
}

func (t *Toolset) Get(ctx tool.Context, args RunLookupArgs) (RunStateResult, error) {
	state, artifact, err := resolveRunState(ctx, args.RunID, args.BookID)
	if err != nil {
		return RunStateResult{}, err
	}
	return RunStateResult{State: state, StateArtifact: artifact, NextBatch: nextPendingBatch(state)}, nil
}

func (t *Toolset) NextBatch(ctx tool.Context, args NextBatchArgs) (BatchResult, error) {
	state, artifact, err := resolveRunState(ctx, args.RunID, args.BookID)
	if err != nil {
		return BatchResult{}, err
	}
	idx := -1
	for i := range state.Batches {
		if state.Batches[i].Status == "" || state.Batches[i].Status == batchPending || state.Batches[i].Status == batchRunning {
			idx = i
			break
		}
	}
	if idx < 0 {
		state.Status = statusCompleted
		state.UpdatedAt = nowRFC3339()
		if err := saveState(ctx, &state); err != nil {
			return BatchResult{}, err
		}
		if err := registerRunStateArtifacts(ctx, state, artifact, latestPointerArtifactName(state.BookID)); err != nil {
			return BatchResult{}, err
		}
		return BatchResult{State: state, StateArtifact: artifact, Done: true}, nil
	}
	markRunning := true
	if args.MarkRunning != nil {
		markRunning = *args.MarkRunning
	}
	if markRunning {
		now := nowRFC3339()
		state.Status = statusRunning
		state.UpdatedAt = now
		state.Batches[idx].Status = batchRunning
		if state.Batches[idx].StartedAt == "" {
			state.Batches[idx].StartedAt = now
		}
		if args.SessionID != "" {
			state.Batches[idx].SessionID = args.SessionID
		}
		if err := saveState(ctx, &state); err != nil {
			return BatchResult{}, err
		}
	}
	batch := state.Batches[idx]
	return BatchResult{State: state, StateArtifact: artifact, Batch: &batch, Done: false, Instructions: batchInstructions(state, batch)}, nil
}

func (t *Toolset) PrepareBatch(ctx tool.Context, args PrepareBatchArgs) (PreparedBatchResult, error) {
	next, err := t.NextBatch(ctx, NextBatchArgs{
		RunID:       args.RunID,
		BookID:      args.BookID,
		MarkRunning: args.MarkRunning,
		SessionID:   args.SessionID,
	})
	if err != nil {
		return PreparedBatchResult{}, err
	}
	result := PreparedBatchResult{
		Done:          next.Done,
		State:         summarizeState(next.State),
		StateArtifact: next.StateArtifact,
		Batch:         next.Batch,
		Instructions:  prepareBatchInstructions(next.State),
	}
	if next.Done || next.Batch == nil {
		return result, nil
	}
	maxBatchChars := normalizedMaxBatchChars(args.MaxBatchChars)
	maxPerChapter := normalizedMaxChapterChars(args.MaxCharsPerChapter, maxBatchChars)
	focusDialogue := isDialogueFocusedRun(next.State) || strings.EqualFold(strings.TrimSpace(args.Focus), "dialogue") || strings.Contains(strings.TrimSpace(args.Focus), "对话")
	remaining := maxBatchChars
	if next.State.CurrentSkillArtifact != "" {
		body, truncated, err := loadBoundedArtifactText(ctx, next.State.CurrentSkillArtifact, defaultPreviousSkillMaxChars, false)
		if err == nil {
			result.CurrentSkillArtifact = next.State.CurrentSkillArtifact
			result.CurrentSkillVersion = next.State.CurrentSkillVersion
			result.CurrentSkillBody = body
			result.CurrentSkillTruncated = truncated
			result.ContextCharCount += runeLen(body)
			remaining -= runeLen(body)
		}
	}
	for _, source := range next.Batch.InputChapterArtifacts {
		if remaining <= 0 {
			break
		}
		limit := maxPerChapter
		if remaining < limit {
			limit = remaining
		}
		if limit <= 0 {
			break
		}
		chapter, err := loadPreparedChapter(ctx, next.State, source, limit, focusDialogue)
		if err != nil {
			return PreparedBatchResult{}, err
		}
		result.Chapters = append(result.Chapters, chapter)
		used := runeLen(chapter.Content)
		result.ContextCharCount += used
		remaining -= used
	}
	return result, nil
}

func (t *Toolset) RecordBatch(ctx tool.Context, args RecordBatchArgs) (BatchResult, error) {
	if strings.TrimSpace(args.RunID) == "" {
		return BatchResult{}, fmt.Errorf("run_id is required")
	}
	state, err := loadStateByRunID(ctx, args.RunID)
	if err != nil {
		return BatchResult{}, err
	}
	if err := ensureStateInCurrentProject(ctx, state); err != nil {
		return BatchResult{}, err
	}
	artifact := stateArtifactName(state.RunID)
	idx := findBatchIndex(state, args.BatchIndex, args.StartChapter, args.EndChapter)
	if idx < 0 {
		return BatchResult{}, fmt.Errorf("batch not found: batch_index=%d range=%d..%d", args.BatchIndex, args.StartChapter, args.EndChapter)
	}
	status := normalizeBatchStatus(args.Status)
	if status == "" {
		return BatchResult{}, fmt.Errorf("status is required; use completed, failed, skipped, or running")
	}
	now := nowRFC3339()
	b := &state.Batches[idx]
	b.Status = status
	if args.AnalysisArtifact != "" {
		b.AnalysisArtifact = args.AnalysisArtifact
	}
	if args.SkillDeltaArtifact != "" {
		b.SkillDeltaArtifact = args.SkillDeltaArtifact
	}
	if args.SkillVersionArtifact != "" {
		b.SkillVersionArtifact = args.SkillVersionArtifact
	}
	if args.EvaluationArtifact != "" {
		b.EvaluationArtifact = args.EvaluationArtifact
	}
	if len(args.SourceArtifacts) > 0 {
		b.SourceArtifacts = appendUnique(b.SourceArtifacts, args.SourceArtifacts...)
	}
	if args.Notes != "" {
		b.Notes = args.Notes
		state.LastNote = args.Notes
	}
	if args.ErrorMessage != "" {
		b.ErrorMessage = args.ErrorMessage
		state.ErrorMessage = args.ErrorMessage
	}
	if status == batchCompleted || status == batchSkipped || status == batchFailed {
		b.CompletedAt = now
	}
	b.AnalysisArtifactVersion = latestArtifactVersion(ctx, b.AnalysisArtifact)
	b.SkillDeltaArtifactVersion = latestArtifactVersion(ctx, b.SkillDeltaArtifact)
	b.SkillVersionArtifactVersion = latestArtifactVersion(ctx, b.SkillVersionArtifact)
	b.EvaluationArtifactVersion = latestArtifactVersion(ctx, b.EvaluationArtifact)
	if status == batchCompleted {
		state.CurrentSkillVersion = b.Index
		if b.SkillVersionArtifact != "" {
			state.CurrentSkillArtifact = b.SkillVersionArtifact
			state.CurrentArtifactVersion = b.SkillVersionArtifactVersion
		}
	}
	state.Status = recomputeRunStatus(state)
	state.UpdatedAt = now
	if err := saveState(ctx, &state); err != nil {
		return BatchResult{}, err
	}
	if err := saveLatestPointer(ctx, state, artifact); err != nil {
		return BatchResult{}, err
	}
	if err := registerRunStateArtifacts(ctx, state, artifact, latestPointerArtifactName(state.BookID)); err != nil {
		return BatchResult{}, err
	}
	if err := registerBatchArtifacts(ctx, state, *b); err != nil {
		return BatchResult{}, err
	}
	batch := state.Batches[idx]
	return BatchResult{State: state, StateArtifact: artifact, Batch: &batch, Done: state.Status == statusCompleted, Instructions: postRecordInstructions(state)}, nil
}

func (t *Toolset) RecordOutputs(ctx tool.Context, args RecordOutputsArgs) (BatchResult, error) {
	if strings.TrimSpace(args.RunID) == "" {
		return BatchResult{}, fmt.Errorf("run_id is required")
	}
	state, err := loadStateByRunID(ctx, args.RunID)
	if err != nil {
		return BatchResult{}, err
	}
	if err := ensureStateInCurrentProject(ctx, state); err != nil {
		return BatchResult{}, err
	}
	idx := findBatchIndex(state, args.BatchIndex, 0, 0)
	if idx < 0 {
		return BatchResult{}, fmt.Errorf("batch not found: batch_index=%d", args.BatchIndex)
	}
	if strings.TrimSpace(args.MergedSkill) == "" {
		return BatchResult{}, fmt.Errorf("merged_skill is required")
	}
	b := state.Batches[idx]
	analysis := firstNonEmpty(args.Analysis, "No batch analysis was provided.")
	delta := firstNonEmpty(args.SkillDelta, "{}")
	evaluation := firstNonEmpty(args.QualityNotes, "{}")
	if err := saveTextArtifact(ctx, b.AnalysisArtifact, analysis, "text/markdown; charset=utf-8"); err != nil {
		return BatchResult{}, fmt.Errorf("save analysis artifact: %w", err)
	}
	if err := saveTextArtifact(ctx, b.SkillDeltaArtifact, delta, "application/json; charset=utf-8"); err != nil {
		return BatchResult{}, fmt.Errorf("save skill delta artifact: %w", err)
	}
	if err := saveTextArtifact(ctx, b.SkillVersionArtifact, args.MergedSkill, "text/markdown; charset=utf-8"); err != nil {
		return BatchResult{}, fmt.Errorf("save merged skill artifact: %w", err)
	}
	if err := saveTextArtifact(ctx, b.EvaluationArtifact, evaluation, "application/json; charset=utf-8"); err != nil {
		return BatchResult{}, fmt.Errorf("save evaluation artifact: %w", err)
	}
	return t.RecordBatch(ctx, RecordBatchArgs{
		RunID:                args.RunID,
		BatchIndex:           b.Index,
		Status:               batchCompleted,
		AnalysisArtifact:     b.AnalysisArtifact,
		SkillDeltaArtifact:   b.SkillDeltaArtifact,
		SkillVersionArtifact: b.SkillVersionArtifact,
		EvaluationArtifact:   b.EvaluationArtifact,
		SourceArtifacts:      b.InputChapterArtifacts,
		Notes:                args.Notes,
	})
}

func (t *Toolset) UpdateStatus(ctx tool.Context, args UpdateStatusArgs) (RunStateResult, error) {
	if strings.TrimSpace(args.RunID) == "" {
		return RunStateResult{}, fmt.Errorf("run_id is required")
	}
	state, err := loadStateByRunID(ctx, args.RunID)
	if err != nil {
		return RunStateResult{}, err
	}
	if err := ensureStateInCurrentProject(ctx, state); err != nil {
		return RunStateResult{}, err
	}
	artifact := stateArtifactName(state.RunID)
	status := normalizeRunStatus(args.Status)
	if status == "" {
		return RunStateResult{}, fmt.Errorf("invalid status %q; use paused, running, failed, or completed", args.Status)
	}
	state.Status = status
	state.UpdatedAt = nowRFC3339()
	if args.Reason != "" {
		state.LastNote = args.Reason
	}
	if args.ErrorMessage != "" {
		state.ErrorMessage = args.ErrorMessage
	}
	if err := saveState(ctx, &state); err != nil {
		return RunStateResult{}, err
	}
	if err := registerRunStateArtifacts(ctx, state, artifact, latestPointerArtifactName(state.BookID)); err != nil {
		return RunStateResult{}, err
	}
	return RunStateResult{State: state, StateArtifact: artifact, NextBatch: nextPendingBatch(state)}, nil
}

func (t *Toolset) List(ctx tool.Context, args ListArgs) (ListResult, error) {
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return ListResult{}, err
	}
	resp, err := ctx.Artifacts().List(ctx)
	if err != nil {
		return ListResult{}, fmt.Errorf("list artifacts: %w", err)
	}
	bookID := sanitizeID(args.BookID)
	status := normalizeRunStatus(args.Status)
	limit := args.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	runs := []RunSummary{}
	for _, name := range resp.FileNames {
		if !isRunStateArtifact(name) {
			continue
		}
		state, err := loadStateByArtifact(ctx, name)
		if err != nil {
			continue
		}
		if !sameProjectID(state.ProjectID, projectID) {
			continue
		}
		if bookID != "" && sanitizeID(state.BookID) != bookID {
			continue
		}
		if status != "" && state.Status != status {
			continue
		}
		runs = append(runs, summarizeRun(state, name))
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].UpdatedAt > runs[j].UpdatedAt })
	if len(runs) > limit {
		runs = runs[:limit]
	}
	return ListResult{Count: len(runs), Runs: runs}, nil
}

func buildBatches(state RunState, chapters []chapterSummary) []BatchPlan {
	chapterArtifacts := map[int]string{}
	for _, ch := range chapters {
		chapterArtifacts[ch.No] = ch.Artifact
	}
	batches := []BatchPlan{}
	idx := 1
	for start := state.StartChapter; start <= state.EndChapter; start += state.BatchSize {
		end := start + state.BatchSize - 1
		if end > state.EndChapter {
			end = state.EndChapter
		}
		inputs := []string{}
		for no := start; no <= end; no++ {
			if art := chapterArtifacts[no]; art != "" {
				inputs = append(inputs, art)
			}
		}
		batches = append(batches, BatchPlan{
			Index:                 idx,
			StartChapter:          start,
			EndChapter:            end,
			Status:                batchPending,
			InputChapterArtifacts: inputs,
			AnalysisArtifact:      batchAnalysisArtifactName(state.BookID, state.RunID, start, end),
			SkillDeltaArtifact:    skillDeltaArtifactName(state.BookID, state.RunID, start, end),
			SkillVersionArtifact:  skillVersionArtifactName(state.BookID, state.RunID, idx),
			EvaluationArtifact:    skillEvaluationArtifactName(state.BookID, state.RunID, idx),
		})
		idx++
	}
	return batches
}

func resolveRunState(ctx tool.Context, runID, bookID string) (RunState, string, error) {
	if strings.TrimSpace(runID) != "" {
		state, err := loadStateByRunID(ctx, runID)
		if err != nil {
			return RunState{}, "", err
		}
		if err := ensureStateInCurrentProject(ctx, state); err != nil {
			return RunState{}, "", err
		}
		return state, stateArtifactName(state.RunID), nil
	}
	resolvedBookID, err := resolveBookID(ctx, bookID)
	if err != nil {
		return RunState{}, "", err
	}
	pointer, err := loadLatestPointer(ctx, resolvedBookID)
	if err != nil {
		return RunState{}, "", err
	}
	state, err := loadStateByArtifact(ctx, pointer.StateArtifact)
	if err != nil {
		return RunState{}, "", err
	}
	if err := ensureStateInCurrentProject(ctx, state); err != nil {
		return RunState{}, "", err
	}
	return state, pointer.StateArtifact, nil
}

func ensureStateInCurrentProject(ctx tool.Context, state RunState) error {
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return err
	}
	if !sameProjectID(state.ProjectID, projectID) {
		return fmt.Errorf("run %s belongs to project %q, not current workspace %q", state.RunID, state.ProjectID, projectID)
	}
	return nil
}

func ensureRunProject(ctx tool.Context, projectID string, manifest bookManifest) (projectartifacttool.ProjectRegistry, error) {
	if projectID == "" {
		return projectartifacttool.ProjectRegistry{}, fmt.Errorf("project_id is required")
	}
	registry, _, err := projectartifacttool.EnsureProject(ctx, projectartifacttool.EnsureProjectRequest{
		ProjectID:   projectID,
		Name:        projectID,
		DisplayName: firstNonEmpty(manifest.Title, projectID),
		Description: fmt.Sprintf("《%s》Book-to-Skill 拆书项目，共 %d 章。", firstNonEmpty(manifest.Title, manifest.BookID), manifest.ChapterCount),
		Tags:        []string{"book", "skill", "long-run"},
	})
	if err != nil {
		return projectartifacttool.ProjectRegistry{}, err
	}
	if _, err := projectartifacttool.MountProject(ctx, registry); err != nil {
		return projectartifacttool.ProjectRegistry{}, err
	}
	return registry, nil
}

func registerRunStateArtifacts(ctx tool.Context, state RunState, stateArtifact, latestArtifact string) error {
	projectID := strings.TrimSpace(state.ProjectID)
	if projectID == "" {
		return nil
	}
	if _, err := ensureRunProject(ctx, projectID, bookManifest{BookID: state.BookID, ProjectID: projectID, Title: state.Title, ChapterCount: state.ChapterCount}); err != nil {
		return err
	}
	if stateArtifact != "" {
		if _, _, err := projectartifacttool.RegisterArtifact(ctx, projectartifacttool.RegisterArtifactRequest{
			ProjectID:        projectID,
			ArtifactName:     stateArtifact,
			Type:             "run.state",
			Title:            "Book-to-Skill 长任务状态",
			Description:      "记录当前批次、进度、Skill 版本和每批产物，用于新 session 恢复。",
			ProducerAgent:    "book_skill_runner",
			Visibility:       projectartifacttool.VisibilitySystemHidden,
			Mountable:        boolPtr(false),
			DefaultForAgents: []string{"book_skill_runner"},
			BookID:           state.BookID,
			RunID:            state.RunID,
			Metadata: map[string]string{
				"status":   state.Status,
				"progress": fmt.Sprintf("%d/%d", completedBatchCount(state), len(state.Batches)),
			},
		}); err != nil {
			return err
		}
	}
	if latestArtifact != "" {
		if _, _, err := projectartifacttool.RegisterArtifact(ctx, projectartifacttool.RegisterArtifactRequest{
			ProjectID:        projectID,
			ArtifactName:     latestArtifact,
			Type:             "run.latest",
			Title:            "最新 Book-to-Skill 长任务指针",
			Description:      "保存本书最新 run_id 和 state artifact，便于不传 run_id 时继续。",
			ProducerAgent:    "book_skill_runner",
			Visibility:       projectartifacttool.VisibilitySystemHidden,
			Mountable:        boolPtr(false),
			DefaultForAgents: []string{"book_skill_runner"},
			BookID:           state.BookID,
			RunID:            state.RunID,
		}); err != nil {
			return err
		}
	}
	return nil
}

func registerBatchArtifacts(ctx tool.Context, state RunState, b BatchPlan) error {
	projectID := strings.TrimSpace(state.ProjectID)
	if projectID == "" {
		return nil
	}
	common := func(name, typ, title, desc, visibility string, skillVersion, artifactVersion int) error {
		if strings.TrimSpace(name) == "" {
			return nil
		}
		metadata := map[string]string{
			"batch_status": b.Status,
			"latest_batch": fmt.Sprintf("%d-%d", b.StartChapter, b.EndChapter),
		}
		if artifactVersion > 0 {
			metadata["artifact_version"] = fmt.Sprintf("%d", artifactVersion)
		}
		_, _, err := projectartifacttool.RegisterArtifact(ctx, projectartifacttool.RegisterArtifactRequest{
			ProjectID:        projectID,
			ArtifactName:     name,
			Type:             typ,
			Title:            title,
			Description:      desc,
			ProducerAgent:    "book_skill_runner",
			Visibility:       visibility,
			Mountable:        boolPtr(visibility != projectartifacttool.VisibilitySystemHidden),
			DefaultForAgents: []string{"book_skill_runner"},
			BookID:           state.BookID,
			RunID:            state.RunID,
			BatchIndex:       b.Index,
			StartChapter:     b.StartChapter,
			EndChapter:       b.EndChapter,
			SkillVersion:     skillVersion,
			Metadata:         metadata,
		})
		return err
	}
	if err := common(b.AnalysisArtifact, "batch.analysis", "批次分析（版本化）", "每个 artifact version 对应一轮章节批次分析，内容包含章节功能、爽点链、伏笔链和可复用技法证据。", projectartifacttool.VisibilityProjectVisible, 0, b.AnalysisArtifactVersion); err != nil {
		return err
	}
	if err := common(b.SkillDeltaArtifact, "skill.delta", "Skill 增量（版本化）", "每个 artifact version 对应一轮对上一版 Skill 的 add/update/remove 增量。", projectartifacttool.VisibilityProjectVisible, 0, b.SkillDeltaArtifactVersion); err != nil {
		return err
	}
	if err := common(b.SkillVersionArtifact, "skill.version", "当前 Skill（版本化）", "同一个 artifact 的每个版本对应一轮合并后的完整 SKILL.md。", projectartifacttool.VisibilityProjectDefault, b.Index, b.SkillVersionArtifactVersion); err != nil {
		return err
	}
	if err := common(b.EvaluationArtifact, "skill.evaluation", "Skill 质量检查（版本化）", "每个 artifact version 对应一轮检查结果，判断 Skill 是否过拟合、复述剧情、破坏旧规律或不可复用。", projectartifacttool.VisibilityProjectVisible, b.Index, b.EvaluationArtifactVersion); err != nil {
		return err
	}
	return nil
}

func completedBatchCount(state RunState) int {
	done := 0
	for _, b := range state.Batches {
		if b.Status == batchCompleted || b.Status == batchSkipped {
			done++
		}
	}
	return done
}

func boolPtr(v bool) *bool { return &v }

func findBatchIndex(state RunState, batchIndex, start, end int) int {
	for i, b := range state.Batches {
		if batchIndex > 0 && b.Index == batchIndex {
			return i
		}
		if batchIndex <= 0 && start > 0 && end > 0 && b.StartChapter == start && b.EndChapter == end {
			return i
		}
	}
	return -1
}

func nextPendingBatch(state RunState) *BatchPlan {
	for _, b := range state.Batches {
		if b.Status == "" || b.Status == batchPending || b.Status == batchRunning {
			copy := b
			return &copy
		}
	}
	return nil
}

func recomputeRunStatus(state RunState) string {
	anyFailed := false
	allDone := true
	for _, b := range state.Batches {
		switch b.Status {
		case batchFailed:
			anyFailed = true
			allDone = false
		case batchCompleted, batchSkipped:
			// done
		default:
			allDone = false
		}
	}
	if anyFailed {
		return statusFailed
	}
	if allDone {
		return statusCompleted
	}
	return statusRunning
}

func summarizeRun(state RunState, artifact string) RunSummary {
	done := 0
	for _, b := range state.Batches {
		if b.Status == batchCompleted || b.Status == batchSkipped {
			done++
		}
	}
	return RunSummary{
		RunID:                  state.RunID,
		BookID:                 state.BookID,
		Title:                  state.Title,
		Status:                 state.Status,
		Progress:               fmt.Sprintf("%d/%d batches", done, len(state.Batches)),
		CurrentSkillVersion:    state.CurrentSkillVersion,
		CurrentSkillArtifact:   state.CurrentSkillArtifact,
		CurrentArtifactVersion: state.CurrentArtifactVersion,
		StateArtifact:          artifact,
		UpdatedAt:              state.UpdatedAt,
	}
}

func summarizeState(state RunState) RunStateSummary {
	return RunStateSummary{
		RunID:                  state.RunID,
		ProjectID:              state.ProjectID,
		BookID:                 state.BookID,
		Title:                  state.Title,
		Status:                 state.Status,
		Progress:               fmt.Sprintf("%d/%d batches", completedBatchCount(state), len(state.Batches)),
		CurrentSkillVersion:    state.CurrentSkillVersion,
		CurrentSkillArtifact:   state.CurrentSkillArtifact,
		CurrentArtifactVersion: state.CurrentArtifactVersion,
	}
}

func startInstructions(state RunState) []string {
	instructions := skillTargetInstructions(state)
	instructions = append(instructions,
		"先调用 book_skill_run_prepare_batch 获取第一批受限章节上下文。",
		"每批只分析 prepare_batch 返回的章节片段，不要重新导入、重新切书或加载全文 artifact。",
		"产出 batch analysis、skill delta、merged SKILL.md 和 evaluation 后，调用 book_skill_run_record_outputs 保存并推进状态。",
		"换新 session 时调用 book_skill_run_start 或 book_skill_run_get，工具会基于当前 workspace 恢复状态。",
	)
	return instructions
}

func prepareBatchInstructions(state RunState) []string {
	instructions := skillTargetInstructions(state)
	instructions = append(instructions,
		"只使用 chapters[].content 和 current_skill_body 进行本轮推理；不要调用通用文件检索、不要加载整本书、不要重新切分。",
		"本轮输出四个字段：analysis、skill_delta、merged_skill、quality_notes。",
		"analysis 可以保留证据、章节号和来源信息；merged_skill 必须是外部项目可复用的运行时 SKILL.md，正文不要出现书名、角色名、章节号、book_id、run_id 或 source_artifacts。",
		"完成后调用 book_skill_run_record_outputs，把四个字段交给工具保存版本并推进进度。",
	)
	return instructions
}

func batchInstructions(state RunState, b BatchPlan) []string {
	base := state.CurrentSkillArtifact
	if base == "" {
		base = "空白 Skill 种子"
	}
	readInstruction := fmt.Sprintf("读取第 %d-%d 章；按 input_chapter_artifacts 返回的精确 artifact 文件名用 load_artifacts 读取，不要重新切书。", b.StartChapter, b.EndChapter)
	if hasNovelStoreInputs(b.InputChapterArtifacts) {
		readInstruction = fmt.Sprintf("读取第 %d-%d 章；本批 input_chapter_artifacts 是 NovelStore 指针，必须用 novel_get_chapter(book_id=%s, split_id=active, chapter_no=N, max_chars=12000) 逐章读取，不要 load_artifacts。", b.StartChapter, b.EndChapter, state.BookID)
	}
	instructions := skillTargetInstructions(state)
	instructions = append(instructions,
		readInstruction,
		fmt.Sprintf("当前基础 Skill：%s。", base),
		fmt.Sprintf("保存批次分析到：%s。", b.AnalysisArtifact),
		fmt.Sprintf("保存增量修改到：%s。", b.SkillDeltaArtifact),
		fmt.Sprintf("保存合并后的下一版 Skill 到：%s。", b.SkillVersionArtifact),
		fmt.Sprintf("保存质量检查到：%s。", b.EvaluationArtifact),
		"这些目标 artifact 是版本化的 canonical 文件；每轮保存到同名 artifact，让 artifact service 生成新版本，不要自行改名追加 v001/v002。",
		"skill version 的读者是外部写作 Agent，不是当前项目读者；它必须是可执行 SKILL.md，让外部 Agent 只加载该 Skill 就能写出相近质量的文本。",
		"skill version 正文必须包含：触发条件、写作目标、核心原则、执行流程、技法模块、反过拟合规则、自检清单。",
		"不要把 skill version 写成来源书技法目录、证据章节表、批次进度、质量检查摘要或项目复盘；这些信息只放在 analysis/delta/evaluation。",
		"Skill 只能沉淀可复用方法论，不要复述剧情，不要把书名、角色名、地名、原书桥段硬写进通用 Skill。",
		"完成这些 artifact 后调用 book_skill_run_record_batch(status=completed)。",
	)
	return instructions
}

func hasNovelStoreInputs(inputs []string) bool {
	for _, input := range inputs {
		if strings.HasPrefix(strings.TrimSpace(input), "novelstore:") {
			return true
		}
	}
	return false
}

func loadPreparedChapter(ctx tool.Context, state RunState, source string, maxChars int, dialogueFocused bool) (PreparedChapter, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return PreparedChapter{}, fmt.Errorf("chapter source is empty")
	}
	if strings.HasPrefix(source, "novelstore:") {
		return loadPreparedNovelStoreChapter(ctx, state, source, maxChars, dialogueFocused)
	}
	text, truncated, err := loadBoundedArtifactText(ctx, source, maxChars, dialogueFocused)
	if err != nil {
		return PreparedChapter{}, err
	}
	no := chapterNoFromArtifactName(source)
	return PreparedChapter{
		ChapterNo:      no,
		SourceArtifact: source,
		CharCount:      runeLen(text),
		Content:        text,
		Truncated:      truncated,
		SafetyMessage:  "chapter context is bounded by book_skill_run_prepare_batch; do not load full source artifacts in the model loop",
	}, nil
}

func loadPreparedNovelStoreChapter(ctx tool.Context, state RunState, source string, maxChars int, dialogueFocused bool) (PreparedChapter, error) {
	bookID, splitID, chapterNo, err := parseNovelStoreChapterPointer(source)
	if err != nil {
		return PreparedChapter{}, err
	}
	if bookID == "" {
		bookID = state.BookID
	}
	projectID, err := currentProjectID(ctx)
	if err != nil {
		return PreparedChapter{}, err
	}
	ns, err := novelStoreServiceFromContext(ctx)
	if err != nil {
		return PreparedChapter{}, err
	}
	rawMax := maxChars
	if dialogueFocused && rawMax < maxChars*2 {
		rawMax = maxChars * 2
	}
	result, err := ns.GetChapter(ctx, novelstore.GetChapterRequest{
		TenantID:  novelstore.DefaultTenantID,
		ProjectID: projectID,
		BookID:    bookID,
		SplitID:   splitID,
		ChapterNo: chapterNo,
		MaxChars:  rawMax,
	})
	if err != nil {
		return PreparedChapter{}, err
	}
	content, excerptTruncated := boundTextForSkill(result.Content, maxChars, dialogueFocused)
	return PreparedChapter{
		ChapterNo:      result.Chapter.ChapterNo,
		Title:          result.Chapter.Title,
		SourceArtifact: source,
		CharCount:      result.Chapter.CharCount,
		Content:        content,
		Truncated:      result.Truncated || excerptTruncated,
		SafetyMessage:  result.SafetyMessage,
	}, nil
}

func novelStoreServiceFromContext(ctx tool.Context) (*novelstore.Service, error) {
	cfg := runtimeconfig.FromContext(ctx)
	if cfg == nil {
		return nil, fmt.Errorf("runtime config is not available")
	}
	db, err := store.OpenGORM(cfg.Storage.Database)
	if err != nil {
		return nil, fmt.Errorf("open platform database: %w", err)
	}
	if cfg.Storage.Database.AutoMigrate {
		if err := novelstore.AutoMigrate(db); err != nil {
			return nil, fmt.Errorf("migrate novelstore: %w", err)
		}
	}
	obj, err := objectstore.FromRuntimeConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("open object store: %w", err)
	}
	return novelstore.NewService(db, obj), nil
}

func parseNovelStoreChapterPointer(source string) (bookID, splitID string, chapterNo int, err error) {
	parts := strings.Split(strings.TrimSpace(source), ":")
	if len(parts) != 5 || parts[0] != "novelstore" || parts[3] != "chapter" {
		return "", "", 0, fmt.Errorf("invalid NovelStore chapter pointer %q", source)
	}
	chapterNo, err = strconv.Atoi(strings.TrimLeft(parts[4], "0"))
	if err != nil || chapterNo <= 0 {
		return "", "", 0, fmt.Errorf("invalid NovelStore chapter number in pointer %q", source)
	}
	return sanitizeID(parts[1]), strings.TrimSpace(parts[2]), chapterNo, nil
}

func loadBoundedArtifactText(ctx tool.Context, name string, maxChars int, dialogueFocused bool) (string, bool, error) {
	text, err := loadArtifactText(ctx, name)
	if err != nil {
		return "", false, err
	}
	content, truncated := boundTextForSkill(text, maxChars, dialogueFocused)
	return content, truncated, nil
}

func boundTextForSkill(text string, maxChars int, dialogueFocused bool) (string, bool) {
	text = normalizeNewlines(text)
	if maxChars <= 0 {
		maxChars = defaultChapterContextChars
	}
	if dialogueFocused {
		if excerpt := dialogueExcerpt(text, maxChars); strings.TrimSpace(excerpt) != "" {
			return excerpt, runeLen(excerpt) < runeLen(text)
		}
	}
	if runeLen(text) <= maxChars {
		return text, false
	}
	runes := []rune(text)
	return string(runes[:maxChars]), true
}

func dialogueExcerpt(text string, maxChars int) string {
	if maxChars <= 0 {
		return ""
	}
	lines := strings.Split(normalizeNewlines(text), "\n")
	var out []string
	used := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !looksDialogueLike(line) {
			continue
		}
		lineLen := runeLen(line)
		if used > 0 && used+lineLen+1 > maxChars {
			break
		}
		if lineLen > maxChars {
			r := []rune(line)
			line = string(r[:maxChars])
			lineLen = maxChars
		}
		out = append(out, line)
		used += lineLen + 1
	}
	return strings.Join(out, "\n")
}

func looksDialogueLike(line string) bool {
	return strings.Contains(line, "“") ||
		strings.Contains(line, "”") ||
		strings.Contains(line, "\"") ||
		strings.Contains(line, "说：") ||
		strings.Contains(line, "道：") ||
		strings.Contains(line, "问：") ||
		strings.Contains(line, "答：")
}

func normalizedMaxBatchChars(v int) int {
	if v <= 0 {
		return defaultBatchContextChars
	}
	if v > maxPreparedBatchContextChars {
		return maxPreparedBatchContextChars
	}
	return v
}

func normalizedMaxChapterChars(v, maxBatch int) int {
	if v <= 0 {
		v = defaultChapterContextChars
	}
	if v > maxPreparedChapterContextRune {
		v = maxPreparedChapterContextRune
	}
	if maxBatch > 0 && v > maxBatch {
		return maxBatch
	}
	return v
}

func chapterNoFromArtifactName(name string) int {
	current := ""
	last := ""
	for _, r := range name {
		if r >= '0' && r <= '9' {
			current += string(r)
		} else if current != "" {
			last = current
			current = ""
		}
	}
	if current != "" {
		last = current
	}
	if last == "" {
		return 0
	}
	n, _ := strconv.Atoi(last)
	return n
}

func runeLen(s string) int {
	return utf8.RuneCountInString(s)
}

func intFromString(v string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

func skillTargetInstructions(state RunState) []string {
	var out []string
	if strings.TrimSpace(state.SkillID) != "" {
		out = append(out, fmt.Sprintf("本轮只训练目标 Skill：%s。", strings.TrimSpace(state.SkillID)))
	}
	if isDialogueFocusedRun(state) {
		out = append(out, dialogueFocusInstructions()...)
	}
	if strings.TrimSpace(state.SkillFocus) != "" || strings.TrimSpace(state.TargetTechnique) != "" {
		out = append(out, fmt.Sprintf("训练目标卡：focus=%s；target_technique=%s；abstraction_level=%s。", firstNonEmpty(state.SkillFocus, "未指定"), firstNonEmpty(state.TargetTechnique, "未指定"), firstNonEmpty(state.AbstractionLevel, "atomic_technique")))
	}
	if strings.TrimSpace(state.TransferScope) != "" {
		out = append(out, "迁移范围："+strings.TrimSpace(state.TransferScope))
	}
	if len(state.NonGoals) > 0 {
		out = append(out, "本轮 non-goals："+strings.Join(state.NonGoals, "；"))
	}
	if len(state.ExcludedTerms) > 0 {
		out = append(out, "正式 SKILL.md 正文禁用词："+strings.Join(state.ExcludedTerms, "；"))
	}
	bodyPolicy := firstNonEmpty(state.RuntimeBodyPolicy, "source_free")
	evidencePolicy := firstNonEmpty(state.EvidencePolicy, "metadata_only")
	out = append(out, "正文策略：runtime_body_policy="+bodyPolicy+"；evidence_policy="+evidencePolicy+"。证据只能进 analysis/delta/evaluation/metadata，不能进运行时 SKILL.md 正文。")
	return out
}

func isDialogueFocusedRun(state RunState) bool {
	text := strings.ToLower(strings.Join([]string{state.SkillID, state.SkillFocus, state.TargetTechnique, state.Goal}, " "))
	if strings.Contains(text, "dialogue") || strings.Contains(text, "novel-dialogue-power-gap") {
		return true
	}
	return strings.Contains(text, "对话") || strings.Contains(text, "对白") || strings.Contains(text, "权力差") || strings.Contains(text, "权力")
}

func dialogueFocusInstructions() []string {
	return []string{
		"本轮进入 dialogue_focus：只训练对白/对话技法，不做整章综合拆书。",
		"读取章节后先筛出直接对白、说话前后动作、旁观者反应、命令/评价/沉默/打断/收束动作；非对白剧情只保留一句必要上下文。",
		"batch analysis 必须用对话证据表：chapter_no、scene_context、speaker_role_map、power_signal、dialogue_asymmetry、bystander_reaction、reusable_technique、rejected_source_detail。",
		"如果章节缺少有效对白证据，标记 dialogue_evidence=insufficient，不要硬提炼。",
		"skill delta 只能更新目标对话 Skill；不要新增历史商战、危机叙事、人物登场、爽点铺设等非对话模块。",
	}
}

func postRecordInstructions(state RunState) []string {
	if state.Status == statusCompleted {
		return []string{"所有批次完成，可以加载 current_skill_artifact 做最终整理或调用 SkillAuthoringToolset 保存真实 Skill 草稿。"}
	}
	return []string{"本批已记录。继续下一批时调用 book_skill_run_next_batch；换新 session 时先 book_skill_run_get。"}
}

func normalizeRunList(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		key := strings.ToLower(s)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, s)
	}
	return out
}

func loadStateByRunID(ctx tool.Context, runID string) (RunState, error) {
	runID = sanitizeID(runID)
	if runID == "" {
		return RunState{}, fmt.Errorf("run_id is required")
	}
	return loadStateByArtifact(ctx, stateArtifactName(runID))
}

func loadStateByArtifact(ctx tool.Context, artifactName string) (RunState, error) {
	text, err := loadArtifactText(ctx, artifactName)
	if err != nil {
		return RunState{}, err
	}
	var state RunState
	if err := json.Unmarshal([]byte(text), &state); err != nil {
		return RunState{}, fmt.Errorf("decode run state %q: %w", artifactName, err)
	}
	if state.SchemaVersion == "" {
		state.SchemaVersion = stateSchemaVersion
	}
	if state.RunID == "" {
		return RunState{}, fmt.Errorf("run state %q has empty run_id", artifactName)
	}
	return state, nil
}

func saveState(ctx tool.Context, state *RunState) error {
	if state == nil {
		return fmt.Errorf("state is nil")
	}
	state.SchemaVersion = stateSchemaVersion
	state.UpdatedAt = firstNonEmpty(state.UpdatedAt, nowRFC3339())
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return saveTextArtifact(ctx, stateArtifactName(state.RunID), string(data), "application/json; charset=utf-8")
}

func saveLatestPointer(ctx tool.Context, state RunState, stateArtifact string) error {
	pointer := latestPointer{RunID: state.RunID, BookID: state.BookID, StateArtifact: stateArtifact, UpdatedAt: state.UpdatedAt}
	data, err := json.MarshalIndent(pointer, "", "  ")
	if err != nil {
		return err
	}
	return saveTextArtifact(ctx, latestPointerArtifactName(state.BookID), string(data), "application/json; charset=utf-8")
}

func loadLatestPointer(ctx tool.Context, bookID string) (latestPointer, error) {
	name := latestPointerArtifactName(bookID)
	text, err := loadArtifactText(ctx, name)
	if err != nil {
		return latestPointer{}, fmt.Errorf("load latest run pointer %q: %w; pass run_id explicitly or start a run first", name, err)
	}
	var pointer latestPointer
	if err := json.Unmarshal([]byte(text), &pointer); err != nil {
		return latestPointer{}, fmt.Errorf("decode latest run pointer %q: %w", name, err)
	}
	return pointer, nil
}

func resolveBookID(ctx tool.Context, explicit string) (string, error) {
	bookID := sanitizeID(explicit)
	if bookID != "" {
		return bookID, nil
	}
	text, err := loadArtifactText(ctx, mountedBookArtifact)
	if err != nil {
		if projectBookID := resolveBookIDFromProject(ctx); projectBookID != "" {
			return projectBookID, nil
		}
		return "", fmt.Errorf("book_id is required; call book_mount first, select a project with book.chapter_manifest, or pass book_id explicitly: %w", err)
	}
	var mounted mountedBook
	if err := json.Unmarshal([]byte(text), &mounted); err != nil {
		return "", fmt.Errorf("decode mounted_book.json: %w", err)
	}
	bookID = sanitizeID(mounted.BookID)
	if bookID == "" {
		return "", fmt.Errorf("mounted_book.json has empty book_id")
	}
	projectID, projectErr := currentProjectID(ctx)
	if projectErr != nil {
		return "", projectErr
	}
	if !sameProjectID(mounted.ProjectID, projectID) {
		if projectBookID := resolveBookIDFromProject(ctx); projectBookID != "" {
			return projectBookID, nil
		}
		return "", fmt.Errorf("mounted_book.json belongs to a different workspace")
	}
	return bookID, nil
}

func resolveBookIDFromProject(ctx tool.Context) string {
	projectID, err := projectartifacttool.ResolveProjectID(ctx, "")
	if err != nil {
		return ""
	}
	registry, err := projectartifacttool.LoadProject(ctx, projectID)
	if err != nil {
		return ""
	}
	for _, art := range registry.Artifacts {
		if art.Type != "book.chapter_manifest" {
			continue
		}
		if !isProjectDefaultForBookSkillRunner(art) {
			continue
		}
		if bookID := sanitizeID(art.BookID); bookID != "" {
			return bookID
		}
		if manifest, err := loadManifestFromArtifact(ctx, art.ArtifactName); err == nil {
			if bookID := sanitizeID(manifest.BookID); bookID != "" {
				return bookID
			}
		}
	}
	for _, art := range registry.Artifacts {
		if art.Type != "novel.active_split" {
			continue
		}
		if !isProjectDefaultForBookSkillRunner(art) {
			continue
		}
		if bookID := sanitizeID(firstNonEmpty(art.BookID, art.Metadata["book_id"])); bookID != "" {
			return bookID
		}
	}
	return ""
}

func isProjectDefaultForBookSkillRunner(art projectartifacttool.ProjectArtifact) bool {
	if art.Visibility == projectartifacttool.VisibilityProjectDefault {
		return true
	}
	for _, agent := range art.DefaultForAgents {
		if strings.EqualFold(strings.TrimSpace(agent), "book_skill_runner") {
			return true
		}
	}
	return false
}

func loadManifest(ctx tool.Context, bookID string) (bookManifest, error) {
	bookID = sanitizeID(bookID)
	if bookID == "" {
		return bookManifest{}, fmt.Errorf("book_id is required")
	}
	names := []string{manifestArtifactName(bookID), legacyManifestArtifactName(bookID)}
	var lastErr error
	for _, name := range names {
		manifest, err := loadManifestFromArtifact(ctx, name)
		if err != nil {
			lastErr = err
			continue
		}
		if manifest.BookID == "" {
			manifest.BookID = bookID
		}
		projectID, projectErr := currentProjectID(ctx)
		if projectErr != nil {
			return bookManifest{}, projectErr
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
		if manifest, err := loadNovelStoreManifestFromProject(ctx, bookID); err == nil {
			return manifest, nil
		}
		return bookManifest{}, lastErr
	}
	if manifest, err := loadNovelStoreManifestFromProject(ctx, bookID); err == nil {
		return manifest, nil
	}
	return bookManifest{}, fmt.Errorf("manifest for book_id %q not found", bookID)
}

func loadNovelStoreManifestFromProject(ctx tool.Context, bookID string) (bookManifest, error) {
	projectID, err := projectartifacttool.ResolveProjectID(ctx, "")
	if err != nil {
		return bookManifest{}, err
	}
	registry, err := projectartifacttool.LoadProject(ctx, projectID)
	if err != nil {
		return bookManifest{}, err
	}
	bookID = sanitizeID(bookID)
	for _, art := range registry.Artifacts {
		if art.Type != "novel.active_split" {
			continue
		}
		artBookID := sanitizeID(firstNonEmpty(art.BookID, art.Metadata["book_id"]))
		if bookID != "" && artBookID != bookID {
			continue
		}
		if !isProjectDefaultForBookSkillRunner(art) {
			continue
		}
		splitID := strings.TrimSpace(firstNonEmpty(art.Metadata["split_id"], art.Metadata["active_split_id"]))
		chapterCount := intFromString(firstNonEmpty(art.Metadata["chapter_count"]))
		if art.EndChapter > chapterCount {
			chapterCount = art.EndChapter
		}
		if artBookID == "" || splitID == "" || chapterCount <= 0 {
			continue
		}
		title := firstNonEmpty(art.Metadata["title"], art.Title, artBookID)
		chapters := make([]chapterSummary, 0, chapterCount)
		for no := 1; no <= chapterCount; no++ {
			chapters = append(chapters, chapterSummary{
				No:       no,
				Title:    fmt.Sprintf("第 %d 章", no),
				Artifact: fmt.Sprintf("novelstore:%s:%s:chapter:%04d", artBookID, splitID, no),
			})
		}
		return bookManifest{
			BookID:           artBookID,
			ProjectID:        projectID,
			Title:            title,
			ManifestArtifact: strings.TrimSpace(art.ArtifactName),
			ChapterCount:     chapterCount,
			Chapters:         chapters,
		}, nil
	}
	return bookManifest{}, fmt.Errorf("no NovelStore active split found for book_id %q in current workspace", bookID)
}

func loadManifestFromArtifact(ctx tool.Context, name string) (bookManifest, error) {
	text, err := loadArtifactText(ctx, name)
	if err != nil {
		return bookManifest{}, err
	}
	var manifest bookManifest
	if err := json.Unmarshal([]byte(text), &manifest); err != nil {
		return bookManifest{}, fmt.Errorf("decode manifest %q: %w", name, err)
	}
	if manifest.ManifestArtifact == "" {
		manifest.ManifestArtifact = name
	}
	return manifest, nil
}

func loadArtifactText(ctx tool.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("artifact name is required")
	}
	resp, err := ctx.Artifacts().Load(ctx, name)
	if err != nil {
		return "", fmt.Errorf("load artifact %q: %w", name, err)
	}
	if resp == nil || resp.Part == nil {
		return "", fmt.Errorf("artifact %q is empty", name)
	}
	if resp.Part.Text != "" {
		return normalizeNewlines(resp.Part.Text), nil
	}
	if resp.Part.InlineData == nil {
		return "", fmt.Errorf("artifact %q has no text or inline data", name)
	}
	return normalizeNewlines(string(resp.Part.InlineData.Data)), nil
}

func saveTextArtifact(ctx tool.Context, name, content, mimeType string) error {
	_, err := ctx.Artifacts().Save(ctx, name, &genai.Part{InlineData: &genai.Blob{MIMEType: mimeType, Data: []byte(content)}})
	return err
}

func latestArtifactVersion(ctx tool.Context, name string) int {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0
	}
	resp, err := ctx.Artifacts().Versions(ctx, name)
	if err != nil || resp == nil {
		return 0
	}
	latest := int64(0)
	for _, v := range resp.Versions {
		if v > latest {
			latest = v
		}
	}
	return int(latest)
}

func stateArtifactName(runID string) string {
	return userScopedArtifactName(runStateArtifactPrefix + sanitizeID(runID) + runStateArtifactSuffix)
}

func isRunStateArtifact(name string) bool {
	name = strings.TrimPrefix(strings.TrimSpace(name), userArtifactPrefix)
	return strings.HasPrefix(name, runStateArtifactPrefix) && strings.HasSuffix(name, runStateArtifactSuffix)
}

func latestPointerArtifactName(bookID string) string {
	return userScopedArtifactName(fmt.Sprintf(runLatestArtifactPattern, sanitizeID(bookID)))
}

func manifestArtifactName(bookID string) string {
	return userScopedArtifactName(sanitizeID(bookID) + manifestArtifactSuffix)
}

func legacyManifestArtifactName(bookID string) string {
	return sanitizeID(bookID) + legacyManifestSuffix
}

func batchAnalysisArtifactName(bookID, runID string, start, end int) string {
	return userScopedArtifactName(fmt.Sprintf("book_skill_batch_analysis__%s__%s.md", sanitizeID(bookID), sanitizeID(runID)))
}

func skillDeltaArtifactName(bookID, runID string, start, end int) string {
	return userScopedArtifactName(fmt.Sprintf("book_skill_delta__%s__%s.json", sanitizeID(bookID), sanitizeID(runID)))
}

func skillVersionArtifactName(bookID, runID string, version int) string {
	return userScopedArtifactName(fmt.Sprintf("book_skill__%s__%s.md", sanitizeID(bookID), sanitizeID(runID)))
}

func skillEvaluationArtifactName(bookID, runID string, version int) string {
	return userScopedArtifactName(fmt.Sprintf("book_skill_eval__%s__%s.json", sanitizeID(bookID), sanitizeID(runID)))
}

func userScopedArtifactName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasPrefix(name, userArtifactPrefix) {
		return name
	}
	return userArtifactPrefix + name
}

func normalizeBatchStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case batchPending, batchRunning, batchCompleted, batchFailed, batchSkipped:
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return ""
	}
}

func normalizeRunStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case statusCreated, statusRunning, statusPaused, statusCompleted, statusFailed:
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return ""
	}
}

func sanitizeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastUnderscore := false
	for _, r := range s {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok || unicode.IsLetter(r) || unicode.IsNumber(r) {
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
	if out == "" {
		out = uuid.NewString()
	}
	if utf8.RuneCountInString(out) > 64 {
		r := []rune(out)
		out = string(r[:64])
	}
	return out
}

func normalizeNewlines(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return s
}

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func appendUnique(base []string, values ...string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(base)+len(values))
	for _, v := range base {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

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

// Package planruntool provides artifact-backed durable plan runs. A plan run is
// a platform-level loop contract: it stores objective, iteration limits, status,
// and per-iteration evidence while agents/tools do the actual work.
package planruntool

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/genai"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/adk/tool/projectartifacttool"
)

const (
	schemaVersion      = "plan-run/v1"
	userArtifactPrefix = "user:"

	statusQueued       = "queued"
	statusRunning      = "running"
	statusPaused       = "paused"
	statusWaitingUser  = "waiting_user"
	statusCompleted    = "completed"
	statusFailed       = "failed"
	statusCancelled    = "cancelled"
	iterationRunning   = "running"
	iterationCompleted = "completed"
	iterationFailed    = "failed"
	iterationSkipped   = "skipped"

	planStateArtifactFormat  = "user:plan_run__%s__state.json"
	planLatestArtifactFormat = "user:%s__%s__plan_run_latest.json"
)

// NewToolset creates the plan run toolset.
func NewToolset() (tool.Toolset, error) {
	ts := &Toolset{}
	builders := []func() (tool.Tool, error){
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "plan_run_start",
				Description: "Create a durable plan run for a bounded automatic loop. Use this before repeatedly processing batches such as whole-book skill extraction.",
			}, ts.Start)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "plan_run_get",
				Description: "Load a durable plan run by plan_run_id, or the latest plan run for project_id + plan_type.",
			}, ts.Get)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "plan_run_next_iteration",
				Description: "Return the next allowed plan iteration and loop instructions. Stops when completed, paused, failed, or max_iterations is reached.",
			}, ts.NextIteration)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "plan_run_record_iteration",
				Description: "Record the result of one plan iteration, including source artifacts and produced artifacts, then decide whether the loop may continue.",
			}, ts.RecordIteration)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "plan_run_update_status",
				Description: "Pause, resume, complete, cancel, or fail a durable plan run.",
			}, ts.UpdateStatus)
		},
		func() (tool.Tool, error) {
			return functiontool.New(functiontool.Config{
				Name:        "plan_run_list",
				Description: "List durable plan runs visible in the current artifact workspace.",
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

// Toolset groups durable plan run tools.
type Toolset struct {
	tools []tool.Tool
}

func (t *Toolset) Name() string { return "PlanRunToolset" }

func (t *Toolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) { return t.tools, nil }

type StartArgs struct {
	PlanRunID     string            `json:"plan_run_id,omitempty" jsonschema:"Optional stable plan run id. Defaults to a generated id."`
	PlanType      string            `json:"plan_type" jsonschema:"Plan type, for example book_skill_loop."`
	ProjectID     string            `json:"project_id,omitempty" jsonschema:"Project workspace id. Defaults to mounted or session project_id."`
	AppName       string            `json:"app_name,omitempty" jsonschema:"Agent/app that should execute this plan, for example book_skill_runner."`
	Objective     string            `json:"objective" jsonschema:"Human-readable objective for the whole plan."`
	MaxIterations int               `json:"max_iterations,omitempty" jsonschema:"Maximum iterations this plan may run automatically. Defaults to 20, capped at 200."`
	Metadata      map[string]string `json:"metadata,omitempty" jsonschema:"Extra stable metadata such as book_id, skill_id, batch_size, run_id."`
	Overwrite     bool              `json:"overwrite,omitempty" jsonschema:"Whether to overwrite an existing plan state with the same id."`
}

type LookupArgs struct {
	PlanRunID string `json:"plan_run_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	PlanType  string `json:"plan_type,omitempty"`
}

type NextIterationArgs struct {
	PlanRunID string `json:"plan_run_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	PlanType  string `json:"plan_type,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type RecordIterationArgs struct {
	PlanRunID       string   `json:"plan_run_id"`
	Iteration       int      `json:"iteration"`
	Status          string   `json:"status" jsonschema:"completed, failed, skipped, or running."`
	SourceArtifacts []string `json:"source_artifacts,omitempty"`
	OutputArtifacts []string `json:"output_artifacts,omitempty"`
	Notes           string   `json:"notes,omitempty"`
	ErrorMessage    string   `json:"error_message,omitempty"`
	CompletedPlan   bool     `json:"completed_plan,omitempty" jsonschema:"Set true when the domain run is fully complete."`
	WaitingForUser  bool     `json:"waiting_for_user,omitempty" jsonschema:"Set true when the next step requires a user decision."`
	CurrentPointer  string   `json:"current_pointer,omitempty" jsonschema:"Optional pointer to latest state/skill artifact after this iteration."`
	DomainRunID     string   `json:"domain_run_id,omitempty" jsonschema:"Optional underlying run id such as BookSkillRunToolset run_id."`
	CanContinue     *bool    `json:"can_continue,omitempty" jsonschema:"Optional explicit continuation decision. Defaults from status and limits."`
}

type UpdateStatusArgs struct {
	PlanRunID    string `json:"plan_run_id"`
	Status       string `json:"status" jsonschema:"paused, running, completed, failed, cancelled, waiting_user."`
	Reason       string `json:"reason,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type ListArgs struct {
	ProjectID string `json:"project_id,omitempty"`
	PlanType  string `json:"plan_type,omitempty"`
	Status    string `json:"status,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type PlanState struct {
	SchemaVersion       string            `json:"schema_version"`
	PlanRunID           string            `json:"plan_run_id"`
	PlanType            string            `json:"plan_type"`
	ProjectID           string            `json:"project_id,omitempty"`
	AppName             string            `json:"app_name,omitempty"`
	Objective           string            `json:"objective"`
	Status              string            `json:"status"`
	MaxIterations       int               `json:"max_iterations"`
	CompletedIterations int               `json:"completed_iterations"`
	CurrentIteration    int               `json:"current_iteration"`
	Metadata            map[string]string `json:"metadata,omitempty"`
	DomainRunID         string            `json:"domain_run_id,omitempty"`
	CurrentPointer      string            `json:"current_pointer,omitempty"`
	Iterations          []IterationRecord `json:"iterations,omitempty"`
	CreatedAt           string            `json:"created_at"`
	UpdatedAt           string            `json:"updated_at"`
	ErrorMessage        string            `json:"error_message,omitempty"`
	LastNote            string            `json:"last_note,omitempty"`
}

type IterationRecord struct {
	Iteration       int      `json:"iteration"`
	Status          string   `json:"status"`
	SessionID       string   `json:"session_id,omitempty"`
	SourceArtifacts []string `json:"source_artifacts,omitempty"`
	OutputArtifacts []string `json:"output_artifacts,omitempty"`
	Notes           string   `json:"notes,omitempty"`
	ErrorMessage    string   `json:"error_message,omitempty"`
	StartedAt       string   `json:"started_at,omitempty"`
	CompletedAt     string   `json:"completed_at,omitempty"`
}

type StartResult struct {
	State          PlanState `json:"state"`
	StateArtifact  string    `json:"state_artifact"`
	LatestArtifact string    `json:"latest_artifact,omitempty"`
	Instructions   []string  `json:"instructions"`
}

type StateResult struct {
	State         PlanState `json:"state"`
	StateArtifact string    `json:"state_artifact"`
}

type NextResult struct {
	State         PlanState `json:"state"`
	StateArtifact string    `json:"state_artifact"`
	Iteration     int       `json:"iteration"`
	Done          bool      `json:"done"`
	CanContinue   bool      `json:"can_continue"`
	Instructions  []string  `json:"instructions,omitempty"`
}

type ListResult struct {
	Count int           `json:"count"`
	Runs  []PlanSummary `json:"runs"`
}

type PlanSummary struct {
	PlanRunID           string `json:"plan_run_id"`
	PlanType            string `json:"plan_type"`
	ProjectID           string `json:"project_id,omitempty"`
	AppName             string `json:"app_name,omitempty"`
	Objective           string `json:"objective"`
	Status              string `json:"status"`
	Progress            string `json:"progress"`
	CompletedIterations int    `json:"completed_iterations"`
	MaxIterations       int    `json:"max_iterations"`
	DomainRunID         string `json:"domain_run_id,omitempty"`
	CurrentPointer      string `json:"current_pointer,omitempty"`
	StateArtifact       string `json:"state_artifact"`
	UpdatedAt           string `json:"updated_at"`
}

func (t *Toolset) Start(ctx tool.Context, args StartArgs) (StartResult, error) {
	planType := normalizePlanType(args.PlanType)
	if planType == "" {
		return StartResult{}, fmt.Errorf("plan_type is required")
	}
	projectID, err := resolveProjectID(ctx, args.ProjectID)
	if err != nil {
		return StartResult{}, err
	}
	objective := strings.TrimSpace(args.Objective)
	if objective == "" {
		return StartResult{}, fmt.Errorf("objective is required")
	}
	maxIterations := args.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 20
	}
	if maxIterations > 200 {
		maxIterations = 200
	}
	planRunID := sanitizeID(args.PlanRunID)
	if planRunID == "" {
		planRunID = fmt.Sprintf("%s_%s_%s", planType, projectID, time.Now().UTC().Format("20060102150405"))
	}
	stateArtifact := stateArtifactName(planRunID)
	if !args.Overwrite {
		if _, err := loadStateByArtifact(ctx, stateArtifact); err == nil {
			return StartResult{}, fmt.Errorf("plan run %q already exists; pass overwrite=true only after deciding to replace it", stateArtifact)
		}
	}
	now := nowRFC3339()
	state := PlanState{
		SchemaVersion: schemaVersion,
		PlanRunID:     planRunID,
		PlanType:      planType,
		ProjectID:     projectID,
		AppName:       firstNonEmpty(strings.TrimSpace(args.AppName), ctx.AppName()),
		Objective:     objective,
		Status:        statusQueued,
		MaxIterations: maxIterations,
		Metadata:      normalizeMetadata(args.Metadata),
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	if err := saveState(ctx, &state); err != nil {
		return StartResult{}, err
	}
	latestArtifact := latestArtifactName(state.ProjectID, state.PlanType)
	if err := saveLatestPointer(ctx, state, stateArtifact); err != nil {
		return StartResult{}, err
	}
	if err := registerPlanArtifacts(ctx, state, stateArtifact, latestArtifact); err != nil {
		return StartResult{}, err
	}
	return StartResult{State: state, StateArtifact: stateArtifact, LatestArtifact: latestArtifact, Instructions: startInstructions(state)}, nil
}

func (t *Toolset) Get(ctx tool.Context, args LookupArgs) (StateResult, error) {
	state, artifact, err := resolveState(ctx, args.PlanRunID, args.ProjectID, args.PlanType)
	if err != nil {
		return StateResult{}, err
	}
	return StateResult{State: state, StateArtifact: artifact}, nil
}

func (t *Toolset) NextIteration(ctx tool.Context, args NextIterationArgs) (NextResult, error) {
	state, artifact, err := resolveState(ctx, args.PlanRunID, args.ProjectID, args.PlanType)
	if err != nil {
		return NextResult{}, err
	}
	if isTerminal(state.Status) || state.Status == statusPaused || state.Status == statusWaitingUser {
		return NextResult{State: state, StateArtifact: artifact, Done: isTerminal(state.Status), CanContinue: false, Instructions: stopInstructions(state)}, nil
	}
	if state.CompletedIterations >= state.MaxIterations {
		state.Status = statusPaused
		state.LastNote = "max_iterations reached; ask the user before continuing"
		state.UpdatedAt = nowRFC3339()
		if err := saveAndRegister(ctx, &state, artifact); err != nil {
			return NextResult{}, err
		}
		return NextResult{State: state, StateArtifact: artifact, Done: false, CanContinue: false, Instructions: stopInstructions(state)}, nil
	}
	next := state.CurrentIteration
	if next <= state.CompletedIterations {
		next = state.CompletedIterations + 1
	}
	state.CurrentIteration = next
	state.Status = statusRunning
	upsertIteration(&state, IterationRecord{Iteration: next, Status: iterationRunning, SessionID: strings.TrimSpace(args.SessionID), StartedAt: nowRFC3339()})
	state.UpdatedAt = nowRFC3339()
	if err := saveAndRegister(ctx, &state, artifact); err != nil {
		return NextResult{}, err
	}
	return NextResult{State: state, StateArtifact: artifact, Iteration: next, Done: false, CanContinue: true, Instructions: iterationInstructions(state, next)}, nil
}

func (t *Toolset) RecordIteration(ctx tool.Context, args RecordIterationArgs) (NextResult, error) {
	if strings.TrimSpace(args.PlanRunID) == "" {
		return NextResult{}, fmt.Errorf("plan_run_id is required")
	}
	state, err := loadStateByRunID(ctx, args.PlanRunID)
	if err != nil {
		return NextResult{}, err
	}
	artifact := stateArtifactName(state.PlanRunID)
	iteration := args.Iteration
	if iteration <= 0 {
		iteration = state.CurrentIteration
	}
	if iteration <= 0 {
		return NextResult{}, fmt.Errorf("iteration is required")
	}
	status := normalizeIterationStatus(args.Status)
	if status == "" {
		return NextResult{}, fmt.Errorf("invalid iteration status %q", args.Status)
	}
	now := nowRFC3339()
	rec := IterationRecord{
		Iteration:       iteration,
		Status:          status,
		SourceArtifacts: normalizeList(args.SourceArtifacts),
		OutputArtifacts: normalizeList(args.OutputArtifacts),
		Notes:           strings.TrimSpace(args.Notes),
		ErrorMessage:    strings.TrimSpace(args.ErrorMessage),
		CompletedAt:     now,
	}
	if existing := findIteration(state, iteration); existing != nil && existing.StartedAt != "" {
		rec.StartedAt = existing.StartedAt
	}
	if rec.StartedAt == "" {
		rec.StartedAt = now
	}
	upsertIteration(&state, rec)
	if status == iterationCompleted || status == iterationSkipped {
		if iteration > state.CompletedIterations {
			state.CompletedIterations = iteration
		}
	}
	if args.CurrentPointer != "" {
		state.CurrentPointer = strings.TrimSpace(args.CurrentPointer)
	}
	if args.DomainRunID != "" {
		state.DomainRunID = strings.TrimSpace(args.DomainRunID)
	}
	if args.Notes != "" {
		state.LastNote = strings.TrimSpace(args.Notes)
	}
	if args.ErrorMessage != "" {
		state.ErrorMessage = strings.TrimSpace(args.ErrorMessage)
	}
	state.UpdatedAt = now
	state.Status = statusRunning
	if status == iterationFailed {
		state.Status = statusFailed
	}
	if args.WaitingForUser {
		state.Status = statusWaitingUser
	}
	if args.CompletedPlan {
		state.Status = statusCompleted
	}
	canContinue := status == iterationCompleted || status == iterationSkipped
	if args.CanContinue != nil {
		canContinue = *args.CanContinue
	}
	if !canContinue && !isTerminal(state.Status) && state.Status != statusWaitingUser {
		state.Status = statusPaused
	}
	if state.CompletedIterations >= state.MaxIterations && !isTerminal(state.Status) {
		canContinue = false
		state.Status = statusPaused
		state.LastNote = firstNonEmpty(state.LastNote, "max_iterations reached; ask the user before continuing")
	}
	if err := saveAndRegister(ctx, &state, artifact); err != nil {
		return NextResult{}, err
	}
	return NextResult{State: state, StateArtifact: artifact, Iteration: iteration, Done: state.Status == statusCompleted, CanContinue: canContinue && state.Status == statusRunning, Instructions: postRecordInstructions(state)}, nil
}

func (t *Toolset) UpdateStatus(ctx tool.Context, args UpdateStatusArgs) (StateResult, error) {
	if strings.TrimSpace(args.PlanRunID) == "" {
		return StateResult{}, fmt.Errorf("plan_run_id is required")
	}
	state, err := loadStateByRunID(ctx, args.PlanRunID)
	if err != nil {
		return StateResult{}, err
	}
	status := normalizeRunStatus(args.Status)
	if status == "" {
		return StateResult{}, fmt.Errorf("invalid status %q", args.Status)
	}
	state.Status = status
	if args.Reason != "" {
		state.LastNote = strings.TrimSpace(args.Reason)
	}
	if args.ErrorMessage != "" {
		state.ErrorMessage = strings.TrimSpace(args.ErrorMessage)
	}
	state.UpdatedAt = nowRFC3339()
	artifact := stateArtifactName(state.PlanRunID)
	if err := saveAndRegister(ctx, &state, artifact); err != nil {
		return StateResult{}, err
	}
	return StateResult{State: state, StateArtifact: artifact}, nil
}

func (t *Toolset) List(ctx tool.Context, args ListArgs) (ListResult, error) {
	resp, err := ctx.Artifacts().List(ctx)
	if err != nil {
		return ListResult{}, err
	}
	projectID := sanitizeID(args.ProjectID)
	planType := normalizePlanType(args.PlanType)
	status := normalizeRunStatus(args.Status)
	limit := args.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	runs := []PlanSummary{}
	for _, name := range resp.FileNames {
		if !isPlanStateArtifact(name) {
			continue
		}
		state, err := loadStateByArtifact(ctx, name)
		if err != nil {
			continue
		}
		if projectID != "" && sanitizeID(state.ProjectID) != projectID {
			continue
		}
		if planType != "" && state.PlanType != planType {
			continue
		}
		if status != "" && state.Status != status {
			continue
		}
		runs = append(runs, summarize(state, name))
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].UpdatedAt > runs[j].UpdatedAt })
	if len(runs) > limit {
		runs = runs[:limit]
	}
	return ListResult{Count: len(runs), Runs: runs}, nil
}

func saveAndRegister(ctx tool.Context, state *PlanState, stateArtifact string) error {
	if err := saveState(ctx, state); err != nil {
		return err
	}
	return registerPlanArtifacts(ctx, *state, stateArtifact, latestArtifactName(state.ProjectID, state.PlanType))
}

func saveState(ctx tool.Context, state *PlanState) error {
	state.SchemaVersion = schemaVersion
	state.UpdatedAt = firstNonEmpty(state.UpdatedAt, nowRFC3339())
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return saveTextArtifact(ctx, stateArtifactName(state.PlanRunID), string(data), "application/json; charset=utf-8")
}

func saveLatestPointer(ctx tool.Context, state PlanState, stateArtifact string) error {
	pointer := map[string]any{
		"plan_run_id":    state.PlanRunID,
		"plan_type":      state.PlanType,
		"project_id":     state.ProjectID,
		"state_artifact": stateArtifact,
		"updated_at":     state.UpdatedAt,
	}
	data, err := json.MarshalIndent(pointer, "", "  ")
	if err != nil {
		return err
	}
	return saveTextArtifact(ctx, latestArtifactName(state.ProjectID, state.PlanType), string(data), "application/json; charset=utf-8")
}

func registerPlanArtifacts(ctx tool.Context, state PlanState, stateArtifact, latestArtifact string) error {
	if strings.TrimSpace(state.ProjectID) == "" {
		return nil
	}
	registry, _, err := projectartifacttool.EnsureProject(ctx, projectartifacttool.EnsureProjectRequest{
		ProjectID:   state.ProjectID,
		Name:        state.ProjectID,
		DisplayName: state.ProjectID,
		Description: "PlanRun durable task workspace.",
		Tags:        []string{"plan-run", state.PlanType},
	})
	if err != nil {
		return err
	}
	if _, err := projectartifacttool.MountProject(ctx, registry); err != nil {
		return err
	}
	if stateArtifact != "" {
		if _, _, err := projectartifacttool.RegisterArtifact(ctx, projectartifacttool.RegisterArtifactRequest{
			ProjectID:        state.ProjectID,
			ArtifactName:     stateArtifact,
			Type:             "plan.run",
			Title:            "计划长任务状态",
			Description:      state.Objective,
			ProducerAgent:    firstNonEmpty(state.AppName, "plan_runner"),
			Visibility:       projectartifacttool.VisibilitySystemHidden,
			Mountable:        boolPtr(false),
			DefaultForAgents: []string{firstNonEmpty(state.AppName, "book_skill_runner")},
			RunID:            state.PlanRunID,
			Metadata: map[string]string{
				"plan_type": state.PlanType,
				"status":    state.Status,
				"progress":  fmt.Sprintf("%d/%d", state.CompletedIterations, state.MaxIterations),
			},
		}); err != nil {
			return err
		}
	}
	if latestArtifact != "" {
		if err := saveLatestPointer(ctx, state, stateArtifact); err != nil {
			return err
		}
		if _, _, err := projectartifacttool.RegisterArtifact(ctx, projectartifacttool.RegisterArtifactRequest{
			ProjectID:        state.ProjectID,
			ArtifactName:     latestArtifact,
			Type:             "plan.latest",
			Title:            "最新计划长任务指针",
			Description:      "保存当前项目和计划类型的最新 plan_run_id。",
			ProducerAgent:    firstNonEmpty(state.AppName, "plan_runner"),
			Visibility:       projectartifacttool.VisibilitySystemHidden,
			Mountable:        boolPtr(false),
			DefaultForAgents: []string{firstNonEmpty(state.AppName, "book_skill_runner")},
			RunID:            state.PlanRunID,
			Metadata: map[string]string{
				"plan_type": state.PlanType,
				"status":    state.Status,
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func resolveState(ctx tool.Context, planRunID, projectID, planType string) (PlanState, string, error) {
	if strings.TrimSpace(planRunID) != "" {
		state, err := loadStateByRunID(ctx, planRunID)
		if err != nil {
			return PlanState{}, "", err
		}
		return state, stateArtifactName(state.PlanRunID), nil
	}
	resolvedProjectID, err := resolveProjectID(ctx, projectID)
	if err != nil {
		return PlanState{}, "", err
	}
	resolvedPlanType := normalizePlanType(planType)
	if resolvedPlanType == "" {
		return PlanState{}, "", fmt.Errorf("plan_type is required when plan_run_id is omitted")
	}
	latestName := latestArtifactName(resolvedProjectID, resolvedPlanType)
	text, err := loadArtifactText(ctx, latestName)
	if err != nil {
		return PlanState{}, "", fmt.Errorf("load latest plan pointer %q: %w", latestName, err)
	}
	var pointer struct {
		StateArtifact string `json:"state_artifact"`
	}
	if err := json.Unmarshal([]byte(text), &pointer); err != nil {
		return PlanState{}, "", fmt.Errorf("decode latest plan pointer %q: %w", latestName, err)
	}
	state, err := loadStateByArtifact(ctx, pointer.StateArtifact)
	if err != nil {
		return PlanState{}, "", err
	}
	return state, pointer.StateArtifact, nil
}

func loadStateByRunID(ctx tool.Context, planRunID string) (PlanState, error) {
	return loadStateByArtifact(ctx, stateArtifactName(sanitizeID(planRunID)))
}

func loadStateByArtifact(ctx tool.Context, name string) (PlanState, error) {
	text, err := loadArtifactText(ctx, name)
	if err != nil {
		return PlanState{}, err
	}
	var state PlanState
	if err := json.Unmarshal([]byte(text), &state); err != nil {
		return PlanState{}, fmt.Errorf("decode plan state %q: %w", name, err)
	}
	if state.SchemaVersion == "" {
		state.SchemaVersion = schemaVersion
	}
	if state.PlanRunID == "" {
		return PlanState{}, fmt.Errorf("plan state %q has empty plan_run_id", name)
	}
	return state, nil
}

func resolveProjectID(ctx tool.Context, explicit string) (string, error) {
	if v := sanitizeID(explicit); v != "" {
		return v, nil
	}
	return projectartifacttool.ResolveProjectID(ctx, "")
}

func loadArtifactText(ctx tool.Context, name string) (string, error) {
	resp, err := ctx.Artifacts().Load(ctx, strings.TrimSpace(name))
	if err != nil {
		return "", err
	}
	if resp == nil || resp.Part == nil {
		return "", fmt.Errorf("artifact %q is empty", name)
	}
	if resp.Part.Text != "" {
		return resp.Part.Text, nil
	}
	if resp.Part.InlineData == nil {
		return "", fmt.Errorf("artifact %q has no text or inline data", name)
	}
	return string(resp.Part.InlineData.Data), nil
}

func saveTextArtifact(ctx tool.Context, name, content, mimeType string) error {
	_, err := ctx.Artifacts().Save(ctx, name, &genai.Part{InlineData: &genai.Blob{MIMEType: mimeType, Data: []byte(content)}})
	return err
}

func upsertIteration(state *PlanState, rec IterationRecord) {
	for i := range state.Iterations {
		if state.Iterations[i].Iteration == rec.Iteration {
			if rec.StartedAt == "" {
				rec.StartedAt = state.Iterations[i].StartedAt
			}
			state.Iterations[i] = rec
			return
		}
	}
	state.Iterations = append(state.Iterations, rec)
	sort.SliceStable(state.Iterations, func(i, j int) bool { return state.Iterations[i].Iteration < state.Iterations[j].Iteration })
}

func findIteration(state PlanState, iteration int) *IterationRecord {
	for i := range state.Iterations {
		if state.Iterations[i].Iteration == iteration {
			return &state.Iterations[i]
		}
	}
	return nil
}

func startInstructions(state PlanState) []string {
	return []string{
		"调用 plan_run_next_iteration 获取第一轮执行许可。",
		"每一轮只执行一个可检查的业务批次，保存产物后调用 plan_run_record_iteration。",
		"如果 record 返回 can_continue=true，可以继续调用 plan_run_next_iteration；否则停下来向用户报告。",
	}
}

func iterationInstructions(state PlanState, iteration int) []string {
	if state.PlanType == "book_skill_loop" {
		return []string{
			fmt.Sprintf("Plan iteration %d/%d: 执行一个 Book-to-Skill 批次。", iteration, state.MaxIterations),
			"如果还没有 BookSkill run，调用 book_skill_run_start；否则调用 book_skill_run_get / book_skill_run_next_batch。",
			"读取本批章节，保存 batch analysis、skill delta、skill version、evaluation。",
			"调用 book_skill_run_record_batch(status=completed) 后，再调用 plan_run_record_iteration(status=completed)。",
			"如果 book_skill_run_next_batch 返回 done=true，则调用 plan_run_record_iteration(completed_plan=true)。",
		}
	}
	return []string{fmt.Sprintf("Plan iteration %d/%d: 执行一个业务批次，保存产物后记录 iteration。", iteration, state.MaxIterations)}
}

func postRecordInstructions(state PlanState) []string {
	if state.Status == statusCompleted {
		return []string{"计划已完成。请整理最终产物；如果是 Skill 生成场景，先 validate draft，再保存为 pending_review。"}
	}
	if state.Status == statusWaitingUser {
		return []string{"计划正在等待用户决策，暂停自动循环。"}
	}
	if state.Status == statusPaused {
		return []string{"计划已暂停。需要用户确认后才能继续。"}
	}
	if state.Status == statusFailed {
		return []string{"计划失败。请报告错误，并等待用户决定重试或修复。"}
	}
	return []string{"本轮已记录；如果 can_continue=true，继续调用 plan_run_next_iteration。"}
}

func stopInstructions(state PlanState) []string {
	return []string{fmt.Sprintf("计划当前状态为 %s，不能自动继续。", state.Status)}
}

func summarize(state PlanState, artifact string) PlanSummary {
	return PlanSummary{
		PlanRunID:           state.PlanRunID,
		PlanType:            state.PlanType,
		ProjectID:           state.ProjectID,
		AppName:             state.AppName,
		Objective:           state.Objective,
		Status:              state.Status,
		Progress:            fmt.Sprintf("%d/%d iterations", state.CompletedIterations, state.MaxIterations),
		CompletedIterations: state.CompletedIterations,
		MaxIterations:       state.MaxIterations,
		DomainRunID:         state.DomainRunID,
		CurrentPointer:      state.CurrentPointer,
		StateArtifact:       artifact,
		UpdatedAt:           state.UpdatedAt,
	}
}

func normalizePlanType(v string) string { return sanitizeID(v) }

func normalizeRunStatus(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", statusQueued, statusRunning, statusPaused, statusWaitingUser, statusCompleted, statusFailed, statusCancelled:
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func normalizeIterationStatus(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case iterationRunning, iterationCompleted, iterationFailed, iterationSkipped:
		return strings.ToLower(strings.TrimSpace(v))
	default:
		return ""
	}
}

func isTerminal(status string) bool {
	switch status {
	case statusCompleted, statusFailed, statusCancelled:
		return true
	default:
		return false
	}
}

func normalizeList(in []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, v := range in {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

func normalizeMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, v := range in {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
	if utf8.RuneCountInString(out) > 96 {
		r := []rune(out)
		out = string(r[:96])
	}
	return out
}

func stateArtifactName(planRunID string) string {
	return fmt.Sprintf(planStateArtifactFormat, sanitizeID(planRunID))
}

func latestArtifactName(projectID, planType string) string {
	return fmt.Sprintf(planLatestArtifactFormat, sanitizeID(projectID), sanitizeID(planType))
}

func isPlanStateArtifact(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasPrefix(name, "user:plan_run__") && strings.HasSuffix(name, "__state.json")
}

func boolPtr(v bool) *bool { return &v }

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

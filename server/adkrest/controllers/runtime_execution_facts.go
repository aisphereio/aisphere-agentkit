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

package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"

	"google.golang.org/adk/internal/platform/auth"
	platformruns "google.golang.org/adk/internal/platform/runs"
	"google.golang.org/adk/internal/sessionnative"
	"google.golang.org/adk/server/adkrest/internal/models"
)

// RuntimeAPIController predates the Runtime execution-fact ownership decision.
// Keep the injected service in a controller sidecar while the controller is
// split into RunEngine/ContextBuilder/ToolBroker components. The dependency is
// still explicit at server construction and can be replaced in tests.
var runtimeExecutionFactServices sync.Map // map[*RuntimeAPIController]platformruns.Service

// SetExecutionFactService injects the Runtime fact store. It is intentionally
// separate from the variadic legacy constructor so the dependency cannot be
// silently ignored by type-switch fallback.
func (c *RuntimeAPIController) SetExecutionFactService(service platformruns.Service) {
	if c == nil {
		return
	}
	if service == nil {
		runtimeExecutionFactServices.Delete(c)
		return
	}
	runtimeExecutionFactServices.Store(c, service)
}

// stripExecutionPlanAuthorization removes the runtime-only authorization
// subtree from a serialized Hub execution plan before it is archived as the
// source spec. The subtree (principalSubject, tool approval decisions) is
// execution context, not immutable source; its key name would otherwise trip
// the credential scanner because "authorization" also names the value of an
// HTTP Authorization header. The in-memory RuntimePlan keeps the subtree for
// enforcement — only the archived copy is pruned.
func stripExecutionPlanAuthorization(planJSON []byte) ([]byte, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(planJSON, &object); err != nil {
		return nil, fmt.Errorf("parse execution plan for ledger: %w", err)
	}
	delete(object, "authorization")
	if rawSkills := object["skills"]; len(rawSkills) > 0 {
		var skills []map[string]any
		if err := json.Unmarshal(rawSkills, &skills); err != nil {
			return nil, fmt.Errorf("parse execution plan skills for ledger: %w", err)
		}
		for _, skill := range skills {
			// Signed package URLs are short-lived bearer capabilities. Durable
			// audit provenance is name/version/object plus content digests.
			delete(skill, "downloadUrl")
		}
		cleanedSkills, err := json.Marshal(skills)
		if err != nil {
			return nil, fmt.Errorf("encode sanitized execution plan skills: %w", err)
		}
		object["skills"] = cleanedSkills
	}
	return json.Marshal(object)
}

func (c *RuntimeAPIController) executionFactService() platformruns.Service {
	if c == nil {
		return nil
	}
	service, _ := runtimeExecutionFactServices.Load(c)
	resolved, _ := service.(platformruns.Service)
	return resolved
}

type executionFactContext struct {
	TenantID string
	Run      *platformruns.Run
	Snapshot *platformruns.ExecutionSnapshot
	Attempt  *platformruns.RunAttempt
	TraceID  string
}

func (c *RuntimeAPIController) beginNativeExecutionFacts(
	ctx context.Context,
	req models.RunAgentRequest,
	lease *sessionnative.SessionLease,
	invocationID string,
) (*executionFactContext, error) {
	service := c.executionFactService()
	if service == nil {
		return nil, fmt.Errorf("runtime execution fact service is not configured")
	}
	if lease == nil || lease.Plan == nil {
		return nil, fmt.Errorf("runtime execution facts require a resolved Hub plan")
	}

	principal := auth.FromContext(ctx)
	tenantID := strings.TrimSpace(principal.TenantID)
	if tenantID == "" {
		tenantID = "default"
	}
	principalID := strings.TrimSpace(principal.UserID)
	if principalID == "" {
		principalID = strings.TrimSpace(req.UserId)
	}
	if invocationID == "" {
		invocationID = newRuntimeInvocationID()
	}

	planJSON, err := json.Marshal(lease.Plan)
	if err != nil {
		return nil, fmt.Errorf("marshal resolved Hub execution plan: %w", err)
	}
	// The authorization subtree is runtime execution context (principal
	// derivation, tool approvals), not part of the immutable source spec, and
	// its key name collides with the ledger's credential scanner (an
	// Authorization header value is a credential). Strip it before archiving;
	// the in-memory plan keeps it for authorization enforcement.
	planJSON, err = stripExecutionPlanAuthorization(planJSON)
	if err != nil {
		return nil, err
	}
	canonicalPlan, err := platformruns.CanonicalizeSnapshotJSON(planJSON)
	if err != nil {
		return nil, fmt.Errorf("validate resolved Hub execution plan: %w", err)
	}
	sourceSpecDigest := platformruns.SnapshotDigest(canonicalPlan)

	snapshotEnvelope := map[string]any{
		"schemaVersion":    platformruns.ExecutionSnapshotSchemaV1,
		"sourceSpec":       json.RawMessage(canonicalPlan),
		"sourceSpecDigest": sourceSpecDigest,
		"principal": map[string]any{
			"tenantId":    tenantID,
			"principalId": principalID,
		},
		"request": map[string]any{
			"appName":      req.AppName,
			"sessionId":    req.SessionId,
			"projectId":    firstNativeNonEmpty(req.ProjectId, req.ProjectID),
			"invocationId": invocationID,
		},
		"runtime": map[string]any{
			"engine":   "adk-go",
			"resolver": "hub-runtime-plan/v1",
		},
	}
	snapshotJSON, err := json.Marshal(snapshotEnvelope)
	if err != nil {
		return nil, fmt.Errorf("marshal Runtime execution snapshot: %w", err)
	}

	run, err := service.CreateRun(ctx, platformruns.CreateRunRequest{
		TenantID:       tenantID,
		ProjectID:      firstNativeNonEmpty(req.ProjectId, req.ProjectID),
		AppName:        req.AppName,
		AgentID:        req.AppName,
		AgentRevision:  req.Version,
		UserID:         req.UserId,
		PrincipalID:    principalID,
		SessionID:      req.SessionId,
		Status:         platformruns.StatusPreparing,
		TriggerType:    "conversation_message",
		IdempotencyKey: invocationID,
		InputSummary:   boundedRunInputSummary(contentText(req.NewMessage)),
		TraceID:        invocationID,
	})
	if err != nil {
		return nil, fmt.Errorf("create Runtime run: %w", err)
	}

	snapshot, err := service.CreateExecutionSnapshot(ctx, platformruns.CreateExecutionSnapshotRequest{
		TenantID:         tenantID,
		RunID:            run.ID,
		SourceSpecDigest: sourceSpecDigest,
		AgentID:          req.AppName,
		AgentRevision:    req.Version,
		ResolverVersion:  "hub-runtime-plan/v1",
		RuntimeEngine:    "adk-go",
		SnapshotJSON:     string(snapshotJSON),
	})
	if err != nil {
		_, _ = service.UpdateRun(ctx, tenantID, run.ID, platformruns.UpdateRunRequest{
			Status:       platformruns.StatusFailed,
			FailureCode:  "SNAPSHOT_PERSIST_FAILED",
			ErrorMessage: err.Error(),
		})
		return nil, fmt.Errorf("persist Runtime execution snapshot: %w", err)
	}

	attempt, err := service.CreateAttempt(ctx, platformruns.CreateAttemptRequest{
		TenantID:        tenantID,
		RunID:           run.ID,
		RuntimeEngine:   "adk-go",
		CompilerVersion: "runtimeexecutor/v1",
	})
	if err != nil {
		_, _ = service.UpdateRun(ctx, tenantID, run.ID, platformruns.UpdateRunRequest{
			Status:       platformruns.StatusFailed,
			FailureCode:  "ATTEMPT_CREATE_FAILED",
			ErrorMessage: err.Error(),
		})
		return nil, fmt.Errorf("create Runtime attempt: %w", err)
	}

	facts := &executionFactContext{
		TenantID: tenantID,
		Run:      run,
		Snapshot: snapshot,
		Attempt:  attempt,
		TraceID:  invocationID,
	}
	if _, err := facts.append(ctx, service, "run.created", map[string]any{
		"runId":          run.ID,
		"snapshotId":     snapshot.ID,
		"snapshotDigest": snapshot.SnapshotDigest,
	}); err != nil {
		facts.fail(ctx, service, "EVENT_LEDGER_FAILED", err)
		return nil, err
	}
	if _, err := facts.append(ctx, service, "attempt.queued", map[string]any{
		"attemptId":     attempt.ID,
		"attemptNumber": attempt.AttemptNumber,
	}); err != nil {
		facts.fail(ctx, service, "EVENT_LEDGER_FAILED", err)
		return nil, err
	}
	if _, err := service.UpdateAttempt(ctx, tenantID, attempt.ID, platformruns.UpdateAttemptRequest{
		Status:             platformruns.AttemptStatusRunning,
		CompiledPlanDigest: sourceSpecDigest,
		SandboxLeaseID:     nativeSandboxID(lease),
	}); err != nil {
		facts.fail(ctx, service, "ATTEMPT_START_FAILED", err)
		return nil, err
	}
	if _, err := service.UpdateRun(ctx, tenantID, run.ID, platformruns.UpdateRunRequest{
		Status:           platformruns.StatusRunning,
		CurrentAttemptID: attempt.ID,
	}); err != nil {
		facts.fail(ctx, service, "RUN_START_FAILED", err)
		return nil, err
	}
	if _, err := facts.append(ctx, service, "attempt.started", map[string]any{
		"attemptId":     attempt.ID,
		"runtimeEngine": "adk-go",
	}); err != nil {
		facts.fail(ctx, service, "EVENT_LEDGER_FAILED", err)
		return nil, err
	}
	return facts, nil
}

func (f *executionFactContext) appendModelEvent(
	ctx context.Context,
	service platformruns.Service,
	event models.Event,
) (*platformruns.RuntimeEvent, error) {
	payload, err := json.Marshal(event)
	if err != nil {
		return nil, fmt.Errorf("marshal ADK event for Runtime ledger: %w", err)
	}
	return f.appendRaw(ctx, service, "agent.event", string(payload))
}

func (f *executionFactContext) append(
	ctx context.Context,
	service platformruns.Service,
	eventType string,
	payload any,
) (*platformruns.RuntimeEvent, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal Runtime event %s: %w", eventType, err)
	}
	return f.appendRaw(ctx, service, eventType, string(encoded))
}

func (f *executionFactContext) appendRaw(
	ctx context.Context,
	service platformruns.Service,
	eventType string,
	payloadJSON string,
) (*platformruns.RuntimeEvent, error) {
	if f == nil || f.Run == nil || f.Attempt == nil {
		return nil, fmt.Errorf("runtime execution facts are incomplete")
	}
	event, err := service.AppendEvent(ctx, platformruns.AppendEventRequest{
		TenantID:    f.TenantID,
		RunID:       f.Run.ID,
		AttemptID:   f.Attempt.ID,
		EventType:   eventType,
		PayloadJSON: payloadJSON,
		TraceID:     f.TraceID,
	})
	if err != nil {
		return nil, fmt.Errorf("append Runtime event %s: %w", eventType, err)
	}
	return event, nil
}

func (f *executionFactContext) succeed(ctx context.Context, service platformruns.Service) error {
	if f == nil {
		return nil
	}
	finalizer, ok := service.(platformruns.ExecutionFinalizer)
	if !ok {
		return fmt.Errorf("runtime execution fact store does not support atomic finalization")
	}
	_, err := finalizer.FinalizeExecution(ctx, platformruns.FinalizeExecutionRequest{
		TenantID:  f.TenantID,
		RunID:     f.Run.ID,
		AttemptID: f.Attempt.ID,
		Status:    platformruns.StatusSucceeded,
		TraceID:   f.TraceID,
	})
	if err != nil {
		return fmt.Errorf("finalize successful Runtime execution: %w", err)
	}
	return nil
}

func (f *executionFactContext) fail(ctx context.Context, service platformruns.Service, code string, cause error) {
	if f == nil || service == nil || cause == nil {
		return
	}
	finalizer, ok := service.(platformruns.ExecutionFinalizer)
	if !ok {
		log.Printf("runtime run %s failure could not be finalized atomically [%s]: %v", runtimeFactRunID(f), code, cause)
		return
	}
	_, err := finalizer.FinalizeExecution(ctx, platformruns.FinalizeExecutionRequest{
		TenantID:     f.TenantID,
		RunID:        f.Run.ID,
		AttemptID:    f.Attempt.ID,
		Status:       platformruns.StatusFailed,
		FailureCode:  code,
		ErrorMessage: cause.Error(),
		TraceID:      f.TraceID,
	})
	if err != nil {
		log.Printf("runtime run %s failure finalization failed [%s]: %v (original: %v)", runtimeFactRunID(f), code, err, cause)
		return
	}
	log.Printf("runtime run %s failed [%s]: %v", runtimeFactRunID(f), code, cause)
}

func boundedRunInputSummary(value string) string {
	const maxRunInputSummaryBytes = 2048
	value = strings.TrimSpace(value)
	if len(value) <= maxRunInputSummaryBytes {
		return value
	}
	return value[:maxRunInputSummaryBytes] + "…"
}

func nativeSandboxID(lease *sessionnative.SessionLease) string {
	if lease == nil {
		return ""
	}
	return strings.TrimSpace(lease.Sandbox.SandboxID)
}

func runtimeFactRunID(facts *executionFactContext) string {
	if facts == nil || facts.Run == nil {
		return ""
	}
	return facts.Run.ID
}

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

package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// ExecutionFinalizer is implemented by stores that can atomically persist the
// terminal Attempt status, Run status, and terminal RuntimeEvents.
type ExecutionFinalizer interface {
	FinalizeExecution(ctx context.Context, req FinalizeExecutionRequest) (*RuntimeEvent, error)
}

type FinalizeExecutionRequest struct {
	TenantID     string
	RunID        string
	AttemptID    string
	Status       string
	FailureCode  string
	ErrorMessage string
	TraceID      string
}

func (s *gormService) FinalizeExecution(ctx context.Context, req FinalizeExecutionRequest) (*RuntimeEvent, error) {
	tenantID := strings.TrimSpace(req.TenantID)
	runID := strings.TrimSpace(req.RunID)
	attemptID := strings.TrimSpace(req.AttemptID)
	if tenantID == "" || runID == "" || attemptID == "" {
		return nil, fmt.Errorf("tenant_id, run_id, and attempt_id are required")
	}

	attemptStatus, runStatus, attemptEventType, runEventType, err := terminalStatusMapping(req.Status)
	if err != nil {
		return nil, err
	}

	var terminalEvent *RuntimeEvent
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run Run
		if err := locked(tx).
			Where("tenant_id = ? AND id = ?", tenantID, runID).
			First(&run).Error; err != nil {
			return err
		}
		var attempt RunAttempt
		if err := locked(tx).
			Where("tenant_id = ? AND id = ? AND run_id = ?", tenantID, attemptID, runID).
			First(&attempt).Error; err != nil {
			return err
		}

		if run.Status == runStatus && attempt.Status == attemptStatus {
			var existing RuntimeEvent
			err := tx.Where(
				"tenant_id = ? AND run_id = ? AND attempt_id = ? AND event_type = ?",
				tenantID,
				runID,
				attemptID,
				runEventType,
			).Order("sequence DESC").First(&existing).Error
			if err == nil {
				terminalEvent = &existing
				return nil
			}
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		if err := validateAttemptTransition(attempt.Status, attemptStatus); err != nil {
			return err
		}
		if err := validateRunTransition(run.Status, runStatus); err != nil {
			return err
		}

		now := time.Now().UTC()
		attempt.Status = attemptStatus
		attempt.FinishedAt = &now
		attempt.FailureCode = strings.TrimSpace(req.FailureCode)
		attempt.ErrorMessage = req.ErrorMessage
		if err := tx.Save(&attempt).Error; err != nil {
			return err
		}

		run.Status = runStatus
		run.FinishedAt = &now
		run.FailureCode = strings.TrimSpace(req.FailureCode)
		run.ErrorMessage = req.ErrorMessage
		if runStatus == StatusCancelled {
			run.CancelledAt = &now
		}
		if err := tx.Save(&run).Error; err != nil {
			return err
		}

		var latest uint64
		if err := tx.Model(&RuntimeEvent{}).
			Where("tenant_id = ? AND run_id = ?", tenantID, runID).
			Select("COALESCE(MAX(sequence), 0)").
			Scan(&latest).Error; err != nil {
			return err
		}

		attemptPayload, err := json.Marshal(map[string]any{
			"attemptId":  attemptID,
			"status":     attemptStatus,
			"failureCode": strings.TrimSpace(req.FailureCode),
			"message":    req.ErrorMessage,
		})
		if err != nil {
			return err
		}
		attemptEvent := &RuntimeEvent{
			TenantID:     tenantID,
			RunID:        runID,
			AttemptID:    attemptID,
			Sequence:     latest + 1,
			EventType:    attemptEventType,
			EventVersion: RuntimeEventVersionV1,
			PayloadJSON:  string(attemptPayload),
			TraceID:      strings.TrimSpace(req.TraceID),
			CreatedAt:    now,
		}
		if err := tx.Create(attemptEvent).Error; err != nil {
			return err
		}

		runPayload, err := json.Marshal(map[string]any{
			"runId":       runID,
			"attemptId":   attemptID,
			"status":      runStatus,
			"failureCode": strings.TrimSpace(req.FailureCode),
			"message":     req.ErrorMessage,
		})
		if err != nil {
			return err
		}
		runEvent := &RuntimeEvent{
			TenantID:     tenantID,
			RunID:        runID,
			AttemptID:    attemptID,
			Sequence:     latest + 2,
			EventType:    runEventType,
			EventVersion: RuntimeEventVersionV1,
			PayloadJSON:  string(runPayload),
			TraceID:      strings.TrimSpace(req.TraceID),
			CreatedAt:    now,
		}
		if err := tx.Create(runEvent).Error; err != nil {
			return err
		}
		terminalEvent = runEvent
		return nil
	})
	if err != nil {
		return nil, err
	}
	return terminalEvent, nil
}

func terminalStatusMapping(status string) (attemptStatus, runStatus, attemptEventType, runEventType string, err error) {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case StatusSucceeded, StatusCompleted:
		return AttemptStatusSucceeded, StatusSucceeded, "attempt.completed", "run.completed", nil
	case StatusFailed:
		return AttemptStatusFailed, StatusFailed, "attempt.failed", "run.failed", nil
	case StatusCancelled:
		return AttemptStatusCancelled, StatusCancelled, "attempt.cancelled", "run.cancelled", nil
	default:
		return "", "", "", "", fmt.Errorf("unsupported terminal execution status %q", status)
	}
}

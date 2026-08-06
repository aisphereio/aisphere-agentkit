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
	"errors"
)

// ErrLegacyPostgresExecutionFacts marks the old pgx-only Run store as a
// migration-only implementation. The Runtime execution fact path is moving to
// the single GORM/PostgreSQL store; new callers must fail closed instead of
// silently writing a second set of run tables.
var ErrLegacyPostgresExecutionFacts = errors.New("legacy pgx run store does not support execution snapshots, attempts, or sequenced runtime events")

func (s *postgresService) CreateExecutionSnapshot(context.Context, CreateExecutionSnapshotRequest) (*ExecutionSnapshot, error) {
	return nil, ErrLegacyPostgresExecutionFacts
}

func (s *postgresService) GetExecutionSnapshot(context.Context, string, string) (*ExecutionSnapshot, error) {
	return nil, ErrLegacyPostgresExecutionFacts
}

func (s *postgresService) CreateAttempt(context.Context, CreateAttemptRequest) (*RunAttempt, error) {
	return nil, ErrLegacyPostgresExecutionFacts
}

func (s *postgresService) GetAttempt(context.Context, string, string) (*RunAttempt, error) {
	return nil, ErrLegacyPostgresExecutionFacts
}

func (s *postgresService) ListAttempts(context.Context, string, string) ([]RunAttempt, error) {
	return nil, ErrLegacyPostgresExecutionFacts
}

func (s *postgresService) UpdateAttempt(context.Context, string, string, UpdateAttemptRequest) (*RunAttempt, error) {
	return nil, ErrLegacyPostgresExecutionFacts
}

func (s *postgresService) AppendEvent(context.Context, AppendEventRequest) (*RuntimeEvent, error) {
	return nil, ErrLegacyPostgresExecutionFacts
}

func (s *postgresService) ListEvents(context.Context, string, string, uint64, int) ([]RuntimeEvent, error) {
	return nil, ErrLegacyPostgresExecutionFacts
}

// isTerminalStatus is retained for the migration-only pgx implementation.
func isTerminalStatus(status string) bool {
	return isTerminalRunStatus(status)
}

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

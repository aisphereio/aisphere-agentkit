package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RunEvent is a durable event/timeline item for platform runs. It is separate
// from ADK conversation events and is used for operations, SSE reconnect, trace
// indexing, and audit-friendly run replay.
type RunEvent struct {
	ID        string          `json:"id"`
	TenantID  string          `json:"tenant_id"`
	RunID     string          `json:"run_id"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
}

// EventService is implemented by run stores that durably keep run events.
type EventService interface {
	AppendRunEvent(ctx context.Context, tenantID, runID, eventType string, payload json.RawMessage) (*RunEvent, error)
	ListRunEvents(ctx context.Context, tenantID, runID string, limit int) ([]RunEvent, error)
}

type postgresService struct {
	pool *pgxpool.Pool
}

// NewPostgresService opens the production run store backed by PostgreSQL and pgxpool.
func NewPostgresService(ctx context.Context, dsn string, autoMigrate bool) (Service, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, fmt.Errorf("postgres run service requires a DSN")
	}
	pcfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if pcfg.MaxConns == 0 {
		pcfg.MaxConns = 30
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	s := &postgresService{pool: pool}
	if autoMigrate {
		if err := s.AutoMigrate(ctx); err != nil {
			pool.Close()
			return nil, err
		}
	}
	return s, nil
}

func (s *postgresService) AutoMigrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS platform_runs (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			app_name TEXT,
			user_id TEXT,
			session_id TEXT,
			status TEXT NOT NULL,
			input_summary TEXT,
			model_ref TEXT,
			error_message TEXT,
			metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
			started_at TIMESTAMPTZ NOT NULL,
			finished_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_runs_tenant_created ON platform_runs(tenant_id,created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_runs_session ON platform_runs(tenant_id,session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_runs_status ON platform_runs(tenant_id,status)`,
		`CREATE TABLE IF NOT EXISTS platform_run_steps (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			run_id TEXT NOT NULL REFERENCES platform_runs(id) ON DELETE CASCADE,
			kind TEXT NOT NULL,
			status TEXT NOT NULL,
			payload JSONB NOT NULL DEFAULT '{}'::jsonb,
			error_message TEXT,
			started_at TIMESTAMPTZ NOT NULL,
			finished_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_run_steps_run ON platform_run_steps(tenant_id,run_id,created_at ASC)`,
		`CREATE TABLE IF NOT EXISTS platform_run_events (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			run_id TEXT NOT NULL REFERENCES platform_runs(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			payload JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_run_events_run ON platform_run_events(tenant_id,run_id,created_at ASC)`,
		`CREATE INDEX IF NOT EXISTS idx_platform_run_events_payload_gin ON platform_run_events USING GIN(payload)`,
	}
	for _, stmt := range stmts {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *postgresService) CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error) {
	if strings.TrimSpace(req.TenantID) == "" {
		return nil, fmt.Errorf("tenant_id is required")
	}
	now := time.Now().UTC()
	r := &Run{ID: uuid.NewString(), TenantID: req.TenantID, AppName: req.AppName, UserID: req.UserID, SessionID: req.SessionID, Status: firstNonEmpty(req.Status, StatusRunning), InputSummary: req.InputSummary, ModelRef: req.ModelRef, MetadataJSON: req.MetadataJSON, StartedAt: now, CreatedAt: now, UpdatedAt: now}
	payload := jsonStringOrObject(r.MetadataJSON)
	_, err := s.pool.Exec(ctx, `INSERT INTO platform_runs(id,tenant_id,app_name,user_id,session_id,status,input_summary,model_ref,metadata,started_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::jsonb,$10,$11,$11)`, r.ID, r.TenantID, r.AppName, r.UserID, r.SessionID, r.Status, r.InputSummary, r.ModelRef, payload, r.StartedAt, r.CreatedAt)
	return r, err
}

func (s *postgresService) GetRun(ctx context.Context, tenantID, id string) (*Run, error) {
	row := s.pool.QueryRow(ctx, `SELECT id,tenant_id,COALESCE(app_name,''),COALESCE(user_id,''),COALESCE(session_id,''),status,COALESCE(input_summary,''),COALESCE(model_ref,''),COALESCE(error_message,''),metadata::text,started_at,finished_at,created_at,updated_at FROM platform_runs WHERE tenant_id=$1 AND id=$2`, tenantID, id)
	return scanRun(row)
}

func (s *postgresService) ListRuns(ctx context.Context, filter ListRunsFilter) ([]Run, error) {
	clauses := []string{"tenant_id=$1"}
	args := []any{filter.TenantID}
	add := func(col, v string) {
		if strings.TrimSpace(v) == "" {
			return
		}
		args = append(args, strings.TrimSpace(v))
		clauses = append(clauses, fmt.Sprintf("%s=$%d", col, len(args)))
	}
	add("app_name", filter.AppName)
	add("user_id", filter.UserID)
	add("session_id", filter.SessionID)
	add("status", filter.Status)
	limit := filter.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `SELECT id,tenant_id,COALESCE(app_name,''),COALESCE(user_id,''),COALESCE(session_id,''),status,COALESCE(input_summary,''),COALESCE(model_ref,''),COALESCE(error_message,''),metadata::text,started_at,finished_at,created_at,updated_at FROM platform_runs WHERE `+strings.Join(clauses, " AND ")+` ORDER BY created_at DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

func (s *postgresService) UpdateRun(ctx context.Context, tenantID, id string, req UpdateRunRequest) (*Run, error) {
	r, err := s.GetRun(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if req.Status != "" {
		r.Status = req.Status
		if isTerminalStatus(req.Status) {
			now := time.Now().UTC()
			r.FinishedAt = &now
		} else {
			r.FinishedAt = nil
		}
	}
	if req.ErrorMessage != "" {
		r.ErrorMessage = req.ErrorMessage
	}
	if req.MetadataJSON != nil {
		r.MetadataJSON = *req.MetadataJSON
	}
	r.UpdatedAt = time.Now().UTC()
	_, err = s.pool.Exec(ctx, `UPDATE platform_runs SET status=$3,error_message=$4,metadata=$5::jsonb,finished_at=$6,updated_at=$7 WHERE tenant_id=$1 AND id=$2`, tenantID, id, r.Status, r.ErrorMessage, jsonStringOrObject(r.MetadataJSON), r.FinishedAt, r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (s *postgresService) CreateStep(ctx context.Context, req CreateStepRequest) (*Step, error) {
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.RunID) == "" {
		return nil, fmt.Errorf("tenant_id and run_id are required")
	}
	now := time.Now().UTC()
	st := &Step{ID: uuid.NewString(), TenantID: req.TenantID, RunID: req.RunID, Kind: firstNonEmpty(req.Kind, "unknown"), Status: firstNonEmpty(req.Status, StatusRunning), PayloadJSON: req.PayloadJSON, StartedAt: now, CreatedAt: now, UpdatedAt: now}
	_, err := s.pool.Exec(ctx, `INSERT INTO platform_run_steps(id,tenant_id,run_id,kind,status,payload,started_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6::jsonb,$7,$8,$8)`, st.ID, st.TenantID, st.RunID, st.Kind, st.Status, jsonStringOrObject(st.PayloadJSON), st.StartedAt, st.CreatedAt)
	return st, err
}

func (s *postgresService) ListSteps(ctx context.Context, tenantID, runID string) ([]Step, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,tenant_id,run_id,kind,status,payload::text,COALESCE(error_message,''),started_at,finished_at,created_at,updated_at FROM platform_run_steps WHERE tenant_id=$1 AND run_id=$2 ORDER BY created_at ASC`, tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Step{}
	for rows.Next() {
		st, err := scanStep(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *st)
	}
	return out, rows.Err()
}

func (s *postgresService) UpdateStep(ctx context.Context, tenantID, id string, req UpdateStepRequest) (*Step, error) {
	var st Step
	row := s.pool.QueryRow(ctx, `SELECT id,tenant_id,run_id,kind,status,payload::text,COALESCE(error_message,''),started_at,finished_at,created_at,updated_at FROM platform_run_steps WHERE tenant_id=$1 AND id=$2`, tenantID, id)
	cur, err := scanStep(row)
	if err != nil {
		return nil, err
	}
	st = *cur
	if req.Status != "" {
		st.Status = req.Status
		if isTerminalStatus(req.Status) {
			now := time.Now().UTC()
			st.FinishedAt = &now
		} else {
			st.FinishedAt = nil
		}
	}
	if req.ErrorMessage != "" {
		st.ErrorMessage = req.ErrorMessage
	}
	if req.PayloadJSON != nil {
		st.PayloadJSON = *req.PayloadJSON
	}
	st.UpdatedAt = time.Now().UTC()
	_, err = s.pool.Exec(ctx, `UPDATE platform_run_steps SET status=$3,payload=$4::jsonb,error_message=$5,finished_at=$6,updated_at=$7 WHERE tenant_id=$1 AND id=$2`, tenantID, id, st.Status, jsonStringOrObject(st.PayloadJSON), st.ErrorMessage, st.FinishedAt, st.UpdatedAt)
	return &st, err
}

func (s *postgresService) AppendRunEvent(ctx context.Context, tenantID, runID, eventType string, payload json.RawMessage) (*RunEvent, error) {
	ev := &RunEvent{ID: uuid.NewString(), TenantID: tenantID, RunID: runID, Type: firstNonEmpty(eventType, "event"), Payload: payload, CreatedAt: time.Now().UTC()}
	if len(ev.Payload) == 0 {
		ev.Payload = []byte(`{}`)
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO platform_run_events(id,tenant_id,run_id,type,payload,created_at) VALUES($1,$2,$3,$4,$5::jsonb,$6)`, ev.ID, ev.TenantID, ev.RunID, ev.Type, string(ev.Payload), ev.CreatedAt)
	return ev, err
}

func (s *postgresService) ListRunEvents(ctx context.Context, tenantID, runID string, limit int) ([]RunEvent, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, `SELECT id,tenant_id,run_id,type,payload,created_at FROM platform_run_events WHERE tenant_id=$1 AND run_id=$2 ORDER BY created_at ASC LIMIT $3`, tenantID, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RunEvent{}
	for rows.Next() {
		var ev RunEvent
		if err := rows.Scan(&ev.ID, &ev.TenantID, &ev.RunID, &ev.Type, &ev.Payload, &ev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

type scanner interface{ Scan(dest ...any) error }

func scanRun(row scanner) (*Run, error) {
	var r Run
	var finished pgtype.Timestamptz
	if err := row.Scan(&r.ID, &r.TenantID, &r.AppName, &r.UserID, &r.SessionID, &r.Status, &r.InputSummary, &r.ModelRef, &r.ErrorMessage, &r.MetadataJSON, &r.StartedAt, &finished, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	if finished.Valid {
		t := finished.Time
		r.FinishedAt = &t
	}
	return &r, nil
}
func scanStep(row scanner) (*Step, error) {
	var s Step
	var finished pgtype.Timestamptz
	if err := row.Scan(&s.ID, &s.TenantID, &s.RunID, &s.Kind, &s.Status, &s.PayloadJSON, &s.ErrorMessage, &s.StartedAt, &finished, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	if finished.Valid {
		t := finished.Time
		s.FinishedAt = &t
	}
	return &s, nil
}
func jsonStringOrObject(v string) string {
	if strings.TrimSpace(v) == "" {
		return "{}"
	}
	if json.Valid([]byte(v)) {
		return v
	}
	b, _ := json.Marshal(map[string]string{"value": v})
	return string(b)
}

var _ Service = (*postgresService)(nil)
var _ EventService = (*postgresService)(nil)

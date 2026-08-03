package session

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"google.golang.org/adk/internal/sessionutils"
)

// PostgresConfig configures the production PostgreSQL session service.
type PostgresConfig struct {
	DSN         string
	AutoMigrate bool
	MaxConns    int32
}

// PostgresService opens a production session service backed by PostgreSQL,
// pgxpool, and JSONB event/state storage.
func PostgresService(ctx context.Context, cfg PostgresConfig) (Service, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, fmt.Errorf("postgres session service requires a DSN")
	}
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, err
	}
	if cfg.MaxConns > 0 {
		pcfg.MaxConns = cfg.MaxConns
	} else if pcfg.MaxConns == 0 {
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
	s := &postgresSessionService{pool: pool}
	if cfg.AutoMigrate {
		if err := s.AutoMigrate(ctx); err != nil {
			pool.Close()
			return nil, err
		}
	}
	return s, nil
}

type postgresSessionService struct {
	pool *pgxpool.Pool
}

func (s *postgresSessionService) AutoMigrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS agentkit_app_state (
			app_name TEXT PRIMARY KEY,
			state JSONB NOT NULL DEFAULT '{}'::jsonb,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`,
		`CREATE TABLE IF NOT EXISTS agentkit_user_state (
			app_name TEXT NOT NULL,
			user_id TEXT NOT NULL,
			state JSONB NOT NULL DEFAULT '{}'::jsonb,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY(app_name,user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS agentkit_sessions (
			app_name TEXT NOT NULL,
			user_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			state JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			PRIMARY KEY(app_name,user_id,session_id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agentkit_sessions_app_user_updated ON agentkit_sessions(app_name,user_id,updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_agentkit_sessions_session_id ON agentkit_sessions(session_id)`,
		`CREATE TABLE IF NOT EXISTS agentkit_session_events (
			seq BIGSERIAL PRIMARY KEY,
			app_name TEXT NOT NULL,
			user_id TEXT NOT NULL,
			session_id TEXT NOT NULL,
			event_id TEXT NOT NULL,
			invocation_id TEXT,
			author TEXT,
			timestamp TIMESTAMPTZ NOT NULL,
			event JSONB NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			UNIQUE(app_name,user_id,session_id,event_id),
			FOREIGN KEY(app_name,user_id,session_id) REFERENCES agentkit_sessions(app_name,user_id,session_id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_agentkit_session_events_session_ts ON agentkit_session_events(app_name,user_id,session_id,timestamp,seq)`,
		`CREATE INDEX IF NOT EXISTS idx_agentkit_session_events_event_gin ON agentkit_session_events USING GIN(event)`,
	}
	for _, stmt := range stmts {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *postgresSessionService) Close() { s.pool.Close() }

func (s *postgresSessionService) Create(ctx context.Context, req *CreateRequest) (*CreateResponse, error) {
	if req == nil || req.AppName == "" || req.UserID == "" {
		return nil, fmt.Errorf("app_name and user_id are required")
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = uuid.NewString()
	}
	state := map[string]any{}
	if req.State != nil {
		state = maps.Clone(req.State)
	}
	appDelta, userDelta, sessionDelta := sessionutils.ExtractStateDeltas(state)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	appState, err := upsertMergedState(ctx, tx, "agentkit_app_state", []string{"app_name"}, []any{req.AppName}, appDelta)
	if err != nil {
		return nil, err
	}
	userState, err := upsertMergedState(ctx, tx, "agentkit_user_state", []string{"app_name", "user_id"}, []any{req.AppName, req.UserID}, userDelta)
	if err != nil {
		return nil, err
	}
	sessionState := stateMap(sessionDelta)
	b, _ := json.Marshal(sessionState)
	now := time.Now().UTC()
	_, err = tx.Exec(ctx, `INSERT INTO agentkit_sessions(app_name,user_id,session_id,state,created_at,updated_at)
		VALUES($1,$2,$3,$4::jsonb,$5,$5)`, req.AppName, req.UserID, sessionID, string(b), now)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	stored := &session{id: id{appName: req.AppName, userID: req.UserID, sessionID: sessionID}, state: sessionutils.MergeStates(appState, userState, sessionState), updatedAt: now}
	return &CreateResponse{Session: stored}, nil
}

func (s *postgresSessionService) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	if req == nil || req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return nil, fmt.Errorf("app_name, user_id, session_id are required")
	}
	stored, err := s.loadSession(ctx, req.AppName, req.UserID, req.SessionID)
	if err != nil {
		return nil, err
	}
	events, err := s.loadEvents(ctx, req.AppName, req.UserID, req.SessionID, req.NumRecentEvents, req.After)
	if err != nil {
		return nil, err
	}
	stored.events = events
	return &GetResponse{Session: stored}, nil
}

func (s *postgresSessionService) List(ctx context.Context, req *ListRequest) (*ListResponse, error) {
	if req == nil || req.AppName == "" {
		return nil, fmt.Errorf("app_name is required")
	}
	clauses := []string{"app_name=$1"}
	args := []any{req.AppName}
	if req.UserID != "" {
		args = append(args, req.UserID)
		clauses = append(clauses, fmt.Sprintf("user_id=$%d", len(args)))
	}
	rows, err := s.pool.Query(ctx, `SELECT app_name,user_id,session_id,state,updated_at FROM agentkit_sessions WHERE `+strings.Join(clauses, " AND ")+` ORDER BY updated_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		stored, err := s.scanSessionRow(ctx, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, stored)
	}
	return &ListResponse{Sessions: out}, rows.Err()
}

func (s *postgresSessionService) Delete(ctx context.Context, req *DeleteRequest) error {
	if req == nil || req.AppName == "" || req.UserID == "" || req.SessionID == "" {
		return fmt.Errorf("app_name, user_id, session_id are required")
	}
	_, err := s.pool.Exec(ctx, `DELETE FROM agentkit_sessions WHERE app_name=$1 AND user_id=$2 AND session_id=$3`, req.AppName, req.UserID, req.SessionID)
	return err
}

func (s *postgresSessionService) AppendEvent(ctx context.Context, curSession Session, event *Event) error {
	if curSession == nil || event == nil || event.Partial {
		return nil
	}
	if event.ID == "" {
		event.ID = uuid.NewString()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}
	processed := trimTempDeltaState(event)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	var rawState []byte
	if err := tx.QueryRow(ctx, `SELECT state FROM agentkit_sessions WHERE app_name=$1 AND user_id=$2 AND session_id=$3 FOR UPDATE`, curSession.AppName(), curSession.UserID(), curSession.ID()).Scan(&rawState); err != nil {
		return err
	}
	sessionState := stateMap{}
	_ = json.Unmarshal(rawState, &sessionState)
	appDelta, userDelta, sessionDelta := sessionutils.ExtractStateDeltas(processed.Actions.StateDelta)
	if _, err := upsertMergedState(ctx, tx, "agentkit_app_state", []string{"app_name"}, []any{curSession.AppName()}, appDelta); err != nil {
		return err
	}
	if _, err := upsertMergedState(ctx, tx, "agentkit_user_state", []string{"app_name", "user_id"}, []any{curSession.AppName(), curSession.UserID()}, userDelta); err != nil {
		return err
	}
	maps.Copy(sessionState, sessionDelta)
	stateBytes, _ := json.Marshal(sessionState)
	eventBytes, err := json.Marshal(processed)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO agentkit_session_events(app_name,user_id,session_id,event_id,invocation_id,author,timestamp,event)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8::jsonb)
		ON CONFLICT(app_name,user_id,session_id,event_id) DO UPDATE SET event=EXCLUDED.event,timestamp=EXCLUDED.timestamp`, curSession.AppName(), curSession.UserID(), curSession.ID(), processed.ID, processed.InvocationID, processed.Author, processed.Timestamp, string(eventBytes))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE agentkit_sessions SET state=$4::jsonb, updated_at=$5 WHERE app_name=$1 AND user_id=$2 AND session_id=$3`, curSession.AppName(), curSession.UserID(), curSession.ID(), string(stateBytes), processed.Timestamp)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *postgresSessionService) ListAll(ctx context.Context, req *ListAllRequest) (*ListResponse, error) {
	if req == nil {
		req = &ListAllRequest{}
	}
	limit := req.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	clauses := []string{"1=1"}
	args := []any{}
	if req.AppName != "" {
		args = append(args, req.AppName)
		clauses = append(clauses, fmt.Sprintf("app_name=$%d", len(args)))
	}
	if req.UserID != "" {
		args = append(args, req.UserID)
		clauses = append(clauses, fmt.Sprintf("user_id=$%d", len(args)))
	}
	args = append(args, limit)
	rows, err := s.pool.Query(ctx, `SELECT app_name,user_id,session_id,state,updated_at FROM agentkit_sessions WHERE `+strings.Join(clauses, " AND ")+` ORDER BY updated_at DESC LIMIT $`+fmt.Sprint(len(args)), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		stored, err := s.scanSessionRow(ctx, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, stored)
	}
	return &ListResponse{Sessions: out}, rows.Err()
}

func (s *postgresSessionService) GetByID(ctx context.Context, req *GetByIDRequest) (*GetResponse, error) {
	if req == nil || req.SessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	var appName, userID string
	if err := s.pool.QueryRow(ctx, `SELECT app_name,user_id FROM agentkit_sessions WHERE session_id=$1 ORDER BY updated_at DESC LIMIT 1`, req.SessionID).Scan(&appName, &userID); err != nil {
		return nil, err
	}
	return s.Get(ctx, &GetRequest{AppName: appName, UserID: userID, SessionID: req.SessionID})
}

func (s *postgresSessionService) loadSession(ctx context.Context, appName, userID, sessionID string) (*session, error) {
	row := s.pool.QueryRow(ctx, `SELECT app_name,user_id,session_id,state,updated_at FROM agentkit_sessions WHERE app_name=$1 AND user_id=$2 AND session_id=$3`, appName, userID, sessionID)
	return s.scanSessionRow(ctx, row)
}

type pgScanner interface{ Scan(dest ...any) error }

func (s *postgresSessionService) scanSessionRow(ctx context.Context, row pgScanner) (*session, error) {
	var appName, userID, sessionID string
	var raw []byte
	var updatedAt time.Time
	if err := row.Scan(&appName, &userID, &sessionID, &raw, &updatedAt); err != nil {
		return nil, err
	}
	sessionState := stateMap{}
	_ = json.Unmarshal(raw, &sessionState)
	appState, _ := s.loadScopedState(ctx, `SELECT state FROM agentkit_app_state WHERE app_name=$1`, appName)
	userState, _ := s.loadScopedState(ctx, `SELECT state FROM agentkit_user_state WHERE app_name=$1 AND user_id=$2`, appName, userID)
	return &session{id: id{appName: appName, userID: userID, sessionID: sessionID}, state: sessionutils.MergeStates(appState, userState, sessionState), updatedAt: updatedAt}, nil
}

func (s *postgresSessionService) loadEvents(ctx context.Context, appName, userID, sessionID string, recent int, after time.Time) ([]*Event, error) {
	rows, err := s.pool.Query(ctx, `SELECT event FROM agentkit_session_events WHERE app_name=$1 AND user_id=$2 AND session_id=$3 ORDER BY timestamp ASC, seq ASC`, appName, userID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Event{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var ev Event
		if err := json.Unmarshal(raw, &ev); err != nil {
			return nil, err
		}
		if !after.IsZero() && ev.Timestamp.Before(after) {
			continue
		}
		out = append(out, &ev)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if recent > 0 && len(out) > recent {
		out = out[len(out)-recent:]
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.Before(out[j].Timestamp) })
	return out, nil
}

func (s *postgresSessionService) loadScopedState(ctx context.Context, query string, args ...any) (stateMap, error) {
	var raw []byte
	if err := s.pool.QueryRow(ctx, query, args...).Scan(&raw); err != nil {
		if err == pgx.ErrNoRows {
			return stateMap{}, nil
		}
		return nil, err
	}
	out := stateMap{}
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

func upsertMergedState(ctx context.Context, tx pgx.Tx, table string, keys []string, values []any, delta map[string]any) (stateMap, error) {
	if len(delta) == 0 {
		return loadStateTx(ctx, tx, table, keys, values)
	}
	cur, err := loadStateTx(ctx, tx, table, keys, values)
	if err != nil {
		return nil, err
	}
	maps.Copy(cur, delta)
	raw, _ := json.Marshal(cur)
	cols := strings.Join(keys, ",")
	placeholders := make([]string, 0, len(values)+1)
	for i := range values {
		placeholders = append(placeholders, fmt.Sprintf("$%d", i+1))
	}
	placeholders = append(placeholders, fmt.Sprintf("$%d::jsonb", len(values)+1))
	conflict := strings.Join(keys, ",")
	args := append(append([]any{}, values...), string(raw))
	_, err = tx.Exec(ctx, fmt.Sprintf(`INSERT INTO %s(%s,state,updated_at) VALUES(%s,now()) ON CONFLICT(%s) DO UPDATE SET state=EXCLUDED.state,updated_at=now()`, table, cols, strings.Join(placeholders, ","), conflict), args...)
	return cur, err
}

func loadStateTx(ctx context.Context, tx pgx.Tx, table string, keys []string, values []any) (stateMap, error) {
	clauses := make([]string, 0, len(keys))
	for i, k := range keys {
		clauses = append(clauses, fmt.Sprintf("%s=$%d", k, i+1))
	}
	var raw []byte
	if err := tx.QueryRow(ctx, fmt.Sprintf(`SELECT state FROM %s WHERE %s`, table, strings.Join(clauses, " AND ")), values...).Scan(&raw); err != nil {
		if err == pgx.ErrNoRows {
			return stateMap{}, nil
		}
		return nil, err
	}
	out := stateMap{}
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

var _ Service = (*postgresSessionService)(nil)
var _ GlobalService = (*postgresSessionService)(nil)

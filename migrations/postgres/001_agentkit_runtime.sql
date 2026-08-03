CREATE TABLE IF NOT EXISTS agentkit_app_state (
  app_name TEXT PRIMARY KEY,
  state JSONB NOT NULL DEFAULT '{}'::jsonb,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS agentkit_user_state (
  app_name TEXT NOT NULL,
  user_id TEXT NOT NULL,
  state JSONB NOT NULL DEFAULT '{}'::jsonb,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(app_name,user_id)
);

CREATE TABLE IF NOT EXISTS agentkit_sessions (
  app_name TEXT NOT NULL,
  user_id TEXT NOT NULL,
  session_id TEXT NOT NULL,
  state JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY(app_name,user_id,session_id)
);
CREATE INDEX IF NOT EXISTS idx_agentkit_sessions_app_user_updated ON agentkit_sessions(app_name,user_id,updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_agentkit_sessions_session_id ON agentkit_sessions(session_id);

CREATE TABLE IF NOT EXISTS agentkit_session_events (
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
);
CREATE INDEX IF NOT EXISTS idx_agentkit_session_events_session_ts ON agentkit_session_events(app_name,user_id,session_id,timestamp,seq);
CREATE INDEX IF NOT EXISTS idx_agentkit_session_events_event_gin ON agentkit_session_events USING GIN(event);

CREATE TABLE IF NOT EXISTS platform_runs (
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
);
CREATE INDEX IF NOT EXISTS idx_platform_runs_tenant_created ON platform_runs(tenant_id,created_at DESC);
CREATE INDEX IF NOT EXISTS idx_platform_runs_session ON platform_runs(tenant_id,session_id);
CREATE INDEX IF NOT EXISTS idx_platform_runs_status ON platform_runs(tenant_id,status);

CREATE TABLE IF NOT EXISTS platform_run_steps (
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
);
CREATE INDEX IF NOT EXISTS idx_platform_run_steps_run ON platform_run_steps(tenant_id,run_id,created_at ASC);

CREATE TABLE IF NOT EXISTS platform_run_events (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL,
  run_id TEXT NOT NULL REFERENCES platform_runs(id) ON DELETE CASCADE,
  type TEXT NOT NULL,
  payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_platform_run_events_run ON platform_run_events(tenant_id,run_id,created_at ASC);
CREATE INDEX IF NOT EXISTS idx_platform_run_events_payload_gin ON platform_run_events USING GIN(payload);

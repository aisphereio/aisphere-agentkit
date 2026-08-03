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

// Package runtimetrace records platform-level runtime trace events.
//
// Runtime trace is intentionally separate from business callbacks:
// callbacks can change execution, while trace is observability-only. The first
// implementation writes JSONL files so local development can inspect exactly
// what context the model received, what tool arguments were parsed, and how
// streaming tool calls were assembled.
package runtimetrace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/adk/session"
)

type contextKey string

const (
	recorderContextKey contextKey = "runtime_trace_recorder"
	runIDContextKey    contextKey = "runtime_trace_run_id"
)

// Config controls runtime trace recording.
type Config struct {
	Enabled         bool
	Root            string
	DumpLLMRequest  bool
	DumpLLMResponse bool
	DumpToolEvents  bool
	DumpStream      bool
	MaxContentChars int
}

// Event is one runtime trace row. It is written as one JSON line.
type Event struct {
	Time         time.Time      `json:"time"`
	Type         string         `json:"type"`
	InvocationID string         `json:"invocation_id,omitempty"`
	RunID        string         `json:"run_id,omitempty"`
	AppName      string         `json:"app_name,omitempty"`
	UserID       string         `json:"user_id,omitempty"`
	SessionID    string         `json:"session_id,omitempty"`
	AgentName    string         `json:"agent_name,omitempty"`
	Branch       string         `json:"branch,omitempty"`
	Data         map[string]any `json:"data,omitempty"`
}

// Recorder stores trace events.
type Recorder interface {
	Enabled() bool
	Record(context.Context, Event)
	Root() string
	List() ([]TraceFile, error)
	Read(invocationID string, limit int) ([]Event, error)
}

// TraceFile describes one trace JSONL file.
type TraceFile struct {
	InvocationID string    `json:"invocation_id"`
	Path         string    `json:"path"`
	SizeBytes    int64     `json:"size_bytes"`
	ModifiedAt   time.Time `json:"modified_at"`
}

// FileRecorder writes traces to .jsonl files grouped by invocation_id.
type FileRecorder struct {
	cfg Config
	mu  sync.Mutex
}

func (r *FileRecorder) DumpStreamChunks() bool {
	return r != nil && r.cfg.DumpStream
}

// NewFileRecorder returns a JSONL file recorder.
func NewFileRecorder(cfg Config) (*FileRecorder, error) {
	if cfg.Root == "" {
		return nil, fmt.Errorf("runtime trace root is required")
	}
	if cfg.MaxContentChars <= 0 {
		cfg.MaxContentChars = 8000
	}
	if cfg.Enabled {
		if err := os.MkdirAll(cfg.Root, 0o755); err != nil {
			return nil, err
		}
	}
	return &FileRecorder{cfg: cfg}, nil
}

func (r *FileRecorder) Enabled() bool { return r != nil && r.cfg.Enabled }
func (r *FileRecorder) Root() string {
	if r == nil {
		return ""
	}
	return r.cfg.Root
}

// Record appends one event. Errors are swallowed intentionally: tracing must
// never break an agent run.
func (r *FileRecorder) Record(ctx context.Context, ev Event) {
	if r == nil || !r.cfg.Enabled {
		return
	}
	ev = Enrich(ctx, ev)
	if ev.Time.IsZero() {
		ev.Time = time.Now()
	}
	if ev.InvocationID == "" {
		ev.InvocationID = "unknown"
	}
	ev.Data = sanitizeMap(ev.Data, r.cfg.MaxContentChars).(map[string]any)

	b, err := json.Marshal(ev)
	if err != nil {
		return
	}
	path := filepath.Join(r.cfg.Root, safeFileName(ev.InvocationID)+".jsonl")
	r.mu.Lock()
	defer r.mu.Unlock()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

func (r *FileRecorder) List() ([]TraceFile, error) {
	if r == nil || r.cfg.Root == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(r.cfg.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]TraceFile, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		out = append(out, TraceFile{InvocationID: id, Path: filepath.Join(r.cfg.Root, e.Name()), SizeBytes: info.Size(), ModifiedAt: info.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ModifiedAt.After(out[j].ModifiedAt) })
	return out, nil
}

func (r *FileRecorder) Read(invocationID string, limit int) ([]Event, error) {
	if r == nil || r.cfg.Root == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = 1000
	}
	path := filepath.Join(r.cfg.Root, safeFileName(invocationID)+".jsonl")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	out := make([]Event, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var ev Event
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		out = append(out, ev)
	}
	return out, nil
}

func WithRecorder(ctx context.Context, rec Recorder) context.Context {
	if rec == nil {
		return ctx
	}
	return context.WithValue(ctx, recorderContextKey, rec)
}

// WithRunID stores the platform run id in the context so every trace event can
// be correlated with PG runs/run_steps and Redis resumable streams.
func WithRunID(ctx context.Context, runID string) context.Context {
	if strings.TrimSpace(runID) == "" {
		return ctx
	}
	return context.WithValue(ctx, runIDContextKey, runID)
}

// RunID returns the platform run id carried by the context, if any.
func RunID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value, _ := ctx.Value(runIDContextKey).(string)
	return value
}

func FromContext(ctx context.Context) Recorder {
	if ctx == nil {
		return nil
	}
	rec, _ := ctx.Value(recorderContextKey).(Recorder)
	return rec
}

func Record(ctx context.Context, typ string, data map[string]any) {
	rec := FromContext(ctx)
	if rec == nil || !rec.Enabled() {
		return
	}
	rec.Record(ctx, Event{Type: typ, Data: data})
}

// Enrich fills common trace metadata from context. Recorders that do not use
// FileRecorder can call this before forwarding events to SSE, PG, or external
// observability sinks.
func Enrich(ctx context.Context, ev Event) Event {
	return enrich(ctx, ev)
}

func enrich(ctx context.Context, ev Event) Event {
	if ev.RunID == "" {
		ev.RunID = RunID(ctx)
	}

	// Tool callbacks receive tool.Context, which embeds agent.ReadonlyContext
	// instead of the full InvocationContext. Earlier enrichment only recognized
	// InvocationContext-like values with Session(), so runtime events emitted
	// from tools (for example subagent.task.*) were sent over SSE without
	// invocation_id/session_id and the Web UI could not attach them to the live
	// message row.
	type readonly interface {
		InvocationID() string
		AppName() string
		UserID() string
		SessionID() string
		AgentName() string
		Branch() string
	}
	if ro, ok := ctx.(readonly); ok {
		if ev.InvocationID == "" {
			ev.InvocationID = ro.InvocationID()
		}
		if ev.AppName == "" {
			ev.AppName = ro.AppName()
		}
		if ev.UserID == "" {
			ev.UserID = ro.UserID()
		}
		if ev.SessionID == "" {
			ev.SessionID = ro.SessionID()
		}
		if ev.AgentName == "" {
			ev.AgentName = ro.AgentName()
		}
		if ev.Branch == "" {
			ev.Branch = ro.Branch()
		}
	}

	type invocation interface {
		InvocationID() string
		Session() session.Session
		Branch() string
	}
	if inv, ok := ctx.(invocation); ok {
		if ev.InvocationID == "" {
			ev.InvocationID = inv.InvocationID()
		}
		if ev.AgentName == "" {
			ev.AgentName = agentNameFromContext(ctx)
		}
		if ev.Branch == "" {
			ev.Branch = inv.Branch()
		}
		if sess := inv.Session(); sess != nil {
			if ev.AppName == "" {
				ev.AppName = sess.AppName()
			}
			if ev.UserID == "" {
				ev.UserID = sess.UserID()
			}
			if ev.SessionID == "" {
				ev.SessionID = sess.ID()
			}
		}
	}
	return ev
}

func agentNameFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	value := reflect.ValueOf(ctx)
	method := value.MethodByName("Agent")
	if !method.IsValid() || method.Type().NumIn() != 0 || method.Type().NumOut() != 1 {
		return ""
	}
	outs := method.Call(nil)
	if len(outs) != 1 || outs[0].IsNil() {
		return ""
	}
	agentValue := outs[0]
	nameMethod := agentValue.MethodByName("Name")
	if !nameMethod.IsValid() || nameMethod.Type().NumIn() != 0 || nameMethod.Type().NumOut() != 1 || nameMethod.Type().Out(0).Kind() != reflect.String {
		return ""
	}
	nameOut := nameMethod.Call(nil)
	if len(nameOut) != 1 {
		return ""
	}
	return nameOut[0].String()
}

func sanitizeMap(m map[string]any, maxChars int) any {
	if m == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = sanitize(v, maxChars)
	}
	return out
}

func sanitize(v any, maxChars int) any {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return truncate(x, maxChars)
	case []byte:
		return fmt.Sprintf("[bytes:%d]", len(x))
	case map[string]any:
		return sanitizeMap(x, maxChars)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = sanitize(item, maxChars)
		}
		return out
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprintf("%v", x)
		}
		if len(b) > maxChars {
			return truncate(string(b), maxChars)
		}
		var decoded any
		if err := json.Unmarshal(b, &decoded); err == nil {
			return decoded
		}
		return truncate(string(b), maxChars)
	}
}

func truncate(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	return s[:maxChars] + fmt.Sprintf("...[truncated %d bytes]", len(s)-maxChars)
}

func safeFileName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return replacer.Replace(s)
}
func DumpStreamChunks(ctx context.Context) bool {
	rec := FromContext(ctx)
	if rec == nil || !rec.Enabled() {
		return false
	}

	type streamChunkDumper interface {
		DumpStreamChunks() bool
	}

	if r, ok := rec.(streamChunkDumper); ok {
		return r.DumpStreamChunks()
	}

	return false
}

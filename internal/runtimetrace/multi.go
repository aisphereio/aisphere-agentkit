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

package runtimetrace

import "context"

// MultiRecorder fans out trace events to multiple recorders. It is intended to
// let file JSONL tracing, platform run_steps, OpenTelemetry, or external
// observability sinks coexist behind one runtimetrace.Record call.
type MultiRecorder struct {
	recorders []Recorder
}

func NewMultiRecorder(recorders ...Recorder) *MultiRecorder {
	out := make([]Recorder, 0, len(recorders))
	for _, rec := range recorders {
		if rec != nil {
			out = append(out, rec)
		}
	}
	return &MultiRecorder{recorders: out}
}

func (r *MultiRecorder) Enabled() bool {
	if r == nil {
		return false
	}
	for _, rec := range r.recorders {
		if rec != nil && rec.Enabled() {
			return true
		}
	}
	return false
}

func (r *MultiRecorder) Record(ctx context.Context, ev Event) {
	if r == nil {
		return
	}
	for _, rec := range r.recorders {
		if rec != nil && rec.Enabled() {
			rec.Record(ctx, ev)
		}
	}
}

func (r *MultiRecorder) Root() string {
	if r == nil {
		return ""
	}
	for _, rec := range r.recorders {
		if rec != nil && rec.Root() != "" {
			return rec.Root()
		}
	}
	return ""
}

func (r *MultiRecorder) List() ([]TraceFile, error) {
	if r == nil {
		return nil, nil
	}
	for _, rec := range r.recorders {
		if rec != nil && rec.Root() != "" {
			return rec.List()
		}
	}
	return nil, nil
}

func (r *MultiRecorder) Read(invocationID string, limit int) ([]Event, error) {
	if r == nil {
		return nil, nil
	}
	for _, rec := range r.recorders {
		if rec != nil && rec.Root() != "" {
			return rec.Read(invocationID, limit)
		}
	}
	return nil, nil
}

func (r *MultiRecorder) DumpStreamChunks() bool {
	if r == nil {
		return false
	}
	for _, rec := range r.recorders {
		typed, ok := rec.(interface{ DumpStreamChunks() bool })
		if ok && typed.DumpStreamChunks() {
			return true
		}
	}
	return false
}

// Copyright 2025 Google LLC
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

package session

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// FileSystemService returns a session service backed by a local JSON snapshot.
//
// This implementation is intended for local development, single-process
// deployments, and demos. It preserves the existing in-memory session semantics
// and persists a snapshot after every mutation. For production multi-instance
// deployments, prefer the database implementation.
func FileSystemService(root string) (Service, error) {
	if root == "" {
		return nil, fmt.Errorf("session filesystem root is required")
	}
	s := &fileSystemService{
		root: root,
		mem: &inMemoryService{
			appState:  make(map[string]stateMap),
			userState: make(map[string]map[string]stateMap),
		},
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

type fileSystemService struct {
	mu   sync.Mutex
	root string
	mem  *inMemoryService
}

type sessionSnapshot struct {
	Version   int                            `json:"version"`
	AppState  map[string]stateMap            `json:"app_state,omitempty"`
	UserState map[string]map[string]stateMap `json:"user_state,omitempty"`
	Sessions  []storedSessionSnapshot        `json:"sessions,omitempty"`
}

type storedSessionSnapshot struct {
	AppName        string         `json:"app_name"`
	UserID         string         `json:"user_id"`
	SessionID      string         `json:"session_id"`
	State          map[string]any `json:"state,omitempty"`
	Events         []*Event       `json:"events,omitempty"`
	LastUpdateTime time.Time      `json:"last_update_time"`
}

func (s *fileSystemService) Create(ctx context.Context, req *CreateRequest) (*CreateResponse, error) {
	resp, err := s.mem.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	if err := s.persist(); err != nil {
		return nil, err
	}
	return resp, nil
}

func (s *fileSystemService) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	return s.mem.Get(ctx, req)
}

func (s *fileSystemService) List(ctx context.Context, req *ListRequest) (*ListResponse, error) {
	return s.mem.List(ctx, req)
}

func (s *fileSystemService) Delete(ctx context.Context, req *DeleteRequest) error {
	if err := s.mem.Delete(ctx, req); err != nil {
		return err
	}
	return s.persist()
}

func (s *fileSystemService) AppendEvent(ctx context.Context, curSession Session, event *Event) error {
	if err := s.mem.AppendEvent(ctx, curSession, event); err != nil {
		return err
	}
	return s.persist()
}

func (s *fileSystemService) snapshotPath() string {
	return filepath.Join(s.root, "sessions.snapshot.json")
}

func (s *fileSystemService) load() error {
	path := s.snapshotPath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var snap sessionSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return fmt.Errorf("load session snapshot %s: %w", path, err)
	}
	if snap.AppState != nil {
		s.mem.appState = snap.AppState
	}
	if snap.UserState != nil {
		s.mem.userState = snap.UserState
	}
	for _, ss := range snap.Sessions {
		st := ss.State
		if st == nil {
			st = make(map[string]any)
		}
		stored := &session{
			id:        id{appName: ss.AppName, userID: ss.UserID, sessionID: ss.SessionID},
			state:     maps.Clone(st),
			events:    append([]*Event(nil), ss.Events...),
			updatedAt: ss.LastUpdateTime,
		}
		s.mem.sessions.Set(stored.id.Encode(), stored)
	}
	return nil
}

func (s *fileSystemService) persist() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mem.mu.RLock()
	defer s.mem.mu.RUnlock()

	snap := sessionSnapshot{
		Version:   1,
		AppState:  cloneNestedState(s.mem.appState),
		UserState: cloneNestedUserState(s.mem.userState),
	}
	for _, stored := range s.mem.sessions.Scan("", "\xff\xff\xff") {
		stored.mu.RLock()
		ss := storedSessionSnapshot{
			AppName:        stored.AppName(),
			UserID:         stored.UserID(),
			SessionID:      stored.ID(),
			State:          maps.Clone(stored.state),
			Events:         append([]*Event(nil), stored.events...),
			LastUpdateTime: stored.updatedAt,
		}
		stored.mu.RUnlock()
		snap.Sessions = append(snap.Sessions, ss)
	}
	data, err := json.MarshalIndent(&snap, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return err
	}
	path := s.snapshotPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func cloneNestedState(in map[string]stateMap) map[string]stateMap {
	out := make(map[string]stateMap, len(in))
	for k, v := range in {
		out[k] = maps.Clone(v)
	}
	return out
}

func cloneNestedUserState(in map[string]map[string]stateMap) map[string]map[string]stateMap {
	out := make(map[string]map[string]stateMap, len(in))
	for app, users := range in {
		out[app] = cloneNestedState(users)
	}
	return out
}

var _ Service = (*fileSystemService)(nil)

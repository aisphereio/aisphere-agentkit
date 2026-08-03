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

package memory

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"google.golang.org/genai"

	"google.golang.org/adk/session"
)

// FileSystemService returns a local-file memory service.
//
// This is a lightweight keyword-search memory backend for local development.
// It stores one JSON file per app/user/session. It intentionally mirrors the
// InMemoryService matching behavior; replace it later with pgvector/qdrant/etc.
// without changing agent code because callers depend only on memory.Service.
func FileSystemService(root string) (Service, error) {
	if root == "" {
		return nil, fmt.Errorf("memory filesystem root is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &fileSystemService{root: root}, nil
}

type fileSystemService struct {
	mu   sync.RWMutex
	root string
}

type memoryFile struct {
	AppName   string  `json:"app_name"`
	UserID    string  `json:"user_id"`
	SessionID string  `json:"session_id"`
	Entries   []Entry `json:"entries,omitempty"`
}

func (s *fileSystemService) AddSessionToMemory(ctx context.Context, curSession session.Session) error {
	if curSession == nil {
		return fmt.Errorf("session is nil")
	}
	var entries []Entry
	for event := range curSession.Events().All() {
		if event == nil || event.LLMResponse.Content == nil {
			continue
		}
		if !contentHasText(event.LLMResponse.Content) {
			continue
		}
		entries = append(entries, Entry{
			ID:             event.ID,
			Content:        event.LLMResponse.Content,
			Author:         event.Author,
			Timestamp:      event.Timestamp,
			CustomMetadata: event.CustomMetadata,
		})
	}
	mf := memoryFile{AppName: curSession.AppName(), UserID: curSession.UserID(), SessionID: curSession.ID(), Entries: entries}
	data, err := json.MarshalIndent(&mf, "", "  ")
	if err != nil {
		return err
	}
	path := s.sessionPath(curSession.AppName(), curSession.UserID(), curSession.ID())
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (s *fileSystemService) SearchMemory(ctx context.Context, req *SearchRequest) (*SearchResponse, error) {
	if req == nil {
		return nil, fmt.Errorf("nil memory search request")
	}
	queryWords := extractWords(req.Query)
	res := &SearchResponse{}
	base := s.userDir(req.AppName, req.UserID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	entries, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return res, nil
		}
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		mf, err := readMemoryFile(filepath.Join(base, entry.Name()))
		if err != nil {
			continue
		}
		for _, e := range mf.Entries {
			if memoryEntryMatches(e, queryWords) {
				res.Memories = append(res.Memories, e)
			}
		}
	}
	return res, nil
}

func readMemoryFile(path string) (*memoryFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var mf memoryFile
	if err := json.Unmarshal(data, &mf); err != nil {
		return nil, err
	}
	return &mf, nil
}

func memoryEntryMatches(e Entry, queryWords map[string]struct{}) bool {
	if len(queryWords) == 0 || e.Content == nil {
		return false
	}
	words := make(map[string]struct{})
	for _, part := range e.Content.Parts {
		if part != nil && part.Text != "" {
			for k := range extractWords(part.Text) {
				words[k] = struct{}{}
			}
		}
	}
	return checkMapsIntersect(words, queryWords)
}

func contentHasText(c *genai.Content) bool {
	if c == nil {
		return false
	}
	for _, part := range c.Parts {
		if part != nil && strings.TrimSpace(part.Text) != "" {
			return true
		}
	}
	return false
}

func (s *fileSystemService) sessionPath(appName, userID, sessionID string) string {
	return filepath.Join(s.userDir(appName, userID), encodeSegment(sessionID)+".json")
}

func (s *fileSystemService) userDir(appName, userID string) string {
	return filepath.Join(s.root, encodeSegment(appName), encodeSegment(userID))
}

func encodeSegment(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

var _ Service = (*fileSystemService)(nil)

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

package artifact

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
)

// FileSystemService returns an artifact service backed by local files.
//
// Layout:
//
//	root/<app>/<user>/<session-or-user>/<file>/<version>.json
//
// The path segments are base64url encoded, so logical artifact names never
// become raw filesystem paths. This keeps the service simple and avoids path
// traversal problems while preserving ADK's app/user/session/file/version model.
func FileSystemService(root string) (Service, error) {
	if root == "" {
		return nil, fmt.Errorf("artifact filesystem root is required")
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

type artifactRecord struct {
	Version      int64          `json:"version"`
	CanonicalURI string         `json:"canonical_uri,omitempty"`
	CreateTime   float64        `json:"create_time"`
	Part         *genai.Part    `json:"part"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

func (s *fileSystemService) Save(ctx context.Context, req *SaveRequest) (*SaveResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	if fileHasUserNamespace(fileName) {
		appName = userScopedArtifactAppKey
		sessionID = userScopedArtifactKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	version := req.Version
	if version <= 0 {
		versions, err := s.versionsLocked(appName, userID, sessionID, fileName)
		if err != nil && !os.IsNotExist(err) {
			return nil, err
		}
		version = int64(1)
		if len(versions) > 0 {
			version = versions[len(versions)-1] + 1
		}
	}
	dir := s.fileDir(appName, userID, sessionID, fileName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	rec := artifactRecord{
		Version:      version,
		CanonicalURI: "file://" + filepath.ToSlash(filepath.Join(dir, fmt.Sprintf("%d.json", version))),
		CreateTime:   float64(time.Now().UnixNano()) / 1e9,
		Part:         req.Part,
	}
	data, err := json.MarshalIndent(&rec, "", "  ")
	if err != nil {
		return nil, err
	}
	path := s.versionPath(appName, userID, sessionID, fileName, version)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return nil, err
	}
	return &SaveResponse{Version: version}, nil
}

func (s *fileSystemService) Load(ctx context.Context, req *LoadRequest) (*LoadResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	if fileHasUserNamespace(fileName) {
		sessionID = userScopedArtifactKey
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	version := req.Version
	if fileHasUserNamespace(fileName) {
		rec, err := s.readUserScopedRecordLocked(appName, userID, fileName, version)
		if err != nil {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		return &LoadResponse{Part: rec.Part}, nil
	}
	if version <= 0 {
		versions, err := s.versionsLocked(appName, userID, sessionID, fileName)
		if err != nil {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		if len(versions) == 0 {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		version = versions[len(versions)-1]
	}
	rec, err := s.readRecord(appName, userID, sessionID, fileName, version)
	if err != nil {
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	return &LoadResponse{Part: rec.Part}, nil
}

func (s *fileSystemService) Delete(ctx context.Context, req *DeleteRequest) error {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	if fileHasUserNamespace(fileName) {
		appName = userScopedArtifactAppKey
		sessionID = userScopedArtifactKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Version > 0 {
		if err := os.Remove(s.versionPath(appName, userID, sessionID, fileName, req.Version)); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.RemoveAll(s.fileDir(appName, userID, sessionID, fileName)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *fileSystemService) List(ctx context.Context, req *ListRequest) (*ListResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	files := map[string]bool{}
	for _, sid := range []string{req.SessionID} {
		dir := s.sessionDir(req.AppName, req.UserID, sid)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name, err := decodeSegment(entry.Name())
			if err == nil {
				files[name] = true
			}
		}
	}
	for _, dir := range s.userScopedSessionDirsLocked(req.UserID) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			name, err := decodeSegment(entry.Name())
			if err == nil && fileHasUserNamespace(name) {
				files[name] = true
			}
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return &ListResponse{FileNames: names}, nil
}

func (s *fileSystemService) Versions(ctx context.Context, req *VersionsRequest) (*VersionsResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName := req.AppName, req.UserID, req.SessionID, req.FileName
	if fileHasUserNamespace(fileName) {
		sessionID = userScopedArtifactKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if fileHasUserNamespace(fileName) {
		for _, candidateApp := range s.userScopedAppCandidatesLocked(appName, userID, fileName) {
			versions, err := s.versionsLocked(candidateApp, userID, sessionID, fileName)
			if err == nil && len(versions) > 0 {
				return &VersionsResponse{Versions: versions}, nil
			}
		}
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	versions, err := s.versionsLocked(appName, userID, sessionID, fileName)
	if err != nil || len(versions) == 0 {
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	return &VersionsResponse{Versions: versions}, nil
}

func (s *fileSystemService) GetArtifactVersion(ctx context.Context, req *GetArtifactVersionRequest) (*GetArtifactVersionResponse, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("request validation failed: %w", err)
	}
	appName, userID, sessionID, fileName, version := req.AppName, req.UserID, req.SessionID, req.FileName, req.Version
	if fileHasUserNamespace(fileName) {
		sessionID = userScopedArtifactKey
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if fileHasUserNamespace(fileName) {
		rec, err := s.readUserScopedRecordLocked(appName, userID, fileName, version)
		if err != nil {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		mimeType := "text/plain"
		if rec.Part != nil && rec.Part.InlineData != nil && rec.Part.InlineData.MIMEType != "" {
			mimeType = rec.Part.InlineData.MIMEType
		}
		return &GetArtifactVersionResponse{ArtifactVersion: &ArtifactVersion{
			Version:        rec.Version,
			CanonicalURI:   rec.CanonicalURI,
			CustomMetadata: rec.Metadata,
			CreateTime:     rec.CreateTime,
			MimeType:       mimeType,
		}}, nil
	}
	if version <= 0 {
		versions, err := s.versionsLocked(appName, userID, sessionID, fileName)
		if err != nil || len(versions) == 0 {
			return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
		}
		version = versions[len(versions)-1]
	}
	rec, err := s.readRecord(appName, userID, sessionID, fileName, version)
	if err != nil {
		return nil, fmt.Errorf("artifact not found: %w", fs.ErrNotExist)
	}
	mimeType := "text/plain"
	if rec.Part != nil && rec.Part.InlineData != nil && rec.Part.InlineData.MIMEType != "" {
		mimeType = rec.Part.InlineData.MIMEType
	}
	return &GetArtifactVersionResponse{ArtifactVersion: &ArtifactVersion{
		Version:        rec.Version,
		CanonicalURI:   rec.CanonicalURI,
		CustomMetadata: rec.Metadata,
		CreateTime:     rec.CreateTime,
		MimeType:       mimeType,
	}}, nil
}

func (s *fileSystemService) readRecord(appName, userID, sessionID, fileName string, version int64) (*artifactRecord, error) {
	data, err := os.ReadFile(s.versionPath(appName, userID, sessionID, fileName, version))
	if err != nil {
		return nil, err
	}
	var rec artifactRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, err
	}
	return &rec, nil
}

func (s *fileSystemService) readUserScopedRecordLocked(appName, userID, fileName string, version int64) (*artifactRecord, error) {
	for _, candidateApp := range s.userScopedAppCandidatesLocked(appName, userID, fileName) {
		readVersion := version
		if readVersion <= 0 {
			versions, err := s.versionsLocked(candidateApp, userID, userScopedArtifactKey, fileName)
			if err != nil || len(versions) == 0 {
				continue
			}
			readVersion = versions[len(versions)-1]
		}
		rec, err := s.readRecord(candidateApp, userID, userScopedArtifactKey, fileName, readVersion)
		if err == nil {
			return rec, nil
		}
	}
	return nil, fs.ErrNotExist
}

func (s *fileSystemService) userScopedAppCandidatesLocked(appName, userID, fileName string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" || seen[v] {
			return
		}
		seen[v] = true
		out = append(out, v)
	}
	add(userScopedArtifactAppKey)
	add(appName)
	for _, dir := range s.userScopedSessionDirsLocked(userID) {
		fileDir := filepath.Join(dir, encodeSegment(fileName))
		if _, err := os.Stat(fileDir); err != nil {
			continue
		}
		appDir := filepath.Dir(filepath.Dir(dir))
		appName, err := decodeSegment(filepath.Base(appDir))
		if err == nil {
			add(appName)
		}
	}
	return out
}

func (s *fileSystemService) userScopedSessionDirsLocked(userID string) []string {
	rootEntries, err := os.ReadDir(s.root)
	if err != nil {
		return nil
	}
	out := []string{}
	encodedUserID := encodeSegment(userID)
	encodedUserScope := encodeSegment(userScopedArtifactKey)
	for _, appEntry := range rootEntries {
		if !appEntry.IsDir() {
			continue
		}
		dir := filepath.Join(s.root, appEntry.Name(), encodedUserID, encodedUserScope)
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out
}

func (s *fileSystemService) versionsLocked(appName, userID, sessionID, fileName string) ([]int64, error) {
	entries, err := os.ReadDir(s.fileDir(appName, userID, sessionID, fileName))
	if err != nil {
		return nil, err
	}
	var versions []int64
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSuffix(entry.Name(), ".json"), 10, 64)
		if err == nil && v > 0 {
			versions = append(versions, v)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	return versions, nil
}

func (s *fileSystemService) versionPath(appName, userID, sessionID, fileName string, version int64) string {
	return filepath.Join(s.fileDir(appName, userID, sessionID, fileName), fmt.Sprintf("%d.json", version))
}

func (s *fileSystemService) fileDir(appName, userID, sessionID, fileName string) string {
	return filepath.Join(s.sessionDir(appName, userID, sessionID), encodeSegment(fileName))
}

func (s *fileSystemService) sessionDir(appName, userID, sessionID string) string {
	return filepath.Join(s.root, encodeSegment(appName), encodeSegment(userID), encodeSegment(sessionID))
}

func encodeSegment(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func decodeSegment(s string) (string, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

var _ Service = (*fileSystemService)(nil)
